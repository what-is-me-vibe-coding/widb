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
		{TypeNull, "NULL"},
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
	tests := []struct {
		a, b Value
		want bool
	}{
		{NewNull(), NewNull(), true},
		{NewNull(), NewInt64(0), false},
		{NewInt64(1), NewInt64(1), true},
		{NewInt64(1), NewInt64(2), false},
		{NewBool(true), NewBool(true), true},
		{NewBool(true), NewBool(false), false},
		{NewFloat64(1.5), NewFloat64(1.5), true},
		{NewString("a"), NewString("a"), true},
		{NewString("a"), NewString("b"), false},
		{NewInt64(1), NewFloat64(1), false}, // 类型不同
	}
	for _, tt := range tests {
		if got := tt.a.Equal(tt.b); got != tt.want {
			t.Errorf("Value(%v).Equal(%v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestValueString(t *testing.T) {
	tests := []struct {
		v    Value
		want string
	}{
		{NewNull(), "NULL"},
		{NewBool(true), "true"},
		{NewBool(false), "false"},
		{NewInt64(42), "42"},
		{NewFloat64(3.14), "3.14"},
		{NewString("hello"), "hello"},
	}
	for _, tt := range tests {
		if got := tt.v.String(); got != tt.want {
			t.Errorf("Value.String() = %q, want %q", got, tt.want)
		}
	}
}
