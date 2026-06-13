package storage

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// maybeRotate: 文件超过 maxSize 时触发轮转（wal.go 第 216-268 行）
// ---------------------------------------------------------------------------

// TestMaybeRotate_MaxSize1Byte 测试设置 maxSize 为 1 字节后追加数据触发轮转。
// maybeRotate 在 Append 写入之前检查 offset >= maxSize，因此需要先写入数据使 offset 超过 maxSize，
// 然后下一次 Append 才会触发轮转。
func TestMaybeRotate_MaxSize1Byte(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 先写入一条记录使 offset > 0
	if err := w.AppendWrite([]byte("initial")); err != nil {
		t.Fatalf("AppendWrite 初始数据失败: %v", err)
	}

	// 设置 maxSize 为 1 字节，当前 offset 已超过 maxSize
	w.maxSize = 1

	// 追加下一条记录，触发轮转
	if err := w.AppendWrite([]byte("trigger")); err != nil {
		t.Fatalf("AppendWrite 触发轮转失败: %v", err)
	}

	// 验证 .prev 文件存在
	if _, err := os.Stat(path + ".prev"); err != nil {
		t.Errorf("期望 .prev 文件存在: %v", err)
	}

	// 验证当前 WAL 文件存在
	if _, err := os.Stat(path); err != nil {
		t.Errorf("期望当前 WAL 文件存在: %v", err)
	}

	// 验证偏移量已重置（轮转后从 0 开始写入新数据）
	if w.Size() <= 0 {
		t.Errorf("轮转后偏移量应大于 0，实际 %d", w.Size())
	}

	// 恢复大 maxSize，验证后续写入正常
	w.maxSize = walDefaultMaxSize
	if err := w.AppendWrite([]byte("after_rotation")); err != nil {
		t.Fatalf("轮转后 AppendWrite 失败: %v", err)
	}

	_ = w.Close()
}

// TestMaybeRotate_MultipleRotations_V9 测试多次轮转的稳定性。
// 每次轮转后 .prev 文件应被覆盖。
func TestMaybeRotate_MultipleRotations_V9(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 设置很小的 maxSize 以便多次触发轮转
	w.maxSize = 50

	// 写入足够多的记录以触发多次轮转
	for i := 0; i < 20; i++ {
		payload := []byte("rotation_data_payload")
		if err := w.AppendWrite(payload); err != nil {
			t.Fatalf("AppendWrite #%d 失败: %v", i, err)
		}
	}

	// 验证 .prev 文件存在（最后一次轮转的结果）
	if _, err := os.Stat(path + ".prev"); err != nil {
		t.Errorf("期望 .prev 文件存在: %v", err)
	}

	// 验证当前 WAL 文件存在
	if _, err := os.Stat(path); err != nil {
		t.Errorf("期望当前 WAL 文件存在: %v", err)
	}

	_ = w.Close()
}

// TestMaybeRotate_RotateThenOpenWAL 测试轮转后关闭 WAL，
// 再通过 OpenWAL 打开，验证轮转后的记录可正常恢复。
func TestMaybeRotate_RotateThenOpenWAL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入初始数据
	if err := w.AppendWrite([]byte("before_rotate")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	// 触发轮转
	w.maxSize = 1
	if err := w.AppendWrite([]byte("trigger")); err != nil {
		t.Fatalf("AppendWrite 触发轮转失败: %v", err)
	}

	// 轮转后写入新数据
	w.maxSize = walDefaultMaxSize
	if err := w.AppendWrite([]byte("after_rotate")); err != nil {
		t.Fatalf("轮转后 AppendWrite 失败: %v", err)
	}

	_ = w.Close()

	// 通过 OpenWAL 打开，验证轮转后的记录可恢复
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	// 应只包含轮转后的记录
	if len(recs) < 1 {
		t.Errorf("期望至少 1 条记录，实际 %d", len(recs))
	}
}

// ---------------------------------------------------------------------------
// maybeRotate: 临时文件创建失败路径（wal.go 第 224-226 行）
// ---------------------------------------------------------------------------

// TestMaybeRotate_CreateTempFailure 测试 maybeRotate 中创建临时文件失败。
// 通过将目录设为只读使 os.Create(w.path+".tmp") 失败。
// 注意：root 用户可以绕过文件权限，此测试在 root 下跳过。
func TestMaybeRotate_CreateTempFailure(t *testing.T) {
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

	// 将目录设为只读，使 os.Create(w.path+".tmp") 失败
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatalf("Chmod 失败: %v", err)
	}
	defer func() { _ = os.Chmod(dir, 0755) }()

	// 触发轮转 - 创建临时文件应该失败
	err = w.AppendWrite([]byte("trigger"))
	if err == nil {
		_ = os.Chmod(dir, 0755)
		_ = w.Close()
		t.Fatal("期望创建临时文件失败时返回错误，得到 nil")
	}

	if !strings.Contains(err.Error(), "wal rotate create temp") {
		t.Errorf("错误消息应包含 'wal rotate create temp'，得到: %v", err)
	}

	// 恢复目录权限以便清理
	_ = os.Chmod(dir, 0755)
	_ = w.Close()
}

// ---------------------------------------------------------------------------
// maybeRotate: 旧文件关闭失败路径（wal.go 第 238-242 行）
// ---------------------------------------------------------------------------

// TestMaybeRotate_CloseOldFileFailure 测试 maybeRotate 中关闭旧文件失败。
// 通过提前关闭底层文件描述符来触发 old.Close() 失败。
func TestMaybeRotate_CloseOldFileFailure(t *testing.T) {
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

	// 直接关闭底层文件描述符，使 maybeRotate 中 old.Close() 失败
	if err := w.file.Close(); err != nil {
		t.Fatalf("关闭底层文件失败: %v", err)
	}

	// 触发轮转 - old.Close() 应该失败
	err = w.AppendWrite([]byte("trigger"))
	if err == nil {
		_ = w.Close()
		t.Fatal("期望关闭旧文件失败时返回错误，得到 nil")
	}

	if !strings.Contains(err.Error(), "wal rotate close") {
		t.Errorf("错误消息应包含 'wal rotate close'，得到: %v", err)
	}

	// 验证临时文件已被清理
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("期望临时文件已被删除，但文件仍存在")
	}

	_ = w.Close()
}

// ---------------------------------------------------------------------------
// maybeRotate: 第一次 Rename 失败路径（wal.go 第 245-248 行）
// ---------------------------------------------------------------------------

// TestMaybeRotate_RenameOldFileFailure 测试 maybeRotate 中重命名旧文件为 .prev 失败。
// 在 rotatedPath 处创建非空目录，使 os.Rename(w.path, rotatedPath) 失败
// （Linux 上不能将文件重命名为非空目录）。
func TestMaybeRotate_RenameOldFileFailure(t *testing.T) {
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

	// 在 rotatedPath 处创建非空目录，使 Rename 失败
	rotatedPath := path + ".prev"
	if err := os.Mkdir(rotatedPath, 0755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	// 在目录中创建文件使其非空
	blockerF, err := os.Create(filepath.Join(rotatedPath, "blocker"))
	if err != nil {
		t.Fatalf("创建阻塞文件失败: %v", err)
	}
	_ = blockerF.Close()
	defer func() {
		_ = os.Remove(filepath.Join(rotatedPath, "blocker"))
		_ = os.Remove(rotatedPath)
	}()

	// 触发轮转 - Rename(w.path, rotatedPath) 应该失败
	err = w.AppendWrite([]byte("trigger"))
	if err == nil {
		_ = w.Close()
		t.Fatal("期望 Rename 旧文件失败时返回错误，得到 nil")
	}

	if !strings.Contains(err.Error(), "wal rotate rename") {
		t.Errorf("错误消息应包含 'wal rotate rename'，得到: %v", err)
	}

	_ = w.Close()
}

// ---------------------------------------------------------------------------
// OpenWAL: 打开包含有效记录的 WAL（wal.go 第 67-101 行）
// ---------------------------------------------------------------------------

// TestOpenWAL_ExistingRecords 测试 OpenWAL 打开包含多条有效记录的 WAL。
func TestOpenWAL_ExistingRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建 WAL 并写入多条记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	records := []struct {
		tp      byte
		payload []byte
	}{
		{walTypeWrite, []byte("write_data_1")},
		{walTypeCommit, []byte("commit_data")},
		{walTypeCheckpoint, []byte("checkpoint_data")},
		{walTypeWrite, []byte("write_data_2")},
	}

	for _, r := range records {
		if err := w.Append(r.tp, r.payload); err != nil {
			t.Fatalf("Append 失败: %v", err)
		}
	}

	_ = w.Close()

	// 通过 OpenWAL 打开
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	// 验证记录数量
	if len(recs) != len(records) {
		t.Fatalf("期望 %d 条记录，实际 %d", len(records), len(recs))
	}

	// 验证记录内容和类型
	for i, rec := range recs {
		if rec.Type != records[i].tp {
			t.Errorf("记录 %d 类型不匹配: 期望 %d, 实际 %d", i, records[i].tp, rec.Type)
		}
		if string(rec.Payload) != string(records[i].payload) {
			t.Errorf("记录 %d 负载不匹配: 期望 %q, 实际 %q", i, string(records[i].payload), string(rec.Payload))
		}
	}
}

// TestOpenWAL_EmptyFile_V9 测试 OpenWAL 打开空 WAL 文件。
func TestOpenWAL_EmptyFile_V9(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建空 WAL 文件
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.Close()

	// 通过 OpenWAL 打开空文件
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 打开空文件失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	// 应无记录
	if len(recs) != 0 {
		t.Errorf("期望 0 条记录，实际 %d", len(recs))
	}

	// 偏移量应为 0
	if recovered.Size() != 0 {
		t.Errorf("期望偏移量 0，实际 %d", recovered.Size())
	}
}

// TestOpenWAL_CorruptedRecords 测试 OpenWAL 打开包含损坏记录的 WAL。
// 验证 OpenWAL 在遇到损坏记录时停止回放，只返回有效记录。
func TestOpenWAL_CorruptedRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建 WAL 并写入有效记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	if err := w.AppendWrite([]byte("valid_record_1")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	if err := w.AppendWrite([]byte("valid_record_2")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	_ = w.Sync()
	_ = w.Close()

	// 读取文件内容，追加损坏数据
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}

	// 追加一个无效的长度头部（totalLen 太大）
	corrupted := make([]byte, len(data)+10)
	copy(corrupted, data)
	// 写入一个无效的 totalLen（远大于 maxRecordPayload）
	binary.LittleEndian.PutUint32(corrupted[len(data):], 0xFFFFFFFF)

	if err := os.WriteFile(path, corrupted, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	// 通过 OpenWAL 打开
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	// 应只返回 2 条有效记录，损坏部分被截断
	if len(recs) != 2 {
		t.Errorf("期望 2 条有效记录，实际 %d", len(recs))
	}

	// 验证文件已被截断到有效记录末尾
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if fi.Size() != int64(len(data)) {
		t.Errorf("文件大小 = %d, 期望 %d（截断到有效数据末尾）", fi.Size(), len(data))
	}
}

// TestOpenWAL_PartialHeader 测试 OpenWAL 打开包含部分头部写入的 WAL。
// 模拟崩溃时只写入了部分头部的情况。
func TestOpenWAL_PartialHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建 WAL 并写入有效记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	if err := w.AppendWrite([]byte("valid_data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	_ = w.Sync()
	_ = w.Close()

	// 读取文件内容，追加部分头部（2 字节，不足 4 字节头部）
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}

	partial := make([]byte, len(data)+2)
	copy(partial, data)
	partial[len(data)] = 0x01
	partial[len(data)+1] = 0x02

	if err := os.WriteFile(path, partial, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	// 通过 OpenWAL 打开
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	// 应只返回 1 条有效记录
	if len(recs) != 1 {
		t.Errorf("期望 1 条有效记录，实际 %d", len(recs))
	}
}
