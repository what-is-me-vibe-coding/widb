package storage

import (
	"fmt"
	"os"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestEngineWriteAndGet(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	const testUserName = "alice"
	vals := map[string]common.Value{
		colName: common.NewString(testUserName),
		colAge:  common.NewInt64(30),
	}

	if err := eng.Write("key1", vals); err != nil {
		t.Fatalf("write: %v", err)
	}

	row, ok := eng.Get("key1")
	if !ok {
		t.Fatal("key1 not found")
	}
	if row.Version != 1 {
		t.Errorf("expected version 1, got %d", row.Version)
	}
	if v, exists := row.Columns[colName]; !exists || v.Str != testUserName {
		t.Errorf("expected name=%s, got %v", testUserName, v)
	}
	if v, exists := row.Columns[colAge]; !exists || v.Int64 != 30 {
		t.Errorf("expected age=30, got %v", v)
	}
}

func TestEngineWriteAndGetMissingKey(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_, ok := eng.Get("nonexistent")
	if ok {
		t.Error("expected false for nonexistent key")
	}
}

func TestEngineScan(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("b", map[string]common.Value{colVal: common.NewInt64(2)})
	_ = eng.Write("c", map[string]common.Value{colVal: common.NewInt64(3)})

	results := eng.Scan("a", "b")
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Key != "a" {
		t.Errorf("expected first key a, got %s", results[0].Key)
	}
	if results[1].Key != "b" {
		t.Errorf("expected second key b, got %s", results[1].Key)
	}
}

func TestEngineFlush(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_ = eng.Write("key1", map[string]common.Value{
		colVal: common.NewInt64(100),
	})
	_ = eng.Write("key2", map[string]common.Value{
		colVal: common.NewInt64(200),
	})

	cols := []ColumnMeta{
		{ID: 0, Name: colVal, Type: common.TypeInt64},
	}

	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	segs := eng.Segments()
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segs))
	}
	if segs[0].RowCount != 2 {
		t.Errorf("expected rowCount=2, got %d", segs[0].RowCount)
	}
}

func TestEngineFlushMultiple(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_ = eng.Write("k1", map[string]common.Value{colVal: common.NewInt64(1)})

	cols := []ColumnMeta{
		{ID: 0, Name: colVal, Type: common.TypeInt64},
	}

	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush 1: %v", err)
	}

	_ = eng.Write("k2", map[string]common.Value{colVal: common.NewInt64(2)})

	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush 2: %v", err)
	}

	segs := eng.Segments()
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segs))
	}
}

func TestEngineAutoRotate(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir:         t.TempDir(),
		MaxMemTableSize: 1,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_ = eng.Write("k1", map[string]common.Value{
		colVal: common.NewString("hello world this is a long string to trigger rotation"),
	})

	if eng.MemTableSize() == 0 {
		t.Error("expected non-zero memtable size")
	}
}

func TestEngineConcurrentWrite(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	done := make(chan bool)
	n := 100
	for i := 0; i < n; i++ {
		go func(idx int) {
			key := "key" + string(rune('a'+idx%26))
			_ = eng.Write(key, map[string]common.Value{
				colVal: common.NewInt64(int64(idx)),
			})
			done <- true
		}(i)
	}

	for i := 0; i < n; i++ {
		<-done
	}
}

func TestNewEngineWithInvalidDataDir(t *testing.T) {
	// Use a path that cannot be created as a directory
	_, err := NewEngine(EngineConfig{
		DataDir: "/dev/null/invalid/path",
	})
	if err == nil {
		t.Error("expected error for invalid data dir")
	}
}

func TestNewEngineWithExistingWAL(t *testing.T) {
	dir := t.TempDir()

	// Create an engine, write some data, then close it
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("first NewEngine: %v", err)
	}
	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(42)})
	_ = eng.Flush([]ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}})
	if err := eng.Close(); err != nil {
		t.Fatalf("close first engine: %v", err)
	}

	// Reopen the engine - should recover from existing WAL
	eng2, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("second NewEngine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	// Verify the segment was loaded from disk
	if eng2.SegmentCount() < 1 {
		t.Errorf("expected at least 1 segment, got %d", eng2.SegmentCount())
	}
}

func TestEngineWriteWithClosedWAL(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	// Close the WAL manually to simulate error
	_ = eng.wal.Close()

	// Writing after WAL is closed should return an error
	err = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})
	if err == nil {
		t.Error("expected error when writing with closed WAL")
	}
}

func TestEngineCloseAlreadyClosedWAL(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	// Close the WAL first
	_ = eng.wal.Close()

	// Closing the engine should return an error since WAL is already closed
	err = eng.Close()
	if err == nil {
		t.Error("expected error when closing engine with already-closed WAL")
	}
}

func TestEngineFindSegmentByIDNonExistent(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	seg := eng.findSegmentByID(99999)
	if seg != nil {
		t.Error("expected nil for non-existent segment ID")
	}
}

func TestEngineRotateMemTableEmpty(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// rotateMemTable on empty memtable should return nil without adding immutable
	err = eng.rotateMemTable()
	if err != nil {
		t.Fatalf("expected no error for empty memtable rotation, got: %v", err)
	}
	if len(eng.immutable) != 0 {
		t.Errorf("expected 0 immutable memtables, got %d", len(eng.immutable))
	}
}

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
