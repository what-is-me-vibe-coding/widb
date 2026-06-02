package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
)

// Engine 是存储引擎的核心结构。
type Engine struct {
	mu            sync.RWMutex
	activeMem     *MemTable
	immutable     []*MemTable
	wal           *WAL
	flusher       *Flusher
	compactor     *Compactor
	segments      []*Segment
	segmentLevels []int
	nextVersion   uint64
	primaryIndex  *index.PrimaryIndex
	bloomIndex    *index.BloomIndex
	sparseIndex   *index.SparseIndex
	indexCache    *index.Cache
	blockCache    *BlockCache
	columnMeta    []ColumnMeta
}

// EngineConfig 是 Engine 的配置参数。
type EngineConfig struct {
	DataDir         string
	MaxMemTableSize int64
	BlockCacheCfg   BlockCacheConfig
	IndexCacheCfg   index.CacheConfig
}

// NewEngine 创建一个新的存储引擎实例。
func NewEngine(cfg EngineConfig) (*Engine, error) {
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("engine: create data dir: %w", err)
	}

	walPath := filepath.Join(cfg.DataDir, "wal.log")
	wal, _, err := OpenWAL(walPath)
	if err != nil {
		wal, err = CreateWAL(walPath)
		if err != nil {
			return nil, fmt.Errorf("engine: create wal: %w", err)
		}
	}

	maxSize := cfg.MaxMemTableSize
	if maxSize <= 0 {
		maxSize = memTableDefaultSize
	}

	eng := &Engine{
		activeMem:    NewMemTableWithSize(maxSize),
		wal:          wal,
		flusher:      NewFlusher(cfg.DataDir),
		compactor:    NewCompactor(cfg.DataDir),
		nextVersion:  1,
		primaryIndex: index.NewPrimaryIndex(),
		bloomIndex:   index.NewBloomIndex(),
		sparseIndex:  index.NewSparseIndex(),
		indexCache:   index.NewCache(cfg.IndexCacheCfg),
		blockCache:   NewBlockCache(cfg.BlockCacheCfg),
	}

	return eng, nil
}

// Write 向引擎写入一行数据。
func (e *Engine) Write(key string, values map[string]common.Value) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	row := Row{
		Version: e.nextVersion,
		Columns: values,
	}
	e.nextVersion++

	if e.activeMem.ShouldFlush() {
		if err := e.rotateMemTable(); err != nil {
			return fmt.Errorf("engine write: rotate memtable: %w", err)
		}
	}

	_, _, err := e.activeMem.Put(key, row)
	if err != nil {
		return fmt.Errorf("engine write: %w", err)
	}

	return nil
}

// Get 根据主键查询一行数据，查询路径：MemTable → Immutable → PrimaryIndex → BloomFilter → Segment。
func (e *Engine) Get(key string) (Row, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if row, ok := e.activeMem.Get(key); ok {
		return row, true
	}

	for i := len(e.immutable) - 1; i >= 0; i-- {
		if row, ok := e.immutable[i].Get(key); ok {
			return row, true
		}
	}

	return e.getFromSegments(key)
}

func (e *Engine) getFromSegments(key string) (Row, bool) {
	segIDs := e.primaryIndex.Lookup(key)
	if len(segIDs) == 0 {
		return Row{}, false
	}

	sortedIDs := make([]uint64, len(segIDs))
	copy(sortedIDs, segIDs)
	sort.Slice(sortedIDs, func(i, j int) bool {
		return sortedIDs[i] > sortedIDs[j]
	})

	for _, segID := range sortedIDs {
		// 优先从 IndexCache 查询布隆过滤器
		var mayContain bool
		if cached, ok := e.indexCache.GetBloom(segID); ok {
			mayContain = cached.Test([]byte(key))
		} else {
			mayContain = e.bloomIndex.MayContain(segID, []byte(key))
		}
		if !mayContain {
			continue
		}

		seg := e.findSegmentByID(segID)
		if seg == nil {
			continue
		}

		rowIdx, found := seg.FindRowByKey(key)
		if !found {
			continue
		}

		row := Row{Version: seg.ID}
		if row.Columns == nil {
			row.Columns = make(map[string]common.Value)
		}
		for colIdx, col := range e.columnMeta {
			val, err := e.getColumnValueCached(seg, uint32(colIdx), rowIdx)
			if err != nil {
				continue
			}
			row.Columns[col.Name] = val
		}
		return row, true
	}

	return Row{}, false
}

// getColumnValueCached 优先从 BlockCache 获取解码后的列值，未命中则解码并缓存。
func (e *Engine) getColumnValueCached(seg *Segment, colIdx uint32, rowIdx uint32) (common.Value, error) {
	if block, ok := e.blockCache.Get(seg.ID, colIdx); ok {
		return extractValueFromCachedBlock(block, rowIdx), nil
	}

	val, err := seg.GetColumnValue(colIdx, rowIdx)
	if err != nil {
		return common.NewNull(), err
	}

	// 解码整列并缓存，供后续查询复用
	if colIdx < uint32(len(seg.Columns)) {
		if block, err := decodeColumnForCache(seg, colIdx); err == nil {
			e.blockCache.Put(seg.ID, colIdx, block)
		}
	}

	return val, nil
}

func (e *Engine) findSegmentByID(segID uint64) *Segment {
	for _, seg := range e.segments {
		if seg.ID == segID {
			return seg
		}
	}
	return nil
}

// Scan 扫描指定键范围内的所有行。
// 使用 MergeIterator 合并所有数据源（MemTable、Immutable、Segment），
// 结果按键排序，重复键取最新版本。
func (e *Engine) Scan(start, end string) []struct {
	Key   string
	Value Row
} {
	e.mu.RLock()
	defer e.mu.RUnlock()

	entries := e.ScanRange(start, end)
	results := make([]struct {
		Key   string
		Value Row
	}, len(entries))
	for i, entry := range entries {
		results[i].Key = entry.Key
		results[i].Value = entry.Value
	}
	return results
}

// Flush 将内存表中的数据刷写到磁盘。
func (e *Engine) Flush(cols []ColumnMeta) error {
	e.mu.Lock()

	if e.activeMem.Len() > 0 {
		e.activeMem.Freeze()
		e.immutable = append(e.immutable, e.activeMem)
		e.activeMem = NewMemTableWithSize(e.activeMem.maxSize)
	}

	immutable := e.immutable
	e.immutable = nil

	if len(e.columnMeta) == 0 && len(cols) > 0 {
		e.columnMeta = make([]ColumnMeta, len(cols))
		copy(e.columnMeta, cols)
	}

	e.mu.Unlock()

	for _, mem := range immutable {
		seg, err := e.flusher.Flush(mem, cols)
		if err != nil {
			return fmt.Errorf("engine flush: %w", err)
		}

		e.mu.Lock()
		e.segments = append(e.segments, seg)
		e.segmentLevels = append(e.segmentLevels, 0)
		e.registerSegmentIndexes(seg, 0)
		e.mu.Unlock()
	}

	return nil
}

// Segments 返回所有 Segment 的副本。
func (e *Engine) Segments() []*Segment {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]*Segment, len(e.segments))
	copy(result, e.segments)
	return result
}

// SegmentCount 返回 Segment 的数量。
func (e *Engine) SegmentCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.segments)
}

// L0SegmentCount 返回 L0 层 Segment 的数量。
func (e *Engine) L0SegmentCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.l0Count()
}

// Compact 执行 Tiered Compaction，将 L0 合并到 L1。
func (e *Engine) Compact(cols []ColumnMeta) error {
	e.mu.Lock()

	l0Segments, l0Indices := e.collectL0Segments()
	if len(l0Segments) == 0 {
		e.mu.Unlock()
		return nil
	}

	l1Segments, l1Indices := e.collectL1Segments()

	allSegments := make([]*Segment, 0, len(l0Segments)+len(l1Segments))
	allSegments = append(allSegments, l0Segments...)
	allSegments = append(allSegments, l1Segments...)

	allIndices := make([]int, 0, len(l0Indices)+len(l1Indices))
	allIndices = append(allIndices, l0Indices...)
	allIndices = append(allIndices, l1Indices...)

	e.mu.Unlock()

	newSeg, err := e.compactor.Compact(allSegments, cols)
	if err != nil {
		return fmt.Errorf("engine compact: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	for _, seg := range allSegments {
		e.unregisterSegmentIndexes(seg.ID)
	}

	sort.Slice(allIndices, func(i, j int) bool {
		return allIndices[i] > allIndices[j]
	})
	for _, idx := range allIndices {
		e.segments = append(e.segments[:idx], e.segments[idx+1:]...)
		e.segmentLevels = append(e.segmentLevels[:idx], e.segmentLevels[idx+1:]...)
	}

	e.segments = append(e.segments, newSeg)
	e.segmentLevels = append(e.segmentLevels, 1)
	e.registerSegmentIndexes(newSeg, 1)

	if err := e.compactor.CleanupSegments(allSegments); err != nil {
		return fmt.Errorf("engine compact: cleanup: %w", err)
	}

	return nil
}

// ShouldCompact 判断是否需要执行 Compaction。
func (e *Engine) ShouldCompact() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.l0Count() >= defaultL0CompactionThreshold
}

// MemTableSize 返回当前活跃 MemTable 的大小。
func (e *Engine) MemTableSize() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.activeMem.Size()
}

// Close 关闭引擎，同步并关闭 WAL。
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.blockCache != nil {
		e.blockCache.Clear()
	}
	if e.indexCache != nil {
		e.indexCache.Clear()
	}

	if err := e.wal.Sync(); err != nil {
		return fmt.Errorf("engine close: sync wal: %w", err)
	}
	if err := e.wal.Close(); err != nil {
		return fmt.Errorf("engine close: close wal: %w", err)
	}

	return nil
}

// PrimaryIndex 返回主键索引实例。
func (e *Engine) PrimaryIndex() *index.PrimaryIndex {
	return e.primaryIndex
}

// BloomIndex 返回布隆过滤器索引实例。
func (e *Engine) BloomIndex() *index.BloomIndex {
	return e.bloomIndex
}

// SparseIndex 返回稀疏索引实例。
func (e *Engine) SparseIndex() *index.SparseIndex {
	return e.sparseIndex
}

// ColumnMeta 返回列元数据的副本。
func (e *Engine) ColumnMeta() []ColumnMeta {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]ColumnMeta, len(e.columnMeta))
	copy(result, e.columnMeta)
	return result
}

// BlockCache 返回 BlockCache 实例。
func (e *Engine) BlockCache() *BlockCache {
	return e.blockCache
}

// IndexCache 返回 IndexCache 实例。
func (e *Engine) IndexCache() *index.Cache {
	return e.indexCache
}

// CacheStats 返回缓存统计信息。
func (e *Engine) CacheStats() (BlockCacheStats, index.CacheStats) {
	var bs BlockCacheStats
	var is index.CacheStats
	if e.blockCache != nil {
		bs = e.blockCache.Stats()
	}
	if e.indexCache != nil {
		is = e.indexCache.Stats()
	}
	return bs, is
}

func (e *Engine) rotateMemTable() error {
	if e.activeMem.Len() == 0 {
		return nil
	}

	e.activeMem.Freeze()
	e.immutable = append(e.immutable, e.activeMem)
	e.activeMem = NewMemTableWithSize(e.activeMem.maxSize)
	return nil
}
