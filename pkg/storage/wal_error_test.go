package storage

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const skipWindows = "windows"

// TestTruncateSyncError 测试 Truncate 在底层文件已关闭时 Sync 失败的错误路径
func TestTruncateSyncError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// 写入一些数据
	if err := w.AppendWrite([]byte("data")); err != nil {
		t.Fatalf("AppendWrite failed: %v", err)
	}

	// 直接关闭底层文件，使后续 Sync 失败
	if err := w.file.Close(); err != nil {
		t.Fatalf("closing underlying file: %v", err)
	}

	// Truncate 应该在 Sync 步骤失败
	err = w.Truncate()
	if err == nil {
		t.Fatal("expected error when calling Truncate with closed file")
	}
}

// TestTruncateCreateError 测试 Truncate 在目录被删除后 Create 失败的错误路径
// 文件被 unlink 后仍可通过文件描述符进行 Sync/Close，但 os.Create 因目录不存在而失败
func TestTruncateCreateError(t *testing.T) {
	if runtime.GOOS == skipWindows {
		t.Skip("cannot remove open file on Windows")
	}

	dir := t.TempDir()
	subDir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}
	path := filepath.Join(subDir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// 写入一些数据
	if err := w.AppendWrite([]byte("data")); err != nil {
		t.Fatalf("AppendWrite failed: %v", err)
	}

	// 删除文件的目录项和目录本身
	// 文件仍处于打开状态，Sync 和 Close 可以在文件描述符上成功
	// 但 os.Create(w.path) 会因目录不存在而失败
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove file: %v", err)
	}
	if err := os.Remove(subDir); err != nil {
		t.Fatalf("Remove dir: %v", err)
	}

	// Truncate 应该在 Create 步骤失败
	err = w.Truncate()
	if err == nil {
		t.Fatal("expected error when calling Truncate with removed directory")
	}
}

// TestCloseAlreadyClosedError 测试对已关闭的 WAL 调用 Close 返回错误
func TestCloseAlreadyClosedError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// 第一次 Close 应该成功
	if err := w.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}

	// 第二次 Close 应该返回错误（文件描述符已关闭）
	err = w.Close()
	if err == nil {
		t.Fatal("expected error on double close")
	}
}

// TestMaybeRotateCloseError 测试 maybeRotate 在底层文件已关闭时 Close 失败的错误路径
func TestMaybeRotateCloseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// 设置很小的 maxSize 以触发轮转
	w.maxSize = 1

	// 写入数据使 offset 超过 maxSize
	if err := w.AppendWrite([]byte("data")); err != nil {
		t.Fatalf("AppendWrite failed: %v", err)
	}

	// 直接关闭底层文件
	if err := w.file.Close(); err != nil {
		t.Fatalf("closing underlying file: %v", err)
	}

	// 下一次写入触发 maybeRotate，Close 应该失败
	err = w.AppendWrite([]byte("more data"))
	if err == nil {
		t.Fatal("expected error when rotating with closed file")
	}
}

// TestMaybeRotateRenameError 测试 maybeRotate 在重命名目标为目录时 Rename 失败的错误路径
func TestMaybeRotateRenameError(t *testing.T) {
	if runtime.GOOS == skipWindows {
		t.Skip("rename over directory behavior differs on Windows")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// 在 .prev 路径创建目录，使 Rename 失败（不能将文件重命名为已存在的目录）
	if err := os.Mkdir(path+".prev", 0755); err != nil {
		t.Fatalf("Mkdir .prev failed: %v", err)
	}

	// 设置很小的 maxSize
	w.maxSize = 1

	// 写入数据使 offset 超过 maxSize
	if err := w.AppendWrite([]byte("data")); err != nil {
		t.Fatalf("AppendWrite failed: %v", err)
	}

	// 下一次写入触发 maybeRotate，Rename 应该失败
	err = w.AppendWrite([]byte("more data"))
	if err == nil {
		t.Fatal("expected error when rename target is a directory")
	}

	// 轮转失败后 WAL 处于不一致状态（文件已被关闭），忽略 Close 错误
	_ = w.Close()
}

// TestOpenWALWithOnlyGarbageData 测试打开只包含垃圾数据的 WAL 文件
// 验证 validOffset 为 0 时 Truncate(0) 和 Seek(0) 正常工作
func TestOpenWALWithOnlyGarbageData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建一个只包含垃圾数据的文件
	garbage := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x03, 0x04}
	if err := os.WriteFile(path, garbage, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// 打开 WAL，应该成功但没有有效记录
	w, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	// 应该没有有效记录
	if len(recs) != 0 {
		t.Fatalf("expected 0 records, got %d", len(recs))
	}

	// 文件应该被截断为 0 字节
	if w.Size() != 0 {
		t.Errorf("expected offset 0, got %d", w.Size())
	}

	// 验证可以继续追加记录
	if err := w.AppendWrite([]byte("after garbage")); err != nil {
		t.Fatalf("AppendWrite after garbage recovery failed: %v", err)
	}
}
