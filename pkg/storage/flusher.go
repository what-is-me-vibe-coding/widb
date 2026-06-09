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
	nextID  uint64
}

// NextID returns the next segment ID under the flusher's mutex.
func (f *Flusher) NextID() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nextID
}

// SetNextID updates the flusher's nextID if the given id is larger.
func (f *Flusher) SetNextID(id uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id > f.nextID {
		f.nextID = id
	}
}

// NewFlusher 创建一个 Flusher 实例。
func NewFlusher(dataDir string) *Flusher {
	return &Flusher{dataDir: dataDir}
}

// Flush 将 MemTable 转换为 Segment 文件，返回生成的 Segment。
func (f *Flusher) Flush(mem *MemTable, cols []ColumnMeta) (*Segment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	rows := mem.All()
	if len(rows) == 0 {
		return nil, fmt.Errorf("flusher: empty memtable")
	}

	f.nextID++
	seg, err := f.buildSegment(f.nextID, rows, cols)
	if err != nil {
		return nil, err
	}

	fileName, err := f.writeSegment(seg)
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

func (f *Flusher) buildEncodedColumn(colMeta ColumnMeta, rows []KeyValue, rowCount uint32) (*EncodedColumn, error) {
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
	return enc, nil
}

func (f *Flusher) writeSegment(seg *Segment) (string, error) {
	if err := os.MkdirAll(f.dataDir, 0755); err != nil {
		return "", fmt.Errorf("flusher: create data dir: %w", err)
	}

	fileName := filepath.Join(f.dataDir, fmt.Sprintf("segment_%d.widb", seg.ID))
	data, err := seg.Serialize()
	if err != nil {
		return "", fmt.Errorf("flusher: serialize segment: %w", err)
	}

	if err := os.WriteFile(fileName, data, 0644); err != nil {
		return "", fmt.Errorf("flusher: write segment file: %w", err)
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
