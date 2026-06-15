package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestWriteAndSyncFileSuccess 测试 writeAndSyncFile 成功写入并同步数据
func TestWriteAndSyncFileSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	fileName := filepath.Join(tmpDir, "test_sync.widb")
	data := []byte("hello fsync")

	if err := writeAndSyncFile(fileName, data, 0644); err != nil {
		t.Fatalf("writeAndSyncFile: %v", err)
	}

	// 验证文件内容
	got, err := os.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("content mismatch: got %q, want %q", got, data)
	}
}

// TestWriteAndSyncFileOverwrite 测试 writeAndSyncFile 覆盖已有文件
func TestWriteAndSyncFileOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	fileName := filepath.Join(tmpDir, "test_overwrite.widb")

	// 先写入初始内容
	if err := writeAndSyncFile(fileName, []byte("old data"), 0644); err != nil {
		t.Fatalf("first write: %v", err)
	}

	// 覆盖写入新内容
	newData := []byte("new data")
	if err := writeAndSyncFile(fileName, newData, 0644); err != nil {
		t.Fatalf("overwrite: %v", err)
	}

	got, err := os.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(got) != string(newData) {
		t.Errorf("content mismatch after overwrite: got %q, want %q", got, newData)
	}
}

// TestWriteAndSyncFileEmptyData 测试 writeAndSyncFile 写入空数据
func TestWriteAndSyncFileEmptyData(t *testing.T) {
	tmpDir := t.TempDir()
	fileName := filepath.Join(tmpDir, "test_empty.widb")

	if err := writeAndSyncFile(fileName, []byte{}, 0644); err != nil {
		t.Fatalf("writeAndSyncFile with empty data: %v", err)
	}

	info, err := os.Stat(fileName)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("expected 0 bytes, got %d", info.Size())
	}
}

// TestWriteAndSyncFileOpenError 测试 writeAndSyncFile 在无法创建文件时返回错误
// 通过将文件路径设为一个已存在目录来触发 OpenFile 失败
func TestWriteAndSyncFileOpenError(t *testing.T) {
	tmpDir := t.TempDir()
	// tmpDir 本身是一个目录，不能作为文件打开
	if err := writeAndSyncFile(tmpDir, []byte("data"), 0644); err == nil {
		t.Error("expected error when opening a directory as file, got nil")
	}
}

// TestWriteAndSyncFileWriteError 测试 writeAndSyncFile 在写入失败时返回错误
// 通过在只读目录中创建文件来触发写入失败
func TestWriteAndSyncFileWriteError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping: root can write to read-only directories")
	}

	tmpDir := t.TempDir()
	readonlyDir := filepath.Join(tmpDir, "readonly")
	if err := os.MkdirAll(readonlyDir, 0555); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	fileName := filepath.Join(readonlyDir, "test.widb")
	err := writeAndSyncFile(fileName, []byte("data"), 0644)
	if err == nil {
		t.Error("expected error when writing to read-only directory, got nil")
	}
}

// TestWriteAndSyncFileLargeData 测试 writeAndSyncFile 写入较大数据
func TestWriteAndSyncFileLargeData(t *testing.T) {
	tmpDir := t.TempDir()
	fileName := filepath.Join(tmpDir, "test_large.widb")

	// 写入 64KB 数据
	data := make([]byte, 64*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	if err := writeAndSyncFile(fileName, data, 0644); err != nil {
		t.Fatalf("writeAndSyncFile large data: %v", err)
	}

	got, err := os.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if len(got) != len(data) {
		t.Errorf("size mismatch: got %d, want %d", len(got), len(data))
	}
	for i := range data {
		if got[i] != data[i] {
			t.Errorf("byte %d mismatch: got %d, want %d", i, got[i], data[i])
			break
		}
	}
}

// TestWriteAndSyncFilePerm 测试 writeAndSyncFile 文件权限设置
func TestWriteAndSyncFilePerm(t *testing.T) {
	tmpDir := t.TempDir()
	fileName := filepath.Join(tmpDir, "test_perm.widb")

	if err := writeAndSyncFile(fileName, []byte("data"), 0644); err != nil {
		t.Fatalf("writeAndSyncFile: %v", err)
	}

	info, err := os.Stat(fileName)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0644 {
		t.Errorf("permission mismatch: got %04o, want %04o", perm, 0644)
	}
}

// TestFlusherWriteSegmentFsync 测试 Flusher.writeSegment 通过 fsync 确保数据持久化
// 验证写入后的文件内容可以被正确读取和反序列化
func TestFlusherWriteSegmentFsync(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	mem := NewMemTable()
	_, _, _ = mem.Put("k1", Row{Version: 1, Columns: map[string]common.Value{
		colVal: common.NewInt64(42),
	}})
	_, _, _ = mem.Put("k2", Row{Version: 2, Columns: map[string]common.Value{
		colVal: common.NewInt64(84),
	}})

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	seg, err := flusher.Flush(mem, cols)
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// 验证文件存在且可读
	if seg.FilePath == "" {
		t.Fatal("expected non-empty FilePath")
	}
	data, err := os.ReadFile(seg.FilePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Error("segment file is empty")
	}

	// 验证可以反序列化
	restored, err := DeserializeSegment(data)
	if err != nil {
		t.Fatalf("DeserializeSegment: %v", err)
	}
	if restored.RowCount != 2 {
		t.Errorf("RowCount: got %d, want 2", restored.RowCount)
	}
}

// TestCompactorBuildSegmentFsync 测试 Compactor.buildSegment 通过 fsync 确保数据持久化
func TestCompactorBuildSegmentFsync(t *testing.T) {
	tmpDir := t.TempDir()
	compactor := NewCompactor(tmpDir)
	compactor.nextID.Store(1)

	rows := []memRow{
		{Key: "k1", Values: []common.Value{common.NewInt64(10)}},
		{Key: "k2", Values: []common.Value{common.NewInt64(20)}},
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	seg, err := compactor.buildSegment(rows, cols)
	if err != nil {
		t.Fatalf("buildSegment: %v", err)
	}

	// 验证文件存在且可读
	if seg.FilePath == "" {
		t.Fatal("expected non-empty FilePath")
	}
	data, err := os.ReadFile(seg.FilePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Error("segment file is empty")
	}

	// 验证可以反序列化
	restored, err := DeserializeSegment(data)
	if err != nil {
		t.Fatalf("DeserializeSegment: %v", err)
	}
	if restored.RowCount != 2 {
		t.Errorf("RowCount: got %d, want 2", restored.RowCount)
	}
}
