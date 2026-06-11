package storage

import (
	"fmt"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
)

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
// 支持 GroupCommitter，在引擎锁外等待 WAL sync 完成。
func (e *Engine) WriteBatch(rows []WriteRow) error {
	if len(rows) == 0 {
		return nil
	}
	e.mu.Lock()
	payload, err := serializeBatchWriteRecord(rows, e.nextVersion)
	if err != nil {
		e.mu.Unlock()
		return fmt.Errorf("engine write batch: serialize: %w", err)
	}
	if err := e.wal.AppendBatch([]BatchRecord{{Type: walTypeBatchWrite, Payload: payload}}); err != nil {
		e.mu.Unlock()
		return fmt.Errorf("engine write batch: wal: %w", err)
	}

	// 根据同步模式选择同步策略，与 Engine.Write 保持一致
	var syncCh <-chan struct{}
	if e.groupCommitter != nil {
		syncCh = e.groupCommitter.Submit()
	} else if err := e.wal.Sync(); err != nil {
		e.mu.Unlock()
		return fmt.Errorf("engine write batch: sync: %w", err)
	}

	// 先写入所有行到 MemTable，全部成功后再递增版本号，避免部分失败导致版本号跳跃
	baseVersion := e.nextVersion
	for i := range rows {
		if _, _, err := e.activeMem.Put(rows[i].Key, Row{Version: baseVersion + uint64(i), Columns: rows[i].Values}); err != nil {
			e.mu.Unlock()
			return fmt.Errorf("engine write batch: %w", err)
		}
	}
	e.nextVersion += uint64(len(rows))
	if e.activeMem.ShouldFlush() {
		if err := e.rotateMemTable(); err != nil {
			e.mu.Unlock()
			return fmt.Errorf("engine write batch: rotate: %w", err)
		}
	}

	// GroupCommit 模式下，在引擎锁外等待 WAL sync 完成
	e.mu.Unlock()
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
