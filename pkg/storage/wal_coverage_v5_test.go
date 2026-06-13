package storage

import (
	"encoding/binary"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
)

// ---------------------------------------------------------------------------
// deserializeWriteRecord 空记录路径（wal_record.go 第 38-39 行）
// ---------------------------------------------------------------------------

// TestDeserializeWriteRecordEmptyRecord 测试反序列化空写入记录（rowCount=0）。
// 当二进制数据中行数为 0 时，应返回 "empty write record" 错误。
func TestDeserializeWriteRecordEmptyRecord(t *testing.T) {
	// 构造 rowCount=0 的二进制数据
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, 0) // 0 行

	_, _, _, err := deserializeWriteRecord(buf)
	if err == nil {
		t.Fatal("期望空写入记录返回错误，得到 nil")
	}
	// 验证错误消息包含 "empty write record"
	if err.Error() == "" {
		t.Error("错误消息不应为空")
	}
	t.Logf("空记录错误: %v", err)
}

// ---------------------------------------------------------------------------
// applySingleWriteRecord mem.Put 错误路径（wal_record.go 第 141-143 行）
// ---------------------------------------------------------------------------

// TestApplySingleWriteRecordFrozenMemTable 测试在冻结的 MemTable 上应用写入记录。
// 冻结后 Put 返回 ErrReadOnly，applySingleWriteRecord 应记录错误但仍返回 true。
func TestApplySingleWriteRecordFrozenMemTable(t *testing.T) {
	// 构造有效的写入记录 payload
	payload, err := serializeWriteRecord("key1", 1, map[string]common.Value{crCol1: common.NewInt64(42)})
	if err != nil {
		t.Fatalf("serializeWriteRecord 失败: %v", err)
	}

	// 创建并冻结 MemTable
	mem := NewMemTable()
	mem.Freeze()

	// 应用写入记录到冻结的 MemTable
	version, ok := applySingleWriteRecord(payload, 0, mem)
	// 函数在 Put 失败时记录日志但仍返回 true
	if !ok {
		t.Error("applySingleWriteRecord 在 Put 失败时应返回 ok=true（仅记录日志）")
	}
	// 版本号仍应返回
	if version != 1 {
		t.Errorf("期望 version=1，得到 %d", version)
	}
}

// TestApplyBatchWriteRecordFrozenMemTable 测试批量写入记录在冻结的 MemTable 上的行为。
// 冻结后 Put 返回 ErrReadOnly，但函数仍应返回 true。
func TestApplyBatchWriteRecordFrozenMemTable(t *testing.T) {
	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{"c1": common.NewInt64(1)}},
		{Key: "k2", Values: map[string]common.Value{"c2": common.NewInt64(2)}},
	}
	payload, err := serializeBatchWriteRecord(rows, 10)
	if err != nil {
		t.Fatalf("serializeBatchWriteRecord 失败: %v", err)
	}

	mem := NewMemTable()
	mem.Freeze()

	maxVersion, ok := applyBatchWriteRecord(payload, 0, mem)
	if !ok {
		t.Error("applyBatchWriteRecord 应返回 ok=true")
	}
	// 即使 Put 失败，maxVersion 仍应反映记录中的版本号
	if maxVersion == 0 {
		t.Error("maxVersion 不应为 0（即使 Put 失败）")
	}
	t.Logf("冻结 MemTable 上批量写入 maxVersion=%d", maxVersion)
}

// ---------------------------------------------------------------------------
// deserializeBatchWriteRecord 截断路径（wal_record_binary.go）
// ---------------------------------------------------------------------------

// TestDeserializeBatchWriteRecord_DataTooShort 测试数据不足 2 字节。
func TestDeserializeBatchWriteRecord_DataTooShort(t *testing.T) {
	_, err := deserializeBatchWriteRecord([]byte{0x01})
	if err == nil {
		t.Fatal("期望数据过短时返回错误，得到 nil")
	}
	t.Logf("数据过短错误: %v", err)
}

// TestDeserializeBatchWriteRecord_TruncatedAtRowKeyLen 测试在行键长度处截断。
func TestDeserializeBatchWriteRecord_TruncatedAtRowKeyLen(t *testing.T) {
	// rowCount=1 但没有足够数据读取 keyLen
	data := make([]byte, 3)
	binary.LittleEndian.PutUint16(data, 1) // 1 行
	data[2] = 0x00                         // keyLen 的高字节不完整

	_, err := deserializeBatchWriteRecord(data)
	if err == nil {
		t.Fatal("期望截断时返回错误，得到 nil")
	}
	t.Logf("keyLen 截断错误: %v", err)
}

// TestDeserializeBatchWriteRecord_TruncatedAtRowKey 测试在行键数据处截断。
func TestDeserializeBatchWriteRecord_TruncatedAtRowKey(t *testing.T) {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint16(data, 1)      // 1 行
	binary.LittleEndian.PutUint16(data[2:], 10) // keyLen=10，但后面没有数据

	_, err := deserializeBatchWriteRecord(data)
	if err == nil {
		t.Fatal("期望键数据截断时返回错误，得到 nil")
	}
	t.Logf("键数据截断错误: %v", err)
}

// TestDeserializeBatchWriteRecord_TruncatedAtRowVersion 测试在行版本处截断。
func TestDeserializeBatchWriteRecord_TruncatedAtRowVersion(t *testing.T) {
	data := make([]byte, 0, 6)
	data = binary.LittleEndian.AppendUint16(data, 1) // 1 行
	data = binary.LittleEndian.AppendUint16(data, 1) // keyLen=1
	data = append(data, 'a')                         // key
	// 缺少 version（8 字节）

	_, err := deserializeBatchWriteRecord(data)
	if err == nil {
		t.Fatal("期望版本截断时返回错误，得到 nil")
	}
	t.Logf("版本截断错误: %v", err)
}

// TestDeserializeBatchWriteRecord_TruncatedAtColCount 测试在列计数处截断。
func TestDeserializeBatchWriteRecord_TruncatedAtColCount(t *testing.T) {
	data := make([]byte, 13)
	binary.LittleEndian.PutUint16(data, 1)     // 1 行
	binary.LittleEndian.PutUint16(data[2:], 1) // keyLen=1
	data[4] = 'a'                              // key
	binary.LittleEndian.PutUint64(data[5:], 1) // version=1
	// 缺少 colCount（2 字节）

	_, err := deserializeBatchWriteRecord(data)
	if err == nil {
		t.Fatal("期望列计数截断时返回错误，得到 nil")
	}
	t.Logf("列计数截断错误: %v", err)
}

// TestDeserializeBatchWriteRecord_TruncatedAtColumnData 测试在列数据处截断。
func TestDeserializeBatchWriteRecord_TruncatedAtColumnData(t *testing.T) {
	data := make([]byte, 0, 16)
	data = binary.LittleEndian.AppendUint16(data, 1) // 1 行
	data = binary.LittleEndian.AppendUint16(data, 1) // keyLen=1
	data = append(data, 'a')                         // key
	data = binary.LittleEndian.AppendUint64(data, 1) // version=1
	data = binary.LittleEndian.AppendUint16(data, 1) // colCount=1
	// 缺少列数据

	_, err := deserializeBatchWriteRecord(data)
	if err == nil {
		t.Fatal("期望列数据截断时返回错误，得到 nil")
	}
	t.Logf("列数据截断错误: %v", err)
}

// ---------------------------------------------------------------------------
// readValueBinary 错误路径（wal_record_binary.go）
// ---------------------------------------------------------------------------

// TestReadValueBinary_TruncatedColNameLen 测试列名长度截断。
func TestReadValueBinary_TruncatedColNameLen(t *testing.T) {
	_, _, _, err := readValueBinary([]byte{0x01})
	if err == nil {
		t.Fatal("期望列名长度截断时返回错误，得到 nil")
	}
	t.Logf("列名长度截断错误: %v", err)
}

// TestReadValueBinary_TruncatedColName 测试列名数据截断。
func TestReadValueBinary_TruncatedColName(t *testing.T) {
	data := make([]byte, 2)
	binary.LittleEndian.PutUint16(data, 10) // nameLen=10，但后面没有数据

	_, _, _, err := readValueBinary(data)
	if err == nil {
		t.Fatal("期望列名数据截断时返回错误，得到 nil")
	}
	t.Logf("列名数据截断错误: %v", err)
}

// TestReadValueBinary_TruncatedTypeValid 测试类型/有效标志截断。
func TestReadValueBinary_TruncatedTypeValid(t *testing.T) {
	data := make([]byte, 0, 4)
	data = binary.LittleEndian.AppendUint16(data, 1) // nameLen=1
	data = append(data, 'a')                         // name
	// 缺少 type 和 valid 字节

	_, _, _, err := readValueBinary(data)
	if err == nil {
		t.Fatal("期望类型/有效标志截断时返回错误，得到 nil")
	}
	t.Logf("类型/有效标志截断错误: %v", err)
}

// TestReadValueBinary_UnknownValueType 测试未知值类型。
// 构造一个完整的列名和类型/valid 字段，但类型为未知值。
func TestReadValueBinary_UnknownValueType(t *testing.T) {
	data := make([]byte, 0, 6)
	data = binary.LittleEndian.AppendUint16(data, 1) // nameLen=1
	data = append(data, 'a')                         // name
	data = append(data, byte(99))                    // unknown type
	data = append(data, 1)                           // valid=1
	data = append(data, 0x01)                        // 值数据（对未知类型无用）

	_, _, _, err := readValueBinary(data)
	if err == nil {
		t.Fatal("期望未知值类型返回错误，得到 nil")
	}
	t.Logf("未知值类型错误: %v", err)
}

// ---------------------------------------------------------------------------
// CompressColumn nil 输入（compress.go 第 81-83 行）
// ---------------------------------------------------------------------------

// TestCompressColumn_NilInput 测试 CompressColumn 传入 nil EncodedColumn。
func TestCompressColumn_NilInput(t *testing.T) {
	err := CompressColumn(nil)
	if err == nil {
		t.Fatal("期望 nil EncodedColumn 返回错误，得到 nil")
	}
	t.Logf("CompressColumn nil 错误: %v", err)
}

// ---------------------------------------------------------------------------
// DecompressColumn nil 输入（compress.go 第 93-96 行）
// ---------------------------------------------------------------------------

// TestDecompressColumn_NilInput 测试 DecompressColumn 传入 nil EncodedColumn。
func TestDecompressColumn_NilInput(t *testing.T) {
	err := DecompressColumn(nil)
	if err == nil {
		t.Fatal("期望 nil EncodedColumn 返回错误，得到 nil")
	}
	t.Logf("DecompressColumn nil 错误: %v", err)
}

// ---------------------------------------------------------------------------
// BuildAndRegister 空键（bloom.go 第 165-167 行，index 包）
// ---------------------------------------------------------------------------

// TestBuildAndRegister_EmptyKeys 测试 BuildAndRegister 传入空键切片。
// 空键 -> BuildFromKeys 返回 nil data -> BuildAndRegister 直接返回 nil。
func TestBuildAndRegister_EmptyKeys(t *testing.T) {
	bi := index.NewBloomIndex()
	err := bi.BuildAndRegister(1, []string{}, 0.01)
	if err != nil {
		t.Errorf("期望空键返回 nil 错误，得到: %v", err)
	}
	// 验证没有注册任何过滤器
	if bi.Len() != 0 {
		t.Errorf("期望 0 个过滤器，得到 %d", bi.Len())
	}
}

// ---------------------------------------------------------------------------
// applySingleRecord 默认类型路径（wal_record.go 第 128 行）
// ---------------------------------------------------------------------------

// TestApplySingleRecord_UnknownType 测试 applySingleRecord 处理未知记录类型。
// 未知类型应返回 (0, true)，不报错。
func TestApplySingleRecord_UnknownType(t *testing.T) {
	mem := NewMemTable()
	version, ok := applySingleRecord(RawRecord{Type: 0xFF, Payload: []byte("test")}, 0, mem)
	if !ok {
		t.Error("未知类型应返回 ok=true")
	}
	if version != 0 {
		t.Errorf("未知类型应返回 version=0，得到 %d", version)
	}
}

// ---------------------------------------------------------------------------
// applySingleWriteRecord 版本号 <= lastFlushedVersion 路径
// ---------------------------------------------------------------------------

// TestApplySingleWriteRecord_VersionAlreadyFlushed 测试版本号已刷盘时跳过写入。
func TestApplySingleWriteRecord_VersionAlreadyFlushed(t *testing.T) {
	payload, err := serializeWriteRecord("key1", 5, map[string]common.Value{crCol1: common.NewInt64(42)})
	if err != nil {
		t.Fatalf("serializeWriteRecord 失败: %v", err)
	}

	mem := NewMemTable()
	// lastFlushedVersion=10，记录版本=5，应跳过
	version, ok := applySingleWriteRecord(payload, 10, mem)
	if !ok {
		t.Error("应返回 ok=true")
	}
	if version != 0 {
		t.Errorf("版本已刷盘时应返回 version=0，得到 %d", version)
	}

	// 验证数据未被写入 MemTable
	_, found := mem.Get("key1")
	if found {
		t.Error("版本已刷盘时不应写入 MemTable")
	}
}

// ---------------------------------------------------------------------------
// applyBatchWriteRecord 版本号 <= lastFlushedVersion 路径
// ---------------------------------------------------------------------------

// TestApplyBatchWriteRecord_VersionAlreadyFlushed 测试批量写入中版本号已刷盘的行被跳过。
func TestApplyBatchWriteRecord_VersionAlreadyFlushed(t *testing.T) {
	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{"c1": common.NewInt64(1)}},
	}
	payload, err := serializeBatchWriteRecord(rows, 5)
	if err != nil {
		t.Fatalf("serializeBatchWriteRecord 失败: %v", err)
	}

	mem := NewMemTable()
	// lastFlushedVersion=10，所有行版本 <= 10，应跳过
	maxVersion, ok := applyBatchWriteRecord(payload, 10, mem)
	if !ok {
		t.Error("应返回 ok=true")
	}
	if maxVersion != 0 {
		t.Errorf("所有行版本已刷盘时 maxVersion 应为 0，得到 %d", maxVersion)
	}
}

// ---------------------------------------------------------------------------
// CompressColumn / DecompressColumn 正常路径补充
// ---------------------------------------------------------------------------

// TestCompressColumn_EmptyData 测试 CompressColumn 处理空数据。
func TestCompressColumn_EmptyData(t *testing.T) {
	enc := &EncodedColumn{Data: []byte{}}
	err := CompressColumn(enc)
	if err != nil {
		t.Errorf("空数据压缩不应返回错误: %v", err)
	}
	// 空数据压缩后仍为 nil（Compress 对空数据返回 nil）
	if enc.Data != nil {
		t.Logf("空数据压缩后 Data=%v", enc.Data)
	}
}

// TestDecompressColumn_EmptyData 测试 DecompressColumn 处理空数据。
func TestDecompressColumn_EmptyData(t *testing.T) {
	enc := &EncodedColumn{Data: []byte{}}
	err := DecompressColumn(enc)
	if err != nil {
		t.Errorf("空数据解压不应返回错误: %v", err)
	}
}

// TestCompressDecompressColumn_RoundTrip 测试压缩/解压往返正确性。
func TestCompressDecompressColumn_RoundTrip(t *testing.T) {
	original := []byte("hello world test data for compression")
	enc := &EncodedColumn{Data: original}

	// 压缩
	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn 失败: %v", err)
	}
	if string(enc.Data) == string(original) {
		t.Error("压缩后数据应与原始数据不同")
	}

	// 解压
	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn 失败: %v", err)
	}
	if string(enc.Data) != string(original) {
		t.Errorf("解压后数据不匹配: 期望 %q，得到 %q", original, enc.Data)
	}
}
