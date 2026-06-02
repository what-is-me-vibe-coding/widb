package storage

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestEnginePointQueryFromSegment(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(100)})
	_ = eng.Write("key2", map[string]common.Value{colVal: common.NewInt64(200)})
	_ = eng.Write("key3", map[string]common.Value{colVal: common.NewInt64(300)})

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	row, ok := eng.Get("key1")
	if !ok {
		t.Fatal("key1 not found in segment")
	}
	if v, exists := row.Columns[colVal]; !exists || v.Int64 != 100 {
		t.Errorf("expected val=100, got %v", v)
	}

	row, ok = eng.Get("key2")
	if !ok {
		t.Fatal("key2 not found in segment")
	}
	if v, exists := row.Columns[colVal]; !exists || v.Int64 != 200 {
		t.Errorf("expected val=200, got %v", v)
	}

	row, ok = eng.Get("key3")
	if !ok {
		t.Fatal("key3 not found in segment")
	}
	if v, exists := row.Columns[colVal]; !exists || v.Int64 != 300 {
		t.Errorf("expected val=300, got %v", v)
	}
}

func TestEnginePointQueryMissingKey(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(100)})

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	_, ok := eng.Get("nonexistent")
	if ok {
		t.Error("expected false for nonexistent key in segment")
	}
}

func TestEnginePointQueryMemTablePrecedence(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(100)})

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(999)})

	row, ok := eng.Get("key1")
	if !ok {
		t.Fatal("key1 not found")
	}
	if v, exists := row.Columns[colVal]; !exists || v.Int64 != 999 {
		t.Errorf("expected val=999 from memtable, got %v", v)
	}
}

func TestEnginePointQueryMultipleSegments(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	_ = eng.Write("k1", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("k2", map[string]common.Value{colVal: common.NewInt64(2)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush 1: %v", err)
	}

	_ = eng.Write("k3", map[string]common.Value{colVal: common.NewInt64(3)})
	_ = eng.Write("k4", map[string]common.Value{colVal: common.NewInt64(4)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush 2: %v", err)
	}

	row, ok := eng.Get("k1")
	if !ok {
		t.Fatal("k1 not found")
	}
	if v, exists := row.Columns[colVal]; !exists || v.Int64 != 1 {
		t.Errorf("expected val=1, got %v", v)
	}

	row, ok = eng.Get("k4")
	if !ok {
		t.Fatal("k4 not found")
	}
	if v, exists := row.Columns[colVal]; !exists || v.Int64 != 4 {
		t.Errorf("expected val=4, got %v", v)
	}
}

func TestEnginePointQueryWithCompaction(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("b", map[string]common.Value{colVal: common.NewInt64(2)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush 1: %v", err)
	}

	_ = eng.Write("c", map[string]common.Value{colVal: common.NewInt64(3)})
	_ = eng.Write("d", map[string]common.Value{colVal: common.NewInt64(4)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush 2: %v", err)
	}

	if err := eng.Compact(cols); err != nil {
		t.Fatalf("compact: %v", err)
	}

	row, ok := eng.Get("a")
	if !ok {
		t.Fatal("a not found after compaction")
	}
	if v, exists := row.Columns[colVal]; !exists || v.Int64 != 1 {
		t.Errorf("expected val=1, got %v", v)
	}

	row, ok = eng.Get("d")
	if !ok {
		t.Fatal("d not found after compaction")
	}
	if v, exists := row.Columns[colVal]; !exists || v.Int64 != 4 {
		t.Errorf("expected val=4, got %v", v)
	}
}

func TestEnginePointQueryMultiColumn(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	const testUserName = "alice"
	_ = eng.Write("k1", map[string]common.Value{
		colName: common.NewString(testUserName),
		colAge:  common.NewInt64(30),
		colVal:  common.NewInt64(100),
	})

	cols := []ColumnMeta{
		{ID: 0, Name: colName, Type: common.TypeString},
		{ID: 1, Name: colAge, Type: common.TypeInt64},
		{ID: 2, Name: colVal, Type: common.TypeInt64},
	}

	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	row, ok := eng.Get("k1")
	if !ok {
		t.Fatal("k1 not found")
	}
	if v, exists := row.Columns[colName]; !exists || v.Str != testUserName {
		t.Errorf("expected name=%s, got %v", testUserName, v)
	}
	if v, exists := row.Columns[colAge]; !exists || v.Int64 != 30 {
		t.Errorf("expected age=30, got %v", v)
	}
	if v, exists := row.Columns[colVal]; !exists || v.Int64 != 100 {
		t.Errorf("expected val=100, got %v", v)
	}
}

func TestEnginePointQueryIndexRegistration(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(100)})
	_ = eng.Write("key2", map[string]common.Value{colVal: common.NewInt64(200)})

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	if eng.PrimaryIndex().SegmentCount() != 1 {
		t.Errorf("expected 1 segment in primary index, got %d", eng.PrimaryIndex().SegmentCount())
	}
	if eng.BloomIndex().Len() != 1 {
		t.Errorf("expected 1 bloom filter, got %d", eng.BloomIndex().Len())
	}
	if eng.SparseIndex().StatCount() == 0 {
		t.Error("expected sparse index stats after flush")
	}

	segIDs := eng.PrimaryIndex().Lookup("key1")
	if len(segIDs) != 1 {
		t.Errorf("expected 1 segment for key1, got %d", len(segIDs))
	}
	if !eng.BloomIndex().MayContain(segIDs[0], []byte("key1")) {
		t.Error("bloom filter should contain key1")
	}
}

func TestEnginePointQueryBloomFilterSkip(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(100)})

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	_, ok := eng.Get("key1_false_positive_test")
	if ok {
		t.Error("expected false for key not in segment data")
	}

	segIDs := eng.PrimaryIndex().Lookup("key1_false_positive_test")
	if len(segIDs) > 0 {
		hit, miss := eng.BloomIndex().Stats()
		if miss == 0 {
			t.Error("expected bloom filter miss count > 0 when key is in range but not in data")
		}
		t.Logf("Bloom filter stats: hit=%d, miss=%d", hit, miss)
	}
}

func TestEnginePointQueryAfterCompactIndexUpdate(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush 1: %v", err)
	}

	_ = eng.Write("b", map[string]common.Value{colVal: common.NewInt64(2)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush 2: %v", err)
	}

	if eng.PrimaryIndex().SegmentCount() != 2 {
		t.Errorf("expected 2 segments before compact, got %d", eng.PrimaryIndex().SegmentCount())
	}

	if err := eng.Compact(cols); err != nil {
		t.Fatalf("compact: %v", err)
	}

	if eng.PrimaryIndex().SegmentCount() != 1 {
		t.Errorf("expected 1 segment after compact, got %d", eng.PrimaryIndex().SegmentCount())
	}
	if eng.BloomIndex().Len() != 1 {
		t.Errorf("expected 1 bloom filter after compact, got %d", eng.BloomIndex().Len())
	}

	row, ok := eng.Get("a")
	if !ok {
		t.Fatal("a not found after compact")
	}
	if v, exists := row.Columns[colVal]; !exists || v.Int64 != 1 {
		t.Errorf("expected val=1, got %v", v)
	}

	row, ok = eng.Get("b")
	if !ok {
		t.Fatal("b not found after compact")
	}
	if v, exists := row.Columns[colVal]; !exists || v.Int64 != 2 {
		t.Errorf("expected val=2, got %v", v)
	}
}
