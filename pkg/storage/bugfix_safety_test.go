package storage

import (
	"sync"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestScanRangeAcquiresRLockInternally 验证 ScanRange 内部获取 RLock，
// 调用方无需手动加锁即可安全调用。
func TestScanRangeAcquiresRLockInternally(t *testing.T) {
	t.Parallel()

	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入一些数据
	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("b", map[string]common.Value{colVal: common.NewInt64(2)})
	_ = eng.Write("c", map[string]common.Value{colVal: common.NewInt64(3)})

	// 不手动获取锁，直接调用 ScanRange，应正常工作
	results := eng.ScanRange("a", "c")
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

// TestScanRangeConcurrentWithWrite 验证 ScanRange 与并发 Write 不会产生数据竞态。
// ScanRange 内部获取 RLock，Write 获取 Lock，两者应正确互斥。
func TestScanRangeConcurrentWithWrite(t *testing.T) {
	t.Parallel()

	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 预先写入数据
	for i := 0; i < 10; i++ {
		key := string(rune('a' + i))
		_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))})
	}

	var wg sync.WaitGroup
	const readers = 4
	const writers = 2

	wg.Add(readers + writers)

	// 并发读
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				results := eng.ScanRange("a", "z")
				_ = results
			}
		}()
	}

	// 并发写
	for i := 0; i < writers; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				key := string(rune('A' + id))
				_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(id*100 + j))})
			}
		}(i)
	}

	wg.Wait()
}

// TestComputeStringStatsShortOffsets 验证 computeStringStats 在 offsets 数组
// 长度不足时不会 panic，而是安全地跳过越界行。
func TestComputeStringStatsShortOffsets(t *testing.T) {
	t.Parallel()

	// 构造 rowCount=5 但 offsets 只有 2 个元素（不足以覆盖所有行）
	data := []byte("helloworld")
	offsets := []uint32{0, 5} // 只能安全访问第 0 行
	rowCount := uint32(5)

	stat := &ColumnStat{}
	// 不应 panic
	computeStringStats(data, offsets, rowCount, nil, stat)

	// 第 0 行 "hello" 应被正确统计
	if stat.Min == nil || string(stat.Min) != testStrHello {
		t.Errorf("expected Min='hello', got %v", stat.Min)
	}
	if stat.Max == nil || string(stat.Max) != testStrHello {
		t.Errorf("expected Max='hello', got %v", stat.Max)
	}
}

// TestComputeStringStatsEmptyOffsets 验证 computeStringStats 在 offsets 为空时不会 panic。
func TestComputeStringStatsEmptyOffsets(t *testing.T) {
	t.Parallel()

	data := []byte(testStrHello)
	rowCount := uint32(3)

	stat := &ColumnStat{}
	// 不应 panic
	computeStringStats(data, nil, rowCount, nil, stat)

	// 无有效行可统计，Min/Max 应为 nil
	if stat.Min != nil || stat.Max != nil {
		t.Errorf("expected nil Min/Max for empty offsets, got Min=%v Max=%v", stat.Min, stat.Max)
	}
}

// TestIndexCacheGetColumnStatsConcurrentReads 验证 IndexCache.GetColumnStats
// 允许多个 goroutine 并发读取而不会死锁。
func TestIndexCacheGetColumnStatsConcurrentReads(t *testing.T) {
	t.Parallel()

	cache := NewIndexCache(100)

	// 预填充数据
	for i := uint64(0); i < 20; i++ {
		cache.PutColumnStats(i, []ColumnStat{{ColumnID: uint32(i), NullCount: uint32(i)}})
	}

	var wg sync.WaitGroup
	const goroutines = 10
	const iterations = 100

	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				// 并发读取不同的 segmentID
				stats, ok := cache.GetColumnStats(uint64(i % 20))
				if ok && len(stats) != 1 {
					t.Errorf("unexpected stats length: %d", len(stats))
				}
			}
		}()
	}
	wg.Wait()
}
