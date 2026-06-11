package storage

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ---------------------------------------------------------------------------
// OpenWAL (76.5%) - seek error path after truncate
// ---------------------------------------------------------------------------

// TestOpenWALSeekErrorAfterTruncate tests the seek error path in OpenWAL.
// After a successful Truncate, if Seek fails, OpenWAL should return an error
// containing "wal seek". We trigger this by creating a symlink to /dev/null,
// which on Linux can be opened with O_RDWR and Truncate(0) succeeds, but
// the file is a character device where Seek may behave differently.
// Note: On Linux, Seek on /dev/null actually succeeds, so this test verifies
// the Truncate error path instead (which is the more reliably triggerable path).
func TestOpenWALSeekErrorAfterTruncate(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("seek/truncate error test relies on Linux-specific behavior")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// Create a symlink to /dev/null. On Linux, /dev/null can be opened with O_RDWR,
	// but f.Truncate returns EINVAL for non-regular files.
	if err := os.Symlink("/dev/null", path); err != nil {
		t.Fatalf("Symlink to /dev/null failed: %v", err)
	}

	_, _, err := OpenWAL(path)
	if err == nil {
		t.Fatal("expected error when opening /dev/null symlink as WAL, got nil")
	}
}

// TestOpenWALReadOnlyFileOpenError tests OpenWAL when the file cannot be opened
// with O_RDWR. We create a directory at the WAL path; opening a directory with
// O_RDWR should fail.
func TestOpenWALReadOnlyFileOpenError(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "test.wal")

	// Create a directory at the WAL path; O_RDWR on a directory fails on Linux
	if err := os.Mkdir(walPath, 0755); err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}

	_, _, err := OpenWAL(walPath)
	if err == nil {
		t.Fatal("expected error when opening directory as WAL, got nil")
	}
}

// verifyWALRecords 验证 WAL 记录的数量和内容
func verifyWALRecords(t *testing.T, records []RawRecord, wantLen int, wantType byte, wantPayload string, idx int) {
	t.Helper()
	if len(records) != wantLen {
		t.Fatalf("expected %d records, got %d", wantLen, len(records))
	}
	if records[idx].Type != wantType {
		t.Errorf("record %d: type=%d, want %d", idx, records[idx].Type, wantType)
	}
	if string(records[idx].Payload) != wantPayload {
		t.Errorf("record %d: payload=%q, want %q", idx, string(records[idx].Payload), wantPayload)
	}
}

// TestOpenWALWithRecordsAndAppend verifies OpenWAL returns a WAL that can append
// new records after recovering existing ones.
func TestOpenWALWithRecordsAndAppend(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "append_after_open.wal")

	// Create WAL and write records
	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("record_a"))
	_ = w.AppendCheckpoint([]byte("checkpoint_1"))
	_ = w.Close()

	// Open the WAL and verify records
	opened, records, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = opened.Close() }()

	verifyWALRecords(t, records, 2, walTypeWrite, "record_a", 0)
	if records[1].Type != walTypeCheckpoint {
		t.Errorf("record 1: type=%d, want %d", records[1].Type, walTypeCheckpoint)
	}

	// Append new record and re-open to verify persistence
	_ = opened.AppendWrite([]byte("new_record"))
	_ = opened.Sync()
	_ = opened.Close()

	opened2, records2, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("second OpenWAL failed: %v", err)
	}
	defer func() { _ = opened2.Close() }()

	verifyWALRecords(t, records2, 3, walTypeWrite, "new_record", 2)
}

// ---------------------------------------------------------------------------
// Compress (83.3%) - empty input and round-trip
// ---------------------------------------------------------------------------

// TestCompressV2EmptyInput verifies Compress returns (nil, nil) for empty input.
func TestCompressV2EmptyInput(t *testing.T) {
	result, err := Compress([]byte{})
	if err != nil {
		t.Fatalf("Compress([]byte{}) returned unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("Compress([]byte{}) = %v, want nil", result)
	}
}

// TestCompressV2NilInput verifies Compress returns (nil, nil) for nil input.
func TestCompressV2NilInput(t *testing.T) {
	result, err := Compress(nil)
	if err != nil {
		t.Fatalf("Compress(nil) returned unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("Compress(nil) = %v, want nil", result)
	}
}

// TestCompressV2RoundTrip verifies Compress followed by Decompress recovers original data.
func TestCompressV2RoundTrip(t *testing.T) {
	original := []byte("test data for v2 compress round trip verification")
	compressed, err := Compress(original)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}
	if compressed == nil {
		t.Fatal("Compress returned nil for non-empty input")
	}

	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if string(decompressed) != string(original) {
		t.Errorf("round-trip mismatch: got %q, want %q", string(decompressed), string(original))
	}
}

// ---------------------------------------------------------------------------
// Decompress (88.9%) - empty input and invalid data
// ---------------------------------------------------------------------------

// TestDecompressV2EmptyInput verifies Decompress returns (nil, nil) for empty input.
func TestDecompressV2EmptyInput(t *testing.T) {
	result, err := Decompress([]byte{})
	if err != nil {
		t.Fatalf("Decompress([]byte{}) returned unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("Decompress([]byte{}) = %v, want nil", result)
	}
}

// TestDecompressV2NilInput verifies Decompress returns (nil, nil) for nil input.
func TestDecompressV2NilInput(t *testing.T) {
	result, err := Decompress(nil)
	if err != nil {
		t.Fatalf("Decompress(nil) returned unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("Decompress(nil) = %v, want nil", result)
	}
}

// TestDecompressV2CorruptedData verifies Decompress returns error for corrupted data.
func TestDecompressV2CorruptedData(t *testing.T) {
	_, err := Decompress([]byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE})
	if err == nil {
		t.Fatal("expected error for corrupted compressed data, got nil")
	}
}

// TestDecompressV2TruncatedZstdHeader verifies Decompress returns error for
// data that looks like a zstd frame but is truncated.
func TestDecompressV2TruncatedZstdHeader(t *testing.T) {
	// Valid zstd magic number is 0xFD2FB528, but with truncated/corrupted rest
	_, err := Decompress([]byte{0x28, 0xB5, 0x2F, 0xFD, 0x00, 0x01})
	if err == nil {
		t.Fatal("expected error for truncated zstd frame, got nil")
	}
}

// ---------------------------------------------------------------------------
// CompressColumn (85.7%) - nil and empty data
// ---------------------------------------------------------------------------

// TestCompressColumnV2NilEncodedColumn verifies CompressColumn returns error for nil EncodedColumn.
func TestCompressColumnV2NilEncodedColumn(t *testing.T) {
	err := CompressColumn(nil)
	if err == nil {
		t.Fatal("expected error for nil EncodedColumn in CompressColumn, got nil")
	}
}

// TestCompressColumnV2EmptyData verifies CompressColumn with empty Data field.
// Compress([]byte{}) returns (nil, nil), so the column's Data should become nil.
func TestCompressColumnV2EmptyData(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeInt64,
		RowCount: 0,
		Data:     []byte{},
	}
	err := CompressColumn(enc)
	if err != nil {
		t.Fatalf("CompressColumn with empty data returned unexpected error: %v", err)
	}
	if enc.Data != nil {
		t.Errorf("expected Data to be nil after compressing empty data, got %v", enc.Data)
	}
}

// TestCompressColumnV2WithData verifies CompressColumn with actual data compresses correctly.
func TestCompressColumnV2WithData(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeInt64,
		RowCount: 10,
		Data:     make([]byte, 80), // 10 int64s
	}
	for i := range 10 {
		enc.Data[i*8] = byte(i)
	}

	err := CompressColumn(enc)
	if err != nil {
		t.Fatalf("CompressColumn failed: %v", err)
	}
	if len(enc.Data) == 0 {
		t.Fatal("expected compressed data to be non-empty")
	}
}

// TestDecompressColumnV2NilEncodedColumn verifies DecompressColumn returns error for nil EncodedColumn.
func TestDecompressColumnV2NilEncodedColumn(t *testing.T) {
	err := DecompressColumn(nil)
	if err == nil {
		t.Fatal("expected error for nil EncodedColumn in DecompressColumn, got nil")
	}
}

// TestDecompressColumnV2EmptyData verifies DecompressColumn with empty Data returns nil without error.
func TestDecompressColumnV2EmptyData(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeInt64,
		RowCount: 0,
		Data:     []byte{},
	}
	err := DecompressColumn(enc)
	if err != nil {
		t.Fatalf("DecompressColumn with empty data returned unexpected error: %v", err)
	}
	if enc.Data != nil {
		t.Errorf("expected Data to remain nil after decompressing empty data, got %v", enc.Data)
	}
}

// TestCompressDecompressColumnV2RoundTrip verifies CompressColumn/DecompressColumn round-trip.
func TestCompressDecompressColumnV2RoundTrip(t *testing.T) {
	original := []byte("column data for round trip test")
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeString,
		RowCount: 1,
		Data:     original,
	}

	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn failed: %v", err)
	}
	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn failed: %v", err)
	}
	if string(enc.Data) != string(original) {
		t.Errorf("round-trip mismatch: got %q, want %q", string(enc.Data), string(original))
	}
}

// ---------------------------------------------------------------------------
// EncodeColumn (85.7%) - edge cases
// ---------------------------------------------------------------------------

// TestEncodeColumnV2EmptyValues tests EncodeColumn with empty values slice.
func TestEncodeColumnV2EmptyValues(t *testing.T) {
	enc, err := EncodeColumn(common.TypeInt64, []int64{}, 0, nil)
	if err != nil {
		t.Fatalf("EncodeColumn with empty int64 slice failed: %v", err)
	}
	if enc.RowCount != 0 {
		t.Errorf("expected RowCount=0, got %d", enc.RowCount)
	}
	if enc.Encoding != EncodingPlain {
		t.Errorf("expected Plain encoding, got %v", enc.Encoding)
	}
}

// TestEncodeColumnV2EmptyFloat64 tests EncodeColumn with empty float64 slice.
func TestEncodeColumnV2EmptyFloat64(t *testing.T) {
	enc, err := EncodeColumn(common.TypeFloat64, []float64{}, 0, nil)
	if err != nil {
		t.Fatalf("EncodeColumn with empty float64 slice failed: %v", err)
	}
	if enc.RowCount != 0 {
		t.Errorf("expected RowCount=0, got %d", enc.RowCount)
	}
}

// TestEncodeColumnV2EmptyString tests EncodeColumn with empty string slice.
func TestEncodeColumnV2EmptyString(t *testing.T) {
	enc, err := EncodeColumn(common.TypeString, []string{}, 0, nil)
	if err != nil {
		t.Fatalf("EncodeColumn with empty string slice failed: %v", err)
	}
	if enc.RowCount != 0 {
		t.Errorf("expected RowCount=0, got %d", enc.RowCount)
	}
}

// TestEncodeColumnV2BoolType tests EncodeColumn with bool type selects Bitmap encoding.
func TestEncodeColumnV2BoolType(t *testing.T) {
	bools := []uint64{1, 0, 1, 1, 0}
	enc, err := EncodeColumn(common.TypeBool, bools, uint32(len(bools)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn with bool type failed: %v", err)
	}
	if enc.Encoding != EncodingBitmap {
		t.Errorf("expected Bitmap encoding for bool type, got %v", enc.Encoding)
	}
	if enc.RowCount != uint32(len(bools)) {
		t.Errorf("expected RowCount=%d, got %d", len(bools), enc.RowCount)
	}
}

// TestEncodeColumnV2StringType tests EncodeColumn with string type selects Dict encoding.
func TestEncodeColumnV2StringType(t *testing.T) {
	strs := []string{testStrAlpha, testStrBeta, testStrGamma}
	enc, err := EncodeColumn(common.TypeString, strs, uint32(len(strs)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn with string type failed: %v", err)
	}
	if enc.Encoding != EncodingDict {
		t.Errorf("expected Dict encoding for string type, got %v", enc.Encoding)
	}
}

// TestEncodeColumnV2RLEInt64 tests EncodeColumn with RLE-eligible int64 data.
func TestEncodeColumnV2RLEInt64(t *testing.T) {
	// Create data with many repeated values to trigger RLE encoding
	ints := make([]int64, 1000)
	for i := range ints {
		ints[i] = int64(i / 100) // 100 runs of 10 identical values each
	}
	enc, err := EncodeColumn(common.TypeInt64, ints, uint32(len(ints)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn with RLE-eligible data failed: %v", err)
	}
	if enc.Encoding != EncodingRLE {
		t.Errorf("expected RLE encoding for highly repetitive int64 data, got %v", enc.Encoding)
	}
}

// TestEncodeColumnV2PlainInt64 tests EncodeColumn with non-RLE int64 data selects Plain encoding.
func TestEncodeColumnV2PlainInt64(t *testing.T) {
	ints := []int64{1, 2, 3, 4, 5} // Too few runs to trigger RLE
	enc, err := EncodeColumn(common.TypeInt64, ints, uint32(len(ints)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn with non-RLE int64 data failed: %v", err)
	}
	if enc.Encoding != EncodingPlain {
		t.Errorf("expected Plain encoding for non-RLE int64 data, got %v", enc.Encoding)
	}
}

// TestEncodeColumnV2Timestamp tests EncodeColumn with timestamp type.
func TestEncodeColumnV2Timestamp(t *testing.T) {
	times := []int64{1000000, 2000000, 3000000}
	enc, err := EncodeColumn(common.TypeTimestamp, times, uint32(len(times)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn with timestamp type failed: %v", err)
	}
	if enc.Encoding != EncodingPlain {
		t.Errorf("expected Plain encoding for timestamp type, got %v", enc.Encoding)
	}
	if enc.RowCount != uint32(len(times)) {
		t.Errorf("expected RowCount=%d, got %d", len(times), enc.RowCount)
	}
}

// TestEncodeColumnV2WithNulls tests EncodeColumn with null bitmap.
func TestEncodeColumnV2WithNulls(t *testing.T) {
	ints := []int64{10, 20, 30, 40, 50}
	nulls := common.NewBitmap(5)
	nulls.Set(1) // row 1 is null
	nulls.Set(3) // row 3 is null

	enc, err := EncodeColumn(common.TypeInt64, ints, 5, nulls)
	if err != nil {
		t.Fatalf("EncodeColumn with nulls failed: %v", err)
	}
	if enc.Nulls == nil {
		t.Fatal("expected Nulls to be set in encoded column")
	}
}

// TestEncodeColumnV2WrongType tests EncodeColumn with mismatched data type.
func TestEncodeColumnV2WrongType(t *testing.T) {
	// Pass []string but declare TypeInt64 - should fail at type assertion
	_, err := EncodeColumn(common.TypeInt64, []string{"not_int64"}, 1, nil)
	if err == nil {
		t.Fatal("expected error for type mismatch in EncodeColumn, got nil")
	}
}

// TestEncodeColumnV2Float64 tests EncodeColumn with float64 data.
func TestEncodeColumnV2Float64(t *testing.T) {
	floats := []float64{1.1, 2.2, 3.3, 4.4, 5.5}
	enc, err := EncodeColumn(common.TypeFloat64, floats, uint32(len(floats)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn with float64 failed: %v", err)
	}
	if enc.Encoding != EncodingPlain {
		t.Errorf("expected Plain encoding for float64, got %v", enc.Encoding)
	}

	// Verify round-trip through DecodeColumn
	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	decodedFloats, ok := decoded.([]float64)
	if !ok {
		t.Fatalf("expected []float64, got %T", decoded)
	}
	for i, v := range floats {
		if decodedFloats[i] != v {
			t.Errorf("row %d: got %f, want %f", i, decodedFloats[i], v)
		}
	}
}
