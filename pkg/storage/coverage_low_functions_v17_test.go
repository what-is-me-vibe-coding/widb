package storage

import (
	"os"
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
