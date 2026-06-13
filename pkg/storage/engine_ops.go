package storage

import (
	"fmt"
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
	count := 0
	for _, lvl := range e.segmentLevels {
		if lvl == 0 {
			count++
		}
	}
	return count
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
