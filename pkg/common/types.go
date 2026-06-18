package common

import (
	"fmt"
	"time"
)

const strNull = "NULL"

// DataType 表示列的数据类型。
type DataType int

// DataType constants
const (
	TypeNull      DataType = iota
	TypeBool               // BOOL
	TypeInt64              // INT64
	TypeFloat64            // FLOAT64
	TypeString             // STRING / VARCHAR
	TypeTimestamp          // TIMESTAMP
	// 以下为 Plan 6 新增类型，均复用 Int64 字段存储（int family）。
	// 枚举值追加在末尾，保证与历史 WAL/Segment/Catalog 持久化数据兼容。
	TypeDate   // DATE（自 1970-01-01 起的天数，存于 Int64）
	TypeInt8   // INT8
	TypeInt16  // INT16
	TypeInt32  // INT32
	TypeUint64 // UINT64
)

// IsIntFamily 报告该类型是否属于整数族（统一以 int64 字段存储）。
// 整数族类型共享 ColumnVector 的 int64s 数组、Plain/RLE 编码与 int64 统计，
// 仅在类型标签、显示与取值范围上存在差异。DATE 亦归入整数族（存储为天数）。
func (t DataType) IsIntFamily() bool {
	switch t {
	case TypeInt64, TypeInt8, TypeInt16, TypeInt32, TypeUint64, TypeDate:
		return true
	}
	return false
}

// String 返回数据类型的可读名称。
func (t DataType) String() string {
	switch t {
	case TypeNull:
		return strNull
	case TypeBool:
		return "BOOL"
	case TypeInt64:
		return "INT64"
	case TypeFloat64:
		return "FLOAT64"
	case TypeString:
		return "STRING"
	case TypeTimestamp:
		return "TIMESTAMP"
	case TypeDate:
		return "DATE"
	case TypeInt8:
		return "INT8"
	case TypeInt16:
		return "INT16"
	case TypeInt32:
		return "INT32"
	case TypeUint64:
		return "UINT64"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", t)
	}
}

// Size 返回该类型在内存中的固定字节数（变长类型返回 -1）。
// 整数族（含 DATE）统一以 int64 存储，固定 8 字节。
func (t DataType) Size() int {
	switch t {
	case TypeNull:
		return 0
	case TypeBool:
		return 1
	case TypeInt64, TypeFloat64, TypeTimestamp:
		return 8
	case TypeInt8, TypeInt16, TypeInt32, TypeUint64, TypeDate:
		return 8
	case TypeString:
		return -1
	default:
		return -1
	}
}

// Value 是统一的列值表示结构体。
// 根据 Typ 字段选择对应的实际值字段。
type Value struct {
	Typ     DataType
	Valid   bool // false 表示 NULL
	Int64   int64
	Float64 float64
	Str     string // 字符串值，字段名避免与 String() 方法冲突
	Time    time.Time
}

// IsNull 判断值是否为 NULL。
func (v Value) IsNull() bool {
	return !v.Valid
}

// float64Value 返回 Value 的 float64 数值表示，仅供跨类型数值比较使用。
// FLOAT64 直接取 Float64 字段；整数族（含 DATE）将 Int64 转为 float64；
// 非数值类型返回 0，调用方需确保仅在数值类型间使用。
func (v Value) float64Value() float64 {
	if v.Typ == TypeFloat64 {
		return v.Float64
	}
	if v.Typ.IsIntFamily() {
		return float64(v.Int64)
	}
	return 0
}

// isFloatIntCrossType 报告两个类型是否为 FLOAT64 与整数族的跨类型组合，
// 此类组合需按 float64 提升后比较，使 `WHERE float_col > 25`（字面量为 INT64）能正确命中。
func isFloatIntCrossType(a, b DataType) bool {
	return (a == TypeFloat64 && b.IsIntFamily()) ||
		(b == TypeFloat64 && a.IsIntFamily())
}

// Equal 比较两个 Value 是否相等（支持 NULL 比较）。
// 整数族类型（INT8/16/32/64/UINT64/DATE）跨类型按 Int64 字段比较，
// 使 `WHERE int8_col = 5`（字面量为 INT64）能正确命中。
// FLOAT64 与整数族跨类型按 float64 比较，使 `WHERE float_col = 30` 能正确命中。
func (v Value) Equal(other Value) bool {
	if !v.Valid && !other.Valid {
		return true
	}
	if !v.Valid || !other.Valid {
		return false
	}
	if v.Typ.IsIntFamily() && other.Typ.IsIntFamily() {
		return v.Int64 == other.Int64
	}
	if isFloatIntCrossType(v.Typ, other.Typ) {
		return v.float64Value() == other.float64Value()
	}
	if v.Typ != other.Typ {
		return false
	}
	switch v.Typ {
	case TypeBool:
		return v.Int64 == other.Int64
	case TypeInt64:
		return v.Int64 == other.Int64
	case TypeFloat64:
		return v.Float64 == other.Float64
	case TypeString:
		return v.Str == other.Str
	case TypeTimestamp:
		return v.Time.Equal(other.Time)
	default:
		return false
	}
}

// Less 比较 Value 是否小于另一个（类型不同或任一 NULL 时返回 false）。
// 整数族类型跨类型按 Int64 字段比较；FLOAT64 与整数族跨类型按 float64 比较。
func (v Value) Less(other Value) bool {
	if !v.Valid || !other.Valid {
		return false
	}
	if v.Typ.IsIntFamily() && other.Typ.IsIntFamily() {
		return v.Int64 < other.Int64
	}
	if isFloatIntCrossType(v.Typ, other.Typ) {
		return v.float64Value() < other.float64Value()
	}
	if v.Typ != other.Typ {
		return false
	}
	switch v.Typ {
	case TypeBool:
		return v.Int64 < other.Int64
	case TypeInt64:
		return v.Int64 < other.Int64
	case TypeTimestamp:
		return v.Time.Before(other.Time)
	case TypeFloat64:
		return v.Float64 < other.Float64
	case TypeString:
		return v.Str < other.Str
	default:
		return false
	}
}

// String 返回 Value 的可读字符串表示。
func (v Value) String() string {
	if !v.Valid {
		return strNull
	}
	switch v.Typ {
	case TypeBool:
		if v.Int64 != 0 {
			return "true"
		}
		return "false"
	case TypeInt64, TypeInt8, TypeInt16, TypeInt32, TypeUint64:
		return fmt.Sprintf("%d", v.Int64)
	case TypeDate:
		return daysToDate(v.Int64).Format(dateFormat)
	case TypeFloat64:
		return fmt.Sprintf("%g", v.Float64)
	case TypeString:
		return v.Str
	case TypeTimestamp:
		return v.Time.Format(time.RFC3339Nano)
	default:
		return "?"
	}
}

// NewNull 创建 NULL 值。
func NewNull() Value {
	return Value{Typ: TypeNull, Valid: false}
}

// NewBool 创建 BOOL 值。
func NewBool(v bool) Value {
	var i int64
	if v {
		i = 1
	}
	return Value{Typ: TypeBool, Valid: true, Int64: i}
}

// NewInt64 创建 INT64 值。
func NewInt64(v int64) Value {
	return Value{Typ: TypeInt64, Valid: true, Int64: v}
}

// NewFloat64 创建 FLOAT64 值。
func NewFloat64(v float64) Value {
	return Value{Typ: TypeFloat64, Valid: true, Float64: v}
}

// NewString 创建 STRING 值。
func NewString(v string) Value {
	return Value{Typ: TypeString, Valid: true, Str: v}
}

// NewTimestamp 创建 TIMESTAMP 值。
func NewTimestamp(v time.Time) Value {
	return Value{Typ: TypeTimestamp, Valid: true, Time: v}
}

// NewInt8 创建 INT8 值。
func NewInt8(v int64) Value {
	return Value{Typ: TypeInt8, Valid: true, Int64: v}
}

// NewInt16 创建 INT16 值。
func NewInt16(v int64) Value {
	return Value{Typ: TypeInt16, Valid: true, Int64: v}
}

// NewInt32 创建 INT32 值。
func NewInt32(v int64) Value {
	return Value{Typ: TypeInt32, Valid: true, Int64: v}
}

// NewUint64 创建 UINT64 值。
func NewUint64(v int64) Value {
	return Value{Typ: TypeUint64, Valid: true, Int64: v}
}

// NewDate 创建 DATE 值，v 为自 1970-01-01 起的天数。
func NewDate(v int64) Value {
	return Value{Typ: TypeDate, Valid: true, Int64: v}
}

// NewIntFamilyValue 按指定的整数族类型创建 Value。
// typ 必须为整数族类型之一，否则回退为 TypeInt64。
func NewIntFamilyValue(typ DataType, v int64) Value {
	if !typ.IsIntFamily() {
		typ = TypeInt64
	}
	return Value{Typ: typ, Valid: true, Int64: v}
}

// NewDateFromTime 创建 DATE 值，按 UTC 日期转换为自 1970-01-01 起的天数。
func NewDateFromTime(v time.Time) Value {
	return Value{Typ: TypeDate, Valid: true, Int64: dateToDays(v)}
}

const dateFormat = "2006-01-02"

// DateFormat 返回 DATE 类型的标准格式字符串（"2006-01-02"）。
func DateFormat() string { return dateFormat }

var epochDate = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

// dateToDays 将 time.Time 转换为自 1970-01-01 起的天数（UTC）。
func dateToDays(t time.Time) int64 {
	return int64(t.UTC().Sub(epochDate) / (24 * time.Hour))
}

// daysToDate 将自 1970-01-01 起的天数转换回 UTC 午夜的 time.Time。
func daysToDate(days int64) time.Time {
	return epochDate.AddDate(0, 0, int(days))
}
