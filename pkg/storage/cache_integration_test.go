package storage

import (
	"fmt"
	"os"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestEngineCacheIntegration(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir:       dir,
		BlockCacheCfg: BlockCacheConfig{Capacity: 1024 * 1024},
	})
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{
		{Name: "id", Type: common.TypeInt64},
		{Name: "name", Type: common.TypeString},
	}

	// 写入数据
	for i := 0; i < 100; i++ {
		key := padKey(i)
		values := map[string]common.Value{
			"id":   common.NewInt64(int64(i)),
			"name": common.NewString("user_" + padKey(i)),
		}
		if err := eng.Write(key, values); err != nil {
			t.Fatalf("write failed: %v", err)
		}
	}

	// Flush 生成 Segment
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush failed: %v", err)
	}

	// 第一次查询（缓存未命中，会填充缓存）
	row, ok := eng.Get(padKey(50))
	if !ok {
		t.Fatal("expected to find key")
	}
	if row.Columns["id"].Int64 != 50 {
		t.Fatalf("expected id=50, got %d", row.Columns["id"].Int64)
	}

	// 第二次查询（应命中缓存）
	row, ok = eng.Get(padKey(50))
	if !ok {
		t.Fatal("expected to find key on second query")
	}
	if row.Columns["id"].Int64 != 50 {
		t.Fatalf("expected id=50 on cache hit, got %d", row.Columns["id"].Int64)
	}

	// 验证缓存统计
	bcStats, icStats := eng.CacheStats()
	if bcStats.LookupCount == 0 {
		t.Fatal("expected block cache lookups")
	}
	if bcStats.HitCount == 0 {
		t.Fatal("expected block cache hits on second query")
	}
	_ = icStats // IndexCache 统计也可能有数据
}

func TestEngineCacheInvalidationOnCompact(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir:       dir,
		BlockCacheCfg: BlockCacheConfig{Capacity: 1024 * 1024},
	})
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{
		{Name: "id", Type: common.TypeInt64},
	}

	// 写入并 flush 多次以产生多个 L0 Segment
	for batch := 0; batch < 4; batch++ {
		for i := 0; i < 10; i++ {
			key := padKey(batch*10 + i)
			values := map[string]common.Value{
				"id": common.NewInt64(int64(batch*10 + i)),
			}
			if err := eng.Write(key, values); err != nil {
				t.Fatalf("write failed: %v", err)
			}
		}
		if err := eng.Flush(cols); err != nil {
			t.Fatalf("flush failed: %v", err)
		}
	}

	// 查询以填充缓存
	eng.Get(padKey(5))
	eng.Get(padKey(15))

	bcStatsBefore, _ := eng.CacheStats()
	if bcStatsBefore.EntryCount == 0 {
		t.Fatal("expected cache entries after queries")
	}

	// Compact
	if err := eng.Compact(cols); err != nil {
		t.Fatalf("compact failed: %v", err)
	}

	// Compact 后旧 Segment 的缓存应失效
	// 新 Segment 的数据仍可查询
	row, ok := eng.Get(padKey(5))
	if !ok {
		t.Fatal("expected to find key after compact")
	}
	if row.Columns["id"].Int64 != 5 {
		t.Fatalf("expected id=5 after compact, got %d", row.Columns["id"].Int64)
	}
}

func TestEngineCacheStats(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir:       dir,
		BlockCacheCfg: BlockCacheConfig{Capacity: 1024 * 1024},
	})
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	bcStats, icStats := eng.CacheStats()
	if bcStats.HitRate != 0 {
		t.Fatal("expected 0 hit rate for empty cache")
	}
	_ = icStats
}

func TestEngineCacheAccessors(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	if eng.BlockCache() == nil {
		t.Fatal("expected non-nil BlockCache")
	}
	if eng.IndexCache() == nil {
		t.Fatal("expected non-nil IndexCache")
	}
}

func TestEngineDefaultCacheConfig(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 默认配置应正常工作
	cols := []ColumnMeta{
		{Name: "val", Type: common.TypeInt64},
	}
	if err := eng.Write("k1", map[string]common.Value{"val": common.NewInt64(1)}); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush failed: %v", err)
	}

	row, ok := eng.Get("k1")
	if !ok {
		t.Fatal("expected to find key")
	}
	if row.Columns["val"].Int64 != 1 {
		t.Fatalf("expected val=1, got %d", row.Columns["val"].Int64)
	}
}

func padKey(i int) string {
	return fmt.Sprintf("key_%03d", i)
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
