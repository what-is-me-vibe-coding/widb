package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const (
	defaultL0CompactionThreshold = 4
	defaultLevelSizeMultiplier   = 2
)

// Compactor 负责将多个 Segment 合并为更少的 Segment。
type Compactor struct {
	mu      sync.Mutex
	dataDir string
	nextID  uint64
}

// SetNextID updates the compactor's nextID if the given id is larger.
func (c *Compactor) SetNextID(id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id > c.nextID {
		c.nextID = id
	}
}

// NewCompactor 创建一个 Compactor 实例。
func NewCompactor(dataDir string) *Compactor {
	return &Compactor{dataDir: dataDir}
}

// Compact 将输入的 segments 合并为一个新的 Segment。
func (c *Compactor) Compact(segments []*Segment, cols []ColumnMeta) (*Segment, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(segments) == 0 {
		return nil, fmt.Errorf("compactor: no segments to compact")
	}

	rows, err := c.mergeSegments(segments, cols)
	if err != nil {
		return nil, fmt.Errorf("compactor: merge segments: %w", err)
	}

	if len(rows) == 0 {
		return nil, fmt.Errorf("compactor: merged result is empty")
	}

	seg, err := c.buildSegment(rows, cols)
	if err != nil {
		return nil, fmt.Errorf("compactor: build segment: %w", err)
	}

	return seg, nil
}

// CompactToLevel 将 L0 的 segments 合并到 L1，或将 Ln 合并到 Ln+1。
func (c *Compactor) CompactToLevel(segments []*Segment, _ int, cols []ColumnMeta) (*Segment, error) {
	seg, err := c.Compact(segments, cols)
	if err != nil {
		return nil, err
	}
	return seg, nil
}

func (c *Compactor) mergeSegments(segments []*Segment, cols []ColumnMeta) ([]memRow, error) {
	var allRows []memRow
	for _, seg := range segments {
		rows, err := c.readSegmentRows(seg, cols)
		if err != nil {
			return nil, fmt.Errorf("compactor: read segment %d: %w", seg.ID, err)
		}
		allRows = append(allRows, rows...)
	}

	sort.Slice(allRows, func(i, j int) bool {
		return allRows[i].Key < allRows[j].Key
	})

	// 去重：同一 key 保留最新版本（L0 Segment ID 更大，排在后面，取最后一个）
	deduped := make([]memRow, 0, len(allRows))
	for i := range allRows {
		if i > 0 && allRows[i].Key == allRows[i-1].Key {
			deduped[len(deduped)-1] = allRows[i]
		} else {
			deduped = append(deduped, allRows[i])
		}
	}

	return deduped, nil
}

func (c *Compactor) readSegmentRows(seg *Segment, _ []ColumnMeta) ([]memRow, error) {
	if seg.RowCount == 0 {
		return nil, nil
	}

	numCols := len(seg.Columns)

	decodedCols := make([]columnData, numCols)
	for i := range seg.Columns {
		cd, err := decodeSegmentColumn(&seg.Columns[i], i)
		if err != nil {
			return nil, err
		}
		decodedCols[i] = cd
	}

	rows := make([]memRow, 0, seg.RowCount)
	for r := uint32(0); r < seg.RowCount; r++ {
		values := make([]common.Value, numCols)
		for i := range decodedCols {
			values[i] = extractValue(decodedCols[i], r)
		}
		var key string
		if int(r) < len(seg.Keys) {
			key = seg.Keys[r]
		} else {
			key = fmt.Sprintf("row_%d", seg.ID*1000000+uint64(len(rows)))
		}
		rows = append(rows, memRow{
			Key:    key,
			Values: values,
		})
	}

	return rows, nil
}

// decodeSegmentColumn copies and decodes a single segment column for compaction.
// A copy is made to avoid modifying the shared segment data during concurrent reads.
func decodeSegmentColumn(src *EncodedColumn, colIdx int) (columnData, error) {
	enc := &EncodedColumn{
		Encoding: src.Encoding,
		Type:     src.Type,
		RowCount: src.RowCount,
	}
	if len(src.Data) > 0 {
		enc.Data = make([]byte, len(src.Data))
		copy(enc.Data, src.Data)
	}
	if len(src.Offsets) > 0 {
		enc.Offsets = make([]uint32, len(src.Offsets))
		copy(enc.Offsets, src.Offsets)
	}
	if len(src.Dict) > 0 {
		enc.Dict = make([]string, len(src.Dict))
		copy(enc.Dict, src.Dict)
	}
	if len(src.Nulls) > 0 {
		enc.Nulls = make([]byte, len(src.Nulls))
		copy(enc.Nulls, src.Nulls)
	}
	if err := DecompressColumn(enc); err != nil {
		return columnData{}, fmt.Errorf("compactor: decompress column %d: %w", colIdx, err)
	}
	data, nulls, err := DecodeColumn(enc)
	if err != nil {
		return columnData{}, fmt.Errorf("compactor: decode column %d: %w", colIdx, err)
	}
	return columnData{data: data, nulls: nulls, typ: enc.Type}, nil
}

type columnData struct {
	data  interface{}
	nulls *common.Bitmap
	typ   common.DataType
}

func extractValue(cd columnData, row uint32) common.Value {
	if cd.nulls != nil && cd.nulls.Get(row) {
		return common.NewNull()
	}

	switch cd.typ {
	case common.TypeInt64:
		return extractInt64Value(cd.data, row)
	case common.TypeFloat64:
		return extractFloat64Value(cd.data, row)
	case common.TypeBool:
		return extractBoolValue(cd.data, row)
	case common.TypeString:
		return extractStringValue(cd.data, row)
	case common.TypeTimestamp:
		return extractTimestampValue(cd.data, row)
	default:
		return common.NewNull()
	}
}

func extractInt64Value(data interface{}, row uint32) common.Value {
	if ints, ok := data.([]int64); ok && row < uint32(len(ints)) {
		return common.NewInt64(ints[row])
	}
	return common.NewNull()
}

func extractFloat64Value(data interface{}, row uint32) common.Value {
	if floats, ok := data.([]float64); ok && row < uint32(len(floats)) {
		return common.NewFloat64(floats[row])
	}
	return common.NewNull()
}

func extractBoolValue(data interface{}, row uint32) common.Value {
	if bools, ok := data.([]uint64); ok && row < uint32(len(bools)) {
		return common.NewBool(bools[row] != 0)
	}
	return common.NewNull()
}

func extractStringValue(data interface{}, row uint32) common.Value {
	if strs, ok := data.([]string); ok && row < uint32(len(strs)) {
		return common.NewString(strs[row])
	}
	return common.NewNull()
}

func extractTimestampValue(data interface{}, row uint32) common.Value {
	if times, ok := data.([]int64); ok && row < uint32(len(times)) {
		return common.NewTimestamp(time.Unix(0, times[row]))
	}
	return common.NewNull()
}

func (c *Compactor) buildSegment(rows []memRow, cols []ColumnMeta) (*Segment, error) {
	rowCount := uint32(len(rows))
	minKey := rows[0].Key
	maxKey := rows[len(rows)-1].Key

	c.nextID++
	builder := NewSegmentBuilder(c.nextID, minKey, maxKey)

	keys := make([]string, len(rows))
	for i, row := range rows {
		keys[i] = row.Key
	}
	builder.SetKeys(keys)

	for colIdx, colMeta := range cols {
		cv := NewColumnVector(colMeta.ID, colMeta.Type, rowCount)
		for _, row := range rows {
			if colIdx >= len(row.Values) {
				if err := cv.Append(common.NewNull()); err != nil {
					return nil, fmt.Errorf("compactor: column %s append null: %w", colMeta.Name, err)
				}
				continue
			}
			val := row.Values[colIdx]
			if err := cv.Append(val); err != nil {
				return nil, fmt.Errorf("compactor: column %s: %w", colMeta.Name, err)
			}
		}

		enc, err := encodeColumnVector(cv)
		if err != nil {
			return nil, fmt.Errorf("compactor: encode column %s: %w", colMeta.Name, err)
		}
		builder.AddEncodedColumn(enc)
	}

	seg, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("compactor: build segment: %w", err)
	}

	fileName := filepath.Join(c.dataDir, fmt.Sprintf("segment_%d.widb", c.nextID))
	data, err := seg.Serialize()
	if err != nil {
		return nil, fmt.Errorf("compactor: serialize segment: %w", err)
	}

	if err := os.MkdirAll(c.dataDir, 0755); err != nil {
		return nil, fmt.Errorf("compactor: create data dir: %w", err)
	}

	if err := os.WriteFile(fileName, data, 0644); err != nil {
		return nil, fmt.Errorf("compactor: write segment file: %w", err)
	}

	seg.FilePath = fileName
	return seg, nil
}

// CleanupSegments 删除旧 Segment 文件。
func (c *Compactor) CleanupSegments(segments []*Segment) error {
	for _, seg := range segments {
		if seg.FilePath != "" {
			if err := os.Remove(seg.FilePath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("compactor: remove segment %s: %w", seg.FilePath, err)
			}
		}
	}
	return nil
}

type memRow struct {
	Key    string
	Values []common.Value
}
