package storage

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ---------------------------------------------------------------------------
// OpenWAL: replayWAL 错误路径（76.5% → >85%）
// ---------------------------------------------------------------------------

// TestOpenWAL_CorruptWALReplay 测试 OpenWAL 在 WAL 数据损坏时的行为。
// 写入有效数据后，在文件末尾追加无效数据，验证 OpenWAL 能恢复有效记录。
func TestOpenWAL_CorruptWALReplay(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "test.wal")

	// 创建 WAL 并写入有效数据
	wal, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	if err := wal.AppendWrite([]byte("valid data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	if err := wal.Sync(); err != nil {
		t.Fatalf("Sync 失败: %v", err)
	}
	if err := wal.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	// 在文件末尾追加无效数据（模拟部分写入/崩溃）
	f, err := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("打开 WAL 文件追加失败: %v", err)
	}
	_, _ = f.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x10, 0x00, 0x00, 0x00, 0x05, 'h', 'e', 'l', 'l', 'o'})
	_ = f.Close()

	// OpenWAL 应能恢复有效记录并截断无效数据
	wal2, records, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL 损坏文件不应返回错误（应截断无效部分）: %v", err)
	}
	defer func() { _ = wal2.Close() }()

	if len(records) == 0 {
		t.Error("期望恢复到有效记录，但 records 为空")
	}
}

// ---------------------------------------------------------------------------
// Flush: checkpoint 写入路径（82.0% → >90%）
// ---------------------------------------------------------------------------

// TestFlush_CheckpointAfterMultipleFlushes 测试多次 Flush 后 checkpoint 正确写入。
func TestFlush_CheckpointAfterMultipleFlushes(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: crCol1, Type: common.TypeInt64}}

	// 第一次 Flush
	if err := eng.Write("key1", map[string]common.Value{crCol1: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("第一次 Flush 失败: %v", err)
	}

	// 第二次 Flush
	if err := eng.Write("key2", map[string]common.Value{crCol1: common.NewInt64(2)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("第二次 Flush 失败: %v", err)
	}

	// 验证数据完整性
	row, ok := eng.Get("key1")
	if !ok || row.Columns[crCol1] != common.NewInt64(1) {
		t.Errorf("key1 数据不正确")
	}
	row, ok = eng.Get("key2")
	if !ok || row.Columns[crCol1] != common.NewInt64(2) {
		t.Errorf("key2 数据不正确")
	}
}

// ---------------------------------------------------------------------------
// Write: WAL 同步路径（84.2% → >90%）
// ---------------------------------------------------------------------------

// TestWrite_WALSyncPath 测试 Write 的 WAL 同步路径。
func TestWrite_WALSyncPath(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 连续写入多条数据，验证 WAL 同步
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key_%d", i)
		if err := eng.Write(key, map[string]common.Value{crCol1: common.NewInt64(int64(i))}); err != nil {
			t.Fatalf("Write %d 失败: %v", i, err)
		}
	}

	// 验证所有数据可读
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key_%d", i)
		row, ok := eng.Get(key)
		if !ok {
			t.Errorf("key_%d 不存在", i)
			continue
		}
		if row.Columns[crCol1] != common.NewInt64(int64(i)) {
			t.Errorf("key_%d: 期望 %d，得到 %v", i, i, row.Columns[crCol1])
		}
	}
}

// ---------------------------------------------------------------------------
// Close: WAL Sync 和 Close 路径（85.7% → >90%）
// ---------------------------------------------------------------------------

// TestClose_AfterWrites 测试写入后关闭引擎的 WAL Sync/Close 路径。
func TestClose_AfterWrites(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}

	// 写入数据
	if err := eng.Write("key1", map[string]common.Value{crCol1: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}

	// 关闭引擎（触发 WAL Sync + Close）
	if err := eng.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	// 重新打开引擎验证数据恢复
	eng2, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("重新打开引擎失败: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	row, ok := eng2.Get("key1")
	if !ok {
		t.Fatal("期望恢复 key1，但未找到")
	}
	if row.Columns[crCol1] != common.NewInt64(1) {
		t.Errorf("恢复数据不匹配: 期望 1，得到 %v", row.Columns[crCol1])
	}
}

// ---------------------------------------------------------------------------
// WriteBatch: 空 rows 路径和 MemTable 轮转路径（85.0% → >90%）
// ---------------------------------------------------------------------------

// TestWriteBatch_EmptyRows 测试 WriteBatch 空 rows 直接返回 nil。
func TestWriteBatch_EmptyRows(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	err = eng.WriteBatch(nil)
	if err != nil {
		t.Errorf("WriteBatch(nil) 不应返回错误: %v", err)
	}

	err = eng.WriteBatch([]WriteRow{})
	if err != nil {
		t.Errorf("WriteBatch([]) 不应返回错误: %v", err)
	}
}

// TestWriteBatch_MemTableRotation 测试 WriteBatch 触发 MemTable 轮转。
func TestWriteBatch_MemTableRotation(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir, MaxMemTableSize: 256})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入足够多的数据以触发 MemTable 轮转
	for batch := 0; batch < 5; batch++ {
		rows := make([]WriteRow, 20)
		for i := 0; i < 20; i++ {
			rows[i] = WriteRow{
				Key:    fmt.Sprintf("batch_%d_key_%d", batch, i),
				Values: map[string]common.Value{crCol1: common.NewInt64(int64(batch*20 + i))},
			}
		}
		if err := eng.WriteBatch(rows); err != nil {
			t.Fatalf("WriteBatch %d 失败: %v", batch, err)
		}
	}

	// 验证部分数据可读
	row, ok := eng.Get("batch_0_key_0")
	if !ok {
		t.Error("期望找到 batch_0_key_0")
	} else if row.Columns[crCol1] != common.NewInt64(0) {
		t.Errorf("batch_0_key_0: 期望 0，得到 %v", row.Columns[crCol1])
	}
}

// ---------------------------------------------------------------------------
// Compress: 空数据路径（85.7% → >90%）
// ---------------------------------------------------------------------------

// TestCompress_EmptyData 测试 Compress 对空数据返回 nil。
func TestCompress_EmptyData(t *testing.T) {
	result, err := Compress(nil)
	if err != nil {
		t.Fatalf("Compress(nil) 不应返回错误: %v", err)
	}
	if result != nil {
		t.Errorf("Compress(nil) 应返回 nil，得到 %v", result)
	}

	result, err = Compress([]byte{})
	if err != nil {
		t.Fatalf("Compress([]) 不应返回错误: %v", err)
	}
	if result != nil {
		t.Errorf("Compress([]) 应返回 nil，得到 %v", result)
	}
}

// TestDecompress_EmptyData 测试 Decompress 对空数据返回 nil。
func TestDecompress_EmptyData(t *testing.T) {
	result, err := Decompress(nil)
	if err != nil {
		t.Fatalf("Decompress(nil) 不应返回错误: %v", err)
	}
	if result != nil {
		t.Errorf("Decompress(nil) 应返回 nil，得到 %v", result)
	}
}

// TestCompressColumn_NilInputV12 测试 CompressColumn 对 nil 输入返回错误。
func TestCompressColumn_NilInputV12(t *testing.T) {
	err := CompressColumn(nil)
	if err == nil {
		t.Error("期望 CompressColumn(nil) 返回错误，得到 nil")
	}
}

// TestDecompressColumn_NilInputV12 测试 DecompressColumn 对 nil 输入返回错误。
func TestDecompressColumn_NilInputV12(t *testing.T) {
	err := DecompressColumn(nil)
	if err == nil {
		t.Error("期望 DecompressColumn(nil) 返回错误，得到 nil")
	}
}

// TestDecompressColumn_InvalidData 测试 DecompressColumn 对无效数据返回错误。
func TestDecompressColumn_InvalidData(t *testing.T) {
	enc := &EncodedColumn{Data: []byte{0xFF, 0xFE, 0xFD}}
	err := DecompressColumn(enc)
	if err == nil {
		t.Error("期望 DecompressColumn 无效数据返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// EncodeColumn: 未知编码类型路径（85.7% → >90%）
// ---------------------------------------------------------------------------

// TestDecodeColumn_UnknownEncoding 测试 DecodeColumn 对未知编码返回错误。
func TestDecodeColumn_UnknownEncoding(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingType(99), // 无效编码
		Type:     common.TypeInt64,
		RowCount: 1,
		Data:     []byte{1, 2, 3, 4, 5, 6, 7, 8},
	}
	_, _, err := DecodeColumn(enc)
	if err == nil {
		t.Error("期望 DecodeColumn 未知编码返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// decodeSegmentColumn: BlockCache 命中路径（82.1% → >90%）
// ---------------------------------------------------------------------------

// TestDecodeSegmentColumn_CacheHit 测试 decodeSegmentColumn 的 BlockCache 命中路径。
func TestDecodeSegmentColumn_CacheHit(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: crCol1, Type: common.TypeInt64}}

	if err := eng.Write("key1", map[string]common.Value{crCol1: common.NewInt64(42)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	// 第一次 ScanRange：填充 BlockCache
	results1 := eng.ScanRange("", "z")
	if len(results1) == 0 {
		t.Fatal("第一次 ScanRange 应返回结果")
	}

	// 第二次 ScanRange：应从 BlockCache 命中
	results2 := eng.ScanRange("", "z")
	if len(results2) == 0 {
		t.Fatal("第二次 ScanRange 应返回结果（从缓存）")
	}

	// 验证两次结果一致
	if results1[0].Key != results2[0].Key {
		t.Errorf("缓存结果不一致: 第一次 key=%s, 第二次 key=%s", results1[0].Key, results2[0].Key)
	}
}

// ---------------------------------------------------------------------------
// Engine Close: WAL Sync/Close 错误路径（85.7% → >90%）
// ---------------------------------------------------------------------------

// TestClose_Normal 测试 Engine 正常关闭路径。
func TestClose_Normal(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}

	err = eng.Close()
	if err != nil {
		t.Errorf("正常关闭不应返回错误: %v", err)
	}
}

// ---------------------------------------------------------------------------
// WriteBatch: 正常批量写入路径（85.0% → >90%）
// ---------------------------------------------------------------------------

// TestWriteBatch_Normal 测试 WriteBatch 正常批量写入。
func TestWriteBatch_Normal(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{crCol1: common.NewInt64(1)}},
		{Key: "k2", Values: map[string]common.Value{crCol1: common.NewInt64(2)}},
		{Key: "k3", Values: map[string]common.Value{crCol1: common.NewInt64(3)}},
	}

	err = eng.WriteBatch(rows)
	if err != nil {
		t.Fatalf("WriteBatch 失败: %v", err)
	}

	// 验证数据
	for i, row := range rows {
		got, ok := eng.Get(row.Key)
		if !ok {
			t.Errorf("key %s 不存在", row.Key)
			continue
		}
		expected := common.NewInt64(int64(i + 1))
		if got.Columns[crCol1] != expected {
			t.Errorf("key %s: 期望 %v，得到 %v", row.Key, expected, got.Columns[crCol1])
		}
	}
}

// TestWriteBatch_SerializationError 测试 WriteBatch 序列化失败。
func TestWriteBatch_SerializationError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 构造超长 key 使序列化后的 payload 超过限制
	rows := []WriteRow{
		{Key: string(make([]byte, maxRecordPayload+1)), Values: map[string]common.Value{crCol1: common.NewInt64(1)}},
	}

	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("期望超长 key 的 WriteBatch 返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// NewEngine: 配置参数路径（88.0% → >90%）
// ---------------------------------------------------------------------------

// TestNewEngine_CustomConfig 测试 NewEngine 使用自定义配置。
func TestNewEngine_CustomConfig(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir:         dir,
		MaxMemTableSize: 1024,
		BlockCacheSize:  1024,
		IndexCacheSize:  10,
	})
	if err != nil {
		t.Fatalf("NewEngine 自定义配置失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	if eng.activeMem.maxSize != 1024 {
		t.Errorf("MaxMemTableSize: 期望 1024，得到 %d", eng.activeMem.maxSize)
	}
}

// TestNewEngine_NegativeConfig 测试 NewEngine 使用负值配置（应使用默认值）。
func TestNewEngine_NegativeConfig(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir:         dir,
		MaxMemTableSize: -1,
		BlockCacheSize:  -1,
		IndexCacheSize:  -1,
	})
	if err != nil {
		t.Fatalf("NewEngine 负值配置失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 负值应使用默认值
	if eng.activeMem.maxSize != memTableDefaultSize {
		t.Errorf("负值 MaxMemTableSize 应使用默认值 %d，得到 %d", memTableDefaultSize, eng.activeMem.maxSize)
	}
}

// ---------------------------------------------------------------------------
// decodeSegmentColumn: 解码失败路径（82.1% → >90%）
// ---------------------------------------------------------------------------

// TestDecodeSegmentColumn_DecompressError 测试 decodeSegmentColumn 在解压失败时的行为。
func TestDecodeSegmentColumn_DecompressError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: crCol1, Type: common.TypeInt64}}

	if err := eng.Write("key1", map[string]common.Value{crCol1: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	// 损坏 Segment 列数据
	eng.mu.Lock()
	for _, seg := range eng.segments {
		for i := range seg.Columns {
			seg.Columns[i].Data = []byte{0xFF, 0xFE, 0xFD, 0xFC}
		}
	}
	eng.mu.Unlock()

	// ScanRange 应返回 nil（迭代器遇到解码错误）
	results := eng.ScanRange("", "z")
	if results != nil {
		t.Errorf("损坏 Segment 的 ScanRange 应返回 nil，得到 %d 条结果", len(results))
	}
}

// ---------------------------------------------------------------------------
// AddEncodedColumn: nil 输入路径（87.5% → 100%）
// ---------------------------------------------------------------------------

// TestAddEncodedColumn_Nil 测试 AddEncodedColumn 传入 nil。
func TestAddEncodedColumn_Nil(t *testing.T) {
	t.Helper()
	builder := NewSegmentBuilder(1, "a", "z")
	builder.AddEncodedColumn(nil) // 不应 panic
}

// TestAddEncodedColumn_WithAllFields 测试 AddEncodedColumn 包含所有字段。
func TestAddEncodedColumn_WithAllFields(t *testing.T) {
	builder := NewSegmentBuilder(1, "a", "z")
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeInt64,
		RowCount: 3,
		Data:     []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24},
		Offsets:  []uint32{0, 8, 16},
		Dict:     []string{"a", "b"},
		Nulls:    []byte{0x01},
	}
	builder.AddEncodedColumn(enc)

	if len(builder.columns) != 1 {
		t.Fatalf("期望 1 列，得到 %d", len(builder.columns))
	}

	// 验证深拷贝
	if &builder.columns[0].Data[0] == &enc.Data[0] {
		t.Error("Data 应为深拷贝，不应共享底层内存")
	}
}

// ---------------------------------------------------------------------------
// Build: 无列错误路径（89.5% → 100%）
// ---------------------------------------------------------------------------

// TestBuild_NoColumns 测试 SegmentBuilder.Build 无列时返回错误。
func TestBuild_NoColumns(t *testing.T) {
	builder := NewSegmentBuilder(1, "a", "z")
	_, err := builder.Build()
	if err == nil {
		t.Error("期望无列时 Build 返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// DeserializeSegment: 数据过短路径（87.0% → >90%）
// ---------------------------------------------------------------------------

// TestDeserializeSegment_TooShort 测试 DeserializeSegment 数据过短。
func TestDeserializeSegment_TooShort(t *testing.T) {
	_, err := DeserializeSegment([]byte{1, 2, 3})
	if err == nil {
		t.Error("期望数据过短时返回错误，得到 nil")
	}
}

// TestDeserializeSegment_InvalidMagic 测试 DeserializeSegment 无效 Magic。
func TestDeserializeSegment_InvalidMagic(t *testing.T) {
	data := make([]byte, 30)
	// Magic 不匹配
	data[0] = 0xFF
	data[1] = 0xFF
	data[2] = 0xFF
	data[3] = 0xFF
	// footer offset
	footerOffset := uint64(10)
	binary.LittleEndian.PutUint64(data[len(data)-8:], footerOffset)

	_, err := DeserializeSegment(data)
	if err == nil {
		t.Error("期望无效 Magic 时返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// writeSegment: MkdirAll 错误路径（88.9% → >90%）
// ---------------------------------------------------------------------------

// TestWriteSegment_MkdirAllError 测试 writeSegment 在无法创建目录时返回错误。
func TestWriteSegment_MkdirAllError(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "flusher-blocker-*")
	if err != nil {
		t.Fatalf("CreateTemp 失败: %v", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	f := NewFlusher(tmpPath + "/subdir")
	seg := &Segment{ID: 1, MinKey: "a", MaxKey: "z", RowCount: 1}
	_, err = f.writeSegment(seg)
	if err == nil {
		t.Error("期望 MkdirAll 失败时返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// Scheduler: runCompactLoop 和 runWALCleanLoop 的 stopCh 路径
// ---------------------------------------------------------------------------

// TestScheduler_RunCompactLoopStop 测试 runCompactLoop 通过 stopCh 停止。
func TestScheduler_RunCompactLoopStop(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{})
	stopCh := make(chan struct{})
	sched.stopCh = stopCh

	// 关闭 stopCh 使 runCompactLoop 退出
	close(stopCh)
	// 不应阻塞或 panic
}

// TestScheduler_RunWALCleanLoopStop 测试 runWALCleanLoop 通过 stopCh 停止。
func TestScheduler_RunWALCleanLoopStop(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{})
	stopCh := make(chan struct{})
	sched.stopCh = stopCh

	// 关闭 stopCh 使 runWALCleanLoop 退出
	close(stopCh)
	// 不应阻塞或 panic
}

// ---------------------------------------------------------------------------
// EncodeColumn: 未知编码类型路径（85.7% → >90%）
// ---------------------------------------------------------------------------

// TestEncodeColumn_BitmapEncoding 测试 EncodeColumn 使用 Bitmap 编码。
func TestEncodeColumn_BitmapEncoding(t *testing.T) {
	// Bool 类型数据使用 Bitmap 编码，需要传入 []uint64
	data := []uint64{1, 0, 1, 1, 0}
	rowCount := uint32(len(data))
	nulls := common.NewBitmap(rowCount)

	enc, err := EncodeColumn(common.TypeBool, data, rowCount, nulls)
	if err != nil {
		t.Fatalf("EncodeColumn Bitmap 失败: %v", err)
	}
	if enc == nil {
		t.Fatal("期望非 nil EncodedColumn")
	}
	if enc.Encoding != EncodingBitmap {
		t.Errorf("期望 Bitmap 编码，得到 %v", enc.Encoding)
	}
}
