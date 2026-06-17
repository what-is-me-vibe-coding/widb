package storage

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestColumnVectorIntFamilyRoundtrip 验证整数族各类型的写入/读取往返一致性。
func TestColumnVectorIntFamilyRoundtrip(t *testing.T) {
	types := []common.DataType{
		common.TypeInt8, common.TypeInt16, common.TypeInt32, common.TypeUint64, common.TypeDate,
	}
	values := []int64{-128, 0, 127, 255, 1000, 19723, -1, 42}
	for _, typ := range types {
		t.Run(typ.String(), func(t *testing.T) {
			cv := NewColumnVector(1, typ, 8)
			fillIntFamilyColumn(t, cv, typ, values)
			verifyIntFamilyColumn(t, cv, typ, values)
		})
	}
}

// fillIntFamilyColumn 向列向量写入整数族测试值。
func fillIntFamilyColumn(t *testing.T, cv *ColumnVector, typ common.DataType, values []int64) {
	t.Helper()
	for i, v := range values {
		if err := cv.SetValue(uint32(i), common.NewIntFamilyValue(typ, v)); err != nil {
			t.Fatalf("SetValue(%d, %d) error: %v", i, v, err)
		}
		cv.len++
	}
	if cv.Len() != uint32(len(values)) {
		t.Fatalf("Len = %d, want %d", cv.Len(), len(values))
	}
}

// verifyIntFamilyColumn 验证列向量中的整数族值与类型标签。
func verifyIntFamilyColumn(t *testing.T, cv *ColumnVector, typ common.DataType, values []int64) {
	t.Helper()
	for i, want := range values {
		got := cv.GetValue(uint32(i))
		if got.Typ != typ {
			t.Errorf("row %d type = %s, want %s", i, got.Typ, typ)
		}
		if got.Int64 != want {
			t.Errorf("row %d value = %d, want %d", i, got.Int64, want)
		}
	}
}

// TestColumnVectorIntFamilyCrossTypeAssign 验证整数族列接受跨类型整数族赋值。
func TestColumnVectorIntFamilyCrossTypeAssign(t *testing.T) {
	cv := NewColumnVector(1, common.TypeInt8, 4)
	// INT8 列接受 INT64 字面量
	if err := cv.SetValue(0, common.NewInt64(42)); err != nil {
		t.Fatalf("SetValue(INT64 42) into INT8 column error: %v", err)
	}
	cv.len++
	// INT8 列接受 DATE 值
	if err := cv.SetValue(1, common.NewDate(100)); err != nil {
		t.Fatalf("SetValue(DATE 100) into INT8 column error: %v", err)
	}
	cv.len++
	// INT8 列接受 UINT64 值
	if err := cv.SetValue(2, common.NewUint64(7)); err != nil {
		t.Fatalf("SetValue(UINT64 7) into INT8 column error: %v", err)
	}
	cv.len++

	// 验证读取时保留列类型标签 INT8
	for i, want := range []int64{42, 100, 7} {
		got := cv.GetValue(uint32(i))
		if got.Typ != common.TypeInt8 {
			t.Errorf("row %d type = %s, want INT8 (列类型应保留)", i, got.Typ)
		}
		if got.Int64 != want {
			t.Errorf("row %d value = %d, want %d", i, got.Int64, want)
		}
	}
}

// TestColumnVectorIntFamilyRejectsNonInt 验证整数族列拒绝非整数族类型。
func TestColumnVectorIntFamilyRejectsNonInt(t *testing.T) {
	cv := NewColumnVector(1, common.TypeInt8, 2)
	// STRING 不应被接受
	if err := cv.SetValue(0, common.NewString("abc")); err == nil {
		t.Error("SetValue(STRING) into INT8 column: expected error, got nil")
	}
	// FLOAT64 不应被接受
	if err := cv.SetValue(0, common.NewFloat64(1.5)); err == nil {
		t.Error("SetValue(FLOAT64) into INT8 column: expected error, got nil")
	}
	// BOOL 不应被接受
	if err := cv.SetValue(0, common.NewBool(true)); err == nil {
		t.Error("SetValue(BOOL) into INT8 column: expected error, got nil")
	}
}

// TestColumnVectorIntFamilySlice 验证整数族列的 Slice 操作。
func TestColumnVectorIntFamilySlice(t *testing.T) {
	cv := NewColumnVector(1, common.TypeInt16, 8)
	for i := 0; i < 5; i++ {
		if err := cv.SetValue(uint32(i), common.NewInt16(int64(i*10))); err != nil {
			t.Fatalf("SetValue(%d) error: %v", i, err)
		}
		cv.len++
	}
	slice, err := cv.Slice(1, 4) // 取索引 1,2,3
	if err != nil {
		t.Fatalf("Slice error: %v", err)
	}
	if slice.Len() != 3 {
		t.Fatalf("Slice Len = %d, want 3", slice.Len())
	}
	for i := uint32(0); i < 3; i++ {
		got := slice.GetValue(i)
		want := int64((i + 1) * 10)
		if got.Int64 != want {
			t.Errorf("Slice row %d = %d, want %d", i, got.Int64, want)
		}
		if got.Typ != common.TypeInt16 {
			t.Errorf("Slice row %d type = %s, want INT16", i, got.Typ)
		}
	}
}

// TestColumnVectorIntFamilyAppend 验证整数族列的 Append 操作。
func TestColumnVectorIntFamilyAppend(t *testing.T) {
	cv := NewColumnVector(1, common.TypeUint64, 2)
	for i := 0; i < 5; i++ {
		if err := cv.Append(common.NewUint64(int64(i))); err != nil {
			t.Fatalf("Append(%d) error: %v", i, err)
		}
	}
	if cv.Len() != 5 {
		t.Fatalf("Len = %d, want 5", cv.Len())
	}
	for i := uint32(0); i < 5; i++ {
		got := cv.GetValue(i)
		if got.Int64 != int64(i) {
			t.Errorf("row %d = %d, want %d", i, got.Int64, int64(i))
		}
	}
}
