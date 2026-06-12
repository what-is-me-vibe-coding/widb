package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
