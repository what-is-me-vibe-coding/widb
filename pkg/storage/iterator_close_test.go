package storage

import (
	"container/heap"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestMemTableIteratorEntryBeforeNext(t *testing.T) {
	mem := NewMemTable()
	_, _, _ = mem.Put("a", Row{Columns: map[string]common.Value{colVal: common.NewInt64(1)}})

	it := newMemTableIterator(mem, "a", "a")

	// Entry() before Next() should return zero value
	entry := it.Entry()
	if entry.Key != "" || entry.Value.Columns != nil {
		t.Errorf("expected zero ScanEntry before Next(), got key=%q value=%v", entry.Key, entry.Value)
	}
}

func TestMemTableIteratorClose(_ *testing.T) {
	mem := NewMemTable()
	_, _, _ = mem.Put("a", Row{Columns: map[string]common.Value{colVal: common.NewInt64(1)}})

	it := newMemTableIterator(mem, "a", "a")
	// Close should not panic
	it.Close()
}

func TestSegmentIteratorEntryBeforeNext(t *testing.T) {
	seg := buildTestSegment(t, []string{"a", "b"}, []int64{1, 2})
	colMeta := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	it := newSegmentIterator(seg, colMeta, "a", "b")

	// Entry() before Next() should return zero value
	entry := it.Entry()
	if entry.Key != "" || entry.Value.Columns != nil {
		t.Errorf("expected zero ScanEntry before Next(), got key=%q", entry.Key)
	}
}

func TestSegmentIteratorClose(t *testing.T) {
	seg := buildTestSegment(t, []string{"a"}, []int64{1})
	colMeta := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	it := newSegmentIterator(seg, colMeta, "a", "a")
	it.Close()
}

func TestMergeIteratorEntryBeforeNext(t *testing.T) {
	entries := []ScanEntry{
		{Key: "a", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(1)}}},
	}
	it := newSliceIterator(entries)
	mi := NewMergeIterator(it)
	defer mi.Close()

	// Entry() before Next() should return zero value
	entry := mi.Entry()
	if entry.Key != "" {
		t.Errorf("expected zero ScanEntry before Next(), got key=%q", entry.Key)
	}
}

func TestMergeIteratorPush(t *testing.T) {
	// Test heap Push operation by creating a mergeHeap and pushing entries
	h := make(mergeHeap, 0)
	heap.Push(&h, &mergeHeapEntry{key: "c", index: 0})
	heap.Push(&h, &mergeHeapEntry{key: "a", index: 1})
	heap.Push(&h, &mergeHeapEntry{key: "b", index: 2})

	if h.Len() != 3 {
		t.Fatalf("expected heap length 3, got %d", h.Len())
	}

	// Pop should return entries in sorted order (min-heap by key)
	first := heap.Pop(&h).(*mergeHeapEntry)
	if first.key != "a" {
		t.Errorf("expected first pop key 'a', got %q", first.key)
	}
	second := heap.Pop(&h).(*mergeHeapEntry)
	if second.key != "b" {
		t.Errorf("expected second pop key 'b', got %q", second.key)
	}
	third := heap.Pop(&h).(*mergeHeapEntry)
	if third.key != "c" {
		t.Errorf("expected third pop key 'c', got %q", third.key)
	}
}

func TestSliceIteratorEntryOutOfRange(t *testing.T) {
	entries := []ScanEntry{
		{Key: "a", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(1)}}},
	}
	it := newSliceIterator(entries)

	// Entry() before Next() should return zero value
	entry := it.Entry()
	if entry.Key != "" {
		t.Errorf("expected zero ScanEntry before Next(), got key=%q", entry.Key)
	}

	// Advance past all entries
	it.Next()
	it.Next()

	// Entry() after all entries should return zero value
	entry = it.Entry()
	if entry.Key != "" {
		t.Errorf("expected zero ScanEntry after all entries, got key=%q", entry.Key)
	}
}

// TestSliceIteratorClose 测试 sliceIterator 的 Close 方法
func TestSliceIteratorClose(_ *testing.T) {
	entries := []ScanEntry{
		{Key: "a", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(1)}}},
	}
	it := newSliceIterator(entries)
	// Close 不应 panic
	it.Close()

	// 多次调用 Close 也不应 panic
	it.Close()
}

// TestSliceIteratorErr 测试 sliceIterator 的 Err 方法
func TestSliceIteratorErr(t *testing.T) {
	entries := []ScanEntry{
		{Key: "a", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(1)}}},
	}
	it := newSliceIterator(entries)
	if it.Err() != nil {
		t.Errorf("expected nil error, got %v", it.Err())
	}
}

// TestMemTableIteratorCloseMultipleCalls 测试 memTableIterator 多次调用 Close
func TestMemTableIteratorCloseMultipleCalls(_ *testing.T) {
	mem := NewMemTable()
	_, _, _ = mem.Put("a", Row{Columns: map[string]common.Value{colVal: common.NewInt64(1)}})
	it := newMemTableIterator(mem, "a", "a")
	it.Close()
	it.Close() // 不应 panic
}

// TestSegmentIteratorCloseMultipleCalls 测试 segmentIterator 多次调用 Close
func TestSegmentIteratorCloseMultipleCalls(t *testing.T) {
	seg := buildTestSegment(t, []string{"a"}, []int64{1})
	colMeta := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	it := newSegmentIterator(seg, colMeta, "a", "a")
	it.Close()
	it.Close() // 不应 panic
}

// TestMergeIteratorCloseWithMultipleIterators 测试 MergeIterator 关闭多个子迭代器
func TestMergeIteratorCloseWithMultipleIterators(t *testing.T) {
	it1 := newSliceIterator([]ScanEntry{
		{Key: "a", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(1)}}},
	})
	it2 := newMemTableIterator(NewMemTable(), "a", "z")
	seg := buildTestSegment(t, []string{"a"}, []int64{1})
	colMeta := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	it3 := newSegmentIterator(seg, colMeta, "a", "a")

	mi := NewMergeIterator(it1, it2, it3)
	// Close 应关闭所有子迭代器，不应 panic
	mi.Close()
	mi.Close() // 多次调用也不应 panic
}

// TestMergeIteratorCloseEmpty 测试空 MergeIterator 的 Close
func TestMergeIteratorCloseEmpty(_ *testing.T) {
	mi := NewMergeIterator()
	mi.Close() // 不应 panic
}
