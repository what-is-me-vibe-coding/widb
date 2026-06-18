package common

import (
	"testing"
	"time"
)

// TestIntFamilyTypesString 验证整数族与 DATE 类型的 String() 输出。
func TestIntFamilyTypesString(t *testing.T) {
	tests := []struct {
		typ  DataType
		want string
	}{
		{TypeDate, "DATE"},
		{TypeInt8, "INT8"},
		{TypeInt16, "INT16"},
		{TypeInt32, "INT32"},
		{TypeUint64, "UINT64"},
	}
	for _, tt := range tests {
		if got := tt.typ.String(); got != tt.want {
			t.Errorf("DataType(%d).String() = %q, want %q", tt.typ, got, tt.want)
		}
	}
}

// TestIntFamilyTypesSize 验证整数族与 DATE 类型的 Size() 返回 8 字节。
func TestIntFamilyTypesSize(t *testing.T) {
	for _, typ := range []DataType{TypeInt8, TypeInt16, TypeInt32, TypeUint64, TypeDate} {
		if got := typ.Size(); got != 8 {
			t.Errorf("DataType(%s).Size() = %d, want 8", typ, got)
		}
	}
}

// TestIsIntFamily 验证 IsIntFamily 方法的判定逻辑。
func TestIsIntFamily(t *testing.T) {
	members := []DataType{TypeInt64, TypeInt8, TypeInt16, TypeInt32, TypeUint64, TypeDate}
	for _, typ := range members {
		if !typ.IsIntFamily() {
			t.Errorf("DataType(%s).IsIntFamily() = false, want true", typ)
		}
	}
	nonMembers := []DataType{TypeNull, TypeBool, TypeFloat64, TypeString, TypeTimestamp, DataType(99)}
	for _, typ := range nonMembers {
		if typ.IsIntFamily() {
			t.Errorf("DataType(%s).IsIntFamily() = true, want false", typ)
		}
	}
}

// TestIntFamilyConstructors 验证整数族构造函数设置正确的类型标签和值。
func TestIntFamilyConstructors(t *testing.T) {
	tests := []struct {
		name string
		got  Value
		typ  DataType
		val  int64
	}{
		{"NewInt8", NewInt8(-5), TypeInt8, -5},
		{"NewInt16", NewInt16(1000), TypeInt16, 1000},
		{"NewInt32", NewInt32(100000), TypeInt32, 100000},
		{"NewUint64", NewUint64(1 << 40), TypeUint64, 1 << 40},
		{"NewDate", NewDate(20000), TypeDate, 20000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got.Typ != tt.typ {
				t.Errorf("%s: Typ = %s, want %s", tt.name, tt.got.Typ, tt.typ)
			}
			if !tt.got.Valid {
				t.Errorf("%s: Valid = false, want true", tt.name)
			}
			if tt.got.Int64 != tt.val {
				t.Errorf("%s: Int64 = %d, want %d", tt.name, tt.got.Int64, tt.val)
			}
		})
	}
}

// TestNewDateFromTime 验证 NewDateFromTime 按 UTC 日期转换为天数。
func TestNewDateFromTime(t *testing.T) {
	// 1970-01-01 UTC -> 0 天
	epoch := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	if v := NewDateFromTime(epoch); v.Int64 != 0 {
		t.Errorf("NewDateFromTime(epoch) = %d, want 0", v.Int64)
	}
	// 1970-01-02 UTC -> 1 天
	day2 := time.Date(1970, 1, 2, 0, 0, 0, 0, time.UTC)
	if v := NewDateFromTime(day2); v.Int64 != 1 {
		t.Errorf("NewDateFromTime(day2) = %d, want 1", v.Int64)
	}
	// 2024-01-01 UTC -> 19723 天
	d2024 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if v := NewDateFromTime(d2024); v.Int64 != 19723 {
		t.Errorf("NewDateFromTime(2024-01-01) = %d, want 19723", v.Int64)
	}
	// 非UTC时区应按UTC日期计算
	tz := time.Date(2024, 1, 1, 23, 0, 0, 0, time.UTC)
	if v := NewDateFromTime(tz); v.Int64 != 19723 {
		t.Errorf("NewDateFromTime(2024-01-01 23:00 UTC) = %d, want 19723", v.Int64)
	}
}

// TestNewIntFamilyValue 验证 NewIntFamilyValue 的类型回退行为。
func TestNewIntFamilyValue(t *testing.T) {
	// 整数族类型保留指定标签
	for _, typ := range []DataType{TypeInt64, TypeInt8, TypeInt16, TypeInt32, TypeUint64, TypeDate} {
		v := NewIntFamilyValue(typ, 42)
		if v.Typ != typ {
			t.Errorf("NewIntFamilyValue(%s, 42): Typ = %s, want %s", typ, v.Typ, typ)
		}
		if v.Int64 != 42 {
			t.Errorf("NewIntFamilyValue(%s, 42): Int64 = %d, want 42", typ, v.Int64)
		}
		if !v.Valid {
			t.Errorf("NewIntFamilyValue(%s, 42): Valid = false, want true", typ)
		}
	}
	// 非整数族类型回退到 INT64
	v := NewIntFamilyValue(TypeString, 7)
	if v.Typ != TypeInt64 {
		t.Errorf("NewIntFamilyValue(TypeString, 7): Typ = %s, want INT64", v.Typ)
	}
	if v.Int64 != 7 {
		t.Errorf("NewIntFamilyValue(TypeString, 7): Int64 = %d, want 7", v.Int64)
	}
}

// TestIntFamilyEqual 验证整数族跨类型相等比较。
func TestIntFamilyEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b Value
		want bool
	}{
		{"INT8 与 INT64 同值相等", NewInt8(5), NewInt64(5), true},
		{"INT8 与 INT64 不同值不等", NewInt8(5), NewInt64(6), false},
		{"INT16 与 INT32 同值相等", NewInt16(100), NewInt32(100), true},
		{"UINT64 与 DATE 同值相等", NewUint64(100), NewDate(100), true},
		{"INT32 与 INT32 同值相等", NewInt32(-1), NewInt32(-1), true},
		{"DATE 与 INT64 跨类型相等", NewDate(19723), NewInt64(19723), true},
		{"INT8 与 FLOAT64 跨类型数值相等", NewInt8(1), NewFloat64(1), true},
		{"INT8 与 FLOAT64 跨类型数值不等", NewInt8(1), NewFloat64(2), false},
		{"INT8 与 STRING 类型不同不等", NewInt8(1), NewString("1"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Equal(tt.b); got != tt.want {
				t.Errorf("Equal() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestIntFamilyLess 验证整数族跨类型小于比较。
func TestIntFamilyLess(t *testing.T) {
	tests := []struct {
		name string
		a, b Value
		want bool
	}{
		{"INT8 < INT64 跨类型", NewInt8(5), NewInt64(6), true},
		{"INT8 < INT64 反向", NewInt8(6), NewInt64(5), false},
		{"INT16 < INT32 跨类型", NewInt16(100), NewInt32(101), true},
		{"DATE < DATE 同类型", NewDate(100), NewDate(101), true},
		{"UINT64 < INT64 跨类型", NewUint64(99), NewInt64(100), true},
		{"INT8 < FLOAT64 跨类型数值比较", NewInt8(1), NewFloat64(2), true},
		{"INT8 < FLOAT64 跨类型反向", NewInt8(3), NewFloat64(2), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Less(tt.b); got != tt.want {
				t.Errorf("Less() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestIntFamilyValueString 验证整数族与 DATE 的 Value.String() 输出。
func TestIntFamilyValueString(t *testing.T) {
	tests := []struct {
		name string
		v    Value
		want string
	}{
		{"INT8", NewInt8(-5), "-5"},
		{"INT16", NewInt16(1000), "1000"},
		{"INT32", NewInt32(100000), "100000"},
		{"UINT64", NewUint64(42), "42"},
		{"DATE epoch", NewDate(0), "1970-01-01"},
		{"DATE 2024-01-01", NewDate(19723), "2024-01-01"},
		{"DATE 1970-01-02", NewDate(1), "1970-01-02"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.v.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDateFormat 验证 DateFormat() 返回标准 Go 日期格式。
func TestDateFormat(t *testing.T) {
	if got := DateFormat(); got != "2006-01-02" {
		t.Errorf("DateFormat() = %q, want %q", got, "2006-01-02")
	}
	// 验证可用于 time.Parse
	t1, err := time.Parse(DateFormat(), "2024-06-15")
	if err != nil {
		t.Fatalf("time.Parse(DateFormat(), 2024-06-15) error: %v", err)
	}
	if v := NewDateFromTime(t1); v.Int64 != 19889 {
		t.Errorf("NewDateFromTime(2024-06-15) = %d, want 19889", v.Int64)
	}
}
