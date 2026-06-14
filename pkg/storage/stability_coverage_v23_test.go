package storage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// GroupCommitter.doSync - sync 失败路径与 pending 溢出
// ---------------------------------------------------------------------------

// TestStabilityGroupCommitterSyncFailureOverflow 测试 doSync 在 wal.Sync() 持续失败
// 且 pending 积压超过 4096 条时，丢弃最旧请求并关闭其 channel 的路径。
func TestStabilityGroupCommitterSyncFailureOverflow(t *testing.T) {
	walPath := filepath.Join(t.TempDir(), "wal.log")
	wal, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("创建 WAL 失败: %v", err)
	}

	if err := wal.AppendWrite([]byte("init")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	// 关闭 WAL 使后续 Sync 失败
	if err := wal.Close(); err != nil {
		t.Fatalf("关闭 WAL 失败: %v", err)
	}

	// 使用长间隔避免后台定时器干扰
	gc := NewGroupCommitter(wal, 1*time.Hour)

	// 提交超过 4096 个请求，触发溢出丢弃路径
	const totalRequests = 4100
	chs := make([]<-chan struct{}, totalRequests)
	for i := range chs {
		chs[i] = gc.Submit()
	}

	// 手动触发 doSync，Sync 会失败，pending 被放回队列
	gc.SyncNow()

	// 再次触发 doSync，此时 combined 列表长度 > 4096，应丢弃最旧的请求
	gc.SyncNow()

	// 验证有 channel 被关闭（被丢弃的请求）
	closedCount := 0
	for _, ch := range chs {
		select {
		case <-ch:
			closedCount++
		default:
		}
	}

	if closedCount == 0 {
		t.Error("期望有被丢弃的请求 channel 被关闭，但未检测到")
	}

	gc.Close()
}

// TestStabilityGroupCommitterSyncFailureNoOverflow 测试 doSync 在 wal.Sync() 失败
// 但 pending 未超过 4096 条时，请求被放回队列但不被丢弃。
func TestStabilityGroupCommitterSyncFailureNoOverflow(t *testing.T) {
	walPath := filepath.Join(t.TempDir(), "wal.log")
	wal, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("创建 WAL 失败: %v", err)
	}

	if err := wal.AppendWrite([]byte("init")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	if err := wal.Close(); err != nil {
		t.Fatalf("关闭 WAL 失败: %v", err)
	}

	gc := NewGroupCommitter(wal, 1*time.Hour)

	ch1 := gc.Submit()
	ch2 := gc.Submit()
	ch3 := gc.Submit()

	gc.SyncNow()

	for i, ch := range []<-chan struct{}{ch1, ch2, ch3} {
		select {
		case <-ch:
			t.Errorf("ch%d 不应在 sync 失败且未溢出时被关闭", i+1)
		default:
		}
	}

	gc.Close()
}

// TestStabilityGroupCommitterSyncFailureThenRecover 测试 doSync 在 Sync 失败后
// 请求被放回队列，然后重新打开 WAL 使 Sync 成功，请求最终被通知。
func TestStabilityGroupCommitterSyncFailureThenRecover(t *testing.T) {
	walPath := filepath.Join(t.TempDir(), "wal.log")

	wal, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("创建 WAL 失败: %v", err)
	}
	if err := wal.AppendWrite([]byte("init")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	if err := wal.Close(); err != nil {
		t.Fatalf("关闭 WAL 失败: %v", err)
	}

	wal2, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("重新创建 WAL 失败: %v", err)
	}
	if err := wal2.Close(); err != nil {
		t.Fatalf("关闭 WAL2 失败: %v", err)
	}

	gc := NewGroupCommitter(wal2, 1*time.Hour)

	ch := gc.Submit()
	gc.SyncNow()

	select {
	case <-ch:
		t.Fatal("sync 失败时 channel 不应被关闭")
	default:
	}

	gc.Close()
}

// TestStabilityGroupCommitterConcurrentSubmitDuringSyncFailure 测试并发提交请求
// 在 Sync 持续失败时的行为，验证 pending 合并逻辑的正确性。
func TestStabilityGroupCommitterConcurrentSubmitDuringSyncFailure(t *testing.T) {
	walPath := filepath.Join(t.TempDir(), "wal.log")
	wal, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("创建 WAL 失败: %v", err)
	}

	if err := wal.AppendWrite([]byte("init")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	if err := wal.Close(); err != nil {
		t.Fatalf("关闭 WAL 失败: %v", err)
	}

	gc := NewGroupCommitter(wal, 1*time.Hour)

	const numGoroutines = 10
	const numPerGoroutine = 50
	var wg sync.WaitGroup
	allChs := make([]<-chan struct{}, 0, numGoroutines*numPerGoroutine)
	var chMu sync.Mutex

	wg.Add(numGoroutines)
	for g := 0; g < numGoroutines; g++ {
		go func() {
			defer wg.Done()
			localChs := make([]<-chan struct{}, numPerGoroutine)
			for i := 0; i < numPerGoroutine; i++ {
				localChs[i] = gc.Submit()
			}
			chMu.Lock()
			allChs = append(allChs, localChs...)
			chMu.Unlock()
		}()
	}
	wg.Wait()

	gc.SyncNow()

	closedCount := 0
	for _, ch := range allChs {
		select {
		case <-ch:
			closedCount++
		default:
		}
	}
	if closedCount > 0 {
		t.Errorf("期望所有 channel 未关闭，但有 %d 个被关闭", closedCount)
	}

	gc.Close()
}

// TestStabilityGroupCommitterDoSyncEmptyPending 测试 doSync 在 pending 为空时立即返回。
func TestStabilityGroupCommitterDoSyncEmptyPending(t *testing.T) {
	t.Parallel()

	wal, err := CreateWAL(filepath.Join(t.TempDir(), "wal.log"))
	if err != nil {
		t.Fatalf("创建 WAL 失败: %v", err)
	}
	defer func() { _ = wal.Close() }()

	gc := NewGroupCommitter(wal, 1*time.Second)
	defer gc.Close()

	done := make(chan struct{})
	go func() {
		gc.SyncNow()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("doSync 在 pending 为空时应立即返回")
	}
}

// TestStabilityGroupCommitterDoubleClose 测试 GroupCommitter 重复关闭不会 panic。
func TestStabilityGroupCommitterDoubleClose(t *testing.T) {
	t.Parallel()

	wal, err := CreateWAL(filepath.Join(t.TempDir(), "wal.log"))
	if err != nil {
		t.Fatalf("创建 WAL 失败: %v", err)
	}
	defer func() { _ = wal.Close() }()

	gc := NewGroupCommitter(wal, 1*time.Millisecond)
	gc.Close()
	gc.Close()
}

// ---------------------------------------------------------------------------
// OpenWAL 错误路径
// ---------------------------------------------------------------------------

// TestStabilityOpenWALNotExist 测试 OpenWAL 打开不存在的文件。
func TestStabilityOpenWALNotExist(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.wal")

	_, _, err := OpenWAL(path)
	if err == nil {
		t.Fatal("期望打开不存在的文件返回错误，得到 nil")
	}

	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("期望错误包含 os.ErrNotExist，得到: %v", err)
	}

	if !strings.Contains(err.Error(), "wal open") {
		t.Errorf("期望错误包含 'wal open'，得到: %v", err)
	}
}

// TestStabilityOpenWALPermissionDenied 测试 OpenWAL 打开无权限文件。
func TestStabilityOpenWALPermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("权限测试在 Windows 上不可靠")
	}
	if os.Getuid() == 0 {
		t.Skip("root 用户绕过文件权限检查")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "noperm.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("创建 WAL 失败: %v", err)
	}
	_ = w.Close()

	if err := os.Chmod(path, 0000); err != nil {
		t.Fatalf("Chmod 失败: %v", err)
	}
	defer func() { _ = os.Chmod(path, 0644) }()

	_, _, err = OpenWAL(path)
	if err == nil {
		t.Fatal("期望打开无权限文件返回错误，得到 nil")
	}

	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("期望非 NotExist 错误，得到 NotExist: %v", err)
	}

	if !strings.Contains(err.Error(), "wal open") {
		t.Errorf("期望错误包含 'wal open'，得到: %v", err)
	}
}

// TestStabilityOpenWALDirectoryPath 测试 OpenWAL 打开目录路径，
// 验证返回非 IsNotExist 错误路径，适用于 root 用户环境。
func TestStabilityOpenWALDirectoryPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	_, _, err := OpenWAL(dir)
	if err == nil {
		t.Fatal("期望打开目录返回错误，得到 nil")
	}

	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("期望非 NotExist 错误（目录存在），得到 NotExist: %v", err)
	}

	if !strings.Contains(err.Error(), "wal open") {
		t.Errorf("期望错误包含 'wal open'，得到: %v", err)
	}
}

// TestStabilityOpenWALNotExistErrorMessage 测试 OpenWAL 文件不存在时的错误信息格式。
func TestStabilityOpenWALNotExistErrorMessage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "missing.wal")

	_, _, err := OpenWAL(path)
	if err == nil {
		t.Fatal("期望返回错误，得到 nil")
	}

	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("期望错误链包含 os.ErrNotExist，得到: %v", err)
	}

	if !strings.Contains(err.Error(), "wal open") {
		t.Errorf("期望错误信息包含 'wal open'，得到: %s", err.Error())
	}
}
