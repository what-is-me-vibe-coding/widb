package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// waitForCondition 轮询等待条件满足，避免 time.Sleep 导致的 flaky 测试。
func waitForCondition(t *testing.T, interval, timeout time.Duration, condition func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(interval)
	}
	t.Fatalf("timed out waiting for condition: %s", msg)
}

func TestNewScheduler(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{})
	if sched == nil {
		t.Fatal("NewScheduler returned nil")
	}

	if sched.config.FlushInterval != defaultFlushInterval {
		t.Errorf("FlushInterval = %v, want %v", sched.config.FlushInterval, defaultFlushInterval)
	}
	if sched.config.CompactInterval != defaultCompactInterval {
		t.Errorf("CompactInterval = %v, want %v", sched.config.CompactInterval, defaultCompactInterval)
	}
	if sched.config.WALCleanInterval != defaultWALCleanInterval {
		t.Errorf("WALCleanInterval = %v, want %v", sched.config.WALCleanInterval, defaultWALCleanInterval)
	}
}

func TestSchedulerCustomConfig(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cfg := SchedulerConfig{
		FlushInterval:     1 * time.Second,
		CompactInterval:   2 * time.Second,
		WALCleanInterval:  3 * time.Second,
		WALCleanThreshold: 1024,
	}
	sched := NewScheduler(eng, cfg)

	if sched.config.FlushInterval != 1*time.Second {
		t.Errorf("FlushInterval = %v, want 1s", sched.config.FlushInterval)
	}
	if sched.config.CompactInterval != 2*time.Second {
		t.Errorf("CompactInterval = %v, want 2s", sched.config.CompactInterval)
	}
	if sched.config.WALCleanInterval != 3*time.Second {
		t.Errorf("WALCleanInterval = %v, want 3s", sched.config.WALCleanInterval)
	}
	if sched.config.WALCleanThreshold != 1024 {
		t.Errorf("WALCleanThreshold = %d, want 1024", sched.config.WALCleanThreshold)
	}
}

func TestSchedulerStartStop(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{
		FlushInterval:    100 * time.Millisecond,
		CompactInterval:  100 * time.Millisecond,
		WALCleanInterval: 100 * time.Millisecond,
	})

	sched.Start()

	_ = sched.Stats()

	sched.Stop()

	// Stop 后应能安全调用多次
	sched.Stop()
}

func TestSchedulerAutoFlush(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir:         dir,
		MaxMemTableSize: 1024, // 很小的阈值便于触发
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{
		{ID: 0, Name: "id", Type: common.TypeInt64},
	}

	sched := NewScheduler(eng, SchedulerConfig{
		FlushInterval:    50 * time.Millisecond,
		CompactInterval:  1 * time.Hour, // 禁用 compaction
		WALCleanInterval: 1 * time.Hour, // 禁用 WAL 清理
	})
	sched.Start()
	defer sched.Stop()

	// 写入足够数据触发 ShouldFlush
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key_%04d", i)
		values := map[string]common.Value{
			"id": common.NewInt64(int64(i)),
		}
		if err := eng.Write(key, values); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	// 先手动 Flush 一次以设置 columnMeta
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Initial Flush: %v", err)
	}

	// 继续写入数据让 MemTable 再次达到阈值
	for i := 100; i < 200; i++ {
		key := fmt.Sprintf("key_%04d", i)
		values := map[string]common.Value{
			"id": common.NewInt64(int64(i)),
		}
		if err := eng.Write(key, values); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	// 等待调度器触发刷盘
	waitForCondition(t, 20*time.Millisecond, 2*time.Second, func() bool {
		return sched.Stats().FlushCount > 0
	}, "scheduler auto flush")

	stats := sched.Stats()
	if stats.FlushCount == 0 {
		t.Error("expected at least one flush, got 0")
	}
}

func TestSchedulerAutoCompact(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{
		{ID: 0, Name: "id", Type: common.TypeInt64},
	}

	sched := NewScheduler(eng, SchedulerConfig{
		FlushInterval:    1 * time.Hour, // 禁用自动 flush
		CompactInterval:  50 * time.Millisecond,
		WALCleanInterval: 1 * time.Hour,
	})
	sched.Start()
	defer sched.Stop()

	// 手动写入并刷盘多个 Segment，触发 Compaction
	for batch := 0; batch < defaultL0CompactionThreshold; batch++ {
		for i := 0; i < 10; i++ {
			key := fmt.Sprintf("b%d_key_%04d", batch, i)
			values := map[string]common.Value{
				"id": common.NewInt64(int64(batch*10 + i)),
			}
			if err := eng.Write(key, values); err != nil {
				t.Fatalf("Write: %v", err)
			}
		}
		if err := eng.Flush(cols); err != nil {
			t.Fatalf("Flush batch %d: %v", batch, err)
		}
	}

	// 等待调度器触发 Compaction
	waitForCondition(t, 20*time.Millisecond, 2*time.Second, func() bool {
		return sched.Stats().CompactCount > 0
	}, "scheduler auto compact")

	stats := sched.Stats()
	if stats.CompactCount == 0 {
		t.Error("expected at least one compaction, got 0")
	}
}

func TestSchedulerWALClean(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{
		FlushInterval:     1 * time.Hour,
		CompactInterval:   1 * time.Hour,
		WALCleanInterval:  50 * time.Millisecond,
		WALCleanThreshold: 1, // 极小阈值，任何旧文件都会被清理
	})
	sched.Start()
	defer sched.Stop()

	// 创建一个模拟的旧 WAL 文件
	prevPath := filepath.Join(dir, "wal.log.prev")
	if err := os.WriteFile(prevPath, make([]byte, 100), 0644); err != nil {
		t.Fatalf("write prev WAL: %v", err)
	}

	// 等待调度器清理
	waitForCondition(t, 20*time.Millisecond, 2*time.Second, func() bool {
		_, err := os.Stat(prevPath)
		return os.IsNotExist(err)
	}, "old WAL file to be cleaned up")

	if _, err := os.Stat(prevPath); !os.IsNotExist(err) {
		t.Error("expected old WAL file to be cleaned up")
	}

	stats := sched.Stats()
	if stats.WALCleanCount == 0 {
		t.Error("expected at least one WAL clean, got 0")
	}
}

func TestSchedulerWALCleanNoFile(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{
		FlushInterval:     1 * time.Hour,
		CompactInterval:   1 * time.Hour,
		WALCleanInterval:  50 * time.Millisecond,
		WALCleanThreshold: 1,
	})
	sched.Start()
	defer sched.Stop()

	// 不创建旧 WAL 文件，调度器应正常运行不报错
	// 轮询检查，如果出现错误立即失败；超时说明无错误，测试通过
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sched.Stats().LastError != "" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	stats := sched.Stats()
	if stats.LastError != "" {
		t.Errorf("unexpected error: %s", stats.LastError)
	}
}

func TestSchedulerStats(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{})

	stats := sched.Stats()
	if stats.FlushCount != 0 || stats.CompactCount != 0 || stats.WALCleanCount != 0 {
		t.Error("initial stats should be zero")
	}
}

func TestSchedulerStopIdempotent(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{
		FlushInterval:    50 * time.Millisecond,
		CompactInterval:  50 * time.Millisecond,
		WALCleanInterval: 50 * time.Millisecond,
	})

	sched.Start()
	// 短暂等待确保调度器已启动
	time.Sleep(10 * time.Millisecond)

	// 多次 Stop 不应 panic
	sched.Stop()
	sched.Stop()
	sched.Stop()
}

func TestSchedulerNoActionWhenNotNeeded(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{
		FlushInterval:    50 * time.Millisecond,
		CompactInterval:  50 * time.Millisecond,
		WALCleanInterval: 50 * time.Millisecond,
	})
	sched.Start()
	defer sched.Stop()

	// 不写入任何数据，调度器应正常运行
	// 轮询检查，如果出现 flush/compact 立即失败；超时说明无操作，测试通过
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stats := sched.Stats()
		if stats.FlushCount > 0 || stats.CompactCount > 0 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	stats := sched.Stats()
	// 无数据时不应触发 flush 或 compact
	if stats.FlushCount != 0 {
		t.Errorf("expected 0 flushes with no data, got %d", stats.FlushCount)
	}
	if stats.CompactCount != 0 {
		t.Errorf("expected 0 compactions with no data, got %d", stats.CompactCount)
	}
}

func TestEngineStartScheduler(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// 未启动调度器时，SchedulerStats 应返回 ok=false
	_, ok := eng.SchedulerStats()
	if ok {
		t.Error("expected ok=false before starting scheduler")
	}

	// 启动调度器
	eng.StartScheduler(SchedulerConfig{
		FlushInterval:    50 * time.Millisecond,
		CompactInterval:  50 * time.Millisecond,
		WALCleanInterval: 50 * time.Millisecond,
	})

	// 启动后应返回 ok=true
	_, ok = eng.SchedulerStats()
	if !ok {
		t.Error("expected ok=true after starting scheduler")
	}

	// 重复调用 StartScheduler 不应 panic 或创建多个调度器
	eng.StartScheduler(SchedulerConfig{
		FlushInterval:    50 * time.Millisecond,
		CompactInterval:  50 * time.Millisecond,
		WALCleanInterval: 50 * time.Millisecond,
	})

	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestEngineSchedulerAutoFlush(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir:         dir,
		MaxMemTableSize: 1024,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	cols := []ColumnMeta{
		{ID: 0, Name: "id", Type: common.TypeInt64},
	}

	eng.StartScheduler(SchedulerConfig{
		FlushInterval:    50 * time.Millisecond,
		CompactInterval:  1 * time.Hour,
		WALCleanInterval: 1 * time.Hour,
	})

	// 写入数据并手动 Flush 一次以设置 columnMeta
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key_%04d", i)
		values := map[string]common.Value{
			"id": common.NewInt64(int64(i)),
		}
		if err := eng.Write(key, values); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Initial Flush: %v", err)
	}

	// 继续写入数据让 MemTable 再次达到阈值
	for i := 100; i < 200; i++ {
		key := fmt.Sprintf("key_%04d", i)
		values := map[string]common.Value{
			"id": common.NewInt64(int64(i)),
		}
		if err := eng.Write(key, values); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	// 等待调度器触发刷盘
	waitForCondition(t, 20*time.Millisecond, 2*time.Second, func() bool {
		stats, _ := eng.SchedulerStats()
		return stats.FlushCount > 0
	}, "engine scheduler auto flush")

	stats, ok := eng.SchedulerStats()
	if !ok {
		t.Fatal("expected scheduler to be running")
	}
	if stats.FlushCount == 0 {
		t.Error("expected at least one auto flush via engine scheduler")
	}

	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestEngineCloseStopsScheduler(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	eng.StartScheduler(SchedulerConfig{
		FlushInterval:    50 * time.Millisecond,
		CompactInterval:  50 * time.Millisecond,
		WALCleanInterval: 50 * time.Millisecond,
	})

	// Close 应该停止调度器
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Close 后 SchedulerStats 应返回 ok=false
	_, ok := eng.SchedulerStats()
	if ok {
		t.Error("expected ok=false after engine close")
	}
}
