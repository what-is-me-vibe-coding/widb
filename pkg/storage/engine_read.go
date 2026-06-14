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
func (e *Engine) Scan(start, end string) []ScanEntry {
	entries, _ := e.ScanWithError(start, end)
	return entries
}

// ScanWithError 扫描指定键范围内的所有行，同时返回迭代过程中遇到的错误。
func (e *Engine) ScanWithError(start, end string) ([]ScanEntry, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.scanRangeUnlocked(start, end)
}
