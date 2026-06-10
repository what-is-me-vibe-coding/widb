package storage

import (
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
)

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
	count := 0
	for _, lvl := range e.segmentLevels {
		if lvl == 0 {
			count++
		}
	}
	return count
}

// MemTableSize 返回当前活跃 MemTable 的大小。
func (e *Engine) MemTableSize() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.activeMem.Size()
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

// SchedulerStats 返回后台调度器的运行统计信息。
// 如果调度器未启动，ok 为 false。
func (e *Engine) SchedulerStats() (stats SchedulerStats, ok bool) {
	e.mu.RLock()
	sched := e.scheduler
	e.mu.RUnlock()

	if sched == nil {
		return SchedulerStats{}, false
	}
	return sched.Stats(), true
}

// StartScheduler 启动后台任务调度器，定时执行刷盘、Compaction 和 WAL 清理。
// 如果调度器已在运行，则不做任何操作。
func (e *Engine) StartScheduler(cfg SchedulerConfig) {
	e.mu.Lock()
	if e.scheduler != nil {
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()

	sched := NewScheduler(e, cfg)
	sched.Start()

	e.mu.Lock()
	e.scheduler = sched
	e.mu.Unlock()
}
