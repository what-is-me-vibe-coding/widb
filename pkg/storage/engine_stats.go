package storage

// EngineStats 汇总 LSM 引擎的运行时状态，用于 /admin/stats 等监控端点。
//
// 字段含义：
//   - RowCount：表中所有存活行数（活跃 + 不可变 MemTable + 全部 Segment），可能略高于实际唯一行（删除墓碑未合并前会重复计数）。
//   - SegmentCount：当前已刷盘 Segment 总数（含 L0 与 L1+）。
//   - L0SegmentCount：L0 层 Segment 数量；高值通常意味着 Compaction 滞后。
//   - ImmutableCount：不可变 MemTable 数量（已冻结但尚未刷盘）。
//   - MemTableSize：活跃 MemTable 当前占用字节数。
//   - ActiveRowCount / ImmutableRowCount：分别统计活跃 / 不可变 MemTable 中的行数。
type EngineStats struct {
	RowCount          int64
	SegmentCount      int
	L0SegmentCount    int
	ImmutableCount    int
	MemTableSize      int64
	ActiveRowCount    int64
	ImmutableRowCount int64
}

// Stats 返回 LSM 引擎的运行时状态快照。所有指标在持读锁期间一次性计算，
// 保证调用方拿到的各字段在时间点上一致（无竞态）。
//
// 实现位于独立文件 engine_stats.go 中以避免 engine.go 触发 500 行上限。
func (e *Engine) Stats() EngineStats {
	e.mu.RLock()
	defer e.mu.RUnlock()

	stats := EngineStats{
		SegmentCount:   len(e.segments),
		L0SegmentCount: e.l0SegmentCount,
		ImmutableCount: len(e.immutable),
	}
	if e.activeMem != nil {
		stats.MemTableSize = e.activeMem.Size()
		stats.ActiveRowCount = int64(e.activeMem.Len())
	}
	for _, m := range e.immutable {
		if m == nil {
			continue
		}
		stats.ImmutableRowCount += int64(m.Len())
	}
	for _, seg := range e.segments {
		if seg == nil {
			continue
		}
		stats.RowCount += int64(seg.RowCount)
	}
	stats.RowCount += stats.ActiveRowCount + stats.ImmutableRowCount
	return stats
}
