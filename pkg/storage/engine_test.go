package storage

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestEngineWriteAndGet(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	const testUserName = "alice"
	vals := map[string]common.Value{
		colName: common.NewString(testUserName),
		colAge:  common.NewInt64(30),
	}

	if err := eng.Write("key1", vals); err != nil {
		t.Fatalf("write: %v", err)
	}

	row, ok := eng.Get("key1")
	if !ok {
		t.Fatal("key1 not found")
	}
	if row.Version != 1 {
		t.Errorf("expected version 1, got %d", row.Version)
	}
	if v, exists := row.Columns[colName]; !exists || v.Str != testUserName {
		t.Errorf("expected name=%s, got %v", testUserName, v)
	}
	if v, exists := row.Columns[colAge]; !exists || v.Int64 != 30 {
		t.Errorf("expected age=30, got %v", v)
	}
}

func TestEngineWriteAndGetMissingKey(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_, ok := eng.Get("nonexistent")
	if ok {
		t.Error("expected false for nonexistent key")
	}
}

func TestEngineScan(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("b", map[string]common.Value{colVal: common.NewInt64(2)})
	_ = eng.Write("c", map[string]common.Value{colVal: common.NewInt64(3)})

	results := eng.Scan("a", "b")
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Key != "a" {
		t.Errorf("expected first key a, got %s", results[0].Key)
	}
	if results[1].Key != "b" {
		t.Errorf("expected second key b, got %s", results[1].Key)
	}
}

func TestEngineFlush(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_ = eng.Write("key1", map[string]common.Value{
		colVal: common.NewInt64(100),
	})
	_ = eng.Write("key2", map[string]common.Value{
		colVal: common.NewInt64(200),
	})

	cols := []ColumnMeta{
		{ID: 0, Name: colVal, Type: common.TypeInt64},
	}

	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	segs := eng.Segments()
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segs))
	}
	if segs[0].RowCount != 2 {
		t.Errorf("expected rowCount=2, got %d", segs[0].RowCount)
	}
}

func TestEngineFlushMultiple(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_ = eng.Write("k1", map[string]common.Value{colVal: common.NewInt64(1)})

	cols := []ColumnMeta{
		{ID: 0, Name: colVal, Type: common.TypeInt64},
	}

	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush 1: %v", err)
	}

	_ = eng.Write("k2", map[string]common.Value{colVal: common.NewInt64(2)})

	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush 2: %v", err)
	}

	segs := eng.Segments()
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segs))
	}
}

func TestEngineAutoRotate(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir:         t.TempDir(),
		MaxMemTableSize: 1,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_ = eng.Write("k1", map[string]common.Value{
		colVal: common.NewString("hello world this is a long string to trigger rotation"),
	})

	if eng.MemTableSize() == 0 {
		t.Error("expected non-zero memtable size")
	}
}

func TestEngineConcurrentWrite(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	done := make(chan bool)
	n := 100
	for i := 0; i < n; i++ {
		go func(idx int) {
			key := "key" + string(rune('a'+idx%26))
			_ = eng.Write(key, map[string]common.Value{
				colVal: common.NewInt64(int64(idx)),
			})
			done <- true
		}(i)
	}

	for i := 0; i < n; i++ {
		<-done
	}
}
