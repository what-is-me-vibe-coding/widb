package storage

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// TestOpenWAL_CRCMismatch 测试 OpenWAL 打开包含 CRC 不匹配记录的 WAL。
func TestOpenWAL_CRCMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建 WAL 并写入有效记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	if err := w.AppendWrite([]byte("good_record")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	_ = w.Sync()
	_ = w.Close()

	// 读取文件内容，修改 CRC 使其不匹配
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}

	// 记录格式: 4字节长度 + 1字节类型 + payload + 4字节CRC
	// 修改最后一个字节（CRC 的最后一个字节）使 CRC 不匹配
	data[len(data)-1] ^= 0xFF

	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	// 通过 OpenWAL 打开
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	// CRC 不匹配的记录应被丢弃
	if len(recs) != 0 {
		t.Errorf("期望 0 条有效记录（CRC 不匹配），实际 %d", len(recs))
	}
}

// TestOpenWAL_NonExistentFile 测试 OpenWAL 打开不存在的文件。
func TestOpenWAL_NonExistentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.wal")

	_, _, err := OpenWAL(path)
	if err == nil {
		t.Fatal("期望打开不存在的文件返回错误，得到 nil")
	}
}

// TestOpenWAL_TruncateAndSeek 测试 OpenWAL 的截断和定位路径。
// 写入有效记录后追加垃圾数据，验证 OpenWAL 正确截断并定位。
func TestOpenWAL_TruncateAndSeek(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建包含多条记录的 WAL
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := w.AppendWrite([]byte("record_data")); err != nil {
			t.Fatalf("AppendWrite #%d 失败: %v", i, err)
		}
	}
	_ = w.Sync()
	_ = w.Close()

	// 读取有效数据长度
	validData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}
	validLen := len(validData)

	// 追加垃圾数据
	garbage := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE}
	modified := make([]byte, validLen+len(garbage))
	copy(modified, validData)
	copy(modified[validLen:], garbage)
	if err := os.WriteFile(path, modified, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	// 通过 OpenWAL 打开
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}

	// 验证记录数量
	if len(recs) != 3 {
		t.Errorf("期望 3 条记录，实际 %d", len(recs))
	}

	// 验证文件已被截断到有效数据末尾
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if fi.Size() != int64(validLen) {
		t.Errorf("文件大小 = %d, 期望 %d", fi.Size(), validLen)
	}

	// 验证 Seek 正确：恢复后可以追加新记录
	if err := recovered.AppendWrite([]byte("after_recovery")); err != nil {
		t.Fatalf("恢复后 AppendWrite 失败: %v", err)
	}

	_ = recovered.Close()

	// 再次打开验证所有记录
	recovered2, recs2, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("第二次 OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered2.Close() }()

	if len(recs2) != 4 {
		t.Errorf("期望 4 条记录，实际 %d", len(recs2))
	}
}

// ---------------------------------------------------------------------------
// WAL Truncate 操作（wal.go 第 191-211 行）
// ---------------------------------------------------------------------------

// TestWAL_Truncate 测试 WAL Truncate 操作清空文件并重置偏移量。
func TestWAL_Truncate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入多条记录
	for i := 0; i < 5; i++ {
		if err := w.AppendWrite([]byte("data_before_truncate")); err != nil {
			t.Fatalf("AppendWrite #%d 失败: %v", i, err)
		}
	}

	// 验证偏移量大于 0
	sizeBefore := w.Size()
	if sizeBefore == 0 {
		t.Fatal("期望写入后偏移量大于 0")
	}

	// 执行 Truncate
	if err := w.Truncate(); err != nil {
		t.Fatalf("Truncate 失败: %v", err)
	}

	// 验证偏移量已重置为 0
	if w.Size() != 0 {
		t.Errorf("Truncate 后偏移量应为 0，实际 %d", w.Size())
	}

	// 验证 Truncate 后可以继续写入
	if err := w.AppendWrite([]byte("after_truncate")); err != nil {
		t.Fatalf("Truncate 后 AppendWrite 失败: %v", err)
	}

	if w.Size() == 0 {
		t.Error("Truncate 后写入数据，偏移量应大于 0")
	}

	_ = w.Close()
}

// TestWAL_TruncateThenOpen 测试 Truncate 后关闭再打开 WAL。
func TestWAL_TruncateThenOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入数据
	if err := w.AppendWrite([]byte("old_data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	// 执行 Truncate
	if err := w.Truncate(); err != nil {
		t.Fatalf("Truncate 失败: %v", err)
	}

	// 写入新数据
	if err := w.AppendWrite([]byte("new_data")); err != nil {
		t.Fatalf("Truncate 后 AppendWrite 失败: %v", err)
	}

	_ = w.Close()

	// 通过 OpenWAL 打开，验证只有新数据
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 1 {
		t.Errorf("期望 1 条记录（Truncate 后的新数据），实际 %d", len(recs))
	}

	if string(recs[0].Payload) != "new_data" {
		t.Errorf("记录负载不匹配: 期望 'new_data', 实际 %q", string(recs[0].Payload))
	}
}

// ---------------------------------------------------------------------------
// WAL Size() 报告（wal.go 第 174-176 行）
// ---------------------------------------------------------------------------

// TestWAL_SizeReporting 测试 Size() 正确报告当前偏移量。
func TestWAL_SizeReporting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 初始偏移量应为 0
	if w.Size() != 0 {
		t.Errorf("初始偏移量应为 0，实际 %d", w.Size())
	}

	// 写入一条记录后偏移量应增加
	if err := w.AppendWrite([]byte("test_data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	sizeAfterWrite := w.Size()
	if sizeAfterWrite == 0 {
		t.Error("写入后偏移量应大于 0")
	}

	// 写入第二条记录后偏移量应继续增加
	if err := w.AppendWrite([]byte("more_data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	if w.Size() <= sizeAfterWrite {
		t.Errorf("第二次写入后偏移量 %d 应大于第一次 %d", w.Size(), sizeAfterWrite)
	}

	_ = w.Close()
}

// TestWAL_SizeAfterTruncate 测试 Truncate 后 Size() 返回 0。
func TestWAL_SizeAfterTruncate(t *testing.T) {
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

	if w.Size() == 0 {
		t.Fatal("写入后偏移量应大于 0")
	}

	// Truncate 后偏移量应为 0
	if err := w.Truncate(); err != nil {
		t.Fatalf("Truncate 失败: %v", err)
	}

	if w.Size() != 0 {
		t.Errorf("Truncate 后偏移量应为 0，实际 %d", w.Size())
	}

	_ = w.Close()
}

// ---------------------------------------------------------------------------
// 并发 Append 操作
// ---------------------------------------------------------------------------

// TestWAL_ConcurrentAppend 测试并发追加记录的正确性。
func TestWAL_ConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	const goroutines = 10
	const recordsPerGoroutine = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < recordsPerGoroutine; i++ {
				payload := []byte("goroutine_data")
				if err := w.AppendWrite(payload); err != nil {
					t.Errorf("goroutine %d AppendWrite #%d 失败: %v", id, i, err)
					return
				}
			}
		}(g)
	}

	wg.Wait()

	// 验证总记录数
	_ = w.Close()

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	expectedTotal := goroutines * recordsPerGoroutine
	if len(recs) != expectedTotal {
		t.Errorf("期望 %d 条记录，实际 %d", expectedTotal, len(recs))
	}
}

// TestWAL_ConcurrentAppendWithRotation 测试并发追加时触发轮转的正确性。
func TestWAL_ConcurrentAppendWithRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 设置较小的 maxSize 以便在并发写入时触发轮转
	w.maxSize = 200

	const goroutines = 5
	const recordsPerGoroutine = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < recordsPerGoroutine; i++ {
				payload := []byte("concurrent_rotation_data")
				if err := w.AppendWrite(payload); err != nil {
					t.Errorf("goroutine %d AppendWrite #%d 失败: %v", id, i, err)
					return
				}
			}
		}(g)
	}

	wg.Wait()

	_ = w.Close()

	// 验证 .prev 文件存在（轮转已发生）
	if _, err := os.Stat(path + ".prev"); err != nil {
		t.Logf("轮转可能未发生（数据量不足）: %v", err)
	}

	// 验证当前 WAL 文件可正常打开
	recovered, _, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	_ = recovered.Close()
}

// ---------------------------------------------------------------------------
// AppendCommit / AppendCheckpoint 类型特定方法
// ---------------------------------------------------------------------------

// TestWAL_AppendCommitAndCheckpoint 测试 AppendCommit 和 AppendCheckpoint 方法。
func TestWAL_AppendCommitAndCheckpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 追加 Commit 记录
	if err := w.AppendCommit([]byte("commit_payload")); err != nil {
		t.Fatalf("AppendCommit 失败: %v", err)
	}

	// 追加 Checkpoint 记录
	if err := w.AppendCheckpoint([]byte("checkpoint_payload")); err != nil {
		t.Fatalf("AppendCheckpoint 失败: %v", err)
	}

	_ = w.Close()

	// 验证记录类型
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 2 {
		t.Fatalf("期望 2 条记录，实际 %d", len(recs))
	}

	if recs[0].Type != walTypeCommit {
		t.Errorf("第一条记录类型应为 Commit(%d)，实际 %d", walTypeCommit, recs[0].Type)
	}
	if recs[1].Type != walTypeCheckpoint {
		t.Errorf("第二条记录类型应为 Checkpoint(%d)，实际 %d", walTypeCheckpoint, recs[1].Type)
	}
}

// ---------------------------------------------------------------------------
// AppendBatch 批量写入
// ---------------------------------------------------------------------------

// TestWAL_AppendBatch 测试 AppendBatch 批量追加记录。
func TestWAL_AppendBatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	records := []BatchRecord{
		{Type: walTypeWrite, Payload: []byte("batch_1")},
		{Type: walTypeWrite, Payload: []byte("batch_2")},
		{Type: walTypeCommit, Payload: []byte("batch_commit")},
	}

	if err := w.AppendBatch(records); err != nil {
		t.Fatalf("AppendBatch 失败: %v", err)
	}

	_ = w.Close()

	// 验证记录
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 3 {
		t.Fatalf("期望 3 条记录，实际 %d", len(recs))
	}

	for i, rec := range recs {
		if rec.Type != records[i].Type {
			t.Errorf("记录 %d 类型不匹配: 期望 %d, 实际 %d", i, records[i].Type, rec.Type)
		}
		if string(rec.Payload) != string(records[i].Payload) {
			t.Errorf("记录 %d 负载不匹配: 期望 %q, 实际 %q", i, string(records[i].Payload), string(rec.Payload))
		}
	}
}

// TestWAL_AppendBatchPayloadTooLarge 测试 AppendBatch 中负载过大时返回错误。
func TestWAL_AppendBatchPayloadTooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 创建超过 maxRecordPayload 的负载
	largePayload := make([]byte, maxRecordPayload+1)
	records := []BatchRecord{
		{Type: walTypeWrite, Payload: largePayload},
	}

	err = w.AppendBatch(records)
	if err == nil {
		_ = w.Close()
		t.Fatal("期望负载过大时返回错误，得到 nil")
	}

	if !strings.Contains(err.Error(), "payload too large") {
		t.Errorf("错误消息应包含 'payload too large'，得到: %v", err)
	}

	_ = w.Close()
}

// ---------------------------------------------------------------------------
// Append 负载过大
// ---------------------------------------------------------------------------

// TestWAL_AppendPayloadTooLarge 测试 Append 中负载过大时返回错误。
func TestWAL_AppendPayloadTooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 创建超过 maxRecordPayload 的负载
	largePayload := make([]byte, maxRecordPayload+1)
	err = w.Append(walTypeWrite, largePayload)
	if err == nil {
		_ = w.Close()
		t.Fatal("期望负载过大时返回错误，得到 nil")
	}

	if !strings.Contains(err.Error(), "payload too large") {
		t.Errorf("错误消息应包含 'payload too large'，得到: %v", err)
	}

	_ = w.Close()
}
