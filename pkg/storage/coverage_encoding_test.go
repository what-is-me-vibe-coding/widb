package storage

import (
	"math"
	"os"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ---------------------------------------------------------------------------
// EncodeColumn additional types — TypeBool and TypeTimestamp
// ---------------------------------------------------------------------------

func TestEncodeColumnBoolRoundTrip(t *testing.T) {
	bools := []uint64{1, 0, 1, 1, 0}
	rowCount := uint32(len(bools))

	enc, err := EncodeColumn(common.TypeBool, bools, rowCount, nil)
	if err != nil {
		t.Fatalf("EncodeColumn TypeBool failed: %v", err)
	}
	if enc.Encoding != EncodingBitmap {
		t.Fatalf("expected Bitmap encoding for TypeBool, got %v", enc.Encoding)
	}
	if enc.Type != common.TypeBool {
		t.Fatalf("expected TypeBool, got %v", enc.Type)
	}
	if enc.RowCount != rowCount {
		t.Fatalf("expected RowCount %d, got %d", rowCount, enc.RowCount)
	}

	decoded, nulls, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn TypeBool failed: %v", err)
	}
	decodedBools, ok := decoded.([]uint64)
	if !ok {
		t.Fatalf("expected []uint64, got %T", decoded)
	}
	if nulls != nil {
		t.Fatalf("expected nil nulls, got non-nil")
	}
	for i, v := range decodedBools {
		if v != bools[i] {
			t.Errorf("row %d: got %d, want %d", i, v, bools[i])
		}
	}
}

func TestEncodeColumnBoolWithNulls(t *testing.T) {
	bools := []uint64{1, 0, 1}
	rowCount := uint32(3)
	nulls := common.NewBitmap(3)
	nulls.Set(1)

	enc, err := EncodeColumn(common.TypeBool, bools, rowCount, nulls)
	if err != nil {
		t.Fatalf("EncodeColumn TypeBool with nulls failed: %v", err)
	}

	decoded, decodedNulls, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn TypeBool with nulls failed: %v", err)
	}

	decodedBools := decoded.([]uint64)
	if decodedNulls == nil || !decodedNulls.Get(1) {
		t.Error("expected row 1 to be null")
	}
	if decodedBools[0] != 1 || decodedBools[2] != 1 {
		t.Errorf("unexpected decoded values: %v", decodedBools)
	}
}

func TestEncodeColumnTimestampRoundTrip(t *testing.T) {
	now := time.Now()
	times := []int64{
		now.UnixNano(),
		now.Add(time.Hour).UnixNano(),
		now.Add(2 * time.Hour).UnixNano(),
	}
	rowCount := uint32(len(times))

	enc, err := EncodeColumn(common.TypeTimestamp, times, rowCount, nil)
	if err != nil {
		t.Fatalf("EncodeColumn TypeTimestamp failed: %v", err)
	}
	if enc.Encoding != EncodingPlain {
		t.Fatalf("expected Plain encoding for TypeTimestamp, got %v", enc.Encoding)
	}
	if enc.Type != common.TypeTimestamp {
		t.Fatalf("expected TypeTimestamp, got %v", enc.Type)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn TypeTimestamp failed: %v", err)
	}
	decodedTimes, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("expected []int64, got %T", decoded)
	}
	for i, v := range decodedTimes {
		if v != times[i] {
			t.Errorf("row %d: got %d, want %d", i, v, times[i])
		}
	}
}

func TestEncodeColumnTimestampWithNulls(t *testing.T) {
	times := []int64{1000, 2000, 3000}
	rowCount := uint32(3)
	nulls := common.NewBitmap(3)
	nulls.Set(0)
	nulls.Set(2)

	enc, err := EncodeColumn(common.TypeTimestamp, times, rowCount, nulls)
	if err != nil {
		t.Fatalf("EncodeColumn TypeTimestamp with nulls failed: %v", err)
	}

	decoded, decodedNulls, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn TypeTimestamp with nulls failed: %v", err)
	}

	decodedTimes := decoded.([]int64)
	if decodedNulls == nil {
		t.Fatal("expected non-nil nulls bitmap")
	}
	if !decodedNulls.Get(0) || !decodedNulls.Get(2) {
		t.Error("expected rows 0 and 2 to be null")
	}
	if decodedNulls.Get(1) {
		t.Error("expected row 1 to not be null")
	}
	if decodedTimes[1] != 2000 {
		t.Errorf("row 1: got %d, want 2000", decodedTimes[1])
	}
}

// ---------------------------------------------------------------------------
// writeSegment error path
// ---------------------------------------------------------------------------

func TestWriteSegmentDirCreationFails(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "flusher-blockdir-*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	flusher := NewFlusher(tmpPath + "/subdir/segment")
	seg := &Segment{ID: 1}
	_, err = flusher.writeSegment(seg)
	if err == nil {
		t.Fatal("expected error when writeSegment cannot create directory")
	}
}

func TestWriteSegmentWriteFileFails(t *testing.T) {
	dir := t.TempDir()
	flusher := NewFlusher(dir)

	seg := &Segment{
		ID:       1,
		MinKey:   "a",
		MaxKey:   "b",
		RowCount: 1,
		Columns: []EncodedColumn{{
			Encoding: EncodingPlain,
			Type:     common.TypeInt64,
			RowCount: 1,
			Data:     make([]byte, 8),
		}},
	}

	fileName, err := flusher.writeSegment(seg)
	if err != nil {
		t.Fatalf("writeSegment to writable dir failed: %v", err)
	}
	if fileName == "" {
		t.Fatal("expected non-empty file name")
	}

	tmpFile, err := os.CreateTemp("", "write-blocker-*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	blockedFlusher := NewFlusher(tmpPath + "/subdir")
	_, err = blockedFlusher.writeSegment(seg)
	if err == nil {
		t.Fatal("expected error when writeSegment cannot create directory")
	}
}

// ---------------------------------------------------------------------------
// Segment Build with no columns
// ---------------------------------------------------------------------------

func TestSegmentBuildNoColumns(t *testing.T) {
	builder := NewSegmentBuilder(1, "a", "z")
	_, err := builder.Build()
	if err == nil {
		t.Fatal("expected error when building segment with no columns")
	}
}

func TestSegmentBuildOnlyNilColumns(t *testing.T) {
	builder := NewSegmentBuilder(1, "a", "z")
	builder.AddEncodedColumn(nil)
	builder.AddEncodedColumn(nil)

	_, err := builder.Build()
	if err == nil {
		t.Fatal("expected error when building segment with only nil columns")
	}
}

// ---------------------------------------------------------------------------
// ScanRange with empty result
// ---------------------------------------------------------------------------

func TestScanRangeNoMatchingKeys(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("b", map[string]common.Value{colVal: common.NewInt64(2)})
	_ = eng.Write("c", map[string]common.Value{colVal: common.NewInt64(3)})

	eng.mu.RLock()
	results := eng.ScanRange("m", "z")
	eng.mu.RUnlock()

	if len(results) != 0 {
		t.Fatalf("expected 0 results for non-overlapping range, got %d", len(results))
	}
}

func TestScanRangeEmptyEngine(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	eng.mu.RLock()
	results := eng.ScanRange("a", "z")
	eng.mu.RUnlock()

	if len(results) != 0 {
		t.Fatalf("expected 0 results from empty engine, got %d", len(results))
	}
}

func TestScanRangeAfterFlushNoMatch(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("b", map[string]common.Value{colVal: common.NewInt64(2)})

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	eng.mu.RLock()
	results := eng.ScanRange("x", "z")
	eng.mu.RUnlock()

	if len(results) != 0 {
		t.Fatalf("expected 0 results for non-overlapping range after flush, got %d", len(results))
	}
}

func TestScanRangeStartAfterAllKeys(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("b", map[string]common.Value{colVal: common.NewInt64(2)})

	eng.mu.RLock()
	results := eng.ScanRange("c", "z")
	eng.mu.RUnlock()

	if len(results) != 0 {
		t.Fatalf("expected 0 results when start > all keys, got %d", len(results))
	}
}

func TestScanRangeEndBeforeAllKeys(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_ = eng.Write("m", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("z", map[string]common.Value{colVal: common.NewInt64(2)})

	eng.mu.RLock()
	results := eng.ScanRange("a", "b")
	eng.mu.RUnlock()

	if len(results) != 0 {
		t.Fatalf("expected 0 results when end < all keys, got %d", len(results))
	}
}

// --- Merged from coverage_encoding_and_scan_test.go ---

func TestEncodeDecodeRLEInt64(t *testing.T) {
	data := []int64{1, 1, 1, 1, 1, 2, 2, 3, 3, 3}
	enc, err := EncodeColumn(common.TypeInt64, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}
	if enc.Encoding != EncodingRLE {
		t.Errorf("encoding = %v, want RLE", enc.Encoding)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	ints, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("expected []int64, got %T", decoded)
	}
	for i, v := range data {
		if ints[i] != v {
			t.Errorf("row %d = %d, want %d", i, ints[i], v)
		}
	}
}

func TestEncodeDecodeRLEInt64WithNulls(t *testing.T) {
	data := []int64{1, 1, 0, 2, 2, 0, 3, 3, 3, 3}
	nulls := common.NewBitmap(10)
	nulls.Set(2)
	nulls.Set(5)

	enc, err := EncodeColumn(common.TypeInt64, data, uint32(len(data)), nulls)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}
	if enc.Encoding != EncodingRLE {
		t.Errorf("encoding = %v, want RLE", enc.Encoding)
	}

	decoded, decodedNulls, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	ints, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("expected []int64, got %T", decoded)
	}

	for i := uint32(0); i < 10; i++ {
		if nulls.Get(i) != decodedNulls.Get(i) {
			t.Errorf("row %d null mismatch: expected %v, got %v", i, nulls.Get(i), decodedNulls.Get(i))
		}
		if !nulls.Get(i) && ints[i] != data[i] {
			t.Errorf("row %d = %d, want %d", i, ints[i], data[i])
		}
	}
}

func TestEncodeRLEInvalidType(t *testing.T) {
	_, err := encodeRLE(common.TypeFloat64, []float64{1.0}, 1, nil)
	if err == nil {
		t.Error("expected error for non-int64 RLE")
	}
}

func TestEncodeDecodeBitmap(t *testing.T) {
	data := []uint64{1, 0, 1, 1, 0, 0, 1, 0}
	enc, err := EncodeColumn(common.TypeBool, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}
	if enc.Encoding != EncodingBitmap {
		t.Errorf("encoding = %v, want Bitmap", enc.Encoding)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	bools, ok := decoded.([]uint64)
	if !ok {
		t.Fatalf("expected []uint64, got %T", decoded)
	}
	for i, v := range data {
		if bools[i] != v {
			t.Errorf("row %d = %d, want %d", i, bools[i], v)
		}
	}
}

func TestEncodeBitmapWithNulls(t *testing.T) {
	data := []uint64{1, 0, 1}
	nulls := common.NewBitmap(3)
	nulls.Set(1)

	enc, err := encodeBitmap(data, 3, nulls)
	if err != nil {
		t.Fatalf("encodeBitmap: %v", err)
	}
	if len(enc.Nulls) == 0 {
		t.Error("expected nulls in encoded column")
	}
}

func TestEncodeBitmapInvalidData(t *testing.T) {
	_, err := encodeBitmap("not bools", 1, nil)
	if err == nil {
		t.Error("expected error for invalid bitmap data")
	}
}

func TestDecodeBitmapWithNulls(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingBitmap,
		Type:     common.TypeBool,
		RowCount: 3,
		Data:     common.NewBitmap(3).ToBytes(),
		Nulls:    common.NewBitmap(3).ToBytes(),
	}
	_, nulls, err := decodeBitmap(enc)
	if err != nil {
		t.Fatalf("decodeBitmap: %v", err)
	}
	if nulls == nil {
		t.Error("expected non-nil nulls")
	}
}

func TestNullBitmapRoundTrip(t *testing.T) {
	t.Run("Int64", func(t *testing.T) {
		data := []int64{1, 0, 2, 0, 3}
		nulls := common.NewBitmap(5)
		nulls.Set(1)
		nulls.Set(3)

		enc, err := EncodeColumn(common.TypeInt64, data, 5, nulls)
		if err != nil {
			t.Fatalf("EncodeColumn: %v", err)
		}
		decoded, decodedNulls, err := DecodeColumn(enc)
		if err != nil {
			t.Fatalf("DecodeColumn: %v", err)
		}
		if decodedNulls == nil {
			t.Fatal("expected non-nil nulls")
		}
		for i := uint32(0); i < 5; i++ {
			if nulls.Get(i) != decodedNulls.Get(i) {
				t.Errorf("row %d null mismatch: %v vs %v", i, nulls.Get(i), decodedNulls.Get(i))
			}
		}
		_ = decoded
	})

	t.Run("Float64", func(t *testing.T) {
		data := []float64{1.0, 0.0, 2.0}
		nulls := common.NewBitmap(3)
		nulls.Set(1)

		enc, err := EncodeColumn(common.TypeFloat64, data, 3, nulls)
		if err != nil {
			t.Fatalf("EncodeColumn: %v", err)
		}
		_, decodedNulls, err := DecodeColumn(enc)
		if err != nil {
			t.Fatalf("DecodeColumn: %v", err)
		}
		if decodedNulls == nil {
			t.Fatal("expected non-nil nulls")
		}
		if !decodedNulls.Get(1) {
			t.Error("row 1 should be null")
		}
	})
}

func TestEncodePlainStrings(t *testing.T) {
	data := []string{testStrHello, testStrWorld, "", testStrFoo}
	enc, err := encodePlainStrings(data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("encodePlainStrings: %v", err)
	}
	if enc.Encoding != EncodingPlain {
		t.Errorf("encoding = %v, want Plain", enc.Encoding)
	}
	if len(enc.Offsets) != 5 {
		t.Errorf("offsets len = %d, want 5", len(enc.Offsets))
	}
}

func TestEncodePlainStringsWithNulls(t *testing.T) {
	data := []string{"a", "b", "c"}
	nulls := common.NewBitmap(3)
	nulls.Set(1)

	enc, err := encodePlainStrings(data, 3, nulls)
	if err != nil {
		t.Fatalf("encodePlainStrings: %v", err)
	}
	if len(enc.Nulls) == 0 {
		t.Error("expected nulls in encoded column")
	}
}

func TestEncodePlainInvalidTimestamp(t *testing.T) {
	_, err := encodePlain(common.TypeTimestamp, "not ints", 1, nil)
	if err == nil {
		t.Error("expected error for invalid timestamp data")
	}
}

func TestDecodePlainString(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeString,
		RowCount: 2,
		Data:     []byte("ab"),
		Offsets:  []uint32{0, 1, 2},
	}
	decoded, _, err := decodePlain(enc)
	if err != nil {
		t.Fatalf("decodePlain: %v", err)
	}
	strs := decoded.([]string)
	if strs[0] != "a" || strs[1] != "b" {
		t.Errorf("got %q, %q", strs[0], strs[1])
	}
}

func TestDecodePlainUnsupportedType(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeNull,
		RowCount: 1,
		Data:     []byte{0},
	}
	_, _, err := decodePlain(enc)
	if err == nil {
		t.Error("expected error for unsupported type in plain decode")
	}
}

func TestEncodingTypeUnknown(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: 99,
		Type:     common.TypeInt64,
		RowCount: 1,
		Data:     make([]byte, 8),
	}
	_, _, err := DecodeColumn(enc)
	if err == nil {
		t.Error("expected error for unknown encoding")
	}
}

func TestEncodeDecodeRoundTripTimestamp(t *testing.T) {
	data := []int64{100, 200, 300}
	enc, err := EncodeColumn(common.TypeTimestamp, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn: %v", err)
	}
	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn: %v", err)
	}
	times := decoded.([]int64)
	for i, v := range data {
		if times[i] != v {
			t.Errorf("row %d = %d, want %d", i, times[i], v)
		}
	}
}

func TestReadWriteIndex(t *testing.T) {
	tests := []struct {
		width int
		idx   uint32
	}{
		{1, 0},
		{1, 255},
		{2, 0},
		{2, 65535},
		{4, 0},
		{4, math.MaxUint32},
	}

	for _, tt := range tests {
		buf := make([]byte, tt.width)
		writeIndex(buf, 0, tt.width, tt.idx)
		got := readIndex(buf, 0, tt.width)
		if got != tt.idx {
			t.Errorf("width=%d: readWriteIndex = %d, want %d", tt.width, got, tt.idx)
		}
	}
}
