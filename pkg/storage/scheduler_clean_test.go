package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestSchedulerTryCleanWALRemovesLargePrevFile 测试 tryCleanWAL 删除超过阈值的 .prev 文件
func TestSchedulerTryCleanWALRemovesLargePrevFile(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{
		WALCleanThreshold: 100, // 100 字节阈值
	})

	// 创建一个超过阈值的 .prev 文件
	prevPath := filepath.Join(dir, "wal.log.prev")
	largeData := make([]byte, 200) // 200 字节，超过阈值
	if err := os.WriteFile(prevPath, largeData, 0644); err != nil {
		t.Fatalf("write prev WAL: %v", err)
	}

	if err := sched.tryCleanWAL(); err != nil {
		t.Fatalf("tryCleanWAL: %v", err)
	}

	// .prev 文件应被删除
	if _, err := os.Stat(prevPath); !os.IsNotExist(err) {
		t.Error("expected large .prev file to be removed")
	}

	stats := sched.Stats()
	if stats.WALCleanCount != 1 {
		t.Errorf("expected WALCleanCount=1, got %d", stats.WALCleanCount)
	}
}

// TestSchedulerTryCleanWALSkipsSmallPrevFile 测试 tryCleanWAL 不删除小于阈值的 .prev 文件
func TestSchedulerTryCleanWALSkipsSmallPrevFile(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{
		WALCleanThreshold: 1024, // 1KB 阈值
	})

	// 创建一个小于阈值的 .prev 文件
	prevPath := filepath.Join(dir, "wal.log.prev")
	smallData := make([]byte, 100) // 100 字节，远小于阈值
	if err := os.WriteFile(prevPath, smallData, 0644); err != nil {
		t.Fatalf("write prev WAL: %v", err)
	}

	if err := sched.tryCleanWAL(); err != nil {
		t.Fatalf("tryCleanWAL: %v", err)
	}

	// .prev 文件不应被删除
	if _, err := os.Stat(prevPath); os.IsNotExist(err) {
		t.Error("expected small .prev file to be kept")
	}

	stats := sched.Stats()
	if stats.WALCleanCount != 0 {
		t.Errorf("expected WALCleanCount=0, got %d", stats.WALCleanCount)
	}
}

// TestSchedulerTryCleanWALNoPrevFile 测试没有 .prev 文件时 tryCleanWAL 正常返回
func TestSchedulerTryCleanWALNoPrevFile(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{
		WALCleanThreshold: 100,
	})

	// 不创建 .prev 文件，tryCleanWAL 应正常返回
	if err := sched.tryCleanWAL(); err != nil {
		t.Fatalf("tryCleanWAL with no prev file: %v", err)
	}

	stats := sched.Stats()
	if stats.WALCleanCount != 0 {
		t.Errorf("expected WALCleanCount=0, got %d", stats.WALCleanCount)
	}
}

// TestSchedulerTryCompactError 测试 tryCompact 在 Compact 失败时返回错误
func TestSchedulerTryCompactError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{
		{ID: 0, Name: "id", Type: common.TypeInt64},
	}

	// 写入并刷盘足够多的 Segment 以触发 ShouldCompact
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

	// 破坏 Segment 的压缩数据，使 Compact 在解压时失败
	for _, seg := range eng.segments {
		for i := range seg.Columns {
			seg.Columns[i].Data = []byte{0xFF, 0xFE, 0xFD} // 无效的压缩数据
		}
	}

	sched := NewScheduler(eng, SchedulerConfig{})
	err = sched.tryCompact()
	if err == nil {
		t.Error("expected tryCompact to fail, got nil")
	}
}

// TestSchedulerTryCleanWALRemoveError 测试 tryCleanWAL 在 os.Remove 失败时返回错误
func TestSchedulerTryCleanWALRemoveError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{
		WALCleanThreshold: 1, // 极小阈值，任何文件都超过
	})

	// 创建一个非空目录作为 .prev 路径，os.Remove 对非空目录会失败
	prevPath := filepath.Join(dir, "wal.log.prev")
	if err := os.MkdirAll(prevPath, 0755); err != nil {
		t.Fatalf("mkdir prev: %v", err)
	}
	// 在目录中创建文件使其非空，os.Remove 会返回 ENOTEMPTY
	if err := os.WriteFile(filepath.Join(prevPath, "inner"), []byte("x"), 0644); err != nil {
		t.Fatalf("write inner file: %v", err)
	}

	err = sched.tryCleanWAL()
	if err == nil {
		t.Error("expected tryCleanWAL to fail when os.Remove fails, got nil")
	}
}

// TestSchedulerTryCleanWALStatError 测试 tryCleanWAL 在 os.Stat 返回非 NotExist 错误时的处理
func TestSchedulerTryCleanWALStatError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{
		WALCleanThreshold: 1,
	})

	// 创建一个指向自身的符号循环链接，os.Stat 会返回 ELOOP（非 NotExist 错误）
	prevPath := filepath.Join(dir, "wal.log.prev")
	if err := os.Symlink(prevPath, prevPath); err != nil {
		// 某些文件系统不支持符号链接，跳过测试
		t.Skipf("skipping: cannot create symlink: %v", err)
	}

	err = sched.tryCleanWAL()
	if err == nil {
		t.Error("expected tryCleanWAL to fail with stat error, got nil")
	}
}
