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
// 延迟物化优化：Key() 方法仅返回当前行的 key，不触发列数据物化；
// Entry() 方法返回完整的行数据（含列值 map），会触发物化。
// 调用方在仅需 key 时应优先使用 Key()，避免不必要的 map 分配。
type ScanIterator interface {
	Next() bool
	Key() string
	Entry() ScanEntry
	Err() error
	Close()
}

// memTableIterator iterates over a MemTable's rows within a key range.
// 直接引用 mem.Scan() 返回的切片，避免双重拷贝。
type memTableIterator struct {
	pairs []struct {
		Key   string
		Value Row
	}
	pos int
	err error
}

// newMemTableIterator creates an iterator over a MemTable for the given range.
// 直接引用 mem.Scan() 返回的切片，消除 ScanEntry 中间转换的拷贝开销。
func newMemTableIterator(mem *MemTable, start, end string) *memTableIterator {
	return &memTableIterator{pairs: mem.Scan(start, end), pos: -1}
}

func (it *memTableIterator) Next() bool {
	it.pos++
	return it.pos < len(it.pairs)
}

func (it *memTableIterator) Key() string {
	if it.pos < 0 || it.pos >= len(it.pairs) {
		return ""
	}
	return it.pairs[it.pos].Key
}

func (it *memTableIterator) Entry() ScanEntry {
	if it.pos < 0 || it.pos >= len(it.pairs) {
		return ScanEntry{}
	}
	p := &it.pairs[it.pos]
	return ScanEntry{Key: p.Key, Value: p.Value}
}

func (it *memTableIterator) Err() error { return it.err }
func (it *memTableIterator) Close()     { it.pos = -1 }

// segmentIterator iterates over a Segment's rows within a key range.
// 延迟物化优化：Next() 仅记录行索引和 key，不构建 map[string]Value，
// Entry() 时按需构建行数据。同时复用 map 缓冲区，避免每行重新分配。
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
	currentKey  string
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
// 使用共享的 prepareEncodedColumn 和 decodeColumnFromEncoded 减少重复代码。
func (it *segmentIterator) decodeSegmentColumn(i int, decodedCols []decodedColumn) (bool, int) {
	cacheKey := CacheKey{SegmentID: it.seg.ID, ColumnIdx: uint32(i)}
	if dc, ok := it.blockCache.get(cacheKey); ok {
		decodedCols[i] = dc
		return true, 1
	}

	dc, err := decodeColumnFromEncoded(&it.seg.Columns[i], i)
	if err != nil {
		it.err = fmt.Errorf("segment: %w", err)
		it.decodedCols = make([]decodedColumn, 0)
		return false, 0
	}
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

		// 延迟物化：仅记录 key 和行索引，不构建 map
		it.currentKey = key
		it.started = true
		return true
	}
}

// buildRowMap 从解码后的列数据构建当前行的列值映射。
// 每次调用创建新 map，确保返回值可安全持有跨行引用。
func (it *segmentIterator) buildRowMap() map[string]common.Value {
	values := make(map[string]common.Value, len(it.colMeta))
	for colIdx, col := range it.colMeta {
		val := it.seg.getColumnValueFromDecoded(it.decodedCols, uint32(colIdx), uint32(it.rowIdx))
		values[col.Name] = val
	}
	return values
}

func (it *segmentIterator) Entry() ScanEntry {
	if !it.started {
		return ScanEntry{}
	}
	// 延迟物化：仅在 Entry() 被调用时构建行数据
	// 注意：返回的 map 是 rowBuf 的引用，调用方不应持有跨行引用
	rowMap := it.buildRowMap()
	return ScanEntry{Key: it.currentKey, Value: Row{Version: it.seg.ID, Columns: rowMap}}
}

func (it *segmentIterator) Err() error { return it.err }
func (it *segmentIterator) Close()     { it.finished = true }

// Key 返回当前行的主键，不触发列数据物化。
func (it *segmentIterator) Key() string {
	if !it.started {
		return ""
	}
	return it.currentKey
}

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
			mi.heap = append(mi.heap, &mergeHeapEntry{
				it:    it,
				key:   it.Key(),
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
		top.key = it.Key()
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

func (it *sliceIterator) Key() string {
	if it.pos < 0 || it.pos >= len(it.entries) {
		return ""
	}
	return it.entries[it.pos].Key
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
	entries, _ := e.scanRangeUnlocked(start, end)
	return entries
}

// scanRangeUnlocked performs the actual scan without acquiring the lock.
// Caller must hold e.mu.RLock.
// Returns scan results and any error encountered during iteration.
// 优化：使用更精确的容量估算，减少结果切片的扩容次数。
func (e *Engine) scanRangeUnlocked(start, end string) ([]ScanEntry, error) {
	iters := e.buildScanIterators(start, end)
	if len(iters) == 0 {
		return nil, nil
	}

	// 精确估算：仅统计满足范围条件的 Segment 行数
	estimatedSize := e.activeMem.Len()
	for _, imm := range e.immutable {
		estimatedSize += imm.Len()
	}
	for _, seg := range e.segments {
		if seg.MinKey <= end && seg.MaxKey >= start {
			estimatedSize += int(seg.RowCount)
		}
	}
	// 上限截断，防止极端情况下的过度分配
	if estimatedSize > 1<<20 {
		estimatedSize = 1 << 20
	}

	mi := NewMergeIterator(iters...)
	defer mi.Close()

	results := make([]ScanEntry, 0, estimatedSize)
	for mi.Next() {
		results = append(results, mi.Entry())
	}

	if err := mi.Err(); err != nil {
		return nil, fmt.Errorf("scan range [%q,%q]: %w", start, end, err)
	}

	return results, nil
}
