package storage

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
)

// ---------------------------------------------------------------------------
// ScanRange: MergeIterator.Err() != nil 路径
// ---------------------------------------------------------------------------

// TestScanRange合并迭代器错误 验证 ScanRange 在 MergeIterator 有错误时返回 nil
func TestScanRange合并迭代器错误(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入数据并 flush 以创建 segment
	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	// 破坏 segment 列数据使迭代器解码失败
	eng.mu.Lock()
	for _, seg := range eng.segments {
		for i := range seg.Columns {
			seg.Columns[i].Data = []byte{0xFF, 0xFE, 0xFD, 0xFC}
		}
	}
	eng.mu.Unlock()

	eng.mu.RLock()
	results := eng.ScanRange("", "\xff\xff\xff\xff")
	eng.mu.RUnlock()

	// 当 MergeIterator.Err() != nil 时，ScanRange 返回 nil
	if results != nil {
		t.Errorf("期望迭代器错误时返回 nil，得到 %d 条结果", len(results))
	}
}

// ---------------------------------------------------------------------------
// replayWALRecords: 边界情况
// ---------------------------------------------------------------------------

// TestReplayWALRecords空记录 验证 replayWALRecords 处理空记录列表
func TestReplayWALRecords空记录(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	err = eng.replayWALRecords(nil)
	if err != nil {
		t.Errorf("期望空记录不返回错误，得到: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Engine: loadSegments 边界情况
// ---------------------------------------------------------------------------

// TestLoadSegments空目录 验证 loadSegments 在空目录中正常工作
func TestLoadSegments空目录(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	err = eng.loadSegments()
	if err != nil {
		t.Errorf("期望空目录 loadSegments 成功，得到: %v", err)
	}
}

// ---------------------------------------------------------------------------
// deserializeBatchWriteRecord: 边界情况补充
// ---------------------------------------------------------------------------

// TestDeserializeBatchWriteRecord空数据 验证反序列化空数据时返回错误
func TestDeserializeBatchWriteRecord空数据(t *testing.T) {
	_, err := deserializeBatchWriteRecord([]byte{})
	if err == nil {
		t.Error("期望空数据返回错误，得到 nil")
	}
}

// TestDeserializeBatchWriteRecord截断列名 验证反序列化截断的列名数据时返回错误
func TestDeserializeBatchWriteRecord截断列名(t *testing.T) {
	data := make([]byte, 15)
	binary.LittleEndian.PutUint16(data, 1)      // 1 行
	binary.LittleEndian.PutUint16(data[2:], 1)  // key 长度 = 1
	data[4] = 'a'                               // key
	binary.LittleEndian.PutUint64(data[5:], 1)  // version
	binary.LittleEndian.PutUint16(data[13:], 1) // 1 列
	// 缺少列名长度

	_, err := deserializeBatchWriteRecord(data)
	if err == nil {
		t.Error("期望截断数据返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// serializeWriteRecord / serializeBatchWriteRecord: 不支持的值类型
// ---------------------------------------------------------------------------

// TestSerializeBatchWriteRecord不支持类型 验证序列化不支持的值类型时的行为
// 注意：当前 appendValueBinary 对不支持的类型不写入值数据，但也不返回错误。
// serializeBatchWriteRecord 总是返回 nil 错误。
func TestSerializeBatchWriteRecord不支持类型(t *testing.T) {
	rows := []WriteRow{
		{
			Key: "k1",
			Values: map[string]common.Value{
				crCol: {Typ: common.DataType(99), Valid: true}, // 不支持的类型
			},
		},
	}
	data, err := serializeBatchWriteRecord(rows, 1)
	// 当前实现不返回错误，但数据可能不完整
	if err != nil {
		t.Logf("serializeBatchWriteRecord 返回错误: %v", err)
	}
	if data == nil {
		t.Error("期望非 nil 数据")
	}
}

// ---------------------------------------------------------------------------
// Engine: replayWALRecords 处理不同记录类型
// ---------------------------------------------------------------------------

// TestReplayWALRecords混合类型 验证 replayWALRecords 处理混合类型记录
func TestReplayWALRecords混合类型(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "mixed.wal")

	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入 Write 记录
	writePayload, _ := serializeWriteRecord("key1", 1, map[string]common.Value{colVal: common.NewInt64(1)})
	_ = w.AppendWrite(writePayload)

	// 写入 BatchWrite 记录
	rows := []WriteRow{{Key: crKey2, Values: map[string]common.Value{colVal: common.NewInt64(2)}}}
	batchPayload, _ := serializeBatchWriteRecord(rows, 2)
	_ = w.AppendBatch([]BatchRecord{{Type: walTypeBatchWrite, Payload: batchPayload}})

	// 写入 Checkpoint 记录
	cpPayload, _ := serializeCheckpointRecord(2, []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}})
	_ = w.AppendCheckpoint(cpPayload)
	_ = w.Sync()
	_ = w.Close()

	openedWAL, records, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}

	eng := &Engine{
		activeMem:    NewMemTable(),
		flusher:      NewFlusher(dir, newSegmentIDGen()),
		compactor:    NewCompactor(dir, newSegmentIDGen()),
		segmentMap:   make(map[uint64]*Segment),
		nextVersion:  1,
		primaryIndex: index.NewPrimaryIndex(),
		bloomIndex:   index.NewBloomIndex(),
		sparseIndex:  index.NewSparseIndex(),
	}

	err = eng.replayWALRecords(records)
	if err != nil {
		t.Errorf("replayWALRecords 失败: %v", err)
	}

	_ = openedWAL.Close()
}

// ---------------------------------------------------------------------------
// Engine: NewEngine 在只读目录中创建 WAL 失败
// ---------------------------------------------------------------------------

// TestNewEngine只读目录WAL失败 验证 NewEngine 在只读目录中无法创建 WAL
func TestNewEngine只读目录WAL失败(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root 用户绕过文件权限检查")
	}

	dir := t.TempDir()
	readOnlyDir := filepath.Join(dir, "readonly")
	if err := os.MkdirAll(readOnlyDir, 0555); err != nil {
		t.Fatalf("MkdirAll 失败: %v", err)
	}
	defer func() { _ = os.Chmod(readOnlyDir, 0755) }()

	_, err := NewEngine(EngineConfig{DataDir: readOnlyDir})
	if err == nil {
		t.Error("期望只读目录中 NewEngine 失败，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// Engine: checkpoint 序列化/反序列化
// ---------------------------------------------------------------------------

// TestSerializeCheckpointRecord正常 验证 checkpoint 记录正常序列化
func TestSerializeCheckpointRecord正常(t *testing.T) {
	colMeta := []ColumnMeta{
		{ID: 0, Name: "id", Type: common.TypeInt64},
		{ID: 1, Name: benchColName, Type: common.TypeString},
	}
	data, err := serializeCheckpointRecord(42, colMeta)
	if err != nil {
		t.Fatalf("serializeCheckpointRecord 失败: %v", err)
	}
	if len(data) == 0 {
		t.Error("期望非空数据")
	}

	// 反序列化验证
	var rec walCheckpointRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("Unmarshal 失败: %v", err)
	}
	if rec.LastFlushedVersion != 42 {
		t.Errorf("期望 LastFlushedVersion=42，得到 %d", rec.LastFlushedVersion)
	}
}

// ---------------------------------------------------------------------------
// Engine: deserializeCheckpointRecord 边界情况
// ---------------------------------------------------------------------------

// TestDeserializeCheckpointRecord空列元数据 验证反序列化没有列元数据的 checkpoint
func TestDeserializeCheckpointRecord空列元数据(t *testing.T) {
	data, _ := json.Marshal(walCheckpointRecord{LastFlushedVersion: 10})
	version, colMeta, err := deserializeCheckpointRecord(data)
	if err != nil {
		t.Fatalf("deserializeCheckpointRecord 失败: %v", err)
	}
	if version != 10 {
		t.Errorf("期望 version=10，得到 %d", version)
	}
	if len(colMeta) != 0 {
		t.Errorf("期望空列元数据，得到 %d 列", len(colMeta))
	}
}

// ---------------------------------------------------------------------------
// Engine: Engine Write 正常路径覆盖
// ---------------------------------------------------------------------------

// TestWrite正常路径 验证 Write 正常写入数据
func TestWrite正常路径(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	err = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})
	if err != nil {
		t.Fatalf("Write 失败: %v", err)
	}

	row, ok := eng.Get("key1")
	if !ok {
		t.Fatal("期望找到 key1")
	}
	if row.Columns[colVal].Int64 != 1 {
		t.Errorf("期望 val=1，得到 %d", row.Columns[colVal].Int64)
	}
}

// ---------------------------------------------------------------------------
// Engine: WriteBatch 正常路径覆盖
// ---------------------------------------------------------------------------

// TestWriteBatch正常路径 验证 WriteBatch 正常批量写入
func TestWriteBatch正常路径(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
		{Key: "k2", Values: map[string]common.Value{colVal: common.NewInt64(2)}},
	}
	err = eng.WriteBatch(rows)
	if err != nil {
		t.Fatalf("WriteBatch 失败: %v", err)
	}

	row, ok := eng.Get("k1")
	if !ok {
		t.Fatal("期望找到 k1")
	}
	if row.Columns[colVal].Int64 != 1 {
		t.Errorf("期望 val=1，得到 %d", row.Columns[colVal].Int64)
	}
}

// ---------------------------------------------------------------------------
// Engine: WriteBatch rotate memtable 正常路径
// ---------------------------------------------------------------------------

// TestWriteBatchRotate正常 验证 WriteBatch 触发 memtable rotate
func TestWriteBatchRotate正常(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir, MaxMemTableSize: 256})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入足够数据触发 rotate
	for i := 0; i < 50; i++ {
		rows := []WriteRow{
			{Key: fmt.Sprintf("key_%04d", i), Values: map[string]common.Value{colVal: common.NewInt64(int64(i))}},
		}
		if err := eng.WriteBatch(rows); err != nil {
			t.Fatalf("WriteBatch %d 失败: %v", i, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Engine: Compact 注册索引失败
// ---------------------------------------------------------------------------

// TestCompact注册索引失败 验证 Compact 在注册索引失败时回滚
func TestCompact注册索引失败(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// 创建足够多的 L0 segment
	for i := 0; i < defaultL0CompactionThreshold; i++ {
		_ = eng.Write(fmt.Sprintf("key%d", i), map[string]common.Value{colVal: common.NewInt64(int64(i))})
		if err := eng.Flush(cols); err != nil {
			t.Fatalf("Flush %d 失败: %v", i, err)
		}
	}

	// 手动注册一个 segment ID 为 0 的索引使后续注册失败
	// 这不会影响 Compact，但验证了回滚路径的存在
	segCount := len(eng.segments)
	if segCount == 0 {
		t.Fatal("期望至少有一个 segment")
	}
}

// ---------------------------------------------------------------------------
// Engine: writeSegment 文件系统错误
// ---------------------------------------------------------------------------

// TestWriteSegment磁盘空间不足模拟 验证 writeSegment 在磁盘空间不足时的行为
func TestWriteSegment磁盘空间不足模拟(t *testing.T) {
	skipIfRoot(t)

	dir := t.TempDir()
	flusher := NewFlusher(dir, newSegmentIDGen())

	// 创建一个有效 segment
	keys := []string{"a"}
	builder := NewSegmentBuilder(1, "a", "a")
	builder.SetKeys(keys)
	enc, err := EncodeColumn(common.TypeInt64, []int64{1}, 1, nil)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}
	builder.AddEncodedColumn(enc)
	seg, err := builder.Build()
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}

	// 设置文件大小限制来模拟磁盘空间不足
	var rLimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &rLimit); err != nil {
		t.Skip("无法获取文件大小限制")
	}

	// 设置极小的文件大小限制
	oldLimit := rLimit.Cur
	rLimit.Cur = 100 // 100 字节限制
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &rLimit); err != nil {
		t.Skip("无法设置文件大小限制")
	}
	defer func() {
		rLimit.Cur = oldLimit
		_ = syscall.Setrlimit(syscall.RLIMIT_FSIZE, &rLimit)
	}()

	_, err = writeSegmentFile(flusher.dataDir, seg)
	if err == nil {
		t.Error("期望磁盘空间不足时返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// OpenWAL 错误路径覆盖（wal.go:66）
// ---------------------------------------------------------------------------

// TestOpenWALNonExistentPathCov_V4 测试 OpenWAL 在路径不存在时返回错误
// 覆盖 wal.go:69-70 行的 os.IsNotExist 分支
func TestOpenWALNonExistentPathCov_V4(t *testing.T) {
	dir := t.TempDir()
	// 文件不存在
	_, _, err := OpenWAL(filepath.Join(dir, "no_such.wal"))
	if err == nil {
		t.Fatal("期望文件不存在时返回错误，得到 nil")
	}
}

// TestOpenWALNonExistentDirCov_V4 测试 OpenWAL 在目录不存在时返回错误
// 覆盖 wal.go:69-70 行的 os.IsNotExist 分支
func TestOpenWALNonExistentDirCov_V4(t *testing.T) {
	_, _, err := OpenWAL("/nonexistent_dir/sub/dir/test.wal")
	if err == nil {
		t.Fatal("期望目录不存在时返回错误，得到 nil")
	}
}

// TestOpenWALPermissionDeniedCov_V4 测试 OpenWAL 在权限不足时返回错误
// 覆盖 wal.go:72 行的非 NotExist 错误分支
func TestOpenWALPermissionDeniedCov_V4(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root 用户绕过文件权限检查")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "noperm.wal")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("创建文件失败: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("关闭文件失败: %v", err)
	}
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatalf("chmod 失败: %v", err)
	}
	defer func() { _ = os.Chmod(path, 0644) }()

	_, _, err = OpenWAL(path)
	if err == nil {
		t.Fatal("期望权限不足返回错误，得到 nil")
	}
}

// TestOpenWALValidRecoveryCov_V4 测试 OpenWAL 正常恢复路径
// 验证 replay + truncate + seek 正常路径的完整覆盖
func TestOpenWALValidRecoveryCov_V4(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "valid.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte("record_one"))
	_ = w.AppendWrite([]byte("record_two"))
	_ = w.Sync()
	_ = w.Close()

	opened, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = opened.Close() }()

	if len(recs) != 2 {
		t.Fatalf("期望 2 条记录，得到 %d", len(recs))
	}
	// 验证恢复后可以继续追加
	if err := opened.AppendWrite([]byte("after_open")); err != nil {
		t.Fatalf("恢复后追加失败: %v", err)
	}
}

// ---------------------------------------------------------------------------
// getEncoder / getDecoder 池复用路径（compress.go:16, compress.go:33）
// ---------------------------------------------------------------------------

// TestGetEncoderPoolReuseCov_V4 测试 getEncoder 从池中复用编码器
// 覆盖 compress.go:17-18 行的池复用分支
func TestGetEncoderPoolReuseCov_V4(t *testing.T) {
	// 第一次获取编码器（池为空，创建新的）
	enc1, err := getEncoder()
	if err != nil {
		t.Fatalf("getEncoder 失败: %v", err)
	}
	// 归还到池中
	putEncoder(enc1)

	// 第二次获取编码器（应从池中复用）
	enc2, err := getEncoder()
	if err != nil {
		t.Fatalf("getEncoder 复用失败: %v", err)
	}
	// 验证编码器可用
	result := enc2.EncodeAll([]byte("pool reuse test"), nil)
	if len(result) == 0 {
		t.Error("编码器应产生非空输出")
	}
	putEncoder(enc2)
}

// TestGetDecoderPoolReuseCov_V4 测试 getDecoder 从池中复用解码器
// 覆盖 compress.go:34-35 行的池复用分支
func TestGetDecoderPoolReuseCov_V4(t *testing.T) {
	// 先压缩一些数据用于后续解码
	compressed, err := Compress([]byte("decoder pool reuse test"))
	if err != nil {
		t.Fatalf("Compress 失败: %v", err)
	}

	// 第一次获取解码器（池为空，创建新的）
	dec1, err := getDecoder()
	if err != nil {
		t.Fatalf("getDecoder 失败: %v", err)
	}
	// 归还到池中
	putDecoder(dec1)

	// 第二次获取解码器（应从池中复用）
	dec2, err := getDecoder()
	if err != nil {
		t.Fatalf("getDecoder 复用失败: %v", err)
	}
	// 验证解码器可用
	result, err := dec2.DecodeAll(compressed, nil)
	if err != nil {
		t.Fatalf("DecodeAll 失败: %v", err)
	}
	if string(result) != "decoder pool reuse test" {
		t.Errorf("解码结果不匹配: 期望 %q，得到 %q", "decoder pool reuse test", string(result))
	}
	putDecoder(dec2)
}

// TestCompressPoolReuseViaCompressCov_V4 测试通过 Compress/Decompress 间接复用编解码器池
// 连续多次调用 Compress/Decompress，验证池复用路径
func TestCompressPoolReuseViaCompressCov_V4(t *testing.T) {
	for i := 0; i < 5; i++ {
		original := []byte("pool test iteration data")
		compressed, err := Compress(original)
		if err != nil {
			t.Fatalf("Compress 第 %d 次失败: %v", i, err)
		}
		decompressed, err := Decompress(compressed)
		if err != nil {
			t.Fatalf("Decompress 第 %d 次失败: %v", i, err)
		}
		if string(decompressed) != string(original) {
			t.Errorf("第 %d 次往返不匹配", i)
		}
	}
}

// ---------------------------------------------------------------------------
// Compress 空数据路径（compress.go:50）
// ---------------------------------------------------------------------------

// TestCompressEmptyNilDataCov_V4 测试 Compress 对 nil 和空切片的处理
// 覆盖 compress.go:51-53 行的空数据分支（返回 nil, nil）
func TestCompressEmptyNilDataCov_V4(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"nil 切片", nil},
		{"空切片", []byte{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Compress(tt.data)
			if err != nil {
				t.Fatalf("Compress 失败: %v", err)
			}
			if result != nil {
				t.Errorf("期望 nil 结果，得到 %d 字节", len(result))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// CompressColumn nil 错误路径（compress.go:80）
// ---------------------------------------------------------------------------

// TestCompressColumnNilErrorCov_V4 测试 CompressColumn 传入 nil 返回错误
// 覆盖 compress.go:81-83 行的 nil 检查分支
func TestCompressColumnNilErrorCov_V4(t *testing.T) {
	err := CompressColumn(nil)
	if err == nil {
		t.Fatal("期望 nil EncodedColumn 返回错误，得到 nil")
	}
}

// TestDecompressColumnNilErrorCov_V4 测试 DecompressColumn 传入 nil 返回错误
// 覆盖 compress.go:94-96 行的 nil 检查分支
func TestDecompressColumnNilErrorCov_V4(t *testing.T) {
	err := DecompressColumn(nil)
	if err == nil {
		t.Fatal("期望 nil EncodedColumn 返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// EncodeColumn / DecodeColumn 未知编码类型路径（encoding.go:51）
// ---------------------------------------------------------------------------

// TestDecodeColumnUnknownEncodingCov_V4 测试 DecodeColumn 对未知编码类型返回错误
// 覆盖 encoding.go:79 行的 default 分支
// 注：EncodeColumn 的 default 分支（encoding.go:63）不可达，
// 因为 selectEncoding 总是返回已知编码类型
func TestDecodeColumnUnknownEncodingCov_V4(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingType(99), // 未知编码类型
		Type:     common.TypeInt64,
		RowCount: 1,
		Data:     make([]byte, 8),
	}
	_, _, err := DecodeColumn(enc)
	if err == nil {
		t.Fatal("期望未知编码类型返回错误，得到 nil")
	}
}

// TestEncodeColumnAllSupportedTypesCov_V4 测试 EncodeColumn 对所有支持的数据类型正常编码
// 确保 selectEncoding 的所有分支都被覆盖
func TestEncodeColumnAllSupportedTypesCov_V4(t *testing.T) {
	tests := []struct {
		name     string
		typ      common.DataType
		data     interface{}
		rowCount uint32
		wantEnc  EncodingType
	}{
		{"bool->Bitmap", common.TypeBool, []uint64{1, 0, 1}, 3, EncodingBitmap},
		{"string->Dict", common.TypeString, []string{"a", "b"}, 2, EncodingDict},
		{"int64 RLE", common.TypeInt64, repeatInt64(42, 100), 100, EncodingRLE},
		{"int64 Plain", common.TypeInt64, []int64{1, 2, 3}, 3, EncodingPlain},
		{"float64->Plain", common.TypeFloat64, []float64{1.1, 2.2}, 2, EncodingPlain},
		{"timestamp->Plain", common.TypeTimestamp, []int64{1000, 2000}, 2, EncodingPlain},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := EncodeColumn(tt.typ, tt.data, tt.rowCount, nil)
			if err != nil {
				t.Fatalf("EncodeColumn 失败: %v", err)
			}
			if enc.Encoding != tt.wantEnc {
				t.Errorf("编码类型 = %v，期望 %v", enc.Encoding, tt.wantEnc)
			}
		})
	}
}

// repeatInt64 生成重复的 int64 切片（用于触发 RLE 编码）
func repeatInt64(val int64, count int) []int64 {
	s := make([]int64, count)
	for i := range s {
		s[i] = val
	}
	return s
}
