package storage

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWALRecoverOpen_FailurePath 测试 recoverOpen 在文件无法打开时的失败路径。
// 通过设置一个不存在的路径来触发 os.OpenFile 失败。
func TestWALRecoverOpen_FailurePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.Close()

	// 将 WAL 的路径修改为不存在的目录
	w.path = filepath.Join(dir, "nonexistent_dir", "fail.wal")

	// 关闭当前文件句柄
	_ = w.file.Close()

	// recoverOpen 应该失败但不 panic
	w.recoverOpen()
	// 验证 file 被重新赋值（失败时仍为 nil 或旧值）
	// 关键是不 panic
}

// TestWALRecoverOpen_SuccessPath 测试 recoverOpen 成功路径。
func TestWALRecoverOpen_SuccessPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// 关闭文件后，recoverOpen 应该能重新打开
	_ = w.file.Close()
	w.recoverOpen()

	// 验证文件已重新打开（可以写入）
	if err := w.AppendWrite([]byte("after_recover")); err != nil {
		t.Fatalf("AppendWrite after recoverOpen failed: %v", err)
	}
	_ = w.Close()
}

// TestOpenWAL_TruncateAndSeekError 测试 OpenWAL 中 Truncate 和 Seek 的错误路径。
// 通过在只读目录中创建文件来触发这些错误。
func TestOpenWAL_TruncateAndSeekError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping: test requires non-root user")
	}

	dir := t.TempDir()
	subDir := filepath.Join(dir, "rodir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	path := filepath.Join(subDir, "test.wal")

	// 创建并写入有效记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("data"))
	_ = w.Sync()
	_ = w.Close()

	// 追加垃圾数据使 OpenWAL 需要 Truncate
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	garbage := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	modifiedData := make([]byte, len(data)+len(garbage))
	copy(modifiedData, data)
	copy(modifiedData[len(data):], garbage)
	if err := os.WriteFile(path, modifiedData, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// 将目录设为只读，使 Truncate 失败
	if err := os.Chmod(subDir, 0555); err != nil {
		t.Fatalf("chmod dir failed: %v", err)
	}
	defer func() { _ = os.Chmod(subDir, 0755) }()

	_, _, err = OpenWAL(path)
	if err == nil {
		t.Fatal("expected error when Truncate fails due to read-only directory, got nil")
	}
}
