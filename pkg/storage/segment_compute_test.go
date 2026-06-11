package storage

import (
	"encoding/binary"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestComputeRLEMinMax 测试 computeRLEMinMax 函数的 RLE 编码列统计信息计算
func TestComputeRLEMinMax(t *testing.T) {
	tests := []struct {
		name     string
		values   []int64
		rowCount uint32
		nulls    *common.Bitmap
		wantMin  int64
		wantMax  int64
	}{
		{
			name:     "基本RLE数据-单一run",
			values:   []int64{5, 5, 5, 5},
			rowCount: 4,
			wantMin:  5,
			wantMax:  5,
		},
		{
			name:     "基本RLE数据-多个run",
			values:   []int64{1, 1, 3, 3, 2, 2},
			rowCount: 6,
			wantMin:  1,
			wantMax:  3,
		},
		{
			name:     "负数RLE数据",
			values:   []int64{-10, -10, -5, -5, -3, -3},
			rowCount: 6,
			wantMin:  -10,
			wantMax:  -3,
		},
		{
			name:     "混合正负数",
			values:   []int64{-5, -5, 0, 0, 10, 10},
			rowCount: 6,
			wantMin:  -5,
			wantMax:  10,
		},
		{
			name:     "带null的RLE数据",
			values:   []int64{1, 1, 3, 3},
			rowCount: 4,
			nulls:    func() *common.Bitmap { bm := common.NewBitmap(4); bm.Set(2); return bm }(),
			// null 值不参与 min/max 计算，仅非 null 值 1 和 3
			wantMin: 1,
			wantMax: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 使用 EncodeColumn 生成 RLE 编码的 EncodedColumn
			enc, err := EncodeColumn(common.TypeInt64, tt.values, tt.rowCount, tt.nulls)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			// 确认是 RLE 编码
			if enc.Encoding != EncodingRLE {
				t.Fatalf("expected RLE encoding, got %v", enc.Encoding)
			}

			stat := computeColumnStat(enc)
			if stat.Min == nil || stat.Max == nil {
				t.Fatalf("expected non-nil min/max, got min=%v max=%v", stat.Min, stat.Max)
			}
			gotMin := int64(binary.LittleEndian.Uint64(stat.Min))
			gotMax := int64(binary.LittleEndian.Uint64(stat.Max))
			if gotMin != tt.wantMin {
				t.Errorf("min: got %d, want %d", gotMin, tt.wantMin)
			}
			if gotMax != tt.wantMax {
				t.Errorf("max: got %d, want %d", gotMax, tt.wantMax)
			}
		})
	}
}

// TestComputeRLEMinMaxEmptyData 测试 computeRLEMinMax 对空数据的处理
func TestComputeRLEMinMaxEmptyData(t *testing.T) {
	// 构造一个空的 RLE 编码列（RowCount=0, Data 为空）
	enc := &EncodedColumn{
		Encoding: EncodingRLE,
		Type:     common.TypeInt64,
		RowCount: 0,
		Data:     nil,
	}
	stat := ColumnStat{}
	computeRLEMinMax(enc, &stat)
	// 空数据不应设置 min/max
	if stat.Min != nil || stat.Max != nil {
		t.Errorf("expected nil min/max for empty RLE data, got min=%v max=%v", stat.Min, stat.Max)
	}
}

// TestComputeRLEMinMaxInvalidData 测试 computeRLEMinMax 对损坏数据的处理
func TestComputeRLEMinMaxInvalidData(t *testing.T) {
	// 构造一个 RLE 编码列但 Data 不是有效的 RLE 格式
	// 当 Data 长度不足 16 字节时，runCount=0，decodeRLE 返回全零数组
	enc := &EncodedColumn{
		Encoding: EncodingRLE,
		Type:     common.TypeInt64,
		RowCount: 3,
		Data:     []byte{0x01, 0x02, 0x03}, // 无效的 RLE 数据（长度不足一个 run）
	}
	stat := ColumnStat{}
	computeRLEMinMax(enc, &stat)
	// 损坏数据解码后产生全零数组，min/max 均为 0
	if stat.Min == nil || stat.Max == nil {
		t.Fatalf("expected non-nil min/max for invalid RLE data with RowCount>0, got min=%v max=%v", stat.Min, stat.Max)
	}
	gotMin := int64(binary.LittleEndian.Uint64(stat.Min))
	gotMax := int64(binary.LittleEndian.Uint64(stat.Max))
	if gotMin != 0 || gotMax != 0 {
		t.Errorf("expected min=0 max=0 for invalid RLE data, got min=%d max=%d", gotMin, gotMax)
	}
}

// TestComputeStringStats 测试 computeStringStats 函数
// 注意：computeStringStats 只在 Plain 编码的字符串列中被调用。
// EncodeColumn 对字符串默认使用 Dict 编码，因此需要手动构造 Plain 编码的 EncodedColumn。
func TestComputeStringStats(t *testing.T) {
	tests := []struct {
		name     string
		strs     []string
		rowCount uint32
		nulls    *common.Bitmap
		wantMin  string
		wantMax  string
	}{
		{
			name: "基本字符串统计", strs: []string{testStrBanana, testStrApple, testStrCherry},
			rowCount: 3,
			wantMin:  testStrApple,
			wantMax:  testStrCherry,
		},
		{
			name: "单字符串", strs: []string{testStrHello},
			rowCount: 1,
			wantMin:  testStrHello,
			wantMax:  testStrHello,
		},
		{
			name: "相同字符串", strs: []string{testStrSame, testStrSame, testStrSame},
			rowCount: 3,
			wantMin:  testStrSame,
			wantMax:  testStrSame,
		},
		{
			name: "带null的字符串统计", strs: []string{"zebra", testStrAlpha, "middle"},
			rowCount: 3,
			nulls:    func() *common.Bitmap { bm := common.NewBitmap(3); bm.Set(1); return bm }(),
			wantMin:  "middle", wantMax: "zebra",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := encodePlain(common.TypeString, tt.strs, tt.rowCount, tt.nulls)
			if err != nil {
				t.Fatalf("encodePlain: %v", err)
			}
			stat := computeColumnStat(enc)
			if stat.Min == nil || stat.Max == nil {
				t.Fatalf("expected non-nil min/max, got min=%v max=%v", stat.Min, stat.Max)
			}
			if gotMin := string(stat.Min); gotMin != tt.wantMin {
				t.Errorf("min: got %q, want %q", gotMin, tt.wantMin)
			}
			if gotMax := string(stat.Max); gotMax != tt.wantMax {
				t.Errorf("max: got %q, want %q", gotMax, tt.wantMax)
			}
		})
	}
}

func TestComputeStringStats_AllNull(t *testing.T) {
	strs := []string{"a", "b", "c"}
	nulls := func() *common.Bitmap { bm := common.NewBitmap(3); bm.Set(0); bm.Set(1); bm.Set(2); return bm }()
	enc, err := encodePlain(common.TypeString, strs, 3, nulls)
	if err != nil {
		t.Fatalf("encodePlain: %v", err)
	}
	stat := computeColumnStat(enc)
	if stat.Min != nil || stat.Max != nil {
		t.Errorf("expected nil min/max for all-null, got min=%v max=%v", stat.Min, stat.Max)
	}
}

func TestComputeStringStats_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		strs    []string
		wantMin string
		wantMax string
	}{
		{name: "空字符串", strs: []string{"", "a", "z"}, wantMin: "", wantMax: "z"},
		{name: "中文字符串", strs: []string{"中文", "English", "日本語"}, wantMin: "English", wantMax: "日本語"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := encodePlain(common.TypeString, tt.strs, uint32(len(tt.strs)), nil)
			if err != nil {
				t.Fatalf("encodePlain: %v", err)
			}
			stat := computeColumnStat(enc)
			if stat.Min == nil || stat.Max == nil {
				t.Fatalf("expected non-nil min/max, got min=%v max=%v", stat.Min, stat.Max)
			}
			if gotMin := string(stat.Min); gotMin != tt.wantMin {
				t.Errorf("min: got %q, want %q", gotMin, tt.wantMin)
			}
			if gotMax := string(stat.Max); gotMax != tt.wantMax {
				t.Errorf("max: got %q, want %q", gotMax, tt.wantMax)
			}
		})
	}
}

// TestComputeColumnStatBitmap 测试 computeColumnStat 对 Bitmap 编码的统计
func TestComputeColumnStatBitmap(t *testing.T) {
	bools := []uint64{1, 0, 1, 0}
	enc, err := EncodeColumn(common.TypeBool, bools, 4, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	stat := computeColumnStat(enc)
	if stat.Min == nil || stat.Max == nil {
		t.Fatalf("expected non-nil min/max for bitmap, got min=%v max=%v", stat.Min, stat.Max)
	}
	if stat.Min[0] != 0 || stat.Max[0] != 1 {
		t.Errorf("expected min=0 max=1, got min=%d max=%d", stat.Min[0], stat.Max[0])
	}
}

// TestComputeColumnStatBitmapAllNulls 测试 Bitmap 编码全为 null 的情况
func TestComputeColumnStatBitmapAllNulls(t *testing.T) {
	bools := []uint64{1, 0}
	nulls := common.NewBitmap(2)
	nulls.Set(0)
	nulls.Set(1)

	enc, err := EncodeColumn(common.TypeBool, bools, 2, nulls)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	stat := computeColumnStat(enc)
	// 全 null 时 NullCount == RowCount，min/max 不应设置
	if stat.NullCount != 2 {
		t.Errorf("expected NullCount=2, got %d", stat.NullCount)
	}
	if stat.Min != nil || stat.Max != nil {
		t.Errorf("expected nil min/max for all-null bitmap, got min=%v max=%v", stat.Min, stat.Max)
	}
}

// TestComputeColumnStatDict 测试 computeColumnStat 对 Dict 编码的统计
func TestComputeColumnStatDict(t *testing.T) {
	strs := []string{testStrCherry, testStrApple, testStrBanana}
	enc, err := EncodeColumn(common.TypeString, strs, 3, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if enc.Encoding != EncodingDict {
		t.Fatalf("expected Dict encoding, got %v", enc.Encoding)
	}

	stat := computeColumnStat(enc)
	if stat.Min == nil || stat.Max == nil {
		t.Fatalf("expected non-nil min/max for dict, got min=%v max=%v", stat.Min, stat.Max)
	}
	gotMin := string(stat.Min)
	gotMax := string(stat.Max)
	// Dict 编码的 min/max 基于 Dict 数组的首尾元素
	if gotMin != enc.Dict[0] {
		t.Errorf("min: got %q, want %q", gotMin, enc.Dict[0])
	}
	if gotMax != enc.Dict[len(enc.Dict)-1] {
		t.Errorf("max: got %q, want %q", gotMax, enc.Dict[len(enc.Dict)-1])
	}
}

// TestComputeColumnStatDictEmpty 测试 Dict 编码空字典的情况
func TestComputeColumnStatDictEmpty(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingDict,
		Type:     common.TypeString,
		RowCount: 0,
		Dict:     []string{},
	}
	stat := computeColumnStat(enc)
	if stat.Min != nil || stat.Max != nil {
		t.Errorf("expected nil min/max for empty dict, got min=%v max=%v", stat.Min, stat.Max)
	}
}
