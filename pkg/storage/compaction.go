package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const (
	defaultL0CompactionThreshold = 4
	defaultLevelSizeMultiplier   = 2
)

// Compactor 负责将多个 Segment 合并为更少的 Segment。
type Compactor struct {
	dataDir string
	nextID  uint64
}

// NewCompactor 创建一个 Compactor 实例。
func NewCompactor(dataDir string) *Compactor {
	return &Compactor{dataDir: dataDir}
}

// Compact 将输入的 segments 合并为一个新的 Segment。
func (c *Compactor) Compact(segments []*Segment, cols []ColumnMeta) (*Segment, error) {
	if len(segments) == 0 {
		return nil, fmt.Errorf("compactor: no segments to compact")
	}

	rows, err := c.mergeSegments(segments)
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
func (c *Compactor) CompactToLevel(segments []*Segment, level int, cols []ColumnMeta) (*Segment, error) {
	seg, err := c.Compact(segments, cols)
	if err != nil {
		return nil, err
	}
	return seg, nil
}

// mergeSegments 从多个 Segment 读取数据并按键合并排序。
func (c *Compactor) mergeSegments(segments []*Segment) ([]memRow, error) {
	var allRows []memRow
	for _, seg := range segments {
		rows, err := c.readSegmentRows(seg)
		if err != nil {
			return nil, fmt.Errorf("compactor: read segment %d: %w", seg.ID, err)
		}
		allRows = append(allRows, rows...)
	}

	sort.Slice(allRows, func(i, j int) bool {
		return allRows[i].Key < allRows[j].Key
	})

	return allRows, nil
}

// readSegmentRows 从 Segment 中读取所有行数据。
func (c *Compactor) readSegmentRows(seg *Segment) ([]memRow, error) {
	if seg.RowCount == 0 {
		return nil, nil
	}

	numCols := len(seg.Columns)

	decodedCols := make([]columnData, numCols)
	for i := range seg.Columns {
		enc := &seg.Columns[i]
		if err := DecompressColumn(enc); err != nil {
			return nil, fmt.Errorf("compactor: decompress column %d: %w", i, err)
		}
		data, nulls, err := DecodeColumn(enc)
		if err != nil {
			return nil, fmt.Errorf("compactor: decode column %d: %w", i, err)
		}
		cd := columnData{
			data:  data,
			nulls: nulls,
			typ:   enc.Type,
		}
		decodedCols[i] = cd
	}

	rows := make([]memRow, 0, seg.RowCount)
	for r := uint32(0); r < seg.RowCount; r++ {
		values := make(map[string]common.Value)
		for i := range decodedCols {
			colName := fmt.Sprintf("col_%d", i)
			val := extractValue(decodedCols[i], r)
			values[colName] = val
		}
		rows = append(rows, memRow{
			Key:   fmt.Sprintf("row_%d", seg.ID*1000000+uint64(len(rows))),
			Value: Row{Columns: values},
		})
	}

	return rows, nil
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
		if ints, ok := cd.data.([]int64); ok && row < uint32(len(ints)) {
			return common.NewInt64(ints[row])
		}
	case common.TypeFloat64:
		if floats, ok := cd.data.([]float64); ok && row < uint32(len(floats)) {
			return common.NewFloat64(floats[row])
		}
	case common.TypeBool:
		if bools, ok := cd.data.([]uint64); ok && row < uint32(len(bools)) {
			return common.NewBool(bools[row] != 0)
		}
	case common.TypeString:
		if strs, ok := cd.data.([]string); ok && row < uint32(len(strs)) {
			return common.NewString(strs[row])
		}
	case common.TypeTimestamp:
		if times, ok := cd.data.([]int64); ok && row < uint32(len(times)) {
			return common.NewTimestamp(time.Unix(0, times[row]))
		}
	}

	return common.NewNull()
}

// buildSegment 从合并后的行数据构建新的 Segment。
func (c *Compactor) buildSegment(rows []memRow, cols []ColumnMeta) (*Segment, error) {
	rowCount := uint32(len(rows))
	minKey := rows[0].Key
	maxKey := rows[len(rows)-1].Key

	c.nextID++
	builder := NewSegmentBuilder(c.nextID, minKey, maxKey)

	for _, colMeta := range cols {
		cv := NewColumnVector(colMeta.ID, colMeta.Type, rowCount)
		for _, row := range rows {
			val, ok := row.Value.Columns[colMeta.Name]
			if !ok {
				if err := cv.Append(common.NewNull()); err != nil {
					return nil, fmt.Errorf("compactor: column %s append null: %w", colMeta.Name, err)
				}
				continue
			}
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
	Key   string
	Value Row
}
