package storage

import "fmt"

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

	// 先注册新 segment 的索引，再注销旧 segment 的索引，
	// 确保任何时刻索引中都有数据可用，避免部分失败导致数据丢失。
	e.segments = append(e.segments, newSeg)
	e.segmentMap[newSeg.ID] = newSeg
	e.segmentLevels = append(e.segmentLevels, 1)
	if err := e.registerSegmentIndexes(newSeg, 1); err != nil {
		// 注册新索引失败，回滚：移除刚添加的 segment
		e.segments = e.segments[:len(e.segments)-1]
		delete(e.segmentMap, newSeg.ID)
		e.segmentLevels = e.segmentLevels[:len(e.segmentLevels)-1]
		return fmt.Errorf("engine compact: %w", err)
	}

	// 新 segment 注册成功后，再注销旧 segment 的索引
	for _, seg := range allSegments {
		e.unregisterSegmentIndexes(seg.ID)
		delete(e.segmentMap, seg.ID)
	}

	// 按 ID 删除旧 segment
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

	if err := e.compactor.CleanupSegments(allSegments); err != nil {
		return fmt.Errorf("engine compact: cleanup: %w", err)
	}

	// 同步 flusher 的 nextID，避免后续 Flush 产生 segment ID 冲突
	e.flusher.SetNextID(e.compactor.NextID())

	return nil
}

// ShouldCompact 判断是否需要执行 Compaction。
func (e *Engine) ShouldCompact() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.l0Count() >= defaultL0CompactionThreshold
}
