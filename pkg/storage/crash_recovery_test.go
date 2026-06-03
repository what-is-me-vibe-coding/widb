package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const (
	crKey1 = "key1"
	crKey2 = "key2"
	crKey3 = "key3"
	crKey4 = "key4"
)

// TestCrashRecovery_BasicRecovery 验证基本崩溃恢复：写入数据后模拟崩溃（不Flush），重新打开引擎后数据应从WAL恢复。
func TestCrashRecovery_BasicRecovery(t *testing.T) {
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	_ = eng.Write(crKey1, map[string]common.Value{
		colVal: common.NewInt64(100),
	})
	_ = eng.Write(crKey2, map[string]common.Value{
		colVal: common.NewInt64(200),
	})

	// Simulate crash: close without flush
	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	// Reopen and verify recovery
	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	row, ok := eng2.Get(crKey1)
	if !ok {
		t.Fatal("key1 not recovered")
	}
	if row.Columns[colVal].Int64 != 100 {
		t.Errorf("key1: expected 100, got %d", row.Columns[colVal].Int64)
	}

	row, ok = eng2.Get(crKey2)
	if !ok {
		t.Fatal("key2 not recovered")
	}
	if row.Columns[colVal].Int64 != 200 {
		t.Errorf("key2: expected 200, got %d", row.Columns[colVal].Int64)
	}
}

// TestCrashRecovery_MultipleWrites 验证大量写入后的崩溃恢复。
func TestCrashRecovery_MultipleWrites(t *testing.T) {
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	const n = 500
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("key_%04d", i)
		_ = eng.Write(key, map[string]common.Value{
			colVal: common.NewInt64(int64(i)),
		})
	}

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	for i := 0; i < n; i++ {
		key := fmt.Sprintf("key_%04d", i)
		row, ok := eng2.Get(key)
		if !ok {
			t.Errorf("key %s not recovered", key)
			continue
		}
		if row.Columns[colVal].Int64 != int64(i) {
			t.Errorf("key %s: expected %d, got %d", key, i, row.Columns[colVal].Int64)
		}
	}
}

// TestCrashRecovery_AfterFlush 验证Flush后写入更多数据再崩溃，段数据和WAL数据都能恢复。
func TestCrashRecovery_AfterFlush(t *testing.T) {
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// Write and flush first batch
	_ = eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(100)})
	_ = eng.Write(crKey2, map[string]common.Value{colVal: common.NewInt64(200)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	// Write more data without flushing
	_ = eng.Write(crKey3, map[string]common.Value{colVal: common.NewInt64(300)})
	_ = eng.Write(crKey4, map[string]common.Value{colVal: common.NewInt64(400)})

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	// Reopen and verify both segment data and WAL data
	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	// Segment data (flushed)
	segExpected := map[string]int64{crKey1: 100, crKey2: 200}
	for key, expected := range segExpected {
		row, ok := eng2.Get(key)
		if !ok {
			t.Errorf("%s not recovered from segment", key)
			continue
		}
		if row.Columns[colVal].Int64 != expected {
			t.Errorf("%s: expected %d, got %d", key, expected, row.Columns[colVal].Int64)
		}
	}

	// WAL data (unflushed)
	walExpected := map[string]int64{crKey3: 300, crKey4: 400}
	for key, expected := range walExpected {
		row, ok := eng2.Get(key)
		if !ok {
			t.Errorf("%s not recovered from WAL", key)
			continue
		}
		if row.Columns[colVal].Int64 != expected {
			t.Errorf("%s: expected %d, got %d", key, expected, row.Columns[colVal].Int64)
		}
	}
}

// TestCrashRecovery_PartialWrite 验证WAL同步行为：同步写入的数据能恢复，未同步的可能丢失。
func TestCrashRecovery_PartialWrite(t *testing.T) {
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	// Write batch 1 and close properly (syncs WAL)
	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	_ = eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write(crKey2, map[string]common.Value{colVal: common.NewInt64(2)})
	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	// Record WAL file size after batch 1
	walPath := filepath.Join(dir, "wal.log")
	info, err := os.Stat(walPath)
	if err != nil {
		t.Fatalf("stat wal: %v", err)
	}
	walSizeAfterBatch1 := info.Size()

	// Reopen and write batch 2
	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	_ = eng2.Write(crKey3, map[string]common.Value{colVal: common.NewInt64(3)})
	_ = eng2.Write(crKey4, map[string]common.Value{colVal: common.NewInt64(4)})

	// Simulate crash: close WAL without syncing, then truncate
	_ = eng2.wal.Close()

	// Truncate WAL to size after batch 1 (simulates unsynced data loss)
	if err := os.Truncate(walPath, walSizeAfterBatch1); err != nil {
		t.Fatalf("truncate wal: %v", err)
	}

	// Reopen and verify only batch 1 is recovered
	eng3, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine after partial write: %v", err)
	}
	defer func() { _ = eng3.Close() }()

	// Batch 1 should be recovered
	if row, ok := eng3.Get(crKey1); !ok || row.Columns[colVal].Int64 != 1 {
		t.Errorf("key1 not recovered correctly")
	}
	if row, ok := eng3.Get(crKey2); !ok || row.Columns[colVal].Int64 != 2 {
		t.Errorf("key2 not recovered correctly")
	}

	// Batch 2 should NOT be recovered (truncated)
	if _, ok := eng3.Get(crKey3); ok {
		t.Errorf("key3 should not be recovered (partial write)")
	}
	if _, ok := eng3.Get(crKey4); ok {
		t.Errorf("key4 should not be recovered (partial write)")
	}
}

// TestCrashRecovery_MultipleRestarts 验证多次崩溃-恢复循环后数据累积正确。
func TestCrashRecovery_MultipleRestarts(t *testing.T) {
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	// First session: write batch 1
	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine 1: %v", err)
	}
	_ = eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(1)})
	if err := eng.Close(); err != nil {
		t.Fatalf("close engine 1: %v", err)
	}

	// Second session: write batch 2
	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine 2: %v", err)
	}
	_ = eng2.Write(crKey2, map[string]common.Value{colVal: common.NewInt64(2)})
	if err := eng2.Close(); err != nil {
		t.Fatalf("close engine 2: %v", err)
	}

	// Third session: write batch 3
	eng3, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine 3: %v", err)
	}
	_ = eng3.Write(crKey3, map[string]common.Value{colVal: common.NewInt64(3)})
	if err := eng3.Close(); err != nil {
		t.Fatalf("close engine 3: %v", err)
	}

	// Final recovery: verify all data
	eng4, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine 4: %v", err)
	}
	defer func() { _ = eng4.Close() }()

	for i := 1; i <= 3; i++ {
		key := fmt.Sprintf("key%d", i)
		row, ok := eng4.Get(key)
		if !ok {
			t.Errorf("%s not recovered", key)
			continue
		}
		if row.Columns[colVal].Int64 != int64(i) {
			t.Errorf("%s: expected %d, got %d", key, i, row.Columns[colVal].Int64)
		}
	}
}

// TestCrashRecovery_Overwrite 验证覆盖写入后崩溃恢复，最新值应被恢复。
func TestCrashRecovery_Overwrite(t *testing.T) {
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	_ = eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(100)})
	_ = eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(999)})

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	row, ok := eng2.Get(crKey1)
	if !ok {
		t.Fatal("key1 not recovered")
	}
	if row.Columns[colVal].Int64 != 999 {
		t.Errorf("expected latest value 999, got %d", row.Columns[colVal].Int64)
	}
}

// TestCrashRecovery_LargeDataset 验证大数据集的崩溃恢复。
func TestCrashRecovery_LargeDataset(t *testing.T) {
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	const n = 10000
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("key_%06d", i)
		_ = eng.Write(key, map[string]common.Value{
			colVal: common.NewInt64(int64(i)),
		})
	}

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	// Verify a sample of keys
	sampleIndices := []int{0, 1, 50, 999, 5000, 9999}
	for _, i := range sampleIndices {
		key := fmt.Sprintf("key_%06d", i)
		row, ok := eng2.Get(key)
		if !ok {
			t.Errorf("key %s not recovered", key)
			continue
		}
		if row.Columns[colVal].Int64 != int64(i) {
			t.Errorf("key %s: expected %d, got %d", key, i, row.Columns[colVal].Int64)
		}
	}

	// Verify total count via scan
	results := eng2.Scan("key_000000", "key_009999")
	if len(results) != n {
		t.Errorf("expected %d results from scan, got %d", n, len(results))
	}
}

// TestCrashRecovery_EmptyWAL 验证打开引擎后不写入直接关闭，再次打开不会出错。
func TestCrashRecovery_EmptyWAL(t *testing.T) {
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	// Verify no data and no errors
	_, ok := eng2.Get("nonexistent")
	if ok {
		t.Error("expected key not found")
	}
}

// TestCrashRecovery_KeyNotFound 验证查询不存在的键时不会出错。
func TestCrashRecovery_KeyNotFound(t *testing.T) {
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	_ = eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(1)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	// Query a key that doesn't exist
	_, ok := eng2.Get("nonexistent_key")
	if ok {
		t.Error("expected key not found")
	}
}
