package storage

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

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
	c := NewCompactor(t.TempDir())
	_, err := c.Compact(nil, nil)
	if err == nil {
		t.Fatal("expected error for empty segments, got nil")
	}
}

// TestCompactBuildSegmentColAppendError 测试 compaction 中列追加失败
func TestCompactBuildSegmentColAppendError(t *testing.T) {
	c := NewCompactor(t.TempDir())
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
	c := NewCompactor(t.TempDir())
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

	c := NewCompactor(dir)
	c.SetNextID(eng.flusher.NextID())
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
	c := NewCompactor(t.TempDir())
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
