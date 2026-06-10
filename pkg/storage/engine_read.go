package storage

import (
	"sort"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

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

	// Sort segment IDs in ascending order so that newer segments (higher IDs)
	// are checked first when iterating in reverse.
	sort.Slice(segIDs, func(i, j int) bool { return segIDs[i] < segIDs[j] })

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

		// 创建新 map 而非清空复用，避免逐键删除的开销
		columns := make(map[string]common.Value, len(e.columnMeta))
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

	entries := e.scanRangeUnlocked(start, end)
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
