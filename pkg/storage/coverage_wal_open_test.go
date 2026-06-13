package storage

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// OpenWAL: 文件不存在错误路径（第 70-71 行）
// ---------------------------------------------------------------------------

// TestCoverageOpenWALNotExist 测试打开不存在的 WAL 文件返回错误，
// 且错误链中包含 os.ErrNotExist（覆盖第 70-71 行的 os.IsNotExist 分支）。
func TestCoverageOpenWALNotExist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.wal")

	_, _, err := OpenWAL(path)
	if err == nil {
		t.Fatal("期望打开不存在的文件返回错误，得到 nil")
	}

	// 验证错误链包含 os.ErrNotExist，确保走的是第 70-71 行的 IsNotExist 分支
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("期望错误包含 os.ErrNotExist，得到: %v", err)
	}

	// 验证错误信息包含 "wal open"
	if !strings.Contains(err.Error(), "wal open") {
		t.Errorf("期望错误包含 'wal open'，得到: %v", err)
	}
}

// ---------------------------------------------------------------------------
// OpenWAL: Truncate 错误路径（第 84-86 行）
// ---------------------------------------------------------------------------

// TestCoverageOpenWALTruncateErrorSymlink 测试 OpenWAL 中 Truncate 失败的错误路径（第 84-86 行）。
// 通过创建指向 /dev/null 的符号链接作为 WAL 路径，使 os.OpenFile 成功但
// f.Truncate 返回 EINVAL（字符设备不支持截断操作），覆盖第 84-86 行。
func TestCoverageOpenWALTruncateErrorSymlink(t *testing.T) {
	if runtime.GOOS != skipNonLinux {
		t.Skip("符号链接行为测试仅在 Linux 上可靠")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建指向 /dev/null 的符号链接
	// /dev/null 可以用 O_RDWR 打开，replayWAL 返回空记录（读 /dev/null 立即返回 EOF），
	// 但 f.Truncate(0) 在字符设备上返回 EINVAL
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

// ---------------------------------------------------------------------------
// OpenWAL: Seek 错误路径（第 88-90 行）
// ---------------------------------------------------------------------------

// TestCoverageOpenWALSeekErrorViaSymlink 测试 OpenWAL 中 Seek 失败的错误路径。
// 通过创建指向 /dev/null 的符号链接作为 WAL 路径，使 Truncate 成功（/dev/null
// 上 Truncate 是空操作），但 Seek 可能返回 ESPIPE 或其他错误。
// 注意：在大多数 Linux 系统上，/dev/null 的 Seek 实际上会成功，
// 因此此测试主要覆盖正常路径，并记录 Seek 错误路径的存在。
func TestCoverageOpenWALSeekErrorViaSymlink(t *testing.T) {
	if runtime.GOOS != skipNonLinux {
		t.Skip("符号链接行为测试仅在 Linux 上可靠")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建指向 /dev/null 的符号链接
	if err := os.Symlink("/dev/null", path); err != nil {
		t.Fatalf("Symlink 失败: %v", err)
	}

	// /dev/null 可以用 O_RDWR 打开，replayWAL 返回空记录
	// Truncate(0) 在 /dev/null 上可能成功（空操作）
	// Seek(0) 在 /dev/null 上通常也成功
	// 因此此测试验证正常路径，如果 Truncate 或 Seek 失败则覆盖错误路径
	_, _, err := OpenWAL(path)
	if err != nil {
		// 如果 Truncate 或 Seek 在 /dev/null 上失败，验证错误信息
		if !strings.Contains(err.Error(), "wal truncate") && !strings.Contains(err.Error(), "wal seek") {
			t.Errorf("期望错误包含 'wal truncate' 或 'wal seek'，得到: %v", err)
		}
		t.Logf("OpenWAL 在 /dev/null 符号链接上返回错误（可能覆盖了错误路径）: %v", err)
	}
}

// ---------------------------------------------------------------------------
// OpenWAL: 损坏数据截断验证
// ---------------------------------------------------------------------------

// TestCoverageOpenWALCorruptedPartialHeader 测试 WAL 文件末尾有部分头部时，
// OpenWAL 截断到有效偏移量并验证文件大小正确。
func TestCoverageOpenWALCorruptedPartialHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建 WAL 并写入有效记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte("valid_data_1"))
	_ = w.AppendWrite([]byte("valid_data_2"))
	_ = w.Sync()
	_ = w.Close()

	// 读取有效数据长度
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}
	validSize := len(data)

	// 在末尾追加部分头部（2 字节，头部需要 4 字节）
	corruptData := make([]byte, validSize+2)
	copy(corruptData, data)
	corruptData[validSize] = 0x05
	corruptData[validSize+1] = 0x00

	if err := os.WriteFile(path, corruptData, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	// 打开 WAL，应截断到有效偏移量
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	// 验证恢复 2 条有效记录
	if len(recs) != 2 {
		t.Fatalf("期望 2 条有效记录，得到 %d", len(recs))
	}

	// 验证文件已被截断到有效偏移量
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if fi.Size() != int64(validSize) {
		t.Errorf("期望文件大小 %d（截断后），得到 %d", validSize, fi.Size())
	}

	// 验证恢复后可以继续追加
	if err := recovered.AppendWrite([]byte("after_recovery")); err != nil {
		t.Fatalf("恢复后追加失败: %v", err)
	}
}

// TestCoverageOpenWALCorruptedBadCRC 测试 WAL 文件中 CRC 损坏时，
// OpenWAL 截断到有效偏移量（损坏记录之前）。
func TestCoverageOpenWALCorruptedBadCRC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建 WAL 并写入多条有效记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte("record_a"))
	_ = w.AppendWrite([]byte("record_b"))
	_ = w.AppendWrite([]byte("record_c"))
	_ = w.Sync()
	_ = w.Close()

	// 读取文件并损坏第二条记录的 CRC
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}

	// 第一条记录大小：header(4) + type(1) + payload(8) + crc(4) = 17
	rec1Size := walHeaderSize + walTypeSize + len("record_a") + walCRCSize
	// 第二条记录的 CRC 位于 rec1Size + header(4) + type(1) + payload(8) 之后
	rec2CRCEnd := rec1Size + walHeaderSize + walTypeSize + len("record_b") + walCRCSize
	// 损坏第二条记录 CRC 的最后一个字节
	data[rec2CRCEnd-1] ^= 0xFF

	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	// 打开 WAL，应只恢复第一条有效记录
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 1 {
		t.Fatalf("期望 1 条有效记录（CRC 损坏前），得到 %d", len(recs))
	}
	if string(recs[0].Payload) != "record_a" {
		t.Errorf("记录 0: 期望 'record_a'，得到 %q", string(recs[0].Payload))
	}

	// 验证文件被截断到第一条记录末尾
	expectedSize := int64(rec1Size)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if fi.Size() != expectedSize {
		t.Errorf("期望文件大小 %d（截断后），得到 %d", expectedSize, fi.Size())
	}
}

// TestCoverageOpenWALCorruptedInvalidLength 测试 WAL 文件中 totalLen 过大时，
// OpenWAL 截断到有效偏移量。
func TestCoverageOpenWALCorruptedInvalidLength(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建 WAL 并写入有效记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte("good_record"))
	_ = w.Sync()
	_ = w.Close()

	// 读取有效数据长度
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}
	validSize := len(data)

	// 追加一个 totalLen 超过最大限制的头部
	invalidHeader := make([]byte, walHeaderSize+10)
	binary.LittleEndian.PutUint32(invalidHeader, uint32(maxRecordPayload+walTypeSize+walCRCSize+1))
	// 填充一些 body 数据
	for i := walHeaderSize; i < len(invalidHeader); i++ {
		invalidHeader[i] = 0x41
	}

	corruptData := make([]byte, validSize+len(invalidHeader))
	copy(corruptData, data)
	copy(corruptData[validSize:], invalidHeader)

	if err := os.WriteFile(path, corruptData, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	// 打开 WAL，应只恢复有效记录
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 1 {
		t.Fatalf("期望 1 条有效记录，得到 %d", len(recs))
	}
	if string(recs[0].Payload) != "good_record" {
		t.Errorf("记录 0: 期望 'good_record'，得到 %q", string(recs[0].Payload))
	}

	// 验证文件被截断到有效数据末尾
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if fi.Size() != int64(validSize) {
		t.Errorf("期望文件大小 %d（截断后），得到 %d", validSize, fi.Size())
	}
}

// ---------------------------------------------------------------------------
// OpenWAL: 正常写入后回放验证
// ---------------------------------------------------------------------------

// TestCoverageOpenWALNormalReplay 测试正常写入后 OpenWAL 正确回放所有记录，
// 且恢复后可以继续追加，验证完整的生命周期。
func TestCoverageOpenWALNormalReplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建 WAL 并写入多种类型的记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	_ = w.AppendWrite([]byte("write_data"))
	_ = w.AppendCommit([]byte("commit_data"))
	_ = w.AppendCheckpoint([]byte("checkpoint_data"))
	_ = w.AppendBatch([]BatchRecord{
		{Type: walTypeWrite, Payload: []byte("batch_1")},
		{Type: walTypeWrite, Payload: []byte("batch_2")},
	})
	_ = w.Sync()

	sizeAfterWrite := w.Size()
	if sizeAfterWrite == 0 {
		t.Fatal("期望写入后偏移量非零")
	}
	_ = w.Close()

	// 打开 WAL 验证回放
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}

	// 验证回放了 5 条记录
	if len(recs) != 5 {
		t.Fatalf("期望 5 条记录，得到 %d", len(recs))
	}

	// 验证记录类型
	if recs[0].Type != walTypeWrite {
		t.Errorf("记录 0: 期望类型 %d，得到 %d", walTypeWrite, recs[0].Type)
	}
	if recs[1].Type != walTypeCommit {
		t.Errorf("记录 1: 期望类型 %d，得到 %d", walTypeCommit, recs[1].Type)
	}
	if recs[2].Type != walTypeCheckpoint {
		t.Errorf("记录 2: 期望类型 %d，得到 %d", walTypeCheckpoint, recs[2].Type)
	}

	// 验证偏移量正确恢复
	if recovered.Size() != sizeAfterWrite {
		t.Errorf("期望偏移量 %d，得到 %d", sizeAfterWrite, recovered.Size())
	}

	// 恢复后继续追加
	if err := recovered.AppendWrite([]byte("after_replay")); err != nil {
		t.Fatalf("恢复后追加失败: %v", err)
	}
	_ = recovered.Sync()
	_ = recovered.Close()

	// 再次打开验证所有 6 条记录
	recovered2, recs2, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("第二次 OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered2.Close() }()

	if len(recs2) != 6 {
		t.Fatalf("期望 6 条记录，得到 %d", len(recs2))
	}
}

// ---------------------------------------------------------------------------
// OpenWAL: 非NotExist错误路径（第 73 行）
// ---------------------------------------------------------------------------

// TestCoverageOpenWALNonNotExistError 测试 OpenWAL 在文件存在但无法以 O_RDWR
// 打开时返回非 NotExist 错误（覆盖第 73 行）。
func TestCoverageOpenWALNonNotExistError(t *testing.T) {
	if runtime.GOOS == skipWindows {
		t.Skip("权限测试在 Windows 上不可靠")
	}
	if os.Getuid() == 0 {
		t.Skip("root 用户绕过文件权限检查")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "readonly.wal")

	// 创建 WAL 文件
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.Close()

	// 将文件设为只读，使 O_RDWR 打开失败
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatalf("Chmod 失败: %v", err)
	}
	defer func() { _ = os.Chmod(path, 0644) }()

	_, _, err = OpenWAL(path)
	if err == nil {
		t.Fatal("期望打开只读文件返回错误，得到 nil")
	}

	// 验证不是 NotExist 错误
	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("期望非 NotExist 错误，得到 NotExist: %v", err)
	}

	// 验证错误信息包含 "wal open"
	if !strings.Contains(err.Error(), "wal open") {
		t.Errorf("期望错误包含 'wal open'，得到: %v", err)
	}
}

// ---------------------------------------------------------------------------
// OpenWAL: 空文件和只有垃圾数据的文件
// ---------------------------------------------------------------------------

// TestCoverageOpenWALEmptyFileTruncate 测试打开空 WAL 文件时，
// Truncate(0) 和 Seek(0) 正常工作，覆盖第 84-90 行的正常路径。
func TestCoverageOpenWALEmptyFileTruncate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.wal")

	// 创建空文件
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("创建空文件失败: %v", err)
	}
	_ = f.Close()

	w, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 空文件失败: %v", err)
	}
	defer func() { _ = w.Close() }()

	if len(recs) != 0 {
		t.Errorf("期望 0 条记录，得到 %d", len(recs))
	}
	if w.Size() != 0 {
		t.Errorf("期望偏移量 0，得到 %d", w.Size())
	}

	// 验证可以继续追加
	if err := w.AppendWrite([]byte("after_empty")); err != nil {
		t.Fatalf("空文件恢复后追加失败: %v", err)
	}
}

// TestCoverageOpenWALOnlyGarbage 测试打开只包含垃圾数据的 WAL 文件，
// validOffset 为 0 时 Truncate(0) 和 Seek(0) 正常工作。
func TestCoverageOpenWALOnlyGarbage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "garbage.wal")

	// 创建只包含垃圾数据的文件
	garbage := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x03, 0x04}
	if err := os.WriteFile(path, garbage, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	w, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 垃圾文件失败: %v", err)
	}
	defer func() { _ = w.Close() }()

	if len(recs) != 0 {
		t.Errorf("期望 0 条记录，得到 %d", len(recs))
	}
	if w.Size() != 0 {
		t.Errorf("期望偏移量 0，得到 %d", w.Size())
	}

	// 验证文件被截断为 0 字节
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if fi.Size() != 0 {
		t.Errorf("期望文件大小 0（截断后），得到 %d", fi.Size())
	}

	// 验证可以继续追加
	if err := w.AppendWrite([]byte("after_garbage")); err != nil {
		t.Fatalf("垃圾文件恢复后追加失败: %v", err)
	}
}
