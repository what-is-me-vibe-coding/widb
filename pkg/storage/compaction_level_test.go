package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestCompactorCompactToLevel(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cols := []ColumnMeta{{ID: 0, Name: col0Name, Type: common.TypeInt64}}
	eng := setupEngine(t, dir, 1<<10)
	writeRows(t, eng, cols, 20, 0)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	segments := eng.Segments()
	compactor := NewCompactor(dir)
	newSeg, err := compactor.CompactToLevel(segments, 0, cols)
	if err != nil {
		t.Fatal(err)
	}

	if newSeg.RowCount != 20 {
		t.Errorf("expected 20 rows, got %d", newSeg.RowCount)
	}
}

func TestCompactorWithFloat64Column(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cols := []ColumnMeta{{ID: 0, Name: "score", Type: common.TypeFloat64}}
	eng := setupEngine(t, dir, 64<<20)
	writeRows(t, eng, cols, 20, 0)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	segments := eng.Segments()
	compactor := NewCompactor(dir)
	newSeg, err := compactor.Compact(segments, cols)
	if err != nil {
		t.Fatal(err)
	}
	if newSeg.RowCount != 20 {
		t.Errorf("expected 20 rows, got %d", newSeg.RowCount)
	}
}

func TestCompactorWithBoolColumn(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cols := []ColumnMeta{{ID: 0, Name: "active", Type: common.TypeBool}}
	eng := setupEngine(t, dir, 64<<20)
	writeRows(t, eng, cols, 20, 0)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	segments := eng.Segments()
	compactor := NewCompactor(dir)
	newSeg, err := compactor.Compact(segments, cols)
	if err != nil {
		t.Fatal(err)
	}
	if newSeg.RowCount != 20 {
		t.Errorf("expected 20 rows, got %d", newSeg.RowCount)
	}
}

func TestCompactorWithStringColumn(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cols := []ColumnMeta{{ID: 0, Name: colName, Type: common.TypeString}}
	eng := setupEngine(t, dir, 64<<20)
	writeRows(t, eng, cols, 20, 0)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	segments := eng.Segments()
	compactor := NewCompactor(dir)
	newSeg, err := compactor.Compact(segments, cols)
	if err != nil {
		t.Fatal(err)
	}
	if newSeg.RowCount != 20 {
		t.Errorf("expected 20 rows, got %d", newSeg.RowCount)
	}
}

func TestCompactorWithTimestampColumn(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cols := []ColumnMeta{{ID: 0, Name: "ts", Type: common.TypeTimestamp}}
	eng := setupEngine(t, dir, 64<<20)
	writeRows(t, eng, cols, 20, 0)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	segments := eng.Segments()
	compactor := NewCompactor(dir)
	newSeg, err := compactor.Compact(segments, cols)
	if err != nil {
		t.Fatal(err)
	}
	if newSeg.RowCount != 20 {
		t.Errorf("expected 20 rows, got %d", newSeg.RowCount)
	}
}

func TestCompactorWithMissingColumn(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	// Write data with one column, then compact with a different column name
	cols := []ColumnMeta{{ID: 0, Name: col0Name, Type: common.TypeInt64}}
	eng := setupEngine(t, dir, 64<<20)
	writeRows(t, eng, cols, 10, 0)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	segments := eng.Segments()
	compactor := NewCompactor(dir)

	// Compact with a different column name - should result in null values
	compactCols := []ColumnMeta{{ID: 0, Name: testColMissing, Type: common.TypeInt64}}
	newSeg, err := compactor.Compact(segments, compactCols)
	if err != nil {
		t.Fatal(err)
	}
	if newSeg.RowCount != 10 {
		t.Errorf("expected 10 rows, got %d", newSeg.RowCount)
	}
}

func TestExtractValueWithNulls(t *testing.T) {
	nulls := common.NewBitmap(4)
	nulls.Set(1)
	nulls.Set(3)

	cd := decodedColumn{
		data:  []int64{10, 20, 30, 40},
		nulls: nulls,
		typ:   common.TypeInt64,
	}

	val := extractValue(cd, 0)
	if val.Int64 != 10 {
		t.Errorf("expected 10, got %d", val.Int64)
	}

	val = extractValue(cd, 1)
	if !val.IsNull() {
		t.Errorf("expected null at row 1, got %v", val)
	}

	val = extractValue(cd, 3)
	if !val.IsNull() {
		t.Errorf("expected null at row 3, got %v", val)
	}
}

func TestExtractValueOutOfRange(t *testing.T) {
	cd := decodedColumn{
		data:  []int64{10},
		nulls: nil,
		typ:   common.TypeInt64,
	}

	// Row index out of range
	val := extractValue(cd, 99)
	if !val.IsNull() {
		t.Errorf("expected null for out-of-range row, got %v", val)
	}
}

// TestCompactToLevelEmptySegments 测试 CompactToLevel 对空输入的处理
func TestCompactToLevelEmptySegments(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	compactor := NewCompactor(dir)
	_, err = compactor.CompactToLevel(nil, 0, nil)
	if err == nil {
		t.Error("expected error for empty segments in CompactToLevel")
	}
}

// TestCompactToLevelMultipleSegments 测试 CompactToLevel 合并多个段
func TestCompactToLevelMultipleSegments(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cols := []ColumnMeta{{ID: 0, Name: col0Name, Type: common.TypeInt64}}
	eng := setupEngine(t, dir, 1<<10)

	// 写入并刷盘两个段
	writeRows(t, eng, cols, 10, 0)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}
	writeRows(t, eng, cols, 10, 10)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	segments := eng.Segments()
	if len(segments) < 2 {
		t.Fatalf("expected at least 2 segments, got %d", len(segments))
	}

	compactor := NewCompactor(dir)
	newSeg, err := compactor.CompactToLevel(segments, 0, cols)
	if err != nil {
		t.Fatalf("CompactToLevel: %v", err)
	}

	if newSeg.RowCount != 20 {
		t.Errorf("expected 20 rows, got %d", newSeg.RowCount)
	}
	if newSeg.FilePath == "" {
		t.Error("expected non-empty file path")
	}
	if _, err := os.Stat(newSeg.FilePath); os.IsNotExist(err) {
		t.Error("compacted segment file does not exist")
	}
}

// TestCompactToLevelWithDifferentColumnTypes 测试 CompactToLevel 对不同列类型的处理
func TestCompactToLevelWithDifferentColumnTypes(t *testing.T) {
	tests := []struct {
		name    string
		colType common.DataType
	}{
		{"float64", common.TypeFloat64},
		{"bool_type", common.TypeBool},
		{"string", common.TypeString},
		{"timestamp", common.TypeTimestamp},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "compactor_test")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.RemoveAll(dir) }()

			cols := []ColumnMeta{{ID: 0, Name: crCol, Type: tt.colType}}
			eng := setupEngine(t, dir, 64<<20)
			writeRows(t, eng, cols, 10, 0)
			if err := eng.Flush(cols); err != nil {
				t.Fatal(err)
			}

			segments := eng.Segments()
			compactor := NewCompactor(dir)
			newSeg, err := compactor.CompactToLevel(segments, 0, cols)
			if err != nil {
				t.Fatalf("CompactToLevel %s: %v", tt.name, err)
			}
			if newSeg.RowCount != 10 {
				t.Errorf("expected 10 rows, got %d", newSeg.RowCount)
			}
		})
	}
}

// TestCompactToLevelWithMissingColumn 测试 CompactToLevel 对缺失列的处理
func TestCompactToLevelWithMissingColumn(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cols := []ColumnMeta{{ID: 0, Name: col0Name, Type: common.TypeInt64}}
	eng := setupEngine(t, dir, 64<<20)
	writeRows(t, eng, cols, 10, 0)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	segments := eng.Segments()
	compactor := NewCompactor(dir)

	// 使用不同的列名进行压缩 - 应产生 null 值
	compactCols := []ColumnMeta{{ID: 0, Name: testColMissing, Type: common.TypeInt64}}
	newSeg, err := compactor.CompactToLevel(segments, 0, compactCols)
	if err != nil {
		t.Fatalf("CompactToLevel with missing column: %v", err)
	}
	if newSeg.RowCount != 10 {
		t.Errorf("expected 10 rows, got %d", newSeg.RowCount)
	}
}

// TestCompactToLevelMergedResultEmpty 测试 CompactToLevel 合并结果为空的情况
func TestCompactToLevelMergedResultEmpty(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	// 创建一个 RowCount=0 的段
	builder := NewSegmentBuilder(1, "", "")
	enc, err := EncodeColumn(common.TypeInt64, []int64{}, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	builder.AddEncodedColumn(enc)
	seg, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	compactor := NewCompactor(dir)
	_, err = compactor.CompactToLevel([]*Segment{seg}, 0, []ColumnMeta{{ID: 0, Name: col0Name, Type: common.TypeInt64}})
	if err == nil {
		t.Error("expected error for empty merged result")
	}
}

// TestCompactorCleanupSegmentsNonExistentFile 测试 CleanupSegments 对不存在文件的处理
func TestCompactorCleanupSegmentsNonExistentFile(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	compactor := NewCompactor(dir)
	seg := &Segment{ID: 999, FilePath: "/nonexistent/path/segment_999.widb"}
	// 清理不存在的文件不应报错
	if err := compactor.CleanupSegments([]*Segment{seg}); err != nil {
		t.Errorf("CleanupSegments should not fail for non-existent files: %v", err)
	}
}

// TestCompactorCleanupSegmentsEmptyFilePath 测试 CleanupSegments 对空文件路径的处理
func TestCompactorCleanupSegmentsEmptyFilePath(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	compactor := NewCompactor(dir)
	seg := &Segment{ID: 1, FilePath: ""}
	// 空文件路径不应报错
	if err := compactor.CleanupSegments([]*Segment{seg}); err != nil {
		t.Errorf("CleanupSegments should not fail for empty file path: %v", err)
	}
}

func TestCompactMergedResultEmpty(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	compactor := NewCompactor(dir)

	// Create segments with RowCount=0 so merge produces 0 rows
	segments := []*Segment{
		{ID: 1, RowCount: 0, Columns: nil, Keys: nil},
		{ID: 2, RowCount: 0, Columns: nil, Keys: nil},
	}
	cols := []ColumnMeta{{ID: 0, Name: col0Name, Type: common.TypeInt64}}

	_, err = compactor.Compact(segments, cols)
	if err == nil {
		t.Fatal("expected error for merged result is empty")
	}
	if err.Error() != "compactor: merged result is empty" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestExtractFloat64ValueOutOfRangeAndWrongType(t *testing.T) {
	val := extractFloat64Value([]float64{1.5, 2.5}, 99)
	if !val.IsNull() {
		t.Errorf("expected null for out-of-range row, got %v", val)
	}

	val = extractFloat64Value([]int64{1, 2}, 0)
	if !val.IsNull() {
		t.Errorf("expected null for wrong data type, got %v", val)
	}
}

func TestExtractBoolValueOutOfRangeAndWrongType(t *testing.T) {
	val := extractBoolValue([]uint64{1, 0}, 99)
	if !val.IsNull() {
		t.Errorf("expected null for out-of-range row, got %v", val)
	}

	val = extractBoolValue([]float64{1.0, 2.0}, 0)
	if !val.IsNull() {
		t.Errorf("expected null for wrong data type, got %v", val)
	}
}

func TestExtractStringValueOutOfRangeAndWrongType(t *testing.T) {
	val := extractStringValue([]string{"a", "b"}, 99)
	if !val.IsNull() {
		t.Errorf("expected null for out-of-range row, got %v", val)
	}

	val = extractStringValue([]int64{1, 2}, 0)
	if !val.IsNull() {
		t.Errorf("expected null for wrong data type, got %v", val)
	}
}

func TestExtractTimestampValueOutOfRangeAndWrongType(t *testing.T) {
	val := extractTimestampValue([]int64{1000, 2000}, 99)
	if !val.IsNull() {
		t.Errorf("expected null for out-of-range row, got %v", val)
	}

	val = extractTimestampValue([]float64{1.0, 2.0}, 0)
	if !val.IsNull() {
		t.Errorf("expected null for wrong data type, got %v", val)
	}
}

func TestExtractInt64ValueOutOfRangeAndWrongType(t *testing.T) {
	val := extractInt64Value([]int64{10, 20}, 99)
	if !val.IsNull() {
		t.Errorf("expected null for out-of-range row, got %v", val)
	}

	val = extractInt64Value([]float64{1.0, 2.0}, 0)
	if !val.IsNull() {
		t.Errorf("expected null for wrong data type, got %v", val)
	}
}

func TestDecodeSegmentColumnCompressed(t *testing.T) {
	data := []int64{10, 20, 30}
	enc, err := EncodeColumn(common.TypeInt64, data, 3, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := CompressColumn(enc); err != nil {
		t.Fatal(err)
	}

	// Verify encoding type is preserved (compression is separate from encoding)
	_ = enc.Encoding

	cd, err := decodeSegmentColumn(enc, 0)
	if err != nil {
		t.Fatalf("decodeSegmentColumn failed: %v", err)
	}

	ints, ok := cd.data.([]int64)
	if !ok {
		t.Fatalf("expected []int64, got %T", cd.data)
	}
	if len(ints) != 3 {
		t.Fatalf("expected 3 values, got %d", len(ints))
	}
	for i, want := range data {
		if ints[i] != want {
			t.Errorf("index %d: expected %d, got %d", i, want, ints[i])
		}
	}
}

func TestCleanupSegmentsEmptyFilePath(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	compactor := NewCompactor(dir)

	segments := []*Segment{
		{ID: 1, RowCount: 5, FilePath: ""},
	}
	if err := compactor.CleanupSegments(segments); err != nil {
		t.Errorf("expected no error for segment with empty FilePath, got: %v", err)
	}
}

func TestCleanupSegmentsNonExistentFile(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	compactor := NewCompactor(dir)

	segments := []*Segment{
		{ID: 1, RowCount: 5, FilePath: filepath.Join(dir, "nonexistent_segment.widb")},
	}
	if err := compactor.CleanupSegments(segments); err != nil {
		t.Errorf("expected no error for non-existent file, got: %v", err)
	}
}

func TestDecodeSegmentColumnWithDictAndNulls(t *testing.T) {
	strs := []string{testStrAlpha, testStrBeta, testStrAlpha, testStrGamma}
	nulls := common.NewBitmap(4)
	nulls.Set(1)

	enc, err := EncodeColumn(common.TypeString, strs, 4, nulls)
	if err != nil {
		t.Fatal(err)
	}

	if err := CompressColumn(enc); err != nil {
		t.Fatal(err)
	}

	cd, err := decodeSegmentColumn(enc, 0)
	if err != nil {
		t.Fatalf("decodeSegmentColumn failed: %v", err)
	}

	if cd.typ != common.TypeString {
		t.Errorf("expected TypeString, got %v", cd.typ)
	}
	result, ok := cd.data.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", cd.data)
	}
	if len(result) != 4 {
		t.Fatalf("expected 4 values, got %d", len(result))
	}
	if result[0] != testStrAlpha {
		t.Errorf("expected alpha, got %s", result[0])
	}
}

func TestDecodeSegmentColumnWithOffsetsAndNulls(t *testing.T) {
	strData := []byte("helloworld")
	offsets := []uint32{0, 5, 10}
	bm := common.NewBitmap(2)
	bm.Set(1)

	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeString,
		RowCount: 2,
		Data:     strData,
		Offsets:  offsets,
		Nulls:    bm.ToBytes(),
	}

	if err := CompressColumn(enc); err != nil {
		t.Fatal(err)
	}

	cd, err := decodeSegmentColumn(enc, 0)
	if err != nil {
		t.Fatalf("decodeSegmentColumn failed: %v", err)
	}

	if cd.typ != common.TypeString {
		t.Errorf("expected TypeString, got %v", cd.typ)
	}
	result, ok := cd.data.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", cd.data)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 values, got %d", len(result))
	}
	if result[0] != testStrHello {
		t.Errorf("expected hello, got %s", result[0])
	}
	if cd.nulls == nil || !cd.nulls.Get(1) {
		t.Error("expected row 1 to be null")
	}
}

func TestDecodeSegmentColumnDecompressError(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeInt64,
		RowCount: 3,
		Data:     []byte{0xFF, 0xFE, 0xFD, 0xFC},
	}
	_, err := decodeSegmentColumn(enc, 0)
	if err == nil {
		t.Error("expected error for invalid compressed data")
	}
}

func TestDecodeSegmentColumnDecodeError(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingType(99),
		Type:     common.TypeInt64,
		RowCount: 3,
		Data:     []byte{1, 2, 3},
	}
	_, err := decodeSegmentColumn(enc, 0)
	if err == nil {
		t.Error("expected error for invalid encoding type")
	}
}

func TestCompactToLevelError(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	compactor := NewCompactor(dir)

	_, err = compactor.CompactToLevel(nil, 0, nil)
	if err == nil {
		t.Error("expected error for CompactToLevel with nil segments")
	}
}
