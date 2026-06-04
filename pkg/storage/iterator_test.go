package storage

import (
	"sort"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestMemTableIterator(t *testing.T) {
	mem := NewMemTable()
	_, _, _ = mem.Put("c", Row{Columns: map[string]common.Value{colVal: common.NewInt64(3)}})
	_, _, _ = mem.Put("a", Row{Columns: map[string]common.Value{colVal: common.NewInt64(1)}})
	_, _, _ = mem.Put("e", Row{Columns: map[string]common.Value{colVal: common.NewInt64(5)}})
	_, _, _ = mem.Put("b", Row{Columns: map[string]common.Value{colVal: common.NewInt64(2)}})
	_, _, _ = mem.Put("d", Row{Columns: map[string]common.Value{colVal: common.NewInt64(4)}})

	it := newMemTableIterator(mem, "b", "d")

	var keys []string
	if !it.Next() {
		t.Fatal("expected at least one entry")
	}
	keys = append(keys, it.Entry().Key)
	for it.Next() {
		keys = append(keys, it.Entry().Key)
	}

	if it.Err() != nil {
		t.Fatalf("unexpected error: %v", it.Err())
	}

	expected := []string{"b", "c", "d"}
	if len(keys) != len(expected) {
		t.Fatalf("expected %d keys, got %d: %v", len(expected), len(keys), keys)
	}
	for i, k := range keys {
		if k != expected[i] {
			t.Errorf("key[%d]: got %q, want %q", i, k, expected[i])
		}
	}
}

func TestMemTableIteratorEmpty(t *testing.T) {
	mem := NewMemTable()
	it := newMemTableIterator(mem, "a", "z")

	if it.Next() {
		t.Error("expected no entries from empty memtable")
	}
}

func TestMemTableIteratorNoOverlap(t *testing.T) {
	mem := NewMemTable()
	_, _, _ = mem.Put("x", Row{Columns: map[string]common.Value{colVal: common.NewInt64(1)}})
	_, _, _ = mem.Put("y", Row{Columns: map[string]common.Value{colVal: common.NewInt64(2)}})

	it := newMemTableIterator(mem, "a", "c")
	if it.Next() {
		t.Error("expected no entries for non-overlapping range")
	}
}

func TestSegmentIterator(t *testing.T) {
	seg := buildTestSegment(t, []string{"a", "c", "e", "g", "i"}, []int64{1, 3, 5, 7, 9})
	colMeta := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	it := newSegmentIterator(seg, colMeta, "c", "g")

	var keys []string
	if !it.Next() {
		t.Fatal("expected at least one entry")
	}
	keys = append(keys, it.Entry().Key)
	for it.Next() {
		keys = append(keys, it.Entry().Key)
	}

	if it.Err() != nil {
		t.Fatalf("unexpected error: %v", it.Err())
	}

	expected := []string{"c", "e", "g"}
	if len(keys) != len(expected) {
		t.Fatalf("expected %d keys, got %d: %v", len(expected), len(keys), keys)
	}
	for i, k := range keys {
		if k != expected[i] {
			t.Errorf("key[%d]: got %q, want %q", i, k, expected[i])
		}
	}
}

func TestSegmentIteratorFullRange(t *testing.T) {
	keys := []string{"a", "b", "c"}
	seg := buildTestSegment(t, keys, []int64{1, 2, 3})
	colMeta := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	it := newSegmentIterator(seg, colMeta, "a", "c")

	var result []string
	for it.Next() {
		result = append(result, it.Entry().Key)
	}

	if len(result) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(result))
	}
}

func TestSegmentIteratorNoOverlap(t *testing.T) {
	seg := buildTestSegment(t, []string{"m", "n", "o"}, []int64{13, 14, 15})
	colMeta := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	it := newSegmentIterator(seg, colMeta, "a", "c")
	if it.Next() {
		t.Error("expected no entries for non-overlapping range")
	}
}

func TestSegmentIteratorSingleKey(t *testing.T) {
	seg := buildTestSegment(t, []string{"a", "b", "c"}, []int64{1, 2, 3})
	colMeta := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	it := newSegmentIterator(seg, colMeta, "b", "b")

	if !it.Next() {
		t.Fatal("expected one entry")
	}
	if it.Entry().Key != "b" {
		t.Errorf("expected key b, got %q", it.Entry().Key)
	}
	if it.Next() {
		t.Error("expected no more entries")
	}
}

func TestMergeIteratorSingleIterator(t *testing.T) {
	entries := []ScanEntry{
		{Key: "a", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(1)}}},
		{Key: "b", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(2)}}},
		{Key: "c", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(3)}}},
	}

	it := newSliceIterator(entries)
	mi := NewMergeIterator(it)
	defer mi.Close()

	var keys []string
	for mi.Next() {
		keys = append(keys, mi.Entry().Key)
	}

	if mi.Err() != nil {
		t.Fatalf("unexpected error: %v", mi.Err())
	}
	expected := []string{"a", "b", "c"}
	if len(keys) != len(expected) {
		t.Fatalf("expected %d keys, got %d", len(expected), len(keys))
	}
	for i, k := range keys {
		if k != expected[i] {
			t.Errorf("key[%d]: got %q, want %q", i, k, expected[i])
		}
	}
}

func TestMergeIteratorTwoIterators(t *testing.T) {
	it1 := newSliceIterator([]ScanEntry{
		{Key: "a", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(1)}}},
		{Key: "c", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(3)}}},
	})
	it2 := newSliceIterator([]ScanEntry{
		{Key: "b", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(2)}}},
		{Key: "d", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(4)}}},
	})

	mi := NewMergeIterator(it1, it2)
	defer mi.Close()

	var keys []string
	for mi.Next() {
		keys = append(keys, mi.Entry().Key)
	}

	expected := []string{"a", "b", "c", "d"}
	if len(keys) != len(expected) {
		t.Fatalf("expected %d keys, got %d: %v", len(expected), len(keys), keys)
	}
	for i, k := range keys {
		if k != expected[i] {
			t.Errorf("key[%d]: got %q, want %q", i, k, expected[i])
		}
	}
}

func TestMergeIteratorDedup(t *testing.T) {
	it1 := newSliceIterator([]ScanEntry{
		{Key: "a", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(1)}}},
		{Key: "b", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(2)}}},
	})
	it2 := newSliceIterator([]ScanEntry{
		{Key: "a", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(10)}}},
		{Key: "c", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(30)}}},
	})

	mi := NewMergeIterator(it1, it2)
	defer mi.Close()

	var results []ScanEntry
	for mi.Next() {
		results = append(results, mi.Entry())
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(results))
	}

	if results[0].Key != "a" || results[0].Value.Columns[colVal].Int64 != 10 {
		t.Errorf("key a: expected val=10 (from it2), got val=%d", results[0].Value.Columns[colVal].Int64)
	}
	if results[1].Key != "b" || results[1].Value.Columns[colVal].Int64 != 2 {
		t.Errorf("key b: expected val=2, got val=%d", results[1].Value.Columns[colVal].Int64)
	}
	if results[2].Key != "c" || results[2].Value.Columns[colVal].Int64 != 30 {
		t.Errorf("key c: expected val=30, got val=%d", results[2].Value.Columns[colVal].Int64)
	}
}

func TestMergeIteratorThreeWayDedup(t *testing.T) {
	it1 := newSliceIterator([]ScanEntry{
		{Key: "a", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(1)}}},
		{Key: "b", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(2)}}},
	})
	it2 := newSliceIterator([]ScanEntry{
		{Key: "a", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(10)}}},
		{Key: "c", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(30)}}},
	})
	it3 := newSliceIterator([]ScanEntry{
		{Key: "a", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(100)}}},
		{Key: "d", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(40)}}},
	})

	mi := NewMergeIterator(it1, it2, it3)
	defer mi.Close()

	var results []ScanEntry
	for mi.Next() {
		results = append(results, mi.Entry())
	}

	if len(results) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(results))
	}

	if results[0].Value.Columns[colVal].Int64 != 100 {
		t.Errorf("key a: expected val=100 (from it3), got val=%d", results[0].Value.Columns[colVal].Int64)
	}
}

func TestMergeIteratorEmpty(t *testing.T) {
	mi := NewMergeIterator()
	defer mi.Close()

	if mi.Next() {
		t.Error("expected no entries from empty merge iterator")
	}
}

func TestMergeIteratorAllEmpty(t *testing.T) {
	it1 := newSliceIterator([]ScanEntry{})
	it2 := newSliceIterator([]ScanEntry{})

	mi := NewMergeIterator(it1, it2)
	defer mi.Close()

	if mi.Next() {
		t.Error("expected no entries from all-empty iterators")
	}
}

func TestMergeIteratorStress(t *testing.T) {
	const numIters = 5
	const keysPerIter = 100

	var iters []ScanIterator
	for i := 0; i < numIters; i++ {
		var entries []ScanEntry
		for j := 0; j < keysPerIter; j++ {
			key := string(rune('a' + j%26))
			entries = append(entries, ScanEntry{
				Key:   key,
				Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(int64(i*1000 + j))}},
			})
		}
		sort.Slice(entries, func(a, b int) bool {
			return entries[a].Key < entries[b].Key
		})
		iters = append(iters, newSliceIterator(entries))
	}

	mi := NewMergeIterator(iters...)
	defer mi.Close()

	var results []ScanEntry
	for mi.Next() {
		results = append(results, mi.Entry())
	}

	if mi.Err() != nil {
		t.Fatalf("unexpected error: %v", mi.Err())
	}

	if len(results) == 0 {
		t.Fatal("expected some results")
	}

	for i := 1; i < len(results); i++ {
		if results[i].Key < results[i-1].Key {
			t.Errorf("results not sorted at index %d: %q > %q", i, results[i-1].Key, results[i].Key)
		}
		if results[i].Key == results[i-1].Key {
			t.Errorf("duplicate key %q in results", results[i].Key)
		}
	}

	lastVal := results[len(results)-1].Value.Columns[colVal].Int64
	if lastVal < 4000 {
		t.Errorf("expected last value from highest-priority iterator (>=4000), got %d", lastVal)
	}
}

func buildTestSegment(t *testing.T, keys []string, values []int64) *Segment {
	t.Helper()
	if len(keys) != len(values) {
		t.Fatalf("keys and values must have same length")
	}

	rowCount := uint32(len(keys))
	minKey := keys[0]
	maxKey := keys[len(keys)-1]

	builder := NewSegmentBuilder(1, minKey, maxKey)
	builder.SetKeys(keys)

	enc, err := EncodeColumn(common.TypeInt64, values, rowCount, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	builder.AddEncodedColumn(enc)

	seg, err := builder.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return seg
}
