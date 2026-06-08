package integration

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// ycsbCols 返回 YCSB 基准测试使用的列元数据。
func ycsbCols() []storage.ColumnMeta {
	return []storage.ColumnMeta{
		{ID: 0, Name: "val", Type: common.TypeInt64},
	}
}

// ycsbWideCols 返回宽表场景的列元数据（100列）。
func ycsbWideCols() []storage.ColumnMeta {
	cols := make([]storage.ColumnMeta, 100)
	for i := range cols {
		cols[i] = storage.ColumnMeta{
			ID:   uint32(i),
			Name: fmt.Sprintf("col_%03d", i),
			Type: common.TypeInt64,
		}
	}
	return cols
}

// ycsbWideValues 生成宽表场景的列值。
func ycsbWideValues(val int64) map[string]common.Value {
	vals := make(map[string]common.Value, 100)
	for i := 0; i < 100; i++ {
		vals[fmt.Sprintf("col_%03d", i)] = common.NewInt64(val + int64(i))
	}
	return vals
}

// ycsbSimpleValues 生成简单场景的列值。
func ycsbSimpleValues(val int64) map[string]common.Value {
	return map[string]common.Value{"val": common.NewInt64(val)}
}

// newYCSBEngine 创建 YCSB 基准测试用的 Engine。
func newYCSBEngine(b *testing.B) *storage.Engine {
	b.Helper()
	dir, err := os.MkdirTemp("", "ycsb-bench-*")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(dir) })

	eng, err := storage.NewEngine(storage.EngineConfig{DataDir: dir})
	if err != nil {
		b.Fatal(err)
	}
	return eng
}

// --- YCSB Workload A: 50% 读, 50% 写 ---

func BenchmarkYCSB_WorkloadA(b *testing.B) {
	eng := newYCSBEngine(b)
	defer func() { _ = eng.Close() }()

	cols := ycsbCols()
	const prefill = 50000

	// 预填充数据
	for i := 0; i < prefill; i++ {
		key := fmt.Sprintf("user_%016d", i)
		_ = eng.Write(key, ycsbSimpleValues(int64(i)))
	}
	// 刷盘以确保点查走 Segment 路径
	_ = eng.Flush(cols)

	var readOps, writeOps atomic.Int64
	keyPool := make([]string, prefill)
	for i := range keyPool {
		keyPool[i] = fmt.Sprintf("user_%016d", i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		for pb.Next() {
			if rng.Float64() < 0.5 {
				// 读操作
				key := keyPool[rng.Intn(prefill)]
				_, _ = eng.Get(key)
				readOps.Add(1)
			} else {
				// 写操作
				idx := rng.Intn(prefill)
				key := keyPool[idx]
				_ = eng.Write(key, ycsbSimpleValues(int64(idx)))
				writeOps.Add(1)
			}
		}
	})
	b.ReportAllocs()
	b.Logf("WorkloadA: reads=%d writes=%d", readOps.Load(), writeOps.Load())
}

// --- YCSB Workload B: 95% 读, 5% 写 ---

func BenchmarkYCSB_WorkloadB(b *testing.B) {
	eng := newYCSBEngine(b)
	defer func() { _ = eng.Close() }()

	cols := ycsbCols()
	const prefill = 50000

	for i := 0; i < prefill; i++ {
		key := fmt.Sprintf("user_%016d", i)
		_ = eng.Write(key, ycsbSimpleValues(int64(i)))
	}
	_ = eng.Flush(cols)

	var readOps, writeOps atomic.Int64
	keyPool := make([]string, prefill)
	for i := range keyPool {
		keyPool[i] = fmt.Sprintf("user_%016d", i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		for pb.Next() {
			if rng.Float64() < 0.95 {
				key := keyPool[rng.Intn(prefill)]
				_, _ = eng.Get(key)
				readOps.Add(1)
			} else {
				idx := rng.Intn(prefill)
				key := keyPool[idx]
				_ = eng.Write(key, ycsbSimpleValues(int64(idx)))
				writeOps.Add(1)
			}
		}
	})
	b.ReportAllocs()
	b.Logf("WorkloadB: reads=%d writes=%d", readOps.Load(), writeOps.Load())
}

// --- YCSB Workload C: 100% 读 ---

func BenchmarkYCSB_WorkloadC(b *testing.B) {
	eng := newYCSBEngine(b)
	defer func() { _ = eng.Close() }()

	cols := ycsbCols()
	const prefill = 50000

	for i := 0; i < prefill; i++ {
		key := fmt.Sprintf("user_%016d", i)
		_ = eng.Write(key, ycsbSimpleValues(int64(i)))
	}
	_ = eng.Flush(cols)

	keyPool := make([]string, prefill)
	for i := range keyPool {
		keyPool[i] = fmt.Sprintf("user_%016d", i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		for pb.Next() {
			key := keyPool[rng.Intn(prefill)]
			_, _ = eng.Get(key)
		}
	})
	b.ReportAllocs()
}

// --- 写入吞吐量基准测试 ---

func BenchmarkYCSB_WriteThroughput(b *testing.B) {
	eng := newYCSBEngine(b)
	defer func() { _ = eng.Close() }()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key_%016d", i)
		_ = eng.Write(key, ycsbSimpleValues(int64(i)))
	}
	b.ReportAllocs()
}

// --- 批量写入吞吐量基准测试 ---

func BenchmarkYCSB_BatchWriteThroughput(b *testing.B) {
	eng := newYCSBEngine(b)
	defer func() { _ = eng.Close() }()

	const batchSize = 100
	batch := make([]storage.WriteRow, batchSize)
	for i := range batch {
		batch[i].Values = ycsbSimpleValues(int64(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range batch {
			batch[j].Key = fmt.Sprintf("key_%016d_%d", i, j)
		}
		_ = eng.WriteBatch(batch)
	}
	b.ReportAllocs()
}

// --- 并行写入吞吐量基准测试 ---

func BenchmarkYCSB_ParallelWriteThroughput(b *testing.B) {
	eng := newYCSBEngine(b)
	defer func() { _ = eng.Close() }()

	var counter atomic.Int64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := counter.Add(1)
			key := fmt.Sprintf("key_%016d", i)
			_ = eng.Write(key, ycsbSimpleValues(i))
		}
	})
	b.ReportAllocs()
}

// --- 点查延迟分布基准测试 ---

func BenchmarkYCSB_PointQueryLatency(b *testing.B) {
	eng := newYCSBEngine(b)
	defer func() { _ = eng.Close() }()

	cols := ycsbCols()
	const numKeys = 10000

	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("key_%016d", i)
		_ = eng.Write(key, ycsbSimpleValues(int64(i)))
	}
	_ = eng.Flush(cols)

	keyPool := make([]string, numKeys)
	for i := range keyPool {
		keyPool[i] = fmt.Sprintf("key_%016d", i)
	}

	latencies := make([]time.Duration, 0, b.N)
	var mu sync.Mutex

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := keyPool[i%numKeys]
		start := time.Now()
		_, _ = eng.Get(key)
		elapsed := time.Since(start)

		mu.Lock()
		latencies = append(latencies, elapsed)
		mu.Unlock()
	}
	b.ReportAllocs()

	// 计算延迟分布
	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})
	p50 := latencies[len(latencies)*50/100]
	p90 := latencies[len(latencies)*90/100]
	p99 := latencies[len(latencies)*99/100]
	p999 := latencies[len(latencies)*999/1000]
	b.Logf("PointQuery Latency: P50=%v P90=%v P99=%v P999=%v", p50, p90, p99, p999)
}

// --- 范围扫描吞吐量基准测试 ---

func BenchmarkYCSB_RangeScan(b *testing.B) {
	eng := newYCSBEngine(b)
	defer func() { _ = eng.Close() }()

	cols := ycsbCols()
	const numKeys = 10000

	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("key_%016d", i)
		_ = eng.Write(key, ycsbSimpleValues(int64(i)))
	}
	_ = eng.Flush(cols)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := fmt.Sprintf("key_%016d", i%9000)
		end := fmt.Sprintf("key_%016d", (i%9000)+1000)
		_ = eng.Scan(start, end)
	}
	b.ReportAllocs()
}

// --- 宽表写入基准测试 ---

func BenchmarkYCSB_WideTableWrite(b *testing.B) {
	eng := newYCSBEngine(b)
	defer func() { _ = eng.Close() }()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("wide_%016d", i)
		_ = eng.Write(key, ycsbWideValues(int64(i)))
	}
	b.ReportAllocs()
}

// --- 宽表读取基准测试 ---

func BenchmarkYCSB_WideTableRead(b *testing.B) {
	eng := newYCSBEngine(b)
	defer func() { _ = eng.Close() }()

	cols := ycsbWideCols()
	const numKeys = 1000

	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("wide_%016d", i)
		_ = eng.Write(key, ycsbWideValues(int64(i)))
	}
	_ = eng.Flush(cols)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("wide_%016d", i%numKeys)
		_, _ = eng.Get(key)
	}
	b.ReportAllocs()
}

// --- 写入+刷盘+Compaction 综合基准测试 ---

func BenchmarkYCSB_WriteFlushCompact(b *testing.B) {
	eng := newYCSBEngine(b)
	defer func() { _ = eng.Close() }()

	cols := ycsbCols()
	const rowsPerIter = 500

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < rowsPerIter; j++ {
			key := fmt.Sprintf("wfc_%016d_%04d", i, j)
			_ = eng.Write(key, ycsbSimpleValues(int64(i*rowsPerIter+j)))
		}
		_ = eng.Flush(cols)
		if eng.ShouldCompact() {
			_ = eng.Compact(cols)
		}
	}
	b.ReportAllocs()
}
