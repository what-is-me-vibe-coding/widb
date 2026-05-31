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

	w.Close()

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
	defer w.Close()

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
	defer w.Close()

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
	defer w.Close()

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
	w.Sync()
	w.Close()

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer recovered.Close()

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
	w.Close()

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer recovered.Close()

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
	w.AppendWrite([]byte("before"))
	w.Sync()
	w.Close()

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer recovered.Close()

	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}

	if err := recovered.AppendWrite([]byte("after")); err != nil {
		t.Fatalf("AppendWrite after recovery failed: %v", err)
	}
	recovered.Sync()
	recovered.Close()

	recovered2, recs2, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("second OpenWAL failed: %v", err)
	}
	defer recovered2.Close()

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
	defer w.Close()

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
	defer w.Close()

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
	defer w.Close()

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

	w.Sync()
	w.Close()

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer recovered.Close()

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
	w.AppendWrite([]byte("valid record"))
	w.Sync()
	w.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read WAL file: %v", err)
	}

	data[len(data)-1] ^= 0xFF

	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write corrupted file: %v", err)
	}

	_, _, err = OpenWAL(path)
	if err == nil {
		t.Fatal("expected error for corrupted CRC")
	}
}
