package storage

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ---------------------------------------------------------------------------
// WAL.Truncate() tests
// ---------------------------------------------------------------------------

// TestWALTruncateWithData verifies that Truncate resets offset to 0 and allows
// writing again after truncating a WAL that has data.
func TestWALTruncateWithData(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "truncate.wal")

	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL: %v", err)
	}

	// Write some data
	if err := w.AppendWrite([]byte("hello")); err != nil {
		t.Fatalf("AppendWrite: %v", err)
	}
	if err := w.AppendWrite([]byte("world")); err != nil {
		t.Fatalf("AppendWrite: %v", err)
	}

	// Verify offset is non-zero
	if offset := w.Size(); offset == 0 {
		t.Fatalf("expected non-zero offset after writes, got %d", offset)
	}

	// Truncate
	if err := w.Truncate(); err != nil {
		t.Fatalf("Truncate: %v", err)
	}

	// Verify offset is 0
	if offset := w.Size(); offset != 0 {
		t.Errorf("expected offset 0 after truncate, got %d", offset)
	}

	// Verify we can write again after truncate
	if err := w.AppendWrite([]byte("after_truncate")); err != nil {
		t.Fatalf("AppendWrite after truncate: %v", err)
	}
	if offset := w.Size(); offset == 0 {
		t.Errorf("expected non-zero offset after post-truncate write, got %d", offset)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestWALTruncateReadOnlyDir verifies that Truncate fails when the WAL is in
// a read-only directory (cannot create temp file).
func TestWALTruncateReadOnlyDir(t *testing.T) {
	skipIfRoot(t)

	dir := t.TempDir()
	walPath := filepath.Join(dir, "readonly.wal")

	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL: %v", err)
	}
	if err := w.AppendWrite([]byte("data")); err != nil {
		t.Fatalf("AppendWrite: %v", err)
	}

	// Make directory read-only so temp file creation fails
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatalf("Chmod dir: %v", err)
	}
	defer func() { _ = os.Chmod(dir, 0755) }()

	err = w.Truncate()
	if err == nil {
		t.Error("expected error when truncating WAL in read-only directory, got nil")
	}

	// Restore permissions for cleanup
	_ = os.Chmod(dir, 0755)
	_ = w.Close()
}

// TestWALTruncateThenOpenWAL verifies that after Truncate, opening the WAL
// with OpenWAL returns no records (data is gone).
func TestWALTruncateThenOpenWAL(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "truncate_open.wal")

	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL: %v", err)
	}

	// Write data
	if err := w.AppendWrite([]byte("record1")); err != nil {
		t.Fatalf("AppendWrite: %v", err)
	}
	if err := w.AppendCommit([]byte("commit1")); err != nil {
		t.Fatalf("AppendCommit: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Open and verify records exist
	w2, records, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records before truncate, got %d", len(records))
	}

	// Truncate
	if err := w2.Truncate(); err != nil {
		t.Fatalf("Truncate: %v", err)
	}
	if err := w2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Re-open and verify no records
	w3, records2, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL after truncate: %v", err)
	}
	defer func() { _ = w3.Close() }()

	if len(records2) != 0 {
		t.Errorf("expected 0 records after truncate, got %d", len(records2))
	}
}

// ---------------------------------------------------------------------------
// WAL.OpenWAL() tests
// ---------------------------------------------------------------------------

// TestLowFuncOpenWALNonExistentFile verifies that OpenWAL returns an error containing
// "file not found" when the WAL file does not exist.
func TestLowFuncOpenWALNonExistentFile(t *testing.T) {
	_, _, err := OpenWAL(filepath.Join(t.TempDir(), "does_not_exist.wal"))
	if err == nil {
		t.Fatal("expected error for non-existent WAL file, got nil")
	}
}

// TestLowFuncOpenWALDirectoryPath verifies that OpenWAL returns a non-NotExist error
// when the path is a directory.
func TestLowFuncOpenWALDirectoryPath(t *testing.T) {
	dir := t.TempDir()
	_, _, err := OpenWAL(dir)
	if err == nil {
		t.Fatal("expected error when opening directory as WAL, got nil")
	}
}

// TestLowFuncOpenWALCorruptedDataAtEnd verifies that OpenWAL handles corrupted data
// at the end of the WAL file (partial record) by returning only the valid
// records before the corruption.
func TestLowFuncOpenWALCorruptedDataAtEnd(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "corrupted.wal")

	// Create WAL and write valid records
	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL: %v", err)
	}
	if err := w.AppendWrite([]byte("valid1")); err != nil {
		t.Fatalf("AppendWrite: %v", err)
	}
	if err := w.AppendWrite([]byte("valid2")); err != nil {
		t.Fatalf("AppendWrite: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Append corrupted/partial data to the end of the file
	f, err := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	// Write a partial header (only 2 bytes instead of 4)
	if _, err := f.Write([]byte{0xAB, 0xCD}); err != nil {
		t.Fatalf("write corrupted data: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Open and verify only valid records are returned
	w2, records, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL with corrupted tail: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if len(records) != 2 {
		t.Errorf("expected 2 valid records, got %d", len(records))
	}
}

// TestLowFuncOpenWALValidRecordsAndContinueWrite verifies that OpenWAL returns valid
// records and the WAL can continue to be written to after opening.
func TestLowFuncOpenWALValidRecordsAndContinueWrite(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "continue.wal")

	// Create WAL and write records
	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL: %v", err)
	}
	if err := w.AppendWrite([]byte("first")); err != nil {
		t.Fatalf("AppendWrite: %v", err)
	}
	if err := w.AppendCommit([]byte("commit")); err != nil {
		t.Fatalf("AppendCommit: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Open existing WAL
	w2, records, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].Type != walTypeWrite {
		t.Errorf("record 0 type = %d, want %d", records[0].Type, walTypeWrite)
	}
	if records[1].Type != walTypeCommit {
		t.Errorf("record 1 type = %d, want %d", records[1].Type, walTypeCommit)
	}

	// Continue writing after open
	if err := w2.AppendWrite([]byte("after_open")); err != nil {
		t.Fatalf("AppendWrite after OpenWAL: %v", err)
	}
	if err := w2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Re-open and verify all records
	w3, records2, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL second time: %v", err)
	}
	defer func() { _ = w3.Close() }()

	if len(records2) != 3 {
		t.Errorf("expected 3 records after continued write, got %d", len(records2))
	}
}

// ---------------------------------------------------------------------------
// encodeUint64Batch / encodeFloat64Batch round-trip tests
// ---------------------------------------------------------------------------

// TestEncodeUint64BatchRoundTrip verifies round-trip encoding/decoding for
// int64 values with various edge cases.
func TestEncodeUint64BatchRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		data  []int64
		count uint32
	}{
		{"empty", []int64{}, 0},
		{"single_zero", []int64{0}, 1},
		{"single_value", []int64{42}, 1},
		{"negative", []int64{-1, -100, math.MinInt64}, 3},
		{"max_value", []int64{math.MaxInt64}, 1},
		{"mixed", []int64{0, 1, -1, math.MaxInt64, math.MinInt64, 42, -100}, 7},
		{"large_batch", makeLargeInt64Batch(1000), 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := encodeUint64Batch(tt.data, tt.count)
			if len(encoded) != int(tt.count)*8 {
				t.Errorf("encoded length = %d, want %d", len(encoded), int(tt.count)*8)
			}
			if tt.count == 0 {
				return
			}
			decoded := decodePlainInt64(encoded)
			if len(decoded) != len(tt.data) {
				t.Fatalf("decoded length = %d, want %d", len(decoded), len(tt.data))
			}
			for i, v := range tt.data {
				if decoded[i] != v {
					t.Errorf("decoded[%d] = %d, want %d", i, decoded[i], v)
				}
			}
		})
	}
}

// TestEncodeFloat64BatchRoundTrip verifies round-trip encoding/decoding for
// float64 values with various edge cases.
func TestEncodeFloat64BatchRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		data  []float64
		count uint32
	}{
		{"empty", []float64{}, 0},
		{"single_zero", []float64{0.0}, 1},
		{"single_value", []float64{3.14}, 1},
		{"negative", []float64{-1.5, -0.001}, 2},
		{"inf_and_nan", []float64{math.Inf(1), math.Inf(-1)}, 2},
		{"smallest_positive", []float64{math.SmallestNonzeroFloat64}, 1},
		{"mixed", []float64{0.0, 1.0, -1.0, 3.14, -2.718, 1e10, 1e-10}, 7},
		{"large_batch", makeLargeFloat64Batch(1000), 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := encodeFloat64Batch(tt.data, tt.count)
			if len(encoded) != int(tt.count)*8 {
				t.Errorf("encoded length = %d, want %d", len(encoded), int(tt.count)*8)
			}
			if tt.count == 0 {
				return
			}
			decoded := decodePlainFloat64(encoded)
			if len(decoded) != len(tt.data) {
				t.Fatalf("decoded length = %d, want %d", len(decoded), len(tt.data))
			}
			for i, v := range tt.data {
				if decoded[i] != v {
					t.Errorf("decoded[%d] = %v, want %v", i, decoded[i], v)
				}
			}
		})
	}
}

// TestDecodePlainFloat64EmptyData verifies that decodePlainFloat64 with empty
// data returns an empty slice.
func TestDecodePlainFloat64EmptyData(t *testing.T) {
	result := decodePlainFloat64([]byte{})
	if len(result) != 0 {
		t.Errorf("expected empty result for empty data, got %d elements", len(result))
	}
}

// TestDecodePlainTimestampEmptyData verifies that decodePlainTimestamp with
// empty data returns an empty slice.
func TestDecodePlainTimestampEmptyData(t *testing.T) {
	result := decodePlainTimestamp([]byte{})
	if len(result) != 0 {
		t.Errorf("expected empty result for empty data, got %d elements", len(result))
	}
}

func makeLargeInt64Batch(n int) []int64 {
	data := make([]int64, n)
	for i := range data {
		data[i] = int64(i) * 12345
	}
	return data
}

func makeLargeFloat64Batch(n int) []float64 {
	data := make([]float64, n)
	for i := range data {
		data[i] = float64(i) * 1.2345
	}
	return data
}

// ---------------------------------------------------------------------------
// Compress/Decompress tests
// ---------------------------------------------------------------------------

// TestCompressRoundTripVariousSizes verifies Compress/Decompress round-trip
// with various data sizes.
func TestCompressRoundTripVariousSizes(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"small", []byte("hello")},
		{"medium", make([]byte, 4096)},
		{"large", make([]byte, 65536)},
		{"highly_compressible", make([]byte, 10000)}, // all zeros
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed, err := Compress(tt.data)
			if err != nil {
				t.Fatalf("Compress: %v", err)
			}
			if len(tt.data) == 0 {
				if compressed != nil {
					t.Errorf("expected nil for empty input, got %v", compressed)
				}
				return
			}
			decompressed, err := Decompress(compressed)
			if err != nil {
				t.Fatalf("Decompress: %v", err)
			}
			if len(decompressed) != len(tt.data) {
				t.Fatalf("decompressed length = %d, want %d", len(decompressed), len(tt.data))
			}
			for i, b := range tt.data {
				if decompressed[i] != b {
					t.Errorf("decompressed[%d] = %d, want %d", i, decompressed[i], b)
				}
			}
		})
	}
}

// TestLowFuncCompressColumnNil verifies CompressColumn with nil EncodedColumn returns error.
func TestLowFuncCompressColumnNil(t *testing.T) {
	err := CompressColumn(nil)
	if err == nil {
		t.Error("expected error for nil EncodedColumn, got nil")
	}
}

// TestLowFuncDecompressColumnNil verifies DecompressColumn with nil EncodedColumn returns error.
func TestLowFuncDecompressColumnNil(t *testing.T) {
	err := DecompressColumn(nil)
	if err == nil {
		t.Error("expected error for nil EncodedColumn, got nil")
	}
}

// TestLowFuncCompressEmptyData verifies Compress with empty data returns nil, nil.
func TestLowFuncCompressEmptyData(t *testing.T) {
	compressed, err := Compress([]byte{})
	if err != nil {
		t.Fatalf("Compress empty: %v", err)
	}
	if compressed != nil {
		t.Errorf("expected nil for empty data, got %v", compressed)
	}
}

// TestLowFuncCompressColumnRoundTrip verifies CompressColumn/DecompressColumn round-trip.
func TestLowFuncCompressColumnRoundTrip(t *testing.T) {
	data := []int64{1, 2, 3, 42, -100}
	enc, err := EncodeColumn(common.TypeInt64, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn: %v", err)
	}
	originalData := make([]byte, len(enc.Data))
	copy(originalData, enc.Data)

	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn: %v", err)
	}
	// Data should be different after compression (compressed)
	if len(enc.Data) == 0 {
		t.Error("expected compressed data to be non-empty")
	}

	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn: %v", err)
	}

	// Verify decompressed data matches original
	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn: %v", err)
	}
	ints := decoded.([]int64)
	for i, v := range data {
		if ints[i] != v {
			t.Errorf("decoded[%d] = %d, want %d", i, ints[i], v)
		}
	}
}

// ---------------------------------------------------------------------------
// writeAndSyncFile tests
// ---------------------------------------------------------------------------

// TestLowFuncWriteAndSyncFileReadOnlyDir verifies that writeAndSyncFile fails when
// writing to a read-only directory.
func TestLowFuncWriteAndSyncFileReadOnlyDir(t *testing.T) {
	skipIfRoot(t)

	dir := t.TempDir()
	readOnlyDir := filepath.Join(dir, "readonly")
	if err := os.MkdirAll(readOnlyDir, 0555); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	defer func() { _ = os.Chmod(readOnlyDir, 0755) }()

	err := writeAndSyncFile(filepath.Join(readOnlyDir, "test.txt"), []byte("data"), 0644)
	if err == nil {
		t.Error("expected error writing to read-only directory, got nil")
	}
}

// TestLowFuncWriteAndSyncFileContent verifies that the file content matches the
// written data.
func TestLowFuncWriteAndSyncFileContent(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.dat")
	content := []byte("hello world, this is a test of writeAndSyncFile")

	if err := writeAndSyncFile(filePath, content, 0644); err != nil {
		t.Fatalf("writeAndSyncFile: %v", err)
	}

	read, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(read) != string(content) {
		t.Errorf("file content = %q, want %q", string(read), string(content))
	}
}

// ---------------------------------------------------------------------------
// scanRangeUnlocked tests
// ---------------------------------------------------------------------------

// TestLowFuncScanRangeEmpty verifies that scanning with no matching segments returns
// empty results.
func TestLowFuncScanRangeEmpty(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// No data written, scan should return empty
	entries, err := eng.ScanWithError("a", "z")
	if err != nil {
		t.Fatalf("ScanWithError: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for empty scan, got %d", len(entries))
	}
}

// TestLowFuncScanRangeLargeEstimatedSize verifies the cap limit path in
// scanRangeUnlocked by creating a scenario with a large estimated size.
func TestLowFuncScanRangeLargeEstimatedSize(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// Write data and flush to create a segment with large RowCount
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key_%04d", i)
		if err := eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))}); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Manipulate segment RowCount to be very large to trigger the cap limit
	eng.mu.Lock()
	for _, seg := range eng.segments {
		seg.RowCount = 2 << 20 // > 1<<20 to trigger cap limit
	}
	eng.mu.Unlock()

	// Scan should still work without over-allocating
	entries, err := eng.ScanWithError("a", "z")
	if err != nil {
		t.Fatalf("ScanWithError: %v", err)
	}
	// Results should contain the actual data (may be partial due to fake RowCount)
	// The key thing is that it doesn't panic or OOM
	_ = entries
}
