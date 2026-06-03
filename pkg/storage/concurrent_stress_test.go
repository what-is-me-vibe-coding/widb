package storage

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// writeConcurrentInts 在 goroutine 中并发写入 int 类型数据。
func writeConcurrentInts(eng *Engine, count int, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < count; i++ {
			_ = eng.Write(fmt.Sprintf("int_%04d", i), map[string]common.Value{
				colVal: common.NewInt64(int64(i)),
			})
		}
	}()
}

// writeConcurrentFloats 在 goroutine 中并发写入 float 类型数据。
func writeConcurrentFloats(eng *Engine, count int, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < count; i++ {
			_ = eng.Write(fmt.Sprintf("float_%04d", i), map[string]common.Value{
				colVal: common.NewFloat64(float64(i) * 1.1),
			})
		}
	}()
}

// writeConcurrentStrings 在 goroutine 中并发写入 string 类型数据。
func writeConcurrentStrings(eng *Engine, count int, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < count; i++ {
			_ = eng.Write(fmt.Sprintf("str_%04d", i), map[string]common.Value{
				colVal: common.NewString(fmt.Sprintf("hello_%d", i)),
			})
		}
	}()
}

// writeConcurrentBools 在 goroutine 中并发写入 bool 类型数据。
func writeConcurrentBools(eng *Engine, count int, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < count; i++ {
			_ = eng.Write(fmt.Sprintf("bool_%04d", i), map[string]common.Value{
				colVal: common.NewBool(i%2 == 0),
			})
		}
	}()
}

// verifyIntValues 验证 int 类型数据是否正确写入。
func verifyIntValues(t *testing.T, eng *Engine, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		row, ok := eng.Get(fmt.Sprintf("int_%04d", i))
		if !ok || row.Columns[colVal].Int64 != int64(i) {
			t.Errorf("int_%04d not correct", i)
		}
	}
}

// verifyFloatValues 验证 float 类型数据是否正确写入。
func verifyFloatValues(t *testing.T, eng *Engine, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		row, ok := eng.Get(fmt.Sprintf("float_%04d", i))
		if !ok {
			t.Errorf("float_%04d not found", i)
			continue
		}
		expected := float64(i) * 1.1
		if row.Columns[colVal].Float64 != expected {
			t.Errorf("float_%04d: expected %f, got %f", i, expected, row.Columns[colVal].Float64)
		}
	}
}

// verifyStringValues 验证 string 类型数据是否正确写入。
func verifyStringValues(t *testing.T, eng *Engine, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		row, ok := eng.Get(fmt.Sprintf("str_%04d", i))
		if !ok || row.Columns[colVal].Str != fmt.Sprintf("hello_%d", i) {
			t.Errorf("str_%04d not correct", i)
		}
	}
}

// verifyBoolValues 验证 bool 类型数据是否正确写入。
func verifyBoolValues(t *testing.T, eng *Engine, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		row, ok := eng.Get(fmt.Sprintf("bool_%04d", i))
		if !ok {
			t.Errorf("bool_%04d not found", i)
			continue
		}
		expected := i%2 == 0
		got := row.Columns[colVal].Int64 != 0
		if got != expected {
			t.Errorf("bool_%04d: expected %v, got %v", i, expected, got)
		}
	}
}

// TestConcurrent_StressWrite 验证高并发写入压力下引擎的稳定性。
func TestConcurrent_StressWrite(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	const goroutines = 16
	const writesPerGoroutine = 200
	var wg sync.WaitGroup
	var errCount atomic.Int32

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < writesPerGoroutine; j++ {
				key := fmt.Sprintf("stress_%d_%04d", gid, j)
				if err := eng.Write(key, map[string]common.Value{
					colVal: common.NewInt64(int64(gid*100000 + j)),
				}); err != nil {
					errCount.Add(1)
				}
			}
		}(g)
	}

	wg.Wait()

	if errCount.Load() > 0 {
		t.Errorf("stress write errors: %d", errCount.Load())
	}

	// Verify total key count by reading each key
	verifiedCount := 0
	for g := 0; g < goroutines; g++ {
		for j := 0; j < writesPerGoroutine; j++ {
			key := fmt.Sprintf("stress_%d_%04d", g, j)
			if _, ok := eng.Get(key); ok {
				verifiedCount++
			}
		}
	}
	expectedCount := goroutines * writesPerGoroutine
	if verifiedCount != expectedCount {
		t.Errorf("expected %d keys, verified %d", expectedCount, verifiedCount)
	}
}

// TestConcurrent_MemTableRotationUnderLoad 验证高负载下 MemTable 自动轮转的正确性。
func TestConcurrent_MemTableRotationUnderLoad(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir:         t.TempDir(),
		MaxMemTableSize: 512, // Small size to trigger frequent rotations
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	const writers = 4
	const writesPerWriter = 100
	var wg sync.WaitGroup

	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < writesPerWriter; j++ {
				key := fmt.Sprintf("rot%d_%04d", gid, j)
				_ = eng.Write(key, map[string]common.Value{
					colVal: common.NewString(fmt.Sprintf("value_%d_%d", gid, j)),
				})
			}
		}(g)
	}

	wg.Wait()

	// Verify all data
	for g := 0; g < writers; g++ {
		for j := 0; j < writesPerWriter; j++ {
			key := fmt.Sprintf("rot%d_%04d", g, j)
			expected := fmt.Sprintf("value_%d_%d", g, j)
			row, ok := eng.Get(key)
			if !ok {
				t.Errorf("key %s not found", key)
				continue
			}
			if row.Columns[colVal].Str != expected {
				t.Errorf("key %s: expected %q, got %q", key, expected, row.Columns[colVal].Str)
			}
		}
	}
}

// TestConcurrent_MultipleDataTypeWriteRead 验证并发写入不同数据类型的一致性。
func TestConcurrent_MultipleDataTypeWriteRead(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	var wg sync.WaitGroup
	const count = 50

	writeConcurrentInts(eng, count, &wg)
	writeConcurrentFloats(eng, count, &wg)
	writeConcurrentStrings(eng, count, &wg)
	writeConcurrentBools(eng, count, &wg)

	wg.Wait()

	verifyIntValues(t, eng, count)
	verifyFloatValues(t, eng, count)
	verifyStringValues(t, eng, count)
	verifyBoolValues(t, eng, count)
}

// TestConcurrent_SnapshotIsolation 验证读取操作看到的是一致的数据快照。
// 写入新值不应影响正在进行的读取。
func TestConcurrent_SnapshotIsolation(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	const key = "snap_key"
	_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(1)})

	var wg sync.WaitGroup
	var readViolations atomic.Int32

	// Reader reads the key many times, should always get a valid value
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			row, ok := eng.Get(key)
			if !ok {
				readViolations.Add(1)
				continue
			}
			val := row.Columns[colVal].Int64
			// Value should be a positive integer (each write increments by 1)
			if val < 1 {
				readViolations.Add(1)
			}
		}
	}()

	// Writer continuously overwrites the key
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 2; i <= 1002; i++ {
			_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))})
		}
	}()

	wg.Wait()

	if readViolations.Load() > 0 {
		t.Errorf("snapshot isolation violations: %d", readViolations.Load())
	}
}

// TestConcurrent_FlushAndReadSegments 验证 Flush 产生的新 Segment 不影响并发读取。
func TestConcurrent_FlushAndReadSegments(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// Pre-write and flush
	for i := 0; i < 50; i++ {
		_ = eng.Write(fmt.Sprintf("seg_%04d", i), map[string]common.Value{colVal: common.NewInt64(int64(i))})
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("initial flush: %v", err)
	}

	var wg sync.WaitGroup
	var readErr atomic.Int32

	// Reader continuously reads from segments
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			row, ok := eng.Get("seg_0025")
			if !ok {
				readErr.Add(1)
			} else if row.Columns[colVal].Int64 != 25 {
				readErr.Add(1)
			}
		}
	}()

	// Writer adds more data and flushes
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 50; i < 100; i++ {
			_ = eng.Write(fmt.Sprintf("seg_%04d", i), map[string]common.Value{colVal: common.NewInt64(int64(i))})
		}
		_ = eng.Flush(cols)
	}()

	wg.Wait()

	if readErr.Load() > 0 {
		t.Errorf("segment read errors during flush: %d", readErr.Load())
	}
}
