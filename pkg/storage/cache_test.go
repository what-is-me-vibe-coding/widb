package storage

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestBlockCacheBasic(t *testing.T) {
	cache := NewBlockCache(1024)

	key := CacheKey{SegmentID: 1, ColumnIdx: 0}

	// 缓存未命中
	_, ok := cache.get(key)
	if ok {
		t.Fatal("expected cache miss")
	}

	// 放入缓存
	dc := decodedColumn{
		data:  []int64{1, 2, 3},
		nulls: nil,
		typ:   common.TypeInt64,
	}
	cache.put(key, dc)

	// 缓存命中
	got, ok := cache.get(key)
	if !ok {
		t.Fatal("expected cache hit")
	}
	ints, ok := got.data.([]int64)
	if !ok {
		t.Fatal("expected []int64 data")
	}
	if len(ints) != 3 || ints[0] != 1 || ints[1] != 2 || ints[2] != 3 {
		t.Fatalf("unexpected data: %v", ints)
	}
}

func TestBlockCacheLRUEviction(t *testing.T) {
	// 小容量缓存，测试 LRU 淘汰
	cache := NewBlockCache(200)

	// 放入多个条目
	for i := uint32(0); i < 5; i++ {
		key := CacheKey{SegmentID: 1, ColumnIdx: i}
		dc := decodedColumn{
			data: []int64{1, 2, 3, 4, 5},
			typ:  common.TypeInt64,
		}
		cache.put(key, dc)
	}

	stats := cache.Stats()
	if stats.Entries == 0 {
		t.Fatal("expected some entries in cache")
	}

	// 最早的条目应该被淘汰
	key0 := CacheKey{SegmentID: 1, ColumnIdx: 0}
	_, ok := cache.get(key0)
	if ok {
		t.Fatal("expected column 0 to be evicted")
	}

	// 较新的条目应该还在
	key4 := CacheKey{SegmentID: 1, ColumnIdx: 4}
	_, ok = cache.get(key4)
	if !ok {
		t.Fatal("expected column 4 to be in cache")
	}
}

func TestBlockCacheInvalidate(t *testing.T) {
	cache := NewBlockCache(4096)

	// 放入两个 Segment 的数据
	for i := uint32(0); i < 3; i++ {
		cache.put(CacheKey{SegmentID: 1, ColumnIdx: i}, decodedColumn{
			data: []int64{1}, typ: common.TypeInt64,
		})
		cache.put(CacheKey{SegmentID: 2, ColumnIdx: i}, decodedColumn{
			data: []int64{2}, typ: common.TypeInt64,
		})
	}

	// 使 Segment 1 失效
	cache.Invalidate(1)

	// Segment 1 的数据应该被清除
	_, ok := cache.get(CacheKey{SegmentID: 1, ColumnIdx: 0})
	if ok {
		t.Fatal("expected segment 1 data to be invalidated")
	}

	// Segment 2 的数据应该还在
	_, ok = cache.get(CacheKey{SegmentID: 2, ColumnIdx: 0})
	if !ok {
		t.Fatal("expected segment 2 data to remain")
	}
}

func TestBlockCacheStats(t *testing.T) {
	cache := NewBlockCache(4096)

	key := CacheKey{SegmentID: 1, ColumnIdx: 0}

	// 未命中
	cache.get(key)
	cache.get(key)

	stats := cache.Stats()
	if stats.Misses != 2 {
		t.Fatalf("expected 2 misses, got %d", stats.Misses)
	}

	// 放入并命中
	cache.put(key, decodedColumn{data: []int64{1}, typ: common.TypeInt64})
	cache.get(key)
	cache.get(key)

	stats = cache.Stats()
	if stats.Hits != 2 {
		t.Fatalf("expected 2 hits, got %d", stats.Hits)
	}
	if stats.Misses != 2 {
		t.Fatalf("expected 2 misses, got %d", stats.Misses)
	}
	if stats.HitRate != 0.5 {
		t.Fatalf("expected 0.5 hit rate, got %f", stats.HitRate)
	}
}

func TestBlockCacheNil(t *testing.T) {
	var cache *BlockCache

	// nil 缓存不应 panic
	_, ok := cache.get(CacheKey{SegmentID: 1, ColumnIdx: 0})
	if ok {
		t.Fatal("expected miss on nil cache")
	}

	cache.put(CacheKey{SegmentID: 1, ColumnIdx: 0}, decodedColumn{})
	cache.Invalidate(1)
	cache.Clear()

	stats := cache.Stats()
	if stats.Hits != 0 || stats.Misses != 0 {
		t.Fatal("expected zero stats on nil cache")
	}
}

func TestBlockCacheDisabled(t *testing.T) {
	cache := NewBlockCache(0) // 容量 <= 0 表示不缓存

	key := CacheKey{SegmentID: 1, ColumnIdx: 0}
	cache.put(key, decodedColumn{data: []int64{1}, typ: common.TypeInt64})

	_, ok := cache.get(key)
	if ok {
		t.Fatal("expected miss on disabled cache")
	}
}

func TestBlockCacheUpdateExisting(t *testing.T) {
	cache := NewBlockCache(4096)

	key := CacheKey{SegmentID: 1, ColumnIdx: 0}

	// 放入初始值
	cache.put(key, decodedColumn{data: []int64{1}, typ: common.TypeInt64})

	// 更新值
	cache.put(key, decodedColumn{data: []int64{2, 3}, typ: common.TypeInt64})

	got, ok := cache.get(key)
	if !ok {
		t.Fatal("expected cache hit")
	}
	ints := got.data.([]int64)
	if len(ints) != 2 || ints[0] != 2 || ints[1] != 3 {
		t.Fatalf("unexpected updated data: %v", ints)
	}
}

func TestBlockCacheClear(t *testing.T) {
	cache := NewBlockCache(4096)

	for i := uint32(0); i < 5; i++ {
		cache.put(CacheKey{SegmentID: 1, ColumnIdx: i}, decodedColumn{
			data: []int64{1}, typ: common.TypeInt64,
		})
	}

	cache.Clear()

	stats := cache.Stats()
	if stats.Entries != 0 {
		t.Fatalf("expected 0 entries after clear, got %d", stats.Entries)
	}
	if stats.Size != 0 {
		t.Fatalf("expected 0 size after clear, got %d", stats.Size)
	}
}

func TestEstimateDecodedSize(t *testing.T) {
	tests := []struct {
		name string
		dc   decodedColumn
	}{
		{"int64", decodedColumn{data: make([]int64, 100), typ: common.TypeInt64}},
		{"float64", decodedColumn{data: make([]float64, 100), typ: common.TypeFloat64}},
		{"bool_type", decodedColumn{data: make([]uint64, 2), typ: common.TypeBool}},
		{"string", decodedColumn{data: make([]string, 10), typ: common.TypeString}},
		{"nil_data", decodedColumn{data: nil, typ: common.TypeInt64}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			size := estimateDecodedSize(tt.dc)
			if size <= 0 {
				t.Fatalf("expected positive size, got %d", size)
			}
		})
	}
}

func TestIndexCacheBasic(t *testing.T) {
	cache := NewIndexCache(10)

	stats := []ColumnStat{
		{ColumnID: 0, NullCount: 5},
		{ColumnID: 1, NullCount: 3},
	}

	// 未命中
	_, ok := cache.GetColumnStats(1)
	if ok {
		t.Fatal("expected cache miss")
	}

	// 放入缓存
	cache.PutColumnStats(1, stats)

	// 命中
	got, ok := cache.GetColumnStats(1)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(got) != 2 || got[0].ColumnID != 0 || got[1].ColumnID != 1 {
		t.Fatalf("unexpected stats: %v", got)
	}
}

func TestIndexCacheLRUEviction(t *testing.T) {
	cache := NewIndexCache(3)

	// 放入 4 个条目，容量为 3
	for i := uint64(1); i <= 4; i++ {
		cache.PutColumnStats(i, []ColumnStat{{ColumnID: uint32(i)}})
	}

	// 最早的条目应该被淘汰
	_, ok := cache.GetColumnStats(1)
	if ok {
		t.Fatal("expected segment 1 to be evicted")
	}

	// 较新的条目应该还在
	_, ok = cache.GetColumnStats(4)
	if !ok {
		t.Fatal("expected segment 4 to be in cache")
	}
}

func TestIndexCacheInvalidate(t *testing.T) {
	cache := NewIndexCache(10)

	cache.PutColumnStats(1, []ColumnStat{{ColumnID: 0}})
	cache.PutColumnStats(2, []ColumnStat{{ColumnID: 1}})

	cache.Invalidate(1)

	_, ok := cache.GetColumnStats(1)
	if ok {
		t.Fatal("expected segment 1 to be invalidated")
	}

	_, ok = cache.GetColumnStats(2)
	if !ok {
		t.Fatal("expected segment 2 to remain")
	}
}

func TestIndexCacheNil(t *testing.T) {
	var cache *IndexCache

	_, ok := cache.GetColumnStats(1)
	if ok {
		t.Fatal("expected miss on nil cache")
	}

	cache.PutColumnStats(1, nil)
	cache.Invalidate(1)
	cache.Clear()

	if cache.Len() != 0 {
		t.Fatal("expected 0 length on nil cache")
	}
}

func TestIndexCacheUpdateExisting(t *testing.T) {
	cache := NewIndexCache(10)

	cache.PutColumnStats(1, []ColumnStat{{ColumnID: 0}})
	cache.PutColumnStats(1, []ColumnStat{{ColumnID: 0}, {ColumnID: 1}})

	got, ok := cache.GetColumnStats(1)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 stats, got %d", len(got))
	}
}

func TestIndexCacheLen(t *testing.T) {
	cache := NewIndexCache(10)

	if cache.Len() != 0 {
		t.Fatal("expected 0 length")
	}

	cache.PutColumnStats(1, []ColumnStat{{ColumnID: 0}})
	if cache.Len() != 1 {
		t.Fatalf("expected 1 length, got %d", cache.Len())
	}

	cache.PutColumnStats(2, []ColumnStat{{ColumnID: 0}})
	if cache.Len() != 2 {
		t.Fatalf("expected 2 length, got %d", cache.Len())
	}
}
