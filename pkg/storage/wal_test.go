package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateWAL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	if w.Size() != 0 {
		t.Errorf("expected size 0, got %d", w.Size())
	}

	_ = w.Close()

	_, err = os.Stat(path)
	if err != nil {
		t.Fatalf("wal file not created: %v", err)
	}
}

func TestWALAppendWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	payload := []byte("hello wal")
	if err := w.AppendWrite(payload); err != nil {
		t.Fatalf("AppendWrite failed: %v", err)
	}

	if err := w.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	if w.Size() == 0 {
		t.Fatal("expected non-zero size after write")
	}
}

func TestWALAppendCommit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	if err := w.AppendCommit([]byte("commit data")); err != nil {
		t.Fatalf("AppendCommit failed: %v", err)
	}
}

func TestWALAppendCheckpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	if err := w.AppendCheckpoint([]byte("checkpoint data")); err != nil {
		t.Fatalf("AppendCheckpoint failed: %v", err)
	}
}

func TestWALLargePayload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	largePayload := make([]byte, maxRecordPayload+1)
	err = w.AppendWrite(largePayload)
	if err == nil {
		t.Fatal("expected error for oversized payload")
	}
}

func TestWALRotate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	w.maxSize = walMetaSize + 100

	for i := 0; i < 10; i++ {
		payload := []byte("test data for rotation")
		if err := w.AppendWrite(payload); err != nil {
			t.Fatalf("AppendWrite #%d failed: %v", i, err)
		}
	}

	_, err = os.Stat(path + ".prev")
	if err != nil {
		t.Fatalf("previous WAL file not created: %v", err)
	}
}

func TestWALConcurrentWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	const goroutines = 10
	const writesPerRoutine = 100
	done := make(chan bool)

	for i := 0; i < goroutines; i++ {
		go func() {
			for j := 0; j < writesPerRoutine; j++ {
				if err := w.AppendWrite([]byte("concurrent")); err != nil {
					t.Errorf("concurrent write failed: %v", err)
				}
			}
			done <- true
		}()
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}

	_ = w.Sync()
	_ = w.Close()

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	expected := goroutines * writesPerRoutine
	if len(recs) != expected {
		t.Errorf("expected %d records, got %d", expected, len(recs))
	}
}

func TestCreateWALInvalidDir(t *testing.T) {
	// Try to create WAL in a non-existent directory
	_, err := CreateWAL("/nonexistent/dir/test.wal")
	if err == nil {
		t.Error("expected error creating WAL in invalid directory")
	}
}

func TestWALMaybeRotate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// Set a very small max size to trigger rotation quickly
	w.maxSize = walMetaSize + 50

	// Write enough data to trigger rotation
	for i := 0; i < 5; i++ {
		if err := w.AppendWrite([]byte("test data for rotation")); err != nil {
			t.Fatalf("AppendWrite #%d failed: %v", i, err)
		}
	}

	_ = w.Close()

	// Verify the .prev file was created (rotation happened)
	_, err = os.Stat(path + ".prev")
	if err != nil {
		t.Fatalf("previous WAL file not created after rotation: %v", err)
	}

	// Verify the current WAL file still exists
	_, err = os.Stat(path)
	if err != nil {
		t.Fatalf("current WAL file not found: %v", err)
	}
}

func TestWALTruncate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// Write some data
	_ = w.AppendWrite([]byte("data to be truncated"))
	_ = w.Sync()

	if w.Size() == 0 {
		t.Fatal("expected non-zero size before truncate")
	}

	// Truncate the WAL
	if err := w.Truncate(); err != nil {
		t.Fatalf("Truncate failed: %v", err)
	}

	if w.Size() != 0 {
		t.Errorf("expected size 0 after truncate, got %d", w.Size())
	}

	// Verify we can still write after truncation
	if err := w.AppendWrite([]byte("after truncate")); err != nil {
		t.Fatalf("AppendWrite after truncate failed: %v", err)
	}

	_ = w.Close()
}

func TestWALSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	initialSize := w.Size()
	if initialSize != 0 {
		t.Errorf("expected initial size 0, got %d", initialSize)
	}

	_ = w.AppendWrite([]byte("test"))

	afterSize := w.Size()
	if afterSize <= initialSize {
		t.Errorf("expected size to increase after write, got %d", afterSize)
	}
}
