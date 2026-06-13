package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

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
