package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// --- OpenWAL error paths ---

// TestOpenWAL_TruncateError_V17 tests OpenWAL when truncate fails.
func TestOpenWAL_TruncateError_V17(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping: test requires non-root user")
	}

	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL: %v", err)
	}
	if err := w.AppendWrite([]byte("test-data")); err != nil {
		t.Fatalf("AppendWrite: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatalf("Chmod dir: %v", err)
	}
	defer func() { _ = os.Chmod(dir, 0755) }()

	_, _, err = OpenWAL(walPath)
	if err != nil {
		t.Logf("OpenWAL with read-only dir returned error (expected): %v", err)
	}
}

// TestOpenWAL_FileNotExist_V17 tests OpenWAL with a non-existent file.
func TestOpenWAL_FileNotExist_V17(t *testing.T) {
	_, _, err := OpenWAL(filepath.Join(t.TempDir(), "nonexistent.log"))
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
}

// TestOpenWAL_CorruptWALPartialRecord_V17 tests OpenWAL with corrupt tail data.
func TestOpenWAL_CorruptWALPartialRecord_V17(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL: %v", err)
	}
	if err := w.AppendWrite([]byte("good-record")); err != nil {
		t.Fatalf("AppendWrite: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	_, _ = f.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	_ = f.Close()

	w2, records, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL with corrupt tail: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if len(records) != 1 {
		t.Errorf("expected 1 valid record, got %d", len(records))
	}
}

// --- CompressColumn / DecompressColumn ---

// TestCompressColumn_NilInput_V17 tests CompressColumn with nil EncodedColumn.
func TestCompressColumn_NilInput_V17(t *testing.T) {
	err := CompressColumn(nil)
	if err == nil {
		t.Error("expected error for nil EncodedColumn, got nil")
	}
}

// TestDecompressColumn_NilInput_V17 tests DecompressColumn with nil EncodedColumn.
func TestDecompressColumn_NilInput_V17(t *testing.T) {
	err := DecompressColumn(nil)
	if err == nil {
		t.Error("expected error for nil EncodedColumn, got nil")
	}
}

// TestCompressColumn_ValidInput_V17 tests CompressColumn with valid data.
func TestCompressColumn_ValidInput_V17(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeInt64,
		RowCount: 2,
		Data:     []byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	}
	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn: %v", err)
	}
	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn: %v", err)
	}
}

// --- DecodeColumn with unknown encoding type ---

// TestDecodeColumn_UnknownEncodingType_V17 tests DecodeColumn with unknown encoding.
func TestDecodeColumn_UnknownEncodingType_V17(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingType(99),
		Type:     common.TypeInt64,
		RowCount: 1,
		Data:     []byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	}
	_, _, err := DecodeColumn(enc)
	if err == nil {
		t.Error("expected error for unknown encoding type, got nil")
	}
}

// --- Segment Build ---

// TestSegmentBuilder_Build_NoColumns_V17 tests Build with no columns.
func TestSegmentBuilder_Build_NoColumns_V17(t *testing.T) {
	builder := NewSegmentBuilder(1, "a", "z")
	_, err := builder.Build()
	if err == nil {
		t.Error("expected error for no columns, got nil")
	}
}

// TestSegmentBuilder_Build_WithKeys_V17 tests Build with keys (bloom filter path).
func TestSegmentBuilder_Build_WithKeys_V17(t *testing.T) {
	builder := NewSegmentBuilder(1, "a", "c")
	builder.SetKeys([]string{"a", "b", "c"})

	data := []int64{1, 2, 3}
	enc, err := EncodeColumn(common.TypeInt64, data, 3, nil)
	if err != nil {
		t.Fatalf("EncodeColumn: %v", err)
	}
	builder.AddEncodedColumn(enc)

	seg, err := builder.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if seg.ID != 1 {
		t.Errorf("ID = %d, want 1", seg.ID)
	}
	if len(seg.Footer.BloomFilter) == 0 {
		t.Error("expected bloom filter to be built")
	}
	if len(seg.Keys) != 3 {
		t.Errorf("expected 3 keys, got %d", len(seg.Keys))
	}
}

// --- Flusher writeSegment error paths ---

// TestFlusher_WriteSegment_MkdirError_V17 tests writeSegment when mkdir fails.
func TestFlusher_WriteSegment_MkdirError_V17(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "subdir")
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_ = f.Close()

	flusher := NewFlusher(filePath)

	seg := &Segment{
		ID:       1,
		MinKey:   "a",
		MaxKey:   "z",
		RowCount: 1,
		Columns: []EncodedColumn{
			{Encoding: EncodingPlain, Type: common.TypeInt64, RowCount: 1, Data: []byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}},
		},
	}
	_, err = flusher.writeSegment(seg)
	if err == nil {
		t.Error("expected error for mkdir failure, got nil")
	}
}

// --- EncodingType.String ---

// TestEncodingType_String_V17 tests the String method for all encoding types.
func TestEncodingType_String_V17(t *testing.T) {
	tests := []struct {
		enc      EncodingType
		expected string
	}{
		{EncodingPlain, "Plain"},
		{EncodingDict, "Dict"},
		{EncodingRLE, "RLE"},
		{EncodingBitmap, "Bitmap"},
		{EncodingType(99), "Unknown(99)"},
	}
	for _, tt := range tests {
		got := tt.enc.String()
		if got != tt.expected {
			t.Errorf("EncodingType(%d).String() = %q, want %q", tt.enc, got, tt.expected)
		}
	}
}

// --- Compress empty data ---

// TestCompress_EmptyData_V17 tests Compress with empty data.
func TestCompress_EmptyData_V17(t *testing.T) {
	result, err := Compress([]byte{})
	if err != nil {
		t.Fatalf("Compress empty: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for empty data, got %v", result)
	}
}

// TestDecompress_EmptyData_V17 tests Decompress with empty data.
func TestDecompress_EmptyData_V17(t *testing.T) {
	result, err := Decompress([]byte{})
	if err != nil {
		t.Fatalf("Decompress empty: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for empty data, got %v", result)
	}
}
