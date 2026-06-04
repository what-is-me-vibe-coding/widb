package storage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestOpenWALIsNotExistError verifies the os.IsNotExist branch in OpenWAL.
func TestOpenWALIsNotExistError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.wal")

	_, _, err := OpenWAL(path)
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error should wrap os.ErrNotExist, got: %v", err)
	}
}

// TestOpenWALPermissionDenied tests OpenWAL on a read-only file,
// triggering a non-NotExist os.OpenFile error.
func TestOpenWALPermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-based test not reliable on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root bypasses file permission checks")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "readonly.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.Close()

	// Make the file read-only so O_RDWR fails with EACCES.
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatalf("Chmod failed: %v", err)
	}
	defer func() { _ = os.Chmod(path, 0644) }() // restore for cleanup

	_, _, err = OpenWAL(path)
	if err == nil {
		t.Fatal("expected error when opening read-only WAL file")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Error("error should not wrap os.ErrNotExist")
	}
}

// TestFindLastCheckpointInvalidPayload tests findLastCheckpoint with
// checkpoint records that have corrupted (non-JSON) payloads.
func TestFindLastCheckpointInvalidPayload(t *testing.T) {
	records := []RawRecord{
		{Type: walTypeCheckpoint, Payload: []byte("not valid json")},
	}
	version, colMeta := findLastCheckpoint(records)
	if version != 0 {
		t.Errorf("expected version 0 for invalid checkpoint, got %d", version)
	}
	if colMeta != nil {
		t.Errorf("expected nil colMeta for invalid checkpoint, got %v", colMeta)
	}
}

// TestFindLastCheckpointMixedValidInvalid tests findLastCheckpoint with
// a mix of valid and invalid checkpoint records, verifying that valid
// records are still processed and invalid ones are skipped.
func TestFindLastCheckpointMixedValidInvalid(t *testing.T) {
	validPayload, err := serializeCheckpointRecord(10, []ColumnMeta{
		{ID: 1, Name: "col1", Type: common.TypeInt64},
	})
	if err != nil {
		t.Fatalf("serializeCheckpointRecord failed: %v", err)
	}

	higherPayload, err := serializeCheckpointRecord(20, []ColumnMeta{
		{ID: 2, Name: "col2", Type: common.TypeString},
	})
	if err != nil {
		t.Fatalf("serializeCheckpointRecord failed: %v", err)
	}

	records := []RawRecord{
		{Type: walTypeCheckpoint, Payload: []byte("invalid json")},
		{Type: walTypeCheckpoint, Payload: validPayload},
		{Type: walTypeCheckpoint, Payload: []byte("also invalid")},
		{Type: walTypeCheckpoint, Payload: higherPayload},
	}

	version, colMeta := findLastCheckpoint(records)
	if version != 20 {
		t.Errorf("expected version 20, got %d", version)
	}
	if len(colMeta) != 1 {
		t.Fatalf("expected 1 column meta, got %d", len(colMeta))
	}
	if colMeta[0].Name != "col2" {
		t.Errorf("expected column name 'col2', got %q", colMeta[0].Name)
	}
}

// TestFindLastCheckpointOnlyInvalidPayloads tests that findLastCheckpoint
// returns zero values when all checkpoint records have invalid payloads.
func TestFindLastCheckpointOnlyInvalidPayloads(t *testing.T) {
	records := []RawRecord{
		{Type: walTypeCheckpoint, Payload: []byte("bad1")},
		{Type: walTypeCheckpoint, Payload: []byte("bad2")},
	}
	version, colMeta := findLastCheckpoint(records)
	if version != 0 {
		t.Errorf("expected version 0, got %d", version)
	}
	if colMeta != nil {
		t.Errorf("expected nil colMeta, got %v", colMeta)
	}
}

// TestFindLastCheckpointWithNonCheckpointRecords tests that findLastCheckpoint
// ignores non-checkpoint record types.
func TestFindLastCheckpointWithNonCheckpointRecords(t *testing.T) {
	validPayload, err := serializeCheckpointRecord(5, []ColumnMeta{
		{ID: 1, Name: "col1", Type: common.TypeInt64},
	})
	if err != nil {
		t.Fatalf("serializeCheckpointRecord failed: %v", err)
	}

	records := []RawRecord{
		{Type: walTypeWrite, Payload: []byte("write data")},
		{Type: walTypeCommit, Payload: []byte("commit data")},
		{Type: walTypeCheckpoint, Payload: validPayload},
		{Type: walTypeWrite, Payload: []byte("more write data")},
	}

	version, colMeta := findLastCheckpoint(records)
	if version != 5 {
		t.Errorf("expected version 5, got %d", version)
	}
	if len(colMeta) != 1 {
		t.Fatalf("expected 1 column meta, got %d", len(colMeta))
	}
}
