package storage

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

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
	f := NewFlusher(invalidDataDir)

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

	_, err = f.writeSegment(seg)
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
