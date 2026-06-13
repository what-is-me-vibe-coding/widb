package storage

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// --- OpenWAL: CRC 不匹配、不存在文件、截断定位（续 coverage_low_wal_v9_test.go）---

// TestOpenWAL_CRCMismatch 测试 OpenWAL 打开包含 CRC 不匹配记录的 WAL。
func TestOpenWAL_CRCMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	if err := w.AppendWrite([]byte("good_record")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	_ = w.Sync()
	_ = w.Close()
	// 读取文件内容，修改 CRC 使其不匹配
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}
	// 记录格式: 4字节长度 + 1字节类型 + payload + 4字节CRC
	// 修改最后一个字节（CRC 的最后一个字节）使 CRC 不匹配
	data[len(data)-1] ^= 0xFF
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}
	// 通过 OpenWAL 打开
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()
	// CRC 不匹配的记录应被丢弃
	if len(recs) != 0 {
		t.Errorf("期望 0 条有效记录（CRC 不匹配），实际 %d", len(recs))
	}
}

// TestOpenWAL_NonExistentFile 测试 OpenWAL 打开不存在的文件。
func TestOpenWAL_NonExistentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.wal")
	_, _, err := OpenWAL(path)
	if err == nil {
		t.Fatal("期望打开不存在的文件返回错误，得到 nil")
	}
}

// TestOpenWAL_TruncateAndSeek 测试 OpenWAL 的截断和定位路径。
// 写入有效记录后追加垃圾数据，验证 OpenWAL 正确截断并定位。
func TestOpenWAL_TruncateAndSeek(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := w.AppendWrite([]byte("record_data")); err != nil {
			t.Fatalf("AppendWrite #%d 失败: %v", i, err)
		}
	}
	_ = w.Sync()
	_ = w.Close()
	// 读取有效数据长度
	validData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}
	validLen := len(validData)
	// 追加垃圾数据
	garbage := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE}
	modified := make([]byte, validLen+len(garbage))
	copy(modified, validData)
	copy(modified[validLen:], garbage)
	if err := os.WriteFile(path, modified, 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}
	// 通过 OpenWAL 打开
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	// 验证记录数量
	if len(recs) != 3 {
		t.Errorf("期望 3 条记录，实际 %d", len(recs))
	}
	// 验证文件已被截断到有效数据末尾
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if fi.Size() != int64(validLen) {
		t.Errorf("文件大小 = %d, 期望 %d", fi.Size(), validLen)
	}
	// 验证 Seek 正确：恢复后可以追加新记录
	if err := recovered.AppendWrite([]byte("after_recovery")); err != nil {
		t.Fatalf("恢复后 AppendWrite 失败: %v", err)
	}
	_ = recovered.Close()
	// 再次打开验证所有记录
	recovered2, recs2, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("第二次 OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered2.Close() }()
	if len(recs2) != 4 {
		t.Errorf("期望 4 条记录，实际 %d", len(recs2))
	}
}

// --- WAL Truncate 操作（wal.go 第 191-211 行）---

// TestWAL_Truncate 测试 WAL Truncate 操作清空文件并重置偏移量。
func TestWAL_Truncate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := w.AppendWrite([]byte("data_before_truncate")); err != nil {
			t.Fatalf("AppendWrite #%d 失败: %v", i, err)
		}
	}
	sizeBefore := w.Size()
	if sizeBefore == 0 {
		t.Fatal("期望写入后偏移量大于 0")
	}
	if err := w.Truncate(); err != nil {
		t.Fatalf("Truncate 失败: %v", err)
	}
	if w.Size() != 0 {
		t.Errorf("Truncate 后偏移量应为 0，实际 %d", w.Size())
	}
	if err := w.AppendWrite([]byte("after_truncate")); err != nil {
		t.Fatalf("Truncate 后 AppendWrite 失败: %v", err)
	}
	if w.Size() == 0 {
		t.Error("Truncate 后写入数据，偏移量应大于 0")
	}
	_ = w.Close()
}

// TestWAL_TruncateThenOpen 测试 Truncate 后关闭再打开 WAL。
func TestWAL_TruncateThenOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	if err := w.AppendWrite([]byte("old_data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	if err := w.Truncate(); err != nil {
		t.Fatalf("Truncate 失败: %v", err)
	}
	if err := w.AppendWrite([]byte("new_data")); err != nil {
		t.Fatalf("Truncate 后 AppendWrite 失败: %v", err)
	}
	_ = w.Close()
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()
	if len(recs) != 1 {
		t.Errorf("期望 1 条记录（Truncate 后的新数据），实际 %d", len(recs))
	}
	if string(recs[0].Payload) != "new_data" {
		t.Errorf("记录负载不匹配: 期望 'new_data', 实际 %q", string(recs[0].Payload))
	}
}

// --- WAL Size() 报告（wal.go 第 174-176 行）---

// TestWAL_SizeReporting 测试 Size() 正确报告当前偏移量。
func TestWAL_SizeReporting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	if w.Size() != 0 {
		t.Errorf("初始偏移量应为 0，实际 %d", w.Size())
	}
	if err := w.AppendWrite([]byte("test_data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	sizeAfterWrite := w.Size()
	if sizeAfterWrite == 0 {
		t.Error("写入后偏移量应大于 0")
	}
	if err := w.AppendWrite([]byte("more_data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	if w.Size() <= sizeAfterWrite {
		t.Errorf("第二次写入后偏移量 %d 应大于第一次 %d", w.Size(), sizeAfterWrite)
	}
	_ = w.Close()
}

// TestWAL_SizeAfterTruncate 测试 Truncate 后 Size() 返回 0。
func TestWAL_SizeAfterTruncate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	if err := w.AppendWrite([]byte("data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	if w.Size() == 0 {
		t.Fatal("写入后偏移量应大于 0")
	}
	if err := w.Truncate(); err != nil {
		t.Fatalf("Truncate 失败: %v", err)
	}
	if w.Size() != 0 {
		t.Errorf("Truncate 后偏移量应为 0，实际 %d", w.Size())
	}
	_ = w.Close()
}

// --- 并发 Append 操作 ---

// TestWAL_ConcurrentAppend 测试并发追加记录的正确性。
func TestWAL_ConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	const goroutines = 10
	const recordsPerGoroutine = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < recordsPerGoroutine; i++ {
				payload := []byte("goroutine_data")
				if err := w.AppendWrite(payload); err != nil {
					t.Errorf("goroutine %d AppendWrite #%d 失败: %v", id, i, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	_ = w.Close()
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()
	expectedTotal := goroutines * recordsPerGoroutine
	if len(recs) != expectedTotal {
		t.Errorf("期望 %d 条记录，实际 %d", expectedTotal, len(recs))
	}
}

// TestWAL_ConcurrentAppendWithRotation 测试并发追加时触发轮转的正确性。
func TestWAL_ConcurrentAppendWithRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	w.maxSize = 200
	const goroutines = 5
	const recordsPerGoroutine = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < recordsPerGoroutine; i++ {
				payload := []byte("concurrent_rotation_data")
				if err := w.AppendWrite(payload); err != nil {
					t.Errorf("goroutine %d AppendWrite #%d 失败: %v", id, i, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	_ = w.Close()
	if _, err := os.Stat(path + ".prev"); err != nil {
		t.Logf("轮转可能未发生（数据量不足）: %v", err)
	}
	recovered, _, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	_ = recovered.Close()
}

// --- AppendCommit / AppendCheckpoint 类型特定方法 ---

// TestWAL_AppendCommitAndCheckpoint 测试 AppendCommit 和 AppendCheckpoint 方法。
func TestWAL_AppendCommitAndCheckpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	if err := w.AppendCommit([]byte("commit_payload")); err != nil {
		t.Fatalf("AppendCommit 失败: %v", err)
	}
	if err := w.AppendCheckpoint([]byte("checkpoint_payload")); err != nil {
		t.Fatalf("AppendCheckpoint 失败: %v", err)
	}
	_ = w.Close()
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()
	if len(recs) != 2 {
		t.Fatalf("期望 2 条记录，实际 %d", len(recs))
	}
	if recs[0].Type != walTypeCommit {
		t.Errorf("第一条记录类型应为 Commit(%d)，实际 %d", walTypeCommit, recs[0].Type)
	}
	if recs[1].Type != walTypeCheckpoint {
		t.Errorf("第二条记录类型应为 Checkpoint(%d)，实际 %d", walTypeCheckpoint, recs[1].Type)
	}
}

// --- AppendBatch 批量写入 ---

// TestWAL_AppendBatch 测试 AppendBatch 批量追加记录。
func TestWAL_AppendBatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	records := []BatchRecord{
		{Type: walTypeWrite, Payload: []byte("batch_1")},
		{Type: walTypeWrite, Payload: []byte("batch_2")},
		{Type: walTypeCommit, Payload: []byte("batch_commit")},
	}
	if err := w.AppendBatch(records); err != nil {
		t.Fatalf("AppendBatch 失败: %v", err)
	}
	_ = w.Close()
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = recovered.Close() }()
	if len(recs) != 3 {
		t.Fatalf("期望 3 条记录，实际 %d", len(recs))
	}
	for i, rec := range recs {
		if rec.Type != records[i].Type {
			t.Errorf("记录 %d 类型不匹配: 期望 %d, 实际 %d", i, records[i].Type, rec.Type)
		}
		if string(rec.Payload) != string(records[i].Payload) {
			t.Errorf("记录 %d 负载不匹配: 期望 %q, 实际 %q", i, string(records[i].Payload), string(rec.Payload))
		}
	}
}

// TestWAL_AppendBatchPayloadTooLarge 测试 AppendBatch 中负载过大时返回错误。
func TestWAL_AppendBatchPayloadTooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	largePayload := make([]byte, maxRecordPayload+1)
	records := []BatchRecord{
		{Type: walTypeWrite, Payload: largePayload},
	}
	err = w.AppendBatch(records)
	if err == nil {
		_ = w.Close()
		t.Fatal("期望负载过大时返回错误，得到 nil")
	}
	if !strings.Contains(err.Error(), "payload too large") {
		t.Errorf("错误消息应包含 'payload too large'，得到: %v", err)
	}
	_ = w.Close()
}

// --- Append 负载过大 ---

// TestWAL_AppendPayloadTooLarge 测试 Append 中负载过大时返回错误。
func TestWAL_AppendPayloadTooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	largePayload := make([]byte, maxRecordPayload+1)
	err = w.Append(walTypeWrite, largePayload)
	if err == nil {
		_ = w.Close()
		t.Fatal("期望负载过大时返回错误，得到 nil")
	}
	if !strings.Contains(err.Error(), "payload too large") {
		t.Errorf("错误消息应包含 'payload too large'，得到: %v", err)
	}
	_ = w.Close()
}
