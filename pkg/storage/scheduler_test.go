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
