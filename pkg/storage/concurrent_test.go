package storage

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestConcurrent_WriteReadConsistency 验证并发写入和读取的数据一致性。
// 多个 goroutine 同时写入不同的 key，同时读取已写入的 key，确保读到正确的值。
func TestConcurrent_WriteReadConsistency(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	const writers = 8
	const writesPerWriter = 100
	var wg sync.WaitGroup
	var writeErr atomic.Int32

	// Concurrent writes
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < writesPerWriter; j++ {
				key := fmt.Sprintf("g%d_key_%04d", gid, j)
				val := int64(gid*10000 + j)
				if err := eng.Write(key, map[string]common.Value{
					colVal: common.NewInt64(val),
				}); err != nil {
					writeErr.Add(1)
				}
			}
		}(g)
	}
	wg.Wait()

	if writeErr.Load() > 0 {
		t.Fatalf("write errors: %d", writeErr.Load())
	}

	// Verify all data is readable and correct
	for g := 0; g < writers; g++ {
		for j := 0; j < writesPerWriter; j++ {
			key := fmt.Sprintf("g%d_key_%04d", g, j)
			expected := int64(g*10000 + j)
			row, ok := eng.Get(key)
			if !ok {
				t.Errorf("key %s not found", key)
				continue
			}
			if row.Columns[colVal].Int64 != expected {
				t.Errorf("key %s: expected %d, got %d", key, expected, row.Columns[colVal].Int64)
			}
		}
	}
}

// startReadWhileWritingReaders 启动并发读取 goroutine，验证读取一致性。
func startReadWhileWritingReaders(eng *Engine, readers, readsPerReader, preWriteCount int, wg *sync.WaitGroup, readErr *atomic.Int32) {
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < readsPerReader; j++ {
				key := fmt.Sprintf("pre_key_%04d", j%preWriteCount)
				row, ok := eng.Get(key)
				if ok {
					val := row.Columns[colVal].Int64
					if val < 0 || val >= int64(preWriteCount) {
						readErr.Add(1)
					}
				}
				_, _ = eng.Get("w0_key_0001")
			}
		}()
	}
}

// TestConcurrent_ReadWhileWriting 验证写入过程中并发读取不会崩溃或返回不一致数据。
// 读取线程可能读到旧值或新值，但不应读到脏数据。
func TestConcurrent_ReadWhileWriting(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	const preWriteCount = 50
	for i := 0; i < preWriteCount; i++ {
		key := fmt.Sprintf("pre_key_%04d", i)
		_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))})
	}

	const writers = 4
	const writesPerWriter = 100
	const readers = 4
	const readsPerReader = 200

	var wg sync.WaitGroup
	var readErr atomic.Int32

	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < writesPerWriter; j++ {
				key := fmt.Sprintf("w%d_key_%04d", gid, j)
				_ = eng.Write(key, map[string]common.Value{
					colVal: common.NewInt64(int64(gid*10000 + j)),
				})
			}
		}(g)
	}

	startReadWhileWritingReaders(eng, readers, readsPerReader, preWriteCount, &wg, &readErr)

	wg.Wait()

	if readErr.Load() > 0 {
		t.Errorf("read consistency errors: %d", readErr.Load())
	}
}

// TestConcurrent_WriteWithFlush 验证并发写入与 Flush 交替执行的数据一致性。
func TestConcurrent_WriteWithFlush(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	const writers = 4
	const writesPerWriter = 100
	const flushCount = 5
	var wg sync.WaitGroup

	// Concurrent writes
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < writesPerWriter; j++ {
				key := fmt.Sprintf("g%d_key_%04d", gid, j)
				_ = eng.Write(key, map[string]common.Value{
					colVal: common.NewInt64(int64(gid*10000 + j)),
				})
			}
		}(g)
	}

	// Periodic flushes
	var flushWg sync.WaitGroup
	for f := 0; f < flushCount; f++ {
		flushWg.Add(1)
		go func() {
			defer flushWg.Done()
			time.Sleep(time.Duration(f+1) * 10 * time.Millisecond)
			_ = eng.Flush(cols)
		}()
	}

	wg.Wait()
	flushWg.Wait()

	// Verify all data is accessible
	for g := 0; g < writers; g++ {
		for j := 0; j < writesPerWriter; j++ {
			key := fmt.Sprintf("g%d_key_%04d", g, j)
			expected := int64(g*10000 + j)
			row, ok := eng.Get(key)
			if !ok {
				t.Errorf("key %s not found after flush", key)
				continue
			}
			if row.Columns[colVal].Int64 != expected {
				t.Errorf("key %s: expected %d, got %d", key, expected, row.Columns[colVal].Int64)
			}
		}
	}
}

// TestConcurrent_WriteFlushCompact 验证并发写入、Flush 和 Compact 的数据一致性。
func TestConcurrent_WriteFlushCompact(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// Write initial data and flush to create L0 segments
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("init_%04d", i)
		_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))})
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("initial flush: %v", err)
	}

	const writers = 4
	const writesPerWriter = 50
	var wg sync.WaitGroup

	// Concurrent writes
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < writesPerWriter; j++ {
				key := fmt.Sprintf("cw%d_key_%04d", gid, j)
				_ = eng.Write(key, map[string]common.Value{
					colVal: common.NewInt64(int64(gid*10000 + j)),
				})
			}
		}(g)
	}

	// Concurrent flush and compact
	var bgWg sync.WaitGroup
	bgWg.Add(1)
	go func() {
		defer bgWg.Done()
		for i := 0; i < 3; i++ {
			time.Sleep(20 * time.Millisecond)
			_ = eng.Flush(cols)
		}
	}()
	bgWg.Add(1)
	go func() {
		defer bgWg.Done()
		time.Sleep(50 * time.Millisecond)
		if eng.ShouldCompact() {
			_ = eng.Compact(cols)
		}
	}()

	wg.Wait()
	bgWg.Wait()

	// Verify all concurrent write data is accessible
	for g := 0; g < writers; g++ {
		for j := 0; j < writesPerWriter; j++ {
			key := fmt.Sprintf("cw%d_key_%04d", g, j)
			expected := int64(g*10000 + j)
			row, ok := eng.Get(key)
			if !ok {
				t.Errorf("key %s not found", key)
				continue
			}
			if row.Columns[colVal].Int64 != expected {
				t.Errorf("key %s: expected %d, got %d", key, expected, row.Columns[colVal].Int64)
			}
		}
	}
}

// TestConcurrent_OverwriteConsistency 验证并发覆盖写入后，最终读取到的是最后一次写入的值之一。
func TestConcurrent_OverwriteConsistency(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	const key = "shared_key"
	const overwriters = 10
	var wg sync.WaitGroup

	// Each goroutine overwrites the same key with its own ID
	for g := 0; g < overwriters; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = eng.Write(key, map[string]common.Value{
					colVal: common.NewInt64(int64(gid)),
				})
			}
		}(g)
	}
	wg.Wait()

	// The final value must be one of the goroutine IDs
	row, ok := eng.Get(key)
	if !ok {
		t.Fatal("shared_key not found")
	}
	val := row.Columns[colVal].Int64
	if val < 0 || val >= overwriters {
		t.Errorf("unexpected value %d for shared_key, expected one of [0, %d]", val, overwriters-1)
	}
}

// TestConcurrent_ScanWhileWriting 验证写入过程中进行 Scan 不会崩溃。
func TestConcurrent_ScanWhileWriting(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// Pre-write data
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("scan_key_%04d", i)
		_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))})
	}

	const writers = 4
	const scanners = 4
	const opsPerWorker = 50
	var wg sync.WaitGroup
	var scanErr atomic.Int32

	// Concurrent writers
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < opsPerWorker; j++ {
				key := fmt.Sprintf("sw%d_key_%04d", gid, j)
				_ = eng.Write(key, map[string]common.Value{
					colVal: common.NewInt64(int64(gid*1000 + j)),
				})
			}
		}(g)
	}

	// Concurrent scanners
	for s := 0; s < scanners; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerWorker; j++ {
				results := eng.Scan("scan_key_0000", "scan_key_0099")
				// Results should be sorted
				for i := 1; i < len(results); i++ {
					if results[i].Key < results[i-1].Key {
						scanErr.Add(1)
						break
					}
				}
			}
		}()
	}

	wg.Wait()

	if scanErr.Load() > 0 {
		t.Errorf("scan consistency errors: %d", scanErr.Load())
	}
}

// TestConcurrent_WithScheduler 验证后台调度器运行时的并发读写一致性。
func TestConcurrent_WithScheduler(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{
		FlushInterval:    50 * time.Millisecond,
		CompactInterval:  100 * time.Millisecond,
		WALCleanInterval: 200 * time.Millisecond,
	})
	sched.Start()
	defer sched.Stop()

	const writers = 4
	const writesPerWriter = 100
	var wg sync.WaitGroup

	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < writesPerWriter; j++ {
				key := fmt.Sprintf("sched%d_key_%04d", gid, j)
				_ = eng.Write(key, map[string]common.Value{
					colVal: common.NewInt64(int64(gid*10000 + j)),
				})
			}
		}(g)
	}

	wg.Wait()

	// Give scheduler time to process
	time.Sleep(300 * time.Millisecond)

	// Verify all data
	for g := 0; g < writers; g++ {
		for j := 0; j < writesPerWriter; j++ {
			key := fmt.Sprintf("sched%d_key_%04d", g, j)
			expected := int64(g*10000 + j)
			row, ok := eng.Get(key)
			if !ok {
				t.Errorf("key %s not found", key)
				continue
			}
			if row.Columns[colVal].Int64 != expected {
				t.Errorf("key %s: expected %d, got %d", key, expected, row.Columns[colVal].Int64)
			}
		}
	}
}
