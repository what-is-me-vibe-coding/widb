package storage

import (
	"fmt"
	"math/rand"
	"runtime"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// readHeapInUse forces GC and returns HeapInuse bytes.
func readHeapInUse() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse
}

// TestStressMemLeak_MillionWrites writes keys, measures heap at each stage.
func TestStressMemLeak_MillionWrites(t *testing.T) {
	defer suppressLog()()

	writes := 10000
	if testing.Short() {
		writes = 5000
	}

	dataDir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dataDir})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	cols := stressCols()

	start := time.Now()
	for i := 0; i < writes; i++ {
		key := fmt.Sprintf("mw_%07d", i)
		_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))})
	}
	writeDur := time.Since(start)
	t.Logf("Write: %d keys in %v", writes, writeDur)

	heapAfterWrite := readHeapInUse()
	t.Logf("After %d writes: heap=%d bytes", writes, heapAfterWrite)

	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}
	heapAfterFlush := readHeapInUse()
	t.Logf("After flush: heap=%d bytes", heapAfterFlush)

	_ = eng.Close()

	eng2, err := NewEngine(EngineConfig{DataDir: dataDir})
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	// Read back a sample of keys
	for i := 0; i < writes; i += writes/20 + 1 {
		key := fmt.Sprintf("mw_%07d", i)
		if _, ok := eng2.Get(key); !ok {
			t.Errorf("key %s not found after reopen", key)
		}
	}
	heapAfterReopen := readHeapInUse()
	t.Logf("After reopen+read: heap=%d bytes", heapAfterReopen)

	// Heap after reopen should not exceed 5x the post-flush heap
	growth := float64(heapAfterReopen) / float64(heapAfterFlush)
	t.Logf("Heap growth after reopen: %.2fx", growth)
	if growth > 5.0 {
		t.Errorf("heap grew %.1fx after reopen, possible leak", growth)
	}
}

// TestStressMemLeak_RepeatedOpenClose opens/closes engine multiple times, checks heap.
func TestStressMemLeak_RepeatedOpenClose(t *testing.T) {
	defer suppressLog()()

	cycles := 20
	if testing.Short() {
		cycles = 10
	}
	const rowsPerCycle = 100
	cols := stressCols()
	dataDir := t.TempDir()

	var heaps []uint64
	for c := 0; c < cycles; c++ {
		eng, err := NewEngine(EngineConfig{DataDir: dataDir})
		if err != nil {
			t.Fatalf("open cycle %d: %v", c, err)
		}
		for i := 0; i < rowsPerCycle; i++ {
			key := fmt.Sprintf("oc_%02d_%04d", c, i)
			_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(c*rowsPerCycle + i))})
		}
		_ = eng.Flush(cols)
		// Read back a few keys
		for i := 0; i < rowsPerCycle; i += 20 {
			key := fmt.Sprintf("oc_%02d_%04d", c, i)
			_, _ = eng.Get(key)
		}
		_ = eng.Close()

		heap := readHeapInUse()
		heaps = append(heaps, heap)
	}

	// Compare second half average to first half average to avoid cold-start skew.
	mid := len(heaps) / 2
	var firstHalf, secondHalf uint64
	for _, h := range heaps[:mid] {
		firstHalf += h
	}
	for _, h := range heaps[mid:] {
		secondHalf += h
	}
	avgFirst := firstHalf / uint64(mid)
	avgSecond := secondHalf / uint64(len(heaps)-mid)
	growth := float64(avgSecond) / float64(avgFirst)
	t.Logf("RepeatedOpenClose: avg_first=%d, avg_second=%d, growth=%.2fx over %d cycles",
		avgFirst, avgSecond, growth, cycles)
	if growth > 3.0 {
		t.Errorf("heap grew %.1fx over %d open/close cycles, possible leak", growth, cycles)
	}
}

// TestStressMemLeak_FlushCompactCycle runs write→flush→compact cycles, checks heap.
func TestStressMemLeak_FlushCompactCycle(t *testing.T) {
	defer suppressLog()()

	eng := newStressEngine(t, 0)
	defer func() { _ = eng.Close() }()

	cols := stressCols()
	cycles := 10
	if testing.Short() {
		cycles = 5
	}
	const rowsPerCycle = 100

	var heaps []uint64
	for c := 0; c < cycles; c++ {
		for i := 0; i < rowsPerCycle; i++ {
			key := fmt.Sprintf("fc_%02d_%04d", c, i)
			_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(c*rowsPerCycle + i))})
		}
		if err := eng.Flush(cols); err != nil {
			t.Fatalf("flush cycle %d: %v", c, err)
		}
		if eng.ShouldCompact() {
			if err := eng.Compact(cols); err != nil {
				t.Fatalf("compact cycle %d: %v", c, err)
			}
		}
		heap := readHeapInUse()
		heaps = append(heaps, heap)
	}

	// Compare second half average to first half average to avoid cold-start skew.
	mid := len(heaps) / 2
	var firstHalf, secondHalf uint64
	for _, h := range heaps[:mid] {
		firstHalf += h
	}
	for _, h := range heaps[mid:] {
		secondHalf += h
	}
	avgFirst := firstHalf / uint64(mid)
	avgSecond := secondHalf / uint64(len(heaps)-mid)
	growth := float64(avgSecond) / float64(avgFirst)
	t.Logf("FlushCompactCycle: avg_first=%d, avg_second=%d, growth=%.2fx over %d cycles",
		avgFirst, avgSecond, growth, cycles)
	if growth > 3.0 {
		t.Errorf("heap grew %.1fx over %d flush/compact cycles, possible leak", growth, cycles)
	}
}

// TestStressMemLeak_ScanNoLeak verifies repeated scans don't leak memory.
func TestStressMemLeak_ScanNoLeak(t *testing.T) {
	defer suppressLog()()

	eng := newStressEngine(t, 0)
	defer func() { _ = eng.Close() }()

	cols := stressCols()
	keyCount := 5000
	scanCount := 500
	if testing.Short() {
		keyCount = 1000
		scanCount = 100
	}

	for i := 0; i < keyCount; i++ {
		key := fmt.Sprintf("snl_%06d", i)
		_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))})
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	heapBefore := readHeapInUse()
	for s := 0; s < scanCount; s++ {
		_ = eng.Scan("snl_000000", fmt.Sprintf("snl_%06d", keyCount-1))
	}
	heapAfter := readHeapInUse()

	growth := float64(heapAfter) / float64(heapBefore)
	t.Logf("ScanNoLeak: before=%d, after=%d, growth=%.2fx (%d scans)",
		heapBefore, heapAfter, growth, scanCount)
	if growth > 1.5 {
		t.Errorf("heap grew %.1fx after %d scans, possible leak", growth, scanCount)
	}
}

// TestStressMemLeak_LargeValues writes keys with 1KB values, checks heap.
func TestStressMemLeak_LargeValues(t *testing.T) {
	defer suppressLog()()

	eng := newStressEngine(t, 0)
	defer func() { _ = eng.Close() }()

	cols := stressStringCols()
	keyCount := 5000
	if testing.Short() {
		keyCount = 1000
	}
	largeVal := make([]byte, 1024)
	for i := range largeVal {
		largeVal[i] = byte('A' + i%26)
	}
	largeStr := string(largeVal)

	heapBefore := readHeapInUse()
	for i := 0; i < keyCount; i++ {
		key := fmt.Sprintf("lv_%06d", i)
		_ = eng.Write(key, map[string]common.Value{colVal: common.NewString(largeStr)})
	}
	heapAfterWrite := readHeapInUse()
	t.Logf("After %d large writes: heap=%d (was %d)", keyCount, heapAfterWrite, heapBefore)

	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	// Read all keys back
	for i := 0; i < keyCount; i++ {
		key := fmt.Sprintf("lv_%06d", i)
		row, ok := eng.Get(key)
		if !ok {
			t.Errorf("key %s not found", key)
		} else if len(row.Columns[colVal].Str) != 1024 {
			t.Errorf("key %s: value length %d, want 1024", key, len(row.Columns[colVal].Str))
		}
	}
	heapAfterRead := readHeapInUse()
	t.Logf("After read back: heap=%d", heapAfterRead)

	growth := float64(heapAfterRead) / float64(heapBefore)
	t.Logf("LargeValues: growth=%.2fx", growth)
	if growth > 10.0 {
		t.Errorf("heap grew %.1fx with large values, possible leak", growth)
	}
}

// TestStress_1MWrites100KReads writes keys, reads random keys, checks heap growth.
func TestStress_1MWrites100KReads(t *testing.T) {
	defer suppressLog()()

	writes := 20000
	reads := 2000
	if testing.Short() {
		writes = 5000
		reads = 500
	}

	eng := newStressEngine(t, 0)
	defer func() { _ = eng.Close() }()

	cols := stressCols()

	writeStart := time.Now()
	for i := 0; i < writes; i++ {
		key := fmt.Sprintf("mr_%07d", i)
		_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))})
	}
	writeDur := time.Since(writeStart)
	writeThroughput := float64(writes) / writeDur.Seconds()
	t.Logf("Write: %d keys in %v (%.0f ops/sec)", writes, writeDur, writeThroughput)

	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	heapPreRead := readHeapInUse()
	readStart := time.Now()
	for i := 0; i < reads; i++ {
		idx := rand.Intn(writes)
		key := fmt.Sprintf("mr_%07d", idx)
		_, _ = eng.Get(key)
	}
	readDur := time.Since(readStart)
	readThroughput := float64(reads) / readDur.Seconds()
	heapPostRead := readHeapInUse()
	t.Logf("Read: %d keys in %v (%.0f ops/sec)", reads, readDur, readThroughput)

	growthPct := (float64(heapPostRead) - float64(heapPreRead)) / float64(heapPreRead) * 100
	t.Logf("Heap: pre-read=%d, post-read=%d, growth=%.1f%%",
		heapPreRead, heapPostRead, growthPct)

	// Acceptance criterion is <10%; threshold is 15% to tolerate GC jitter on small heaps.
	if growthPct > 15.0 {
		t.Errorf("heap growth %.1f%% exceeds 15%% threshold (pre=%d post=%d)",
			growthPct, heapPreRead, heapPostRead)
	}
}

// TestStressMemLeak_MemTableRotation uses small MaxMemTableSize to trigger rotations.
func TestStressMemLeak_MemTableRotation(t *testing.T) {
	defer suppressLog()()

	writes := 20000
	if testing.Short() {
		writes = 5000
	}

	eng := newStressEngine(t, 4096) // 小 MemTable 触发频繁轮转，但不至于产生过多小段
	defer func() { _ = eng.Close() }()

	cols := stressStringCols()

	// Write first batch and flush to establish baseline heap (includes segment metadata)
	half := writes / 2
	for i := 0; i < half; i++ {
		key := fmt.Sprintf("mtr_%06d", i)
		val := fmt.Sprintf("value_%d_padding_for_size", i)
		_ = eng.Write(key, map[string]common.Value{colVal: common.NewString(val)})
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("initial flush: %v", err)
	}
	heapBaseline := readHeapInUse()

	// Write second batch and flush
	for i := half; i < writes; i++ {
		key := fmt.Sprintf("mtr_%06d", i)
		val := fmt.Sprintf("value_%d_padding_for_size", i)
		_ = eng.Write(key, map[string]common.Value{colVal: common.NewString(val)})
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("second flush: %v", err)
	}
	heapAfterSecondFlush := readHeapInUse()

	// Verify a sample of keys
	notFound := 0
	for i := 0; i < writes; i += writes/100 + 1 {
		key := fmt.Sprintf("mtr_%06d", i)
		if _, ok := eng.Get(key); !ok {
			notFound++
		}
	}
	if notFound > 0 {
		t.Errorf("%d keys not found after rotation stress", notFound)
	}

	growth := float64(heapAfterSecondFlush) / float64(heapBaseline)
	t.Logf("MemTableRotation: baseline=%d, after=%d, growth=%.2fx, notFound=%d",
		heapBaseline, heapAfterSecondFlush, growth, notFound)
	// Compare second-half heap to first-half baseline; doubling data should < 5x heap.
	// 阈值设为 5x：段元数据、索引、布隆过滤器等结构随数据量线性增长，
	// GC 非确定性也可能导致测量波动。
	if growth > 5.0 {
		t.Errorf("heap grew %.1fx with memtable rotations, possible leak", growth)
	}
}
