package storage

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ---------------------------------------------------------------------------
// EncodeColumn 空数据测试
// ---------------------------------------------------------------------------

// TestEncodeColumnEmptyData 测试 rowCount=0 的编码。
func TestEncodeColumnEmptyData(t *testing.T) {
	enc, err := EncodeColumn(common.TypeInt64, []int64{}, 0, nil)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}
	if enc.RowCount != 0 {
		t.Errorf("期望 RowCount=0，实际 %d", enc.RowCount)
	}
	if enc.Encoding != EncodingPlain {
		t.Errorf("期望 EncodingPlain，实际 %v", enc.Encoding)
	}
}

// ---------------------------------------------------------------------------
// 往返编解码综合测试
// ---------------------------------------------------------------------------

// TestEncodeDecodeRoundTrip 测试所有类型的往返编解码。
func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Run("Bool", func(t *testing.T) {
		roundTripBool(t, []uint64{1, 0, 1, 1, 0, 1}, 6)
	})
	t.Run("Int64Plain", func(t *testing.T) {
		roundTripInt64(t, []int64{100, 200, 300}, 3)
	})
	t.Run("Int64RLE", func(t *testing.T) {
		roundTripInt64(t, []int64{5, 5, 5, 5, 5, 10, 10, 10}, 8)
	})
	t.Run("Float64", func(t *testing.T) {
		roundTripFloat64(t, []float64{1.5, 2.5, 3.5}, 3)
	})
	t.Run("String", func(t *testing.T) {
		roundTripString(t, []string{testStrAlpha, testStrBeta, testStrAlpha}, 3)
	})
	t.Run("Timestamp", func(t *testing.T) {
		roundTripInt64(t, []int64{1000, 2000, 3000}, 3)
	})
}

func roundTripBool(t *testing.T, data []uint64, rowCount uint32) {
	t.Helper()
	enc, err := EncodeColumn(common.TypeBool, data, rowCount, nil)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}
	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}
	result := decoded.([]uint64)
	for i, v := range data {
		if result[i] != v {
			t.Errorf("索引 %d: 期望 %d，实际 %d", i, v, result[i])
		}
	}
}

func roundTripInt64(t *testing.T, data []int64, rowCount uint32) {
	t.Helper()
	enc, err := EncodeColumn(common.TypeInt64, data, rowCount, nil)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}
	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}
	result := decoded.([]int64)
	for i, v := range data {
		if result[i] != v {
			t.Errorf("索引 %d: 期望 %d，实际 %d", i, v, result[i])
		}
	}
}

func roundTripFloat64(t *testing.T, data []float64, rowCount uint32) {
	t.Helper()
	enc, err := EncodeColumn(common.TypeFloat64, data, rowCount, nil)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}
	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}
	result := decoded.([]float64)
	for i, v := range data {
		if result[i] != v {
			t.Errorf("索引 %d: 期望 %f，实际 %f", i, v, result[i])
		}
	}
}

func roundTripString(t *testing.T, data []string, rowCount uint32) {
	t.Helper()
	enc, err := EncodeColumn(common.TypeString, data, rowCount, nil)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}
	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}
	result := decoded.([]string)
	for i, v := range data {
		if result[i] != v {
			t.Errorf("索引 %d: 期望 %q，实际 %q", i, v, result[i])
		}
	}
}

// ---------------------------------------------------------------------------
// EncodeColumn 未知编码类型测试
// ---------------------------------------------------------------------------

// TestEncodeColumnUnknownEncodingType 测试 EncodeColumn 对未知编码类型的处理。
// 通过构造 EncodingType 为未知值来测试 DecodeColumn 的 default 分支。
func TestEncodeColumnUnknownEncodingType(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingType(99),
		Type:     common.TypeInt64,
		RowCount: 1,
		Data:     make([]byte, 8),
	}
	_, _, err := DecodeColumn(enc)
	if err == nil {
		t.Fatal("期望错误，实际返回 nil")
	}
}

// TestEncodeColumnStringWithNullsDict 测试 String 类型带 nulls 的 Dict 编码。
func TestEncodeColumnStringWithNullsDict(t *testing.T) {
	data := []string{testStrHello, testStrWorld, testStrFoo}
	nulls := common.NewBitmap(3)
	nulls.Set(1) // 第二行为 null

	enc, err := EncodeColumn(common.TypeString, data, 3, nulls)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}
	if enc.Encoding != EncodingDict {
		t.Errorf("期望 EncodingDict，实际 %v", enc.Encoding)
	}

	// 往返解码
	decoded, decodedNulls, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}
	strs := decoded.([]string)
	if decodedNulls == nil || !decodedNulls.Get(1) {
		t.Error("期望索引 1 为 null")
	}
	if strs[0] != testStrHello {
		t.Errorf("索引 0: 期望 %q，实际 %q", testStrHello, strs[0])
	}
	if strs[2] != testStrFoo {
		t.Errorf("索引 2: 期望 %q，实际 %q", testStrFoo, strs[2])
	}
}

// TestEncodeColumnInt64RLEWithNulls 测试 Int64 RLE 编码带 nulls 位图。
func TestEncodeColumnInt64RLEWithNulls(t *testing.T) {
	// 大量重复值触发 RLE
	data := make([]int64, 20)
	for i := 0; i < 10; i++ {
		data[i] = 5
	}
	for i := 10; i < 20; i++ {
		data[i] = 10
	}
	nulls := common.NewBitmap(20)
	nulls.Set(5)  // 第 6 行为 null
	nulls.Set(15) // 第 16 行为 null

	enc, err := EncodeColumn(common.TypeInt64, data, 20, nulls)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}
	if enc.Encoding != EncodingRLE {
		t.Errorf("期望 EncodingRLE，实际 %v", enc.Encoding)
	}

	// 往返解码
	decoded, decodedNulls, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}
	ints := decoded.([]int64)
	if len(ints) != 20 {
		t.Fatalf("期望长度 20，实际 %d", len(ints))
	}
	if decodedNulls == nil {
		t.Fatal("期望 nulls 非 nil")
	}
	if !decodedNulls.Get(5) {
		t.Error("期望索引 5 为 null")
	}
	if !decodedNulls.Get(15) {
		t.Error("期望索引 15 为 null")
	}
}
