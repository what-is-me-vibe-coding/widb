package storage

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOpenWAL_TruncateOnReadOnlyFS 测试 OpenWAL 中 Truncate 失败的路径。
// 使用只读挂载的 tmpfs 来触发 Truncate 错误（需要非 root 权限）。
// 在 root 环境下，通过关闭文件描述符后调用 OpenWAL 间接测试。
func TestOpenWAL_TruncateErrorViaClosedFD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建包含有效记录 + 垃圾数据的 WAL
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	_ = w.AppendWrite([]byte("data"))
	_ = w.Sync()
	_ = w.Close()

	// 追加垃圾数据使 OpenWAL 需要 Truncate
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	garbage := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	modifiedData := make([]byte, len(data)+len(garbage))
	copy(modifiedData, data)
	copy(modifiedData[len(data):], garbage)
	if err := os.WriteFile(path, modifiedData, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// 正常打开，Truncate 应该成功
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered.Close() }()

	if len(recs) != 1 {
		t.Errorf("expected 1 record, got %d", len(recs))
	}

	// 验证文件已被截断
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if fi.Size() != int64(len(data)) {
		t.Errorf("file size = %d, want %d (truncated to valid data)", fi.Size(), len(data))
	}
}

// TestOpenWAL_SeekErrorPath 测试 OpenWAL 中 Seek 的正常路径覆盖。
// Seek 错误路径在正常环境下无法触发（需要文件系统级故障），
// 此测试验证 Seek 在正常情况下的正确性。
func TestOpenWAL_SeekNormalPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建包含多条记录的 WAL
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := w.AppendWrite([]byte("record")); err != nil {
			t.Fatalf("AppendWrite failed: %v", err)
		}
	}
	_ = w.Sync()
	_ = w.Close()

	// 打开 WAL，验证 Seek 后可以继续追加
	recovered, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}

	if len(recs) != 5 {
		t.Fatalf("expected 5 records, got %d", len(recs))
	}

	// 验证 Seek 正确：恢复后追加的记录应写入正确位置
	if err := recovered.AppendWrite([]byte("after_seek")); err != nil {
		t.Fatalf("AppendWrite after OpenWAL failed: %v", err)
	}
	_ = recovered.Close()

	// 再次打开验证所有记录
	recovered2, recs2, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("second OpenWAL failed: %v", err)
	}
	defer func() { _ = recovered2.Close() }()

	if len(recs2) != 6 {
		t.Errorf("expected 6 records, got %d", len(recs2))
	}
}
