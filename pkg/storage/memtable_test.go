package storage

import (
	"fmt"
	"sync"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestSkipListPutGet(t *testing.T) {
	sl := NewSkipList()

	row := Row{Version: 1, Columns: map[string]common.Value{"name": common.NewString("alice")}}
	sl.Put("key1", row)

	got, err := sl.Get("key1")
	if err != nil {
		t.Fatalf("Get(key1) failed: %v", err)
	}
	if got.Version != 1 {
		t.Errorf("Version = %d, want 1", got.Version)
	}
	if got.Columns["name"].Str != "alice" {
		t.Errorf("name = %s, want alice", got.Columns["name"].Str)
	}
}

func TestSkipListGetNotFound(t *testing.T) {
	sl := NewSkipList()

	_, err := sl.Get("notexist")
	if err != common.ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestSkipListPutUpdate(t *testing.T) {
	sl := NewSkipList()

	sl.Put("key1", Row{Version: 1, Columns: map[string]common.Value{"val": common.NewInt64(10)}})
	sl.Put("key1", Row{Version: 2, Columns: map[string]common.Value{"val": common.NewInt64(20)}})

	got, err := sl.Get("key1")
	if err != nil {
		t.Fatalf("Get(key1) failed: %v", err)
	}
	if got.Version != 2 {
		t.Errorf("Version = %d, want 2", got.Version)
	}
	if got.Columns["val"].Int64 != 20 {
		t.Errorf("val = %d, want 20", got.Columns["val"].Int64)
	}
}

func TestSkipListDelete(t *testing.T) {
	sl := NewSkipList()

	sl.Put("key1", Row{Version: 1})
	if err := sl.Delete("key1"); err != nil {
		t.Fatalf("Delete(key1) failed: %v", err)
	}

	_, err := sl.Get("key1")
	if err != common.ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestSkipListDeleteNotFound(t *testing.T) {
	sl := NewSkipList()

	err := sl.Delete("notexist")
	if err != common.ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestSkipListLen(t *testing.T) {
	sl := NewSkipList()

	if sl.Len() != 0 {
		t.Errorf("Len = %d, want 0", sl.Len())
	}

	for i := 0; i < 100; i++ {
		sl.Put(fmt.Sprintf("key%03d", i), Row{Version: 1})
	}

	if sl.Len() != 100 {
		t.Errorf("Len = %d, want 100", sl.Len())
	}
}

func TestSkipListIter(t *testing.T) {
	sl := NewSkipList()

	keys := []string{"b", "a", "c", "e", "d"}
	for _, k := range keys {
		sl.Put(k, Row{Version: 1})
	}

	iter := sl.Iter()
	var got []string
	for iter.Next() {
		got = append(got, iter.Key())
	}

	if len(got) != 5 {
		t.Fatalf("len = %d, want 5", len(got))
	}

	expected := []string{"a", "b", "c", "d", "e"}
	for i, k := range expected {
		if got[i] != k {
			t.Errorf("got[%d] = %s, want %s", i, got[i], k)
		}
	}
}

func TestSkipListConcurrentPut(t *testing.T) {
	sl := NewSkipList()

	const goroutines = 10
	const entriesPerRoutine = 1000
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for j := 0; j < entriesPerRoutine; j++ {
				key := fmt.Sprintf("key%05d", base*entriesPerRoutine+j)
				sl.Put(key, Row{Version: uint64(base*entriesPerRoutine + j)})
			}
		}(i)
	}
	wg.Wait()

	expected := goroutines * entriesPerRoutine
	if sl.Len() != expected {
		t.Errorf("Len = %d, want %d", sl.Len(), expected)
	}
}

func TestMemTablePutGet(t *testing.T) {
	mt := NewMemTable(0)

	row := Row{
		Version: 1,
		Columns: map[string]common.Value{
			"name": common.NewString("alice"),
			"age":  common.NewInt64(30),
		},
	}
	if err := mt.Put("user1", row); err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	got, err := mt.Get("user1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Version != 1 {
		t.Errorf("Version = %d, want 1", got.Version)
	}
	if got.Columns["name"].Str != "alice" {
		t.Errorf("name = %s, want alice", got.Columns["name"].Str)
	}
	if got.Columns["age"].Int64 != 30 {
		t.Errorf("age = %d, want 30", got.Columns["age"].Int64)
	}
}

func TestMemTableGetNotFound(t *testing.T) {
	mt := NewMemTable(0)

	_, err := mt.Get("notexist")
	if err != common.ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestMemTableDelete(t *testing.T) {
	mt := NewMemTable(0)

	if err := mt.Put("key1", Row{Version: 1}); err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	if err := mt.Delete("key1"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err := mt.Get("key1")
	if err != common.ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestMemTableSize(t *testing.T) {
	mt := NewMemTable(0)

	if mt.Size() != 0 {
		t.Errorf("Size = %d, want 0", mt.Size())
	}

	if err := mt.Put("key1", Row{
		Version: 1,
		Columns: map[string]common.Value{
			"data": common.NewString("hello world"),
		},
	}); err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	if mt.Size() <= 0 {
		t.Errorf("Size should be > 0 after put")
	}
}

func TestMemTableLen(t *testing.T) {
	mt := NewMemTable(0)

	for i := 0; i < 50; i++ {
		_ = mt.Put(fmt.Sprintf("key%03d", i), Row{Version: 1})
	}

	if mt.Len() != 50 {
		t.Errorf("Len = %d, want 50", mt.Len())
	}
}

func TestMemTableNeedFlush(t *testing.T) {
	mt := NewMemTable(1024) // small threshold

	if mt.NeedFlush() {
		t.Fatal("should not need flush initially")
	}

	largeStr := make([]byte, 2048)
	for i := range largeStr {
		largeStr[i] = 'x'
	}

	if err := mt.Put("key1", Row{
		Version: 1,
		Columns: map[string]common.Value{
			"data": common.NewString(string(largeStr)),
		},
	}); err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	if !mt.NeedFlush() {
		t.Fatal("should need flush after large write")
	}
}

func TestMemTableFreeze(t *testing.T) {
	mt := NewMemTable(0)

	if err := mt.Put("key1", Row{Version: 1}); err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	if mt.IsFrozen() {
		t.Fatal("should not be frozen initially")
	}

	mt.Freeze()

	if !mt.IsFrozen() {
		t.Fatal("should be frozen after Freeze()")
	}

	err := mt.Put("key2", Row{Version: 2})
	if err != common.ErrReadOnly {
		t.Errorf("expected ErrReadOnly, got %v", err)
	}

	err = mt.Delete("key1")
	if err != common.ErrReadOnly {
		t.Errorf("expected ErrReadOnly, got %v", err)
	}

	got, err := mt.Get("key1")
	if err != nil {
		t.Fatalf("Get should still work on frozen MemTable: %v", err)
	}
	if got.Version != 1 {
		t.Errorf("Version = %d, want 1", got.Version)
	}
}

func TestMemTableSnapshot(t *testing.T) {
	mt := NewMemTable(0)

	rows := []struct {
		key string
		ver uint64
	}{
		{"c", 1},
		{"a", 2},
		{"b", 3},
	}

	for _, r := range rows {
		_ = mt.Put(r.key, Row{Version: r.ver})
	}

	snapshot := mt.Snapshot()

	if len(snapshot) != 3 {
		t.Fatalf("snapshot len = %d, want 3", len(snapshot))
	}

	expectedKeys := []string{"a", "b", "c"}
	for i, entry := range snapshot {
		if entry.Key != expectedKeys[i] {
			t.Errorf("snapshot[%d].Key = %s, want %s", i, entry.Key, expectedKeys[i])
		}
	}
}

func TestMemTableScan(t *testing.T) {
	mt := NewMemTable(0)

	for i := 0; i < 10; i++ {
		_ = mt.Put(fmt.Sprintf("key%02d", i), Row{Version: uint64(i)})
	}

	var keys []string
	err := mt.Scan("key03", "key07", func(key string, _ Row) error {
		keys = append(keys, key)
		return nil
	})
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	expected := []string{"key03", "key04", "key05", "key06", "key07"}
	if len(keys) != len(expected) {
		t.Fatalf("scan len = %d, want %d", len(keys), len(expected))
	}
	for i, k := range expected {
		if keys[i] != k {
			t.Errorf("keys[%d] = %s, want %s", i, keys[i], k)
		}
	}
}

func TestMemTableConcurrentWrite(t *testing.T) {
	mt := NewMemTable(0)

	const goroutines = 10
	const entriesPerRoutine = 1000
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for j := 0; j < entriesPerRoutine; j++ {
				key := fmt.Sprintf("key%05d", base*entriesPerRoutine+j)
				_ = mt.Put(key, Row{Version: uint64(base*entriesPerRoutine + j)})
			}
		}(i)
	}
	wg.Wait()

	expected := goroutines * entriesPerRoutine
	if mt.Len() != expected {
		t.Errorf("Len = %d, want %d", mt.Len(), expected)
	}
}

func TestMemTableConcurrentReadWrite(t *testing.T) {
	mt := NewMemTable(0)

	for i := 0; i < 100; i++ {
		_ = mt.Put(fmt.Sprintf("key%03d", i), Row{Version: uint64(i)})
	}

	const goroutines = 10
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := fmt.Sprintf("key%03d", j)
				_, err := mt.Get(key)
				if err != nil && err != common.ErrKeyNotFound {
					t.Errorf("Get(%s) error: %v", key, err)
				}
			}
		}(i)
	}
	wg.Wait()
}
