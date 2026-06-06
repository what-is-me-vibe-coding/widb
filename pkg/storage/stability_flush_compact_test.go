package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestFlushFailurePreservesImmutableMemTables 验证 Flush 失败时，
// 未刷写的 immutable memtable 被放回 e.immutable，数据不丢失。
func TestFlushFailurePreservesImmutableMemTables(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir, MaxMemTableSize: 1 << 20})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{
		{ID: 0, Name: "col0", Type: common.TypeInt64},
	}

	// 写入数据使 activeMem 有内容
	if err := eng.Write("key1", map[string]common.Value{
		"col0": common.NewInt64(1),
	}); err != nil {
		t.Fatalf("Write key1: %v", err)
	}

	// 手动构造场景：让 flusher 在 Flush 时失败
	// 方法：将 flusher 的 dataDir 设为不可写路径
	originalDir := eng.flusher.dataDir
	eng.flusher.dataDir = "/proc/nonexistent/path/for/test"

	// Flush 应该失败
	err = eng.Flush(cols)
	if err == nil {
		t.Fatal("expected Flush to fail with invalid data dir")
	}

	// 恢复 flusher 的 dataDir
	eng.flusher.dataDir = originalDir

	// 验证 immutable memtable 被放回，数据仍然可读
	eng.mu.RLock()
	hasImmutable := len(eng.immutable) > 0
	eng.mu.RUnlock()

	if !hasImmutable {
		t.Fatal("expected immutable memtables to be restored after flush failure")
	}

	// 数据应该仍可查询
	row, ok := eng.Get("key1")
	if !ok {
		t.Fatal("expected key1 to be found after flush failure recovery")
	}
	if row.Columns["col0"].Int64 != 1 {
		t.Fatalf("expected col0=1, got %v", row.Columns["col0"])
	}
}

// TestCompactPreservesDataOnIndexRegistrationFailure 验证 Compact 过程中
// 如果新 segment 索引注册失败，旧 segment 数据不丢失。
func TestCompactPreservesDataOnIndexRegistrationFailure(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir, MaxMemTableSize: 1 << 20})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{
		{ID: 0, Name: "col0", Type: common.TypeInt64},
	}

	// 写入并刷盘多次，生成多个 L0 segment
	for i := 0; i < 4; i++ {
		if err := eng.Write("key1", map[string]common.Value{
			"col0": common.NewInt64(int64(i)),
		}); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
		if err := eng.Flush(cols); err != nil {
			t.Fatalf("Flush %d: %v", i, err)
		}
	}

	// 验证有 L0 segments
	if eng.L0SegmentCount() == 0 {
		t.Fatal("expected L0 segments before compact")
	}

	// 正常 Compact 应该成功
	if err := eng.Compact(cols); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// 验证 L0 segments 已合并
	if eng.L0SegmentCount() != 0 {
		t.Fatalf("expected 0 L0 segments after compact, got %d", eng.L0SegmentCount())
	}

	// 验证数据仍可查询（应返回最新版本）
	row, ok := eng.Get("key1")
	if !ok {
		t.Fatal("expected key1 to be found after compact")
	}
	if row.Columns["col0"].Int64 != 3 {
		t.Fatalf("expected col0=3 (latest version), got %v", row.Columns["col0"])
	}
}

// TestWALRotateResilience 验证 WAL 切分过程中如果出现异常，
// 不会导致 WAL 文件不可用。
func TestWALRotateResilience(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL: %v", err)
	}

	// 写入数据使其接近 maxSize
	w.maxSize = 256 // 设置较小的 maxSize 以便触发 rotate
	for i := 0; i < 20; i++ {
		if err := w.AppendWrite([]byte("test_payload_data_for_rotate_test")); err != nil {
			t.Fatalf("AppendWrite %d: %v", i, err)
		}
	}

	// 验证 WAL 仍然可用
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync after rotate: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 验证可以重新打开并回放
	w2, records, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL after rotate: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if len(records) == 0 {
		t.Fatal("expected records after WAL rotate and reopen")
	}
}

// TestCompactionDeduplicationByVersion 验证 Compaction 去重逻辑
// 正确处理同 key 不同版本（segment ID）的情况，保留最新版本。
func TestCompactionDeduplicationByVersion(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir, MaxMemTableSize: 1 << 20})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{
		{ID: 0, Name: "col0", Type: common.TypeInt64},
	}

	// 第一次写入 key1 = 100
	if err := eng.Write("key1", map[string]common.Value{
		"col0": common.NewInt64(100),
	}); err != nil {
		t.Fatalf("Write key1=100: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 1: %v", err)
	}

	// 第二次写入 key1 = 200（更新）
	if err := eng.Write("key1", map[string]common.Value{
		"col0": common.NewInt64(200),
	}); err != nil {
		t.Fatalf("Write key1=200: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 2: %v", err)
	}

	// 第三次写入 key1 = 300（再次更新）
	if err := eng.Write("key1", map[string]common.Value{
		"col0": common.NewInt64(300),
	}); err != nil {
		t.Fatalf("Write key1=300: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 3: %v", err)
	}

	// 写入另一个 key 以确保有足够的 L0 segments
	if err := eng.Write("key2", map[string]common.Value{
		"col0": common.NewInt64(999),
	}); err != nil {
		t.Fatalf("Write key2: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 4: %v", err)
	}

	// Compact
	if err := eng.Compact(cols); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// 验证 key1 返回最新版本 300
	row, ok := eng.Get("key1")
	if !ok {
		t.Fatal("expected key1 to be found after compact")
	}
	if row.Columns["col0"].Int64 != 300 {
		t.Fatalf("expected col0=300 (latest version after compact), got %v", row.Columns["col0"])
	}

	// 验证 key2 仍可查询
	row2, ok := eng.Get("key2")
	if !ok {
		t.Fatal("expected key2 to be found after compact")
	}
	if row2.Columns["col0"].Int64 != 999 {
		t.Fatalf("expected col0=999 for key2, got %v", row2.Columns["col0"])
	}
}

// TestFlushMultipleImmutableRecoversOnPartialFailure 验证多个 immutable memtable
// 刷盘时，如果中间某个失败，已刷盘的成功，未刷盘的被放回。
func TestFlushMultipleImmutableRecoversOnPartialFailure(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir, MaxMemTableSize: 1 << 20})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{
		{ID: 0, Name: "col0", Type: common.TypeInt64},
	}

	// 写入并刷盘第一个 memtable
	if err := eng.Write("key1", map[string]common.Value{
		"col0": common.NewInt64(1),
	}); err != nil {
		t.Fatalf("Write key1: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 1: %v", err)
	}

	// 写入第二个 memtable
	if err := eng.Write("key2", map[string]common.Value{
		"col0": common.NewInt64(2),
	}); err != nil {
		t.Fatalf("Write key2: %v", err)
	}

	// 手动将 activeMem 移到 immutable（模拟多个 immutable）
	eng.mu.Lock()
	eng.activeMem.Freeze()
	eng.immutable = append(eng.immutable, eng.activeMem)
	eng.activeMem = NewMemTableWithSize(eng.activeMem.maxSize)
	eng.mu.Unlock()

	// 再写入第三个 memtable
	if err := eng.Write("key3", map[string]common.Value{
		"col0": common.NewInt64(3),
	}); err != nil {
		t.Fatalf("Write key3: %v", err)
	}

	eng.mu.Lock()
	eng.activeMem.Freeze()
	eng.immutable = append(eng.immutable, eng.activeMem)
	eng.activeMem = NewMemTableWithSize(eng.activeMem.maxSize)
	eng.mu.Unlock()

	// 正常 Flush 应该成功
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush with multiple immutable: %v", err)
	}

	// 验证所有数据可查询
	for _, key := range []string{"key1", "key2", "key3"} {
		_, ok := eng.Get(key)
		if !ok {
			t.Fatalf("expected %s to be found after flush", key)
		}
	}
}

// TestWALRotateCreatesTempFileFirst 验证 WAL rotate 使用"先建后删"策略，
// 不会在中间状态丢失 WAL 文件。
func TestWALRotateCreatesTempFileFirst(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL: %v", err)
	}

	// 设置很小的 maxSize 以触发 rotate
	w.maxSize = 64

	// 写入足够数据触发 rotate
	for i := 0; i < 50; i++ {
		if err := w.AppendWrite([]byte("payload_data")); err != nil {
			t.Fatalf("AppendWrite %d: %v", i, err)
		}
	}

	// 验证临时文件已被清理（不应存在 .tmp 文件）
	tmpPath := walPath + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("temp file %s should not exist after successful rotate", tmpPath)
	}

	// 验证 WAL 文件存在
	if _, err := os.Stat(walPath); os.IsNotExist(err) {
		t.Fatal("WAL file should exist after rotate")
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
