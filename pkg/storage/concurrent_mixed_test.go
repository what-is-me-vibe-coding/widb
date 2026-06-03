package storage

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// startMixedWriters 启动 4 个并发写入 goroutine，持续写入直到收到停止信号。
func startMixedWriters(eng *Engine, done <-chan struct{}, wg *sync.WaitGroup, ops *atomic.Int64) {
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			i := 0
			for {
				select {
				case <-done:
					return
				default:
					key := fmt.Sprintf("mix_w%d_%04d", gid, i)
					_ = eng.Write(key, map[string]common.Value{
						colVal: common.NewInt64(int64(gid*10000 + i)),
					})
					ops.Add(1)
					i++
				}
			}
		}(g)
	}
}

// startMixedReaders 启动 4 个并发读取 goroutine，持续读取直到收到停止信号。
func startMixedReaders(eng *Engine, done <-chan struct{}, wg *sync.WaitGroup, ops *atomic.Int64) {
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					_, _ = eng.Get("mix_pre_0010")
					ops.Add(1)
				}
			}
		}()
	}
}

// startMixedScanners 启动 2 个并发扫描 goroutine，持续扫描直到收到停止信号。
func startMixedScanners(eng *Engine, done <-chan struct{}, wg *sync.WaitGroup, ops *atomic.Int64, errors *atomic.Int32) {
	for s := 0; s < 2; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					results := eng.Scan("mix_pre_0000", "mix_pre_0049")
					for i := 1; i < len(results); i++ {
						if results[i].Key < results[i-1].Key {
							errors.Add(1)
							break
						}
					}
					ops.Add(1)
				}
			}
		}()
	}
}

// startMixedFlusher 启动 1 个周期性 Flush goroutine，持续刷新直到收到停止信号。
func startMixedFlusher(eng *Engine, cols []ColumnMeta, done <-chan struct{}, wg *sync.WaitGroup, ops *atomic.Int64) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				_ = eng.Flush(cols)
				ops.Add(1)
				time.Sleep(50 * time.Millisecond)
			}
		}
	}()
}

// TestConcurrent_MixedOperations 验证混合并发操作（Write/Get/Scan/Flush）的数据一致性。
func TestConcurrent_MixedOperations(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// Pre-write data
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("mix_pre_%04d", i)
		_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))})
	}

	const duration = 500 * time.Millisecond
	var ops atomic.Int64
	var errors atomic.Int32

	done := make(chan struct{})
	time.AfterFunc(duration, func() { close(done) })

	var wg sync.WaitGroup
	startMixedWriters(eng, done, &wg, &ops)
	startMixedReaders(eng, done, &wg, &ops)
	startMixedScanners(eng, done, &wg, &ops, &errors)
	startMixedFlusher(eng, cols, done, &wg, &ops)

	wg.Wait()

	t.Logf("Completed %d operations, %d errors", ops.Load(), errors.Load())
	if errors.Load() > 0 {
		t.Errorf("found %d consistency errors during mixed operations", errors.Load())
	}
}

// verifyRecoveredIntKeys 验证崩溃恢复后指定前缀的 int64 键值是否正确。
func verifyRecoveredIntKeys(t *testing.T, eng *Engine, prefix string, groups, perGroup int) {
	t.Helper()
	for g := 0; g < groups; g++ {
		for j := 0; j < perGroup; j++ {
			key := fmt.Sprintf("%s%d_key_%04d", prefix, g, j)
			expected := int64(g*10000 + j)
			row, ok := eng.Get(key)
			if !ok {
				t.Errorf("key %s not recovered", key)
				continue
			}
			if row.Columns[colVal].Int64 != expected {
				t.Errorf("key %s: expected %d, got %d", key, expected, row.Columns[colVal].Int64)
			}
		}
	}
}

// verifyRecoveredExtraKeys 验证崩溃恢复后从 WAL 恢复的额外键值是否正确。
func verifyRecoveredExtraKeys(t *testing.T, eng *Engine, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("rcv_extra_%04d", i)
		expected := int64(i + 1000)
		row, ok := eng.Get(key)
		if !ok {
			t.Errorf("extra key %s not recovered from WAL", key)
			continue
		}
		if row.Columns[colVal].Int64 != expected {
			t.Errorf("extra key %s: expected %d, got %d", key, expected, row.Columns[colVal].Int64)
		}
	}
}

// TestConcurrent_WriteAfterFlushRecovery 验证并发写入后 Flush，数据在崩溃恢复后仍然一致。
func TestConcurrent_WriteAfterFlushRecovery(t *testing.T) {
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	const writers = 4
	const writesPerWriter = 50
	var wg sync.WaitGroup

	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < writesPerWriter; j++ {
				key := fmt.Sprintf("rcv%d_key_%04d", gid, j)
				_ = eng.Write(key, map[string]common.Value{
					colVal: common.NewInt64(int64(gid*10000 + j)),
				})
			}
		}(g)
	}
	wg.Wait()

	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	for i := 0; i < 30; i++ {
		key := fmt.Sprintf("rcv_extra_%04d", i)
		_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i + 1000))})
	}

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	verifyRecoveredIntKeys(t, eng2, "rcv", writers, writesPerWriter)
	verifyRecoveredExtraKeys(t, eng2, 30)
}
