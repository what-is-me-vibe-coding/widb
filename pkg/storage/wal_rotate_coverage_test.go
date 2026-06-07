package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestMaybeRotate_TriggerRotation tests that maybeRotate creates a new WAL file
// when the current file exceeds maxSize.
func TestMaybeRotate_TriggerRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rotate.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// Set a very small maxSize to trigger rotation
	w.maxSize = 1 // 1 byte, any write should trigger rotation

	// Write a record to increase offset beyond maxSize
	if err := w.AppendWrite([]byte("trigger_rotation")); err != nil {
		t.Fatalf("AppendWrite failed: %v", err)
	}

	// After rotation, the WAL should still be functional
	if err := w.AppendWrite([]byte("after_rotation")); err != nil {
		t.Fatalf("AppendWrite after rotation failed: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify the WAL can be opened and records after rotation are present
	w2, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = w2.Close() }()

	// After rotation, the new file should contain the record written after rotation
	if len(records) == 0 {
		t.Error("expected at least 1 record after rotation, got 0")
	}
}

// TestMaybeRotate_NoRotationNeeded tests that maybeRotate does nothing when
// offset is below maxSize.
func TestMaybeRotate_NoRotationNeeded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no_rotate.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// Keep maxSize large so rotation is not triggered
	w.maxSize = 1 << 30 // 1GB

	if err := w.AppendWrite([]byte("small_record")); err != nil {
		t.Fatalf("AppendWrite failed: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify the WAL file exists and has the record
	w2, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if string(records[0].Payload) != "small_record" {
		t.Errorf("payload = %q, want %q", string(records[0].Payload), "small_record")
	}
}

// TestMaybeRotate_MultipleRotations tests that multiple rotations work correctly.
func TestMaybeRotate_MultipleRotations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multi_rotate.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// Set a small maxSize to trigger rotation frequently
	w.maxSize = 100

	// Write multiple records, triggering multiple rotations
	for i := 0; i < 10; i++ {
		payload := []byte("record_" + string(rune('a'+i)))
		if err := w.AppendWrite(payload); err != nil {
			t.Fatalf("AppendWrite #%d failed: %v", i, err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify the WAL can be opened after multiple rotations
	w2, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = w2.Close() }()

	// After rotation, only the records in the current WAL file should be present
	if len(records) == 0 {
		t.Error("expected at least 1 record after multiple rotations, got 0")
	}
}

// TestMaybeRotate_PrevFileCreated tests that the .prev file is created during rotation.
func TestMaybeRotate_PrevFileCreated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prev_file.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// Write a record first so there's data to rotate
	if err := w.AppendWrite([]byte("data_to_rotate")); err != nil {
		t.Fatalf("AppendWrite failed: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	// Set a small maxSize to trigger rotation on next write
	w.maxSize = 1

	// Write another record to trigger rotation
	if err := w.AppendWrite([]byte("trigger")); err != nil {
		t.Fatalf("AppendWrite trigger failed: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// The .prev file should exist after rotation
	prevPath := path + ".prev"
	if _, err := os.Stat(prevPath); os.IsNotExist(err) {
		t.Error("expected .prev file to exist after rotation")
	}
}

// TestMaybeRotate_ContinueWritingAfterRotation tests that writing continues
// correctly after a rotation.
func TestMaybeRotate_ContinueWritingAfterRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "continue.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// Write initial data
	if err := w.AppendWrite([]byte("initial_data")); err != nil {
		t.Fatalf("AppendWrite initial failed: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	// Force rotation
	w.maxSize = 1

	// Write to trigger rotation
	if err := w.AppendWrite([]byte("after_rotate_1")); err != nil {
		t.Fatalf("AppendWrite after rotate 1 failed: %v", err)
	}

	// Reset maxSize to avoid further rotations
	w.maxSize = 1 << 30

	// Write more data after rotation
	if err := w.AppendWrite([]byte("after_rotate_2")); err != nil {
		t.Fatalf("AppendWrite after rotate 2 failed: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Open and verify
	w2, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = w2.Close() }()

	// Should have the records written after rotation
	if len(records) < 2 {
		t.Errorf("expected at least 2 records after rotation, got %d", len(records))
	}
}

// TestOpenWAL_SeekError tests the error path when Seek fails after Truncate.
// This is difficult to trigger directly, so we test the normal OpenWAL flow
// with valid records to ensure the Seek path is covered.
func TestOpenWAL_SeekAfterTruncate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seek_test.wal")

	// Create a WAL with valid records
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := w.AppendWrite([]byte("data")); err != nil {
			t.Fatalf("AppendWrite #%d failed: %v", i, err)
		}
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Open the WAL - this exercises the Truncate + Seek path
	w2, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if len(records) != 5 {
		t.Fatalf("expected 5 records, got %d", len(records))
	}

	// Verify we can append after OpenWAL (which did Seek)
	if err := w2.AppendWrite([]byte("new_data")); err != nil {
		t.Fatalf("AppendWrite after OpenWAL failed: %v", err)
	}
}

// TestApplySingleWriteRecord_OldVersion tests that records with version <=
// lastFlushedVersion are skipped.
func TestApplySingleWriteRecord_OldVersion(t *testing.T) {
	mem := NewMemTable()

	// Write a record with version 10
	mem.Put("key1", Row{Version: 10, Columns: map[string]common.Value{"col1": common.NewInt64(100)}})

	// Apply a record with version 5 (which is <= lastFlushedVersion=10)
	// This should be skipped
	payload, err := serializeWriteRecord("key2", 5, map[string]common.Value{
		"col1": common.NewInt64(200),
	})
	if err != nil {
		t.Fatalf("serializeWriteRecord failed: %v", err)
	}

	v, ok := applySingleWriteRecord(payload, 10, mem)
	if !ok {
		t.Error("applySingleWriteRecord returned ok=false for old version")
	}
	if v != 0 {
		t.Errorf("version = %d, want 0 for skipped record", v)
	}
}
