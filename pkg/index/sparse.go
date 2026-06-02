package index

import (
	"encoding/binary"
	"math"
	"sync"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

type PredicateOp int

const (
	OpEqual PredicateOp = iota
	OpNotEqual
	OpLess
	OpLessEqual
	OpGreater
	OpGreaterEqual
)

type ColumnSparseStat struct {
	MinValue  common.Value
	MaxValue  common.Value
	NullCount uint32
	HasValues bool
}

type SegmentStats interface {
	SegmentID() uint64
	ForEachColumnStat(func(colID uint32, colType common.DataType, min, max []byte, nullCount uint32))
}

type ColumnVectorReader interface {
	Len() uint32
	NullBitmap() *common.Bitmap
	GetValue(i uint32) common.Value
}

type SparseIndex struct {
	mu    sync.RWMutex
	stats map[colStatKey]ColumnSparseStat
}

type colStatKey struct {
	SegID uint64
	ColID uint32
}

func NewSparseIndex() *SparseIndex {
	return &SparseIndex{
		stats: make(map[colStatKey]ColumnSparseStat),
	}
}

func (si *SparseIndex) RegisterColumnStat(segID uint64, colID uint32, min, max []byte, nullCount uint32, dataType common.DataType) {
	si.mu.Lock()
	defer si.mu.Unlock()

	key := colStatKey{SegID: segID, ColID: colID}
	css := ColumnSparseStat{
		NullCount: nullCount,
	}

	if len(min) > 0 && len(max) > 0 {
		css.MinValue = bytesToValue(min, dataType)
		css.MaxValue = bytesToValue(max, dataType)
		css.HasValues = true
	}

	si.stats[key] = css
}

func (si *SparseIndex) GetColumnStat(segID uint64, colID uint32) (ColumnSparseStat, bool) {
	si.mu.RLock()
	defer si.mu.RUnlock()

	css, ok := si.stats[colStatKey{SegID: segID, ColID: colID}]
	return css, ok
}

func (si *SparseIndex) UnregisterSegment(segID uint64) {
	si.mu.Lock()
	defer si.mu.Unlock()

	for key := range si.stats {
		if key.SegID == segID {
			delete(si.stats, key)
		}
	}
}

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

func (si *SparseIndex) LoadFromSegment(seg SegmentStats, _, _ string, _ int) {
	if seg == nil {
		return
	}

	segID := seg.SegmentID()
	seg.ForEachColumnStat(func(colID uint32, colType common.DataType, min, max []byte, nullCount uint32) {
		si.RegisterColumnStat(segID, colID, min, max, nullCount, colType)
	})
}

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

func (si *SparseIndex) StatCount() int {
	si.mu.RLock()
	defer si.mu.RUnlock()
	return len(si.stats)
}

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
			return common.NewInt64(v)
		}
	case common.TypeString:
		return common.NewString(string(b))
	}
	return common.NewNull()
}
