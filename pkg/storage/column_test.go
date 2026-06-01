package storage

import (
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const (
	testNameFloat64   = "Float64"
	testNameBool      = "Bool"
	testNameString    = "String"
	testNameTimestamp = "Timestamp"
)

func TestNewColumnVector(t *testing.T) {
	tests := []struct {
		name     string
		colID    uint32
		typ      common.DataType
		capacity uint32
		wantCap  uint32
	}{
		{"Int64_default_capacity", 1, common.TypeInt64, 0, defaultColumnCapacity},
		{"Int64_custom_capacity", 2, common.TypeInt64, 128, 128},
		{testNameFloat64, 3, common.TypeFloat64, 0, defaultColumnCapacity},
		{testNameBool, 4, common.TypeBool, 0, defaultColumnCapacity},
		{testNameString, 5, common.TypeString, 0, defaultColumnCapacity},
		{testNameTimestamp, 6, common.TypeTimestamp, 0, defaultColumnCapacity},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cv := NewColumnVector(tt.colID, tt.typ, tt.capacity)
			if cv.ColumnID != tt.colID {
				t.Errorf("ColumnID = %d, want %d", cv.ColumnID, tt.colID)
			}
			if cv.Typ != tt.typ {
				t.Errorf("Typ = %v, want %v", cv.Typ, tt.typ)
			}
			if cv.Capacity() != tt.wantCap {
				t.Errorf("Capacity = %d, want %d", cv.Capacity(), tt.wantCap)
			}
			if cv.Len() != 0 {
				t.Errorf("Len = %d, want 0", cv.Len())
			}
		})
	}
}

func TestColumnVectorSetAndGetInt64(t *testing.T) {
	cv := NewColumnVector(1, common.TypeInt64, 16)

	for i := uint32(0); i < 10; i++ {
		cv.SetInt64(i, int64(i*100))
		cv.len++
	}

	for i := uint32(0); i < 10; i++ {
		v := cv.GetValue(i)
		if v.IsNull() {
			t.Errorf("row %d is NULL, want Int64", i)
		}
		if v.Typ != common.TypeInt64 {
			t.Errorf("row %d type = %v, want Int64", i, v.Typ)
		}
		if v.Int64 != int64(i*100) {
			t.Errorf("row %d value = %d, want %d", i, v.Int64, int64(i*100))
		}
	}
}

func TestColumnVectorSetAndGetFloat64(t *testing.T) {
	cv := NewColumnVector(1, common.TypeFloat64, 16)

	for i := uint32(0); i < 10; i++ {
		cv.SetFloat64(i, float64(i)*1.5)
		cv.len++
	}

	for i := uint32(0); i < 10; i++ {
		v := cv.GetValue(i)
		if v.Typ != common.TypeFloat64 {
			t.Errorf("row %d type = %v, want Float64", i, v.Typ)
		}
		if v.Float64 != float64(i)*1.5 {
			t.Errorf("row %d value = %f, want %f", i, v.Float64, float64(i)*1.5)
		}
	}
}

func TestColumnVectorSetAndGetBool(t *testing.T) {
	cv := NewColumnVector(1, common.TypeBool, 16)

	for i := uint32(0); i < 10; i++ {
		cv.SetBool(i, i%2 == 0)
		cv.len++
	}

	for i := uint32(0); i < 10; i++ {
		v := cv.GetValue(i)
		if v.Typ != common.TypeBool {
			t.Errorf("row %d type = %v, want Bool", i, v.Typ)
		}
		expected := i%2 == 0
		got := v.Int64 != 0
		if got != expected {
			t.Errorf("row %d value = %v, want %v", i, got, expected)
		}
	}

	if cv.GetBool(0) != true {
		t.Error("GetBool(0) should be true")
	}
	if cv.GetBool(1) != false {
		t.Error("GetBool(1) should be false")
	}
}

func TestColumnVectorSetAndGetString(t *testing.T) {
	cv := NewColumnVector(1, common.TypeString, 16)

	values := []string{"hello", "world", testStrTest, "", testStrFoo}
	for i, s := range values {
		cv.SetString(uint32(i), s)
		cv.len++
	}

	for i, expected := range values {
		v := cv.GetValue(uint32(i))
		if v.Typ != common.TypeString {
			t.Errorf("row %d type = %v, want String", i, v.Typ)
		}
		if v.Str != expected {
			t.Errorf("row %d value = %q, want %q", i, v.Str, expected)
		}
	}
}

func TestColumnVectorSetAndGetTimestamp(t *testing.T) {
	cv := NewColumnVector(1, common.TypeTimestamp, 16)

	now := time.Now()
	for i := uint32(0); i < 10; i++ {
		cv.SetTimestamp(i, now.Add(time.Duration(i)*time.Hour))
		cv.len++
	}

	for i := uint32(0); i < 10; i++ {
		v := cv.GetValue(i)
		if v.Typ != common.TypeTimestamp {
			t.Errorf("row %d type = %v, want Timestamp", i, v.Typ)
		}
		expected := now.Add(time.Duration(i) * time.Hour)
		if !v.Time.Equal(expected) {
			t.Errorf("row %d value = %v, want %v", i, v.Time, expected)
		}
	}
}

func TestColumnVectorNullHandling(t *testing.T) {
	cv := NewColumnVector(1, common.TypeInt64, 16)

	cv.SetInt64(0, 42)
	cv.len++
	cv.SetInt64(1, 100)
	cv.SetNull(1)
	cv.len++

	if cv.IsNull(0) {
		t.Error("row 0 should not be NULL")
	}
	if !cv.IsNull(1) {
		t.Error("row 1 should be NULL")
	}

	v0 := cv.GetValue(0)
	if v0.IsNull() {
		t.Error("GetValue(0) should not be NULL")
	}
	if v0.Int64 != 42 {
		t.Errorf("GetValue(0) = %d, want 42", v0.Int64)
	}

	v1 := cv.GetValue(1)
	if !v1.IsNull() {
		t.Error("GetValue(1) should be NULL")
	}

	if cv.NullCount() != 1 {
		t.Errorf("NullCount = %d, want 1", cv.NullCount())
	}
}

func TestColumnVectorSetValue(t *testing.T) {
	tests := []struct {
		name  string
		typ   common.DataType
		value common.Value
	}{
		{"Int64", common.TypeInt64, common.NewInt64(42)},
		{testNameFloat64, common.TypeFloat64, common.NewFloat64(3.14)},
		{testNameBool, common.TypeBool, common.NewBool(true)},
		{testNameString, common.TypeString, common.NewString("hello")},
		{testNameTimestamp, common.TypeTimestamp, common.NewTimestamp(time.Now())},
		{"Null", common.TypeInt64, common.NewNull()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cv := NewColumnVector(1, tt.typ, 8)
			cv.len = 1

			if err := cv.SetValue(0, tt.value); err != nil {
				t.Fatalf("SetValue failed: %v", err)
			}

			got := cv.GetValue(0)
			if !got.Equal(tt.value) {
				t.Errorf("GetValue = %v, want %v", got, tt.value)
			}
		})
	}
}

func TestColumnVectorSetValueTypeMismatch(t *testing.T) {
	cv := NewColumnVector(1, common.TypeInt64, 8)
	cv.len = 1

	err := cv.SetValue(0, common.NewString("not int"))
	if err == nil {
		t.Fatal("expected error for type mismatch")
	}
}

func TestColumnVectorAppend(t *testing.T) {
	cv := NewColumnVector(1, common.TypeInt64, 4)

	for i := int64(0); i < 10; i++ {
		if err := cv.Append(common.NewInt64(i)); err != nil {
			t.Fatalf("Append %d failed: %v", i, err)
		}
	}

	if cv.Len() != 10 {
		t.Errorf("Len = %d, want 10", cv.Len())
	}

	if cv.Capacity() < 10 {
		t.Errorf("Capacity = %d, want at least 10 after grow", cv.Capacity())
	}

	for i := uint32(0); i < 10; i++ {
		v := cv.GetValue(i)
		if v.Int64 != int64(i) {
			t.Errorf("row %d = %d, want %d", i, v.Int64, int64(i))
		}
	}
}

func TestColumnVectorAppendTypeMismatch(t *testing.T) {
	cv := NewColumnVector(1, common.TypeInt64, 8)

	err := cv.Append(common.NewString("wrong type"))
	if err == nil {
		t.Fatal("expected error for type mismatch in Append")
	}
}

func TestColumnVectorReset(t *testing.T) {
	cv := NewColumnVector(1, common.TypeInt64, 8)

	for i := 0; i < 5; i++ {
		_ = cv.Append(common.NewInt64(int64(i)))
	}
	cv.SetNull(2)

	cv.Reset()
	if cv.Len() != 0 {
		t.Errorf("Len after Reset = %d, want 0", cv.Len())
	}
	if cv.NullCount() != 0 {
		t.Errorf("NullCount after Reset = %d, want 0", cv.NullCount())
	}
}

func TestColumnVectorNullBitmap(t *testing.T) {
	cv := NewColumnVector(1, common.TypeInt64, 16)
	cv.len = 4
	cv.SetNull(0)
	cv.SetNull(2)

	bm := cv.NullBitmap()
	if bm == nil {
		t.Fatal("NullBitmap returned nil")
	}
	if !bm.Get(0) {
		t.Error("bit 0 should be set")
	}
	if bm.Get(1) {
		t.Error("bit 1 should not be set")
	}
	if !bm.Get(2) {
		t.Error("bit 2 should be set")
	}
}

func TestColumnVectorClearNull(t *testing.T) {
	cv := NewColumnVector(1, common.TypeInt64, 8)
	cv.len = 2
	cv.SetNull(0)
	cv.SetInt64(1, 42)

	cv.ClearNull(0)
	if cv.IsNull(0) {
		t.Error("row 0 should not be NULL after ClearNull")
	}

	v := cv.GetValue(0)
	if v.IsNull() {
		t.Error("GetValue(0) should not be NULL after ClearNull")
	}
}

func TestColumnVectorDataAccessors(t *testing.T) {
	t.Run("Int64Data", func(t *testing.T) {
		cv := NewColumnVector(1, common.TypeInt64, 4)
		cv.len = 2
		cv.SetInt64(0, 10)
		cv.SetInt64(1, 20)
		data := cv.Int64Data()
		if data[0] != 10 || data[1] != 20 {
			t.Errorf("Int64Data = %v, want [10, 20, ...]", data[:2])
		}
	})

	t.Run("Float64Data", func(t *testing.T) {
		cv := NewColumnVector(1, common.TypeFloat64, 4)
		cv.len = 2
		cv.SetFloat64(0, 1.5)
		cv.SetFloat64(1, 2.5)
		data := cv.Float64Data()
		if data[0] != 1.5 || data[1] != 2.5 {
			t.Errorf("Float64Data = %v, want [1.5, 2.5, ...]", data[:2])
		}
	})

	t.Run("StringData", func(t *testing.T) {
		cv := NewColumnVector(1, common.TypeString, 4)
		cv.len = 2
		cv.SetString(0, "a")
		cv.SetString(1, "b")
		data := cv.StringData()
		if data[0] != "a" || data[1] != "b" {
			t.Errorf("StringData = %v, want [a, b, ...]", data[:2])
		}
	})

	t.Run("BoolData", func(t *testing.T) {
		cv := NewColumnVector(1, common.TypeBool, 4)
		cv.len = 2
		cv.SetBool(0, true)
		cv.SetBool(1, false)
		data := cv.BoolData()
		if len(data) == 0 {
			t.Fatal("BoolData should not be empty")
		}
	})

	t.Run("TimeData", func(t *testing.T) {
		cv := NewColumnVector(1, common.TypeTimestamp, 4)
		cv.len = 2
		now := time.Now()
		cv.SetTimestamp(0, now)
		cv.SetTimestamp(1, now.Add(time.Hour))
		data := cv.TimeData()
		if !data[0].Equal(now) {
			t.Errorf("TimeData[0] = %v, want %v", data[0], now)
		}
	})
}

func TestColumnVectorGrowForAllTypes(t *testing.T) {
	tests := []struct {
		name string
		typ  common.DataType
		val  common.Value
	}{
		{testNameBool, common.TypeBool, common.NewBool(true)},
		{testNameFloat64, common.TypeFloat64, common.NewFloat64(1.0)},
		{testNameString, common.TypeString, common.NewString("x")},
		{testNameTimestamp, common.TypeTimestamp, common.NewTimestamp(time.Now())},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cv := NewColumnVector(1, tt.typ, 2)

			for i := 0; i < 5; i++ {
				if err := cv.Append(tt.val); err != nil {
					t.Fatalf("Append %d failed: %v", i, err)
				}
			}

			if cv.Capacity() < 5 {
				t.Errorf("Capacity = %d, want >= 5 after grow", cv.Capacity())
			}
			if cv.Len() != 5 {
				t.Errorf("Len = %d, want 5", cv.Len())
			}
		})
	}
}

func TestColumnVectorGrowPreservesNulls(t *testing.T) {
	cv := NewColumnVector(1, common.TypeInt64, 2)
	cv.len = 2
	cv.SetInt64(0, 10)
	cv.SetInt64(1, 20)
	cv.SetNull(0)

	for i := 0; i < 3; i++ {
		if err := cv.Append(common.NewInt64(int64(i + 100))); err != nil {
			t.Fatalf("Append %d failed: %v", i, err)
		}
	}

	if !cv.IsNull(0) {
		t.Error("row 0 should still be NULL after grow")
	}
	if cv.GetValue(1).Int64 != 20 {
		t.Errorf("row 1 = %d, want 20", cv.GetValue(1).Int64)
	}
	if cv.GetValue(2).Int64 != 100 {
		t.Errorf("row 2 = %d, want 100", cv.GetValue(2).Int64)
	}
}
