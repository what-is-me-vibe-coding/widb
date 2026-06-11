package storage

import (
	"container/list"
	"sync"
	"time"
)

// CacheKey 标识一个缓存条目，由 Segment ID 和列索引组成。
type CacheKey struct {
	SegmentID uint64
	ColumnIdx uint32
}

// cacheEntry 是 LRU 链表中的缓存条目。
type cacheEntry struct {
	key  CacheKey
	data decodedColumn
	size int64
}

// BlockCache 是 Segment 列数据的 LRU 缓存。
// 缓存已解码的列数据，避免重复解压和解码，提升点查和范围扫描性能。
type BlockCache struct {
	mu       sync.RWMutex
	capacity int64
	used     int64
	items    map[CacheKey]*list.Element
	segIndex map[uint64][]CacheKey // segmentID → cache keys，加速 Invalidate
	order    *list.List
	hits     int64
	misses   int64
}

// NewBlockCache 创建一个指定容量（字节）的 BlockCache。
// capacity <= 0 表示不缓存。
func NewBlockCache(capacity int64) *BlockCache {
	return &BlockCache{
		capacity: capacity,
		items:    make(map[CacheKey]*list.Element),
		segIndex: make(map[uint64][]CacheKey),
		order:    list.New(),
	}
}

// get 从缓存中获取指定列的已解码数据。
// 返回 (decodedColumn, true) 表示命中，(decodedColumn{}, false) 表示未命中。
// 使用 RLock 快速路径检查存在性，仅在命中时升级为写锁以更新 LRU 顺序。
func (c *BlockCache) get(key CacheKey) (decodedColumn, bool) {
	if c == nil || c.capacity <= 0 {
		return decodedColumn{}, false
	}

	// 快速路径：读锁检查是否存在
	c.mu.RLock()
	_, ok := c.items[key]
	c.mu.RUnlock()

	if !ok {
		c.mu.Lock()
		c.misses++
		c.mu.Unlock()
		return decodedColumn{}, false
	}

	// 命中：写锁更新 LRU 顺序和计数
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		// 双检：在 RLock 和 Lock 之间可能被淘汰
		c.misses++
		return decodedColumn{}, false
	}

	c.order.MoveToFront(elem)
	c.hits++
	return elem.Value.(*cacheEntry).data, true
}

// put 将已解码的列数据放入缓存。
// 如果缓存已满，会按 LRU 策略淘汰最久未使用的条目。
func (c *BlockCache) put(key CacheKey, data decodedColumn) {
	if c == nil || c.capacity <= 0 {
		return
	}

	size := estimateDecodedSize(data)

	c.mu.Lock()
	defer c.mu.Unlock()

	// 如果已存在，更新数据并移到前端
	if elem, ok := c.items[key]; ok {
		entry := elem.Value.(*cacheEntry)
		c.used -= entry.size
		entry.data = data
		entry.size = size
		c.used += size
		c.order.MoveToFront(elem)
		return
	}

	// 淘汰旧条目直到有足够空间
	for c.used+size > c.capacity && c.order.Len() > 0 {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		entry := c.order.Remove(oldest).(*cacheEntry)
		delete(c.items, entry.key)
		c.removeFromSegIndex(entry.key)
		c.used -= entry.size
	}

	entry := &cacheEntry{key: key, data: data, size: size}
	elem := c.order.PushFront(entry)
	c.items[key] = elem
	c.used += size
	c.addToSegIndex(key)
}

// addToSegIndex 将缓存键添加到 segment 索引。调用方需持有写锁。
func (c *BlockCache) addToSegIndex(key CacheKey) {
	c.segIndex[key.SegmentID] = append(c.segIndex[key.SegmentID], key)
}

// removeFromSegIndex 从 segment 索引中移除缓存键。调用方需持有写锁。
func (c *BlockCache) removeFromSegIndex(key CacheKey) {
	keys := c.segIndex[key.SegmentID]
	for i, k := range keys {
		if k == key {
			// 用最后一个元素覆盖被删除的位置，然后截断
			// 不需要保持顺序，因为 Invalidate 会删除整个 segment 的所有缓存
			lastIdx := len(keys) - 1
			if i < lastIdx {
				keys[i] = keys[lastIdx]
			}
			keys[lastIdx] = CacheKey{} // 零化防止内存泄漏
			c.segIndex[key.SegmentID] = keys[:lastIdx]
			break
		}
	}
	if len(c.segIndex[key.SegmentID]) == 0 {
		delete(c.segIndex, key.SegmentID)
	}
}

// Invalidate 使指定 Segment 的所有缓存条目失效。
// 在 Compaction 或 Segment 删除时调用。
func (c *BlockCache) Invalidate(segmentID uint64) {
	if c == nil || c.capacity <= 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	keys, ok := c.segIndex[segmentID]
	if !ok {
		return
	}

	for _, key := range keys {
		if elem, ok := c.items[key]; ok {
			entry := c.order.Remove(elem).(*cacheEntry)
			c.used -= entry.size
			delete(c.items, key)
		}
	}
	delete(c.segIndex, segmentID)
}

// Clear 清空所有缓存条目。
func (c *BlockCache) Clear() {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[CacheKey]*list.Element)
	c.segIndex = make(map[uint64][]CacheKey)
	c.order.Init()
	c.used = 0
}

// CacheStats holds cache statistics.
type CacheStats struct {
	Hits     int64
	Misses   int64
	Size     int64
	Capacity int64
	Entries  int
	HitRate  float64
}

// Stats 返回当前缓存的统计信息。
func (c *BlockCache) Stats() CacheStats {
	if c == nil {
		return CacheStats{}
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	total := c.hits + c.misses
	var hitRate float64
	if total > 0 {
		hitRate = float64(c.hits) / float64(total)
	}

	return CacheStats{
		Hits:     c.hits,
		Misses:   c.misses,
		Size:     c.used,
		Capacity: c.capacity,
		Entries:  len(c.items),
		HitRate:  hitRate,
	}
}

// estimateDecodedSize 估算已解码列数据的内存占用。
func estimateDecodedSize(dc decodedColumn) int64 {
	const overhead = 64 // decodedColumn 结构体本身的开销

	if dc.data == nil {
		return overhead
	}

	var dataSize int64
	switch v := dc.data.(type) {
	case []int64:
		dataSize = int64(len(v)) * 8
	case []float64:
		dataSize = int64(len(v)) * 8
	case []uint64:
		dataSize = int64(len(v)) * 8
	case []string:
		for _, s := range v {
			dataSize += int64(len(s)) + 16 // 字符串头开销
		}
	case []time.Time:
		dataSize = int64(len(v)) * 24
	default:
		dataSize = 256 // 未知类型默认估算
	}

	// NULL 位图开销
	if dc.nulls != nil {
		dataSize += int64(dc.nulls.Len()/8 + 32)
	}

	return overhead + dataSize
}

// IndexCache 缓存 Segment 级别的索引元数据。
// 当前 BloomIndex 和 SparseIndex 已在内存中维护，
// IndexCache 主要缓存 Segment Footer 的列统计信息，
// 避免重复解析 Segment 文件。
type IndexCache struct {
	mu       sync.RWMutex
	capacity int
	used     int
	items    map[uint64]*list.Element // key: segmentID
	order    *list.List
}

type indexCacheEntry struct {
	segmentID uint64
	stats     []ColumnStat
}

// NewIndexCache 创建指定容量（条目数）的 IndexCache。
func NewIndexCache(capacity int) *IndexCache {
	return &IndexCache{
		capacity: capacity,
		items:    make(map[uint64]*list.Element),
		order:    list.New(),
	}
}

// GetColumnStats 从缓存中获取指定 Segment 的列统计信息。
// 使用 RLock 快速路径检查存在性，仅在命中时升级为写锁以更新 LRU 顺序。
func (c *IndexCache) GetColumnStats(segmentID uint64) ([]ColumnStat, bool) {
	if c == nil || c.capacity <= 0 {
		return nil, false
	}

	c.mu.RLock()
	if elem, ok := c.items[segmentID]; ok {
		stats := elem.Value.(*indexCacheEntry).stats
		c.mu.RUnlock()

		// LRU 移动需要写锁，单独获取
		c.mu.Lock()
		// 重新检查，因为期间可能被淘汰
		if elem, ok := c.items[segmentID]; ok {
			c.order.MoveToFront(elem)
		}
		c.mu.Unlock()

		return stats, true
	}
	c.mu.RUnlock()

	return nil, false
}

// PutColumnStats 将 Segment 的列统计信息放入缓存。
func (c *IndexCache) PutColumnStats(segmentID uint64, stats []ColumnStat) {
	if c == nil || c.capacity <= 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[segmentID]; ok {
		entry := elem.Value.(*indexCacheEntry)
		entry.stats = stats
		c.order.MoveToFront(elem)
		return
	}

	// LRU 淘汰
	for c.used >= c.capacity && c.order.Len() > 0 {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		entry := c.order.Remove(oldest).(*indexCacheEntry)
		delete(c.items, entry.segmentID)
		c.used--
	}

	entry := &indexCacheEntry{segmentID: segmentID, stats: stats}
	elem := c.order.PushFront(entry)
	c.items[segmentID] = elem
	c.used++
}

// Invalidate 使指定 Segment 的缓存条目失效。
func (c *IndexCache) Invalidate(segmentID uint64) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[segmentID]; ok {
		c.order.Remove(elem)
		delete(c.items, segmentID)
		c.used--
	}
}

// Clear 清空所有缓存条目。
func (c *IndexCache) Clear() {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[uint64]*list.Element)
	c.order.Init()
	c.used = 0
}

// Len 返回当前缓存条目数。
func (c *IndexCache) Len() int {
	if c == nil {
		return 0
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.used
}

// BlockCache 返回 Engine 的 BlockCache 实例。
func (e *Engine) BlockCache() *BlockCache {
	return e.blockCache
}

// IndexCache 返回 Engine 的 IndexCache 实例。
func (e *Engine) IndexCache() *IndexCache {
	return e.indexCache
}

// CacheStats 返回 BlockCache 和 IndexCache 的统计信息。
func (e *Engine) CacheStats() (blockStats CacheStats, indexEntries int) {
	blockStats = e.blockCache.Stats()
	indexEntries = e.indexCache.Len()
	return
}
