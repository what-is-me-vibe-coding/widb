package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const (
	walRecFirst  = "first"
	walRecSecond = "second"
	walRec1      = "record1"
	walRec2      = "record2"
	walRec3      = "record3"
	walValidData = "valid_data"
)

// --- Merged from wal_coverage_test.go ---

// TestOpenWALIsNotExistError verifies the os.IsNotExist branch in OpenWAL.
func TestOpenWALIsNotExistError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.wal")

	_, _, err := OpenWAL(path)
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error should wrap os.ErrNotExist, got: %v", err)
	}
}

// TestOpenWALPermissionDenied tests OpenWAL on a read-only file,
// triggering a non-NotExist os.OpenFile error.
func TestOpenWALPermissionDenied(t *testing.T) {
	if runtime.GOOS == skipWindows {
		t.Skip("permission-based test not reliable on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root bypasses file permission checks")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "readonly.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.Close()

	// Make the file read-only so O_RDWR fails with EACCES.
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatalf("Chmod failed: %v", err)
	}
	defer func() { _ = os.Chmod(path, 0644) }() // restore for cleanup

	_, _, err = OpenWAL(path)
	if err == nil {
		t.Fatal("expected error when opening read-only WAL file")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Error("error should not wrap os.ErrNotExist")
	}
}

// TestFindLastCheckpointInvalidPayload tests findLastCheckpoint with
// checkpoint records that have corrupted (non-JSON) payloads.
func TestFindLastCheckpointInvalidPayload(t *testing.T) {
	records := []RawRecord{
		{Type: walTypeCheckpoint, Payload: []byte("not valid json")},
	}
	version, colMeta := findLastCheckpoint(records)
	if version != 0 {
		t.Errorf("expected version 0 for invalid checkpoint, got %d", version)
	}
	if colMeta != nil {
		t.Errorf("expected nil colMeta for invalid checkpoint, got %v", colMeta)
	}
}

// TestFindLastCheckpointMixedValidInvalid tests findLastCheckpoint with
// a mix of valid and invalid checkpoint records, verifying that valid
// records are still processed and invalid ones are skipped.
func TestFindLastCheckpointMixedValidInvalid(t *testing.T) {
	validPayload, err := serializeCheckpointRecord(10, []ColumnMeta{
		{ID: 1, Name: crCol1, Type: common.TypeInt64},
	})
	if err != nil {
		t.Fatalf("serializeCheckpointRecord failed: %v", err)
	}

	higherPayload, err := serializeCheckpointRecord(20, []ColumnMeta{
		{ID: 2, Name: "col2", Type: common.TypeString},
	})
	if err != nil {
		t.Fatalf("serializeCheckpointRecord failed: %v", err)
	}

	records := []RawRecord{
		{Type: walTypeCheckpoint, Payload: []byte("invalid json")},
		{Type: walTypeCheckpoint, Payload: validPayload},
		{Type: walTypeCheckpoint, Payload: []byte("also invalid")},
		{Type: walTypeCheckpoint, Payload: higherPayload},
	}

	version, colMeta := findLastCheckpoint(records)
	if version != 20 {
		t.Errorf("expected version 20, got %d", version)
	}
	if len(colMeta) != 1 {
		t.Fatalf("expected 1 column meta, got %d", len(colMeta))
	}
	if colMeta[0].Name != "col2" {
		t.Errorf("expected column name 'col2', got %q", colMeta[0].Name)
	}
}

// TestFindLastCheckpointOnlyInvalidPayloads tests that findLastCheckpoint
// returns zero values when all checkpoint records have invalid payloads.
func TestFindLastCheckpointOnlyInvalidPayloads(t *testing.T) {
	records := []RawRecord{
		{Type: walTypeCheckpoint, Payload: []byte("bad1")},
		{Type: walTypeCheckpoint, Payload: []byte("bad2")},
	}
	version, colMeta := findLastCheckpoint(records)
	if version != 0 {
		t.Errorf("expected version 0, got %d", version)
	}
	if colMeta != nil {
		t.Errorf("expected nil colMeta, got %v", colMeta)
	}
}

// TestFindLastCheckpointWithNonCheckpointRecords tests that findLastCheckpoint
// ignores non-checkpoint record types.
func TestFindLastCheckpointWithNonCheckpointRecords(t *testing.T) {
	validPayload, err := serializeCheckpointRecord(5, []ColumnMeta{
		{ID: 1, Name: crCol1, Type: common.TypeInt64},
	})
	if err != nil {
		t.Fatalf("serializeCheckpointRecord failed: %v", err)
	}

	records := []RawRecord{
		{Type: walTypeWrite, Payload: []byte("write data")},
		{Type: walTypeCommit, Payload: []byte("commit data")},
		{Type: walTypeCheckpoint, Payload: validPayload},
		{Type: walTypeWrite, Payload: []byte("more write data")},
	}

	version, colMeta := findLastCheckpoint(records)
	if version != 5 {
		t.Errorf("expected version 5, got %d", version)
	}
	if len(colMeta) != 1 {
		t.Fatalf("expected 1 column meta, got %d", len(colMeta))
	}
}

// --- Merged from wal_open_test.go ---

// TestOpenWALTruncateAfterPartialData 测试 WAL 文件末尾有垃圾数据时，OpenWAL 会截断到有效偏移量
func TestOpenWALTruncateAfterPartialData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建 WAL 并写入记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("valid1"))
	_ = w.AppendWrite([]byte("valid2"))
	_ = w.Sync()
	_ = w.Close()

	// 读取文件内容，获取有效数据的长度
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read WAL file: %v", err)
	}
	validSize := len(data)

	// 在末尾追加垃圾数据（模拟崩溃时的部分写入）
	garbage := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x03, 0x04}
	modifiedData := make([]byte, validSize+len(garbage))
	copy(modifiedData, data)
	copy(modifiedData[validSize:], garbage)

	if err := os.WriteFile(path, modifiedData, 0644); err != nil {
		t.Fatalf("write modified file: %v", err)
	}

	// 打开 WAL，验证文件被截断到有效偏移量
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	// 应该恢复 2 条有效记录
	if len(recs) != 2 {
		t.Fatalf("expected 2 valid records, got %d", len(recs))
	}
	if string(recs[0].Payload) != testPayloadValid1 {
		t.Errorf("record 0: expected 'valid1', got %q", string(recs[0].Payload))
	}
	if string(recs[1].Payload) != testPayloadValid2 {
		t.Errorf("record 1: expected 'valid2', got %q", string(recs[1].Payload))
	}

	// 验证文件已被截断到有效偏移量
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat WAL file: %v", err)
	}
	if fileInfo.Size() != int64(validSize) {
		t.Errorf("expected file size %d after truncation, got %d", validSize, fileInfo.Size())
	}

	// 验证恢复后可以继续追加
	if err := recovered.AppendWrite([]byte("after_truncate")); err != nil {
		t.Fatalf("AppendWrite after truncate recovery failed: %v", err)
	}
}

// TestWALMaybeRotateMaxSizeExceeded 测试 WAL 文件超过 maxSize 时正确触发轮转
func TestWALMaybeRotateMaxSizeExceeded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// 设置一个很小的 maxSize，写入多条记录后触发轮转
	w.maxSize = walMetaSize + 10

	// 写入足够多的记录以触发轮转
	for i := 0; i < 5; i++ {
		if err := w.AppendWrite([]byte("trigger rotation data")); err != nil {
			t.Fatalf("AppendWrite #%d failed: %v", i, err)
		}
	}

	// 验证轮转后 offset 被重置（新文件写入了一条或多条记录）
	if w.Size() == 0 {
		t.Error("expected non-zero size after rotation and write")
	}

	// 验证 .prev 文件存在
	_, err = os.Stat(path + ".prev")
	if err != nil {
		t.Fatalf("expected .prev file after rotation: %v", err)
	}

	_ = w.Close()
}

func TestWALAppendBatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	records := []BatchRecord{
		{Type: walTypeWrite, Payload: []byte("batch_record_1")},
		{Type: walTypeWrite, Payload: []byte("batch_record_2")},
		{Type: walTypeWrite, Payload: []byte("batch_record_3")},
	}
	if err := w.AppendBatch(records); err != nil {
		t.Fatalf("AppendBatch failed: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	_ = w.Close()

	// Verify by OpenWAL
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 3 {
		t.Fatalf("expected 3 records, got %d", len(recs))
	}
	for i, rec := range recs {
		expected := fmt.Sprintf("batch_record_%d", i+1)
		if string(rec.Payload) != expected {
			t.Errorf("record %d: expected %q, got %q", i, expected, string(rec.Payload))
		}
	}
}

func TestWALAppendBatchPayloadTooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	largePayload := make([]byte, maxRecordPayload+1)
	records := []BatchRecord{
		{Type: walTypeWrite, Payload: largePayload},
	}
	err = w.AppendBatch(records)
	if err == nil {
		t.Fatal("expected error for oversized payload in AppendBatch")
	}
}

func TestWALAppendBatchWriteError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// Close the WAL file first to trigger write error
	_ = w.Close()

	records := []BatchRecord{
		{Type: walTypeWrite, Payload: []byte("should fail")},
	}
	err = w.AppendBatch(records)
	if err == nil {
		t.Fatal("expected error when writing to closed WAL file")
	}
}

func TestOpenWALFileNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.wal")

	_, _, err := OpenWAL(path)
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
	// The error is wrapped by fmt.Errorf, so we check the error message contains "no such file"
	if !os.IsNotExist(err) {
		// Wrapped errors don't match os.IsNotExist, so check the error chain
		if !strings.Contains(err.Error(), "no such file") && !strings.Contains(err.Error(), "cannot find") {
			t.Errorf("expected file-not-found error, got: %v", err)
		}
	}
}

func TestOpenWALWithCorruptHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.wal")

	// Create a valid WAL with some records first
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("valid_record"))
	_ = w.Sync()
	_ = w.Close()

	// Read the valid file content
	validData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	// Append corrupt data: a header with totalLen too small (less than walTypeSize+walCRCSize)
	// totalLen = 1 (too small, should be at least walTypeSize+walCRCSize = 5)
	corruptData := make([]byte, len(validData), len(validData)+4)
	copy(corruptData, validData)
	corruptData = append(corruptData, []byte{1, 0, 0, 0}...) // totalLen=1 (invalid)
	if err := os.WriteFile(path, corruptData, 0644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	// OpenWAL should return records up to the corruption point
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 1 {
		t.Fatalf("expected 1 valid record before corruption, got %d", len(recs))
	}
	if string(recs[0].Payload) != "valid_record" {
		t.Errorf("record 0: expected 'valid_record', got %q", string(recs[0].Payload))
	}
}

// TestOpenWALNotExist tests that OpenWAL returns an error wrapping os.ErrNotExist
// when the file path is in a non-existent directory.
func TestOpenWALNotExist(t *testing.T) {
	_, _, err := OpenWAL("/nonexistent/directory/test.wal")
	if err == nil {
		t.Fatal("expected error for non-existent path, got nil")
	}
	// The error is wrapped by fmt.Errorf, so os.IsNotExist may not match.
	// Verify the error chain contains a "no such file" indicator.
	if !os.IsNotExist(err) && !strings.Contains(err.Error(), "no such file") {
		t.Errorf("expected file-not-found error, got: %v", err)
	}
}

// TestOpenWALReadOnlyFile tests that OpenWAL fails when the WAL file
// is read-only (cannot be opened with O_RDWR for truncate/seek).
func TestOpenWALReadOnlyFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping: test requires non-root user")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "readonly.wal")

	// Create a WAL file with valid records
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("record1"))
	_ = w.AppendWrite([]byte("record2"))
	_ = w.Sync()
	_ = w.Close()

	// Make the file read-only
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatalf("chmod failed: %v", err)
	}

	// Try to open the WAL - should fail because OpenWAL uses O_RDWR
	_, _, err = OpenWAL(path)
	if err == nil {
		t.Fatal("expected error when opening read-only WAL file, got nil")
	}
	// The error should NOT be os.IsNotExist (the file exists, just not writable)
	if os.IsNotExist(err) {
		t.Errorf("expected non-NotExist error for read-only file, got NotExist: %v", err)
	}
}

// --- Merged from wal_open_edge_test.go ---

// TestOpenWALWithEmptyFile 测试打开空 WAL 文件（0 字节）
func TestOpenWALWithEmptyFile(t *testing.T) {
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
}

// TestOpenWALWithPartialHeader 测试打开只有部分头部（<4 字节）的 WAL 文件
func TestOpenWALWithPartialHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partial_header.wal")

	// 写入只有 3 字节的数据（头部需要 4 字节）
	if err := os.WriteFile(path, []byte{0x01, 0x02, 0x03}, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	w, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 部分头部失败: %v", err)
	}
	defer func() { _ = w.Close() }()

	if len(recs) != 0 {
		t.Errorf("期望 0 条记录，得到 %d", len(recs))
	}
	if w.Size() != 0 {
		t.Errorf("期望偏移量 0，得到 %d", w.Size())
	}
}

// TestOpenWALWithCorruptedHeader 测试打开头部 totalLen 过大的 WAL 文件
func TestOpenWALWithCorruptedHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupted_header.wal")

	// 写入一个头部，totalLen 超过最大限制
	data := make([]byte, walHeaderSize+100)
	binary.LittleEndian.PutUint32(data, uint32(maxRecordPayload+walTypeSize+walCRCSize+1))
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	w, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 损坏头部失败: %v", err)
	}
	defer func() { _ = w.Close() }()

	// 无效记录长度应导致停止回放
	if len(recs) != 0 {
		t.Errorf("期望 0 条记录，得到 %d", len(recs))
	}
}

// TestOpenWALWithValidThenCorruptedHeader 测试有效记录后跟损坏头部的场景
func TestOpenWALWithValidThenCorruptedHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "valid_then_corrupt.wal")

	// 创建 WAL 并写入有效记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte(walValidData))
	_ = w.Sync()
	_ = w.Close()

	// 读取文件内容
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}
	validSize := len(data)

	// 追加一个 totalLen 过大的头部
	header := make([]byte, walHeaderSize)
	binary.LittleEndian.PutUint32(header, uint32(maxRecordPayload+walTypeSize+walCRCSize+100))
	body := make([]byte, 50)
	modifiedData := make([]byte, validSize+len(header)+len(body))
	copy(modifiedData, data)
	copy(modifiedData[validSize:], header)
	copy(modifiedData[validSize+walHeaderSize:], body)

	if err := os.WriteFile(path, modifiedData, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	// 打开 WAL，应恢复有效记录
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 1 {
		t.Fatalf("期望 1 条有效记录，得到 %d", len(recs))
	}
	if string(recs[0].Payload) != walValidData {
		t.Errorf("记录 0: 期望 %q，得到 %q", walValidData, string(recs[0].Payload))
	}
}

// TestOpenWALWithOnlyHeaderNoBody 测试打开只有头部没有 body 的 WAL 文件
func TestOpenWALWithOnlyHeaderNoBody(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "header_only.wal")

	// 写入一个完整头部但 totalLen 指向需要 body 数据的记录
	header := make([]byte, walHeaderSize)
	binary.LittleEndian.PutUint32(header, uint32(walTypeSize+walCRCSize+10)) // totalLen 指向 10 字节 payload
	if err := os.WriteFile(path, header, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	w, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 只有头部失败: %v", err)
	}
	defer func() { _ = w.Close() }()

	// 部分消息体应导致停止回放
	if len(recs) != 0 {
		t.Errorf("期望 0 条记录，得到 %d", len(recs))
	}
}

// TestOpenWALWithValidThenPartialBody 测试有效记录后跟部分 body
func TestOpenWALWithValidThenPartialBody(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "valid_then_partial_body.wal")

	// 创建 WAL 并写入有效记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte(walRecFirst))
	_ = w.AppendWrite([]byte(walRecSecond))
	_ = w.Sync()
	_ = w.Close()

	// 读取文件内容
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}
	validSize := len(data)

	// 追加完整头部 + 部分 body（模拟崩溃时的部分写入）
	header := make([]byte, walHeaderSize)
	binary.LittleEndian.PutUint32(header, uint32(walTypeSize+walCRCSize+20)) // 需要 20 字节 payload
	partialBody := []byte{0x01}                                              // 只有 1 字节 body

	modifiedData := make([]byte, validSize+len(header)+len(partialBody))
	copy(modifiedData, data)
	copy(modifiedData[validSize:], header)
	copy(modifiedData[validSize+walHeaderSize:], partialBody)

	if err := os.WriteFile(path, modifiedData, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 2 {
		t.Fatalf("期望 2 条有效记录，得到 %d", len(recs))
	}
	if string(recs[0].Payload) != walRecFirst {
		t.Errorf("记录 0: 期望 %q，得到 %q", walRecFirst, string(recs[0].Payload))
	}
	if string(recs[1].Payload) != walRecSecond {
		t.Errorf("记录 1: 期望 %q，得到 %q", walRecSecond, string(recs[1].Payload))
	}
}

// TestOpenWALWithZeroLengthHeader 测试打开 totalLen 为 0 的 WAL 文件
func TestOpenWALWithZeroLengthHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zero_len.wal")

	// 写入一个 totalLen=0 的头部
	header := make([]byte, walHeaderSize)
	binary.LittleEndian.PutUint32(header, 0)
	if err := os.WriteFile(path, header, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	w, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = w.Close() }()

	// totalLen=0 小于最小有效长度，应停止回放
	if len(recs) != 0 {
		t.Errorf("期望 0 条记录，得到 %d", len(recs))
	}
}

// TestOpenWALWithMultipleValidAndCorruptRecords 测试混合有效和损坏记录
func TestOpenWALWithMultipleValidAndCorruptRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mixed.wal")

	// 创建 WAL 并写入多条有效记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte(walRec1))
	_ = w.AppendWrite([]byte(walRec2))
	_ = w.AppendWrite([]byte(walRec3))
	_ = w.Sync()
	_ = w.Close()

	// 读取文件
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}

	// 破坏第二条记录的 CRC（第二条记录的 CRC 是其最后 4 字节）
	rec1Size := walHeaderSize + walTypeSize + len(walRec1) + walCRCSize
	rec2CRCStart := rec1Size + walHeaderSize + walTypeSize + len(walRec2)
	// 破坏 CRC 的最后一个字节
	data[rec2CRCStart+3] ^= 0xFF

	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	// 只有第一条记录是有效的（CRC 不匹配后停止回放）
	if len(recs) != 1 {
		t.Fatalf("期望 1 条有效记录，得到 %d", len(recs))
	}
	if string(recs[0].Payload) != walRec1 {
		t.Errorf("记录 0: 期望 %q，得到 %q", walRec1, string(recs[0].Payload))
	}
}

// TestOpenWALRecoveryAndContinueAppend 测试恢复后继续追加记录的完整流程
func TestOpenWALRecoveryAndContinueAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recovery_append.wal")

	// 创建 WAL 并写入记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	for i := 0; i < 5; i++ {
		payload := []byte("data_" + string(rune('a'+i)))
		if err := w.AppendWrite(payload); err != nil {
			t.Fatalf("AppendWrite #%d 失败: %v", i, err)
		}
	}
	_ = w.Sync()
	_ = w.Close()

	// 打开 WAL 进行恢复
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}

	if len(recs) != 5 {
		t.Fatalf("期望 5 条记录，得到 %d", len(recs))
	}

	// 恢复后继续追加
	for i := 0; i < 3; i++ {
		payload := []byte("new_" + string(rune('a'+i)))
		if err := recovered.AppendWrite(payload); err != nil {
			t.Fatalf("追加新记录 #%d 失败: %v", i, err)
		}
	}
	_ = recovered.Sync()
	_ = recovered.Close()

	// 再次打开验证所有记录
	recovered2, recs2, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("第二次 OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered2.Close() }()

	if len(recs2) != 8 {
		t.Fatalf("期望 8 条记录，得到 %d", len(recs2))
	}
}
