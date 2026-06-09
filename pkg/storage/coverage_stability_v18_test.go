package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestOpenWAL_TruncateError 测试 OpenWAL 中 Truncate 失败的错误路径。
func TestOpenWAL_TruncateError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping: root user bypasses file permission checks")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("record1"))
	_ = w.Sync()
	_ = w.Close()
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatalf("chmod failed: %v", err)
	}
	defer func() { _ = os.Chmod(path, 0644) }()
	_, _, err = OpenWAL(path)
	if err == nil {
		t.Error("expected error when Truncate fails, got nil")
	}
}

// TestOpenWAL_PermissionError 测试 OpenWAL 在权限不足时的错误路径（非 os.IsNotExist）。
func TestOpenWAL_PermissionError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping: root user bypasses file permission checks")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.Close()
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatalf("chmod failed: %v", err)
	}
	defer func() { _ = os.Chmod(path, 0644) }()
	_, _, err = OpenWAL(path)
	if err == nil {
		t.Error("expected error when opening file with no permissions, got nil")
	}
	if os.IsNotExist(err) {
		t.Errorf("expected non-NotExist error, got NotExist: %v", err)
	}
}

// TestWrite_GroupCommitSync 测试 Write 在 GroupCommit 模式下的同步路径。
func TestWrite_GroupCommitSync(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir: dir, SyncMode: SyncGroupCommit, SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()
	err = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(42)})
	if err != nil {
		t.Fatalf("Write with GroupCommit failed: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	row, ok := eng.Get("key1")
	if !ok {
		t.Fatal("expected to find key1")
	}
	if row.Columns[colVal].Int64 != 42 {
		t.Errorf("expected value 42, got %d", row.Columns[colVal].Int64)
	}
}

// TestWrite_GroupCommitMultipleWrites 测试 GroupCommit 模式下多次写入共享 sync。
func TestWrite_GroupCommitMultipleWrites(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir: dir, SyncMode: SyncGroupCommit, SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()
	for i := 0; i < 10; i++ {
		key := filepath.Base(dir) + "_" + string(rune('a'+i))
		err = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))})
		if err != nil {
			t.Fatalf("Write %d with GroupCommit failed: %v", i, err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	count := 0
	for range eng.Scan("", "\xff") {
		count++
	}
	if count != 10 {
		t.Errorf("expected 10 entries, got %d", count)
	}
}

// TestWriteCheckpoint_GroupCommitSyncNow 测试 writeCheckpoint 在 GroupCommit 模式下调用 SyncNow。
func TestWriteCheckpoint_GroupCommitSyncNow(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir: dir, SyncMode: SyncGroupCommit, SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()
	if err := eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush with GroupCommit failed: %v", err)
	}
}

// TestWriteBatch_WALAppendBatchError 测试 WriteBatch 当 WAL AppendBatch 失败时的错误路径。
func TestWriteBatch_WALAppendBatchError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	if err := eng.wal.Close(); err != nil {
		t.Fatalf("WAL Close failed: %v", err)
	}
	rows := []WriteRow{{Key: "k1", Values: map[string]common.Value{colVal: common.NewInt64(1)}}}
	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("expected error when WAL AppendBatch fails, got nil")
	}
}

// TestMaybeRotate_CloseOldFileError 测试 maybeRotate 在关闭旧文件失败时的错误路径。
func TestMaybeRotate_CloseOldFileError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	if err := w.file.Close(); err != nil {
		t.Fatalf("file Close failed: %v", err)
	}
	w.offset = w.maxSize + 1
	err = w.maybeRotate()
	if err == nil {
		t.Error("expected error when closing old file fails during rotate, got nil")
	}
}

// TestDecompress_InvalidData 测试 Decompress 处理无效压缩数据时的错误。
func TestDecompress_InvalidData(t *testing.T) {
	_, err := Decompress([]byte{0xFF, 0xFE, 0xFD, 0xFC, 0xFB})
	if err == nil {
		t.Error("expected error for invalid compressed data, got nil")
	}
}

// TestCompressColumn_NormalPath 测试 CompressColumn/DecompressColumn 正常路径。
func TestCompressColumn_NormalPath(t *testing.T) {
	enc := &EncodedColumn{Encoding: EncodingPlain, Type: common.TypeInt64, Data: []byte{1, 2, 3, 4, 5, 6, 7, 8}}
	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn failed: %v", err)
	}
	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn failed: %v", err)
	}
}

// TestWrite_PutError 测试 Write 在 MemTable.Put 失败时的错误路径。
func TestWrite_PutError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()
	eng.activeMem.Freeze()
	err = eng.Write("key", map[string]common.Value{colVal: common.NewInt64(1)})
	if err == nil {
		t.Error("expected error when MemTable.Put fails, got nil")
	}
}

// TestWALTruncate_NormalPath 测试 WAL.Truncate 方法正常路径。
func TestWALTruncate_NormalPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("record1"))
	_ = w.AppendWrite([]byte("record2"))
	_ = w.Sync()
	if w.Size() == 0 {
		t.Fatal("expected non-zero size after writes")
	}
	if err := w.Truncate(); err != nil {
		t.Fatalf("Truncate failed: %v", err)
	}
	if w.Size() != 0 {
		t.Errorf("expected size 0 after truncate, got %d", w.Size())
	}
	if err := w.AppendWrite([]byte("after_truncate")); err != nil {
		t.Fatalf("AppendWrite after truncate failed: %v", err)
	}
	_ = w.Close()
}

// TestWALTruncate_SyncError 测试 WAL.Truncate 在 Sync 失败时的错误路径。
func TestWALTruncate_SyncError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	if err := w.file.Close(); err != nil {
		t.Fatalf("file Close failed: %v", err)
	}
	err = w.Truncate()
	if err == nil {
		t.Error("expected error when Sync fails during Truncate, got nil")
	}
}

// TestWALClose_SyncError 测试 WAL.Close 在 Sync 失败时的错误路径。
func TestWALClose_SyncError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	if err := w.file.Close(); err != nil {
		t.Fatalf("file Close failed: %v", err)
	}
	err = w.Close()
	if err == nil {
		t.Error("expected error when Sync fails during Close, got nil")
	}
}

// TestGroupCommitter_CloseIdempotent 测试 GroupCommitter.Close 可以安全地多次调用。
func TestGroupCommitter_CloseIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()
	gc := NewGroupCommitter(w, 1*time.Millisecond)
	gc.Close()
	gc.Close()
}

// TestGroupCommitter_SubmitAndSync 测试 GroupCommitter 的 Submit 和 SyncNow 方法。
func TestGroupCommitter_SubmitAndSync(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()
	gc := NewGroupCommitter(w, 1*time.Millisecond)
	defer gc.Close()
	_ = w.AppendWrite([]byte("data"))
	ch := gc.Submit()
	if ch == nil {
		t.Fatal("Submit returned nil channel")
	}
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for GroupCommit sync")
	}
	_ = w.AppendWrite([]byte("data2"))
	gc.SyncNow()
}

// TestWrite_RotateMemTable 测试 Write 触发 MemTable 轮转的路径。
func TestWrite_RotateMemTable(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir, MaxMemTableSize: 256})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()
	for i := 0; i < 100; i++ {
		key := string(rune('a' + i%26))
		err = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))})
		if err != nil {
			t.Fatalf("Write %d failed: %v", i, err)
		}
	}
}

// TestWriteBatch_RotateMemTable 测试 WriteBatch 触发 MemTable 轮转的路径。
func TestWriteBatch_RotateMemTable(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir, MaxMemTableSize: 256})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()
	for i := 0; i < 20; i++ {
		rows := []WriteRow{{Key: string(rune('a' + i%26)), Values: map[string]common.Value{colVal: common.NewInt64(int64(i))}}}
		if err := eng.WriteBatch(rows); err != nil {
			t.Fatalf("WriteBatch %d failed: %v", i, err)
		}
	}
}

// TestWriteCheckpoint_WALClosedError 测试 writeCheckpoint 在 WAL 关闭时的错误路径。
func TestWriteCheckpoint_WALClosedError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	if err := eng.wal.Close(); err != nil {
		t.Fatalf("WAL Close failed: %v", err)
	}
	err = eng.writeCheckpoint(1)
	if err == nil {
		t.Error("expected error when WAL is closed during writeCheckpoint, got nil")
	}
}

// TestWALAppendWrite_PayloadTooLarge 测试 AppendWrite 在 payload 过大时的错误。
func TestWALAppendWrite_PayloadTooLarge(t *testing.T) {
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
		t.Error("expected error for oversized payload, got nil")
	}
}

// TestStartScheduler_Idempotent 测试 StartScheduler 可以安全地多次调用。
func TestStartScheduler_Idempotent(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()
	cfg := SchedulerConfig{FlushInterval: 1 * time.Hour, CompactInterval: 1 * time.Hour, WALCleanInterval: 1 * time.Hour}
	eng.StartScheduler(cfg)
	eng.StartScheduler(cfg)
}

// TestSchedulerStats_NotStarted 测试未启动调度器时 SchedulerStats 返回 false。
func TestSchedulerStats_NotStarted(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()
	_, ok := eng.SchedulerStats()
	if ok {
		t.Error("expected ok=false when scheduler is not started")
	}
}

// TestEncodeColumn_Float64Plain 测试 EncodeColumn 对 Float64 类型使用 Plain 编码。
func TestEncodeColumn_Float64Plain(t *testing.T) {
	data := []float64{1.1, 2.2, 3.3}
	enc, err := EncodeColumn(common.TypeFloat64, data, 3, nil)
	if err != nil {
		t.Fatalf("EncodeColumn Float64 failed: %v", err)
	}
	if enc.Encoding != EncodingPlain {
		t.Errorf("expected Plain encoding for Float64, got %v", enc.Encoding)
	}
}

// TestWALAppendCommit_GroupCommit 测试 AppendCommit 在 GroupCommit 模式下的行为。
func TestWALAppendCommit_GroupCommit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	gc := NewGroupCommitter(w, 1*time.Millisecond)
	defer gc.Close()
	if err := w.AppendCommit([]byte("commit_data")); err != nil {
		t.Fatalf("AppendCommit failed: %v", err)
	}
	gc.SyncNow()
	_ = w.Close()
}

// TestWALAppendCheckpoint_GroupCommit 测试 AppendCheckpoint 在 GroupCommit 模式下的行为。
func TestWALAppendCheckpoint_GroupCommit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	gc := NewGroupCommitter(w, 1*time.Millisecond)
	defer gc.Close()
	if err := w.AppendCheckpoint([]byte("checkpoint_data")); err != nil {
		t.Fatalf("AppendCheckpoint failed: %v", err)
	}
	gc.SyncNow()
	_ = w.Close()
}

// TestWALSync 测试 WAL.Sync 正常路径。
func TestWALSync(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()
	_ = w.AppendWrite([]byte("data"))
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
}

// TestEngineSegments 测试 Segments 方法返回正确的副本。
func TestEngineSegments(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()
	if len(eng.Segments()) != 0 {
		t.Errorf("expected 0 segments")
	}
}

// TestEngineSegmentCount 测试 SegmentCount 方法。
func TestEngineSegmentCount(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()
	if eng.SegmentCount() != 0 {
		t.Errorf("expected 0 segments")
	}
}

// TestWALSize_Accessor 测试 WAL.Size 方法。
func TestWALSize_Accessor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()
	if w.Size() != 0 {
		t.Errorf("expected initial size 0, got %d", w.Size())
	}
	_ = w.AppendWrite([]byte("data"))
	if w.Size() == 0 {
		t.Error("expected non-zero size after write")
	}
}
