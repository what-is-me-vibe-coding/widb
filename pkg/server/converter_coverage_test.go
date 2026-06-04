package server

import (
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// TestToInt64ValueTypeMismatch 测试 toInt64Value 对不匹配类型返回错误
func TestToInt64ValueTypeMismatch(t *testing.T) {
	tests := []struct {
		name string
		raw  interface{}
	}{
		{"string类型", "not_a_number"},
		{"bool类型", true},
		{"nil输入", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := toInt64Value(tt.raw)
			if err == nil {
				t.Errorf("toInt64Value(%v) expected error, got value %v", tt.raw, val)
			}
		})
	}
}

// TestToFloat64ValueTypeMismatch 测试 toFloat64Value 对不匹配类型返回错误
func TestToFloat64ValueTypeMismatch(t *testing.T) {
	tests := []struct {
		name string
		raw  interface{}
	}{
		{"string类型", "not_a_float"},
		{"bool类型", true},
		{"nil输入", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := toFloat64Value(tt.raw)
			if err == nil {
				t.Errorf("toFloat64Value(%v) expected error, got value %v", tt.raw, val)
			}
		})
	}
}

// TestToInt64ValueValidTypes 测试 toInt64Value 对合法类型的转换
func TestToInt64ValueValidTypes(t *testing.T) {
	tests := []struct {
		name    string
		raw     interface{}
		wantVal int64
	}{
		{"float64转int64", float64(42), 42},
		{"int64直接", int64(42), 42},
		{"int转int64", int(42), 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := toInt64Value(tt.raw)
			if err != nil {
				t.Fatalf("toInt64Value(%v) unexpected error: %v", tt.raw, err)
			}
			if val.Int64 != tt.wantVal {
				t.Errorf("toInt64Value(%v) = %d, want %d", tt.raw, val.Int64, tt.wantVal)
			}
		})
	}
}

// TestToFloat64ValueValidTypes 测试 toFloat64Value 对合法类型的转换
func TestToFloat64ValueValidTypes(t *testing.T) {
	tests := []struct {
		name    string
		raw     interface{}
		wantVal float64
	}{
		{"float64直接", float64(3.14), 3.14},
		{"int64转float64", int64(42), 42.0},
		{"int转float64", int(42), 42.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := toFloat64Value(tt.raw)
			if err != nil {
				t.Fatalf("toFloat64Value(%v) unexpected error: %v", tt.raw, err)
			}
			if val.Float64 != tt.wantVal {
				t.Errorf("toFloat64Value(%v) = %g, want %g", tt.raw, val.Float64, tt.wantVal)
			}
		})
	}
}

// TestInterfaceToValueUnsupportedType 测试 interfaceToValue 对不支持的数据类型返回错误
func TestInterfaceToValueUnsupportedType(t *testing.T) {
	val, err := interfaceToValue("hello", common.DataType(99))
	if err == nil {
		t.Errorf("interfaceToValue with unsupported type expected error, got value %v", val)
	}
}

// TestInterfaceToValueNil 测试 interfaceToValue 对 nil 值返回 NULL
func TestInterfaceToValueNil(t *testing.T) {
	val, err := interfaceToValue(nil, common.TypeInt64)
	if err != nil {
		t.Fatalf("interfaceToValue(nil) unexpected error: %v", err)
	}
	if val.Valid {
		t.Errorf("expected NULL for nil input, got %v", val)
	}
}

// TestInterfaceToValueBoolType 测试 interfaceToValue 对 bool 类型的转换
func TestInterfaceToValueBoolType(t *testing.T) {
	val, err := interfaceToValue(true, common.TypeBool)
	if err != nil {
		t.Fatalf("interfaceToValue(true, TypeBool) unexpected error: %v", err)
	}
	if val.Int64 != 1 {
		t.Errorf("expected Int64=1 for true bool, got %d", val.Int64)
	}
}

// TestInterfaceToValueBoolTypeMismatch 测试 interfaceToValue 对 bool 类型不匹配返回错误
func TestInterfaceToValueBoolTypeMismatch(t *testing.T) {
	val, err := interfaceToValue("not_bool", common.TypeBool)
	if err == nil {
		t.Errorf("interfaceToValue with bool type mismatch expected error, got value %v", val)
	}
}

// TestInterfaceToValueStringTypeMismatch 测试 interfaceToValue 对 string 类型不匹配返回错误
func TestInterfaceToValueStringTypeMismatch(t *testing.T) {
	val, err := interfaceToValue(42, common.TypeString)
	if err == nil {
		t.Errorf("interfaceToValue with string type mismatch expected error, got value %v", val)
	}
}

// TestToTimestampValueInvalidType 测试 toTimestampValue 对非字符串输入返回错误
func TestToTimestampValueInvalidType(t *testing.T) {
	val, err := toTimestampValue(12345)
	if err == nil {
		t.Errorf("toTimestampValue(int) expected error, got value %v", val)
	}
}

// TestToTimestampValueInvalidFormat 测试 toTimestampValue 对无效时间格式返回错误
func TestToTimestampValueInvalidFormat(t *testing.T) {
	val, err := toTimestampValue("not-a-timestamp")
	if err == nil {
		t.Errorf("toTimestampValue(invalid format) expected error, got value %v", val)
	}
}

// TestToTimestampValueValid 测试 toTimestampValue 对合法时间戳的转换
func TestToTimestampValueValid(t *testing.T) {
	ts := "2024-01-15T10:30:00Z"
	val, err := toTimestampValue(ts)
	if err != nil {
		t.Fatalf("toTimestampValue(%q) unexpected error: %v", ts, err)
	}
	if !val.Valid {
		t.Fatal("expected valid timestamp value")
	}
}

// TestValueToInterfaceNull 测试 valueToInterface 对 NULL 值返回 nil
func TestValueToInterfaceNull(t *testing.T) {
	result := valueToInterface(common.NewNull())
	if result != nil {
		t.Errorf("valueToInterface(NULL) = %v, want nil", result)
	}
}

// TestValueToInterfaceTimestamp 测试 valueToInterface 对 Timestamp 类型的转换
func TestValueToInterfaceTimestamp(t *testing.T) {
	now := time.Now()
	val := common.NewTimestamp(now)
	result := valueToInterface(val)
	str, ok := result.(string)
	if !ok {
		t.Fatalf("valueToInterface(Timestamp) = %T, want string", result)
	}
	expected := now.Format(time.RFC3339Nano)
	if str != expected {
		t.Errorf("valueToInterface(Timestamp) = %q, want %q", str, expected)
	}
}

// TestValueToInterfaceBool 测试 valueToInterface 对 Bool 类型的转换
func TestValueToInterfaceBool(t *testing.T) {
	val := common.NewBool(true)
	result := valueToInterface(val)
	b, ok := result.(bool)
	if !ok {
		t.Fatalf("valueToInterface(Bool) = %T, want bool", result)
	}
	if !b {
		t.Error("valueToInterface(Bool(true)) = false, want true")
	}
}

// TestChunksToRowsNilChunk 测试 chunksToRows 跳过 nil chunk
func TestChunksToRowsNilChunk(t *testing.T) {
	result := chunksToRows([]*storage.Chunk{nil})
	if len(result) != 0 {
		t.Errorf("chunksToRows with nil chunk = %d rows, want 0", len(result))
	}
}

// TestCountRowsNilChunk 测试 countRows 跳过 nil chunk
func TestCountRowsNilChunk(t *testing.T) {
	total := countRows([]*storage.Chunk{nil})
	if total != 0 {
		t.Errorf("countRows with nil chunk = %d, want 0", total)
	}
}

// TestChunksToRowsEmpty 测试 chunksToRows 对空切片返回 nil
func TestChunksToRowsEmpty(t *testing.T) {
	result := chunksToRows([]*storage.Chunk{})
	if result != nil {
		t.Errorf("chunksToRows(empty) = %v, want nil", result)
	}
}
