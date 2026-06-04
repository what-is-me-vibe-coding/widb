package index

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestBytesToValue 测试 bytesToValue 函数对所有数据类型的转换。
func TestBytesToValue_Int64(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		dataType common.DataType
		wantVal  common.Value
	}{
		{name: "Int64正常8字节", data: int64ToBytes(42), dataType: common.TypeInt64, wantVal: common.NewInt64(42)},
		{name: "Int64不足8字节", data: []byte{1, 2, 3}, dataType: common.TypeInt64, wantVal: common.NewNull()},
		{name: "Int64空bytes", data: []byte{}, dataType: common.TypeInt64, wantVal: common.NewNull()},
		{name: "Int64负数", data: int64ToBytes(-100), dataType: common.TypeInt64, wantVal: common.NewInt64(-100)},
		{name: "Int64零值", data: int64ToBytes(0), dataType: common.TypeInt64, wantVal: common.NewInt64(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bytesToValue(tt.data, tt.dataType)
			if !got.Equal(tt.wantVal) {
				t.Errorf("bytesToValue() = %v (type=%s, valid=%v), want %v (type=%s, valid=%v)",
					got, got.Typ.String(), got.Valid,
					tt.wantVal, tt.wantVal.Typ.String(), tt.wantVal.Valid)
			}
		})
	}
}

func TestBytesToValue_Float64(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		dataType common.DataType
		wantVal  common.Value
	}{
		{name: "Float64正常8字节", data: float64ToBytes(3.14), dataType: common.TypeFloat64, wantVal: common.NewFloat64(3.14)},
		{name: "Float64不足8字节", data: []byte{1, 2, 3, 4}, dataType: common.TypeFloat64, wantVal: common.NewNull()},
		{name: "Float64空bytes", data: []byte{}, dataType: common.TypeFloat64, wantVal: common.NewNull()},
		{name: "Float64零值", data: float64ToBytes(0.0), dataType: common.TypeFloat64, wantVal: common.NewFloat64(0.0)},
		{name: "Float64负数", data: float64ToBytes(-99.9), dataType: common.TypeFloat64, wantVal: common.NewFloat64(-99.9)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bytesToValue(tt.data, tt.dataType)
			if !got.Equal(tt.wantVal) {
				t.Errorf("bytesToValue() = %v (type=%s, valid=%v), want %v (type=%s, valid=%v)",
					got, got.Typ.String(), got.Valid,
					tt.wantVal, tt.wantVal.Typ.String(), tt.wantVal.Valid)
			}
		})
	}
}

func TestBytesToValue_BoolAndTimestamp(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		dataType common.DataType
		wantVal  common.Value
	}{
		{name: "Bool_true", data: []byte{1}, dataType: common.TypeBool, wantVal: common.NewBool(true)},
		{name: "Bool_false", data: []byte{0}, dataType: common.TypeBool, wantVal: common.NewBool(false)},
		{name: "Bool_非0值视为true", data: []byte{42}, dataType: common.TypeBool, wantVal: common.NewBool(true)},
		{name: "Bool空bytes", data: []byte{}, dataType: common.TypeBool, wantVal: common.NewNull()},
		{name: "Timestamp正常8字节", data: int64ToBytes(1700000000), dataType: common.TypeTimestamp, wantVal: common.NewInt64(1700000000)},
		{name: "Timestamp不足8字节", data: []byte{1, 2, 3, 4, 5, 6, 7}, dataType: common.TypeTimestamp, wantVal: common.NewNull()},
		{name: "Timestamp空bytes", data: []byte{}, dataType: common.TypeTimestamp, wantVal: common.NewNull()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bytesToValue(tt.data, tt.dataType)
			if !got.Equal(tt.wantVal) {
				t.Errorf("bytesToValue() = %v (type=%s, valid=%v), want %v (type=%s, valid=%v)",
					got, got.Typ.String(), got.Valid,
					tt.wantVal, tt.wantVal.Typ.String(), tt.wantVal.Valid)
			}
		})
	}
}

func TestBytesToValue_StringAndOther(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		dataType common.DataType
		wantVal  common.Value
	}{
		{name: "String正常", data: []byte("hello"), dataType: common.TypeString, wantVal: common.NewString("hello")},
		{name: "String空字符串", data: []byte{}, dataType: common.TypeString, wantVal: common.NewString("")},
		{name: "String中文", data: []byte("你好世界"), dataType: common.TypeString, wantVal: common.NewString("你好世界")},
		{name: "未知类型", data: []byte{1, 2, 3, 4, 5, 6, 7, 8}, dataType: common.DataType(99), wantVal: common.NewNull()},
		{name: "TypeNull类型", data: []byte{1, 2, 3}, dataType: common.TypeNull, wantVal: common.NewNull()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bytesToValue(tt.data, tt.dataType)
			if !got.Equal(tt.wantVal) {
				t.Errorf("bytesToValue() = %v (type=%s, valid=%v), want %v (type=%s, valid=%v)",
					got, got.Typ.String(), got.Valid,
					tt.wantVal, tt.wantVal.Typ.String(), tt.wantVal.Valid)
			}
		})
	}
}
