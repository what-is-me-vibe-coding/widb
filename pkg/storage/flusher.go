package storage

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ColumnMeta 描述需要刷盘的列的元数据。
type ColumnMeta struct {
	ID   uint32
	Name string
	Type common.DataType
}

// Flusher 负责将 MemTable 刷盘为 Segment 文件。
type Flusher struct {
	dataDir string
	nextID  uint64
}

// NewFlusher 创建一个 Flusher 实例。
func NewFlusher(dataDir string) *Flusher {
	return &Flusher{dataDir: dataDir}
}

// Flush 将 MemTable 转换为 Segment 文件，返回生成的 Segment。
func (f *Flusher) Flush(mem *MemTable, cols []ColumnMeta) (*Segment, error) {
	rows := mem.All()
	if len(rows) == 0 {
		return nil, fmt.Errorf("flusher: empty memtable")
	}

	rowCount := uint32(len(rows))
	minKey := rows[0].Key
	maxKey := rows[len(rows)-1].Key

	f.nextID++
	builder := NewSegmentBuilder(f.nextID, minKey, maxKey)

	for _, colMeta := range cols {
		cv := NewColumnVector(colMeta.ID, colMeta.Type, rowCount)
		for _, row := range rows {
			val, ok := row.Value.Columns[colMeta.Name]
			if !ok {
				if err := cv.Append(common.NewNull()); err != nil {
					return nil, fmt.Errorf("flusher: column %s append null: %w", colMeta.Name, err)
				}
				continue
			}
			if err := cv.Append(val); err != nil {
				return nil, fmt.Errorf("flusher: column %s: %w", colMeta.Name, err)
			}
		}

		enc, err := encodeColumnVector(cv)
		if err != nil {
			return nil, fmt.Errorf("flusher: encode column %s: %w", colMeta.Name, err)
		}
		builder.AddEncodedColumn(enc)
	}

	seg, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("flusher: build segment: %w", err)
	}

	fileName := filepath.Join(f.dataDir, fmt.Sprintf("segment_%d.widb", f.nextID))
	data, err := seg.Serialize()
	if err != nil {
		return nil, fmt.Errorf("flusher: serialize segment: %w", err)
	}

	if err := os.MkdirAll(f.dataDir, 0755); err != nil {
		return nil, fmt.Errorf("flusher: create data dir: %w", err)
	}

	if err := os.WriteFile(fileName, data, 0644); err != nil {
		return nil, fmt.Errorf("flusher: write segment file: %w", err)
	}

	seg.FilePath = fileName
	return seg, nil
}

// encodeColumnVector 将 ColumnVector 编码为 EncodedColumn。
func encodeColumnVector(cv *ColumnVector) (*EncodedColumn, error) {
	rowCount := cv.Len()
	nulls := cv.NullBitmap()

	var data interface{}
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
