package storage

import (
	"fmt"
	"os"
	"path/filepath"
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
	segmentMap    map[uint64]*Segment
	segmentLevels []int
	nextVersion   uint64
	primaryIndex  *index.PrimaryIndex
	bloomIndex    *index.BloomIndex
	sparseIndex   *index.SparseIndex
	columnMeta    []ColumnMeta
	blockCache    *BlockCache
	indexCache    *IndexCache
}

// EngineConfig 是 Engine 的配置参数。
type EngineConfig struct {
	DataDir         string
	MaxMemTableSize int64
	BlockCacheSize  int64 // BlockCache 容量（字节），默认 256MB，<=0 表示不缓存
	IndexCacheSize  int   // IndexCache 容量（条目数），默认 1000，<=0 表示不缓存
}

// NewEngine 创建一个新的存储引擎实例。
func NewEngine(cfg EngineConfig) (*Engine, error) {
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("engine: create data dir: %w", err)
	}

	maxSize := cfg.MaxMemTableSize
	if maxSize <= 0 {
		maxSize = memTableDefaultSize
	}

	blockCacheSize := cfg.BlockCacheSize
	if blockCacheSize == 0 {
		blockCacheSize = 256 * 1024 * 1024 // 默认 256MB
	}

	indexCacheSize := cfg.IndexCacheSize
	if indexCacheSize == 0 {
		indexCacheSize = 1000 // 默认 1000 条目
	}

	eng := &Engine{
		activeMem:    NewMemTableWithSize(maxSize),
		flusher:      NewFlusher(cfg.DataDir),
		compactor:    NewCompactor(cfg.DataDir),
		segmentMap:   make(map[uint64]*Segment),
		nextVersion:  1,
		primaryIndex: index.NewPrimaryIndex(),
		bloomIndex:   index.NewBloomIndex(),
		sparseIndex:  index.NewSparseIndex(),
		blockCache:   NewBlockCache(blockCacheSize),
		indexCache:   NewIndexCache(indexCacheSize),
	}

	// Load existing segments from disk
	if err := eng.loadSegments(); err != nil {
		return nil, fmt.Errorf("engine: load segments: %w", err)
	}

	// Open or create WAL
	walPath := filepath.Join(cfg.DataDir, "wal.log")
	wal, records, err := OpenWAL(walPath)
	if err != nil {
		wal, err = CreateWAL(walPath)
		if err != nil {
			return nil, fmt.Errorf("engine: create wal: %w", err)
		}
	} else {
		// Replay WAL records to recover data
		if err := eng.replayWALRecords(records); err != nil {
			_ = wal.Close()
			return nil, fmt.Errorf("engine: replay wal: %w", err)
		}
	}
	eng.wal = wal

	return eng, nil
}

// Write 向引擎写入一行数据。
func (e *Engine) Write(key string, values map[string]common.Value) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	version := e.nextVersion

	// Write to WAL first (write-ahead logging)
	payload, err := serializeWriteRecord(key, version, values)
	if err != nil {
		return fmt.Errorf("engine write: serialize wal: %w", err)
	}
	if err := e.wal.AppendWrite(payload); err != nil {
		return fmt.Errorf("engine write: wal append: %w", err)
	}
	if err := e.wal.Sync(); err != nil {
		return fmt.Errorf("engine write: wal sync: %w", err)
	}

	e.nextVersion++

	row := Row{
		Version: version,
		Columns: values,
	}

	if e.activeMem.ShouldFlush() {
		if err := e.rotateMemTable(); err != nil {
			return fmt.Errorf("engine write: rotate memtable: %w", err)
		}
	}

	_, _, err = e.activeMem.Put(key, row)
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

	// 预分配 map 并在循环中复用，减少短命对象分配
	columns := make(map[string]common.Value, len(e.columnMeta))

	// Iterate in reverse order: since segment IDs are monotonically increasing,
	// higher IDs appear later in the slice, so reverse iteration checks
	// newer segments first without allocating a sorted copy.
	for i := len(segIDs) - 1; i >= 0; i-- {
		segID := segIDs[i]
		if !e.bloomIndex.MayContainString(segID, key) {
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

		// 清空 map 复用，避免每次迭代分配新 map
		for k := range columns {
			delete(columns, k)
		}
		for colIdx, col := range e.columnMeta {
			// 优先从 BlockCache 获取已解码的列数据
			cacheKey := CacheKey{SegmentID: segID, ColumnIdx: uint32(colIdx)}
			if dc, ok := e.blockCache.get(cacheKey); ok {
				columns[col.Name] = extractValue(dc, rowIdx)
			} else {
				val, err := seg.GetColumnValue(uint32(colIdx), rowIdx)
				if err != nil {
					continue
				}
				columns[col.Name] = val
			}
		}
		return Row{Version: seg.ID, Columns: columns}, true
	}

	return Row{}, false
}

func (e *Engine) findSegmentByID(segID uint64) *Segment {
	return e.segmentMap[segID]
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

	var flushVersion uint64
	if e.nextVersion > 0 {
		flushVersion = e.nextVersion - 1
	}

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

	if len(immutable) == 0 {
		e.mu.Unlock()
		return nil
	}

	e.mu.Unlock()

	for _, mem := range immutable {
		seg, err := e.flusher.Flush(mem, cols)
		if err != nil {
			return fmt.Errorf("engine flush: %w", err)
		}

		e.mu.Lock()
		e.segments = append(e.segments, seg)
		e.segmentMap[seg.ID] = seg
		e.segmentLevels = append(e.segmentLevels, 0)
		if err := e.registerSegmentIndexes(seg, 0); err != nil {
			e.mu.Unlock()
			return fmt.Errorf("engine flush: %w", err)
		}
		e.mu.Unlock()
	}

	// Write checkpoint after successful flush
	e.mu.Lock()
	colMeta := e.columnMeta
	e.mu.Unlock()

	checkpointPayload, err := serializeCheckpointRecord(flushVersion, colMeta)
	if err != nil {
		return fmt.Errorf("engine flush: serialize checkpoint: %w", err)
	}
	if err := e.wal.AppendCheckpoint(checkpointPayload); err != nil {
		return fmt.Errorf("engine flush: write checkpoint: %w", err)
	}
	if err := e.wal.Sync(); err != nil {
		return fmt.Errorf("engine flush: sync checkpoint: %w", err)
	}

	return nil
}

// Compact 执行 Tiered Compaction，将 L0 合并到 L1。
func (e *Engine) Compact(cols []ColumnMeta) error {
	e.mu.Lock()

	// Sync compactor nextID with flusher to avoid segment ID conflicts
	e.compactor.SetNextID(e.flusher.NextID())

	l0Segments, _ := e.collectL0Segments()
	if len(l0Segments) == 0 {
		e.mu.Unlock()
		return nil
	}

	l1Segments, _ := e.collectL1Segments()

	allSegments := make([]*Segment, 0, len(l0Segments)+len(l1Segments))
	allSegments = append(allSegments, l0Segments...)
	allSegments = append(allSegments, l1Segments...)

	// 记录待删除的 segment ID，而非索引，避免并发操作导致索引失效
	compactIDs := make(map[uint64]struct{}, len(allSegments))
	for _, seg := range allSegments {
		compactIDs[seg.ID] = struct{}{}
	}

	e.mu.Unlock()

	newSeg, err := e.compactor.Compact(allSegments, cols)
	if err != nil {
		return fmt.Errorf("engine compact: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// 按 ID 注销索引
	for _, seg := range allSegments {
		e.unregisterSegmentIndexes(seg.ID)
		delete(e.segmentMap, seg.ID)
	}

	// 按 ID 删除 segment，避免并发 Flush/Compact 导致索引偏移
	remaining := make([]*Segment, 0, len(e.segments))
	remainingLevels := make([]int, 0, len(e.segmentLevels))
	for i, seg := range e.segments {
		if _, ok := compactIDs[seg.ID]; !ok {
			remaining = append(remaining, seg)
			remainingLevels = append(remainingLevels, e.segmentLevels[i])
		}
	}
	e.segments = remaining
	e.segmentLevels = remainingLevels

	e.segments = append(e.segments, newSeg)
	e.segmentMap[newSeg.ID] = newSeg
	e.segmentLevels = append(e.segmentLevels, 1)
	if err := e.registerSegmentIndexes(newSeg, 1); err != nil {
		return fmt.Errorf("engine compact: %w", err)
	}

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

// Close 关闭引擎，同步并关闭 WAL。
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.wal.Sync(); err != nil {
		return fmt.Errorf("engine close: sync wal: %w", err)
	}
	if err := e.wal.Close(); err != nil {
		return fmt.Errorf("engine close: close wal: %w", err)
	}

	return nil
}
