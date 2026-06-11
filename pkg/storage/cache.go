// Package storage 实现存储引擎，包括 WAL、MemTable、Segment、Compaction 与索引缓存。
package storage

import (
	"container/list"
	"sync"
	"sync/atomic"
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
// 使用 RLock 读取缓存数据，减少读路径锁竞争；hits/misses 使用原子操作避免竞态。
func (c *BlockCache) get(key CacheKey) (decodedColumn, bool) {
	if c == nil || c.capacity <= 0 {
		return decodedColumn{}, false
	}

	// 快速路径：读锁查找（允许并发读）
	c.mu.RLock()
	if elem, ok := c.items[key]; ok {
		data := elem.Value.(*cacheEntry).data
		c.mu.RUnlock()
		atomic.AddInt64(&c.hits, 1)
		// 慢路径：短暂写锁更新 LRU 顺序
		c.mu.Lock()
		// 双检：在 RUnlock 和 Lock 之间可能被淘汰
		if elem, ok = c.items[key]; ok {
			c.order.MoveToFront(elem)
		}
		c.mu.Unlock()
		return data, true
	}
	c.mu.RUnlock()
	atomic.AddInt64(&c.misses, 1)
	return decodedColumn{}, false
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
			lastIdx := len(keys) - 1
			if i < lastIdx {
				keys[i] = keys[lastIdx]
			}
			keys[lastIdx] = CacheKey{}
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
// hits/misses 通过原子操作读取，无需持锁，避免与读路径的 RLock 竞争。
func (c *BlockCache) Stats() CacheStats {
	if c == nil {
		return CacheStats{}
	}

	hits := atomic.LoadInt64(&c.hits)
	misses := atomic.LoadInt64(&c.misses)

	c.mu.RLock()
	used := c.used
	capacity := c.capacity
	entries := len(c.items)
	c.mu.RUnlock()

	total := hits + misses
	var hitRate float64
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	return CacheStats{
		Hits:     hits,
		Misses:   misses,
		Size:     used,
		Capacity: capacity,
		Entries:  entries,
		HitRate:  hitRate,
	}
}

// estimateDecodedSize 估算已解码列数据的内存占用。
func estimateDecodedSize(dc decodedColumn) int64 {
	const overhead = 64

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
			dataSize += int64(len(s)) + 16
		}
	case []time.Time:
		dataSize = int64(len(v)) * 24
	default:
		dataSize = 256
	}

	if dc.nulls != nil {
		dataSize += int64(dc.nulls.Len()/8 + 32)
	}

	return overhead + dataSize
}

// IndexCache 缓存 Segment 级别的索引元数据。
type IndexCache struct {
	mu       sync.RWMutex
	capacity int
	used     int
	items    map[uint64]*list.Element
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

	// 快速路径：读锁查找
	c.mu.RLock()
	elem, ok := c.items[segmentID]
	if !ok {
		c.mu.RUnlock()
		return nil, false
	}
	stats := elem.Value.(*indexCacheEntry).stats
	c.mu.RUnlock()

	// 慢路径：短暂写锁更新 LRU 顺序
	c.mu.Lock()
	// 双检：在 RUnlock 和 Lock 之间可能被淘汰
	if elem, ok = c.items[segmentID]; ok {
		c.order.MoveToFront(elem)
	}
	c.mu.Unlock()

	return stats, true
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
