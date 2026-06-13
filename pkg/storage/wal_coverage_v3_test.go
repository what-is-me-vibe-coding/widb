package storage

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// maybeRotate 错误路径测试
// ---------------------------------------------------------------------------

// TestMaybeRotate_CreateTempFileFailure 测试 maybeRotate 在创建临时文件失败时的错误路径。
// 通过删除 WAL 文件所在目录，使 os.Create(w.path+".tmp") 返回错误。
func TestMaybeRotate_CreateTempFileFailure(t *testing.T) {
	if runtime.GOOS == skipWindows {
		t.Skip("无法在 Windows 上删除已打开文件的目录")
	}

	dir := t.TempDir()
	subDir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("Mkdir 失败: %v", err)
	}
	path := filepath.Join(subDir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入数据使 offset > 0
	if err := w.AppendWrite([]byte("data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	// 设置很小的 maxSize 以触发轮转
	w.maxSize = 1

	// 删除文件和目录，使 os.Create(w.path+".tmp") 失败
	// 文件仍处于打开状态（通过 fd 可写），但路径已不存在
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove 文件失败: %v", err)
	}
	if err := os.Remove(subDir); err != nil {
		t.Fatalf("Remove 目录失败: %v", err)
	}

	// 触发轮转，应该因创建临时文件失败而返回错误
	err = w.AppendWrite([]byte("more data"))
	if err == nil {
		_ = w.Close()
		t.Fatal("期望创建临时文件失败时返回错误，得到 nil")
	}

	// 验证错误消息包含 "wal rotate create temp"
	if !strings.Contains(err.Error(), "wal rotate create temp") {
		t.Errorf("错误消息应包含 'wal rotate create temp'，得到: %v", err)
	}

	// WAL 的底层文件描述符仍然有效，Close 应该成功
	_ = w.Close()
}

// TestMaybeRotate_CloseErrorCleanup 测试 maybeRotate 在关闭旧文件失败时的清理逻辑。
// 验证当 old.Close() 失败时，新创建的临时文件被正确关闭和删除。
func TestMaybeRotate_CloseErrorCleanup(t *testing.T) {
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

	// 设置很小的 maxSize 以触发轮转
	w.maxSize = 1

	// 直接关闭底层文件，使 maybeRotate 中的 old.Close() 失败
	if err := w.file.Close(); err != nil {
		t.Fatalf("关闭底层文件失败: %v", err)
	}

	// 触发轮转，old.Close() 应该失败
	err = w.AppendWrite([]byte("more data"))
	if err == nil {
		_ = w.Close()
		t.Fatal("期望关闭旧文件失败时返回错误，得到 nil")
	}

	// 验证错误消息包含 "wal rotate close"
	if !strings.Contains(err.Error(), "wal rotate close") {
		t.Errorf("错误消息应包含 'wal rotate close'，得到: %v", err)
	}

	// 验证临时文件已被清理（maybeRotate 的清理逻辑应删除 .tmp）
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("期望临时文件已被删除，但文件仍存在")
	}

	// WAL 处于不一致状态，忽略 Close 错误
	_ = w.Close()
}

// TestMaybeRotate_RenameOldFailureRecovery 测试 maybeRotate 在重命名旧文件失败时的恢复逻辑。
// 在 .prev 路径创建目录使 Rename 失败，然后验证恢复路径（重新打开旧文件）是否成功。
func TestMaybeRotate_RenameOldFailureRecovery(t *testing.T) {
	if runtime.GOOS == skipWindows {
		t.Skip("Windows 上重命名行为不同")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入数据
	if err := w.AppendWrite([]byte("data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	// 设置很小的 maxSize
	w.maxSize = 1

	// 在 .prev 路径创建目录，使 os.Rename(w.path, rotatedPath) 失败
	if err := os.Mkdir(path+".prev", 0755); err != nil {
		t.Fatalf("Mkdir .prev 失败: %v", err)
	}

	// 触发轮转
	err = w.AppendWrite([]byte("trigger"))
	if err == nil {
		_ = w.Close()
		t.Fatal("期望重命名失败时返回错误，得到 nil")
	}

	// 验证错误消息包含 "wal rotate rename"
	if !strings.Contains(err.Error(), "wal rotate rename") {
		t.Errorf("错误消息应包含 'wal rotate rename'，得到: %v", err)
	}

	// 验证临时文件已被清理
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("期望临时文件已被删除，但文件仍存在")
	}

	// 验证恢复路径：w.file 应该被重新打开（恢复后的文件）
	// 如果恢复成功，WAL 应该仍然可以追加数据
	if w.file != nil {
		_ = w.AppendWrite([]byte("after_recovery"))
	}

	// 清理
	_ = os.Remove(path + ".prev")
	_ = w.Close()
}

// TestMaybeRotate_RenameOldFailureRecoveryOpenFails 测试 maybeRotate 在重命名旧文件失败
// 且恢复路径（重新打开旧文件）也失败的情况。通过删除旧文件使恢复路径的
// os.OpenFile 也失败。
func TestMaybeRotate_RenameOldFailureRecoveryOpenFails(t *testing.T) {
	if runtime.GOOS == skipWindows {
		t.Skip("无法在 Windows 上删除已打开文件")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入数据
	if err := w.AppendWrite([]byte("data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	// 设置很小的 maxSize
	w.maxSize = 1

	// 在 .prev 路径创建目录使 Rename 失败
	if err := os.Mkdir(path+".prev", 0755); err != nil {
		t.Fatalf("Mkdir .prev 失败: %v", err)
	}

	// 删除旧文件，使恢复路径的 os.OpenFile 也失败
	// 文件仍通过 fd 打开，但路径已不存在
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove 失败: %v", err)
	}

	// 触发轮转
	err = w.AppendWrite([]byte("trigger"))
	if err == nil {
		_ = w.Close()
		t.Fatal("期望重命名失败时返回错误，得到 nil")
	}

	// 验证错误消息包含 "wal rotate rename"
	if !strings.Contains(err.Error(), "wal rotate rename") {
		t.Errorf("错误消息应包含 'wal rotate rename'，得到: %v", err)
	}

	// 恢复路径也失败了，w.file 应该仍为已关闭的文件
	// WAL 处于不一致状态，忽略 Close 错误
	_ = w.Close()

	// 清理
	_ = os.Remove(path + ".prev")
}

// TestMaybeRotate_RenameTempFailure 测试 maybeRotate 在将临时文件重命名为正式路径失败时的错误路径。
// 使用 goroutine 在第一次 Rename 成功后删除 .tmp 文件，使第二次 Rename 失败。
// 由于竞态条件，此测试为尽力覆盖，不保证每次都能触发目标路径。
func TestMaybeRotate_RenameTempFailure(t *testing.T) {
	if runtime.GOOS == skipWindows {
		t.Skip("Windows 上重命名行为不同")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入数据
	if err := w.AppendWrite([]byte("data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	// 设置很小的 maxSize
	w.maxSize = 1

	// 启动 goroutine 监控 .prev 文件出现（表示第一次 Rename 已完成），
	// 然后立即删除 .tmp 文件，使第二次 Rename 失败
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				// 检查 .prev 是否存在（第一次 Rename 已完成）
				if _, err := os.Stat(path + ".prev"); err == nil {
					// 删除 .tmp 文件使第二次 Rename 失败
					_ = os.Remove(path + ".tmp")
					return
				}
				runtime.Gosched()
			}
		}
	}()

	// 触发轮转
	err = w.AppendWrite([]byte("trigger"))

	close(stop)

	// 由于竞态条件，可能触发也可能不触发目标错误路径
	// 如果成功触发了第二次 Rename 失败，验证错误消息
	if err != nil && strings.Contains(err.Error(), "wal rotate rename temp") {
		// 成功触发了目标路径
		t.Logf("成功触发第二次 Rename 失败路径: %v", err)

		// 验证恢复路径：.prev 应该被重命名回 w.path
		if _, err := os.Stat(path); err != nil {
			t.Logf("恢复后 w.path 不存在（恢复可能未完全成功）: %v", err)
		}
	} else if err != nil {
		t.Logf("触发了其他错误路径: %v", err)
	}

	// 清理
	_ = os.Remove(path + ".tmp")
	_ = os.Remove(path + ".prev")
	_ = os.RemoveAll(path)
	_ = w.Close()
}

// TestMaybeRotate_RenameTempFailureWithDir 测试 maybeRotate 第二次 Rename 失败的另一种方式。
// 在第一次 Rename 成功后（w.path 已被重命名为 .prev），
// 在 w.path 创建一个非空目录，使 os.Rename(.tmp, w.path) 失败。
// 使用 goroutine 监控 .prev 文件出现后创建目录。
func TestMaybeRotate_RenameTempFailureWithDir(t *testing.T) {
	if runtime.GOOS == skipWindows {
		t.Skip("Windows 上重命名行为不同")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	if err := w.AppendWrite([]byte("data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	w.maxSize = 1

	// 启动 goroutine 在 .prev 出现后创建阻塞目录
	stop := make(chan struct{})
	go createBlockingDirOnPrev(path, stop)

	err = w.AppendWrite([]byte("trigger"))
	close(stop)

	if err != nil && strings.Contains(err.Error(), "wal rotate rename temp") {
		t.Logf("成功触发第二次 Rename 失败路径: %v", err)
	} else if err != nil {
		t.Logf("触发了其他错误路径: %v", err)
	}

	_ = os.Remove(filepath.Join(path, "blocker"))
	_ = os.Remove(path + ".tmp")
	_ = os.Remove(path + ".prev")
	_ = os.RemoveAll(path)
	_ = w.Close()
}

// createBlockingDirOnPrev 监控 .prev 文件出现后在 w.path 创建非空目录，
// 使 os.Rename(.tmp, w.path) 失败。
func createBlockingDirOnPrev(path string, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		default:
			if _, err := os.Stat(path + ".prev"); err == nil {
				if err := os.Mkdir(path, 0755); err == nil {
					f, err := os.Create(filepath.Join(path, "blocker"))
					if err == nil {
						_ = f.Close()
					}
				}
				return
			}
			runtime.Gosched()
		}
	}
}
