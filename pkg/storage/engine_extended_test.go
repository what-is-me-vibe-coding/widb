package storage

import (
	"fmt"
	"os"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestEngineLoadSegmentsFromDisk(t *testing.T) {
	dir := t.TempDir()

	// Create an engine, write data, flush, and close
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("first NewEngine: %v", err)
	}
	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("b", map[string]common.Value{colVal: common.NewInt64(2)})
	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen - should load segments from disk
	eng2, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("second NewEngine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	if eng2.SegmentCount() < 1 {
		t.Errorf("expected at least 1 segment after reload, got %d", eng2.SegmentCount())
	}

	// Verify data can be read from loaded segments
	row, ok := eng2.Get("a")
	if !ok {
		t.Error("key 'a' not found after reload")
	} else if row.Columns[colVal].Int64 != 1 {
		t.Errorf("key 'a': expected 1, got %d", row.Columns[colVal].Int64)
	}
}

func TestEngineLoadSegmentsCorruptFile(t *testing.T) {
	dir := t.TempDir()

	// Create a valid segment file first by using the engine
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("first NewEngine: %v", err)
	}
	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Now also create a corrupt segment file alongside the valid one
	corruptPath := dir + "/segment_999.widb"
	if err := os.WriteFile(corruptPath, []byte("corrupt data that is long enough"), 0644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	// Opening engine should succeed - the valid segment loads, the corrupt one is skipped
	eng2, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine with corrupt segment alongside valid one: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	// The valid segment should still be loaded
	if eng2.SegmentCount() < 1 {
		t.Errorf("expected at least 1 valid segment, got %d", eng2.SegmentCount())
	}
}

func TestEngineLoadSegmentsAllCorrupt(t *testing.T) {
	dir := t.TempDir()

	// Create only corrupt segment files - all fail to load
	corruptPath := dir + "/segment_1.widb"
	if err := os.WriteFile(corruptPath, []byte("corrupt"), 0644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	// When all segment files fail, loadSegments should return an error
	_, err := NewEngine(EngineConfig{DataDir: dir})
	if err == nil {
		t.Error("expected error when all segment files are corrupt")
	}
}

// TestEngineWriteTriggersMemTableRotation 测试 Write 触发 MemTable 轮转
func TestEngineWriteTriggersMemTableRotation(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir:         t.TempDir(),
		MaxMemTableSize: 1, // 极小的阈值，第一次写入就触发轮转
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入足够数据以触发 MemTable 轮转
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("key_%d", i)
		vals := map[string]common.Value{
			colVal: common.NewString("a long string to increase memtable size and trigger rotation"),
		}
		if err := eng.Write(key, vals); err != nil {
			t.Fatalf("write %s: %v", key, err)
		}
	}

	// 验证写入成功
	row, ok := eng.Get("key_0")
	if !ok {
		t.Fatal("key_0 not found")
	}
	if v, exists := row.Columns[colVal]; !exists || v.Str == "" {
		t.Errorf("expected non-empty val, got %v", v)
	}
}

// TestEngineWriteWALSyncError 测试 Write 在 WAL 同步失败时的行为
func TestEngineWriteWALSyncError(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	// 关闭 WAL 以触发后续写入错误
	_ = eng.wal.Close()

	// 写入应该返回错误（WAL 已关闭）
	err = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})
	if err == nil {
		t.Error("expected error when writing with closed WAL")
	}
}

// TestEngineWriteMultipleVersions 测试 Write 递增版本号
func TestEngineWriteMultipleVersions(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入多条记录，验证版本号递增
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("key_%d", i)
		if err := eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))}); err != nil {
			t.Fatalf("write %s: %v", key, err)
		}
	}

	// 验证最后一条记录的版本号
	row, ok := eng.Get("key_4")
	if !ok {
		t.Fatal("key_4 not found")
	}
	if row.Version != 5 {
		t.Errorf("expected version 5, got %d", row.Version)
	}
}

// TestEngineWriteOverwriteKey 测试 Write 覆盖已有键
func TestEngineWriteOverwriteKey(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入初始值
	if err := eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(100)}); err != nil {
		t.Fatalf("write 1: %v", err)
	}

	// 覆盖写入
	if err := eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(200)}); err != nil {
		t.Fatalf("write 2: %v", err)
	}

	// 验证值为最新写入
	row, ok := eng.Get("key1")
	if !ok {
		t.Fatal("key1 not found")
	}
	if row.Columns[colVal].Int64 != 200 {
		t.Errorf("expected 200, got %d", row.Columns[colVal].Int64)
	}
}

// TestEngineRegisterSegmentIndexesBloomFilter verifies that segments with bloom
// filters have their bloom indexes properly registered after flush and after
// reopening the engine.
func TestEngineRegisterSegmentIndexesBloomFilter(t *testing.T) {
	dir := t.TempDir()

	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("key2", map[string]common.Value{colVal: common.NewInt64(2)})
	_ = eng.Write("key3", map[string]common.Value{colVal: common.NewInt64(3)})

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	// Verify the segment has a bloom filter
	segs := eng.Segments()
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segs))
	}
	if len(segs[0].Footer.BloomFilter) == 0 {
		t.Error("expected segment to have bloom filter data")
	}

	// Verify point queries work (using bloom filter)
	row, ok := eng.Get("key1")
	if !ok {
		t.Error("key1 not found")
	} else if row.Columns[colVal].Int64 != 1 {
		t.Errorf("key1: expected 1, got %d", row.Columns[colVal].Int64)
	}

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	// Reopen and verify bloom filter is re-registered
	eng2, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	row, ok = eng2.Get("key2")
	if !ok {
		t.Error("key2 not found after reload")
	} else if row.Columns[colVal].Int64 != 2 {
		t.Errorf("key2 after reload: expected 2, got %d", row.Columns[colVal].Int64)
	}

	_, ok = eng2.Get("nonexistent_key")
	if ok {
		t.Error("expected nonexistent_key to not be found")
	}
}

// TestEngineCloseFlushesActiveMemTable 验证 Close() 会将 activeMem 中的数据刷写到磁盘，
// 重启后数据可从 segment 文件恢复，无需依赖 WAL 回放。
func TestEngineCloseFlushesActiveMemTable(t *testing.T) {
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}
	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	// 写入数据但不显式 Flush，直接 Close
	_ = eng.Write("x", map[string]common.Value{colVal: common.NewInt64(10)})
	_ = eng.Write("y", map[string]common.Value{colVal: common.NewInt64(20)})

	// 设置 columnMeta 以便 Close 中的 flush 能正确编码列
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush to set columnMeta: %v", err)
	}

	// 再写入新数据到 activeMem
	_ = eng.Write("z", map[string]common.Value{colVal: common.NewInt64(30)})

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	// 重启引擎，验证所有数据都可恢复
	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	expectedData := map[string]int64{"x": 10, "y": 20, "z": 30}
	for key, expected := range expectedData {
		row, ok := eng2.Get(key)
		if !ok {
			t.Errorf("key %s not recovered after Close flush", key)
			continue
		}
		if row.Columns[colVal].Int64 != expected {
			t.Errorf("key %s: expected %d, got %d", key, expected, row.Columns[colVal].Int64)
		}
	}
}
