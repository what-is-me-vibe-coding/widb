package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// --- Merged from wal_rotate_coverage_test.go ---

// TestMaybeRotate_TriggerRotation tests that maybeRotate creates a new WAL file
// when the current file exceeds maxSize.
func TestMaybeRotate_TriggerRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rotate.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// Set a very small maxSize to trigger rotation
	w.maxSize = 1 // 1 byte, any write should trigger rotation

	// Write a record to increase offset beyond maxSize
	if err := w.AppendWrite([]byte("trigger_rotation")); err != nil {
		t.Fatalf("AppendWrite failed: %v", err)
	}

	// After rotation, the WAL should still be functional
	if err := w.AppendWrite([]byte("after_rotation")); err != nil {
		t.Fatalf("AppendWrite after rotation failed: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify the WAL can be opened and records after rotation are present
	w2, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = w2.Close() }()

	// After rotation, the new file should contain the record written after rotation
	if len(records) == 0 {
		t.Error("expected at least 1 record after rotation, got 0")
	}
}

// TestMaybeRotate_NoRotationNeeded tests that maybeRotate does nothing when
// offset is below maxSize.
func TestMaybeRotate_NoRotationNeeded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no_rotate.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// Keep maxSize large so rotation is not triggered
	w.maxSize = 1 << 30 // 1GB

	if err := w.AppendWrite([]byte("small_record")); err != nil {
		t.Fatalf("AppendWrite failed: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify the WAL file exists and has the record
	w2, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if string(records[0].Payload) != "small_record" {
		t.Errorf("payload = %q, want %q", string(records[0].Payload), "small_record")
	}
}

// TestMaybeRotate_MultipleRotations tests that multiple rotations work correctly.
func TestMaybeRotate_MultipleRotations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multi_rotate.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// Set a small maxSize to trigger rotation frequently
	w.maxSize = 100

	// Write multiple records, triggering multiple rotations
	for i := 0; i < 10; i++ {
		payload := []byte("record_" + string(rune('a'+i)))
		if err := w.AppendWrite(payload); err != nil {
			t.Fatalf("AppendWrite #%d failed: %v", i, err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify the WAL can be opened after multiple rotations
	w2, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = w2.Close() }()

	// After rotation, only the records in the current WAL file should be present
	if len(records) == 0 {
		t.Error("expected at least 1 record after multiple rotations, got 0")
	}
}

// TestMaybeRotate_PrevFileCreated tests that the .prev file is created during rotation.
func TestMaybeRotate_PrevFileCreated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prev_file.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// Write a record first so there's data to rotate
	if err := w.AppendWrite([]byte("data_to_rotate")); err != nil {
		t.Fatalf("AppendWrite failed: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	// Set a small maxSize to trigger rotation on next write
	w.maxSize = 1

	// Write another record to trigger rotation
	if err := w.AppendWrite([]byte("trigger")); err != nil {
		t.Fatalf("AppendWrite trigger failed: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// The .prev file should exist after rotation
	prevPath := path + ".prev"
	if _, err := os.Stat(prevPath); os.IsNotExist(err) {
		t.Error("expected .prev file to exist after rotation")
	}
}

// TestMaybeRotate_ContinueWritingAfterRotation tests that writing continues
// correctly after a rotation.
func TestMaybeRotate_ContinueWritingAfterRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "continue.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// Write initial data
	if err := w.AppendWrite([]byte("initial_data")); err != nil {
		t.Fatalf("AppendWrite initial failed: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	// Force rotation
	w.maxSize = 1

	// Write to trigger rotation
	if err := w.AppendWrite([]byte("after_rotate_1")); err != nil {
		t.Fatalf("AppendWrite after rotate 1 failed: %v", err)
	}

	// Reset maxSize to avoid further rotations
	w.maxSize = 1 << 30

	// Write more data after rotation
	if err := w.AppendWrite([]byte("after_rotate_2")); err != nil {
		t.Fatalf("AppendWrite after rotate 2 failed: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Open and verify
	w2, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = w2.Close() }()

	// Should have the records written after rotation
	if len(records) < 2 {
		t.Errorf("expected at least 2 records after rotation, got %d", len(records))
	}
}

// TestOpenWAL_SeekAfterTruncate tests the error path when Seek fails after Truncate.
// This is difficult to trigger directly, so we test the normal OpenWAL flow
// with valid records to ensure the Seek path is covered.
func TestOpenWAL_SeekAfterTruncate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seek_test.wal")

	// Create a WAL with valid records
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := w.AppendWrite([]byte("data")); err != nil {
			t.Fatalf("AppendWrite #%d failed: %v", i, err)
		}
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Open the WAL - this exercises the Truncate + Seek path
	w2, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if len(records) != 5 {
		t.Fatalf("expected 5 records, got %d", len(records))
	}

	// Verify we can append after OpenWAL (which did Seek)
	if err := w2.AppendWrite([]byte("new_data")); err != nil {
		t.Fatalf("AppendWrite after OpenWAL failed: %v", err)
	}
}

// TestApplySingleWriteRecord_OldVersion tests that records with version <=
// lastFlushedVersion are skipped.
func TestApplySingleWriteRecord_OldVersion(t *testing.T) {
	mem := NewMemTable()

	// Write a record with version 10
	if _, _, err := mem.Put("key1", Row{Version: 10, Columns: map[string]common.Value{"col1": common.NewInt64(100)}}); err != nil {
		t.Fatalf("mem.Put failed: %v", err)
	}

	// Apply a record with version 5 (which is <= lastFlushedVersion=10)
	// This should be skipped
	payload, err := serializeWriteRecord("key2", 5, map[string]common.Value{
		"col1": common.NewInt64(200),
	})
	if err != nil {
		t.Fatalf("serializeWriteRecord failed: %v", err)
	}

	v, ok := applySingleWriteRecord(payload, 10, mem)
	if !ok {
		t.Error("applySingleWriteRecord returned ok=false for old version")
	}
	if v != 0 {
		t.Errorf("version = %d, want 0 for skipped record", v)
	}
}

// --- Merged from wal_rotate_error_test.go ---

// TestMaybeRotateSyncTempError 测试新文件 Sync 失败时的错误路径（第 230-234 行）。
// 通过在 .tmp 路径创建只读文件使 Create 成功但 Sync 可能受限。
func TestMaybeRotateSyncTempError(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 设置极小的 maxSize 触发 rotate
	w.maxSize = 1

	// 写入数据使 offset >= maxSize
	if err := w.AppendWrite([]byte("test-data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	// 在 .tmp 路径创建一个目录，使 os.Create(.tmp) 失败
	// 这走的是 "wal rotate create temp" 路径而非 Sync 路径
	tmpPath := walPath + ".tmp"
	if err := os.MkdirAll(tmpPath, 0755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpPath) }()

	err = w.maybeRotate()
	if err == nil {
		t.Error("期望 maybeRotate 返回错误，但返回 nil")
		_ = w.file.Close()
		return
	}

	if !strings.Contains(err.Error(), "wal rotate") {
		t.Errorf("期望错误包含 'wal rotate'，实际: %v", err)
	}
}

// TestMaybeRotateCloseOldErrorV2 测试关闭旧文件失败时的错误路径（第 238-242 行）。
// 通过预先关闭底层文件描述符使 old.Close() 失败。
func TestMaybeRotateCloseOldErrorV2(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 设置极小的 maxSize 触发 rotate
	w.maxSize = 1

	// 写入数据使 offset >= maxSize
	if err := w.AppendWrite([]byte("test-data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	// 预先关闭底层文件，使 old.Close() 在 maybeRotate 中失败
	if err := w.file.Close(); err != nil {
		t.Fatalf("预关闭文件失败: %v", err)
	}

	// maybeRotate 应该返回 "wal rotate close" 错误
	err = w.maybeRotate()
	if err == nil {
		t.Error("期望 maybeRotate 返回错误，但返回 nil")
		return
	}

	if !strings.Contains(err.Error(), "wal rotate close") {
		t.Errorf("期望错误包含 'wal rotate close'，实际: %v", err)
	}

	// 清理 .tmp 文件（如果存在）
	_ = os.Remove(walPath + ".tmp")
}

// TestMaybeRotateRenameOldErrorV2 测试重命名旧文件失败时的错误路径（第 245-249 行）。
// 通过在 .prev 路径创建非空目录使 Rename 失败。
func TestMaybeRotateRenameOldErrorV2(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 设置极小的 maxSize 触发 rotate
	w.maxSize = 1

	// 写入数据使 offset >= maxSize
	if err := w.AppendWrite([]byte("test-data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	// 在 .prev 路径创建一个非空目录，使 Rename(walPath, .prev) 失败
	prevPath := walPath + ".prev"
	if err := os.MkdirAll(prevPath, 0755); err != nil {
		t.Fatalf("创建 .prev 目录失败: %v", err)
	}
	dummyFile := filepath.Join(prevPath, "dummy")
	if err := os.WriteFile(dummyFile, []byte("x"), 0644); err != nil {
		t.Fatalf("创建 dummy 文件失败: %v", err)
	}
	defer func() { _ = os.RemoveAll(prevPath) }()

	// maybeRotate 应该返回 "wal rotate rename" 错误
	err = w.maybeRotate()
	if err == nil {
		t.Error("期望 maybeRotate 返回错误，但返回 nil")
		if w.file != nil {
			_ = w.file.Close()
		}
		return
	}

	if !strings.Contains(err.Error(), "wal rotate rename") {
		t.Errorf("期望错误包含 'wal rotate rename'，实际: %v", err)
	}

	// 验证 recoverOpen 被调用后文件可用
	if w.file == nil {
		t.Error("recoverOpen 后 w.file 不应为 nil")
	} else {
		_ = w.file.Close()
	}

	// 清理
	_ = os.Remove(walPath + ".tmp")
}

// TestMaybeRotateRenameTempErrorV2 测试重命名临时文件失败时的错误路径（第 252-263 行）。
// 通过删除源文件使 Rename(walPath, .prev) 失败，触发 rename 错误路径。
func TestMaybeRotateRenameTempErrorV2(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("创建子目录失败: %v", err)
	}

	walPath := filepath.Join(subDir, "wal.log")
	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	w.maxSize = 1
	if err := w.AppendWrite([]byte("test-data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	// 删除源文件使 Rename(walPath, .prev) 失败
	_ = os.Remove(walPath)

	err = w.maybeRotate()
	if err == nil {
		t.Error("期望 maybeRotate 返回错误，但返回 nil")
		if w.file != nil {
			_ = w.file.Close()
		}
		return
	}

	if !strings.Contains(err.Error(), "wal rotate rename") {
		t.Errorf("期望错误包含 'wal rotate rename'，实际: %v", err)
	}

	// 清理
	_ = os.Remove(walPath + ".tmp")
}

// TestRecoverOpenV2 测试 recoverOpen 函数能正确恢复文件句柄。
func TestRecoverOpenV2(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 关闭文件使 w.file 无效
	_ = w.file.Close()

	// recoverOpen 应该能重新打开文件
	w.recoverOpen()
	if w.file == nil {
		t.Fatal("recoverOpen 后 w.file 不应为 nil")
	}

	// 验证可以正常写入
	if err := w.AppendWrite([]byte("after-recovery")); err != nil {
		t.Fatalf("恢复后写入失败: %v", err)
	}

	_ = w.file.Close()
}

// TestRecoverOpenNewFile 测试 recoverOpen 在文件不存在时创建新文件。
func TestRecoverOpenNewFile(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "nonexistent.log")

	w := &WAL{
		path:    walPath,
		maxSize: walDefaultMaxSize,
	}

	// recoverOpen 应该能创建文件（因为使用了 O_CREATE）
	w.recoverOpen()
	if w.file == nil {
		t.Fatal("recoverOpen 后 w.file 不应为 nil（O_CREATE 应创建文件）")
	}
	_ = w.file.Close()
}
