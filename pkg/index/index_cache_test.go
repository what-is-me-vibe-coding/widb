package index

import (
	"testing"

	"github.com/bits-and-blooms/bloom/v3"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestCacheBloomOperations(t *testing.T) {
	cache := NewCache(CacheConfig{
		BloomCapacity:  1024,
		SparseCapacity: 1024,
	})

	// 创建布隆过滤器
	filter := bloom.NewWithEstimates(100, 0.01)
	filter.Add([]byte("key1"))
	filter.Add([]byte("key2"))

	// Put and Get
	cache.PutBloom(1, filter)
	got, ok := cache.GetBloom(1)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if !got.Test([]byte("key1")) {
		t.Fatal("expected bloom filter to contain key1")
	}

	// Miss
	_, ok = cache.GetBloom(999)
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestCacheBloomEviction(t *testing.T) {
	cache := NewCache(CacheConfig{
		BloomCapacity:  200,
		SparseCapacity: 1024,
	})

	// 插入多个布隆过滤器，触发淘汰
	for i := 0; i < 10; i++ {
		filter := bloom.NewWithEstimates(100, 0.01)
		filter.Add([]byte("key"))
		cache.PutBloom(uint64(i), filter)
	}

	stats := cache.Stats()
	if stats.BloomEntryCount >= 10 {
		t.Fatalf("expected some eviction, got %d entries", stats.BloomEntryCount)
	}
}

func TestCacheBloomInvalidate(t *testing.T) {
	cache := NewCache(CacheConfig{
		BloomCapacity:  1024,
		SparseCapacity: 1024,
	})

	filter := bloom.NewWithEstimates(100, 0.01)
	cache.PutBloom(1, filter)

	cache.InvalidateBloom(1)

	_, ok := cache.GetBloom(1)
	if ok {
		t.Fatal("expected miss after invalidation")
	}
}

func TestCacheSparseOperations(t *testing.T) {
	cache := NewCache(CacheConfig{
		BloomCapacity:  1024,
		SparseCapacity: 1024,
	})

	stat := ColumnSparseStat{
		MinValue:  common.NewInt64(10),
		MaxValue:  common.NewInt64(100),
		NullCount: 5,
		HasValues: true,
	}

	// Put and Get
	cache.PutSparse(1, 0, stat)
	got, ok := cache.GetSparse(1, 0)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if !got.HasValues {
		t.Fatal("expected HasValues=true")
	}
	if got.NullCount != 5 {
		t.Fatalf("expected NullCount=5, got %d", got.NullCount)
	}

	// Miss
	_, ok = cache.GetSparse(999, 0)
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestCacheSparseInvalidate(t *testing.T) {
	cache := NewCache(CacheConfig{
		BloomCapacity:  1024,
		SparseCapacity: 1024,
	})

	stat := ColumnSparseStat{
		MinValue:  common.NewInt64(10),
		MaxValue:  common.NewInt64(100),
		HasValues: true,
	}
	for i := 0; i < 3; i++ {
		cache.PutSparse(1, uint32(i), stat)
	}

	cache.InvalidateSparse(1, 3)

	for i := 0; i < 3; i++ {
		_, ok := cache.GetSparse(1, uint32(i))
		if ok {
			t.Fatalf("expected miss after invalidation for col %d", i)
		}
	}
}

func TestCacheStats(t *testing.T) {
	cache := NewCache(CacheConfig{
		BloomCapacity:  1024,
		SparseCapacity: 1024,
	})

	filter := bloom.NewWithEstimates(100, 0.01)
	cache.PutBloom(1, filter)
	cache.GetBloom(1) // hit
	cache.GetBloom(1) // hit
	cache.GetBloom(2) // miss

	stat := ColumnSparseStat{MinValue: common.NewInt64(1), HasValues: true}
	cache.PutSparse(1, 0, stat)
	cache.GetSparse(1, 0) // hit
	cache.GetSparse(2, 0) // miss

	stats := cache.Stats()
	if stats.BloomHitCount != 2 {
		t.Fatalf("expected 2 bloom hits, got %d", stats.BloomHitCount)
	}
	if stats.BloomMissCount != 1 {
		t.Fatalf("expected 1 bloom miss, got %d", stats.BloomMissCount)
	}
	if stats.SparseHitCount != 1 {
		t.Fatalf("expected 1 sparse hit, got %d", stats.SparseHitCount)
	}
	if stats.SparseMissCount != 1 {
		t.Fatalf("expected 1 sparse miss, got %d", stats.SparseMissCount)
	}
}

func TestCacheClear(t *testing.T) {
	cache := NewCache(CacheConfig{
		BloomCapacity:  1024,
		SparseCapacity: 1024,
	})

	filter := bloom.NewWithEstimates(100, 0.01)
	cache.PutBloom(1, filter)
	cache.PutSparse(1, 0, ColumnSparseStat{HasValues: true})

	cache.Clear()

	stats := cache.Stats()
	if stats.BloomEntryCount != 0 || stats.SparseEntryCount != 0 {
		t.Fatalf("expected 0 entries after clear, got bloom=%d sparse=%d",
			stats.BloomEntryCount, stats.SparseEntryCount)
	}
}

func TestCachePutNilBloom(t *testing.T) {
	cache := NewCache(CacheConfig{
		BloomCapacity:  1024,
		SparseCapacity: 1024,
	})
	cache.PutBloom(1, nil) // 不应 panic
	_, ok := cache.GetBloom(1)
	if ok {
		t.Fatal("expected miss for nil put")
	}
}

func TestCacheDefaultConfig(t *testing.T) {
	cache := NewCache(CacheConfig{}) // 使用默认配置
	if cache == nil {
		t.Fatal("expected non-nil cache")
	}

	filter := bloom.NewWithEstimates(100, 0.01)
	cache.PutBloom(1, filter)
	_, ok := cache.GetBloom(1)
	if !ok {
		t.Fatal("expected cache hit with default config")
	}
}
