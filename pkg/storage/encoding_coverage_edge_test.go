package storage

import (
	"bytes"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const crCol1 = "col1"
const crCol0 = "col0"
const crCol = "col"

// --- Compress/Decompress 测试 ---

// TestCompressEmptyReturnsNil_V7 测试 Compress 对空数据返回 nil, nil。
func TestCompressEmptyReturnsNil_V7(t *testing.T) {
	result, err := Compress([]byte{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if result != nil {
		t.Errorf("期望 nil，实际 %d 字节", len(result))
	}
}

// TestCompressColumnNil_V7 测试 CompressColumn 对 nil EncodedColumn 返回错误。
func TestCompressColumnNil_V7(t *testing.T) {
	err := CompressColumn(nil)
	if err == nil {
		t.Fatal("期望错误，实际返回 nil")
	}
}

// TestDecompressColumnNil_V7 测试 DecompressColumn 对 nil EncodedColumn 返回错误。
func TestDecompressColumnNil_V7(t *testing.T) {
	err := DecompressColumn(nil)
	if err == nil {
		t.Fatal("期望错误，实际返回 nil")
	}
}

// TestDecompressColumnInvalidData_V7 测试 DecompressColumn 对无效压缩数据返回错误。
func TestDecompressColumnInvalidData_V7(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     0,
		RowCount: 1,
		Data:     []byte{0xFF, 0xFE, 0xFD, 0xFC}, // 无效的 zstd 数据
	}
	err := DecompressColumn(enc)
	if err == nil {
		t.Fatal("期望解压错误，实际返回 nil")
	}
}

// TestCompressDecompressRoundTripSmall_V7 测试小数据的压缩/解压往返。
func TestCompressDecompressRoundTripSmall_V7(t *testing.T) {
	data := []byte("hello zstd")
	compressed, err := Compress(data)
	if err != nil {
		t.Fatalf("Compress 失败: %v", err)
	}
	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress 失败: %v", err)
	}
	if !bytes.Equal(decompressed, data) {
		t.Errorf("往返不匹配: 期望 %q, 实际 %q", string(data), string(decompressed))
	}
}

// TestCompressDecompressRoundTripMedium_V7 测试中等大小数据的压缩/解压往返。
func TestCompressDecompressRoundTripMedium_V7(t *testing.T) {
	data := bytes.Repeat([]byte("medium data block "), 500)
	compressed, err := Compress(data)
	if err != nil {
		t.Fatalf("Compress 失败: %v", err)
	}
	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress 失败: %v", err)
	}
	if !bytes.Equal(decompressed, data) {
		t.Errorf("往返不匹配: 长度 期望 %d, 实际 %d", len(data), len(decompressed))
	}
}

// TestEncoderDecoderPoolReuse_V7 测试编码器/解码器池的复用。
func TestEncoderDecoderPoolReuse_V7(t *testing.T) {
	// 第一次压缩/解压：创建新的编码器/解码器
	data1 := []byte("first compression")
	compressed1, err := Compress(data1)
	if err != nil {
		t.Fatalf("第一次 Compress 失败: %v", err)
	}
	_, err = Decompress(compressed1)
	if err != nil {
		t.Fatalf("第一次 Decompress 失败: %v", err)
	}

	// 第二次压缩/解压：应从池中复用编码器/解码器
	data2 := []byte("second compression test data")
	compressed2, err := Compress(data2)
	if err != nil {
		t.Fatalf("第二次 Compress 失败: %v", err)
	}
	decompressed2, err := Decompress(compressed2)
	if err != nil {
		t.Fatalf("第二次 Decompress 失败: %v", err)
	}
	if !bytes.Equal(decompressed2, data2) {
		t.Errorf("池复用后往返不匹配")
	}
}

// TestDecompressInvalidZstdData_V7 测试 Decompress 对无效 zstd 数据返回错误。
func TestDecompressInvalidZstdData_V7(t *testing.T) {
	_, err := Decompress([]byte{0x00, 0x01, 0x02, 0x03})
	if err == nil {
		t.Fatal("期望解压错误，实际返回 nil")
	}
}

// TestCompressDecompressSingleByte_V7 测试单字节数据的压缩/解压。
func TestCompressDecompressSingleByte_V7(t *testing.T) {
	data := []byte{0x42}
	compressed, err := Compress(data)
	if err != nil {
		t.Fatalf("Compress 失败: %v", err)
	}
	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress 失败: %v", err)
	}
	if !bytes.Equal(decompressed, data) {
		t.Errorf("单字节往返不匹配: 期望 %v, 实际 %v", data, decompressed)
	}
}

// TestCompressColumnWithEmptyData_V7 测试 CompressColumn 对空数据列的处理。
func TestCompressColumnWithEmptyData_V7(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     0,
		RowCount: 0,
		Data:     []byte{},
	}
	err := CompressColumn(enc)
	if err != nil {
		t.Fatalf("CompressColumn 空数据不应报错: %v", err)
	}
	// 空数据压缩后 Data 应为 nil
	if enc.Data != nil {
		t.Errorf("期望 Data 为 nil，实际 %d 字节", len(enc.Data))
	}
}

// --- Encoding 错误路径测试 ---

// TestEncodingTypeStringUnknown_V7 测试 EncodingType.String() 对未知类型的输出。
func TestEncodingTypeStringUnknown_V7(t *testing.T) {
	unknown := EncodingType(42)
	got := unknown.String()
	want := "Unknown(42)"
	if got != want {
		t.Errorf("期望 %q, 实际 %q", want, got)
	}
}

// TestDecodeColumnUnknownEncoding_V7 测试 DecodeColumn 对未知编码类型返回错误。
func TestDecodeColumnUnknownEncoding_V7(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingType(99),
		Type:     common.TypeInt64,
		RowCount: 1,
		Data:     []byte{0, 0, 0, 0, 0, 0, 0, 0},
	}
	_, _, err := DecodeColumn(enc)
	if err == nil {
		t.Fatal("期望错误，实际返回 nil")
	}
}

// TestEncodeColumnUnknownEncoding_V7 测试 EncodeColumn 的 default 分支。
// selectEncoding 对 TypeNull 返回 EncodingPlain，但 encodePlain 对 TypeNull 返回错误。
func TestEncodeColumnUnknownEncoding_V7(t *testing.T) {
	_, err := EncodeColumn(common.TypeNull, nil, 1, nil)
	if err == nil {
		t.Fatal("期望错误，实际返回 nil")
	}
}

// TestEncodePlainUnsupportedType_V7 测试 encodePlain 对不支持类型的错误路径。
func TestEncodePlainUnsupportedType_V7(t *testing.T) {
	_, err := encodePlain(common.DataType(99), nil, 1, nil)
	if err == nil {
		t.Fatal("期望错误，实际返回 nil")
	}
}

// TestEncodeDictNonStringType_V7 测试 encodeDict 对非字符串类型的错误路径。
func TestEncodeDictNonStringType_V7(t *testing.T) {
	_, err := encodeDict(common.TypeInt64, []int64{1, 2}, 2, nil)
	if err == nil {
		t.Fatal("期望错误，实际返回 nil")
	}
}

// TestEncodeRLENonInt64Type_V7 测试 encodeRLE 对非 int64 类型的错误路径。
func TestEncodeRLENonInt64Type_V7(t *testing.T) {
	_, err := encodeRLE(common.TypeString, []string{"a"}, 1, nil)
	if err == nil {
		t.Fatal("期望错误，实际返回 nil")
	}
}

// TestEncodeBitmapWrongDataType_V7 测试 encodeBitmap 对错误数据类型的处理。
func TestEncodeBitmapWrongDataType_V7(t *testing.T) {
	_, err := encodeBitmap([]int64{1, 0}, 2, nil)
	if err == nil {
		t.Fatal("期望错误，实际返回 nil")
	}
}

// TestDecodePlainUnsupportedType_V7 测试 decodePlain 对不支持类型的错误路径。
func TestDecodePlainUnsupportedType_V7(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.DataType(99),
		RowCount: 1,
		Data:     []byte{0, 0, 0, 0, 0, 0, 0, 0},
	}
	_, _, err := decodePlain(enc)
	if err == nil {
		t.Fatal("期望错误，实际返回 nil")
	}
}

// TestDecodeDictOutOfRangeIndex_V7 测试 decodeDict 对越界索引的错误路径。
func TestDecodeDictOutOfRangeIndex_V7(t *testing.T) {
	// 构造一个字典编码列，其中索引指向不存在的字典条目
	enc := &EncodedColumn{
		Encoding: EncodingDict,
		Type:     common.TypeString,
		RowCount: 1,
		Data:     []byte{0x02}, // 索引 2，但字典只有 1 个条目
		Dict:     []string{testStrHello},
	}
	_, _, err := DecodeColumn(enc)
	if err == nil {
		t.Fatal("期望越界索引错误，实际返回 nil")
	}
}

// TestReadIndexDefaultWidth_V7 测试 readIndex 对无效 width 的默认返回值。
func TestReadIndexDefaultWidth_V7(t *testing.T) {
	buf := []byte{0xAB, 0xCD, 0xEF, 0x01}
	result := readIndex(buf, 0, 3) // width=3 不在 1/2/4 中
	if result != 0 {
		t.Errorf("期望 0，实际 %d", result)
	}
}

// TestSelectEncodingFloat64_V7 测试 selectEncoding 对 float64 类型返回 Plain。
func TestSelectEncodingFloat64_V7(t *testing.T) {
	enc := selectEncoding(common.TypeFloat64, nil, 10)
	if enc != EncodingPlain {
		t.Errorf("期望 EncodingPlain，实际 %v", enc)
	}
}

// TestSelectEncodingTimestamp_V7 测试 selectEncoding 对 timestamp 类型返回 Plain。
func TestSelectEncodingTimestamp_V7(t *testing.T) {
	enc := selectEncoding(common.TypeTimestamp, nil, 10)
	if enc != EncodingPlain {
		t.Errorf("期望 EncodingPlain，实际 %v", enc)
	}
}

// TestIsRLEInt64ShortData_V7 测试 isRLEInt64 对少于 2 个元素的数据返回 false。
func TestIsRLEInt64ShortData_V7(t *testing.T) {
	if isRLEInt64([]int64{1}, 1) {
		t.Error("期望 false，实际 true")
	}
	if isRLEInt64(nil, 0) {
		t.Error("期望 false，实际 true")
	}
}

// TestIsRLEInt64WrongType_V7 测试 isRLEInt64 对非 []int64 类型返回 false。
func TestIsRLEInt64WrongType_V7(t *testing.T) {
	if isRLEInt64("not ints", 1) {
		t.Error("期望 false，实际 true")
	}
}

// TestEncodeDictWithNulls_V7 测试 encodeDict 带 null 值的编码。
func TestEncodeDictWithNulls_V7(t *testing.T) {
	strs := []string{testStrHello, "world", testStrHello}
	nulls := common.NewBitmap(3)
	nulls.Set(1) // 第二行为 null

	enc, err := encodeDict(common.TypeString, strs, 3, nulls)
	if err != nil {
		t.Fatalf("encodeDict 失败: %v", err)
	}
	if enc.Encoding != EncodingDict {
		t.Errorf("期望 EncodingDict，实际 %v", enc.Encoding)
	}
}

// TestEncodeRLEWithNulls_V7 测试 encodeRLE 带 null 值的编码。
func TestEncodeRLEWithNulls_V7(t *testing.T) {
	ints := []int64{1, 1, 1, 2, 2}
	nulls := common.NewBitmap(5)
	nulls.Set(2) // 第三行为 null

	enc, err := encodeRLE(common.TypeInt64, ints, 5, nulls)
	if err != nil {
		t.Fatalf("encodeRLE 失败: %v", err)
	}
	if enc.Encoding != EncodingRLE {
		t.Errorf("期望 EncodingRLE，实际 %v", enc.Encoding)
	}
}

// --- encodePlain 错误路径补充测试 ---

// TestEncodePlainUnsupportedDataType 测试编码不支持的类型时的错误
func TestEncodePlainUnsupportedDataType(t *testing.T) {
	_, err := encodePlain(common.DataType(99), nil, 1, nil)
	if err == nil {
		t.Fatal("expected error for unsupported type in encodePlain, got nil")
	}
}

// TestEncodePlainInt64TypeMismatch 测试编码时 int64 数据类型不匹配的错误
func TestEncodePlainInt64TypeMismatch(t *testing.T) {
	_, err := encodePlain(common.TypeInt64, []string{"not_int"}, 1, nil)
	if err == nil {
		t.Fatal("expected error for type mismatch in encodePlain, got nil")
	}
}

// TestEncodePlainFloat64TypeMismatch 测试 float64 类型不匹配
func TestEncodePlainFloat64TypeMismatch(t *testing.T) {
	_, err := encodePlain(common.TypeFloat64, []string{"not_float"}, 1, nil)
	if err == nil {
		t.Fatal("expected error for float type mismatch, got nil")
	}
}

// TestEncodePlainTimestampTypeMismatch 测试 timestamp 类型不匹配
func TestEncodePlainTimestampTypeMismatch(t *testing.T) {
	_, err := encodePlain(common.TypeTimestamp, []string{"not_timestamp"}, 1, nil)
	if err == nil {
		t.Fatal("expected error for timestamp type mismatch, got nil")
	}
}

// TestEncodePlainStringTypeMismatch 测试 string 类型不匹配
func TestEncodePlainStringTypeMismatch(t *testing.T) {
	_, err := encodePlain(common.TypeString, []int64{1, 2}, 2, nil)
	if err == nil {
		t.Fatal("expected error for string type mismatch, got nil")
	}
}

// --- encodeDict/encodeRLE 错误路径补充测试 ---

// TestEncodeDictNonStringType 测试字典编码非字符串类型时的错误
func TestEncodeDictNonStringType(t *testing.T) {
	_, err := encodeDict(common.TypeInt64, []int64{1, 2}, 2, nil)
	if err == nil {
		t.Fatal("expected error for non-string type in encodeDict, got nil")
	}
}

// TestEncodeDictTypeMismatch 测试字典编码数据类型不匹配
func TestEncodeDictTypeMismatch(t *testing.T) {
	_, err := encodeDict(common.TypeString, []int64{1, 2}, 2, nil)
	if err == nil {
		t.Fatal("expected error for type mismatch in encodeDict, got nil")
	}
}

// TestEncodeRLENonInt64Type 测试 RLE 编码非 int64 类型时的错误
func TestEncodeRLENonInt64Type(t *testing.T) {
	_, err := encodeRLE(common.TypeString, []string{"a", "b"}, 2, nil)
	if err == nil {
		t.Fatal("expected error for non-int64 type in encodeRLE, got nil")
	}
}

// TestEncodeRLETypeMismatch 测试 RLE 编码数据类型不匹配
func TestEncodeRLETypeMismatch(t *testing.T) {
	_, err := encodeRLE(common.TypeInt64, []string{"not_int"}, 1, nil)
	if err == nil {
		t.Fatal("expected error for type mismatch in encodeRLE, got nil")
	}
}

// TestEncodeBitmapTypeMismatch 测试 bitmap 编码数据类型不匹配
func TestEncodeBitmapTypeMismatch(t *testing.T) {
	_, err := encodeBitmap([]int64{1, 2}, 2, nil)
	if err == nil {
		t.Fatal("expected error for type mismatch in encodeBitmap, got nil")
	}
}

// --- extractValue 默认分支测试 ---

// TestExtractValueUnknownDataType 测试 extractValue 对未知类型的处理
func TestExtractValueUnknownDataType(t *testing.T) {
	cd := decodedColumn{data: nil, nulls: nil, typ: common.DataType(99)}
	val := extractValue(cd, 0)
	if val.Valid {
		t.Error("expected null value for unknown type, got valid value")
	}
}

// --- readIndex 默认分支测试 ---

// TestReadIndexInvalidWidthValue 测试 readIndex 对无效 width 的处理
func TestReadIndexInvalidWidthValue(t *testing.T) {
	buf := []byte{0x01, 0x02, 0x03, 0x04}
	result := readIndex(buf, 0, 3)
	if result != 0 {
		t.Errorf("expected 0 for invalid width, got %d", result)
	}
}

// --- decodePlain 默认分支测试 ---

// TestDecodePlainUnsupportedDataType 测试解码不支持的类型
func TestDecodePlainUnsupportedDataType(t *testing.T) {
	enc := &EncodedColumn{Encoding: EncodingPlain, Type: common.DataType(99), RowCount: 1, Data: []byte{0x01}}
	_, _, err := decodePlain(enc)
	if err == nil {
		t.Fatal("expected error for unsupported type in decodePlain, got nil")
	}
}

// --- Compaction 错误路径补充测试 ---

// TestCompactEmptySegmentsList 测试合并空 segment 列表时的错误
func TestCompactEmptySegmentsList(t *testing.T) {
	c := NewCompactor(t.TempDir(), newSegmentIDGen())
	_, err := c.Compact(nil, nil)
	if err == nil {
		t.Fatal("expected error for empty segments, got nil")
	}
}

// TestCompactBuildSegmentColAppendError 测试 compaction 中列追加失败
func TestCompactBuildSegmentColAppendError(t *testing.T) {
	c := NewCompactor(t.TempDir(), newSegmentIDGen())
	rows := []memRow{{Key: "a", Values: []common.Value{common.NewInt64(1)}}}
	// 使用不匹配的列元数据触发 Append 类型错误
	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeString}}
	_, err := c.buildSegment(rows, cols)
	if err == nil {
		t.Fatal("expected error for column type mismatch in buildSegment, got nil")
	}
}

// --- Compaction readSegmentRows 边界测试 ---

// TestCompactorReadSegmentRowsNoRows 测试读取空 Segment 的行
func TestCompactorReadSegmentRowsNoRows(t *testing.T) {
	c := NewCompactor(t.TempDir(), newSegmentIDGen())
	seg := &Segment{ID: 1, RowCount: 0, Columns: []EncodedColumn{}, Keys: []string{}}
	rows, err := c.readSegmentRows(seg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}

// --- Compaction mergeSegments 去重测试 ---

// TestCompactorMergeSegmentsDedupV2 测试合并时正确去重（保留最新版本）
func TestCompactorMergeSegmentsDedupV2(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("key2", map[string]common.Value{colVal: common.NewInt64(2)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush 1: %v", err)
	}

	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(10)})
	_ = eng.Write("key3", map[string]common.Value{colVal: common.NewInt64(3)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush 2: %v", err)
	}

	if err := eng.Compact(cols); err != nil {
		t.Fatalf("compact: %v", err)
	}

	row, ok := eng.Get("key1")
	if !ok {
		t.Fatal("key1 not found after compaction")
	}
	if row.Columns[colVal].Int64 != 10 {
		t.Errorf("key1: expected 10 (latest version), got %d", row.Columns[colVal].Int64)
	}

	row2, ok2 := eng.Get("key2")
	if !ok2 || row2.Columns[colVal].Int64 != 2 {
		t.Errorf("key2: expected 2, got %d, ok=%v", row2.Columns[colVal].Int64, ok2)
	}

	row3, ok3 := eng.Get("key3")
	if !ok3 || row3.Columns[colVal].Int64 != 3 {
		t.Errorf("key3: expected 3, got %d, ok=%v", row3.Columns[colVal].Int64, ok3)
	}
}

// --- Compaction CompactToLevel 测试 ---

// TestCompactorCompactToLevelV2 测试 CompactToLevel 方法
func TestCompactorCompactToLevelV2(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("b", map[string]common.Value{colVal: common.NewInt64(2)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	segments := eng.Segments()
	if len(segments) == 0 {
		t.Fatal("expected at least 1 segment")
	}

	c := NewCompactor(dir, eng.segIDGen)
	seg, err := c.CompactToLevel(segments, 1, cols)
	if err != nil {
		t.Fatalf("CompactToLevel failed: %v", err)
	}
	if seg == nil {
		t.Fatal("expected non-nil segment")
	}
}

// TestCompactorCleanupMissingFile 测试清理不存在的文件时不报错
func TestCompactorCleanupMissingFile(t *testing.T) {
	c := NewCompactor(t.TempDir(), newSegmentIDGen())
	segments := []*Segment{{ID: 999, FilePath: "/nonexistent/path/segment_999.widb"}}
	err := c.CleanupSegments(segments)
	if err != nil {
		t.Errorf("expected no error for non-existent file cleanup, got: %v", err)
	}
}

// --- ColumnVector 类型错误测试 ---

// TestColumnVectorSetValueWrongType 测试 SetValue 类型不匹配时的错误
func TestColumnVectorSetValueWrongType(t *testing.T) {
	cv := NewColumnVector(0, common.TypeInt64, 10)
	err := cv.SetValue(0, common.NewString("not_int"))
	if err == nil {
		t.Fatal("expected error for type mismatch in SetValue, got nil")
	}
}

// TestColumnVectorAppendWrongType 测试 Append 类型不匹配时的错误
func TestColumnVectorAppendWrongType(t *testing.T) {
	cv := NewColumnVector(0, common.TypeInt64, 10)
	err := cv.Append(common.NewString("not_int"))
	if err == nil {
		t.Fatal("expected error for type mismatch in Append, got nil")
	}
}

// TestColumnVectorGetValueUnknownDataType 测试 GetValue 对未知类型的处理
func TestColumnVectorGetValueUnknownDataType(t *testing.T) {
	cv := &ColumnVector{Typ: common.DataType(99), capacity: 10, nulls: common.NewBitmap(10)}
	val := cv.GetValue(0)
	if val.Valid {
		t.Error("expected null value for unknown type, got valid value")
	}
}

// --- Merged from encoding_dict_test.go ---

func TestEncodeDecodePlainString(t *testing.T) {
	data := []string{testStrHello, testStrWorld, "", testStrTest, testStrFoo}
	enc, err := EncodeColumn(common.TypeString, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}
	if enc.Encoding != EncodingDict {
		t.Errorf("encoding = %v, want Dict", enc.Encoding)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	strs, ok := decoded.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", decoded)
	}
	for i, v := range data {
		if strs[i] != v {
			t.Errorf("row %d = %q, want %q", i, strs[i], v)
		}
	}
}

func TestEncodeDecodeDictString(t *testing.T) {
	data := []string{testStrApple, testStrBanana, testStrApple, testStrApple, testStrBanana, testStrCherry, testStrApple}
	enc, err := EncodeColumn(common.TypeString, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}
	if enc.Encoding != EncodingDict {
		t.Errorf("encoding = %v, want Dict", enc.Encoding)
	}
	if len(enc.Dict) != 3 {
		t.Errorf("dict size = %d, want 3", len(enc.Dict))
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	strs, ok := decoded.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", decoded)
	}
	for i, v := range data {
		if strs[i] != v {
			t.Errorf("row %d = %q, want %q", i, strs[i], v)
		}
	}
}

func TestEncodeDecodeDictStringWithNulls(t *testing.T) {
	data := []string{"a", "b", "a", "c", "b"}
	nulls := common.NewBitmap(5)
	nulls.Set(1)
	nulls.Set(3)

	enc, err := EncodeColumn(common.TypeString, data, uint32(len(data)), nulls)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}

	decoded, decodedNulls, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	strs, ok := decoded.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", decoded)
	}

	for i := uint32(0); i < 5; i++ {
		if nulls.Get(i) != decodedNulls.Get(i) {
			t.Errorf("row %d null mismatch: expected %v, got %v", i, nulls.Get(i), decodedNulls.Get(i))
		}
		if !nulls.Get(i) && strs[i] != data[i] {
			t.Errorf("row %d = %q, want %q", i, strs[i], data[i])
		}
	}
}

func TestEncodeDecodeDictLargeIndex(t *testing.T) {
	const n = 300
	data := make([]string, n)
	for i := 0; i < n; i++ {
		data[i] = "value_" + string(rune('a'+i%26))
	}
	enc, err := EncodeColumn(common.TypeString, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	strs, ok := decoded.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", decoded)
	}
	for i := 0; i < n; i++ {
		if strs[i] != data[i] {
			t.Errorf("row %d = %q, want %q", i, strs[i], data[i])
		}
	}
}

func TestEncodeDictInvalidType(t *testing.T) {
	_, err := encodeDict(common.TypeInt64, []int64{1}, 1, nil)
	if err == nil {
		t.Error("expected error for non-string dict")
	}
}

func TestDecodeColumnCorruptedData(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingDict,
		Type:     common.TypeString,
		RowCount: 1,
		Data:     []byte{0x02},
		Dict:     []string{"a", "b"},
	}
	_, _, err := DecodeColumn(enc)
	if err == nil {
		t.Error("expected error for corrupted dict index")
	}
}
