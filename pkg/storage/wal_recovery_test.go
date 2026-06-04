package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWALRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	records := []struct {
		tp      byte
		payload string
	}{
		{walTypeWrite, "row1"},
		{walTypeWrite, "row2"},
		{walTypeCommit, "commit1"},
		{walTypeWrite, "row3"},
		{walTypeCheckpoint, "checkpoint"},
	}

	for _, r := range records {
		if err := w.Append(r.tp, []byte(r.payload)); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}
	_ = w.Sync()
	_ = w.Close()

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != len(records) {
		t.Fatalf("expected %d records, got %d", len(records), len(recs))
	}

	for i, r := range records {
		if recs[i].Type != r.tp {
			t.Errorf("record %d: expected type %d, got %d", i, r.tp, recs[i].Type)
		}
		if string(recs[i].Payload) != r.payload {
			t.Errorf("record %d: expected payload %q, got %q", i, r.payload, string(recs[i].Payload))
		}
	}
}

func TestWALRecoveryEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.Close()

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 0 {
		t.Errorf("expected 0 records, got %d", len(recs))
	}
}

func TestWALRecoveryMissingFile(t *testing.T) {
	_, _, err := OpenWAL("/nonexistent/path/file.wal")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestWALAppendAfterRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("before"))
	_ = w.Sync()
	_ = w.Close()

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}

	if err := recovered.AppendWrite([]byte("after")); err != nil {
		t.Fatalf("AppendWrite after recovery failed: %v", err)
	}
	_ = recovered.Sync()
	_ = recovered.Close()

	recovered2, recs2, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("second OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered2.Close() }()

	if len(recs2) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs2))
	}
}

func TestWALCorruptedCRC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("valid record"))
	_ = w.Sync()
	_ = w.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read WAL file: %v", err)
	}

	data[len(data)-1] ^= 0xFF

	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write corrupted file: %v", err)
	}

	// With crash-resilient replay, corrupted records are skipped
	// and valid records before the corruption are returned.
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL should not fail on corrupted CRC: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	// The corrupted record should be skipped, so 0 valid records
	if len(recs) != 0 {
		t.Errorf("expected 0 valid records after CRC corruption, got %d", len(recs))
	}
}

func TestOpenWALWithExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// Create and write some records
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("record1"))
	_ = w.AppendWrite([]byte("record2"))
	_ = w.AppendCheckpoint([]byte("checkpoint1"))
	_ = w.Sync()
	_ = w.Close()

	// Open existing WAL file
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 3 {
		t.Fatalf("expected 3 records, got %d", len(recs))
	}

	if recs[0].Type != walTypeWrite || string(recs[0].Payload) != "record1" {
		t.Errorf("record 0: unexpected type=%d payload=%q", recs[0].Type, recs[0].Payload)
	}
	if recs[2].Type != walTypeCheckpoint {
		t.Errorf("record 2: expected checkpoint type, got %d", recs[2].Type)
	}

	// Verify the WAL can still be appended to
	if err := recovered.AppendWrite([]byte("record3")); err != nil {
		t.Fatalf("AppendWrite after OpenWAL failed: %v", err)
	}
}

// TestOpenWALNonExistentFile 测试打开不存在的 WAL 文件
func TestOpenWALNonExistentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.wal")

	_, _, err := OpenWAL(path)
	if err == nil {
		t.Fatal("expected error when opening non-existent file")
	}
}

// TestOpenWALEmptyFile 测试打开空 WAL 文件
func TestOpenWALEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.wal")

	// 创建空文件
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create empty file: %v", err)
	}
	_ = f.Close()

	w, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL empty file: %v", err)
	}
	defer func() { _ = w.Close() }()

	if len(recs) != 0 {
		t.Errorf("expected 0 records from empty file, got %d", len(recs))
	}
	if w.Size() != 0 {
		t.Errorf("expected size 0, got %d", w.Size())
	}
}

// TestOpenWALTruncatedRecord 测试打开有截断记录的 WAL 文件（崩溃恢复场景）
func TestOpenWALTruncatedRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "truncated.wal")

	// 创建 WAL 并写入有效记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("valid1"))
	_ = w.AppendWrite([]byte("valid2"))
	_ = w.Sync()
	_ = w.Close()

	// 读取文件内容并在末尾追加部分数据（模拟崩溃时的部分写入）
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read WAL file: %v", err)
	}

	// 追加不完整的头部（只有 2 字节，而头部需要 4 字节）
	truncatedData := make([]byte, len(data), len(data)+2)
	copy(truncatedData, data)
	truncatedData = append(truncatedData, 0x01, 0x02)
	if err := os.WriteFile(path, truncatedData, 0644); err != nil {
		t.Fatalf("write truncated file: %v", err)
	}

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL truncated: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	// 应该恢复 2 条有效记录，截断的部分被丢弃
	if len(recs) != 2 {
		t.Fatalf("expected 2 valid records, got %d", len(recs))
	}
	if string(recs[0].Payload) != "valid1" {
		t.Errorf("record 0: expected 'valid1', got %q", string(recs[0].Payload))
	}
	if string(recs[1].Payload) != "valid2" {
		t.Errorf("record 1: expected 'valid2', got %q", string(recs[1].Payload))
	}
}

// TestOpenWALPartialBody 测试打开有部分消息体的 WAL 文件
func TestOpenWALPartialBody(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partial_body.wal")

	// 创建 WAL 并写入有效记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("valid"))
	_ = w.Sync()
	_ = w.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read WAL file: %v", err)
	}

	// 追加一个完整的头部但消息体不完整
	// 头部 4 字节表示 totalLen=20，但只追加部分 body
	header := make([]byte, 4)
	header[0] = 20              // totalLen = 20（远大于实际追加的数据）
	partialBody := []byte{0x01} // 只有 1 字节 body
	truncatedData := make([]byte, len(data), len(data)+len(header)+len(partialBody))
	copy(truncatedData, data)
	truncatedData = append(truncatedData, header...)
	truncatedData = append(truncatedData, partialBody...)

	if err := os.WriteFile(path, truncatedData, 0644); err != nil {
		t.Fatalf("write partial body file: %v", err)
	}

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL partial body: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	// 应该只恢复有效记录
	if len(recs) != 1 {
		t.Fatalf("expected 1 valid record, got %d", len(recs))
	}
	if string(recs[0].Payload) != "valid" {
		t.Errorf("record 0: expected 'valid', got %q", string(recs[0].Payload))
	}
}

// TestOpenWALInvalidRecordLength 测试打开有无效记录长度的 WAL 文件
func TestOpenWALInvalidRecordLength(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invalid_len.wal")

	// 创建 WAL 并写入有效记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("valid"))
	_ = w.Sync()
	_ = w.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read WAL file: %v", err)
	}

	// 追加一个头部，其 totalLen 太小（小于最小有效长度）
	header := make([]byte, 4)
	header[0] = 1 // totalLen = 1，小于 walTypeSize + walCRCSize = 5
	// 再追加一些 body 数据
	body := make([]byte, 10)
	invalidData := make([]byte, len(data), len(data)+len(header)+len(body))
	copy(invalidData, data)
	invalidData = append(invalidData, header...)
	invalidData = append(invalidData, body...)

	if err := os.WriteFile(path, invalidData, 0644); err != nil {
		t.Fatalf("write invalid length file: %v", err)
	}

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL invalid length: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	// 应该只恢复有效记录
	if len(recs) != 1 {
		t.Fatalf("expected 1 valid record, got %d", len(recs))
	}
}

// TestOpenWALCanAppendAfterRecovery 测试恢复后可以继续追加记录
func TestOpenWALCanAppendAfterRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "append_after.wal")

	// 创建并写入记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("before"))
	_ = w.Sync()
	_ = w.Close()

	// 恢复
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}

	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}

	// 恢复后追加新记录
	if err := recovered.AppendWrite([]byte("after")); err != nil {
		t.Fatalf("AppendWrite after recovery: %v", err)
	}
	_ = recovered.Sync()
	_ = recovered.Close()

	// 再次恢复验证
	recovered2, recs2, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("second OpenWAL: %v", err)
	}
	defer func() { _ = recovered2.Close() }()

	if len(recs2) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs2))
	}
}

// TestOpenWALMultipleRecordTypes 测试打开包含多种记录类型的 WAL 文件
func TestOpenWALMultipleRecordTypes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multi_type.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// 写入不同类型的记录
	if err := w.AppendWrite([]byte("write_data")); err != nil {
		t.Fatalf("AppendWrite: %v", err)
	}
	if err := w.AppendCommit([]byte("commit_data")); err != nil {
		t.Fatalf("AppendCommit: %v", err)
	}
	if err := w.AppendCheckpoint([]byte("checkpoint_data")); err != nil {
		t.Fatalf("AppendCheckpoint: %v", err)
	}
	_ = w.Sync()
	_ = w.Close()

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 3 {
		t.Fatalf("expected 3 records, got %d", len(recs))
	}

	// 验证记录类型
	if recs[0].Type != walTypeWrite {
		t.Errorf("record 0: expected type %d, got %d", walTypeWrite, recs[0].Type)
	}
	if recs[1].Type != walTypeCommit {
		t.Errorf("record 1: expected type %d, got %d", walTypeCommit, recs[1].Type)
	}
	if recs[2].Type != walTypeCheckpoint {
		t.Errorf("record 2: expected type %d, got %d", walTypeCheckpoint, recs[2].Type)
	}
}
