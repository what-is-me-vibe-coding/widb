package storage

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

// TestOpenWALWithValidRecords tests OpenWAL successfully replays records from an existing WAL file
func TestOpenWALWithValidRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// Create a WAL and write some records
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	if err := w.AppendWrite([]byte(walRec1)); err != nil {
		t.Fatalf("AppendWrite failed: %v", err)
	}
	if err := w.AppendCommit([]byte("commit1")); err != nil {
		t.Fatalf("AppendCommit failed: %v", err)
	}
	if err := w.AppendCheckpoint([]byte("checkpoint1")); err != nil {
		t.Fatalf("AppendCheckpoint failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Open the WAL and verify records are replayed
	w2, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}
	if records[0].Type != walTypeWrite || string(records[0].Payload) != walRec1 {
		t.Errorf("record 0: type=%d payload=%q", records[0].Type, records[0].Payload)
	}
	if records[1].Type != walTypeCommit || string(records[1].Payload) != "commit1" {
		t.Errorf("record 1: type=%d payload=%q", records[1].Type, records[1].Payload)
	}
	if records[2].Type != walTypeCheckpoint || string(records[2].Payload) != "checkpoint1" {
		t.Errorf("record 2: type=%d payload=%q", records[2].Type, records[2].Payload)
	}

	// Verify the WAL is ready for appending
	if err := w2.AppendWrite([]byte("after_open")); err != nil {
		t.Fatalf("AppendWrite after OpenWAL failed: %v", err)
	}
}

// TestOpenWALWithCorruptedTrailingData tests that OpenWAL truncates corrupted trailing data
func TestOpenWALWithCorruptedTrailingData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// Create a WAL with valid records
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	if err := w.AppendWrite([]byte("valid_record")); err != nil {
		t.Fatalf("AppendWrite failed: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Append garbage data to the end of the file
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("OpenFile for append failed: %v", err)
	}
	_, err = f.Write([]byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x03, 0x04, 0x05})
	if err != nil {
		t.Fatalf("Write garbage failed: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Open the WAL - should recover valid records and truncate garbage
	w2, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if len(records) != 1 {
		t.Fatalf("expected 1 valid record, got %d", len(records))
	}
	if string(records[0].Payload) != "valid_record" {
		t.Errorf("record payload = %q, want %q", string(records[0].Payload), "valid_record")
	}

	// Verify the file was truncated (offset should be at the end of valid data)
	// We can verify by writing new data and checking it works
	if err := w2.AppendWrite([]byte("after_recovery")); err != nil {
		t.Fatalf("AppendWrite after recovery failed: %v", err)
	}
}

// TestOpenWALTruncateError tests the error path when Truncate fails after replay.
// It replaces the WAL file with a symlink to /dev/null, which can be opened with
// O_RDWR but does not support f.Truncate (returns EINVAL on Linux).
func TestOpenWALTruncateError(t *testing.T) {
	if runtime.GOOS != skipWindows && runtime.GOOS != skipNonLinux {
		t.Skip("test relies on /dev/null Truncate behavior on Linux")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// Replace the WAL path with a symlink to /dev/null.
	// /dev/null can be opened with O_RDWR, but f.Truncate returns EINVAL.
	if err := os.Symlink("/dev/null", path); err != nil {
		t.Fatalf("Symlink failed: %v", err)
	}

	// OpenWAL should fail because Truncate on /dev/null returns EINVAL
	_, _, err := OpenWAL(path)
	if err == nil {
		t.Fatal("expected error when Truncate fails on non-regular file")
	}
}

// TestOpenWALTruncateErrorAppendOnly tests the error path when Truncate fails
// due to the append-only file flag. This requires root on a filesystem that
// supports file attributes (e.g. ext4).
func TestOpenWALTruncateErrorAppendOnly(t *testing.T) {
	if runtime.GOOS != skipNonLinux {
		t.Skip("append-only flag test is Linux-specific")
	}
	if os.Getuid() != 0 {
		t.Skip("requires root to set append-only file flag via ioctl")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// Create a WAL with valid records
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	if err := w.AppendWrite([]byte("data")); err != nil {
		t.Fatalf("AppendWrite failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Set the append-only flag on the file using ioctl
	// This allows opening for writing but prevents Truncate
	if err := setFileAppendOnly(path, true); err != nil {
		t.Skipf("filesystem does not support append-only flag: %v", err)
	}
	defer func() { _ = setFileAppendOnly(path, false) }()

	// OpenWAL should fail because Truncate on append-only file returns EPERM
	_, _, err = OpenWAL(path)
	if err == nil {
		t.Fatal("expected error when Truncate fails on append-only file")
	}
}

// TestOpenWALWithPartialHeaderAtEnd tests OpenWAL with a partial header appended
// after valid records, simulating a crash during header write.
func TestOpenWALWithPartialHeaderAtEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// Create a WAL with valid records
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	if err := w.AppendWrite([]byte("data1")); err != nil {
		t.Fatalf("AppendWrite failed: %v", err)
	}
	if err := w.AppendWrite([]byte("data2")); err != nil {
		t.Fatalf("AppendWrite 2 failed: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Append a partial header (3 bytes instead of 4) to simulate crash during write
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("OpenFile for append failed: %v", err)
	}
	_, err = f.Write([]byte{0x01, 0x02, 0x03}) // partial header
	if err != nil {
		t.Fatalf("Write partial header failed: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Open the WAL - should recover valid records and truncate partial header
	w2, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if len(records) != 2 {
		t.Fatalf("expected 2 valid records, got %d", len(records))
	}
}

// TestOpenWALWithCRCMismatch tests that OpenWAL stops replay at CRC mismatch
func TestOpenWALWithCRCMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// Create a WAL with valid records
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	if err := w.AppendWrite([]byte("good_data")); err != nil {
		t.Fatalf("AppendWrite failed: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Read the file, corrupt the CRC of the record, write back
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	// The CRC is the last 4 bytes of the record. Corrupt it.
	// Record format: [4 bytes totalLen][1 byte type][payload][4 bytes CRC]
	// Corrupt the last byte of the CRC
	data[len(data)-1] ^= 0xFF

	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Open the WAL - should return 0 records since the CRC is corrupted
	w2, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if len(records) != 0 {
		t.Fatalf("expected 0 records with corrupted CRC, got %d", len(records))
	}
}

// setFileAppendOnly sets or clears the append-only flag on a file using ioctl.
// This requires root (CAP_LINUX_IMMUTABLE capability) and a filesystem that
// supports file attributes (e.g. ext4, not tmpfs/virtiofs).
func setFileAppendOnly(path string, appendOnly bool) error {
	const (
		fsIocGetflags = 0x80046601
		fsIocSetflags = 0x40046602
		fsAppendFl    = 0x20
	)

	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	var flags int32
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(fsIocGetflags), unsafePtr(&flags))
	if errno != 0 {
		return errno
	}

	if appendOnly {
		flags |= fsAppendFl
	} else {
		flags &^= fsAppendFl
	}

	_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(fsIocSetflags), unsafePtr(&flags))
	if errno != 0 {
		return errno
	}

	return nil
}

// unsafePtr returns the uintptr of an int32 pointer for use with syscall.Syscall.
func unsafePtr(p *int32) uintptr {
	return uintptr(unsafe.Pointer(p))
}

// TestOpenWALReplayCorruptCRC 测试 OpenWAL 在遇到 CRC 损坏记录时，仅返回损坏点之前的有效记录
func TestOpenWALReplayCorruptCRC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建 WAL 并写入多条有效记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	if err := w.AppendWrite([]byte(walRec1)); err != nil {
		t.Fatalf("AppendWrite record1 失败: %v", err)
	}
	if err := w.AppendWrite([]byte("record2")); err != nil {
		t.Fatalf("AppendWrite record2 失败: %v", err)
	}
	if err := w.AppendWrite([]byte("record3")); err != nil {
		t.Fatalf("AppendWrite record3 失败: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync 失败: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	// 读取文件内容，在最后一条记录的 CRC 处进行损坏
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}

	// 损坏最后一条记录的 CRC（最后一个字节取反）
	data[len(data)-1] ^= 0xFF

	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	// OpenWAL 应返回前两条有效记录，第三条因 CRC 损坏被截断
	w2, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if len(records) != 2 {
		t.Fatalf("期望 2 条有效记录，得到 %d", len(records))
	}
	if string(records[0].Payload) != walRec1 {
		t.Errorf("record0: 期望 %q，得到 %q", walRec1, string(records[0].Payload))
	}
	if string(records[1].Payload) != "record2" {
		t.Errorf("record1: 期望 %q，得到 %q", "record2", string(records[1].Payload))
	}
}

// TestOpenWALEmptyFileReplay 测试打开空文件时 OpenWAL 应成功返回 0 条记录并可继续追加
func TestOpenWALEmptyFileReplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建空文件（不使用 CreateWAL，直接创建空文件）
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	// OpenWAL 应成功，返回 0 条记录
	w, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 空文件失败: %v", err)
	}
	defer func() { _ = w.Close() }()

	if len(records) != 0 {
		t.Fatalf("期望 0 条记录，得到 %d", len(records))
	}

	// 验证 WAL 可以继续追加新记录
	if err := w.AppendWrite([]byte("after_empty")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
}

// TestOpenWALPartialHeader 测试文件仅包含不完整头部时 OpenWAL 的行为
func TestOpenWALPartialHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建仅包含 2 字节的文件（不完整的头部，头部需要 4 字节）
	if err := os.WriteFile(path, []byte{0x01, 0x02}, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	// OpenWAL 应优雅处理，返回 0 条记录且不报错
	w, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 部分头部文件失败: %v", err)
	}
	defer func() { _ = w.Close() }()

	if len(records) != 0 {
		t.Fatalf("期望 0 条记录，得到 %d", len(records))
	}

	// 验证 WAL 可以继续追加新记录
	if err := w.AppendWrite([]byte("after_partial")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
}

// TestOpenWALWithOnlyCheckpointRecords 测试仅包含 Checkpoint 记录的 WAL 文件恢复
func TestOpenWALWithOnlyCheckpointRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建 WAL 并仅写入 Checkpoint 记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	if err := w.AppendCheckpoint([]byte("checkpoint_v1")); err != nil {
		t.Fatalf("AppendCheckpoint v1 失败: %v", err)
	}
	if err := w.AppendCheckpoint([]byte("checkpoint_v2")); err != nil {
		t.Fatalf("AppendCheckpoint v2 失败: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	// OpenWAL 应恢复所有 Checkpoint 记录
	w2, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if len(records) != 2 {
		t.Fatalf("期望 2 条记录，得到 %d", len(records))
	}
	if records[0].Type != walTypeCheckpoint || string(records[0].Payload) != "checkpoint_v1" {
		t.Errorf("record 0: type=%d payload=%q, 期望 type=%d payload=%q",
			records[0].Type, string(records[0].Payload), walTypeCheckpoint, "checkpoint_v1")
	}
	if records[1].Type != walTypeCheckpoint || string(records[1].Payload) != "checkpoint_v2" {
		t.Errorf("record 1: type=%d payload=%q, 期望 type=%d payload=%q",
			records[1].Type, string(records[1].Payload), walTypeCheckpoint, "checkpoint_v2")
	}
}
