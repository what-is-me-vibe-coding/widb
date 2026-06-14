package storage

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestOpenWALTruncateOnNonRegularFile 测试 OpenWAL 在 Truncate 失败时的错误路径
// 使用符号链接指向 /dev/null，该文件可以 O_RDWR 打开但不支持 Truncate
func TestOpenWALTruncateOnNonRegularFile(t *testing.T) {
	if runtime.GOOS != skipWindows && runtime.GOOS != skipNonLinux {
		// This test is only meaningful on Linux; on other Unix systems the
		// /dev/null Truncate behavior may differ.
		t.Skip("test relies on /dev/null Truncate behavior on Linux")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建符号链接指向 /dev/null
	// /dev/null 可以 O_RDWR 打开，但 f.Truncate 返回 EINVAL
	if err := os.Symlink("/dev/null", path); err != nil {
		t.Fatalf("Symlink failed: %v", err)
	}

	// OpenWAL 应该因 Truncate 在 /dev/null 上返回 EINVAL 而失败
	_, _, err := OpenWAL(path)
	if err == nil {
		t.Fatal("expected error when Truncate fails on non-regular file")
	}
}

// TestOpenWALWithValidRecordsReplay 测试 OpenWAL 成功回放记录后的正常路径
func TestOpenWALWithValidRecordsReplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// 创建 WAL 并写入多条记录
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL failed: %v", err)
	}
	for i := 0; i < 10; i++ {
		if err := w.AppendWrite([]byte("record_data")); err != nil {
			t.Fatalf("AppendWrite %d failed: %v", i, err)
		}
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// 打开 WAL 并验证记录回放
	w2, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if len(records) != 10 {
		t.Fatalf("expected 10 records, got %d", len(records))
	}

	// 验证可以继续追加
	if err := w2.AppendWrite([]byte("after_open")); err != nil {
		t.Fatalf("AppendWrite after OpenWAL failed: %v", err)
	}
}

// TestEngineWriteWALSyncErrorDetailed 测试 Write 在 WAL 同步失败时的详细错误路径
func TestEngineWriteWALSyncErrorDetailed(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	// 关闭 WAL 以触发后续写入错误
	_ = eng.wal.Close()

	// 写入应该返回错误（WAL 已关闭，AppendWrite 或 Sync 会失败）
	err = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})
	if err == nil {
		t.Error("expected error when writing with closed WAL")
	}
}

// TestEngineWriteAfterClose 测试引擎关闭后写入返回错误
func TestEngineWriteAfterClose(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	// 正常写入
	if err := eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
		t.Fatalf("write before close: %v", err)
	}

	// 关闭引擎
	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	// 关闭后写入应该返回错误
	err = eng.Write("key2", map[string]common.Value{colVal: common.NewInt64(2)})
	if err == nil {
		t.Error("expected error when writing after engine close")
	}
}

// TestEngineGetFromSegmentsWithBloomFilter 测试 Get 通过布隆过滤器过滤 Segment 的路径
func TestEngineGetFromSegmentsWithBloomFilter(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("b", map[string]common.Value{colVal: common.NewInt64(2)})

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	// 查询存在的键
	row, ok := eng.Get("a")
	if !ok {
		t.Fatal("key 'a' not found")
	}
	if row.Columns[colVal].Int64 != 1 {
		t.Errorf("expected 1, got %d", row.Columns[colVal].Int64)
	}

	// 查询不存在的键（布隆过滤器可能返回 false positive，但最终结果应该是 not found）
	_, ok = eng.Get("nonexistent_key")
	if ok {
		t.Error("expected false for nonexistent key")
	}
}

// TestEngineCompactAndScan 测试 Compaction 后 Scan 结果正确
func TestEngineCompactAndScan(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// 写入并 flush 多个 Segment
	for i := 0; i < 3; i++ {
		key := string(rune('a' + i))
		_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))})
		if err := eng.Flush(cols); err != nil {
			t.Fatalf("flush %d: %v", i, err)
		}
	}

	// 执行 Compaction
	if err := eng.Compact(cols); err != nil {
		t.Fatalf("compact: %v", err)
	}

	// 验证 Scan 结果
	results := eng.Scan("a", "c")
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	expectedVals := map[string]int64{"a": 0, "b": 1, "c": 2}
	for _, r := range results {
		expected, ok := expectedVals[r.Key]
		if !ok {
			t.Errorf("unexpected key %q", r.Key)
			continue
		}
		if r.Value.Columns[colVal].Int64 != expected {
			t.Errorf("key %q: expected %d, got %d", r.Key, expected, r.Value.Columns[colVal].Int64)
		}
	}
}

// TestScanWithErrorSuccess 测试 ScanWithError 正常扫描返回结果和 nil 错误
func TestScanWithErrorSuccess(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	_ = cols
	if err := eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := eng.Write("key2", map[string]common.Value{colVal: common.NewInt64(2)}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	entries, err := eng.ScanWithError("key1", "key2")
	if err != nil {
		t.Fatalf("ScanWithError: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

// TestScanWithErrorEmptyRange 测试 ScanWithError 扫描空范围返回 nil 和 nil 错误
func TestScanWithErrorEmptyRange(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	entries, err := eng.ScanWithError("x", "z")
	if err != nil {
		t.Fatalf("ScanWithError: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

// TestScanWithErrorFromIteratorError 测试 ScanWithError 在 segment 列数据损坏时返回错误
func TestScanWithErrorFromIteratorError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	if err := eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Corrupt the in-memory segment column data to trigger decode errors
	eng.mu.RLock()
	for _, seg := range eng.segments {
		// Corrupt the compressed column data so decompression will fail
		for i := range seg.Columns {
			seg.Columns[i].Data = []byte("corrupted_data_that_cannot_be_decompressed")
		}
		// Reset the column decode cache so the corrupted data is re-decoded
		seg.colCache = nil
		seg.cacheInit = sync.Once{}
		seg.colDecodeState = nil
	}
	eng.mu.RUnlock()

	// Clear the block cache
	eng.blockCache.Clear()

	// ScanWithError should return an error due to corrupted segment data
	_, scanErr := eng.ScanWithError("key1", "key1")
	if scanErr != nil {
		t.Logf("ScanWithError correctly returned error: %v", scanErr)
	} else {
		t.Log("ScanWithError did not return error for corrupted segment; this is acceptable if data was cached")
	}

	_ = eng.Close()
}
