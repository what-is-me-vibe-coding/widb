package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateWAL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	if w.Size() != 0 {
		t.Errorf("expected size 0, got %d", w.Size())
	}

	_ = w.Close()

	_, err = os.Stat(path)
	if err != nil {
		t.Fatalf("wal file not created: %v", err)
	}
}

func TestWALAppendWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	payload := []byte("hello wal")
	if err := w.AppendWrite(payload); err != nil {
		t.Fatalf("AppendWrite failed: %v", err)
	}

	if err := w.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	if w.Size() == 0 {
		t.Fatal("expected non-zero size after write")
	}
}

func TestWALAppendCommit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	if err := w.AppendCommit([]byte("commit data")); err != nil {
		t.Fatalf("AppendCommit failed: %v", err)
	}
}

func TestWALAppendCheckpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	if err := w.AppendCheckpoint([]byte("checkpoint data")); err != nil {
		t.Fatalf("AppendCheckpoint failed: %v", err)
	}
}

func TestWALLargePayload(t *testing.T) {
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
		t.Fatal("expected error for oversized payload")
	}
}

func TestWALRotate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	w.maxSize = walMetaSize + 100

	for i := 0; i < 10; i++ {
		payload := []byte("test data for rotation")
		if err := w.AppendWrite(payload); err != nil {
			t.Fatalf("AppendWrite #%d failed: %v", i, err)
		}
	}

	_, err = os.Stat(path + ".prev")
	if err != nil {
		t.Fatalf("previous WAL file not created: %v", err)
	}
}

func TestWALConcurrentWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	const goroutines = 10
	const writesPerRoutine = 100
	done := make(chan bool)

	for i := 0; i < goroutines; i++ {
		go func() {
			for j := 0; j < writesPerRoutine; j++ {
				if err := w.AppendWrite([]byte("concurrent")); err != nil {
					t.Errorf("concurrent write failed: %v", err)
				}
			}
			done <- true
		}()
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}

	_ = w.Sync()
	_ = w.Close()

	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	expected := goroutines * writesPerRoutine
	if len(recs) != expected {
		t.Errorf("expected %d records, got %d", expected, len(recs))
	}
}

func TestCreateWALInvalidDir(t *testing.T) {
	// Try to create WAL in a non-existent directory
	_, err := CreateWAL("/nonexistent/dir/test.wal")
	if err == nil {
		t.Error("expected error creating WAL in invalid directory")
	}
}

func TestWALMaybeRotate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// Set a very small max size to trigger rotation quickly
	w.maxSize = walMetaSize + 50

	// Write enough data to trigger rotation
	for i := 0; i < 5; i++ {
		if err := w.AppendWrite([]byte("test data for rotation")); err != nil {
			t.Fatalf("AppendWrite #%d failed: %v", i, err)
		}
	}

	_ = w.Close()

	// Verify the .prev file was created (rotation happened)
	_, err = os.Stat(path + ".prev")
	if err != nil {
		t.Fatalf("previous WAL file not created after rotation: %v", err)
	}

	// Verify the current WAL file still exists
	_, err = os.Stat(path)
	if err != nil {
		t.Fatalf("current WAL file not found: %v", err)
	}
}

func TestWALTruncate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// Write some data
	_ = w.AppendWrite([]byte("data to be truncated"))
	_ = w.Sync()

	if w.Size() == 0 {
		t.Fatal("expected non-zero size before truncate")
	}

	// Truncate the WAL
	if err := w.Truncate(); err != nil {
		t.Fatalf("Truncate failed: %v", err)
	}

	if w.Size() != 0 {
		t.Errorf("expected size 0 after truncate, got %d", w.Size())
	}

	// Verify we can still write after truncation
	if err := w.AppendWrite([]byte("after truncate")); err != nil {
		t.Fatalf("AppendWrite after truncate failed: %v", err)
	}

	_ = w.Close()
}

func TestWALSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	initialSize := w.Size()
	if initialSize != 0 {
		t.Errorf("expected initial size 0, got %d", initialSize)
	}

	_ = w.AppendWrite([]byte("test"))

	afterSize := w.Size()
	if afterSize <= initialSize {
		t.Errorf("expected size to increase after write, got %d", afterSize)
	}
}

// TestOpenWALPermissionError 测试打开目录路径作为 WAL 文件（应得到非 NotExist 错误）
func TestOpenWALPermissionError(t *testing.T) {
	dir := t.TempDir()
	// 尝试打开目录路径作为 WAL 文件，应得到非 NotExist 错误
	_, _, err := OpenWAL(dir)
	if err == nil {
		t.Fatal("expected error when opening directory as WAL file")
	}
	// 确保不是 NotExist 错误
	if os.IsNotExist(err) {
		t.Errorf("expected non-NotExist error, got NotExist: %v", err)
	}
}

// TestOpenWALWithValidOffsetRecovery 测试 OpenWAL 恢复后偏移量正确设置，且可以继续追加
func TestOpenWALWithValidOffsetRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建 WAL 并写入多条记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	_ = w.AppendWrite([]byte("record1"))
	_ = w.AppendWrite([]byte("record2"))
	_ = w.AppendWrite([]byte("record3"))
	_ = w.Sync()

	sizeAfterWrite := w.Size()
	if sizeAfterWrite == 0 {
		t.Fatal("expected non-zero size after writes")
	}
	_ = w.Close()

	// 重新打开 WAL，验证偏移量正确恢复
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}

	if len(recs) != 3 {
		t.Fatalf("expected 3 records, got %d", len(recs))
	}

	// 验证恢复后的偏移量与写入后的一致
	if recovered.Size() != sizeAfterWrite {
		t.Errorf("expected offset %d after recovery, got %d", sizeAfterWrite, recovered.Size())
	}

	// 验证恢复后可以继续追加记录
	if err := recovered.AppendWrite([]byte("record4")); err != nil {
		t.Fatalf("AppendWrite after recovery failed: %v", err)
	}
	_ = recovered.Sync()
	_ = recovered.Close()

	// 再次打开验证所有 4 条记录
	recovered2, recs2, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("second OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered2.Close() }()

	if len(recs2) != 4 {
		t.Fatalf("expected 4 records, got %d", len(recs2))
	}
}

// TestOpenWALTruncateAfterPartialData 测试 WAL 文件末尾有垃圾数据时，OpenWAL 会截断到有效偏移量
func TestOpenWALTruncateAfterPartialData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建 WAL 并写入记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("valid1"))
	_ = w.AppendWrite([]byte("valid2"))
	_ = w.Sync()
	_ = w.Close()

	// 读取文件内容，获取有效数据的长度
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read WAL file: %v", err)
	}
	validSize := len(data)

	// 在末尾追加垃圾数据（模拟崩溃时的部分写入）
	garbage := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x03, 0x04}
	modifiedData := make([]byte, validSize+len(garbage))
	copy(modifiedData, data)
	copy(modifiedData[validSize:], garbage)

	if err := os.WriteFile(path, modifiedData, 0644); err != nil {
		t.Fatalf("write modified file: %v", err)
	}

	// 打开 WAL，验证文件被截断到有效偏移量
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	// 应该恢复 2 条有效记录
	if len(recs) != 2 {
		t.Fatalf("expected 2 valid records, got %d", len(recs))
	}
	if string(recs[0].Payload) != "valid1" {
		t.Errorf("record 0: expected 'valid1', got %q", string(recs[0].Payload))
	}
	if string(recs[1].Payload) != "valid2" {
		t.Errorf("record 1: expected 'valid2', got %q", string(recs[1].Payload))
	}

	// 验证文件已被截断到有效偏移量
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat WAL file: %v", err)
	}
	if fileInfo.Size() != int64(validSize) {
		t.Errorf("expected file size %d after truncation, got %d", validSize, fileInfo.Size())
	}

	// 验证恢复后可以继续追加
	if err := recovered.AppendWrite([]byte("after_truncate")); err != nil {
		t.Fatalf("AppendWrite after truncate recovery failed: %v", err)
	}
}

// TestWALMaybeRotateMaxSizeExceeded 测试 WAL 文件超过 maxSize 时正确触发轮转
func TestWALMaybeRotateMaxSizeExceeded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}

	// 设置一个很小的 maxSize，写入多条记录后触发轮转
	w.maxSize = walMetaSize + 10

	// 写入足够多的记录以触发轮转
	for i := 0; i < 5; i++ {
		if err := w.AppendWrite([]byte("trigger rotation data")); err != nil {
			t.Fatalf("AppendWrite #%d failed: %v", i, err)
		}
	}

	// 验证轮转后 offset 被重置（新文件写入了一条或多条记录）
	if w.Size() == 0 {
		t.Error("expected non-zero size after rotation and write")
	}

	// 验证 .prev 文件存在
	_, err = os.Stat(path + ".prev")
	if err != nil {
		t.Fatalf("expected .prev file after rotation: %v", err)
	}

	_ = w.Close()
}
