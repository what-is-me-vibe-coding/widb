package storage

import (
	"fmt"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const defaultColumnCapacity = 1024

// ColumnVector 是单个列在内存中的向量化表示。
// 承载一批行中某一列的所有数据，使用强类型数组进行紧凑存储。
type ColumnVector struct {
	ColumnID uint32
	Typ      common.DataType
	len      uint32
	capacity uint32
	nulls    *common.Bitmap
	int64s   []int64
	float64s []float64
	bools    []uint64
	strings  []string
	times    []time.Time
	strLen   uint32
}

// NewColumnVector 创建一个指定列ID、数据类型和容量的列向量。
func NewColumnVector(colID uint32, typ common.DataType, capacity uint32) *ColumnVector {
	if capacity == 0 {
		capacity = defaultColumnCapacity
	}
	cv := &ColumnVector{
		ColumnID: colID,
		Typ:      typ,
		capacity: capacity,
		nulls:    common.NewBitmap(capacity),
	}
	cv.allocateData()
	return cv
}

// allocateData 根据数据类型分配对应的存储数组。
func (cv *ColumnVector) allocateData() {
	switch cv.Typ {
	case common.TypeBool:
		words := (cv.capacity + 63) / 64
		cv.bools = make([]uint64, words)
	case common.TypeInt64:
		cv.int64s = make([]int64, cv.capacity)
	case common.TypeFloat64:
		cv.float64s = make([]float64, cv.capacity)
	case common.TypeString:
		cv.strings = make([]string, cv.capacity)
	case common.TypeTimestamp:
		cv.times = make([]time.Time, cv.capacity)
	}
}

// Len 返回列向量中当前有效行数。
func (cv *ColumnVector) Len() uint32 {
	return cv.len
}

// Capacity 返回列向量的容量上限。
func (cv *ColumnVector) Capacity() uint32 {
	return cv.capacity
}

// grow 扩容列向量，新容量为当前容量的两倍。
func (cv *ColumnVector) grow() {
	newCap := cv.capacity * 2
	switch cv.Typ {
	case common.TypeBool:
		words := (newCap + 63) / 64
		newBools := make([]uint64, words)
		copy(newBools, cv.bools)
		cv.bools = newBools
	case common.TypeInt64:
		newInt64s := make([]int64, newCap)
		copy(newInt64s, cv.int64s)
		cv.int64s = newInt64s
	case common.TypeFloat64:
		newFloat64s := make([]float64, newCap)
		copy(newFloat64s, cv.float64s)
		cv.float64s = newFloat64s
	case common.TypeString:
		newStrings := make([]string, newCap)
		copy(newStrings, cv.strings)
		cv.strings = newStrings
	case common.TypeTimestamp:
		newTimes := make([]time.Time, newCap)
		copy(newTimes, cv.times)
		cv.times = newTimes
	}
	cv.capacity = newCap
	newNulls := common.NewBitmap(newCap)
	for i := uint32(0); i < cv.len; i++ {
		if cv.nulls.Get(i) {
			newNulls.Set(i)
		}
	}
	cv.nulls = newNulls
}

// ensureCapacity 确保容量至少为 required 行。
func (cv *ColumnVector) ensureCapacity(required uint32) {
	for cv.capacity < required {
		cv.grow()
	}
}

// SetNull 将指定行的 NULL 标识设置为 true。
func (cv *ColumnVector) SetNull(rowIdx uint32) {
	cv.nulls.Set(rowIdx)
}

// ClearNull 清除指定行的 NULL 标识。
func (cv *ColumnVector) ClearNull(rowIdx uint32) {
	cv.nulls.Clear(rowIdx)
}

// IsNull 判断指定行是否为 NULL。
func (cv *ColumnVector) IsNull(rowIdx uint32) bool {
	return cv.nulls.Get(rowIdx)
}

// SetBool 设置指定行的 BOOL 值。
func (cv *ColumnVector) SetBool(rowIdx uint32, v bool) {
	word := rowIdx / 64
	bit := rowIdx % 64
	if v {
		cv.bools[word] |= 1 << bit
	} else {
		cv.bools[word] &^= 1 << bit
	}
	cv.ClearNull(rowIdx)
}

// SetInt64 设置指定行的 INT64 值。
func (cv *ColumnVector) SetInt64(rowIdx uint32, v int64) {
	cv.int64s[rowIdx] = v
	cv.ClearNull(rowIdx)
}

// SetFloat64 设置指定行的 FLOAT64 值。
func (cv *ColumnVector) SetFloat64(rowIdx uint32, v float64) {
	cv.float64s[rowIdx] = v
	cv.ClearNull(rowIdx)
}

// SetString 设置指定行的 STRING 值。
func (cv *ColumnVector) SetString(rowIdx uint32, v string) {
	cv.strings[rowIdx] = v
	cv.ClearNull(rowIdx)
}

// SetTimestamp 设置指定行的 TIMESTAMP 值。
func (cv *ColumnVector) SetTimestamp(rowIdx uint32, v time.Time) {
	cv.times[rowIdx] = v
	cv.ClearNull(rowIdx)
}

// GetValue 返回指定行的 common.Value 表示。
func (cv *ColumnVector) GetValue(rowIdx uint32) common.Value {
	if cv.IsNull(rowIdx) {
		return common.NewNull()
	}
	switch cv.Typ {
	case common.TypeBool:
		return common.NewBool(cv.GetBool(rowIdx))
	case common.TypeInt64:
		return common.NewInt64(cv.int64s[rowIdx])
	case common.TypeFloat64:
		return common.NewFloat64(cv.float64s[rowIdx])
	case common.TypeString:
		return common.NewString(cv.strings[rowIdx])
	case common.TypeTimestamp:
		return common.NewTimestamp(cv.times[rowIdx])
	default:
		return common.NewNull()
	}
}

// GetBool 返回指定行的 BOOL 值（调用者需保证非 NULL）。
func (cv *ColumnVector) GetBool(rowIdx uint32) bool {
	word := rowIdx / 64
	bit := rowIdx % 64
	return (cv.bools[word] & (1 << bit)) != 0
}

// SetValue 按 common.Value 类型设置指定行的值。
func (cv *ColumnVector) SetValue(rowIdx uint32, v common.Value) error {
	if v.IsNull() {
		cv.SetNull(rowIdx)
		return nil
	}
	if v.Typ != cv.Typ {
		return fmt.Errorf("%w: column type %s, value type %s",
			common.ErrTypeMismatch, cv.Typ.String(), v.Typ.String())
	}
	switch v.Typ {
	case common.TypeBool:
		cv.SetBool(rowIdx, v.Int64 != 0)
	case common.TypeInt64:
		cv.SetInt64(rowIdx, v.Int64)
	case common.TypeFloat64:
		cv.SetFloat64(rowIdx, v.Float64)
	case common.TypeString:
		cv.SetString(rowIdx, v.Str)
	case common.TypeTimestamp:
		cv.SetTimestamp(rowIdx, v.Time)
	}
	return nil
}

// Append 在列向量末尾添加一个值，必要时自动扩容。
func (cv *ColumnVector) Append(v common.Value) error {
	cv.ensureCapacity(cv.len + 1)
	if err := cv.SetValue(cv.len, v); err != nil {
		return fmt.Errorf("column append: %w", err)
	}
	cv.len++
	return nil
}

// Reset 重置列向量为空状态，保留已分配的容量。
func (cv *ColumnVector) Reset() {
	cv.len = 0
	cv.strLen = 0
	for i := uint32(0); i < cv.capacity; i++ {
		cv.nulls.Clear(i)
	}
}

// NullCount 返回 NULL 值的个数。
func (cv *ColumnVector) NullCount() uint32 {
	count := uint32(0)
	for i := uint32(0); i < cv.len; i++ {
		if cv.IsNull(i) {
			count++
		}
	}
	return count
}

// Slice 返回当前列向量在 [startRow, endRow) 范围内的切片。
// 使用直接内存拷贝，避免逐行 Append 的开销。
func (cv *ColumnVector) Slice(startRow, endRow uint32) (*ColumnVector, error) {
	if endRow > cv.len {
		return nil, fmt.Errorf("column slice: end %d exceeds length %d", endRow, cv.len)
	}
	if startRow > endRow {
		return nil, fmt.Errorf("column slice: start %d > end %d", startRow, endRow)
	}

	rowCount := endRow - startRow
	result := NewColumnVector(cv.ColumnID, cv.Typ, rowCount)
	result.len = rowCount

	switch cv.Typ {
	case common.TypeInt64:
		copy(result.int64s, cv.int64s[startRow:endRow])
	case common.TypeFloat64:
		copy(result.float64s, cv.float64s[startRow:endRow])
	case common.TypeString:
		copy(result.strings, cv.strings[startRow:endRow])
	case common.TypeTimestamp:
		copy(result.times, cv.times[startRow:endRow])
	case common.TypeBool:
		// 按 word 拷贝 bool bitmap
		startWord := startRow / 64
		endWord := (endRow + 63) / 64
		if endWord > uint32(len(cv.bools)) {
			endWord = uint32(len(cv.bools))
		}
		copy(result.bools, cv.bools[startWord:endWord])
	}

	// 拷贝 null bitmap
	for i := uint32(0); i < rowCount; i++ {
		if cv.nulls.Get(startRow + i) {
			result.nulls.Set(i)
		}
	}

	return result, nil
}

// NullBitmap 返回内部的 NULL 位图（只读引用，不要修改）。
func (cv *ColumnVector) NullBitmap() *common.Bitmap {
	return cv.nulls
}

// Int64Data 返回 INT64 类型列的底层数组（调用者需确保类型匹配）。
func (cv *ColumnVector) Int64Data() []int64 {
	return cv.int64s
}

// Float64Data 返回 FLOAT64 类型列的底层数组。
func (cv *ColumnVector) Float64Data() []float64 {
	return cv.float64s
}

// BoolData 返回 BOOL 类型列的底层 bitmap 数组。
func (cv *ColumnVector) BoolData() []uint64 {
	return cv.bools
}

// StringData 返回 STRING 类型列的底层数组。
func (cv *ColumnVector) StringData() []string {
	return cv.strings
}

// TimeData 返回 TIMESTAMP 类型列的底层数组。
func (cv *ColumnVector) TimeData() []time.Time {
	return cv.times
}
