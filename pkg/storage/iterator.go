package storage

import (
	"container/heap"
	"fmt"
	"sync"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ScanEntry represents a key-value pair from a scan operation.
type ScanEntry struct {
	Key   string
	Value Row
}

// ScanIterator is the interface for iterating over scan results in key order.
type ScanIterator interface {
	Next() bool
	Entry() ScanEntry
	Err() error
	Close()
}

// memTableIterator iterates over a MemTable's rows within a key range.
type memTableIterator struct {
	entries []ScanEntry
	pos     int
	err     error
}

// newMemTableIterator creates an iterator over a MemTable for the given range.
func newMemTableIterator(mem *MemTable, start, end string) *memTableIterator {
	pairs := mem.Scan(start, end)
	entries := make([]ScanEntry, len(pairs))
	for i, p := range pairs {
		entries[i] = ScanEntry{Key: p.Key, Value: p.Value}
	}
	return &memTableIterator{entries: entries, pos: -1}
}

func (it *memTableIterator) Next() bool {
	it.pos++
	return it.pos < len(it.entries)
}

func (it *memTableIterator) Entry() ScanEntry {
	if it.pos < 0 || it.pos >= len(it.entries) {
		return ScanEntry{}
	}
	return ScanEntry{Key: it.entries[it.pos].Key, Value: it.entries[it.pos].Value}
}

func (it *memTableIterator) Err() error { return it.err }
func (it *memTableIterator) Close()     { it.pos = -1 }

// segmentIterator iterates over a Segment's rows within a key range.
// Column decoding is deferred until the first row is accessed, avoiding
// unnecessary work for segments that are skipped by index pruning.
// Thread safety: ensureDecoded uses sync.Once for idempotent lazy init;
// all other methods are NOT safe for concurrent use — callers (e.g. MergeIterator)
// must ensure serial access.
type segmentIterator struct {
	seg         *Segment
	colMeta     []ColumnMeta
	start       string
	end         string
	rowIdx      int
	current     ScanEntry
	err         error
	started     bool
	finished    bool
	decodedCols []decodedColumn
	decodeOnce  sync.Once
	blockCache  *BlockCache
}

// newSegmentIterator creates an iterator over a Segment for the given range.
// Column decoding is deferred until the first row is accessed, avoiding
// unnecessary work for segments that are skipped by index pruning.
func newSegmentIterator(seg *Segment, colMeta []ColumnMeta, start, end string, blockCache *BlockCache) *segmentIterator {
	return &segmentIterator{
		seg:        seg,
		colMeta:    colMeta,
		start:      start,
		end:        end,
		rowIdx:     -1,
		blockCache: blockCache,
	}
}

// ensureDecoded lazily decodes all columns on first access.
// Uses sync.Once to guarantee thread-safe, idempotent initialization.
// On decode failure, decodedCols is set to an empty (non-nil) slice and err is recorded.
// 优先从 BlockCache 获取已解码的列数据，未命中时解码并写入缓存。
// decodeSegmentColumn 从 Segment 中解码单列数据，优先从 BlockCache 获取。
func (it *segmentIterator) decodeSegmentColumn(i int, decodedCols []decodedColumn) (bool, int) {
	cacheKey := CacheKey{SegmentID: it.seg.ID, ColumnIdx: uint32(i)}
	if dc, ok := it.blockCache.get(cacheKey); ok {
		decodedCols[i] = dc
		return true, 1
	}

	src := &it.seg.Columns[i]
	enc := &EncodedColumn{
		Encoding: src.Encoding,
		Type:     src.Type,
		RowCount: src.RowCount,
	}
	if len(src.Data) > 0 {
		enc.Data = make([]byte, len(src.Data))
		copy(enc.Data, src.Data)
	}
	if len(src.Offsets) > 0 {
		enc.Offsets = src.Offsets
	}
	if len(src.Dict) > 0 {
		enc.Dict = src.Dict
	}
	if len(src.Nulls) > 0 {
		enc.Nulls = src.Nulls
	}
	if err := DecompressColumn(enc); err != nil {
		it.err = fmt.Errorf("segment: decompress column %d: %w", i, err)
		it.decodedCols = make([]decodedColumn, 0)
		return false, 0
	}
	decoded, nulls, err := DecodeColumn(enc)
	if err != nil {
		it.err = fmt.Errorf("segment: decode column %d: %w", i, err)
		it.decodedCols = make([]decodedColumn, 0)
		return false, 0
	}
	dc := decodedColumn{data: decoded, nulls: nulls, typ: src.Type, encTyp: src.Encoding}
	decodedCols[i] = dc
	it.blockCache.put(cacheKey, dc)
	return true, 0
}

func (it *segmentIterator) ensureDecoded() {
	it.decodeOnce.Do(func() {
		decodedCols := make([]decodedColumn, len(it.seg.Columns))
		cacheHitCount := 0

		for i := range it.seg.Columns {
			ok, hits := it.decodeSegmentColumn(i, decodedCols)
			if !ok {
				return
			}
			cacheHitCount += hits
		}

		it.decodedCols = decodedCols
	})
}

func (it *segmentIterator) Next() bool {
	if it.finished || it.err != nil {
		return false
	}

	it.ensureDecoded()
	if it.err != nil {
		return false
	}

	for {
		it.rowIdx++
		if it.rowIdx >= len(it.seg.Keys) {
			it.finished = true
			return false
		}

		key := it.seg.Keys[it.rowIdx]
		if key < it.start {
			continue
		}
		if key > it.end {
			it.finished = true
			return false
		}

		values := make(map[string]common.Value, len(it.colMeta))
		for colIdx, col := range it.colMeta {
			val := it.seg.getColumnValueFromDecoded(it.decodedCols, uint32(colIdx), uint32(it.rowIdx))
			values[col.Name] = val
		}

		it.current = ScanEntry{
			Key:   key,
			Value: Row{Version: it.seg.ID, Columns: values},
		}
		it.started = true
		return true
	}
}

func (it *segmentIterator) Entry() ScanEntry {
	if !it.started {
		return ScanEntry{}
	}
	return ScanEntry{Key: it.current.Key, Value: it.current.Value}
}

func (it *segmentIterator) Err() error { return it.err }
func (it *segmentIterator) Close()     { it.finished = true }

// mergeHeapEntry wraps an iterator for use in the merge heap.
type mergeHeapEntry struct {
	it    ScanIterator
	key   string
	index int
}

// mergeHeap implements heap.Interface for merging sorted iterators.
type mergeHeap []*mergeHeapEntry

func (h mergeHeap) Len() int { return len(h) }

func (h mergeHeap) Less(i, j int) bool {
	if h[i].key != h[j].key {
		return h[i].key < h[j].key
	}
	return h[i].index > h[j].index
}

func (h mergeHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *mergeHeap) Push(x any) {
	*h = append(*h, x.(*mergeHeapEntry))
}

func (h *mergeHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// MergeIterator merges multiple sorted iterators into one, deduplicating by key
// with priority given to higher-index iterators (newer data wins).
type MergeIterator struct {
	heap     mergeHeap
	current  ScanEntry
	err      error
	started  bool
	finished bool
	iters    []ScanIterator
}

// NewMergeIterator creates a merge iterator from multiple sorted iterators.
// Iterators are ordered by priority: higher index = higher priority.
// When the same key appears in multiple iterators, the one with the highest
// index wins (i.e., the last iterator's value takes precedence).
func NewMergeIterator(iters ...ScanIterator) *MergeIterator {
	mi := &MergeIterator{
		iters: iters,
		heap:  make(mergeHeap, 0, len(iters)),
	}

	for i, it := range iters {
		if it.Next() {
			entry := it.Entry()
			mi.heap = append(mi.heap, &mergeHeapEntry{
				it:    it,
				key:   entry.Key,
				index: i,
			})
		}
		if it.Err() != nil {
			mi.err = it.Err()
			return mi
		}
	}

	heap.Init(&mi.heap)
	return mi
}

// Next advances the iterator to the next unique key.
func (mi *MergeIterator) Next() bool {
	if mi.finished || mi.err != nil {
		return false
	}

	if !mi.started {
		return mi.advanceFirst()
	}

	return mi.advanceNext()
}

func (mi *MergeIterator) advanceFirst() bool {
	if len(mi.heap) == 0 {
		mi.finished = true
		return false
	}

	mi.started = true
	entry := mi.heap[0]
	mi.current = ScanEntry{Key: entry.key}

	it := entry.it
	mi.current.Value = it.Entry().Value

	mi.advanceHeapTop()
	return true
}

func (mi *MergeIterator) advanceNext() bool {
	if len(mi.heap) == 0 {
		mi.finished = true
		return false
	}

	prevKey := mi.current.Key

	for len(mi.heap) > 0 && mi.heap[0].key == prevKey {
		mi.advanceHeapTop()
	}

	if len(mi.heap) == 0 {
		mi.finished = true
		return false
	}

	entry := mi.heap[0]
	mi.current = ScanEntry{Key: entry.key}

	it := entry.it
	mi.current.Value = it.Entry().Value

	mi.advanceHeapTop()
	return true
}

func (mi *MergeIterator) advanceHeapTop() {
	top := mi.heap[0]
	it := top.it

	if it.Next() {
		top.key = it.Entry().Key
		heap.Fix(&mi.heap, 0)
	} else {
		heap.Pop(&mi.heap)
		if it.Err() != nil && mi.err == nil {
			mi.err = it.Err()
		}
	}
}

// Entry returns the current scan entry.
func (mi *MergeIterator) Entry() ScanEntry {
	if !mi.started {
		return ScanEntry{}
	}
	return ScanEntry{Key: mi.current.Key, Value: mi.current.Value}
}

// Err returns any error encountered during iteration.
func (mi *MergeIterator) Err() error { return mi.err }

// Close closes all underlying iterators.
func (mi *MergeIterator) Close() {
	for _, it := range mi.iters {
		it.Close()
	}
}

// sliceIterator iterates over an in-memory slice of ScanEntry.
type sliceIterator struct {
	entries []ScanEntry
	pos     int
}

func newSliceIterator(entries []ScanEntry) *sliceIterator {
	return &sliceIterator{entries: entries, pos: -1}
}

func (it *sliceIterator) Next() bool {
	it.pos++
	return it.pos < len(it.entries)
}

func (it *sliceIterator) Entry() ScanEntry {
	if it.pos < 0 || it.pos >= len(it.entries) {
		return ScanEntry{}
	}
	return ScanEntry{Key: it.entries[it.pos].Key, Value: it.entries[it.pos].Value}
}

func (it *sliceIterator) Err() error { return nil }
func (it *sliceIterator) Close()     { it.pos = -1 }

// buildScanIterators creates iterators for all data sources in priority order.
// Order: segments (lowest priority) → immutable memtables → active memtable (highest).
func (e *Engine) buildScanIterators(start, end string) []ScanIterator {
	// 预分配迭代器切片容量
	capacity := len(e.segments) + len(e.immutable) + 1
	iters := make([]ScanIterator, 0, capacity)

	for _, seg := range e.segments {
		if seg.MinKey > end || seg.MaxKey < start {
			continue
		}
		iters = append(iters, newSegmentIterator(seg, e.columnMeta, start, end, e.blockCache))
	}

	for i := 0; i < len(e.immutable); i++ {
		iters = append(iters, newMemTableIterator(e.immutable[i], start, end))
	}

	iters = append(iters, newMemTableIterator(e.activeMem, start, end))

	return iters
}

// ScanRange performs a range scan using the MergeIterator for sorted,
// deduplicated results across all data sources.
// Caller must hold e.mu.RLock.
func (e *Engine) ScanRange(start, end string) []ScanEntry {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.scanRangeUnlocked(start, end)
}

// scanRangeUnlocked performs the actual scan without acquiring the lock.
// Caller must hold e.mu.RLock.
func (e *Engine) scanRangeUnlocked(start, end string) []ScanEntry {
	iters := e.buildScanIterators(start, end)
	if len(iters) == 0 {
		return nil
	}

	// Pre-allocate results slice with estimated capacity from MemTable and segments.
	estimatedSize := e.activeMem.Len()
	for _, imm := range e.immutable {
		estimatedSize += imm.Len()
	}
	for _, seg := range e.segments {
		if seg.MinKey <= end && seg.MaxKey >= start {
			estimatedSize += int(seg.RowCount)
		}
	}

	mi := NewMergeIterator(iters...)
	defer mi.Close()

	results := make([]ScanEntry, 0, estimatedSize)
	for mi.Next() {
		results = append(results, mi.Entry())
	}

	if mi.Err() != nil {
		return nil
	}

	return results
}
