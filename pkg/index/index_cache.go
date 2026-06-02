package index

import (
	"fmt"
	"sync/atomic"

	"github.com/bits-and-blooms/bloom/v3"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const (
	// defaultCacheCapacity 是 Cache 的默认容量（字节）。
	defaultCacheCapacity = 32 * 1024 * 1024 // 32MB
)

// Cache 缓存索引数据（布隆过滤器、稀疏索引统计信息），
// 提供容量限制和 LRU 淘汰策略，避免内存无限增长。
type Cache struct {
	bloomCache    *common.LRUCache // 缓存布隆过滤器
	sparseCache   *common.LRUCache // 缓存稀疏索引统计信息
	bloomLookups  uint64
	bloomHits     uint64
	sparseLookups uint64
	sparseHits    uint64
}

// CacheConfig 是 Cache 的配置参数。
type CacheConfig struct {
	BloomCapacity  int // 布隆过滤器缓存容量（字节），0 表示使用默认值
	SparseCapacity int // 稀疏索引缓存容量（字节），0 表示使用默认值
}

// NewCache 创建一个 Cache 实例。
func NewCache(cfg CacheConfig) *Cache {
	bloomCap := cfg.BloomCapacity
	if bloomCap <= 0 {
		bloomCap = defaultCacheCapacity
	}
	sparseCap := cfg.SparseCapacity
	if sparseCap <= 0 {
		sparseCap = defaultCacheCapacity
	}
	return &Cache{
		bloomCache:  common.NewLRUCache(bloomCap),
		sparseCache: common.NewLRUCache(sparseCap),
	}
}

// bloomCacheKey 生成布隆过滤器缓存键。
func bloomCacheKey(segID uint64) string {
	return fmt.Sprintf("bloom:%d", segID)
}

// sparseCacheKey 生成稀疏索引缓存键。
func sparseCacheKey(segID uint64, colID uint32) string {
	return fmt.Sprintf("sparse:%d:%d", segID, colID)
}

// GetBloom 从缓存中获取布隆过滤器。
func (ic *Cache) GetBloom(segID uint64) (*bloom.BloomFilter, bool) {
	atomic.AddUint64(&ic.bloomLookups, 1)
	val, ok := ic.bloomCache.Get(bloomCacheKey(segID))
	if !ok {
		return nil, false
	}
	atomic.AddUint64(&ic.bloomHits, 1)
	return val.(*bloom.BloomFilter), true
}

// PutBloom 将布隆过滤器放入缓存。
func (ic *Cache) PutBloom(segID uint64, filter *bloom.BloomFilter) {
	if filter == nil {
		return
	}
	data, err := filter.MarshalBinary()
	size := 0
	if err == nil {
		size = len(data)
	} else {
		size = 1024 // 估算值
	}
	ic.bloomCache.Put(bloomCacheKey(segID), filter, size)
}

// InvalidateBloom 使指定 Segment 的布隆过滤器缓存失效。
func (ic *Cache) InvalidateBloom(segID uint64) {
	ic.bloomCache.Delete(bloomCacheKey(segID))
}

// GetSparse 从缓存中获取稀疏索引统计信息。
func (ic *Cache) GetSparse(segID uint64, colID uint32) (ColumnSparseStat, bool) {
	atomic.AddUint64(&ic.sparseLookups, 1)
	val, ok := ic.sparseCache.Get(sparseCacheKey(segID, colID))
	if !ok {
		return ColumnSparseStat{}, false
	}
	atomic.AddUint64(&ic.sparseHits, 1)
	return val.(ColumnSparseStat), true
}

// PutSparse 将稀疏索引统计信息放入缓存。
func (ic *Cache) PutSparse(segID uint64, colID uint32, stat ColumnSparseStat) {
	size := 64 // 估算：两个 Value + 元数据
	ic.sparseCache.Put(sparseCacheKey(segID, colID), stat, size)
}

// InvalidateSparse 使指定 Segment 的所有稀疏索引缓存失效。
func (ic *Cache) InvalidateSparse(segID uint64, columnCount int) {
	for i := 0; i < columnCount; i++ {
		ic.sparseCache.Delete(sparseCacheKey(segID, uint32(i)))
	}
}

// Clear 清空所有缓存。
func (ic *Cache) Clear() {
	ic.bloomCache.Clear()
	ic.sparseCache.Clear()
}

// CacheStats 是 Cache 的统计信息。
type CacheStats struct {
	BloomHitCount     uint64
	BloomMissCount    uint64
	BloomLookupCount  uint64
	BloomHitRate      float64
	BloomEntryCount   int
	BloomTotalSize    int
	SparseHitCount    uint64
	SparseMissCount   uint64
	SparseLookupCount uint64
	SparseHitRate     float64
	SparseEntryCount  int
	SparseTotalSize   int
}

// Stats 返回缓存统计信息。
func (ic *Cache) Stats() CacheStats {
	bHit, bMiss := ic.bloomCache.Stats()
	sHit, sMiss := ic.sparseCache.Stats()
	return CacheStats{
		BloomHitCount:     bHit,
		BloomMissCount:    bMiss,
		BloomLookupCount:  atomic.LoadUint64(&ic.bloomLookups),
		BloomHitRate:      ic.bloomCache.HitRate(),
		BloomEntryCount:   ic.bloomCache.Len(),
		BloomTotalSize:    ic.bloomCache.TotalSize(),
		SparseHitCount:    sHit,
		SparseMissCount:   sMiss,
		SparseLookupCount: atomic.LoadUint64(&ic.sparseLookups),
		SparseHitRate:     ic.sparseCache.HitRate(),
		SparseEntryCount:  ic.sparseCache.Len(),
		SparseTotalSize:   ic.sparseCache.TotalSize(),
	}
}
