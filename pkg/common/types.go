package common

import (
	"fmt"
	"time"
)

// DataType 表示列的数据类型。
type DataType int

const (
	TypeNull      DataType = iota
	TypeBool               // BOOL
	TypeInt64              // INT64
	TypeFloat64            // FLOAT64
	TypeString             // STRING / VARCHAR
	TypeTimestamp          // TIMESTAMP
)

// String 返回数据类型的可读名称。
func (t DataType) String() string {
	switch t {
	case TypeNull:
		return "NULL"
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
	default:
		return fmt.Sprintf("UNKNOWN(%d)", t)
	}
}

// Size 返回该类型在内存中的固定字节数（变长类型返回 -1）。
func (t DataType) Size() int {
	switch t {
	case TypeNull:
		return 0
	case TypeBool:
		return 1
	case TypeInt64, TypeFloat64, TypeTimestamp:
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

// Equal 比较两个 Value 是否相等（支持 NULL 比较）。
func (v Value) Equal(other Value) bool {
	if v.Typ != other.Typ {
		return false
	}
	if !v.Valid && !other.Valid {
		return true
	}
	if !v.Valid || !other.Valid {
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

// String 返回 Value 的可读字符串表示。
func (v Value) String() string {
	if !v.Valid {
		return "NULL"
	}
	switch v.Typ {
	case TypeBool:
		if v.Int64 != 0 {
			return "true"
		}
		return "false"
	case TypeInt64:
		return fmt.Sprintf("%d", v.Int64)
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
