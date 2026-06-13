package storage

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ---------------------------------------------------------------------------
// EncodeColumn 各类型编码选择测试
// ---------------------------------------------------------------------------

// TestEncodeColumnBoolBitmap 测试 TypeBool 数据使用 Bitmap 编码。
func TestEncodeColumnBoolBitmap(t *testing.T) {
	data := []uint64{1, 0, 1, 1, 0}
	enc, err := EncodeColumn(common.TypeBool, data, 5, nil)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}
	if enc.Encoding != EncodingBitmap {
		t.Errorf("期望 EncodingBitmap，实际 %v", enc.Encoding)
	}
	if enc.RowCount != 5 {
		t.Errorf("期望 RowCount=5，实际 %d", enc.RowCount)
	}

	// 往返解码验证
	decoded, nulls, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}
	bools, ok := decoded.([]uint64)
	if !ok {
		t.Fatalf("期望 []uint64，实际 %T", decoded)
	}
	if len(bools) != 5 {
		t.Fatalf("期望长度 5，实际 %d", len(bools))
	}
	if nulls != nil {
		t.Errorf("期望 nulls 为 nil，实际非 nil")
	}
	for i, v := range data {
		if bools[i] != v {
			t.Errorf("索引 %d: 期望 %d，实际 %d", i, v, bools[i])
		}
	}
}

// TestEncodeColumnStringDict 测试 TypeString 数据使用 Dict 编码。
func TestEncodeColumnStringDict(t *testing.T) {
	data := []string{testStrHello, testStrWorld, testStrHello, testStrFoo}
	enc, err := EncodeColumn(common.TypeString, data, 4, nil)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}
	if enc.Encoding != EncodingDict {
		t.Errorf("期望 EncodingDict，实际 %v", enc.Encoding)
	}
	if enc.RowCount != 4 {
		t.Errorf("期望 RowCount=4，实际 %d", enc.RowCount)
	}
	if len(enc.Dict) != 3 {
		t.Errorf("期望字典大小 3，实际 %d", len(enc.Dict))
	}

	// 往返解码验证
	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}
	strs, ok := decoded.([]string)
	if !ok {
		t.Fatalf("期望 []string，实际 %T", decoded)
	}
	for i, v := range data {
		if strs[i] != v {
			t.Errorf("索引 %d: 期望 %q，实际 %q", i, v, strs[i])
		}
	}
}

// TestEncodeColumnInt64RLE 测试 TypeInt64 数据具有 RLE 模式时使用 RLE 编码。
func TestEncodeColumnInt64RLE(t *testing.T) {
	// 大量重复值，满足 RLE 条件（runCount/rowCount <= 0.5）
	data := make([]int64, 100)
	for i := 0; i < 30; i++ {
		data[i] = 1
	}
	for i := 30; i < 70; i++ {
		data[i] = 2
	}
	for i := 70; i < 100; i++ {
		data[i] = 3
	}

	enc, err := EncodeColumn(common.TypeInt64, data, 100, nil)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}
	if enc.Encoding != EncodingRLE {
		t.Errorf("期望 EncodingRLE，实际 %v", enc.Encoding)
	}

	// 往返解码验证
	decoded, nulls, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}
	ints, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("期望 []int64，实际 %T", decoded)
	}
	if len(ints) != 100 {
		t.Fatalf("期望长度 100，实际 %d", len(ints))
	}
	// decodeRLE 始终返回非 nil 的 nulls 位图，验证没有 null 位被设置
	if nulls != nil {
		for i := uint32(0); i < 100; i++ {
			if nulls.Get(i) {
				t.Errorf("索引 %d 不应为 null", i)
			}
		}
	}
	for i, v := range data {
		if ints[i] != v {
			t.Errorf("索引 %d: 期望 %d，实际 %d", i, v, ints[i])
		}
	}
}

// TestEncodeColumnInt64Plain 测试 TypeInt64 数据无 RLE 模式时使用 Plain 编码。
func TestEncodeColumnInt64Plain(t *testing.T) {
	// 每个值都不同，不满足 RLE 条件
	data := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	enc, err := EncodeColumn(common.TypeInt64, data, 10, nil)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}
	if enc.Encoding != EncodingPlain {
		t.Errorf("期望 EncodingPlain，实际 %v", enc.Encoding)
	}

	// 往返解码验证
	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}
	ints, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("期望 []int64，实际 %T", decoded)
	}
	for i, v := range data {
		if ints[i] != v {
			t.Errorf("索引 %d: 期望 %d，实际 %d", i, v, ints[i])
		}
	}
}

// TestEncodeColumnFloat64Plain 测试 TypeFloat64 数据使用 Plain 编码。
func TestEncodeColumnFloat64Plain(t *testing.T) {
	data := []float64{1.1, 2.2, 3.3, 4.4}
	enc, err := EncodeColumn(common.TypeFloat64, data, 4, nil)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}
	if enc.Encoding != EncodingPlain {
		t.Errorf("期望 EncodingPlain，实际 %v", enc.Encoding)
	}

	// 往返解码验证
	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}
	floats, ok := decoded.([]float64)
	if !ok {
		t.Fatalf("期望 []float64，实际 %T", decoded)
	}
	for i, v := range data {
		if floats[i] != v {
			t.Errorf("索引 %d: 期望 %f，实际 %f", i, v, floats[i])
		}
	}
}

// TestEncodeColumnTimestampPlain 测试 TypeTimestamp 数据使用 Plain 编码。
func TestEncodeColumnTimestampPlain(t *testing.T) {
	data := []int64{1000000, 2000000, 3000000}
	enc, err := EncodeColumn(common.TypeTimestamp, data, 3, nil)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}
	if enc.Encoding != EncodingPlain {
		t.Errorf("期望 EncodingPlain，实际 %v", enc.Encoding)
	}

	// 往返解码验证
	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}
	times, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("期望 []int64，实际 %T", decoded)
	}
	for i, v := range data {
		if times[i] != v {
			t.Errorf("索引 %d: 期望 %d，实际 %d", i, v, times[i])
		}
	}
}

// ---------------------------------------------------------------------------
// EncodeColumn 带 nulls 位图测试
// ---------------------------------------------------------------------------

// TestEncodeColumnWithNulls 测试带 nulls 位图的编码。
func TestEncodeColumnWithNulls(t *testing.T) {
	tests := []struct {
		name     string
		typ      common.DataType
		data     any
		rowCount uint32
		nullIdx  int // 设为 null 的行索引
	}{
		{
			name:     "Int64带null",
			typ:      common.TypeInt64,
			data:     []int64{10, 20, 30},
			rowCount: 3,
			nullIdx:  1,
		},
		{
			name:     "Float64带null",
			typ:      common.TypeFloat64,
			data:     []float64{1.1, 2.2, 3.3},
			rowCount: 3,
			nullIdx:  0,
		},
		{
			name:     "Timestamp带null",
			typ:      common.TypeTimestamp,
			data:     []int64{100, 200, 300},
			rowCount: 3,
			nullIdx:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nulls := common.NewBitmap(tt.rowCount)
			nulls.Set(uint32(tt.nullIdx))

			enc, err := EncodeColumn(tt.typ, tt.data, tt.rowCount, nulls)
			if err != nil {
				t.Fatalf("EncodeColumn 失败: %v", err)
			}
			if len(enc.Nulls) == 0 {
				t.Error("期望 Nulls 非空，实际为空")
			}

			// 解码验证 nulls 位图
			_, decodedNulls, err := DecodeColumn(enc)
			if err != nil {
				t.Fatalf("DecodeColumn 失败: %v", err)
			}
			if decodedNulls == nil {
				t.Fatal("期望解码后 nulls 非 nil")
			}
			if !decodedNulls.Get(uint32(tt.nullIdx)) {
				t.Errorf("期望索引 %d 为 null", tt.nullIdx)
			}
		})
	}
}

// TestEncodeColumnBoolWithNullsV9 测试 Bool 类型带 nulls 位图的编码。
func TestEncodeColumnBoolWithNullsV9(t *testing.T) {
	data := []uint64{1, 0, 1}
	nulls := common.NewBitmap(3)
	nulls.Set(1) // 第二行为 null

	enc, err := EncodeColumn(common.TypeBool, data, 3, nulls)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}
	if enc.Encoding != EncodingBitmap {
		t.Errorf("期望 EncodingBitmap，实际 %v", enc.Encoding)
	}
	if len(enc.Nulls) == 0 {
		t.Error("期望 Nulls 非空")
	}

	decoded, decodedNulls, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}
	bools := decoded.([]uint64)
	if bools[0] != 1 || bools[2] != 1 {
		t.Errorf("解码值不正确: %v", bools)
	}
	if decodedNulls == nil || !decodedNulls.Get(1) {
		t.Error("期望索引 1 为 null")
	}
}

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
