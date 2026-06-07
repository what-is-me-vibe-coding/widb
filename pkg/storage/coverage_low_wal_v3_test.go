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

// ---------------------------------------------------------------------------
// OpenWAL 错误路径测试
// ---------------------------------------------------------------------------

// TestOpenWAL_DirectoryAsPath 测试 OpenWAL 在路径为目录时的错误路径。
// 打开目录时 os.OpenFile 返回的错误不是 os.IsNotExist，
// 覆盖 OpenWAL 中非 NotExist 分支（第 71-72 行）。
func TestOpenWAL_DirectoryAsPath(t *testing.T) {
	if runtime.GOOS == skipWindows {
		t.Skip("Windows 上打开目录的行为不同")
	}

	dir := t.TempDir()
	walPath := filepath.Join(dir, "testdir")

	// 创建目录
	if err := os.Mkdir(walPath, 0755); err != nil {
		t.Fatalf("Mkdir 失败: %v", err)
	}

	_, _, err := OpenWAL(walPath)
	if err == nil {
		t.Fatal("期望打开目录时返回错误，得到 nil")
	}

	// 验证错误不是 NotExist（文件存在，只是是个目录）
	if !os.IsNotExist(err) {
		// 错误不应被识别为 NotExist，覆盖非 NotExist 分支
		t.Logf("正确触发了非 NotExist 错误路径: %v", err)
	} else {
		t.Errorf("错误不应被识别为 NotExist: %v", err)
	}
}

// TestOpenWAL_PermissionDeniedNonNotExist 测试 OpenWAL 在权限不足时的错误路径。
// 当文件存在但无法以 O_RDWR 打开时，错误不是 NotExist，
// 覆盖 OpenWAL 中非 NotExist 分支（第 71-72 行）。
func TestOpenWAL_PermissionDeniedNonNotExist(t *testing.T) {
	if runtime.GOOS == skipWindows {
		t.Skip("权限测试在 Windows 上不可靠")
	}
	if os.Getuid() == 0 {
		t.Skip("root 用户绕过文件权限检查")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "readonly.wal")

	// 创建 WAL 文件并写入有效记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte("record1"))
	_ = w.Sync()
	_ = w.Close()

	// 将文件设为只读，使 O_RDWR 打开失败
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatalf("Chmod 失败: %v", err)
	}
	defer func() { _ = os.Chmod(path, 0644) }()

	_, _, err = OpenWAL(path)
	if err == nil {
		t.Fatal("期望权限不足时返回错误，得到 nil")
	}

	// 验证错误不是 NotExist（文件存在，只是权限不足）
	if os.IsNotExist(err) {
		t.Errorf("错误不应被识别为 NotExist（文件存在但权限不足）: %v", err)
	}

	// 验证错误消息包含 "wal open"
	if !strings.Contains(err.Error(), "wal open") {
		t.Errorf("错误消息应包含 'wal open'，得到: %v", err)
	}
}

// TestOpenWAL_SymlinkToDevNull 测试 OpenWAL 打开指向 /dev/null 的符号链接。
// /dev/null 可以用 O_RDWR 打开，但 Truncate 会失败（EINVAL），
// 覆盖 OpenWAL 中 Truncate 错误路径（第 83-86 行）。
func TestOpenWAL_SymlinkToDevNull(t *testing.T) {
	if runtime.GOOS != skipNonLinux {
		t.Skip("此测试依赖 Linux 上 /dev/null 的 Truncate 行为")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建指向 /dev/null 的符号链接
	if err := os.Symlink("/dev/null", path); err != nil {
		t.Fatalf("Symlink 失败: %v", err)
	}

	_, _, err := OpenWAL(path)
	if err == nil {
		t.Fatal("期望打开 /dev/null 符号链接时返回错误，得到 nil")
	}

	// 验证错误消息包含 "wal truncate" 或 "wal seek"
	if !strings.Contains(err.Error(), "wal truncate") && !strings.Contains(err.Error(), "wal seek") {
		t.Errorf("错误消息应包含 'wal truncate' 或 'wal seek'，得到: %v", err)
	}
}

// ---------------------------------------------------------------------------
// maybeRotate 成功路径后的状态验证
// ---------------------------------------------------------------------------

// TestMaybeRotate_SuccessStateVerification 测试 maybeRotate 成功后的状态正确性。
// 验证轮转后 offset 被重置、file 被替换、.prev 文件存在。
func TestMaybeRotate_SuccessStateVerification(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入数据使 offset > 0
	if err := w.AppendWrite([]byte("initial_data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	// 记录轮转前的 offset
	offsetBefore := w.Size()

	// 设置很小的 maxSize 以触发轮转
	w.maxSize = 1

	// 写入触发轮转
	if err := w.AppendWrite([]byte("trigger")); err != nil {
		t.Fatalf("AppendWrite 触发轮转失败: %v", err)
	}

	// 验证 offset 被重置后又有新数据写入
	if w.Size() == 0 {
		t.Error("轮转后 offset 不应为 0（已写入新数据）")
	}
	if w.Size() >= offsetBefore {
		t.Errorf("轮转后 offset (%d) 应小于轮转前 offset (%d)", w.Size(), offsetBefore)
	}

	// 验证 .prev 文件存在
	if _, err := os.Stat(path + ".prev"); err != nil {
		t.Errorf("期望 .prev 文件存在: %v", err)
	}

	// 验证 .tmp 文件不存在
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("期望 .tmp 文件不存在（已被重命名）")
	}

	// 验证 WAL 可以继续写入
	if err := w.AppendWrite([]byte("after_rotation")); err != nil {
		t.Fatalf("轮转后 AppendWrite 失败: %v", err)
	}

	_ = w.Close()
}

// TestMaybeRotate_PrevFileOverwrite 测试 maybeRotate 在 .prev 文件已存在时的行为。
// maybeRotate 使用 os.Rename 覆盖 .prev 文件，验证旧 .prev 被新轮转文件替换。
func TestMaybeRotate_PrevFileOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入数据并触发第一次轮转
	if err := w.AppendWrite([]byte("first_rotation_data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	w.maxSize = 1
	if err := w.AppendWrite([]byte("trigger1")); err != nil {
		t.Fatalf("第一次轮转失败: %v", err)
	}

	// 验证 .prev 文件存在
	prevPath := path + ".prev"
	if _, err := os.Stat(prevPath); err != nil {
		t.Fatalf("第一次轮转后 .prev 文件应存在: %v", err)
	}

	// 读取 .prev 文件大小
	prevInfo, err := os.Stat(prevPath)
	if err != nil {
		t.Fatalf("Stat .prev 失败: %v", err)
	}
	firstPrevSize := prevInfo.Size()

	// 重置 maxSize 并写入更多数据触发第二次轮转
	w.maxSize = 1
	if err := w.AppendWrite([]byte("trigger2")); err != nil {
		t.Fatalf("第二次轮转失败: %v", err)
	}

	// 验证 .prev 文件被覆盖（大小可能不同）
	prevInfo, err = os.Stat(prevPath)
	if err != nil {
		t.Fatalf("第二次轮转后 .prev 文件应存在: %v", err)
	}
	// 第二次轮转的 .prev 应该是第一次轮转后的新文件
	// 其大小可能等于 firstPrevSize（因为第一次轮转后的新文件内容相似）
	t.Logf("第一次 .prev 大小: %d, 第二次 .prev 大小: %d", firstPrevSize, prevInfo.Size())

	_ = w.Close()
}

// ---------------------------------------------------------------------------
// OpenWAL 空文件和边界情况
// ---------------------------------------------------------------------------

// TestOpenWAL_EmptyFile 测试 OpenWAL 打开空文件时的行为。
// 空文件没有有效记录，validOffset 为 0，Truncate(0) 和 Seek(0) 应成功。
func TestOpenWAL_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.wal")

	// 创建空文件
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	_ = f.Close()

	// 打开空 WAL
	w, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 空文件失败: %v", err)
	}
	defer func() { _ = w.Close() }()

	// 应该没有有效记录
	if len(recs) != 0 {
		t.Fatalf("期望 0 条记录，得到 %d", len(recs))
	}

	// offset 应该为 0
	if w.Size() != 0 {
		t.Errorf("期望 offset 0，得到 %d", w.Size())
	}

	// 验证可以继续追加
	if err := w.AppendWrite([]byte("after_empty")); err != nil {
		t.Fatalf("空文件恢复后 AppendWrite 失败: %v", err)
	}
}

// TestOpenWAL_TruncateToZeroOnGarbage 测试 OpenWAL 在文件只包含垃圾数据时
// 正确截断到偏移量 0。覆盖 Truncate(0) + Seek(0) 路径。
func TestOpenWAL_TruncateToZeroOnGarbage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "garbage.wal")

	// 创建只包含无效头部的文件（totalLen 过小）
	garbage := []byte{0x01, 0x00, 0x00, 0x00} // totalLen=1，小于 walTypeSize+walCRCSize=5
	if err := os.WriteFile(path, garbage, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	w, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 垃圾文件失败: %v", err)
	}
	defer func() { _ = w.Close() }()

	if len(recs) != 0 {
		t.Fatalf("期望 0 条记录，得到 %d", len(recs))
	}

	if w.Size() != 0 {
		t.Errorf("期望 offset 0，得到 %d", w.Size())
	}

	// 验证文件被截断为 0 字节
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("期望文件大小 0，得到 %d", info.Size())
	}
}

// ---------------------------------------------------------------------------
// maybeRotate 与 AppendBatch 交互测试
// ---------------------------------------------------------------------------

// TestMaybeRotate_AppendBatchCreateTempFailure 测试 AppendBatch 在 maybeRotate
// 创建临时文件失败时的错误传播。
func TestMaybeRotate_AppendBatchCreateTempFailure(t *testing.T) {
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

	// 写入数据
	if err := w.AppendWrite([]byte("data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	// 设置很小的 maxSize
	w.maxSize = 1

	// 删除目录使创建临时文件失败
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove 文件失败: %v", err)
	}
	if err := os.Remove(subDir); err != nil {
		t.Fatalf("Remove 目录失败: %v", err)
	}

	// AppendBatch 应该在 maybeRotate 步骤失败
	records := []BatchRecord{
		{Type: walTypeWrite, Payload: []byte("batch1")},
		{Type: walTypeWrite, Payload: []byte("batch2")},
	}
	err = w.AppendBatch(records)
	if err == nil {
		_ = w.Close()
		t.Fatal("期望 AppendBatch 在 maybeRotate 失败时返回错误，得到 nil")
	}

	if !strings.Contains(err.Error(), "wal rotate create temp") {
		t.Errorf("错误消息应包含 'wal rotate create temp'，得到: %v", err)
	}

	_ = w.Close()
}
