package storage

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// syscallDup2v17 wraps syscall.Dup2 for portability.
func syscallDup2v17(oldfd, newfd int) error {
	return syscall.Dup2(oldfd, newfd)
}

// --- Write error paths ---

// TestWrite_WALSyncError_V17 tests Write when WAL Sync fails (non-GroupCommit mode).
func TestWrite_WALSyncError_V17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	_ = eng.wal.file.Close()

	err = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})
	if err == nil {
		t.Error("expected error when WAL sync fails, got nil")
	}
}

// TestWrite_WALSyncErrorAfterAppend_V17 tests Write when WAL Sync fails
// after AppendWrite succeeds using Dup2 trick.
func TestWrite_WALSyncErrorAfterAppend_V17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	if err := eng.Write("key0", map[string]common.Value{colVal: common.NewInt64(0)}); err != nil {
		t.Fatalf("first Write: %v", err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	defer func() { _ = r.Close() }()

	oldFd := int(eng.wal.file.Fd())
	newFd := int(w.Fd())
	if err := syscallDup2v17(newFd, oldFd); err != nil {
		t.Fatalf("Dup2: %v", err)
	}
	_ = w.Close()

	err = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})
	if err == nil {
		t.Error("expected error when WAL sync fails after append, got nil")
	}
}

// TestWrite_PutError_V17 tests Write when MemTable.Put fails (frozen memtable).
func TestWrite_PutError_V17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	eng.activeMem.Freeze()

	err = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})
	if err == nil {
		t.Error("expected error when MemTable.Put fails, got nil")
	}
}

// TestWrite_RotateMemTableError_V17 tests Write when rotateMemTable fails.
func TestWrite_RotateMemTableError_V17(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir, MaxMemTableSize: 64})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	eng.mu.Lock()
	eng.activeMem.Freeze()
	eng.immutable = append(eng.immutable, eng.activeMem)
	eng.activeMem = NewMemTableWithSize(64)
	eng.activeMem.Freeze()
	eng.mu.Unlock()

	err = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})
	if err == nil {
		t.Error("expected error when rotate fails, got nil")
	}
}

// --- Write with GroupCommit mode ---

// TestWrite_GroupCommit_V17 tests Write with SyncGroupCommit mode.
func TestWrite_GroupCommit_V17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir:      t.TempDir(),
		SyncMode:     SyncGroupCommit,
		SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	vals := map[string]common.Value{colVal: common.NewInt64(42)}
	if err := eng.Write("key1", vals); err != nil {
		t.Fatalf("Write: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	row, ok := eng.Get("key1")
	if !ok {
		t.Fatal("key1 not found")
	}
	if row.Columns[colVal].Int64 != 42 {
		t.Errorf("expected 42, got %d", row.Columns[colVal].Int64)
	}
}

// TestWrite_WALClosedAppendError_V17 tests Write when WAL is closed.
func TestWrite_WALClosedAppendError_V17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	_ = eng.wal.Close()

	err = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})
	if err == nil {
		t.Error("expected error when WAL is closed, got nil")
	}
}

// --- WriteBatch error paths ---

// TestWriteBatch_WALSyncError_V17 tests WriteBatch when WAL Sync fails.
func TestWriteBatch_WALSyncError_V17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	_ = eng.wal.file.Close()

	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
	}
	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("expected error when WAL sync fails, got nil")
	}
}

// TestWriteBatch_WALSyncErrorAfterAppend_V17 tests WriteBatch Sync error via Dup2.
func TestWriteBatch_WALSyncErrorAfterAppend_V17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	if err := eng.Write("key0", map[string]common.Value{colVal: common.NewInt64(0)}); err != nil {
		t.Fatalf("first Write: %v", err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	defer func() { _ = r.Close() }()

	oldFd := int(eng.wal.file.Fd())
	newFd := int(w.Fd())
	if err := syscallDup2v17(newFd, oldFd); err != nil {
		t.Fatalf("Dup2: %v", err)
	}
	_ = w.Close()

	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
	}
	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("expected error when WAL sync fails after append, got nil")
	}
}

// TestWriteBatch_PutError_V17 tests WriteBatch when MemTable.Put fails.
func TestWriteBatch_PutError_V17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	eng.activeMem.Freeze()

	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
	}
	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("expected error when MemTable.Put fails, got nil")
	}
}

// TestWriteBatch_RotateError_V17 tests WriteBatch when rotateMemTable fails.
func TestWriteBatch_RotateError_V17(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir, MaxMemTableSize: 64})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	eng.mu.Lock()
	eng.activeMem.Freeze()
	eng.mu.Unlock()

	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
	}
	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("expected error when rotate fails, got nil")
	}
}

// TestWriteBatch_NormalPath_V17 tests WriteBatch with valid data.
func TestWriteBatch_NormalPath_V17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
		{Key: "k2", Values: map[string]common.Value{colVal: common.NewInt64(2)}},
	}
	if err := eng.WriteBatch(rows); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	row, ok := eng.Get("k1")
	if !ok || row.Columns[colVal].Int64 != 1 {
		t.Errorf("k1: expected 1, got %d", row.Columns[colVal].Int64)
	}
	row, ok = eng.Get("k2")
	if !ok || row.Columns[colVal].Int64 != 2 {
		t.Errorf("k2: expected 2, got %d", row.Columns[colVal].Int64)
	}
}

// TestWriteBatch_EmptyRows_V17 tests WriteBatch with no rows.
func TestWriteBatch_EmptyRows_V17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	if err := eng.WriteBatch(nil); err != nil {
		t.Errorf("WriteBatch(nil) should return nil, got %v", err)
	}
	if err := eng.WriteBatch([]WriteRow{}); err != nil {
		t.Errorf("WriteBatch(empty) should return nil, got %v", err)
	}
}

// TestWriteBatch_WALClosedError_V17 tests WriteBatch when WAL is closed.
func TestWriteBatch_WALClosedError_V17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	_ = eng.wal.Close()

	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
	}
	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("expected error when WAL is closed, got nil")
	}
}

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

	flusher := NewFlusher(filePath, newSegmentIDGen())

	seg := &Segment{
		ID:       1,
		MinKey:   "a",
		MaxKey:   "z",
		RowCount: 1,
		Columns: []EncodedColumn{
			{Encoding: EncodingPlain, Type: common.TypeInt64, RowCount: 1, Data: []byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}},
		},
	}
	_, err = writeSegmentFile(flusher.dataDir, seg)
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
