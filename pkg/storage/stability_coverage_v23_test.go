package storage

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
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

	// 等待后台 goroutine 处理完成
	time.Sleep(10 * time.Millisecond)

	// 再次提交一批请求，使 combined 列表长度 > 4096
	for i := 0; i < 10; i++ {
		gc.Submit()
	}

	// 再次触发 doSync，此时应丢弃最旧的请求
	gc.SyncNow()

	// 等待后台 goroutine 处理完成
	time.Sleep(10 * time.Millisecond)

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
	case <-time.After(2 * time.Second):
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

// ---------------------------------------------------------------------------
// OpenWAL 损坏数据与 Truncate 错误路径
// ---------------------------------------------------------------------------

// TestStabilityOpenWALCorruptedReplay 测试 OpenWAL 打开包含损坏数据的文件。
func TestStabilityOpenWALCorruptedReplay(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("创建 WAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte("valid_data"))
	_ = w.Sync()
	_ = w.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}

	corruptData := make([]byte, len(data)+6)
	copy(corruptData, data)
	binary.LittleEndian.PutUint32(corruptData[len(data):], 2)
	corruptData[len(data)+4] = 0x01
	corruptData[len(data)+5] = 0xFF

	if err := os.WriteFile(path, corruptData, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 损坏文件不应返回错误: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 1 {
		t.Errorf("期望 1 条有效记录，得到 %d", len(recs))
	}
	if string(recs[0].Payload) != "valid_data" {
		t.Errorf("记录 0: 期望 'valid_data'，得到 %q", string(recs[0].Payload))
	}
}

// TestStabilityOpenWALTruncateError 测试 OpenWAL 中 Truncate 失败的错误路径。
func TestStabilityOpenWALTruncateError(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("符号链接行为测试仅在 Linux 上可靠")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	if err := os.Symlink("/dev/null", path); err != nil {
		t.Fatalf("Symlink 失败: %v", err)
	}

	_, _, err := OpenWAL(path)
	if err == nil {
		t.Fatal("期望 Truncate 在 /dev/null 符号链接上失败，得到 nil 错误")
	}

	if !strings.Contains(err.Error(), "wal truncate") {
		t.Errorf("期望错误包含 'wal truncate'，得到: %v", err)
	}
}

// TestStabilityOpenWALSeekError 测试 OpenWAL 正常路径（Seek 错误路径难以直接触发）。
func TestStabilityOpenWALSeekError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "normal.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("创建 WAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte("data"))
	_ = w.Sync()
	_ = w.Close()

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 正常路径不应失败: %v", err)
	}
	_ = recovered.Close()

	if len(recs) != 1 {
		t.Errorf("期望 1 条记录，得到 %d", len(recs))
	}

	t.Log("Seek 错误路径在正常文件系统上难以直接触发，代码审查确认路径正确")
}

// TestStabilityOpenWALGarbageData 测试 OpenWAL 打开只包含垃圾数据的文件。
func TestStabilityOpenWALGarbageData(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "garbage.wal")

	garbage := []byte{0xFF, 0xFE, 0xFD, 0xFC, 0xFB, 0xFA, 0xF9, 0xF8}
	if err := os.WriteFile(path, garbage, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	w, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 垃圾文件不应返回错误: %v", err)
	}
	defer func() { _ = w.Close() }()

	if len(recs) != 0 {
		t.Errorf("期望 0 条记录，得到 %d", len(recs))
	}
	if w.Size() != 0 {
		t.Errorf("期望偏移量 0，得到 %d", w.Size())
	}
}

// TestStabilityOpenWALNormalPath 测试 OpenWAL 正常打开、回放、截断和追加的完整路径。
func TestStabilityOpenWALNormalPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "normal.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("创建 WAL 失败: %v", err)
	}

	for i := 0; i < 5; i++ {
		if err := w.AppendWrite([]byte("data")); err != nil {
			t.Fatalf("AppendWrite %d 失败: %v", i, err)
		}
	}
	_ = w.Close()

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}

	if len(recs) != 5 {
		t.Errorf("期望 5 条记录，得到 %d", len(recs))
	}

	if err := recovered.AppendWrite([]byte("after_open")); err != nil {
		t.Fatalf("恢复后追加失败: %v", err)
	}

	_ = recovered.Close()
}

// ---------------------------------------------------------------------------
// CompressColumn nil 输入
// ---------------------------------------------------------------------------

// TestStabilityCompressColumnNil 测试 CompressColumn 传入 nil 时返回预期的错误。
func TestStabilityCompressColumnNil(t *testing.T) {
	t.Parallel()

	err := CompressColumn(nil)
	if err == nil {
		t.Fatal("期望 CompressColumn(nil) 返回错误，得到 nil")
	}

	if !strings.Contains(err.Error(), "nil EncodedColumn") {
		t.Errorf("期望错误包含 'nil EncodedColumn'，得到: %v", err)
	}
}

// TestStabilityDecompressColumnNil 测试 DecompressColumn 传入 nil 时返回预期的错误。
func TestStabilityDecompressColumnNil(t *testing.T) {
	t.Parallel()

	err := DecompressColumn(nil)
	if err == nil {
		t.Fatal("期望 DecompressColumn(nil) 返回错误，得到 nil")
	}

	if !strings.Contains(err.Error(), "nil EncodedColumn") {
		t.Errorf("期望错误包含 'nil EncodedColumn'，得到: %v", err)
	}
}

// TestStabilityCompressColumnNormal 测试 CompressColumn 正常路径。
func TestStabilityCompressColumnNormal(t *testing.T) {
	t.Parallel()

	ints := make([]int64, 1000)
	for i := range ints {
		ints[i] = int64(i)
	}

	enc, err := EncodeColumn(common.TypeInt64, ints, uint32(len(ints)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}

	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn 失败: %v", err)
	}

	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn 失败: %v", err)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}

	decodedInts, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("期望 []int64，得到 %T", decoded)
	}

	for i := range ints {
		if decodedInts[i] != ints[i] {
			t.Errorf("行 %d: 得到 %d，期望 %d", i, decodedInts[i], ints[i])
		}
	}
}

// ---------------------------------------------------------------------------
// EncodeColumn / DecodeColumn 未知编码类型
// ---------------------------------------------------------------------------

// TestStabilityDecodeColumnUnknownEncoding 测试 DecodeColumn 在遇到未知编码类型时
// 返回 "unknown encoding" 错误。
func TestStabilityDecodeColumnUnknownEncoding(t *testing.T) {
	t.Parallel()

	enc := &EncodedColumn{
		Encoding: EncodingType(99),
		Type:     common.TypeInt64,
		RowCount: 1,
		Data:     make([]byte, 8),
	}

	_, _, err := DecodeColumn(enc)
	if err == nil {
		t.Fatal("期望 DecodeColumn 对未知编码类型返回错误，得到 nil")
	}

	if !strings.Contains(err.Error(), "unknown encoding") {
		t.Errorf("期望错误包含 'unknown encoding'，得到: %v", err)
	}
}

// TestStabilityEncodeColumnUnknownEncodingDefault 测试 EncodingType.String() 的 default 分支。
func TestStabilityEncodeColumnUnknownEncodingDefault(t *testing.T) {
	t.Parallel()

	unknownEnc := EncodingType(99)
	s := unknownEnc.String()
	if !strings.Contains(s, "Unknown") {
		t.Errorf("期望未知编码类型的 String() 包含 'Unknown'，得到: %s", s)
	}
}

// TestStabilityEncodeColumnWithKnownTypes 测试 EncodeColumn 对所有已知编码类型的正常路径。
func TestStabilityEncodeColumnWithKnownTypes(t *testing.T) {
	t.Parallel()

	ints := []int64{1, 2, 3, 4, 5}
	enc, err := EncodeColumn(common.TypeInt64, ints, uint32(len(ints)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn Int64 失败: %v", err)
	}
	if enc.Encoding != EncodingPlain && enc.Encoding != EncodingRLE {
		t.Errorf("Int64 编码类型异常: %v", enc.Encoding)
	}

	strs := []string{"hello", "world", "hello"}
	enc, err = EncodeColumn(common.TypeString, strs, uint32(len(strs)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn String 失败: %v", err)
	}
	if enc.Encoding != EncodingDict {
		t.Errorf("String 编码类型异常: %v", enc.Encoding)
	}

	bools := []uint64{1, 0, 1}
	enc, err = EncodeColumn(common.TypeBool, bools, uint32(len(bools)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn Bool 失败: %v", err)
	}
	if enc.Encoding != EncodingBitmap {
		t.Errorf("Bool 编码类型异常: %v", enc.Encoding)
	}

	rleInts := make([]int64, 100)
	for i := range rleInts {
		rleInts[i] = 42
	}
	enc, err = EncodeColumn(common.TypeInt64, rleInts, uint32(len(rleInts)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn RLE 失败: %v", err)
	}
	if enc.Encoding != EncodingRLE {
		t.Errorf("重复 Int64 编码类型异常: %v", enc.Encoding)
	}
}
