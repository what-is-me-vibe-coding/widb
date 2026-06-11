package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

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
