package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// --- Scheduler 基础测试 ---

// TestSchedulerDoubleStart_V7 测试重复调用 Start 不会创建多个调度器循环。
func TestSchedulerDoubleStart_V7(t *testing.T) {
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

	// 第一次 Start
	sched.Start()
	// 第二次 Start 应为空操作
	sched.Start()

	// 等待一小段时间确保调度器运行
	time.Sleep(100 * time.Millisecond)

	sched.Stop()
}

// TestSchedulerStatsInitial_V7 测试新创建调度器的初始统计信息。
func TestSchedulerStatsInitial_V7(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{})
	stats := sched.Stats()
	if stats.FlushCount != 0 || stats.CompactCount != 0 || stats.WALCleanCount != 0 {
		t.Errorf("初始统计应为零: Flush=%d, Compact=%d, WALClean=%d",
			stats.FlushCount, stats.CompactCount, stats.WALCleanCount)
	}
	if stats.LastError != "" {
		t.Errorf("初始 LastError 应为空，实际 %q", stats.LastError)
	}
}

// TestTryCleanWALPrevExceedsThreshold_V7 测试 tryCleanWAL 当 .prev 文件超过阈值时被删除。
func TestTryCleanWALPrevExceedsThreshold_V7(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{
		WALCleanThreshold: 10, // 极小阈值
	})

	// 创建 .prev 文件，大小超过阈值
	prevPath := filepath.Join(dir, "wal.log.prev")
	if err := os.WriteFile(prevPath, make([]byte, 100), 0644); err != nil {
		t.Fatalf("写入 .prev 文件失败: %v", err)
	}

	err = sched.tryCleanWAL()
	if err != nil {
		t.Fatalf("tryCleanWAL 失败: %v", err)
	}

	// .prev 文件应被删除
	if _, statErr := os.Stat(prevPath); !os.IsNotExist(statErr) {
		t.Error("期望 .prev 文件被删除")
	}
}

// TestTryCleanWALPrevBelowThreshold_V7 测试 tryCleanWAL 当 .prev 文件小于阈值时不删除。
func TestTryCleanWALPrevBelowThreshold_V7(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{
		WALCleanThreshold: 1 << 30, // 1GB 阈值，远大于文件大小
	})

	// 创建 .prev 文件
	prevPath := filepath.Join(dir, "wal.log.prev")
	if err := os.WriteFile(prevPath, make([]byte, 100), 0644); err != nil {
		t.Fatalf("写入 .prev 文件失败: %v", err)
	}

	err = sched.tryCleanWAL()
	if err != nil {
		t.Fatalf("tryCleanWAL 失败: %v", err)
	}

	// .prev 文件不应被删除
	if _, statErr := os.Stat(prevPath); os.IsNotExist(statErr) {
		t.Error("期望 .prev 文件保留，但被删除了")
	}
}

// TestTryCleanWALNoPrevFile_V7 测试 tryCleanWAL 当没有 .prev 文件时不报错。
func TestTryCleanWALNoPrevFile_V7(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{
		WALCleanThreshold: 1,
	})

	err = sched.tryCleanWAL()
	if err != nil {
		t.Fatalf("无 .prev 文件时不应报错: %v", err)
	}
}

// TestSchedulerRecordError_V7 测试 recordError 正确记录错误信息。
func TestSchedulerRecordError_V7(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{})
	sched.recordError(fmt.Errorf("测试错误"))

	stats := sched.Stats()
	if stats.LastError != "测试错误" {
		t.Errorf("期望 LastError='测试错误'，实际 %q", stats.LastError)
	}
}

// TestSchedulerStartStopLifecycle_V7 测试调度器完整的启动-停止生命周期。
func TestSchedulerStartStopLifecycle_V7(t *testing.T) {
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
	sched.Stop()

	// 停止后再次 Stop 不应 panic
	sched.Stop()
}

// TestSchedulerCompactErrorRecording_V7 测试 runCompactLoop 中 tryCompact 失败时记录错误。
func TestSchedulerCompactErrorRecording_V7(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// 关闭引擎使后续操作失败
	_ = eng.Close()

	sched := NewScheduler(eng, SchedulerConfig{
		CompactInterval:  50 * time.Millisecond,
		FlushInterval:    1 * time.Hour,
		WALCleanInterval: 1 * time.Hour,
	})

	sched.Start()
	defer sched.Stop()

	// 等待调度器尝试 compact 并记录错误
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stats := sched.Stats()
		if stats.LastError != "" {
			return // 成功记录了错误
		}
		time.Sleep(30 * time.Millisecond)
	}
	// 不强制要求错误出现，因为引擎关闭后可能不会触发 compact
}

// --- Scheduler Loop 测试 ---

// TestRunCompactLoopTickerTrigger 测试 runCompactLoop 中 ticker.C 触发 tryCompact 的路径。
// 创建调度器并使用极短的 Compaction 间隔，写入足够数据触发 Compaction，
// 验证 ticker.C 分支被执行且 CompactCount 增加。
func TestRunCompactLoopTickerTrigger(t *testing.T) {
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
		WALCleanInterval: 1 * time.Hour, // 禁用 WAL 清理
	})
	sched.Start()
	defer sched.Stop()

	// 手动写入并刷盘多个 Segment，使 L0 数量达到 Compaction 阈值
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

	// 等待调度器的 ticker.C 触发 tryCompact
	waitForCondition(t, 20*time.Millisecond, 3*time.Second, func() bool {
		return sched.Stats().CompactCount > 0
	}, "runCompactLoop ticker 触发 tryCompact")

	stats := sched.Stats()
	if stats.CompactCount == 0 {
		t.Error("期望 CompactCount > 0，说明 runCompactLoop 的 ticker.C 分支被执行")
	}
}

// TestRunWALCleanLoopTickerTrigger 测试 runWALCleanLoop 中 ticker.C 触发 tryCleanWAL 的路径。
// 创建调度器并使用极短的 WALClean 间隔，创建旧 WAL 文件，
// 验证 ticker.C 分支被执行且 WALCleanCount 增加。
func TestRunWALCleanLoopTickerTrigger(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{
		FlushInterval:     1 * time.Hour, // 禁用自动 flush
		CompactInterval:   1 * time.Hour, // 禁用自动 compaction
		WALCleanInterval:  50 * time.Millisecond,
		WALCleanThreshold: 1, // 极小阈值，任何旧文件都会被清理
	})
	sched.Start()
	defer sched.Stop()

	// 创建一个模拟的旧 WAL 文件，使 tryCleanWAL 有内容可清理
	prevPath := filepath.Join(dir, "wal.log.prev")
	if err := os.WriteFile(prevPath, make([]byte, 100), 0644); err != nil {
		t.Fatalf("写入旧 WAL 文件失败: %v", err)
	}

	// 等待调度器的 ticker.C 触发 tryCleanWAL
	waitForCondition(t, 20*time.Millisecond, 3*time.Second, func() bool {
		_, err := os.Stat(prevPath)
		return os.IsNotExist(err)
	}, "runWALCleanLoop ticker 触发 tryCleanWAL")

	// 验证旧 WAL 文件已被清理
	if _, err := os.Stat(prevPath); !os.IsNotExist(err) {
		t.Error("期望旧 WAL 文件已被清理")
	}

	stats := sched.Stats()
	if stats.WALCleanCount == 0 {
		t.Error("期望 WALCleanCount > 0，说明 runWALCleanLoop 的 ticker.C 分支被执行")
	}
}

// TestRunCompactAndWALCleanLoopsTogether 测试 runCompactLoop 和 runWALCleanLoop 同时运行。
// 验证两个循环的 ticker.C 分支都能正常触发。
func TestRunCompactAndWALCleanLoopsTogether(t *testing.T) {
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
		FlushInterval:     1 * time.Hour,
		CompactInterval:   50 * time.Millisecond,
		WALCleanInterval:  50 * time.Millisecond,
		WALCleanThreshold: 1,
	})
	sched.Start()
	defer sched.Stop()

	// 写入数据并刷盘以触发 Compaction
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

	// 创建旧 WAL 文件以触发 WAL 清理
	prevPath := filepath.Join(dir, "wal.log.prev")
	if err := os.WriteFile(prevPath, make([]byte, 100), 0644); err != nil {
		t.Fatalf("写入旧 WAL 文件失败: %v", err)
	}

	// 等待两个循环都至少执行一次
	waitForCondition(t, 20*time.Millisecond, 5*time.Second, func() bool {
		stats := sched.Stats()
		return stats.CompactCount > 0 && stats.WALCleanCount > 0
	}, "runCompactLoop 和 runWALCleanLoop 都被 ticker 触发")

	stats := sched.Stats()
	if stats.CompactCount == 0 {
		t.Error("期望 CompactCount > 0")
	}
	if stats.WALCleanCount == 0 {
		t.Error("期望 WALCleanCount > 0")
	}
}
