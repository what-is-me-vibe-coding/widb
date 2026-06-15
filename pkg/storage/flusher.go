package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// KeyValue 表示 MemTable 中的键值对。
type KeyValue struct {
	Key   string
	Value Row
}

// ColumnMeta 描述需要刷盘的列的元数据。
type ColumnMeta struct {
	ID   uint32
	Name string
	Type common.DataType
}

// Flusher 负责将 MemTable 刷盘为 Segment 文件。
type Flusher struct {
	mu      sync.Mutex
	dataDir string
	idGen   *segmentIDGen
}

// NewFlusher 创建一个 Flusher 实例。
func NewFlusher(dataDir string, idGen *segmentIDGen) *Flusher {
	return &Flusher{dataDir: dataDir, idGen: idGen}
}

// Flush 将 MemTable 转换为 Segment 文件，返回生成的 Segment。
func (f *Flusher) Flush(mem *MemTable, cols []ColumnMeta) (*Segment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	rows := mem.All()
	if len(rows) == 0 {
		return nil, fmt.Errorf("flusher: empty memtable")
	}

	segID := f.idGen.Next()
	seg, err := f.buildSegment(segID, rows, cols)
	if err != nil {
		return nil, err
	}

	fileName, err := writeSegmentFile(f.dataDir, seg)
	if err != nil {
		return nil, err
	}

	seg.FilePath = fileName
	return seg, nil
}

func (f *Flusher) buildSegment(segID uint64, rows []KeyValue, cols []ColumnMeta) (*Segment, error) {
	rowCount := uint32(len(rows))
	minKey := rows[0].Key
	maxKey := rows[len(rows)-1].Key

	builder := NewSegmentBuilder(segID, minKey, maxKey)

	keys := make([]string, len(rows))
	for i, row := range rows {
		keys[i] = row.Key
	}
	builder.SetKeys(keys)

	for _, colMeta := range cols {
		enc, err := f.buildEncodedColumn(colMeta, rows, rowCount)
		if err != nil {
			return nil, err
		}
		builder.AddEncodedColumn(enc)
	}

	return builder.Build()
}

// valueProvider 是列值的提供函数，用于统一 Flusher 和 Compactor 的列编码逻辑。
// 返回 (value, ok)，ok=false 表示该行该列缺失，应填充 NULL。
type valueProvider func(rowIdx int) (common.Value, bool)

// encodeColumnFromProvider 通过值提供函数构建并编码列数据。
// 此函数被 Flusher 和 Compactor 共享，避免重复的列编码逻辑。
func encodeColumnFromProvider(colMeta ColumnMeta, rowCount uint32, provider valueProvider) (*EncodedColumn, error) {
	cv := NewColumnVector(colMeta.ID, colMeta.Type, rowCount)
	for i := 0; i < int(rowCount); i++ {
		val, ok := provider(i)
		if !ok {
			if err := cv.Append(common.NewNull()); err != nil {
				return nil, fmt.Errorf("column %s append null: %w", colMeta.Name, err)
			}
			continue
		}
		if err := cv.Append(val); err != nil {
			return nil, fmt.Errorf("column %s: %w", colMeta.Name, err)
		}
	}

	enc, err := encodeColumnVector(cv)
	if err != nil {
		return nil, fmt.Errorf("encode column %s: %w", colMeta.Name, err)
	}
	return enc, nil
}

func (f *Flusher) buildEncodedColumn(colMeta ColumnMeta, rows []KeyValue, rowCount uint32) (*EncodedColumn, error) {
	return encodeColumnFromProvider(colMeta, rowCount, func(rowIdx int) (common.Value, bool) {
		val, ok := rows[rowIdx].Value.Columns[colMeta.Name]
		return val, ok
	})
}

// writeSegmentFile 将 Segment 序列化并写入磁盘文件。
// 此函数被 Flusher 和 Compactor 共享，避免重复的文件写入逻辑。
func writeSegmentFile(dataDir string, seg *Segment) (string, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return "", fmt.Errorf("write segment: create data dir: %w", err)
	}
	fileName := filepath.Join(dataDir, fmt.Sprintf("segment_%d.widb", seg.ID))
	data, err := seg.Serialize()
	if err != nil {
		return "", fmt.Errorf("write segment: serialize: %w", err)
	}
	if err := os.WriteFile(fileName, data, 0644); err != nil {
		return "", fmt.Errorf("write segment: write file: %w", err)
	}
	return fileName, nil
}

// encodeColumnVector 将 ColumnVector 编码为 EncodedColumn。
func encodeColumnVector(cv *ColumnVector) (*EncodedColumn, error) {
	rowCount := cv.Len()
	nulls := cv.NullBitmap()

	var data any
	switch cv.Typ {
	case common.TypeInt64:
		data = cv.Int64Data()
	case common.TypeFloat64:
		data = cv.Float64Data()
	case common.TypeBool:
		data = cv.BoolData()
	case common.TypeString:
		data = cv.StringData()
	case common.TypeTimestamp:
		times := cv.TimeData()
		int64s := make([]int64, len(times))
		for i, t := range times {
			int64s[i] = t.UnixNano()
		}
		data = int64s
	default:
		return nil, fmt.Errorf("encode column vector: unsupported type %v", cv.Typ)
	}

	return EncodeColumn(cv.Typ, data, rowCount, nulls)
}
