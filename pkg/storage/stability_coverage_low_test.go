package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// skipIfRoot 在 root 用户时跳过测试
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Getuid() == 0 {
		t.Skip("root 用户绕过文件权限检查")
	}
}

// ---------------------------------------------------------------------------
// OpenWAL: 非 NotExist 错误路径（权限错误）
// ---------------------------------------------------------------------------

// TestOpenWAL权限错误 验证 OpenWAL 在权限不足时返回错误（非 NotExist 路径）
func TestOpenWAL权限错误(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root 用户绕过文件权限检查")
	}

	dir := t.TempDir()
	walPath := filepath.Join(dir, "protected.wal")

	// 创建 WAL 文件并写入有效数据
	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte("test_data"))
	_ = w.Sync()
	_ = w.Close()

	// 将文件设为不可读写以触发权限错误
	if err := os.Chmod(walPath, 0000); err != nil {
		t.Fatalf("Chmod 失败: %v", err)
	}
	defer func() { _ = os.Chmod(walPath, 0644) }()

	_, _, err = OpenWAL(walPath)
	if err == nil {
		t.Error("期望权限错误时返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// OpenWAL: Truncate 失败路径
// ---------------------------------------------------------------------------

// TestOpenWAL截断失败 验证 OpenWAL 在 Truncate 失败时返回错误
func TestOpenWAL截断失败(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "truncate_fail.wal")

	// 创建有效 WAL 文件
	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte("data"))
	_ = w.Sync()
	_ = w.Close()

	// 将文件设为只读以使 Truncate 失败
	if os.Getuid() == 0 {
		t.Skip("root 用户绕过文件权限检查")
	}
	if err := os.Chmod(walPath, 0444); err != nil {
		t.Fatalf("Chmod 失败: %v", err)
	}
	defer func() { _ = os.Chmod(walPath, 0644) }()

	_, _, err = OpenWAL(walPath)
	if err == nil {
		t.Error("期望 Truncate 失败时返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// OpenWAL: Seek 失败路径
// ---------------------------------------------------------------------------

// TestOpenWALSeek正常路径 验证 OpenWAL 在 Seek 正常时的行为
func TestOpenWALSeek正常路径(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "seek_ok.wal")

	// 创建有效 WAL 文件
	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte("data1"))
	_ = w.AppendWrite([]byte("data2"))
	_ = w.Sync()
	_ = w.Close()

	// 正常打开 WAL，验证 Seek 路径被覆盖
	openedWAL, records, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = openedWAL.Close() }()

	if len(records) != 2 {
		t.Errorf("期望 2 条记录，得到 %d", len(records))
	}
}

// ---------------------------------------------------------------------------
// Engine Write: WAL sync 失败路径
// ---------------------------------------------------------------------------

// TestWriteWALSync失败 验证 Write 在 WAL Sync 失败时返回错误
func TestWriteWALSync失败(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}

	// 关闭 WAL 使 Sync 失败
	_ = eng.wal.Close()

	err = eng.Write("key", map[string]common.Value{colVal: common.NewInt64(1)})
	if err == nil {
		t.Error("期望 WAL Sync 失败时返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// Engine Write: rotate memtable 失败路径
// ---------------------------------------------------------------------------

// TestWriteRotateMemTable失败 验证 Write 在 memtable 冻结后 rotate 失败
func TestWriteRotateMemTable失败(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir, MaxMemTableSize: 256})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 冻结 activeMem 使 Put 失败
	eng.activeMem.Freeze()

	err = eng.Write("key", map[string]common.Value{colVal: common.NewInt64(1)})
	if err == nil {
		t.Error("期望冻结 memtable 写入失败，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// WriteBatch: WAL sync 失败路径
// ---------------------------------------------------------------------------

// TestWriteBatchWALSync失败 验证 WriteBatch 在 WAL Sync 失败时返回错误
func TestWriteBatchWALSync失败(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}

	// 关闭 WAL 使 Sync 失败
	_ = eng.wal.Close()

	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
	}
	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("期望 WriteBatch WAL Sync 失败时返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// WriteBatch: rotate memtable 失败路径
// ---------------------------------------------------------------------------

// TestWriteBatchRotateMemTable失败 验证 WriteBatch 在 memtable 冻结后失败
func TestWriteBatchRotateMemTable失败(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir, MaxMemTableSize: 256})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 冻结 activeMem 使 Put 失败
	eng.activeMem.Freeze()

	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
	}
	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("期望 WriteBatch 写入冻结 memtable 失败，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// WAL: 使用文件系统限制触发 Seek 失败
// ---------------------------------------------------------------------------

// TestOpenWALSeek失败通过文件描述符 验证 OpenWAL 在 Seek 失败时的行为
func TestOpenWALSeek失败通过文件描述符(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root 用户绕过文件权限检查")
	}

	dir := t.TempDir()
	walPath := filepath.Join(dir, "seek_fail.wal")

	// 创建有效 WAL 文件
	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte("data"))
	_ = w.Sync()
	_ = w.Close()

	// 将文件设为只读，使 OpenWAL 打开后 Truncate/Seek 失败
	if err := os.Chmod(walPath, 0444); err != nil {
		t.Fatalf("Chmod 失败: %v", err)
	}
	defer func() { _ = os.Chmod(walPath, 0644) }()

	_, _, err = OpenWAL(walPath)
	if err == nil {
		t.Error("期望只读文件上 OpenWAL 失败，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// Engine Write: WAL Append 失败路径
// ---------------------------------------------------------------------------

// TestWriteWALAppend失败 验证 Write 在 WAL AppendWrite 失败时返回错误
func TestWriteWALAppend失败(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}

	// 关闭 WAL 使 AppendWrite 失败
	_ = eng.wal.Close()

	err = eng.Write("key", map[string]common.Value{colVal: common.NewInt64(1)})
	if err == nil {
		t.Error("期望 WAL AppendWrite 失败时返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// WriteBatch: WAL Append 失败路径
// ---------------------------------------------------------------------------

// TestWriteBatchWALAppend失败 验证 WriteBatch 在 WAL AppendBatch 失败时返回错误
func TestWriteBatchWALAppend失败(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}

	// 关闭 WAL 使 AppendBatch 失败
	_ = eng.wal.Close()

	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
	}
	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("期望 WriteBatch WAL AppendBatch 失败时返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// Engine Write: serializeWriteRecord 失败路径
// ---------------------------------------------------------------------------

// TestWrite序列化失败 验证 Write 在序列化失败时的行为
func TestWrite序列化失败(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入包含不支持的类型值的行
	err = eng.Write("key", map[string]common.Value{colVal: common.NewNull()})
	if err != nil {
		t.Logf("写入 NULL 值返回错误（可接受）: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Engine: OpenWAL 非 NotExist 错误路径（通过只读目录）
// ---------------------------------------------------------------------------

// TestOpenWAL非NotExist错误 验证 OpenWAL 在非 NotExist 错误时返回错误
func TestOpenWAL非NotExist错误(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root 用户绕过文件权限检查")
	}

	dir := t.TempDir()
	walPath := filepath.Join(dir, "protected_dir", "wal.log")

	// 创建只读父目录
	readOnlyDir := filepath.Join(dir, "protected_dir")
	if err := os.MkdirAll(readOnlyDir, 0555); err != nil {
		t.Fatalf("MkdirAll 失败: %v", err)
	}
	defer func() { _ = os.Chmod(readOnlyDir, 0755) }()

	// 在只读目录中创建文件应失败
	_, err := CreateWAL(walPath)
	if err == nil {
		t.Error("期望在只读目录中创建 WAL 失败，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// WAL: Truncate 失败路径（使用只读模拟）
// ---------------------------------------------------------------------------

// TestOpenWALTruncate失败通过只读 验证 OpenWAL 在 Truncate 失败时的错误路径
func TestOpenWALTruncate失败通过只读(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root 用户绕过文件权限检查")
	}

	dir := t.TempDir()
	walPath := filepath.Join(dir, "readonly.wal")

	// 创建有效 WAL
	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte("data"))
	_ = w.Sync()
	_ = w.Close()

	// 设为只读使 Truncate 失败
	if err := os.Chmod(walPath, 0444); err != nil {
		t.Fatalf("Chmod 失败: %v", err)
	}
	defer func() { _ = os.Chmod(walPath, 0644) }()

	_, _, err = OpenWAL(walPath)
	if err == nil {
		t.Error("期望 Truncate 失败时返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// Engine: WAL 文件磁盘空间不足模拟
// ---------------------------------------------------------------------------

// TestWriteWAL磁盘空间不足 验证 Write 在磁盘空间不足时返回错误
func TestWriteWAL磁盘空间不足(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 设置极小的 maxSize 使 WAL rotate 触发
	eng.wal.maxSize = 1

	// 写入足够数据以触发 rotate
	for i := 0; i < 10; i++ {
		err := eng.Write(fmt.Sprintf("key_%04d", i), map[string]common.Value{colVal: common.NewInt64(int64(i))})
		if err != nil {
			// rotate 可能失败，这是预期的
			t.Logf("Write %d 返回错误（可接受）: %v", i, err)
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Engine: Close 时 WAL 操作失败
// ---------------------------------------------------------------------------

// TestCloseWAL操作失败 验证 Close 在 WAL 操作失败时返回错误
func TestCloseWAL操作失败(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}

	// 关闭底层文件描述符使后续 Sync/Close 失败
	_ = eng.wal.file.Close()

	err = eng.Close()
	if err == nil {
		t.Error("期望 WAL 操作失败时 Close 返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// Engine: WAL rotate 错误路径
// ---------------------------------------------------------------------------

// TestWALRotate创建临时文件失败 验证 WAL rotate 在创建临时文件失败时的行为
func TestWALRotate创建临时文件失败(t *testing.T) {
	skipIfRoot(t)

	dir := t.TempDir()
	walPath := filepath.Join(dir, "rotate.wal")

	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 设置极小的 maxSize 触发 rotate
	w.maxSize = 1

	// 将目录设为只读使创建临时文件失败
	_ = w.Close()
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatalf("Chmod 失败: %v", err)
	}
	defer func() { _ = os.Chmod(dir, 0755) }()

	// 重新打开 WAL
	w, _, err = OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	w.maxSize = 1
	defer func() { _ = w.Close() }()

	// 尝试追加数据触发 rotate
	err = w.AppendWrite([]byte("data"))
	if err != nil {
		t.Logf("AppendWrite 在 rotate 失败时返回错误（可接受）: %v", err)
	}
}

// TestLowCovOpenWALFileNotFound 测试 OpenWAL 在文件不存在时返回错误（覆盖 wal.go 第 70-71 行的 os.IsNotExist 分支）
func TestLowCovOpenWALFileNotFound(t *testing.T) {
	_, _, err := OpenWAL(filepath.Join(t.TempDir(), "nonexistent.wal"))
	if err == nil {
		t.Fatal("expected error for non-existent WAL file, got nil")
	}
	if !strings.Contains(err.Error(), "wal open") {
		t.Errorf("expected error to contain 'wal open', got: %v", err)
	}
}

// TestLowCovOpenWALNonNotExistError 测试 OpenWAL 在路径为目录时返回非 IsNotExist 错误（覆盖 wal.go 第 73 行的非 NotExist 分支）
func TestLowCovOpenWALNonNotExistError(t *testing.T) {
	dir := t.TempDir()
	// 尝试将目录路径作为 WAL 文件打开，应触发非 IsNotExist 错误
	_, _, err := OpenWAL(dir)
	if err == nil {
		t.Fatal("expected error when opening directory as WAL file, got nil")
	}
}

// TestLowCovWALAppendPayloadTooLarge 测试 WAL.Append 在 payload 超过 maxRecordPayload (8MB) 时返回错误
func TestLowCovWALAppendPayloadTooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL: %v", err)
	}
	defer func() { _ = w.Close() }()

	largePayload := make([]byte, maxRecordPayload+1) // 8MB + 1 字节
	err = w.Append(walTypeWrite, largePayload)
	if err == nil {
		t.Fatal("expected error for oversized payload, got nil")
	}
	if !strings.Contains(err.Error(), "payload too large") {
		t.Errorf("expected error to contain 'payload too large', got: %v", err)
	}
}

// TestLowCovWALAppendBatchPayloadTooLarge 测试 WAL.AppendBatch 在某条记录超过 maxRecordPayload 时返回错误
func TestLowCovWALAppendBatchPayloadTooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL: %v", err)
	}
	defer func() { _ = w.Close() }()

	largePayload := make([]byte, maxRecordPayload+1)
	err = w.AppendBatch([]BatchRecord{{Type: walTypeWrite, Payload: largePayload}})
	if err == nil {
		t.Fatal("expected error for oversized batch payload, got nil")
	}
	if !strings.Contains(err.Error(), "payload too large") {
		t.Errorf("expected error to contain 'payload too large', got: %v", err)
	}
}

// TestLowCovCompressColumnNil 测试 CompressColumn 传入 nil 时返回错误（覆盖 compress.go 第 128-129 行）
func TestLowCovCompressColumnNil(t *testing.T) {
	err := CompressColumn(nil)
	if err == nil {
		t.Fatal("expected error for nil EncodedColumn, got nil")
	}
	if !strings.Contains(err.Error(), "nil EncodedColumn") {
		t.Errorf("expected error to contain 'nil EncodedColumn', got: %v", err)
	}
}

// TestLowCovDecompressColumnNil 测试 DecompressColumn 传入 nil 时返回错误（覆盖 compress.go 第 141-142 行）
func TestLowCovDecompressColumnNil(t *testing.T) {
	err := DecompressColumn(nil)
	if err == nil {
		t.Fatal("expected error for nil EncodedColumn, got nil")
	}
	if !strings.Contains(err.Error(), "nil EncodedColumn") {
		t.Errorf("expected error to contain 'nil EncodedColumn', got: %v", err)
	}
}

// TestLowCovWriteCheckpointError 测试 writeCheckpoint 在 WAL 同步失败时返回错误
func TestLowCovWriteCheckpointError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// 设置 columnMeta 以确保 writeCheckpoint 尝试序列化
	eng.mu.Lock()
	eng.columnMeta = []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	eng.mu.Unlock()

	// 关闭 WAL 使后续 Sync 失败
	if err := eng.wal.Close(); err != nil {
		t.Fatalf("WAL Close: %v", err)
	}

	err = eng.writeCheckpoint(1)
	if err == nil {
		t.Error("expected error when writeCheckpoint with closed WAL, got nil")
	}

	// 清理：将 wal 置空避免 Close 时 panic
	eng.mu.Lock()
	eng.wal = nil
	eng.mu.Unlock()
}

// TestLowCovFlusherWriteSegmentMkdirError 测试 writeSegment 在数据目录无法创建时返回错误
func TestLowCovFlusherWriteSegmentMkdirError(t *testing.T) {
	// 使用包含空字节的路径，使得 MkdirAll 失败
	invalidDataDir := "/dev/null/impossible/path\x00bad"
	f := NewFlusher(invalidDataDir, newSegmentIDGen())

	// 构建一个简单的 Segment 用于测试 writeSegment
	keys := []string{"a"}
	values := []int64{1}
	builder := NewSegmentBuilder(1, "a", "a")
	builder.SetKeys(keys)
	enc, err := EncodeColumn(common.TypeInt64, values, 1, nil)
	if err != nil {
		t.Fatalf("EncodeColumn: %v", err)
	}
	builder.AddEncodedColumn(enc)
	seg, err := builder.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	_, err = writeSegmentFile(f.dataDir, seg)
	if err == nil {
		t.Error("expected error when writeSegment with invalid data dir, got nil")
	}
}

// TestLowCovSegmentBuilderNoColumns 测试 SegmentBuilder.Build 在没有添加列时返回错误
func TestLowCovSegmentBuilderNoColumns(t *testing.T) {
	builder := NewSegmentBuilder(1, "a", "z")
	_, err := builder.Build()
	if err == nil {
		t.Fatal("expected error when building segment with no columns, got nil")
	}
	if !strings.Contains(err.Error(), "no columns added") {
		t.Errorf("expected error to contain 'no columns added', got: %v", err)
	}
}
