package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestEncodePlainFloat64TypeAssertionError 测试 encodePlain 在 Float64 类型断言失败时的错误路径。
func TestEncodePlainFloat64TypeAssertionError(t *testing.T) {
	// 传入 string 切片而非 []float64，触发类型断言失败
	_, err := encodePlain(common.TypeFloat64, "not floats", 1, nil)
	if err == nil {
		t.Error("expected error when Float64 type assertion fails, got nil")
	}
}

// TestEncodePlainTimestampTypeAssertionError 测试 encodePlain 在 Timestamp 类型断言失败时的错误路径。
func TestEncodePlainTimestampTypeAssertionError(t *testing.T) {
	// 传入 string 而非 []int64，触发 Timestamp 类型断言失败
	_, err := encodePlain(common.TypeTimestamp, "not ints", 1, nil)
	if err == nil {
		t.Error("expected error when Timestamp type assertion fails, got nil")
	}
}

// TestEncodePlainBoolUnsupportedType 测试 encodePlain 在 TypeBool 时进入 default 分支返回错误。
func TestEncodePlainBoolUnsupportedType(t *testing.T) {
	// TypeBool 在 encodePlain 中走 default 分支，返回 unsupported type 错误
	_, err := encodePlain(common.TypeBool, nil, 1, nil)
	if err == nil {
		t.Error("expected error for TypeBool in encodePlain, got nil")
	}
}

// TestCompressEmptyDataV19 测试 Compress 在输入为空字节切片时返回 nil, nil。
func TestCompressEmptyDataV19(t *testing.T) {
	result, err := Compress([]byte{})
	if err != nil {
		t.Fatalf("expected nil error for empty data, got: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for empty data, got: %v", result)
	}
}

// TestDecompressEmptyDataV19 测试 Decompress 在输入为空字节切片时返回 nil, nil。
func TestDecompressEmptyDataV19(t *testing.T) {
	result, err := Decompress([]byte{})
	if err != nil {
		t.Fatalf("expected nil error for empty data, got: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for empty data, got: %v", result)
	}
}

// TestCompressColumnNilInput 测试 CompressColumn 在输入为 nil 时返回错误。
func TestCompressColumnNilInput(t *testing.T) {
	err := CompressColumn(nil)
	if err == nil {
		t.Error("expected error for nil EncodedColumn, got nil")
	}
}

// TestDecompressColumnNilInput 测试 DecompressColumn 在输入为 nil 时返回错误。
func TestDecompressColumnNilInput(t *testing.T) {
	err := DecompressColumn(nil)
	if err == nil {
		t.Error("expected error for nil EncodedColumn, got nil")
	}
}

// TestWriteGroupCommitSyncWait 测试 Write 在 GroupCommit 模式下 syncCh 等待路径被正确执行。
func TestWriteGroupCommitSyncWait(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir:      dir,
		SyncMode:     SyncGroupCommit,
		SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入数据，验证 syncCh 等待路径被覆盖
	err = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(42)})
	if err != nil {
		t.Fatalf("Write with GroupCommit failed: %v", err)
	}

	// 等待 GroupCommitter 完成同步
	time.Sleep(50 * time.Millisecond)

	// 验证数据可读
	row, ok := eng.Get("key1")
	if !ok {
		t.Fatal("expected to find key1 after GroupCommit write")
	}
	if row.Columns[colVal].Int64 != 42 {
		t.Errorf("expected value 42, got %d", row.Columns[colVal].Int64)
	}
}

// TestWriteCheckpointWALSyncError 测试 writeCheckpoint 在 WAL Sync 失败时的错误路径。
func TestWriteCheckpointWALSyncError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}

	// 写入数据使引擎有内容
	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})

	// 关闭 WAL 以触发 writeCheckpoint 中 AppendCheckpoint/Sync 错误
	_ = eng.wal.Close()

	// 调用 Flush 触发 writeCheckpoint
	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	err = eng.Flush(cols)
	if err == nil {
		t.Error("expected error when writeCheckpoint WAL sync fails, got nil")
	}
}

// TestWriteBatchWALClosedAppendError 测试 WriteBatch 在 WAL 关闭后 AppendBatch 失败的错误路径。
func TestWriteBatchWALClosedAppendError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}

	// 关闭 WAL 以触发 AppendBatch 错误
	_ = eng.wal.Close()

	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
	}
	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("expected error when WriteBatch WAL AppendBatch fails, got nil")
	}
}

// TestWriteBatchWALClosedSyncError 测试 WriteBatch 在 WAL 文件描述符关闭后 Sync 失败的错误路径。
func TestWriteBatchWALClosedSyncError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}

	// 关闭 WAL 文件描述符但保留 WAL 结构，以触发 Sync 错误
	_ = eng.wal.file.Close()

	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
	}
	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("expected error when WriteBatch WAL Sync fails, got nil")
	}
}

// TestOpenWALDirError 测试 OpenWAL 在路径为目录时触发非 IsNotExist 错误。
func TestOpenWALDirError(t *testing.T) {
	dir := t.TempDir()
	// 创建一个目录作为 WAL 路径，用 O_RDWR 打开目录应失败
	dirPath := filepath.Join(dir, "wal_dir")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	_, _, err := OpenWAL(dirPath)
	if err == nil {
		t.Error("expected error when opening directory as WAL, got nil")
	}
	// 确保不是 IsNotExist 错误
	if os.IsNotExist(err) {
		t.Errorf("expected non-NotExist error, got NotExist: %v", err)
	}
}

// TestFlusherWriteSegmentMkdirError 测试 writeSegment 在 MkdirAll 失败时的错误路径。
func TestFlusherWriteSegmentMkdirError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping: root user bypasses file permission checks")
	}

	dir := t.TempDir()
	// 创建一个只读父目录，使 MkdirAll 失败
	readOnlyDir := filepath.Join(dir, "readonly")
	if err := os.MkdirAll(readOnlyDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	// 将父目录设为只读，使子目录创建失败
	if err := os.Chmod(readOnlyDir, 0555); err != nil {
		t.Fatalf("Chmod failed: %v", err)
	}
	defer func() { _ = os.Chmod(readOnlyDir, 0755) }()

	flusher := NewFlusher(filepath.Join(readOnlyDir, "nested", "data"))
	// 直接调用 writeSegment，触发 MkdirAll 错误
	seg := &Segment{ID: 1}
	_, err := flusher.writeSegment(seg)
	if err == nil {
		t.Error("expected error when MkdirAll fails, got nil")
	}
}

// TestScanRangeMergeIteratorErrorV19 测试 ScanRange 在 MergeIterator 遇到错误时返回 nil。
func TestScanRangeMergeIteratorErrorV19(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入数据并刷盘，创建 segment
	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})
	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	// 损坏 segment 的列数据，使 segmentIterator 解码失败
	eng.mu.Lock()
	for _, seg := range eng.segments {
		for i := range seg.Columns {
			// 将压缩数据替换为无效数据，触发 DecompressColumn 错误
			seg.Columns[i].Data = []byte{0xFF, 0xFE, 0xFD, 0xFC}
		}
	}
	eng.mu.Unlock()

	// ScanRange 在 MergeIterator 遇到错误时应返回 nil
	eng.mu.RLock()
	results := eng.ScanRange("", "\xff")
	eng.mu.RUnlock()

	if results != nil {
		t.Errorf("expected nil results when MergeIterator encounters error, got %d entries", len(results))
	}
}
