package storage

import (
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
