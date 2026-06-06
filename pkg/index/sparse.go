package index

import (
	"encoding/binary"
	"math"
	"sync"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// PredicateOp 表示谓词比较操作类型。
type PredicateOp int

const (
	// OpEqual 等于。
	OpEqual PredicateOp = iota
	// OpNotEqual 不等于。
	OpNotEqual
	// OpLess 小于。
	OpLess
	// OpLessEqual 小于等于。
	OpLessEqual
	// OpGreater 大于。
	OpGreater
	// OpGreaterEqual 大于等于。
	OpGreaterEqual
)

// ColumnSparseStat 表示单列的稀疏统计信息。
type ColumnSparseStat struct {
	MinValue  common.Value
	MaxValue  common.Value
	NullCount uint32
	HasValues bool
}

// SegmentStats 是 Segment 统计信息的接口。
type SegmentStats interface {
	SegmentID() uint64
	ForEachColumnStat(func(colID uint32, colType common.DataType, minVal, maxVal []byte, nullCount uint32))
}

// ColumnVectorReader 是列向量读取接口。
type ColumnVectorReader interface {
	Len() uint32
	NullBitmap() *common.Bitmap
	GetValue(i uint32) common.Value
}

// SparseIndex 是基于 Min/Max 的稀疏索引。
type SparseIndex struct {
	mu    sync.RWMutex
	stats map[colStatKey]ColumnSparseStat
}

type colStatKey struct {
	SegID uint64
	ColID uint32
}

// NewSparseIndex 创建一个空的稀疏索引。
func NewSparseIndex() *SparseIndex {
	return &SparseIndex{
		stats: make(map[colStatKey]ColumnSparseStat),
	}
}

// RegisterColumnStat 注册一列的稀疏统计信息。
func (si *SparseIndex) RegisterColumnStat(segID uint64, colID uint32, minVal, maxVal []byte, nullCount uint32, dataType common.DataType) {
	si.mu.Lock()
	defer si.mu.Unlock()

	key := colStatKey{SegID: segID, ColID: colID}
	css := ColumnSparseStat{
		NullCount: nullCount,
	}

	if len(minVal) > 0 && len(maxVal) > 0 {
		css.MinValue = bytesToValue(minVal, dataType)
		css.MaxValue = bytesToValue(maxVal, dataType)
		css.HasValues = true
	}

	si.stats[key] = css
}

// GetColumnStat 获取指定 Segment 中某列的稀疏统计。
func (si *SparseIndex) GetColumnStat(segID uint64, colID uint32) (ColumnSparseStat, bool) {
	si.mu.RLock()
	defer si.mu.RUnlock()

	css, ok := si.stats[colStatKey{SegID: segID, ColID: colID}]
	return css, ok
}

// UnregisterSegment 注销指定 Segment 的所有列统计。
func (si *SparseIndex) UnregisterSegment(segID uint64) {
	si.mu.Lock()
	defer si.mu.Unlock()

	for key := range si.stats {
		if key.SegID == segID {
			delete(si.stats, key)
		}
	}
}

// CanSkip 判断是否可以根据稀疏统计跳过指定列的扫描。
func (si *SparseIndex) CanSkip(segID uint64, colID uint32, op PredicateOp, value common.Value) bool {
	css, ok := si.GetColumnStat(segID, colID)
	if !ok || !css.HasValues {
		return false
	}

	minVal := css.MinValue
	maxVal := css.MaxValue

	switch op {
	case OpEqual:
		return value.Less(minVal) || maxVal.Less(value)
	case OpNotEqual:
		return false
	case OpLess:
		return !minVal.Less(value)
	case OpLessEqual:
		return value.Less(minVal)
	case OpGreater:
		return !value.Less(maxVal)
	case OpGreaterEqual:
		return maxVal.Less(value)
	default:
		return false
	}
}

// LoadFromSegment 从 SegmentStats 加载所有列统计信息。
func (si *SparseIndex) LoadFromSegment(seg SegmentStats, _, _ string, _ int) {
	if seg == nil {
		return
	}

	segID := seg.SegmentID()
	seg.ForEachColumnStat(func(colID uint32, colType common.DataType, minVal, maxVal []byte, nullCount uint32) {
		si.RegisterColumnStat(segID, colID, minVal, maxVal, nullCount, colType)
	})
}

// BuildFromColumnVector 从列向量构建稀疏统计。
func (si *SparseIndex) BuildFromColumnVector(segID uint64, colID uint32, cv ColumnVectorReader) {
	if cv == nil || cv.Len() == 0 {
		return
	}

	stat := ColumnSparseStat{}
	nullBitmap := cv.NullBitmap()

	first := true
	for i := uint32(0); i < cv.Len(); i++ {
		if nullBitmap != nil && nullBitmap.Get(i) {
			stat.NullCount++
			continue
		}

		val := cv.GetValue(i)
		if val.IsNull() {
			continue
		}

		if first {
			stat.MinValue = val
			stat.MaxValue = val
			stat.HasValues = true
			first = false
		} else {
			if val.Less(stat.MinValue) {
				stat.MinValue = val
			}
			if stat.MaxValue.Less(val) {
				stat.MaxValue = val
			}
		}
	}

	si.mu.Lock()
	si.stats[colStatKey{SegID: segID, ColID: colID}] = stat
	si.mu.Unlock()
}

// StatCount 返回已注册的列统计数量。
func (si *SparseIndex) StatCount() int {
	si.mu.RLock()
	defer si.mu.RUnlock()
	return len(si.stats)
}

// Clear 清空所有统计信息。
func (si *SparseIndex) Clear() {
	si.mu.Lock()
	defer si.mu.Unlock()
	si.stats = make(map[colStatKey]ColumnSparseStat)
}

func bytesToValue(b []byte, dataType common.DataType) common.Value {
	switch dataType {
	case common.TypeInt64:
		if len(b) >= 8 {
			v := int64(binary.LittleEndian.Uint64(b))
			return common.NewInt64(v)
		}
	case common.TypeFloat64:
		if len(b) >= 8 {
			v := math.Float64frombits(binary.LittleEndian.Uint64(b))
			return common.NewFloat64(v)
		}
	case common.TypeBool:
		if len(b) > 0 {
			return common.NewBool(b[0] != 0)
		}
	case common.TypeTimestamp:
		if len(b) >= 8 {
			v := int64(binary.LittleEndian.Uint64(b))
			return common.NewTimestamp(time.Unix(0, v))
		}
	case common.TypeString:
		return common.NewString(string(b))
	}
	return common.NewNull()
}
