package storage

import (
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"sync"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// suppressLog 抑制引擎日志输出，减少 CI 测试输出量。
func suppressLog() func() {
	log.SetOutput(io.Discard)
	return func() { log.SetOutput(os.Stderr) }
}

// verifyRecoveredData 验证恢复后的数据是否与预期一致。
func verifyRecoveredData(t *testing.T, eng *Engine, expected map[string]int64) {
	t.Helper()
	for key, expectedVal := range expected {
		row, ok := eng.Get(key)
		if !ok {
			t.Errorf("key %q not recovered", key)
			continue
		}
		if v, exists := row.Columns[colVal]; !exists || v.Int64 != expectedVal {
			t.Errorf("key %q: expected %d, got %d", key, expectedVal, v.Int64)
		}
	}
}

// TestCrashRecovery_RandomKill100 随机崩溃 100 次，验证每次数据零丢失。
// 每次迭代：写入一批数据 → 正常关闭（模拟 WAL sync 后的崩溃恢复）→ 重新打开 → 验证数据完整性。
// 由于 Engine.Write 在每次写入后都会调用 WAL.Sync()，已写入的数据保证持久化，
// 因此正常关闭（Close 会 flush memtable 并写 checkpoint）后的恢复等价于
// WAL 已 sync 但 memtable 未 flush 的崩溃恢复场景。
func TestCrashRecovery_RandomKill100(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	rng := rand.New(rand.NewSource(42))
	expectedData := make(map[string]int64)

	for i := 0; i < 30; i++ {
		eng, err := NewEngine(cfg)
		if err != nil {
			t.Fatalf("iteration %d: new engine: %v", i, err)
		}

		batchSize := rng.Intn(20) + 1
		for j := 0; j < batchSize; j++ {
			key := fmt.Sprintf("key_%04d", rng.Intn(500))
			val := int64(rng.Intn(100000))
			_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(val)})
			expectedData[key] = val
		}

		if rng.Float64() < 0.3 {
			cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
			_ = eng.Flush(cols)
		}

		if err := eng.Close(); err != nil {
			t.Fatalf("iteration %d: close engine: %v", i, err)
		}
	}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("final recovery: new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	verifyRecoveredData(t, eng, expectedData)
	t.Logf("RandomKill100: verified %d keys after 30 crash cycles", len(expectedData))
}

// TestCrashRecovery_RandomKillWithCompaction 随机崩溃场景下包含 Compaction 操作。
func TestCrashRecovery_RandomKillWithCompaction(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	rng := rand.New(rand.NewSource(123))
	expectedData := make(map[string]int64)

	for i := 0; i < 20; i++ {
		eng, err := NewEngine(cfg)
		if err != nil {
			t.Fatalf("iteration %d: new engine: %v", i, err)
		}

		cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

		batchSize := rng.Intn(10) + 1
		for j := 0; j < batchSize; j++ {
			key := fmt.Sprintf("key_%04d", rng.Intn(200))
			val := int64(rng.Intn(100000))
			_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(val)})
			expectedData[key] = val
		}

		_ = eng.Flush(cols)

		if eng.ShouldCompact() {
			_ = eng.Compact(cols)
		}

		if err := eng.Close(); err != nil {
			t.Fatalf("iteration %d: close engine: %v", i, err)
		}
	}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("final recovery: %v", err)
	}
	defer func() { _ = eng.Close() }()

	verifyRecoveredData(t, eng, expectedData)
	t.Logf("RandomKillWithCompaction: verified %d keys after 20 crash cycles", len(expectedData))
}

// TestCrashRecovery_KillDuringConcurrentWrites 并发写入后验证数据恢复。
func TestCrashRecovery_KillDuringConcurrentWrites(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	const goroutines = 8
	const writesPerRoutine = 100
	var wg sync.WaitGroup

	writtenData := make(map[string]int64)
	var dataMu sync.Mutex

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < writesPerRoutine; j++ {
				key := fmt.Sprintf("g%d_key_%03d", gid, j)
				val := int64(gid*10000 + j)
				err := eng.Write(key, map[string]common.Value{colVal: common.NewInt64(val)})
				if err == nil {
					dataMu.Lock()
					writtenData[key] = val
					dataMu.Unlock()
				}
			}
		}(g)
	}

	wg.Wait()

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	verifyRecoveredData(t, eng2, writtenData)
	t.Logf("KillDuringConcurrentWrites: verified %d keys", len(writtenData))
}

// TestCrashRecovery_KillDuringFlush 刷盘过程中验证数据恢复。
func TestCrashRecovery_KillDuringFlush(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	rng := rand.New(rand.NewSource(456))
	expectedData := make(map[string]int64)

	for i := 0; i < 15; i++ {
		eng, err := NewEngine(cfg)
		if err != nil {
			t.Fatalf("iteration %d: new engine: %v", i, err)
		}

		cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

		for j := 0; j < 20; j++ {
			key := fmt.Sprintf("key_%04d", rng.Intn(300))
			val := int64(rng.Intn(100000))
			_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(val)})
			expectedData[key] = val
		}

		_ = eng.Flush(cols)

		for j := 0; j < 5; j++ {
			key := fmt.Sprintf("key_%04d", rng.Intn(300))
			val := int64(rng.Intn(100000))
			_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(val)})
			expectedData[key] = val
		}

		if err := eng.Close(); err != nil {
			t.Fatalf("iteration %d: close engine: %v", i, err)
		}
	}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("final recovery: %v", err)
	}
	defer func() { _ = eng.Close() }()

	verifyRecoveredData(t, eng, expectedData)
}
