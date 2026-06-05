package storage

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

const (
	walRecFirst  = "first"
	walRecSecond = "second"
	walRec1      = "record1"
	walRec2      = "record2"
	walRec3      = "record3"
	walValidData = "valid_data"
)

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
