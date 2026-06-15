package storage

import (
	"fmt"
	"log"
	"sort"
	"sync/atomic"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
)

// setNextIDAtomic 使用 CAS 将 id 设置到 target，仅当 id 大于当前值时更新。
func setNextIDAtomic(target *atomic.Uint64, id uint64) {
	for {
		current := target.Load()
		if id <= current {
			return
		}
		if target.CompareAndSwap(current, id) {
			return
		}
	}
}

// --- Engine Helpers ---

// addSegment 将 Segment 添加到引擎的内部数据结构并注册索引。
// 调用者必须持有 e.mu 写锁。如果索引注册失败，已添加的数据会被回滚。
func (e *Engine) addSegment(seg *Segment, level int) error {
	e.segments = append(e.segments, seg)
	e.segmentMap[seg.ID] = seg
	e.segmentLevels = append(e.segmentLevels, level)
	if level == 0 {
		e.l0SegmentCount++
	}

	if err := e.registerSegmentIndexes(seg, level); err != nil {
		// 回滚已添加的数据
		e.segments = e.segments[:len(e.segments)-1]
		delete(e.segmentMap, seg.ID)
		e.segmentLevels = e.segmentLevels[:len(e.segmentLevels)-1]
		if level == 0 {
			e.l0SegmentCount--
		}
		return err
	}
	return nil
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

func (e *Engine) l0Count() int {
	return e.l0SegmentCount
}

// --- Engine Batch ---

// WriteRow 是批量写入的单行数据。
type WriteRow struct {
	Key    string
	Values map[string]common.Value
}

// WriteBatch 批量写入多行数据，所有行共享一次 WAL sync，大幅提升批量写入吞吐。
// 优化：释放引擎锁进行 WAL I/O，避免阻塞并发读写；支持 GroupCommitter。
func (e *Engine) WriteBatch(rows []WriteRow) error {
	if len(rows) == 0 {
		return nil
	}

	// Step 1: Allocate versions under lock (brief hold)
	e.mu.Lock()
	baseVersion := e.nextVersion
	e.nextVersion += uint64(len(rows))
	e.mu.Unlock()

	// Step 2: Serialize WAL record (no lock needed, CPU-bound)
	payload, err := serializeBatchWriteRecord(rows, baseVersion)
	if err != nil {
		return fmt.Errorf("engine write batch: serialize: %w", err)
	}

	// Step 3: WAL append + sync (I/O-bound, no engine lock needed)
	if err := e.wal.AppendBatch([]BatchRecord{{Type: walTypeBatchWrite, Payload: payload}}); err != nil {
		return fmt.Errorf("engine write batch: wal: %w", err)
	}

	// 根据同步模式选择同步策略，与 Engine.Write 保持一致
	var syncCh <-chan struct{}
	e.mu.RLock()
	gc := e.groupCommitter
	e.mu.RUnlock()
	if gc != nil {
		syncCh = gc.Submit()
	} else if err := e.wal.Sync(); err != nil {
		return fmt.Errorf("engine write batch: sync: %w", err)
	}

	// Step 4: Put all rows to memtable under lock (brief hold)
	e.mu.Lock()
	for i := range rows {
		if _, _, err := e.activeMem.Put(rows[i].Key, Row{Version: baseVersion + uint64(i), Columns: rows[i].Values}); err != nil {
			e.mu.Unlock()
			return fmt.Errorf("engine write batch: %w", err)
		}
	}
	if e.activeMem.ShouldFlush() {
		if err := e.rotateMemTable(); err != nil {
			e.mu.Unlock()
			return fmt.Errorf("engine write batch: rotate: %w", err)
		}
	}
	e.mu.Unlock()

	// Step 5: Wait for WAL sync completion (outside engine lock, GroupCommit 模式)
	if syncCh != nil {
		<-syncCh
	}

	return nil
}

// --- Engine Index ---

// registerSegmentIndexes 将 Segment 注册到所有索引（主键、布隆、稀疏），
// 并将列统计信息缓存到 IndexCache。
func (e *Engine) registerSegmentIndexes(seg *Segment, level int) error {
	segMeta := index.SegmentMeta{
		ID:     seg.ID,
		MinKey: seg.MinKey,
		MaxKey: seg.MaxKey,
		Level:  level,
	}
	if err := e.primaryIndex.RegisterSegment(segMeta); err != nil {
		return fmt.Errorf("engine: register primary index for segment %d: %w", seg.ID, err)
	}

	if len(seg.Footer.BloomFilter) > 0 {
		if err := e.bloomIndex.RegisterFromBytes(seg.ID, seg.Footer.BloomFilter); err != nil {
			return fmt.Errorf("engine: register bloom index for segment %d: %w", seg.ID, err)
		}
	}

	e.sparseIndex.LoadFromSegment(seg, seg.MinKey, seg.MaxKey, level)

	// 缓存列统计信息到 IndexCache
	if len(seg.Footer.ColumnStats) > 0 {
		stats := make([]ColumnStat, len(seg.Footer.ColumnStats))
		copy(stats, seg.Footer.ColumnStats)
		e.indexCache.PutColumnStats(seg.ID, stats)
	}

	return nil
}

// unregisterSegmentIndexes 从所有索引中注销 Segment，并清除相关缓存。
func (e *Engine) unregisterSegmentIndexes(segID uint64) {
	_ = e.primaryIndex.UnregisterSegment(segID) // 注销失败不影响后续清理
	e.bloomIndex.Unregister(segID)
	e.sparseIndex.UnregisterSegment(segID)
	e.blockCache.Invalidate(segID)
	e.indexCache.Invalidate(segID)
}

// --- Engine Read ---

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

	// 仅在多个 segment 时排序，单 segment 跳过排序减少开销
	if len(segIDs) > 1 {
		sort.Slice(segIDs, func(i, j int) bool { return segIDs[i] < segIDs[j] })
	}

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

		columns := e.fetchColumnsFromSegment(seg, segID, rowIdx)
		return Row{Version: seg.ID, Columns: columns}, true
	}

	return Row{}, false
}

// fetchColumnsFromSegment 从 Segment 中提取指定行所有列的值，优先从 BlockCache 读取。
// 解码后的列数据在大小允许时写入 BlockCache，防止大体积冷数据驱逐热数据。
func (e *Engine) fetchColumnsFromSegment(seg *Segment, segID uint64, rowIdx uint32) map[string]common.Value {
	columns := make(map[string]common.Value, len(e.columnMeta))
	for colIdx, col := range e.columnMeta {
		cacheKey := CacheKey{SegmentID: segID, ColumnIdx: uint32(colIdx)}
		if dc, ok := e.blockCache.get(cacheKey); ok {
			columns[col.Name] = extractValue(dc, rowIdx)
			continue
		}
		val, err := seg.GetColumnValue(uint32(colIdx), rowIdx)
		if err != nil {
			continue
		}
		columns[col.Name] = val
		if dc, ok := seg.getColCache(uint32(colIdx)); ok {
			if estimateDecodedSize(dc) <= e.blockCacheMaxEntrySize {
				e.blockCache.put(cacheKey, dc)
			}
		}
	}
	return columns
}

func (e *Engine) findSegmentByID(segID uint64) *Segment {
	return e.segmentMap[segID]
}

// Scan 扫描指定键范围内的所有行，直接返回 ScanEntry 切片，避免额外结构体复制。
// 注意：此方法静默丢弃迭代错误。如需获取错误信息，请使用 ScanWithError。
func (e *Engine) Scan(start, end string) []ScanEntry {
	entries, err := e.ScanWithError(start, end)
	if err != nil {
		log.Printf("engine scan [%q,%q]: %v", start, end, err)
	}
	return entries
}

// ScanWithError 扫描指定键范围内的所有行，同时返回迭代过程中遇到的错误。
func (e *Engine) ScanWithError(start, end string) ([]ScanEntry, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.scanRangeUnlocked(start, end)
}
