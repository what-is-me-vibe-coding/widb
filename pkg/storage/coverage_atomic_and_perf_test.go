package storage

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ---------------------------------------------------------------------------
// 1. WAL.Size() with atomic — concurrent reads without locks
// ---------------------------------------------------------------------------

func TestWALSizeAtomicConcurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	if s := w.Size(); s != 0 {
		t.Fatalf("expected initial size 0, got %d", s)
	}

	if err := w.AppendWrite([]byte("hello")); err != nil {
		t.Fatalf("AppendWrite failed: %v", err)
	}
	sizeAfterOne := w.Size()
	if sizeAfterOne == 0 {
		t.Fatal("expected non-zero size after write")
	}

	if err := w.AppendWrite([]byte("world")); err != nil {
		t.Fatalf("AppendWrite failed: %v", err)
	}
	sizeAfterTwo := w.Size()
	if sizeAfterTwo <= sizeAfterOne {
		t.Fatalf("expected size to increase after second write: before=%d after=%d", sizeAfterOne, sizeAfterTwo)
	}

	const readers = 50
	var wg sync.WaitGroup
	var sizeErrors atomic.Int64
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := w.Size()
			if s < sizeAfterTwo {
				sizeErrors.Add(1)
			}
		}()
	}
	wg.Wait()

	if sizeErrors.Load() > 0 {
		t.Errorf("got %d size errors during concurrent reads", sizeErrors.Load())
	}
}

func TestWALSizeAtomicDuringWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	const writers = 4
	const writesPer = 50
	const readers = 4

	var wg sync.WaitGroup

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < writesPer; j++ {
				_ = w.AppendWrite([]byte("concurrent-size-test"))
			}
		}()
	}

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < writesPer*10; j++ {
				_ = w.Size()
			}
		}()
	}

	wg.Wait()

	finalSize := w.Size()
	if finalSize == 0 {
		t.Fatal("expected non-zero final size after concurrent writes")
	}
}

// ---------------------------------------------------------------------------
// 2. segmentIDGen Next/Current/InitIfLarger with atomic / CAS
// ---------------------------------------------------------------------------

func TestFlusherNextIDBasic(t *testing.T) {
	idGen := newSegmentIDGen()

	if id := idGen.Current(); id != 0 {
		t.Fatalf("expected initial Current 0, got %d", id)
	}
}

func TestFlusherSetNextIDCAS(t *testing.T) {
	idGen := newSegmentIDGen()

	idGen.InitIfLarger(10)
	if id := idGen.Current(); id != 10 {
		t.Fatalf("expected Current 10 after InitIfLarger(10), got %d", id)
	}

	idGen.InitIfLarger(5)
	if id := idGen.Current(); id != 10 {
		t.Fatalf("expected Current 10 after InitIfLarger(5) (smaller), got %d", id)
	}

	idGen.InitIfLarger(10)
	if id := idGen.Current(); id != 10 {
		t.Fatalf("expected Current 10 after InitIfLarger(10) (same), got %d", id)
	}

	idGen.InitIfLarger(20)
	if id := idGen.Current(); id != 20 {
		t.Fatalf("expected Current 20 after InitIfLarger(20), got %d", id)
	}
}

func TestFlusherSetNextIDConcurrent(t *testing.T) {
	idGen := newSegmentIDGen()

	const goroutines = 20
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(val uint64) {
			defer wg.Done()
			idGen.InitIfLarger(val)
		}(uint64(i + 1))
	}
	wg.Wait()

	if id := idGen.Current(); id != goroutines {
		t.Fatalf("expected Current %d after concurrent InitIfLarger, got %d", goroutines, id)
	}
}

func TestFlusherNextIDAfterFlush(t *testing.T) {
	dir := t.TempDir()
	idGen := newSegmentIDGen()
	f := NewFlusher(dir, idGen)

	mem := NewMemTable()
	_, _, _ = mem.Put("k1", Row{Version: 1, Columns: map[string]common.Value{
		colVal: common.NewInt64(1),
	}})
	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	_, err := f.Flush(mem, cols)
	if err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
	if id := idGen.Current(); id != 1 {
		t.Fatalf("expected Current 1 after first flush, got %d", id)
	}

	mem2 := NewMemTable()
	_, _, _ = mem2.Put("k2", Row{Version: 1, Columns: map[string]common.Value{
		colVal: common.NewInt64(2),
	}})
	_, err = f.Flush(mem2, cols)
	if err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
	if id := idGen.Current(); id != 2 {
		t.Fatalf("expected Current 2 after second flush, got %d", id)
	}
}

// ---------------------------------------------------------------------------
// 3. Compress/Decompress edge cases
// ---------------------------------------------------------------------------

func TestCompressEmptyDataReturnsNilNil(t *testing.T) {
	compressed, err := Compress(nil)
	if err != nil {
		t.Fatalf("Compress(nil) returned error: %v", err)
	}
	if compressed != nil {
		t.Errorf("Compress(nil) = %v, want nil", compressed)
	}

	compressed, err = Compress([]byte{})
	if err != nil {
		t.Fatalf("Compress([]byte{}) returned error: %v", err)
	}
	if compressed != nil {
		t.Errorf("Compress([]byte{}) = %v, want nil", compressed)
	}
}

func TestCompressColumnNilInput(t *testing.T) {
	err := CompressColumn(nil)
	if err == nil {
		t.Fatal("expected error for CompressColumn(nil), got nil")
	}
}

func TestDecompressColumnNilInput(t *testing.T) {
	err := DecompressColumn(nil)
	if err == nil {
		t.Fatal("expected error for DecompressColumn(nil), got nil")
	}
}

func TestCompressColumnWithEmptyData(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeInt64,
		RowCount: 0,
		Data:     nil,
	}
	err := CompressColumn(enc)
	if err != nil {
		t.Fatalf("CompressColumn with nil Data: %v", err)
	}
	if enc.Data != nil {
		t.Errorf("expected nil Data after CompressColumn with nil input, got %d bytes", len(enc.Data))
	}
}
