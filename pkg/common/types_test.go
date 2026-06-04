package common

import (
	"testing"
	"time"
)

func TestDataTypeString(t *testing.T) {
	tests := []struct {
		typ  DataType
		want string
	}{
		{TypeNull, strNull},
		{TypeBool, "BOOL"},
		{TypeInt64, "INT64"},
		{TypeFloat64, "FLOAT64"},
		{TypeString, "STRING"},
		{TypeTimestamp, "TIMESTAMP"},
		{DataType(99), "UNKNOWN(99)"},
	}
	for _, tt := range tests {
		if got := tt.typ.String(); got != tt.want {
			t.Errorf("DataType(%d).String() = %q, want %q", tt.typ, got, tt.want)
		}
	}
}

func TestDataTypeSize(t *testing.T) {
	tests := []struct {
		typ  DataType
		want int
	}{
		{TypeNull, 0},
		{TypeBool, 1},
		{TypeInt64, 8},
		{TypeFloat64, 8},
		{TypeString, -1},
		{TypeTimestamp, 8},
		{DataType(99), -1}, // 未知类型，返回 -1
	}
	for _, tt := range tests {
		if got := tt.typ.Size(); got != tt.want {
			t.Errorf("DataType(%d).Size() = %d, want %d", tt.typ, got, tt.want)
		}
	}
}

func TestValueConstructors(t *testing.T) {
	if v := NewNull(); !v.IsNull() || v.Typ != TypeNull {
		t.Errorf("NewNull() = %+v, want NULL", v)
	}
	if v := NewBool(true); v.Typ != TypeBool || v.Int64 != 1 {
		t.Errorf("NewBool(true) = %+v", v)
	}
	if v := NewBool(false); v.Typ != TypeBool || v.Int64 != 0 {
		t.Errorf("NewBool(false) = %+v", v)
	}
	if v := NewInt64(42); v.Typ != TypeInt64 || v.Int64 != 42 {
		t.Errorf("NewInt64(42) = %+v", v)
	}
	if v := NewFloat64(3.14); v.Typ != TypeFloat64 || v.Float64 != 3.14 {
		t.Errorf("NewFloat64(3.14) = %+v", v)
	}
	if v := NewString("hello"); v.Typ != TypeString || v.Str != "hello" {
		t.Errorf("NewString(hello) = %+v", v)
	}
	now := time.Now()
	if v := NewTimestamp(now); v.Typ != TypeTimestamp || !v.Time.Equal(now) {
		t.Errorf("NewTimestamp(now) = %+v", v)
	}
}

func TestValueEqual(t *testing.T) {
	ts1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	ts2 := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		a, b Value
		want bool
	}{
		{"两个 NULL 相等", NewNull(), NewNull(), true},
		{"NULL 与非 NULL 不相等", NewNull(), NewInt64(0), false},
		{"Int64 相等", NewInt64(1), NewInt64(1), true},
		{"Int64 不相等", NewInt64(1), NewInt64(2), false},
		{"Bool 相等", NewBool(true), NewBool(true), true},
		{"Bool 不相等", NewBool(true), NewBool(false), false},
		{"Float64 相等", NewFloat64(1.5), NewFloat64(1.5), true},
		{"String 相等", NewString("a"), NewString("a"), true},
		{"String 不相等", NewString("a"), NewString("b"), false},
		{"类型不同不相等", NewInt64(1), NewFloat64(1), false},
		{"Timestamp 相等", NewTimestamp(ts1), NewTimestamp(ts1), true},
		{"Timestamp 不相等", NewTimestamp(ts1), NewTimestamp(ts2), false},
		{"Timestamp 与 Int64 类型不同", NewTimestamp(ts1), NewInt64(ts1.Unix()), false},
		{"同类型一个 NULL 一个非 NULL", Value{Typ: TypeInt64, Valid: false}, NewInt64(1), false},
		{"同类型一个非 NULL 一个 NULL", NewInt64(1), Value{Typ: TypeInt64, Valid: false}, false},
		{"未知类型 Valid 都为 true", Value{Typ: DataType(99), Valid: true}, Value{Typ: DataType(99), Valid: true}, false},
		{"TypeNull Valid 都为 true", Value{Typ: TypeNull, Valid: true}, Value{Typ: TypeNull, Valid: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Equal(tt.b); got != tt.want {
				t.Errorf("Equal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValueString(t *testing.T) {
	ts := time.Date(2024, 1, 1, 12, 30, 45, 0, time.UTC)

	tests := []struct {
		name string
		v    Value
		want string
	}{
		{"NULL", NewNull(), strNull},
		{"Bool true", NewBool(true), "true"},
		{"Bool false", NewBool(false), "false"},
		{"Int64", NewInt64(42), "42"},
		{"Float64", NewFloat64(3.14), "3.14"},
		{"String", NewString("hello"), "hello"},
		{"Timestamp", NewTimestamp(ts), ts.Format(time.RFC3339Nano)},
		{"未知类型 Valid=true", Value{Typ: DataType(99), Valid: true}, "?"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.v.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValueLess(t *testing.T) {
	tests := []struct {
		a, b Value
		want bool
	}{
		{NewNull(), NewNull(), false},
		{NewNull(), NewInt64(0), false},
		{NewInt64(0), NewNull(), false},
		{NewInt64(1), NewInt64(2), true},
		{NewInt64(2), NewInt64(1), false},
		{NewInt64(1), NewInt64(1), false},
		{NewFloat64(1.0), NewFloat64(2.0), true},
		{NewFloat64(2.0), NewFloat64(1.0), false},
		{NewString("a"), NewString("b"), true},
		{NewString("b"), NewString("a"), false},
		{NewBool(false), NewBool(true), true},
		{NewBool(true), NewBool(false), false},
		{NewInt64(1), NewFloat64(2), false}, // 类型不同
	}
	for _, tt := range tests {
		if got := tt.a.Less(tt.b); got != tt.want {
			t.Errorf("Value(%v).Less(%v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
