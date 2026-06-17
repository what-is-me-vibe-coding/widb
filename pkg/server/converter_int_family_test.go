package server

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestInterfaceToValueIntFamily 验证整数族类型的 interfaceToValue 转换。
func TestInterfaceToValueIntFamily(t *testing.T) {
	tests := []struct {
		name    string
		raw     interface{}
		typ     common.DataType
		wantTyp common.DataType
		wantVal int64
	}{
		{"float64 -> INT8", float64(42), common.TypeInt8, common.TypeInt8, 42},
		{"int64 -> INT16", int64(100), common.TypeInt16, common.TypeInt16, 100},
		{"int -> INT32", int(1000), common.TypeInt32, common.TypeInt32, 1000},
		{"float64 -> UINT64", float64(99), common.TypeUint64, common.TypeUint64, 99},
		{"int64 -> INT64", int64(7), common.TypeInt64, common.TypeInt64, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := interfaceToValue(tt.raw, tt.typ)
			if err != nil {
				t.Fatalf("interfaceToValue error: %v", err)
			}
			if v.Typ != tt.wantTyp {
				t.Errorf("type = %s, want %s", v.Typ, tt.wantTyp)
			}
			if v.Int64 != tt.wantVal {
				t.Errorf("value = %d, want %d", v.Int64, tt.wantVal)
			}
		})
	}
}

// TestInterfaceToValueDate 验证 DATE 类型的 interfaceToValue 转换。
func TestInterfaceToValueDate(t *testing.T) {
	// 字符串解析
	v, err := interfaceToValue("2024-01-01", common.TypeDate)
	if err != nil {
		t.Fatalf("interfaceToValue(2024-01-01, DATE) error: %v", err)
	}
	if v.Typ != common.TypeDate {
		t.Errorf("type = %s, want DATE", v.Typ)
	}
	if v.Int64 != 19723 {
		t.Errorf("value = %d, want 19723", v.Int64)
	}
	// int64 天数
	v2, err := interfaceToValue(int64(100), common.TypeDate)
	if err != nil {
		t.Fatalf("interfaceToValue(int64(100), DATE) error: %v", err)
	}
	if v2.Int64 != 100 {
		t.Errorf("value = %d, want 100", v2.Int64)
	}
	// float64 天数
	v3, err := interfaceToValue(float64(200), common.TypeDate)
	if err != nil {
		t.Fatalf("interfaceToValue(float64(200), DATE) error: %v", err)
	}
	if v3.Int64 != 200 {
		t.Errorf("value = %d, want 200", v3.Int64)
	}
	// int 天数
	v4, err := interfaceToValue(int(300), common.TypeDate)
	if err != nil {
		t.Fatalf("interfaceToValue(int(300), DATE) error: %v", err)
	}
	if v4.Int64 != 300 {
		t.Errorf("value = %d, want 300", v4.Int64)
	}
}

// TestInterfaceToValueDateInvalid 验证 DATE 类型拒绝非法输入。
func TestInterfaceToValueDateInvalid(t *testing.T) {
	// 非法日期字符串
	if _, err := interfaceToValue("not-a-date", common.TypeDate); err == nil {
		t.Error("interfaceToValue(invalid string, DATE): expected error, got nil")
	}
	// 非法类型
	if _, err := interfaceToValue(true, common.TypeDate); err == nil {
		t.Error("interfaceToValue(bool, DATE): expected error, got nil")
	}
}

// TestValueToInterfaceIntFamily 验证整数族类型的 valueToInterface 转换。
func TestValueToInterfaceIntFamily(t *testing.T) {
	tests := []struct {
		name string
		v    common.Value
		want interface{}
	}{
		{"INT8", common.NewInt8(-5), int64(-5)},
		{"INT16", common.NewInt16(1000), int64(1000)},
		{"INT32", common.NewInt32(100000), int64(100000)},
		{"UINT64", common.NewUint64(42), int64(42)},
		{"INT64", common.NewInt64(7), int64(7)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := valueToInterface(tt.v)
			if got != tt.want {
				t.Errorf("valueToInterface() = %v (%T), want %v (%T)", got, got, tt.want, tt.want)
			}
		})
	}
}

// TestValueToInterfaceDate 验证 DATE 类型的 valueToInterface 转换为字符串。
func TestValueToInterfaceDate(t *testing.T) {
	v := common.NewDate(19723) // 2024-01-01
	got := valueToInterface(v)
	want := "2024-01-01"
	if got != want {
		t.Errorf("valueToInterface(DATE 19723) = %v, want %v", got, want)
	}
}
