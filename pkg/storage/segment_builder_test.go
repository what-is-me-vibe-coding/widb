package storage

import (
	"fmt"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestSegmentBuilderSetBloomFPRate(t *testing.T) {
	keys := []string{"a", "b", "c"}
	values := []int64{1, 2, 3}

	builder := NewSegmentBuilder(100, "a", "c")
	builder.SetKeys(keys)
	builder.SetBloomFPRate(0.001) // Custom FP rate

	enc, err := EncodeColumn(common.TypeInt64, values, 3, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	builder.AddEncodedColumn(enc)

	seg, err := builder.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if len(seg.Footer.BloomFilter) == 0 {
		t.Error("expected bloom filter to be built with custom FP rate")
	}
}

func TestSegmentGetColumnValueOutOfRangeIndex(t *testing.T) {
	seg := buildTestSegmentForSegment(t)

	// Request column index that doesn't exist
	_, err := seg.GetColumnValue(99, 0)
	if err == nil {
		t.Error("expected error for out-of-range column index")
	}
}

func TestSegmentFindRowByKeyNotFoundInList(t *testing.T) {
	seg := &Segment{Keys: []string{"a", "c", "e"}}
	_, found := seg.FindRowByKey("b")
	if found {
		t.Error("expected false for key not in sorted list")
	}
}

func TestSegmentForEachColumnStat(t *testing.T) {
	seg := buildTestSegmentForSegment(t)

	var colIDs []uint32
	seg.ForEachColumnStat(func(colID uint32, _ common.DataType, _, _ []byte, _ uint32) {
		colIDs = append(colIDs, colID)
	})
	if len(colIDs) == 0 {
		t.Error("expected at least one column stat")
	}
}

func TestSegmentGetAllColumnValuesFromBuilder(t *testing.T) {
	seg := buildTestSegmentForSegment(t)
	colMeta := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	vals, err := seg.GetAllColumnValues(0, colMeta)
	if err != nil {
		t.Fatalf("GetAllColumnValues: %v", err)
	}
	if len(vals) == 0 {
		t.Error("expected at least one column value")
	}
}

// TestGetColumnValueBasic 测试 GetColumnValue 的基本功能
func TestGetColumnValueBasic(t *testing.T) {
	keys := []string{"a", "b", "c"}
	values := []int64{10, 20, 30}
	rowCount := uint32(3)

	builder := NewSegmentBuilder(200, "a", "c")
	builder.SetKeys(keys)
	enc, err := EncodeColumn(common.TypeInt64, values, rowCount, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	builder.AddEncodedColumn(enc)

	seg, err := builder.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	tests := []struct {
		rowIdx   uint32
		wantVal  int64
		wantNull bool
	}{
		{0, 10, false},
		{1, 20, false},
		{2, 30, false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("row_%d", tt.rowIdx), func(t *testing.T) {
			val, err := seg.GetColumnValue(0, tt.rowIdx)
			if err != nil {
				t.Fatalf("GetColumnValue: %v", err)
			}
			if tt.wantNull {
				if !val.IsNull() {
					t.Errorf("expected null, got %v", val)
				}
			} else {
				if val.Int64 != tt.wantVal {
					t.Errorf("got %d, want %d", val.Int64, tt.wantVal)
				}
			}
		})
	}
}

// TestGetColumnValueWithNulls 测试 GetColumnValue 对 null 值的处理
func TestGetColumnValueWithNulls(t *testing.T) {
	keys := []string{"a", "b", "c"}
	values := []int64{10, 20, 30}
	rowCount := uint32(3)
	nulls := common.NewBitmap(3)
	nulls.Set(1) // 第二行为 null

	builder := NewSegmentBuilder(201, "a", "c")
	builder.SetKeys(keys)
	enc, err := EncodeColumn(common.TypeInt64, values, rowCount, nulls)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	builder.AddEncodedColumn(enc)

	seg, err := builder.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// 验证 null 行
	val, err := seg.GetColumnValue(0, 1)
	if err != nil {
		t.Fatalf("GetColumnValue: %v", err)
	}
	if !val.IsNull() {
		t.Errorf("expected null at row 1, got %v", val)
	}

	// 验证非 null 行
	val, err = seg.GetColumnValue(0, 0)
	if err != nil {
		t.Fatalf("GetColumnValue: %v", err)
	}
	if val.Int64 != 10 {
		t.Errorf("expected 10, got %d", val.Int64)
	}
}

// TestGetColumnValueStringColumn 测试 GetColumnValue 对字符串列的处理
func TestGetColumnValueStringColumn(t *testing.T) {
	keys := []string{"a", "b", "c"}
	strs := []string{testStrHello, testStrWorld, testStrFoo}
	rowCount := uint32(3)

	builder := NewSegmentBuilder(202, "a", "c")
	builder.SetKeys(keys)
	enc, err := EncodeColumn(common.TypeString, strs, rowCount, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	builder.AddEncodedColumn(enc)

	seg, err := builder.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	val, err := seg.GetColumnValue(0, 1)
	if err != nil {
		t.Fatalf("GetColumnValue: %v", err)
	}
	if val.Str != testStrWorld {
		t.Errorf("expected 'world', got %q", val.Str)
	}
}

// TestGetColumnValueFloat64Column 测试 GetColumnValue 对 Float64 列的处理
func TestGetColumnValueFloat64Column(t *testing.T) {
	keys := []string{"a", "b"}
	floats := []float64{1.5, 2.7}
	rowCount := uint32(2)

	builder := NewSegmentBuilder(203, "a", "b")
	builder.SetKeys(keys)
	enc, err := EncodeColumn(common.TypeFloat64, floats, rowCount, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	builder.AddEncodedColumn(enc)

	seg, err := builder.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	val, err := seg.GetColumnValue(0, 0)
	if err != nil {
		t.Fatalf("GetColumnValue: %v", err)
	}
	if val.Float64 != 1.5 {
		t.Errorf("expected 1.5, got %f", val.Float64)
	}
}

// TestGetColumnValueBoolColumn 测试 GetColumnValue 对 Bool 列的处理
func TestGetColumnValueBoolColumn(t *testing.T) {
	keys := []string{"a", "b"}
	bools := []uint64{1, 0}
	rowCount := uint32(2)

	builder := NewSegmentBuilder(204, "a", "b")
	builder.SetKeys(keys)
	enc, err := EncodeColumn(common.TypeBool, bools, rowCount, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	builder.AddEncodedColumn(enc)

	seg, err := builder.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	val, err := seg.GetColumnValue(0, 0)
	if err != nil {
		t.Fatalf("GetColumnValue: %v", err)
	}
	if val.IsNull() || val.Int64 != 1 {
		t.Errorf("expected true, got %v", val)
	}

	val, err = seg.GetColumnValue(0, 1)
	if err != nil {
		t.Fatalf("GetColumnValue: %v", err)
	}
	if val.IsNull() || val.Int64 != 0 {
		t.Errorf("expected false, got %v", val)
	}
}

// TestGetColumnValueTimestampColumn 测试 GetColumnValue 对 Timestamp 列的处理
func TestGetColumnValueTimestampColumn(t *testing.T) {
	keys := []string{"a", "b"}
	times := []int64{1000000000, 2000000000}
	rowCount := uint32(2)

	builder := NewSegmentBuilder(205, "a", "b")
	builder.SetKeys(keys)
	enc, err := EncodeColumn(common.TypeTimestamp, times, rowCount, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	builder.AddEncodedColumn(enc)

	seg, err := builder.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	val, err := seg.GetColumnValue(0, 0)
	if err != nil {
		t.Fatalf("GetColumnValue: %v", err)
	}
	if val.IsNull() {
		t.Fatal("expected non-null timestamp")
	}
}

// TestGetColumnValueRowOutOfRange 测试 GetColumnValue 行索引越界
func TestGetColumnValueRowOutOfRange(t *testing.T) {
	seg := buildTestSegmentForSegment(t)
	// 行索引越界 - 应返回 null 而不是 panic
	val, err := seg.GetColumnValue(0, 999)
	if err != nil {
		// 也可能返回错误，取决于实现
		t.Logf("GetColumnValue with out-of-range row: %v", err)
	} else if !val.IsNull() {
		t.Errorf("expected null for out-of-range row, got %v", val)
	}
}

// TestAddEncodedColumnNil 测试 AddEncodedColumn 对 nil 的处理
func TestAddEncodedColumnNil(t *testing.T) {
	builder := NewSegmentBuilder(300, "a", "c")
	builder.AddEncodedColumn(nil)
	// 添加 nil 列不应 panic，也不应添加任何列
	_, err := builder.Build()
	if err == nil {
		t.Error("expected error when building segment with no columns")
	}
}

func buildTestSegmentForSegment(t *testing.T) *Segment {
	t.Helper()
	keys := []string{"a", "b", "c"}
	values := []int64{1, 2, 3}

	builder := NewSegmentBuilder(50, "a", "c")
	builder.SetKeys(keys)

	enc, err := EncodeColumn(common.TypeInt64, values, 3, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	builder.AddEncodedColumn(enc)

	seg, err := builder.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return seg
}
