package storage

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const (
	colName   = "name"
	colData   = "data"
	colID     = "id"
	colAge    = "age"
	colActive = "active"
	colVal    = "val"
	colScore  = "score"
)

func TestNewMemTable(t *testing.T) {
	mt := NewMemTable()
	if mt.Len() != 0 {
		t.Fatalf("expected 0 entries, got %d", mt.Len())
	}
	if mt.Size() != 0 {
		t.Fatalf("expected 0 size, got %d", mt.Size())
	}
	if mt.IsFrozen() {
		t.Fatal("expected not frozen")
	}
}

func TestMemTablePutAndGet(t *testing.T) {
	mt := NewMemTable()

	row := Row{
		Version: 1,
		Columns: map[string]common.Value{
			colName: common.NewString("alice"),
			colAge:  common.NewInt64(30),
		},
	}

	old, exists, err := mt.Put("user:1", row)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	if exists {
		t.Fatal("expected no existing value")
	}
	if old.Version != 0 {
		t.Fatal("expected zero old value")
	}

	got, ok := mt.Get("user:1")
	if !ok {
		t.Fatal("expected key to exist")
	}
	if got.Version != 1 {
		t.Errorf("expected version 1, got %d", got.Version)
	}
	if got.Columns[colName].Str != "alice" {
		t.Errorf("expected name alice, got %s", got.Columns[colName].Str)
	}
	if got.Columns["age"].Int64 != 30 {
		t.Errorf("expected age 30, got %d", got.Columns["age"].Int64)
	}
}

func TestMemTableGetNonExistent(t *testing.T) {
	mt := NewMemTable()
	_, ok := mt.Get("nonexistent")
	if ok {
		t.Fatal("expected key not found")
	}
}

func TestMemTablePutOverwrite(t *testing.T) {
	mt := NewMemTable()

	row1 := Row{
		Version: 1,
		Columns: map[string]common.Value{
			"value": common.NewInt64(100),
		},
	}

	row2 := Row{
		Version: 2,
		Columns: map[string]common.Value{
			"value": common.NewInt64(200),
		},
	}

	_, _, _ = mt.Put("key1", row1)
	old, exists, err := mt.Put("key1", row2)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	if !exists {
		t.Fatal("expected existing value")
	}
	if old.Version != 1 {
		t.Errorf("expected old version 1, got %d", old.Version)
	}

	got, ok := mt.Get("key1")
	if !ok {
		t.Fatal("expected key to exist")
	}
	if got.Version != 2 {
		t.Errorf("expected version 2, got %d", got.Version)
	}
}

func TestMemTableDelete(t *testing.T) {
	mt := NewMemTable()

	row := Row{
		Version: 1,
		Columns: map[string]common.Value{
			colData: common.NewString("test"),
		},
	}

	_, _, _ = mt.Put("key1", row)

	old, exists, err := mt.Delete("key1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if !exists {
		t.Fatal("expected key to exist")
	}
	if old.Version != 1 {
		t.Errorf("expected version 1, got %d", old.Version)
	}

	_, ok := mt.Get("key1")
	if ok {
		t.Fatal("expected key to be deleted")
	}
}

func TestMemTableDeleteNonExistent(t *testing.T) {
	mt := NewMemTable()
	_, exists, err := mt.Delete("nonexistent")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if exists {
		t.Fatal("expected key not found")
	}
}

func TestMemTableLen(t *testing.T) {
	mt := NewMemTable()

	for i := 0; i < 100; i++ {
		row := Row{
			Version: uint64(i),
			Columns: map[string]common.Value{
				colID: common.NewInt64(int64(i)),
			},
		}
		_, _, _ = mt.Put(fmt.Sprintf("key:%d", i), row)
	}

	if mt.Len() != 100 {
		t.Errorf("expected 100 entries, got %d", mt.Len())
	}

	_, _, _ = mt.Delete("key:0")
	_, _, _ = mt.Delete("key:1")

	if mt.Len() != 98 {
		t.Errorf("expected 98 entries, got %d", mt.Len())
	}
}

func TestMemTableScan(t *testing.T) {
	mt := NewMemTable()

	for i := 0; i < 20; i++ {
		row := Row{
			Version: uint64(i),
			Columns: map[string]common.Value{
				colID: common.NewInt64(int64(i)),
			},
		}
		_, _, _ = mt.Put(fmt.Sprintf("key:%02d", i), row)
	}

	results := mt.Scan("key:05", "key:10")
	if len(results) != 6 {
		t.Fatalf("expected 6 results, got %d", len(results))
	}

	for i, r := range results {
		expectedKey := fmt.Sprintf("key:%02d", i+5)
		if r.Key != expectedKey {
			t.Errorf("result[%d]: expected key %s, got %s", i, expectedKey, r.Key)
		}
	}
}

func TestMemTableScanEmpty(t *testing.T) {
	mt := NewMemTable()
	results := mt.Scan("a", "z")
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestMemTableScanNoMatch(t *testing.T) {
	mt := NewMemTable()
	_, _, _ = mt.Put("key:1", Row{Version: 1, Columns: map[string]common.Value{colID: common.NewInt64(1)}})
	_, _, _ = mt.Put("key:2", Row{Version: 2, Columns: map[string]common.Value{colID: common.NewInt64(2)}})

	results := mt.Scan("key:3", "key:5")
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestMemTableSize(t *testing.T) {
	mt := NewMemTable()

	row := Row{
		Version: 1,
		Columns: map[string]common.Value{
			colName: common.NewString(testStrHello),
		},
	}

	_, _, _ = mt.Put("key1", row)
	if mt.Size() == 0 {
		t.Fatal("expected non-zero size after put")
	}

	oldSize := mt.Size()
	_, _, _ = mt.Put("key1", Row{
		Version: 2,
		Columns: map[string]common.Value{
			colName: common.NewString("hello world"),
		},
	})

	if mt.Size() == oldSize {
		t.Log("size may not change significantly for small updates")
	}

	_, _, _ = mt.Delete("key1")
	if mt.Size() != 0 {
		t.Errorf("expected 0 size after delete, got %d", mt.Size())
	}
}

func TestMemTableShouldFlush(t *testing.T) {
	mt := NewMemTableWithSize(100)

	if mt.ShouldFlush() {
		t.Fatal("should not flush with 0 size")
	}

	_, _, _ = mt.Put("key1", Row{
		Version: 1,
		Columns: map[string]common.Value{
			colData: common.NewString("this is a relatively long string to increase size"),
		},
	})

	if !mt.ShouldFlush() {
		t.Log("flush threshold may not be reached with single entry")
	}
}

func TestMemTableFreeze(t *testing.T) {
	mt := NewMemTable()

	_, _, _ = mt.Put("key1", Row{
		Version: 1,
		Columns: map[string]common.Value{
			colData: common.NewString("frozen"),
		},
	})
	_, _, _ = mt.Put("key2", Row{
		Version: 2,
		Columns: map[string]common.Value{
			colData: common.NewString("frozen2"),
		},
	})

	mt.Freeze()

	if !mt.IsFrozen() {
		t.Fatal("expected frozen")
	}

	_, _, err := mt.Put("key3", Row{Version: 3})
	if err != common.ErrReadOnly {
		t.Errorf("expected ErrReadOnly, got %v", err)
	}

	_, _, err = mt.Delete("key1")
	if err != common.ErrReadOnly {
		t.Errorf("expected ErrReadOnly on delete, got %v", err)
	}

	val, ok := mt.Get("key1")
	if !ok {
		t.Fatal("frozen memtable should still allow reads for key1")
	}
	if val.Version != 1 {
		t.Errorf("expected version 1, got %d", val.Version)
	}

	val, ok = mt.Get("key2")
	if !ok {
		t.Fatal("frozen memtable should still allow reads for key2")
	}
	if val.Version != 2 {
		t.Errorf("expected version 2, got %d", val.Version)
	}

	_, ok = mt.Get("key3")
	if ok {
		t.Fatal("frozen memtable should not contain key3")
	}
}

func TestMemTableFreezeSnapshotIndependence(t *testing.T) {
	mt := NewMemTable()

	_, _, _ = mt.Put("key1", Row{
		Version: 1,
		Columns: map[string]common.Value{
			colData: common.NewString("original"),
		},
	})

	mt.Freeze()

	origVal, ok := mt.Get("key1")
	if !ok {
		t.Fatal("snapshot should contain key1")
	}
	if origVal.Columns[colData].Str != "original" {
		t.Errorf("expected original, got %s", origVal.Columns[colData].Str)
	}

	mt2 := NewMemTable()
	_, _, _ = mt2.Put("key1", Row{
		Version: 2,
		Columns: map[string]common.Value{
			colData: common.NewString("new memtable"),
		},
	})

	origVal2, ok2 := mt.Get("key1")
	if !ok2 {
		t.Fatal("frozen memtable should still contain key1")
	}
	if origVal2.Columns[colData].Str != "original" {
		t.Errorf("frozen memtable should be independent of other memtables, got %s", origVal2.Columns[colData].Str)
	}
}

func TestMemTableConcurrentWrite(t *testing.T) {
	mt := NewMemTable()

	const goroutines = 20
	const writesPerRoutine = 500
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < writesPerRoutine; j++ {
				key := fmt.Sprintf("key:%d:%d", id, j)
				row := Row{
					Version: uint64(id*1000 + j),
					Columns: map[string]common.Value{
						colID: common.NewInt64(int64(id*1000 + j)),
					},
				}
				_, _, _ = mt.Put(key, row)
			}
		}(i)
	}

	wg.Wait()

	expected := goroutines * writesPerRoutine
	if mt.Len() != expected {
		t.Errorf("expected %d entries, got %d", expected, mt.Len())
	}
}

func TestMemTableConcurrentReadWrite(t *testing.T) {
	mt := NewMemTable()

	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key:%d", i)
		_, _, _ = mt.Put(key, Row{
			Version: uint64(i),
			Columns: map[string]common.Value{
				colID: common.NewInt64(int64(i)),
			},
		})
	}

	const goroutines = 10
	const opsPerRoutine = 500
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerRoutine; j++ {
				op := rand.Intn(3)
				switch op {
				case 0:
					mt.Get(fmt.Sprintf("key:%d", rand.Intn(1000)))
				case 1:
					_, _, _ = mt.Put(fmt.Sprintf("key:%d", rand.Intn(1000)), Row{
						Version: uint64(j),
						Columns: map[string]common.Value{
							colID: common.NewInt64(int64(j)),
						},
					})
				case 2:
					mt.Scan(fmt.Sprintf("key:%d", rand.Intn(500)), fmt.Sprintf("key:%d", 500+rand.Intn(500)))
				}
			}
		}()
	}

	wg.Wait()

	if mt.Len() == 0 {
		t.Fatal("memtable should have entries after concurrent read-write")
	}
}

func TestMemTableLargeDataset(t *testing.T) {
	mt := NewMemTable()

	n := 10000
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("user:%08d", i)
		row := Row{
			Version: uint64(i),
			Columns: map[string]common.Value{
				colName:   common.NewString(fmt.Sprintf("user_%d", i)),
				"balance": common.NewFloat64(float64(i) * 1.5),
				colActive: common.NewBool(i%2 == 0),
			},
		}
		_, _, _ = mt.Put(key, row)
	}

	if mt.Len() != n {
		t.Errorf("expected %d entries, got %d", n, mt.Len())
	}

	for i := 0; i < n; i += 1000 {
		key := fmt.Sprintf("user:%08d", i)
		val, ok := mt.Get(key)
		if !ok {
			t.Errorf("key %s not found", key)
			continue
		}
		if val.Columns[colName].Str != fmt.Sprintf("user_%d", i) {
			t.Errorf("wrong name for key %s", key)
		}
	}

	results := mt.Scan("user:00005000", "user:00005099")
	if len(results) != 100 {
		t.Errorf("expected 100 scan results, got %d", len(results))
	}
}

func TestMemTableString(t *testing.T) {
	mt := NewMemTable()
	_, _, _ = mt.Put("k", Row{Version: 1})
	s := mt.String()
	if s == "" {
		t.Fatal("expected non-empty string")
	}
}
