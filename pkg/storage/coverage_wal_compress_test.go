package storage

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// --- OpenWAL 错误路径补充测试 ---

// TestOpenWALPartialBodyAfterValid 测试有效记录后跟部分 body 时的恢复
func TestOpenWALPartialBodyAfterValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("data1"))
	_ = w.Sync()
	_ = w.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	partialHeader := make([]byte, walHeaderSize)
	binary.LittleEndian.PutUint32(partialHeader, uint32(walTypeSize+walCRCSize+5))
	modifiedData := make([]byte, len(data)+len(partialHeader)+2)
	copy(modifiedData, data)
	copy(modifiedData[len(data):], partialHeader)
	modifiedData = modifiedData[:len(data)+len(partialHeader)+2]

	if err := os.WriteFile(path, modifiedData, 0644); err != nil {
		t.Fatalf("write modified file: %v", err)
	}

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 1 {
		t.Errorf("expected 1 valid record, got %d", len(recs))
	}
}

// TestOpenWALRecoveryAndContinueAppendV2 测试恢复后继续追加记录
func TestOpenWALRecoveryAndContinueAppendV2(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("record1"))
	_ = w.AppendWrite([]byte("record2"))
	_ = w.Sync()
	_ = w.Close()

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}

	if err := recovered.AppendWrite([]byte("record3")); err != nil {
		t.Fatalf("AppendWrite after recovery failed: %v", err)
	}
	_ = recovered.Sync()

	recovered2, recs2, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("second OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered2.Close() }()

	if len(recs2) != 3 {
		t.Errorf("expected 3 records after second recovery, got %d", len(recs2))
	}
}

// --- Compress/Decompress 错误路径补充测试 ---

// TestCompressColumnNilInputV2 测试 CompressColumn 传入 nil 时的错误
func TestCompressColumnNilInputV2(t *testing.T) {
	err := CompressColumn(nil)
	if err == nil {
		t.Fatal("expected error for nil EncodedColumn, got nil")
	}
}

// TestDecompressColumnNilInputV2 测试 DecompressColumn 传入 nil 时的错误
func TestDecompressColumnNilInputV2(t *testing.T) {
	err := DecompressColumn(nil)
	if err == nil {
		t.Fatal("expected error for nil EncodedColumn, got nil")
	}
}

// TestDecompressInvalidZstdData 测试解压无效 ZSTD 数据时的错误
func TestDecompressInvalidZstdData(t *testing.T) {
	_, err := Decompress([]byte{0xFF, 0xFE, 0xFD, 0xFC})
	if err == nil {
		t.Fatal("expected error for invalid compressed data, got nil")
	}
}

// TestDecompressColumnInvalidCompressedData 测试解压列数据失败时的错误
func TestDecompressColumnInvalidCompressedData(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeInt64,
		RowCount: 1,
		Data:     []byte{0xFF, 0xFE, 0xFD, 0xFC},
	}
	err := DecompressColumn(enc)
	if err == nil {
		t.Fatal("expected error for invalid compressed column data, got nil")
	}
}

// --- GetColumnValue 错误路径补充测试 ---

// TestGetColumnValueColIdxOutOfRange 测试列索引越界时的错误
func TestGetColumnValueColIdxOutOfRange(t *testing.T) {
	seg := &Segment{Columns: []EncodedColumn{}, Keys: []string{}}
	_, err := seg.GetColumnValue(0, 0)
	if err == nil {
		t.Fatal("expected error for column index out of range, got nil")
	}
}

// TestGetColumnValueDecompressFailure 测试 GetColumnValue 解压失败时的错误
func TestGetColumnValueDecompressFailure(t *testing.T) {
	seg := &Segment{
		Columns: []EncodedColumn{
			{Encoding: EncodingPlain, Type: common.TypeInt64, RowCount: 1, Data: []byte{0xFF, 0xFE, 0xFD, 0xFC}},
		},
		Keys: []string{crKey1},
	}
	_, err := seg.GetColumnValue(0, 0)
	if err == nil {
		t.Fatal("expected error for decompress failure in GetColumnValue, got nil")
	}
}

// --- DeserializeColumnBlock 错误路径补充测试 ---

// TestDeserializeColumnBlockDataTooShort 测试反序列化数据过短时的错误
func TestDeserializeColumnBlockDataTooShort(t *testing.T) {
	_, err := DeserializeColumnBlock([]byte{0x01, 0x02, 0x03})
	if err == nil {
		t.Fatal("expected error for too short data, got nil")
	}
}

// TestDeserializeColumnBlockNullsOverflow 测试 nulls 数据超出缓冲区时的错误
func TestDeserializeColumnBlockNullsOverflow(t *testing.T) {
	data := make([]byte, 20)
	pos := 0
	binary.LittleEndian.PutUint32(data[pos:], 0)
	pos += 4
	data[pos] = byte(EncodingPlain)
	pos += 2
	data[pos] = byte(common.TypeInt64)
	pos++
	binary.LittleEndian.PutUint32(data[pos:], 1)
	pos += 4
	binary.LittleEndian.PutUint32(data[pos:], 1000)
	_, err := DeserializeColumnBlock(data)
	if err == nil {
		t.Fatal("expected error for truncated nulls data, got nil")
	}
}

// TestDeserializeColumnBlockDataOverflow 测试列数据超出缓冲区时的错误
func TestDeserializeColumnBlockDataOverflow(t *testing.T) {
	data := make([]byte, 20)
	pos := 0
	binary.LittleEndian.PutUint32(data[pos:], 0)
	pos += 4
	data[pos] = byte(EncodingPlain)
	pos += 2
	data[pos] = byte(common.TypeInt64)
	pos++
	binary.LittleEndian.PutUint32(data[pos:], 1)
	pos += 4
	binary.LittleEndian.PutUint32(data[pos:], 0)
	pos += 4
	binary.LittleEndian.PutUint32(data[pos:], 1000)
	_, err := DeserializeColumnBlock(data)
	if err == nil {
		t.Fatal("expected error for truncated column data, got nil")
	}
}

// TestDeserializeColumnBlockOffsetsOverflow 测试 offsets 数据超出缓冲区时的错误
func TestDeserializeColumnBlockOffsetsOverflow(t *testing.T) {
	data := make([]byte, 40)
	pos := 0
	binary.LittleEndian.PutUint32(data[pos:], 0)
	pos += 4
	data[pos] = byte(EncodingPlain)
	pos += 2
	data[pos] = byte(common.TypeInt64)
	pos++
	binary.LittleEndian.PutUint32(data[pos:], 1)
	pos += 4
	binary.LittleEndian.PutUint32(data[pos:], 0)
	pos += 4
	binary.LittleEndian.PutUint32(data[pos:], 8)
	pos += 4
	pos += 8
	binary.LittleEndian.PutUint32(data[pos:], 1000)
	_, err := DeserializeColumnBlock(data)
	if err == nil {
		t.Fatal("expected error for truncated offsets data, got nil")
	}
}

// TestDeserializeColumnBlockDictOverflow 测试 dict 数据超出缓冲区时的错误
func TestDeserializeColumnBlockDictOverflow(t *testing.T) {
	data := make([]byte, 30)
	pos := 0
	binary.LittleEndian.PutUint32(data[pos:], 0)
	pos += 4
	data[pos] = byte(EncodingDict)
	pos += 2
	data[pos] = byte(common.TypeString)
	pos++
	binary.LittleEndian.PutUint32(data[pos:], 1)
	pos += 4
	binary.LittleEndian.PutUint32(data[pos:], 0)
	pos += 4
	binary.LittleEndian.PutUint32(data[pos:], 1)
	pos += 4
	data[pos] = 0
	pos++
	binary.LittleEndian.PutUint32(data[pos:], 0)
	pos += 4
	binary.LittleEndian.PutUint32(data[pos:], 1000)
	_, err := DeserializeColumnBlock(data)
	if err == nil {
		t.Fatal("expected error for truncated dict data, got nil")
	}
}

// --- EncodeColumn 错误路径补充测试 ---

// TestEncodeColumnEmptyDataV2 测试编码空数据
func TestEncodeColumnEmptyDataV2(t *testing.T) {
	enc, err := EncodeColumn(common.TypeInt64, []int64{}, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enc.RowCount != 0 {
		t.Errorf("expected 0 rowCount, got %d", enc.RowCount)
	}
}

// TestDecodeColumnUnknownEncodingType 测试解码未知编码类型时的错误
func TestDecodeColumnUnknownEncodingType(t *testing.T) {
	enc := &EncodedColumn{Encoding: EncodingType(99), Type: common.TypeInt64, RowCount: 1}
	_, _, err := DecodeColumn(enc)
	if err == nil {
		t.Fatal("expected error for unknown encoding in DecodeColumn, got nil")
	}
}

// --- WAL Truncate 测试 ---

// TestWALTruncateAndContinue 测试 WAL Truncate 正常流程
func TestWALTruncateAndContinue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("data1"))
	_ = w.Sync()

	if err := w.Truncate(); err != nil {
		t.Fatalf("Truncate failed: %v", err)
	}
	if w.Size() != 0 {
		t.Errorf("expected size 0 after truncate, got %d", w.Size())
	}
	if err := w.AppendWrite([]byte("data2")); err != nil {
		t.Fatalf("AppendWrite after truncate failed: %v", err)
	}
	_ = w.Close()
}

// TestWALAppendOversizedPayload 测试追加超大 payload 时的错误
func TestWALAppendOversizedPayload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	largePayload := make([]byte, maxRecordPayload+1)
	err = w.AppendWrite(largePayload)
	if err == nil {
		t.Fatal("expected error for oversized payload, got nil")
	}
}

// --- Serialization 截断数据测试 ---

// TestReadBloomFilterTruncatedData 测试 readBloomFilter 数据截断
func TestReadBloomFilterTruncatedData(t *testing.T) {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, 100)
	_, _, err := readBloomFilter(data, 0)
	if err == nil {
		t.Fatal("expected error for truncated bloom filter data, got nil")
	}
}

// TestReadRawKeysTruncatedData 测试 readRawKeys 数据截断
func TestReadRawKeysTruncatedData(t *testing.T) {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, 100)
	_, _, err := readRawKeys(data, 0)
	if err == nil {
		t.Fatal("expected error for truncated raw keys data, got nil")
	}
}

// TestReadDictStringOverflow 测试 readDict 字符串长度超出数据
func TestReadDictStringOverflow(t *testing.T) {
	data := make([]byte, 12)
	binary.LittleEndian.PutUint32(data[0:], 1)
	binary.LittleEndian.PutUint32(data[4:], 100)
	_, err := readDict(data, 0, &EncodedColumn{})
	if err == nil {
		t.Fatal("expected error for truncated dict string data, got nil")
	}
}

// TestReadDictLengthFieldTruncated 测试 readDict 数据长度字段不足
func TestReadDictLengthFieldTruncated(t *testing.T) {
	_, err := readDict([]byte{0x01, 0x02}, 0, &EncodedColumn{})
	if err == nil {
		t.Fatal("expected error for truncated dict length field, got nil")
	}
}

// TestDeserializeFooterTooShortV2 测试反序列化过短的 footer
func TestDeserializeFooterTooShortV2(t *testing.T) {
	_, err := deserializeFooter([]byte{0x01, 0x02})
	if err == nil {
		t.Fatal("expected error for too short footer, got nil")
	}
}

// TestDeserializeSegmentDataTooShort 测试反序列化过短的 segment 数据
func TestDeserializeSegmentDataTooShort(t *testing.T) {
	_, err := DeserializeSegment([]byte{0x01, 0x02, 0x03})
	if err == nil {
		t.Fatal("expected error for too short segment data, got nil")
	}
}

// TestDeserializeSegmentBadMagic 测试反序列化 magic 不匹配
func TestDeserializeSegmentBadMagic(t *testing.T) {
	data := make([]byte, 22)
	binary.LittleEndian.PutUint32(data[0:], 0xDEADBEEF)
	_, err := DeserializeSegment(data)
	if err == nil {
		t.Fatal("expected error for invalid magic, got nil")
	}
}

// TestReadOffsetsLengthFieldTruncated 测试 readOffsets 长度字段超出数据
func TestReadOffsetsLengthFieldTruncated(t *testing.T) {
	_, err := readOffsets([]byte{0x01, 0x02}, 0, &EncodedColumn{})
	if err == nil {
		t.Fatal("expected error for truncated offsets length, got nil")
	}
}

// TestReadNullsDataOverflow 测试 readNulls 数据超出缓冲区
func TestReadNullsDataOverflow(t *testing.T) {
	data := make([]byte, 8)
	binary.LittleEndian.PutUint32(data[0:], 100)
	_, err := readNulls(data, 0, &EncodedColumn{})
	if err == nil {
		t.Fatal("expected error for truncated nulls data, got nil")
	}
}

// TestReadColumnDataLengthFieldTruncated 测试 readColumnData 长度字段超出数据
func TestReadColumnDataLengthFieldTruncated(t *testing.T) {
	_, err := readColumnData([]byte{0x01, 0x02}, 0, &EncodedColumn{})
	if err == nil {
		t.Fatal("expected error for truncated column data length, got nil")
	}
}
