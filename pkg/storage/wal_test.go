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

func TestWALRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	records := []struct {
		tp      byte
		payload string
	}{
		{walTypeWrite, "row1"},
		{walTypeWrite, "row2"},
		{walTypeCommit, "commit1"},
		{walTypeWrite, "row3"},
		{walTypeCheckpoint, "checkpoint"},
	}

	for _, r := range records {
		if err := w.Append(r.tp, []byte(r.payload)); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}
	_ = w.Sync()
	_ = w.Close()

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != len(records) {
		t.Fatalf("expected %d records, got %d", len(records), len(recs))
	}

	for i, r := range records {
		if recs[i].Type != r.tp {
			t.Errorf("record %d: expected type %d, got %d", i, r.tp, recs[i].Type)
		}
		if string(recs[i].Payload) != r.payload {
			t.Errorf("record %d: expected payload %q, got %q", i, r.payload, string(recs[i].Payload))
		}
	}
}

func TestWALRecoveryEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.Close()

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 0 {
		t.Errorf("expected 0 records, got %d", len(recs))
	}
}

func TestWALRecoveryMissingFile(t *testing.T) {
	_, _, err := OpenWAL("/nonexistent/path/file.wal")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestWALAppendAfterRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("before"))
	_ = w.Sync()
	_ = w.Close()

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}

	if err := recovered.AppendWrite([]byte("after")); err != nil {
		t.Fatalf("AppendWrite after recovery failed: %v", err)
	}
	_ = recovered.Sync()
	_ = recovered.Close()

	recovered2, recs2, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("second OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered2.Close() }()

	if len(recs2) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs2))
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

func TestWALCorruptedCRC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("valid record"))
	_ = w.Sync()
	_ = w.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read WAL file: %v", err)
	}

	data[len(data)-1] ^= 0xFF

	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write corrupted file: %v", err)
	}

	// With crash-resilient replay, corrupted records are skipped
	// and valid records before the corruption are returned.
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL should not fail on corrupted CRC: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	// The corrupted record should be skipped, so 0 valid records
	if len(recs) != 0 {
		t.Errorf("expected 0 valid records after CRC corruption, got %d", len(recs))
	}
}

func TestOpenWALWithExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// Create and write some records
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("record1"))
	_ = w.AppendWrite([]byte("record2"))
	_ = w.AppendCheckpoint([]byte("checkpoint1"))
	_ = w.Sync()
	_ = w.Close()

	// Open existing WAL file
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 3 {
		t.Fatalf("expected 3 records, got %d", len(recs))
	}

	if recs[0].Type != walTypeWrite || string(recs[0].Payload) != "record1" {
		t.Errorf("record 0: unexpected type=%d payload=%q", recs[0].Type, recs[0].Payload)
	}
	if recs[2].Type != walTypeCheckpoint {
		t.Errorf("record 2: expected checkpoint type, got %d", recs[2].Type)
	}

	// Verify the WAL can still be appended to
	if err := recovered.AppendWrite([]byte("record3")); err != nil {
		t.Fatalf("AppendWrite after OpenWAL failed: %v", err)
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
