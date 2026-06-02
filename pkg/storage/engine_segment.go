package storage

import (
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
)

func (e *Engine) registerSegmentIndexes(seg *Segment, level int) {
	segMeta := index.SegmentMeta{
		ID:     seg.ID,
		MinKey: seg.MinKey,
		MaxKey: seg.MaxKey,
		Level:  level,
	}
	_ = e.primaryIndex.RegisterSegment(segMeta)

	if len(seg.Footer.BloomFilter) > 0 {
		_ = e.bloomIndex.RegisterFromBytes(seg.ID, seg.Footer.BloomFilter)
		// 同步到 IndexCache
		if filter, ok := e.bloomIndex.GetFilter(seg.ID); ok {
			e.indexCache.PutBloom(seg.ID, filter)
		}
	}

	e.sparseIndex.LoadFromSegment(seg, seg.MinKey, seg.MaxKey, level)
	// 同步稀疏索引到 IndexCache
	for _, stat := range seg.Footer.ColumnStats {
		colID := stat.ColumnID
		var dt common.DataType
		if int(colID) < len(seg.Columns) {
			dt = seg.Columns[colID].Type
		}
		css := index.ColumnSparseStat{NullCount: stat.NullCount}
		if len(stat.Min) > 0 && len(stat.Max) > 0 {
			css.MinValue = index.BytesToValue(stat.Min, dt)
			css.MaxValue = index.BytesToValue(stat.Max, dt)
			css.HasValues = true
		}
		e.indexCache.PutSparse(seg.ID, colID, css)
	}
}

func (e *Engine) unregisterSegmentIndexes(segID uint64) {
	_ = e.primaryIndex.UnregisterSegment(segID)
	e.bloomIndex.Unregister(segID)
	e.sparseIndex.UnregisterSegment(segID)
	e.indexCache.InvalidateBloom(segID)
	e.indexCache.InvalidateSparse(segID, len(e.columnMeta))
	e.blockCache.InvalidateSegment(segID, len(e.columnMeta))
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

func (e *Engine) collectL0Segments() ([]*Segment, []int) {
	var segments []*Segment
	var indices []int
	for i, lvl := range e.segmentLevels {
		if lvl == 0 {
			segments = append(segments, e.segments[i])
			indices = append(indices, i)
		}
	}
	return segments, indices
}

func (e *Engine) collectL1Segments() ([]*Segment, []int) {
	var segments []*Segment
	var indices []int
	for i, lvl := range e.segmentLevels {
		if lvl == 1 {
			segments = append(segments, e.segments[i])
			indices = append(indices, i)
		}
	}
	return segments, indices
}
