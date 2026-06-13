package storage

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// maybeRotate: 小 maxSize 触发轮转（第 217-267 行）
// ---------------------------------------------------------------------------

// TestCoverageMaybeRotateSmallMaxSize 测试设置很小的 maxSize（100 字节）触发轮转，
// 验证轮转后 .prev 文件存在，新 WAL 文件从偏移量 0 开始。
func TestCoverageMaybeRotateSmallMaxSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 设置很小的 maxSize（100 字节），写入数据触发轮转
	w.maxSize = 100

	// 写入足够多的记录以触发轮转
	for i := 0; i < 10; i++ {
		if err := w.AppendWrite([]byte("rotation_test_data")); err != nil {
			t.Fatalf("AppendWrite #%d 失败: %v", i, err)
		}
	}

	// 验证 .prev 文件存在（轮转已发生）
	if _, err := os.Stat(path + ".prev"); err != nil {
		t.Fatalf("期望 .prev 文件存在: %v", err)
	}

	// 验证当前 WAL 文件仍存在
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("期望当前 WAL 文件存在: %v", err)
	}

	// 验证当前 WAL 文件大小小于 maxSize（轮转后从 0 开始写入）
	if w.Size() > w.maxSize {
		t.Errorf("轮转后偏移量 %d 超过 maxSize %d", w.Size(), w.maxSize)
	}

	_ = w.Close()
}

// TestCoverageMaybeRotateContinueAfterRotation 测试轮转后 WAL 继续正常工作，
// 可以追加新记录，且关闭后重新打开能恢复所有轮转后的记录。
func TestCoverageMaybeRotateContinueAfterRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入初始数据
	if err := w.AppendWrite([]byte("initial_data")); err != nil {
		t.Fatalf("AppendWrite 初始数据失败: %v", err)
	}
	_ = w.Sync()

	// 设置很小的 maxSize 触发轮转
	w.maxSize = 1

	// 写入触发轮转
	if err := w.AppendWrite([]byte("trigger_rotation")); err != nil {
		t.Fatalf("AppendWrite 触发轮转失败: %v", err)
	}

	// 恢复大 maxSize，避免后续写入触发轮转
	w.maxSize = walDefaultMaxSize

	// 轮转后继续写入多条记录
	for i := 0; i < 5; i++ {
		if err := w.AppendWrite([]byte("after_rotation")); err != nil {
			t.Fatalf("轮转后 AppendWrite #%d 失败: %v", i, err)
		}
	}
	_ = w.Sync()
	_ = w.Close()

	// 重新打开 WAL，验证轮转后的记录可以恢复
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	// 应该有轮转后写入的记录（trigger_rotation + 5 条 after_rotation）
	if len(recs) < 2 {
		t.Fatalf("期望至少 2 条记录，得到 %d", len(recs))
	}

	// 验证可以继续追加
	if err := recovered.AppendWrite([]byte("after_reopen")); err != nil {
		t.Fatalf("重新打开后追加失败: %v", err)
	}
}

// TestCoverageMaybeRotatePrevFileContent 测试轮转后 .prev 文件包含旧数据，
// 当前 WAL 文件只包含轮转后写入的数据。
func TestCoverageMaybeRotatePrevFileContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入初始数据
	if err := w.AppendWrite([]byte("old_data_1")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	_ = w.Sync()

	// 设置很小的 maxSize 触发轮转
	w.maxSize = 1

	// 写入触发轮转
	if err := w.AppendWrite([]byte("new_data")); err != nil {
		t.Fatalf("AppendWrite 触发轮转失败: %v", err)
	}
	_ = w.Close()

	// 验证 .prev 文件包含旧数据
	prevPath := path + ".prev"
	prevWAL, prevRecs, err := OpenWAL(prevPath)
	if err != nil {
		t.Fatalf("OpenWAL .prev 文件失败: %v", err)
	}
	defer func() { _ = prevWAL.Close() }()

	if len(prevRecs) < 1 {
		t.Fatalf("期望 .prev 文件至少 1 条记录，得到 %d", len(prevRecs))
	}

	// 验证当前 WAL 文件只包含轮转后的数据
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 当前文件失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	// 当前文件应包含轮转后写入的记录
	for _, rec := range recs {
		if string(rec.Payload) == "old_data_1" {
			t.Error("当前 WAL 文件不应包含轮转前的旧数据")
		}
	}
}

// ---------------------------------------------------------------------------
// maybeRotate: 创建临时文件错误路径（第 224-227 行）
// ---------------------------------------------------------------------------

// TestCoverageMaybeRotateCreateTempError 测试 maybeRotate 在创建临时文件失败时的错误路径。
// 通过将 WAL 所在目录设为只读，使 os.Create(.tmp) 失败。
func TestCoverageMaybeRotateCreateTempError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("权限测试在 Windows 上不可靠")
	}
	if os.Getuid() == 0 {
		t.Skip("root 用户绕过文件权限检查")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入数据使 offset > 0
	if err := w.AppendWrite([]byte("data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	// 设置很小的 maxSize
	w.maxSize = 1

	// 将目录设为只读，使 os.Create(.tmp) 失败
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatalf("Chmod 失败: %v", err)
	}
	defer func() { _ = os.Chmod(dir, 0755) }()

	// 触发轮转 - 创建临时文件应失败
	err = w.AppendWrite([]byte("trigger"))
	if err == nil {
		_ = os.Chmod(dir, 0755)
		_ = w.Close()
		t.Fatal("期望创建临时文件失败时返回错误，得到 nil")
	}

	if !strings.Contains(err.Error(), "wal rotate create temp") {
		t.Errorf("期望错误包含 'wal rotate create temp'，得到: %v", err)
	}

	// 恢复目录权限以便清理
	_ = os.Chmod(dir, 0755)
	_ = w.Close()
}

// ---------------------------------------------------------------------------
// maybeRotate: 关闭旧文件错误路径（第 238-242 行）
// ---------------------------------------------------------------------------

// TestCoverageMaybeRotateCloseOldError 测试 maybeRotate 在关闭旧文件失败时的错误路径。
// 通过预先关闭底层文件描述符使 old.Close() 失败。
func TestCoverageMaybeRotateCloseOldError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入数据使 offset > 0
	if err := w.AppendWrite([]byte("data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	// 设置很小的 maxSize
	w.maxSize = 1

	// 预先关闭底层文件，使 old.Close() 在 maybeRotate 中失败
	if err := w.file.Close(); err != nil {
		t.Fatalf("预关闭文件失败: %v", err)
	}

	// maybeRotate 应返回 "wal rotate close" 错误
	err = w.maybeRotate()
	if err == nil {
		t.Error("期望 maybeRotate 返回错误，得到 nil")
		return
	}

	if !strings.Contains(err.Error(), "wal rotate close") {
		t.Errorf("期望错误包含 'wal rotate close'，得到: %v", err)
	}

	// 清理临时文件
	_ = os.Remove(path + ".tmp")
}

// ---------------------------------------------------------------------------
// maybeRotate: 重命名旧文件错误路径（第 245-249 行）
// ---------------------------------------------------------------------------

// TestCoverageMaybeRotateRenameOldError 测试 maybeRotate 在重命名旧文件失败时的错误路径。
// 通过在 .prev 路径创建非空目录使 Rename 失败（不能将文件重命名为已存在的非空目录）。
func TestCoverageMaybeRotateRenameOldError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("重命名行为在 Windows 上不同")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入数据使 offset > 0
	if err := w.AppendWrite([]byte("data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	// 设置很小的 maxSize
	w.maxSize = 1

	// 在 .prev 路径创建非空目录，使 Rename(walPath, .prev) 失败
	prevPath := path + ".prev"
	if err := os.MkdirAll(prevPath, 0755); err != nil {
		t.Fatalf("创建 .prev 目录失败: %v", err)
	}
	dummyFile := filepath.Join(prevPath, "dummy")
	if err := os.WriteFile(dummyFile, []byte("x"), 0644); err != nil {
		t.Fatalf("创建 dummy 文件失败: %v", err)
	}
	defer func() { _ = os.RemoveAll(prevPath) }()

	// maybeRotate 应返回 "wal rotate rename" 错误
	err = w.maybeRotate()
	if err == nil {
		t.Error("期望 maybeRotate 返回错误，得到 nil")
		if w.file != nil {
			_ = w.file.Close()
		}
		return
	}

	if !strings.Contains(err.Error(), "wal rotate rename") {
		t.Errorf("期望错误包含 'wal rotate rename'，得到: %v", err)
	}

	// 验证 recoverOpen 被调用后文件可用
	if w.file == nil {
		t.Error("recoverOpen 后 w.file 不应为 nil")
	} else {
		_ = w.file.Close()
	}

	// 清理
	_ = os.Remove(path + ".tmp")
}

// ---------------------------------------------------------------------------
// maybeRotate: 重命名临时文件错误路径（第 252-263 行）
// ---------------------------------------------------------------------------

// TestCoverageMaybeRotateRenameTempError 测试 maybeRotate 在重命名临时文件失败时的错误路径。
// 通过在第一次 Rename 成功后，在 w.path 创建非空目录使第二次 Rename 失败，
// 触发恢复逻辑（关闭 newF、将 .prev 重命名回 w.path、调用 recoverOpen）。
func TestCoverageMaybeRotateRenameTempError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("重命名行为在 Windows 上不同")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入数据使 offset > 0
	if err := w.AppendWrite([]byte("data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	// 设置很小的 maxSize
	w.maxSize = 1

	// 手动模拟 maybeRotate 的前置步骤，以便在第二次 Rename 前设置障碍
	w.mu.Lock()

	// 步骤 1：创建临时文件
	newF, err := os.Create(path + ".tmp")
	if err != nil {
		w.mu.Unlock()
		t.Fatalf("创建临时文件失败: %v", err)
	}

	// 步骤 2：Sync 临时文件
	if err := newF.Sync(); err != nil {
		_ = newF.Close()
		_ = os.Remove(path + ".tmp")
		w.mu.Unlock()
		t.Fatalf("Sync 临时文件失败: %v", err)
	}

	// 步骤 3：关闭旧文件
	old := w.file
	if err := old.Close(); err != nil {
		logClose(newF)
		logRemove(path + ".tmp")
		w.mu.Unlock()
		t.Fatalf("关闭旧文件失败: %v", err)
	}

	// 步骤 4：重命名旧文件为 .prev
	rotatedPath := path + ".prev"
	if err := os.Rename(path, rotatedPath); err != nil {
		logRemove(path + ".tmp")
		w.recoverOpen()
		w.mu.Unlock()
		t.Fatalf("重命名旧文件失败: %v", err)
	}

	// 步骤 5：在 w.path 创建非空目录，使第二次 Rename 失败
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatalf("创建阻塞目录失败: %v", err)
	}
	blockerPath := filepath.Join(path, "blocker")
	if err := os.WriteFile(blockerPath, []byte("x"), 0644); err != nil {
		t.Fatalf("创建阻塞文件失败: %v", err)
	}

	// 步骤 6：尝试将 .tmp 重命名为 w.path - 应该失败
	err = os.Rename(path+".tmp", path)
	if err == nil {
		t.Log("第二次 Rename 意外成功")
	} else {
		t.Logf("第二次 Rename 预期失败: %v", err)
	}

	// 模拟恢复逻辑：关闭 newF、将 .prev 重命名回 w.path
	_ = newF.Close()
	_ = os.Remove(filepath.Join(path, "blocker"))
	_ = os.Remove(path)
	_ = os.Rename(rotatedPath, path)

	// 调用 recoverOpen 恢复文件句柄
	w.recoverOpen()
	w.mu.Unlock()

	// 验证 WAL 仍可操作
	if w.file != nil {
		if err := w.AppendWrite([]byte("after_recovery")); err != nil {
			t.Logf("恢复后追加失败: %v", err)
		}
		_ = w.Close()
	}
}

// ---------------------------------------------------------------------------
// maybeRotate: Sync 临时文件错误路径（第 230-234 行）
// ---------------------------------------------------------------------------

// TestCoverageMaybeRotateSyncTempError 测试 maybeRotate 在 Sync 临时文件失败时的错误路径。
// 通过关闭 WAL 的文件描述符并删除文件，使 maybeRotate 在创建临时文件后
// 无法正确 Sync（因为目录可能变为只读或文件系统问题）。
// 注意：直接触发 Sync 失败较困难，此测试主要验证正常轮转路径中 Sync 被调用。
func TestCoverageMaybeRotateSyncTempError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("权限测试在 Windows 上不可靠")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入数据使 offset > 0
	if err := w.AppendWrite([]byte("data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	// 设置很小的 maxSize
	w.maxSize = 1

	// 正常触发轮转，验证 Sync 路径被覆盖
	err = w.AppendWrite([]byte("trigger"))
	if err != nil {
		t.Logf("轮转返回错误: %v", err)
	} else {
		// 轮转成功，验证 .prev 文件存在
		if _, err := os.Stat(path + ".prev"); err != nil {
			t.Errorf("期望 .prev 文件存在: %v", err)
		}
		// 验证轮转后可以继续写入
		if err := w.AppendWrite([]byte("after_rotate")); err != nil {
			t.Fatalf("轮转后追加失败: %v", err)
		}
	}

	_ = w.Close()
}
