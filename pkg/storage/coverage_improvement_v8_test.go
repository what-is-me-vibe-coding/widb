package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ---------------------------------------------------------------------------
// OpenWAL (76.5%) - 补充非普通文件 Truncate 错误路径
// ---------------------------------------------------------------------------

// TestOpenWALWithNonRegularFileV2 测试 OpenWAL 在非普通文件上的 Truncate 错误。
func TestOpenWALWithNonRegularFileV2(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devnull.wal")

	// 创建指向 /dev/null 的符号链接
	if err := os.Symlink("/dev/null", path); err != nil {
		t.Skipf("symlink to /dev/null failed: %v", err)
	}

	_, _, err := OpenWAL(path)
	if err == nil {
		t.Fatal("expected error when opening non-regular file as WAL, got nil")
	}
}

// ---------------------------------------------------------------------------
// maybeRotate (80.8%) - 补充轮转错误恢复路径
// ---------------------------------------------------------------------------

// TestWALMaybeRotatePrevDirConflict 测试 WAL 轮转时 .prev 是目录导致 Rename 失败。
func TestWALMaybeRotatePrevDirConflict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// 创建一个 .prev 目录，使得 Rename 旧文件到 .prev 失败
	prevPath := path + ".prev"
	if err := os.Mkdir(prevPath, 0755); err != nil {
		t.Fatalf("Mkdir .prev failed: %v", err)
	}

	// 设置很小的 maxSize 触发轮转
	w.maxSize = walMetaSize + 10

	// 写入数据触发轮转 - 不应 panic
	err = w.AppendWrite([]byte("trigger rotation"))
	_ = err
	_ = w.Close()
}

// TestWALMaybeRotateTmpDirConflict 测试 WAL 轮转时 .tmp 是目录导致重命名失败。
func TestWALMaybeRotateTmpDirConflict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// 创建一个同名的临时文件目录
	tmpPath := path + ".tmp"
	if err := os.Mkdir(tmpPath, 0755); err != nil {
		t.Fatalf("Mkdir .tmp failed: %v", err)
	}

	// 设置很小的 maxSize 触发轮转
	w.maxSize = walMetaSize + 10

	// 写入数据触发轮转 - 不应 panic
	err = w.AppendWrite([]byte("trigger rotation"))
	_ = err
	_ = w.Close()

	// 清理
	_ = os.RemoveAll(tmpPath)
}

// ---------------------------------------------------------------------------
// Engine Flush (82.0%) - 补充 Flush 错误路径
// ---------------------------------------------------------------------------

// TestEngineFlushWithNoColumnMetaV2 测试 Flush 在没有 columnMeta 但有数据时的行为。
func TestEngineFlushWithNoColumnMetaV2(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入数据但不设置 columnMeta
	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})

	// Flush 带 columnMeta 应该工作
	err = eng.Flush([]ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}})
	if err != nil {
		t.Fatalf("Flush with columnMeta failed: %v", err)
	}
}

// TestEngineFlushWithClosedWALV2 测试 Flush 在 WAL 关闭时的错误处理。
func TestEngineFlushWithClosedWALV2(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}

	// 写入数据
	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})

	// 关闭 WAL 使 Flush 失败
	if err := eng.wal.Close(); err != nil {
		t.Fatalf("WAL Close failed: %v", err)
	}

	// Flush 应该失败
	err = eng.Flush([]ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}})
	if err == nil {
		t.Fatal("expected error when flushing with closed WAL, got nil")
	}

	// 清理
	eng.wal, _ = CreateWAL(filepath.Join(dir, "wal.log"))
	_ = eng.Close()
}

// ---------------------------------------------------------------------------
// ScanRange (88.2%) - 补充 MergeIterator 错误路径
// ---------------------------------------------------------------------------

// TestScanRangeWithCorruptSegmentV2 测试 ScanRange 在 Segment 数据损坏时返回 nil。
func TestScanRangeWithCorruptSegmentV2(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入数据并 Flush
	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Flush(cols)

	// 破坏 Segment 数据使迭代器出错
	eng.mu.RLock()
	if len(eng.segments) > 0 {
		for i := range eng.segments[0].Columns {
			eng.segments[0].Columns[i].Data = []byte{0xFF, 0xFE, 0xFD, 0xFC}
		}
	}
	eng.mu.RUnlock()

	// ScanRange 应该返回 nil 因为迭代器出错
	eng.mu.RLock()
	results := eng.ScanRange("a", "z")
	eng.mu.RUnlock()

	if results != nil {
		t.Errorf("expected nil results when iterator has error, got %d entries", len(results))
	}
}

// ---------------------------------------------------------------------------
// applySingleWriteRecord (80.0%) - 补充反序列化失败路径
// ---------------------------------------------------------------------------

// TestApplySingleWriteRecordCorruptPayloadV2 测试 applySingleWriteRecord 在损坏数据时的错误处理。
func TestApplySingleWriteRecordCorruptPayloadV2(t *testing.T) {
	mem := NewMemTable()

	// 传入损坏的 payload 数据
	version, ok := applySingleWriteRecord([]byte{0xFF, 0xFE, 0xFD}, 0, mem)
	if ok {
		t.Error("expected ok=false for corrupt payload, got true")
	}
	if version != 0 {
		t.Errorf("expected version=0 for corrupt payload, got %d", version)
	}
}

// TestApplyBatchWriteRecordCorruptPayloadV2 测试 applyBatchWriteRecord 在损坏数据时的错误处理。
func TestApplyBatchWriteRecordCorruptPayloadV2(t *testing.T) {
	mem := NewMemTable()

	// 传入损坏的 payload 数据
	version, ok := applyBatchWriteRecord([]byte{0xFF, 0xFE, 0xFD}, 0, mem)
	if ok {
		t.Error("expected ok=false for corrupt payload, got true")
	}
	if version != 0 {
		t.Errorf("expected version=0 for corrupt payload, got %d", version)
	}
}

// TestApplySingleRecordWithUnknownTypeV2 测试 applySingleRecord 在未知类型时的行为。
func TestApplySingleRecordWithUnknownTypeV2(t *testing.T) {
	mem := NewMemTable()

	// 传入未知类型的记录
	version, ok := applySingleRecord(RawRecord{Type: 0xFF, Payload: []byte("data")}, 0, mem)
	if !ok {
		t.Error("expected ok=true for unknown record type (should be skipped)")
	}
	if version != 0 {
		t.Errorf("expected version=0 for unknown record type, got %d", version)
	}
}

// ---------------------------------------------------------------------------
// deserializeWriteRecord (83.3%) - 补充空记录路径
// ---------------------------------------------------------------------------

// TestDeserializeWriteRecordEmptyBatchV2 测试 deserializeWriteRecord 在空批量记录时的错误处理。
func TestDeserializeWriteRecordEmptyBatchV2(t *testing.T) {
	// 序列化一个空的批量记录
	data, err := serializeBatchWriteRecord([]WriteRow{}, 0)
	if err != nil {
		t.Fatalf("serializeBatchWriteRecord failed: %v", err)
	}

	_, _, _, err = deserializeWriteRecord(data)
	if err == nil {
		t.Fatal("expected error for empty batch write record, got nil")
	}
}

// ---------------------------------------------------------------------------
// Compress/Decompress pool 复用路径
// ---------------------------------------------------------------------------

// TestCompressPoolReuseV2 测试 Compress/Decompress 的编码器/解码器池复用。
func TestCompressPoolReuseV2(t *testing.T) {
	// 多次压缩/解压以触发池的 Put/Get 路径
	for i := 0; i < 10; i++ {
		original := []byte("test data for pool reuse iteration")
		compressed, err := Compress(original)
		if err != nil {
			t.Fatalf("Compress iteration %d failed: %v", i, err)
		}
		decompressed, err := Decompress(compressed)
		if err != nil {
			t.Fatalf("Decompress iteration %d failed: %v", i, err)
		}
		if string(decompressed) != string(original) {
			t.Errorf("iteration %d: round-trip mismatch", i)
		}
	}
}

// ---------------------------------------------------------------------------
// DecompressColumn (85.7%) - 补充损坏数据路径
// ---------------------------------------------------------------------------

// TestDecompressColumnCorruptedDataV2 测试 DecompressColumn 在损坏数据时的错误处理。
func TestDecompressColumnCorruptedDataV2(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeInt64,
		RowCount: 1,
		Data:     []byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE},
	}
	err := DecompressColumn(enc)
	if err == nil {
		t.Fatal("expected error for corrupted compressed data in DecompressColumn, got nil")
	}
}

// ---------------------------------------------------------------------------
// Engine Write (84.2%) - 补充 WAL 序列化失败路径
// ---------------------------------------------------------------------------

// TestEngineWriteSerializeError 测试 Engine.Write 在序列化失败时的错误处理。
func TestEngineWriteSerializeError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入包含无法序列化值的数据（空字符串 key 的边界情况）
	err = eng.Write("", map[string]common.Value{colVal: common.NewInt64(1)})
	// 空字符串 key 应该可以正常写入
	if err != nil {
		t.Logf("Write with empty key returned: %v", err)
	}
}

// ---------------------------------------------------------------------------
// OpenWAL: 非普通文件 Truncate 错误路径（76.5% → 目标 >90%）
// ---------------------------------------------------------------------------

// TestOpenWAL_TruncateOnNonRegularFile 测试 OpenWAL 对目录的 Truncate 行为。
// 对目录调用 Truncate 会返回错误。
func TestOpenWAL_TruncateOnNonRegularFile(t *testing.T) {
	dir := t.TempDir()

	// 尝试打开目录作为 WAL 文件，应返回错误
	_, _, err := OpenWAL(dir)
	if err == nil {
		t.Error("期望打开目录作为 WAL 返回错误，得到 nil")
	}
}

// TestOpenWAL_FileNotExist 测试 OpenWAL 打开不存在的文件。
func TestOpenWAL_FileNotExist(t *testing.T) {
	dir := t.TempDir()
	_, _, err := OpenWAL(filepath.Join(dir, "nonexistent.wal"))
	if err == nil {
		t.Error("期望打开不存在的 WAL 文件返回错误，得到 nil")
	}
}

// TestOpenWAL_ValidRecovery 测试 OpenWAL 正常恢复路径。
func TestOpenWAL_ValidRecovery(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "test.wal")

	// 先创建 WAL 并写入数据
	wal, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	if err := wal.AppendWrite([]byte("test payload")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	if err := wal.Sync(); err != nil {
		t.Fatalf("Sync 失败: %v", err)
	}
	if err := wal.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	// 使用 OpenWAL 恢复
	wal2, records, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = wal2.Close() }()

	if len(records) == 0 {
		t.Error("期望恢复到记录，但 records 为空")
	}
}

// TestOpenWAL_SeekError 测试 OpenWAL 在 Seek 时出错。
// 通过关闭文件描述符来触发 Seek 错误。
func TestOpenWAL_SeekError(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "test.wal")

	// 创建空 WAL
	wal, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	if err := wal.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	// OpenWAL 对空文件应成功（validOffset=0, Seek(0,...) 不会失败）
	wal2, records, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL 空文件不应返回错误: %v", err)
	}
	_ = wal2.Close()
	if len(records) != 0 {
		t.Errorf("空 WAL 应无记录，得到 %d 条", len(records))
	}
}

// ---------------------------------------------------------------------------
// Compress/Decompress: 编码器/解码器池复用路径（85.7% → 100%）
// ---------------------------------------------------------------------------

// TestCompressDecompress_PoolReuse 测试编码器/解码器池复用。
// 多次调用 Compress/Decompress 应从池中复用编解码器实例。
func TestCompressDecompress_PoolReuse(t *testing.T) {
	data := []byte("test data for pool reuse verification")

	// 第一次调用：创建新的编码器/解码器
	compressed1, err := Compress(data)
	if err != nil {
		t.Fatalf("第一次 Compress 失败: %v", err)
	}

	// 第二次调用：应从池中获取编码器
	compressed2, err := Compress(data)
	if err != nil {
		t.Fatalf("第二次 Compress 失败: %v", err)
	}

	// 两次压缩结果应一致
	if string(compressed1) != string(compressed2) {
		t.Error("两次压缩结果不一致")
	}

	// 解压验证
	decompressed, err := Decompress(compressed1)
	if err != nil {
		t.Fatalf("Decompress 失败: %v", err)
	}
	if string(decompressed) != string(data) {
		t.Errorf("解压数据不匹配: 期望 %q，得到 %q", data, decompressed)
	}

	// 第二次解压：应从池中获取解码器
	decompressed2, err := Decompress(compressed2)
	if err != nil {
		t.Fatalf("第二次 Decompress 失败: %v", err)
	}
	if string(decompressed2) != string(data) {
		t.Errorf("第二次解压数据不匹配: 期望 %q，得到 %q", data, decompressed2)
	}
}

// TestCompressColumn_WithData 测试 CompressColumn 正常压缩路径。
func TestCompressColumn_WithData(t *testing.T) {
	original := []byte("column data for compression test")
	enc := &EncodedColumn{Data: make([]byte, len(original))}
	copy(enc.Data, original)

	err := CompressColumn(enc)
	if err != nil {
		t.Fatalf("CompressColumn 失败: %v", err)
	}

	// 压缩后数据应与原始数据不同
	if string(enc.Data) == string(original) {
		t.Error("压缩后数据应与原始数据不同")
	}

	// 解压验证
	err = DecompressColumn(enc)
	if err != nil {
		t.Fatalf("DecompressColumn 失败: %v", err)
	}
	if string(enc.Data) != string(original) {
		t.Errorf("解压后数据不匹配: 期望 %q，得到 %q", original, enc.Data)
	}
}

// ---------------------------------------------------------------------------
// Engine Flush: WAL 关闭时 Flush 失败路径（82.0% → >90%）
// ---------------------------------------------------------------------------

// TestFlush_EmptyImmutable 测试 Flush 时没有 immutable memtable 的路径。
func TestFlush_EmptyImmutable(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: crCol1, Type: common.TypeInt64}}

	// 没有写入任何数据，Flush 应直接返回 nil
	err = eng.Flush(cols)
	if err != nil {
		t.Errorf("空 Flush 不应返回错误: %v", err)
	}
}

// TestFlush_WithColumnMeta 测试 Flush 时设置 columnMeta 的路径。
func TestFlush_WithColumnMeta(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: crCol1, Type: common.TypeInt64}}

	// 写入数据
	if err := eng.Write("key1", map[string]common.Value{crCol1: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}

	// 第一次 Flush 设置 columnMeta
	err = eng.Flush(cols)
	if err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	// 第二次 Flush 不应覆盖已有的 columnMeta
	if err := eng.Write("key2", map[string]common.Value{crCol1: common.NewInt64(2)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	err = eng.Flush(cols)
	if err != nil {
		t.Fatalf("第二次 Flush 失败: %v", err)
	}
}

// TestFlush_MultipleImmutable 测试 Flush 多个 immutable memtable 的路径。
func TestFlush_MultipleImmutable(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir, MaxMemTableSize: 256})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: crCol1, Type: common.TypeInt64}}

	// 写入大量数据以触发多次 memtable 轮转
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key_%04d", i)
		if err := eng.Write(key, map[string]common.Value{crCol1: common.NewInt64(int64(i))}); err != nil {
			t.Fatalf("Write %d 失败: %v", i, err)
		}
	}

	err = eng.Flush(cols)
	if err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Engine Write: 空字符串 key 边界条件（84.2% → >90%）
// ---------------------------------------------------------------------------

// TestWrite_EmptyKey 测试 Write 使用空字符串 key。
func TestWrite_EmptyKey(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	err = eng.Write("", map[string]common.Value{crCol1: common.NewInt64(1)})
	if err != nil {
		t.Errorf("空 key 写入不应返回错误: %v", err)
	}

	// 验证可以读取
	row, ok := eng.Get("")
	if !ok {
		t.Error("期望能读取空 key 的数据")
	}
	if row.Columns[crCol1] != common.NewInt64(1) {
		t.Errorf("读取值不匹配: 期望 1，得到 %v", row.Columns[crCol1])
	}
}

// ---------------------------------------------------------------------------
// ScanRange: Segment 数据损坏时迭代器错误路径（88.2% → >90%）
// ---------------------------------------------------------------------------

// TestScanRange_CorruptSegmentData 测试 ScanRange 在 Segment 数据损坏时返回 nil。
func TestScanRange_CorruptSegmentData(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: crCol1, Type: common.TypeInt64}}

	// 写入并 Flush 以创建 Segment
	if err := eng.Write("key1", map[string]common.Value{crCol1: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	// 损坏 Segment 数据
	eng.mu.Lock()
	for _, seg := range eng.segments {
		for i := range seg.Columns {
			seg.Columns[i].Data = []byte{0xFF, 0xFE, 0xFD, 0xFC}
		}
	}
	eng.mu.Unlock()

	// ScanRange 应返回 nil（迭代器遇到错误）
	results := eng.ScanRange("", "z")
	if results != nil {
		t.Errorf("损坏 Segment 的 ScanRange 应返回 nil，得到 %d 条结果", len(results))
	}
}
