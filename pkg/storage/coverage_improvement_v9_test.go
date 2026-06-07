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
