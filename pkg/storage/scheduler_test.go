package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

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
	time.Sleep(50 * time.Millisecond)

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
	time.Sleep(300 * time.Millisecond)

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
	time.Sleep(300 * time.Millisecond)

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
	time.Sleep(300 * time.Millisecond)

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
	time.Sleep(200 * time.Millisecond)

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
	time.Sleep(30 * time.Millisecond)

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
	time.Sleep(200 * time.Millisecond)

	stats := sched.Stats()
	// 无数据时不应触发 flush 或 compact
	if stats.FlushCount != 0 {
		t.Errorf("expected 0 flushes with no data, got %d", stats.FlushCount)
	}
	if stats.CompactCount != 0 {
		t.Errorf("expected 0 compactions with no data, got %d", stats.CompactCount)
	}
}
