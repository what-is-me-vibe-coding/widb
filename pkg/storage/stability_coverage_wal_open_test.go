package storage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ===========================================================================
// OpenWAL 错误路径
// ===========================================================================

// TestStabilityWALOpenFileNotExist 测试打开不存在的 WAL 文件返回错误，
// 且错误链中包含 os.ErrNotExist。
func TestStabilityWALOpenFileNotExist(t *testing.T) {
	dir := t.TempDir()
	_, _, err := OpenWAL(filepath.Join(dir, "no_such_file.wal"))
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

// TestStabilityWALOpenTruncatedFile 测试 WAL 文件被截断后重新打开，
// validOffset != fileSize 时 OpenWAL 正确截断文件到有效偏移量。
func TestStabilityWALOpenTruncatedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "truncated.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte("record_one"))
	_ = w.AppendWrite([]byte("record_two"))
	_ = w.Sync()
	_ = w.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}

	// 在有效数据后追加垃圾字节，模拟 validOffset != fileSize
	truncData := make([]byte, len(data)+20)
	copy(truncData, data)
	if err := os.WriteFile(path, truncData, 0644); err != nil {
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
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if fi.Size() != int64(len(data)) {
		t.Errorf("期望文件大小 %d（截断后），得到 %d", len(data), fi.Size())
	}
}

// TestStabilityWALOpenPartialBody 测试 WAL 文件包含部分写入的记录
// （头部完整但消息体不完整），OpenWAL 应截断到有效偏移量。
func TestStabilityWALOpenPartialBody(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partial_body.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte("valid_record"))
	_ = w.Sync()
	_ = w.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}
	validSize := len(data)

	// 追加一个头部完整但消息体不完整的记录
	totalLen := uint32(walTypeSize + 100 + walCRCSize)
	partialRecord := make([]byte, walHeaderSize+10)
	binary.LittleEndian.PutUint32(partialRecord[0:4], totalLen)
	partialRecord[4] = walTypeWrite

	corruptData := make([]byte, len(data), len(data)+len(partialRecord))
	copy(corruptData, data)
	corruptData = append(corruptData, partialRecord...)
	if err := os.WriteFile(path, corruptData, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 1 || string(recs[0].Payload) != "valid_record" {
		t.Fatalf("期望 1 条有效记录 'valid_record'，得到 %d 条", len(recs))
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if fi.Size() != int64(validSize) {
		t.Errorf("期望文件大小 %d（截断后），得到 %d", validSize, fi.Size())
	}
}

// TestStabilityWALOpenCRCMismatch 测试 WAL 文件包含 CRC 不匹配的记录，
// OpenWAL 应截断到 CRC 损坏记录之前。
func TestStabilityWALOpenCRCMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crc_mismatch.wal")

	buf1 := encodeRecord(walTypeWrite, []byte("good_payload"))
	buf2 := encodeRecord(walTypeWrite, []byte("bad_payload"))
	buf2[len(buf2)-1] ^= 0xFF // 损坏 CRC

	fileData := make([]byte, len(buf1), len(buf1)+len(buf2))
	copy(fileData, buf1)
	fileData = append(fileData, buf2...)
	if err := os.WriteFile(path, fileData, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 1 {
		t.Fatalf("期望 1 条有效记录（CRC 损坏前），得到 %d", len(recs))
	}
	if string(recs[0].Payload) != "good_payload" {
		t.Errorf("记录 0: 期望 'good_payload'，得到 %q", string(recs[0].Payload))
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if fi.Size() != int64(len(buf1)) {
		t.Errorf("期望文件大小 %d（截断后），得到 %d", len(buf1), fi.Size())
	}
}

// TestStabilityWALOpenInvalidLength 测试 WAL 文件包含无效长度的记录
// （totalLen < walTypeSize+walCRCSize），OpenWAL 应截断到有效偏移量。
func TestStabilityWALOpenInvalidLength(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invalid_len.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte("ok_data"))
	_ = w.Sync()
	_ = w.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}
	validSize := len(data)

	// 追加一个 totalLen 为 0 的无效头部
	invalidHeader := make([]byte, walHeaderSize+5)
	binary.LittleEndian.PutUint32(invalidHeader, 0)

	corruptData := make([]byte, len(data), len(data)+len(invalidHeader))
	copy(corruptData, data)
	corruptData = append(corruptData, invalidHeader...)
	if err := os.WriteFile(path, corruptData, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 1 || string(recs[0].Payload) != "ok_data" {
		t.Fatalf("期望 1 条有效记录 'ok_data'，得到 %d 条", len(recs))
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if fi.Size() != int64(validSize) {
		t.Errorf("期望文件大小 %d（截断后），得到 %d", validSize, fi.Size())
	}
}

// TestStabilityWALOpenEmptyFile 测试打开空的 WAL 文件，
// 应返回 0 条记录且偏移量为 0。
func TestStabilityWALOpenEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.wal")

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
	if err := w.AppendWrite([]byte("after_empty")); err != nil {
		t.Fatalf("空文件恢复后追加失败: %v", err)
	}
}

// TestStabilityReadBloomFilterExceedsMaxSize 测试 bloom filter 长度超过
// maxBloomSize (16MB) 的损坏数据返回错误。
func TestStabilityReadBloomFilterExceedsMaxSize(t *testing.T) {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, uint32(16<<20+1))
	_, _, err := readBloomFilter(data, 0)
	if err == nil {
		t.Fatal("期望 bloom filter 超过最大长度时返回错误，得到 nil")
	}
	if !strings.Contains(err.Error(), "exceeds max") {
		t.Errorf("期望错误包含 'exceeds max'，得到: %v", err)
	}
}

// TestStabilityReadBloomFilterTruncated 测试 bloom filter 数据截断
// （pos+bloomLen > len(data)）返回错误。
func TestStabilityReadBloomFilterTruncated(t *testing.T) {
	data := make([]byte, 4+10)
	binary.LittleEndian.PutUint32(data[0:4], 100) // bloomLen = 100，但只有 10 字节
	_, _, err := readBloomFilter(data, 0)
	if err == nil {
		t.Fatal("期望 bloom filter 截断时返回错误，得到 nil")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("期望错误包含 'truncated'，得到: %v", err)
	}
}

// TestStabilityReadBloomFilterEmpty 测试空的 bloom filter
// （bloomLen = 0）返回 nil 数据。
func TestStabilityReadBloomFilterEmpty(t *testing.T) {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, 0)
	pos, bloom, err := readBloomFilter(data, 0)
	if err != nil {
		t.Fatalf("期望空 bloom filter 不返回错误，得到: %v", err)
	}
	if bloom != nil {
		t.Errorf("期望 bloom 为 nil，得到 %v", bloom)
	}
	if pos != 4 {
		t.Errorf("期望 pos = 4，得到 %d", pos)
	}
}

// TestStabilityReadBloomFilterLengthFieldTruncated 测试 bloom filter
// 长度字段本身被截断（pos+4 > len(data)）返回错误。
func TestStabilityReadBloomFilterLengthFieldTruncated(t *testing.T) {
	data := make([]byte, 3) // 不足 4 字节读取 bloomLen
	_, _, err := readBloomFilter(data, 0)
	if err == nil {
		t.Fatal("期望 bloom filter 长度字段截断时返回错误，得到 nil")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("期望错误包含 'truncated'，得到: %v", err)
	}
}

// TestStabilityReadBloomFilterValidData 测试正常 bloom filter 数据读取。
func TestStabilityReadBloomFilterValidData(t *testing.T) {
	bloomData := []byte{0x01, 0x02, 0x03, 0x04}
	data := make([]byte, 4+len(bloomData))
	binary.LittleEndian.PutUint32(data[0:4], uint32(len(bloomData)))
	copy(data[4:], bloomData)
	pos, bloom, err := readBloomFilter(data, 0)
	if err != nil {
		t.Fatalf("期望正常 bloom filter 不返回错误，得到: %v", err)
	}
	if !bytes.Equal(bloom, bloomData) {
		t.Errorf("bloom 数据不匹配")
	}
	if pos != 4+len(bloomData) {
		t.Errorf("期望 pos = %d，得到 %d", 4+len(bloomData), pos)
	}
}

// TestStabilityReadRawKeysExceedsMaxSize 测试 rawKeys 长度超过
// maxRawKeysSize (64MB) 的损坏数据返回错误。
func TestStabilityReadRawKeysExceedsMaxSize(t *testing.T) {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, uint32(64<<20+1))
	_, _, err := readRawKeys(data, 0)
	if err == nil {
		t.Fatal("期望 raw keys 超过最大长度时返回错误，得到 nil")
	}
	if !strings.Contains(err.Error(), "exceeds max") {
		t.Errorf("期望错误包含 'exceeds max'，得到: %v", err)
	}
}

// TestStabilityReadRawKeysTruncated 测试 rawKeys 数据截断
// （pos+rawKeysLen > len(data)）返回错误。
func TestStabilityReadRawKeysTruncated(t *testing.T) {
	data := make([]byte, 4+5)
	binary.LittleEndian.PutUint32(data[0:4], 200) // rawKeysLen = 200，但只有 5 字节
	_, _, err := readRawKeys(data, 0)
	if err == nil {
		t.Fatal("期望 raw keys 截断时返回错误，得到 nil")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("期望错误包含 'truncated'，得到: %v", err)
	}
}

// TestStabilityReadRawKeysEmpty 测试空的 rawKeys
// （rawKeysLen = 0）返回 nil 数据。
func TestStabilityReadRawKeysEmpty(t *testing.T) {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, 0)
	pos, rawKeys, err := readRawKeys(data, 0)
	if err != nil {
		t.Fatalf("期望空 raw keys 不返回错误，得到: %v", err)
	}
	if rawKeys != nil {
		t.Errorf("期望 rawKeys 为 nil，得到 %v", rawKeys)
	}
	if pos != 4 {
		t.Errorf("期望 pos = 4，得到 %d", pos)
	}
}

// TestStabilityReadRawKeysLengthFieldTruncated 测试 rawKeys
// 长度字段本身被截断（pos+4 > len(data)）返回 nil 而非错误。
func TestStabilityReadRawKeysLengthFieldTruncated(t *testing.T) {
	data := make([]byte, 3) // 不足 4 字节读取 rawKeysLen
	pos, rawKeys, err := readRawKeys(data, 0)
	if err != nil {
		t.Fatalf("期望 rawKeys 长度字段截断时不返回错误，得到: %v", err)
	}
	if rawKeys != nil {
		t.Errorf("期望 rawKeys 为 nil，得到 %v", rawKeys)
	}
	if pos != 0 {
		t.Errorf("期望 pos = 0，得到 %d", pos)
	}
}

// TestStabilityReadRawKeysValidData 测试正常 rawKeys 数据读取。
func TestStabilityReadRawKeysValidData(t *testing.T) {
	keysData := []byte{0x0A, 0x0B, 0x0C}
	data := make([]byte, 4+len(keysData))
	binary.LittleEndian.PutUint32(data[0:4], uint32(len(keysData)))
	copy(data[4:], keysData)
	pos, rawKeys, err := readRawKeys(data, 0)
	if err != nil {
		t.Fatalf("期望正常 raw keys 不返回错误，得到: %v", err)
	}
	if !bytes.Equal(rawKeys, keysData) {
		t.Errorf("rawKeys 数据不匹配")
	}
	if pos != 4+len(keysData) {
		t.Errorf("期望 pos = %d，得到 %d", 4+len(keysData), pos)
	}
}

// TestStabilityDeserializeFooterTooShort 测试 footer 数据太短
// （< 4 bytes）返回错误。
func TestStabilityDeserializeFooterTooShort(t *testing.T) {
	_, err := deserializeFooter([]byte{0x01, 0x02, 0x03})
	if err == nil {
		t.Fatal("期望 footer 太短时返回错误，得到 nil")
	}
	if !strings.Contains(err.Error(), "too short") {
		t.Errorf("期望错误包含 'too short'，得到: %v", err)
	}
}

// TestStabilityDeserializeFooterTruncatedAtColumnStat 测试 footer 在
// column stat 处截断返回错误。
func TestStabilityDeserializeFooterTruncatedAtColumnStat(t *testing.T) {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, 1) // colCount = 1，但后面没有数据
	_, err := deserializeFooter(data)
	if err == nil {
		t.Fatal("期望 column stat 截断时返回错误，得到 nil")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("期望错误包含 'truncated'，得到: %v", err)
	}
}

// TestStabilityDeserializeFooterTruncatedAtBloomFilter 测试 footer 在
// bloom filter 处截断返回错误。
func TestStabilityDeserializeFooterTruncatedAtBloomFilter(t *testing.T) {
	data := make([]byte, 4+2) // colCount(4) + 2 字节（不足 bloomLen 的 4 字节）
	binary.LittleEndian.PutUint32(data[0:4], 0)
	_, err := deserializeFooter(data)
	if err == nil {
		t.Fatal("期望 bloom filter 截断时返回错误，得到 nil")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("期望错误包含 'truncated'，得到: %v", err)
	}
}

// TestStabilityDeserializeFooterTruncatedAtIndexOffset 测试 footer 在
// index offset 处截断返回错误。
func TestStabilityDeserializeFooterTruncatedAtIndexOffset(t *testing.T) {
	data := make([]byte, 4+4+4+3) // colCount + bloomLen + rawKeysLen + 3 字节
	binary.LittleEndian.PutUint32(data[0:4], 0)
	binary.LittleEndian.PutUint32(data[4:8], 0)
	binary.LittleEndian.PutUint32(data[8:12], 0)
	_, err := deserializeFooter(data)
	if err == nil {
		t.Fatal("期望 index offset 截断时返回错误，得到 nil")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("期望错误包含 'truncated'，得到: %v", err)
	}
}

// TestStabilityDeserializeFooterRoundTrip 测试正常的 footer
// 序列化/反序列化 round-trip。
func TestStabilityDeserializeFooterRoundTrip(t *testing.T) {
	footer := &SegmentFooter{
		ColumnStats: []ColumnStat{
			{ColumnID: 1, Min: []byte("min_val"), Max: []byte("max_val"), NullCount: 5},
			{ColumnID: 2, Min: []byte{}, Max: []byte("x"), NullCount: 0},
		},
		BloomFilter: []byte{0xAA, 0xBB, 0xCC},
		RawKeys:     []byte{0x01, 0x02},
		IndexOffset: 12345,
	}

	deserialized, err := deserializeFooter(serializeFooter(footer))
	if err != nil {
		t.Fatalf("deserializeFooter 失败: %v", err)
	}

	if len(deserialized.ColumnStats) != 2 {
		t.Fatalf("ColumnStats 长度: 期望 2，得到 %d", len(deserialized.ColumnStats))
	}
	for i, want := range []struct {
		id       uint32
		min, max string
		nulls    uint32
	}{{1, "min_val", "max_val", 5}, {2, "", "x", 0}} {
		got := deserialized.ColumnStats[i]
		if got.ColumnID != want.id || string(got.Min) != want.min ||
			string(got.Max) != want.max || got.NullCount != want.nulls {
			t.Errorf("ColumnStats[%d] 不匹配: got id=%d min=%q max=%q nulls=%d",
				i, got.ColumnID, got.Min, got.Max, got.NullCount)
		}
	}
	if !bytes.Equal(deserialized.BloomFilter, footer.BloomFilter) {
		t.Errorf("BloomFilter 不匹配")
	}
	if !bytes.Equal(deserialized.RawKeys, footer.RawKeys) {
		t.Errorf("RawKeys 不匹配")
	}
	if deserialized.IndexOffset != footer.IndexOffset {
		t.Errorf("IndexOffset: 期望 %d，得到 %d", footer.IndexOffset, deserialized.IndexOffset)
	}
}

// TestStabilityDeserializeFooterEmptyRoundTrip 测试空 footer 的
// 序列化/反序列化 round-trip。
func TestStabilityDeserializeFooterEmptyRoundTrip(t *testing.T) {
	footer := &SegmentFooter{}
	deserialized, err := deserializeFooter(serializeFooter(footer))
	if err != nil {
		t.Fatalf("deserializeFooter 失败: %v", err)
	}
	if len(deserialized.ColumnStats) != 0 {
		t.Errorf("ColumnStats 长度: 期望 0，得到 %d", len(deserialized.ColumnStats))
	}
	if deserialized.BloomFilter != nil || deserialized.RawKeys != nil {
		t.Errorf("期望 BloomFilter 和 RawKeys 为 nil")
	}
	if deserialized.IndexOffset != 0 {
		t.Errorf("IndexOffset: 期望 0，得到 %d", deserialized.IndexOffset)
	}
}

// TestStabilityWALMaybeRotateExceedsMaxSize 测试当 WAL 文件超过 maxSize 时
// 触发 rotate，验证 .prev 文件存在。
func TestStabilityWALMaybeRotateExceedsMaxSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rotate.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	w.maxSize = 50

	for i := 0; i < 10; i++ {
		if err := w.AppendWrite([]byte("rotation_test_data")); err != nil {
			t.Fatalf("AppendWrite #%d 失败: %v", i, err)
		}
	}

	if _, err := os.Stat(path + ".prev"); err != nil {
		t.Fatalf("期望 .prev 文件存在: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("期望当前 WAL 文件存在: %v", err)
	}
	_ = w.Close()
}

// TestStabilityWALMaybeRotateContinueAfterRotate 测试 rotate 后
// 继续写入新记录，验证数据完整性。
func TestStabilityWALMaybeRotateContinueAfterRotate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "continue.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte("initial"))
	_ = w.Sync()

	w.maxSize = 1 // 触发轮转
	_ = w.AppendWrite([]byte("trigger"))
	w.maxSize = walDefaultMaxSize

	for i := 0; i < 5; i++ {
		if err := w.AppendWrite([]byte("after_rotate")); err != nil {
			t.Fatalf("轮转后 AppendWrite #%d 失败: %v", i, err)
		}
	}
	_ = w.Sync()
	_ = w.Close()

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) < 2 {
		t.Fatalf("期望至少 2 条记录，得到 %d", len(recs))
	}
	if err := recovered.AppendWrite([]byte("after_reopen")); err != nil {
		t.Fatalf("重新打开后追加失败: %v", err)
	}
}

// TestStabilityWALMaybeRotateNoRotationNeeded 测试 offset < maxSize 时
// maybeRotate 不触发轮转。
func TestStabilityWALMaybeRotateNoRotationNeeded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no_rotate.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	w.maxSize = 1 << 30
	_ = w.AppendWrite([]byte("small_record"))
	_ = w.Sync()
	_ = w.Close()

	if _, err := os.Stat(path + ".prev"); err == nil {
		t.Error("不期望 .prev 文件存在")
	}

	w2, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if len(recs) != 1 || string(recs[0].Payload) != "small_record" {
		t.Fatalf("期望 1 条记录 'small_record'，得到 %d 条", len(recs))
	}
}

func createAndCorruptWAL(t *testing.T, dir, filename string, validPayload []byte, makeInvalidRec func() []byte) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.AppendWrite(validPayload)
	_ = w.Sync()
	_ = w.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}

	invalidRec := makeInvalidRec()
	if err := os.WriteFile(path, append(data, invalidRec...), 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}
	return path
}

// TestStabilityReplayWALInvalidLengths 测试 replayWAL 遇到
// totalLen 过小或过大时停止回放。
func TestStabilityReplayWALInvalidLengths(t *testing.T) {
	dir := t.TempDir()

	// 子测试：totalLen 过小
	t.Run("TooSmall", func(t *testing.T) {
		path := createAndCorruptWAL(t, dir, "small_total.wal", []byte("first"), func() []byte {
			invalidRec := make([]byte, walHeaderSize+1)
			binary.LittleEndian.PutUint32(invalidRec, 1) // totalLen = 1，无效
			invalidRec[4] = 0xEE
			return invalidRec
		})

		recovered, recs, err := OpenWAL(path)
		if err != nil {
			t.Fatalf("OpenWAL 失败: %v", err)
		}
		defer func() { _ = recovered.Close() }()

		if len(recs) != 1 || string(recs[0].Payload) != "first" {
			t.Fatalf("期望 1 条有效记录 'first'，得到 %d 条", len(recs))
		}
	})

	// 子测试：totalLen 过大
	t.Run("TooLarge", func(t *testing.T) {
		path := createAndCorruptWAL(t, dir, "huge_total.wal", []byte("valid"), func() []byte {
			invalidRec := make([]byte, walHeaderSize+10)
			binary.LittleEndian.PutUint32(invalidRec, uint32(maxRecordPayload+walTypeSize+walCRCSize+1))
			return invalidRec
		})

		recovered, recs, err := OpenWAL(path)
		if err != nil {
			t.Fatalf("OpenWAL 失败: %v", err)
		}
		defer func() { _ = recovered.Close() }()

		if len(recs) != 1 {
			t.Fatalf("期望 1 条有效记录，得到 %d", len(recs))
		}
	})
}

// TestStabilityReplayWALMultipleValidThenCorrupt 测试多条有效记录后
// 遇到损坏记录，只恢复有效部分。
func TestStabilityReplayWALMultipleValidThenCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multi_then_corrupt.wal")

	var fileData []byte
	for i := 0; i < 3; i++ {
		rec := encodeRecord(walTypeWrite, []byte("record_"+string(rune('0'+i))))
		fileData = append(fileData, rec...)
	}
	badRec := encodeRecord(walTypeWrite, []byte("corrupted"))
	badRec[len(badRec)-1] ^= 0xFF
	fileData = append(fileData, badRec...)

	if err := os.WriteFile(path, fileData, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 3 {
		t.Fatalf("期望 3 条有效记录，得到 %d", len(recs))
	}
	for i, rec := range recs {
		expected := "record_" + string(rune('0'+i))
		if string(rec.Payload) != expected {
			t.Errorf("记录 %d: 期望 %q，得到 %q", i, expected, string(rec.Payload))
		}
	}
}

// TestStabilityEncodeRecordCRC 测试 encodeRecord 生成的 CRC 可以被
// replayWAL 正确验证。
func TestStabilityEncodeRecordCRC(t *testing.T) {
	payload := []byte("crc_test_payload")
	buf := encodeRecord(walTypeWrite, payload)

	totalLen := binary.LittleEndian.Uint32(buf[0:4])
	body := buf[walHeaderSize : walHeaderSize+int(totalLen)]
	payloadLen := int(totalLen) - walTypeSize - walCRCSize
	storedCRC := binary.LittleEndian.Uint32(body[1+payloadLen:])
	computedCRC := crc32.Checksum(body[:1+payloadLen], crcTable)

	if storedCRC != computedCRC {
		t.Errorf("CRC 不匹配: stored=%d, computed=%d", storedCRC, computedCRC)
	}
	putRecordBuf(buf)
}

// TestStabilityReadColumnStatTruncated 测试 readColumnStat 在
// 截断情况下的错误返回。
func TestStabilityReadColumnStatTruncated(t *testing.T) {
	// ColumnID 字段截断
	_, _, err := readColumnStat([]byte{0x01, 0x02}, 0, 0)
	if err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Errorf("期望 ColumnID 截断时返回 'truncated' 错误，得到: %v", err)
	}

	// Min 长度字段截断
	data := make([]byte, 4+2)
	binary.LittleEndian.PutUint32(data[0:4], 1)
	_, _, err = readColumnStat(data, 0, 0)
	if err == nil {
		t.Error("期望 Min 长度截断时返回错误，得到 nil")
	}
}

// TestStabilityReadStatBytesExceedsMax 测试 readStatBytes 中
// 单列统计值长度超过 maxStatSize (1MB) 的损坏数据。
func TestStabilityReadStatBytesExceedsMax(t *testing.T) {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, uint32(1<<20+1))
	_, _, err := readStatBytes(data, 0, "min", 0)
	if err == nil || !strings.Contains(err.Error(), "exceeds max") {
		t.Errorf("期望 'exceeds max' 错误，得到: %v", err)
	}
}

// TestStabilityReadStatBytesTruncatedAndEmpty 测试 readStatBytes 中
// 数据截断和长度为 0 的情况。
func TestStabilityReadStatBytesTruncatedAndEmpty(t *testing.T) {
	// 数据截断：声称长度 100，但数据不足
	data := make([]byte, 4+5)
	binary.LittleEndian.PutUint32(data[0:4], 100)
	_, _, err := readStatBytes(data, 0, "min", 0)
	if err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Errorf("期望 'truncated' 错误，得到: %v", err)
	}

	// 长度为 0
	data2 := make([]byte, 4)
	binary.LittleEndian.PutUint32(data2, 0)
	pos, result, err := readStatBytes(data2, 0, "min", 0)
	if err != nil {
		t.Fatalf("期望空 stat bytes 不返回错误，得到: %v", err)
	}
	if result != nil || pos != 4 {
		t.Errorf("期望 result=nil, pos=4，得到 result=%v, pos=%d", result, pos)
	}
}
