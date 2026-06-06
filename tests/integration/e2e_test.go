package integration

import (
	"os"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

const (
	colName  = "name"
	colAge   = "age"
	colVal   = "val"
	colScore = "score"
	colValue = "value"
)

// TestEndToEndWriteFlushScan 测试完整的写入→刷盘→扫描流程
func TestEndToEndWriteFlushScan(t *testing.T) {
	dir, err := os.MkdirTemp("", "e2e_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	eng, err := storage.NewEngine(storage.EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	cols := []storage.ColumnMeta{
		{ID: 0, Name: colName, Type: common.TypeString},
		{ID: 1, Name: colAge, Type: common.TypeInt64},
	}

	// 写入数据
	_ = eng.Write("user1", map[string]common.Value{
		colName: common.NewString("alice"),
		colAge:  common.NewInt64(30),
	})
	_ = eng.Write("user2", map[string]common.Value{
		colName: common.NewString("bob"),
		colAge:  common.NewInt64(25),
	})

	// 刷盘
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	// 继续写入
	_ = eng.Write("user3", map[string]common.Value{
		colName: common.NewString("charlie"),
		colAge:  common.NewInt64(35),
	})

	// 扫描：应包含刷盘和内存中的数据
	results := eng.Scan("user1", "user3")
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// 验证数据正确性
	resultMap := make(map[string]map[string]common.Value)
	for _, r := range results {
		resultMap[r.Key] = r.Value.Columns
	}

	if v := resultMap["user1"][colName]; v.Str != "alice" {
		t.Errorf("user1 name: expected alice, got %s", v.Str)
	}
	if v := resultMap["user2"][colAge]; v.Int64 != 25 {
		t.Errorf("user2 age: expected 25, got %d", v.Int64)
	}
	if v := resultMap["user3"][colName]; v.Str != "charlie" {
		t.Errorf("user3 name: expected charlie, got %s", v.Str)
	}
}

// TestEndToEndOverwriteAndCompact 测试覆盖写入和 Compaction 流程
func TestEndToEndOverwriteAndCompact(t *testing.T) {
	dir, err := os.MkdirTemp("", "e2e_overwrite_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	eng, err := storage.NewEngine(storage.EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	cols := []storage.ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// 写入初始数据
	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("b", map[string]common.Value{colVal: common.NewInt64(2)})
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	// 覆盖写入
	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(100)})
	_ = eng.Write("c", map[string]common.Value{colVal: common.NewInt64(3)})
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	// Compact
	if err := eng.Compact(cols); err != nil {
		t.Fatal(err)
	}

	// 验证覆盖后数据正确
	row, ok := eng.Get("a")
	if !ok {
		t.Fatal("expected to find key a")
	}
	if v, exists := row.Columns[colVal]; !exists || v.Int64 != 100 {
		t.Errorf("key a: expected val=100, got %d", v.Int64)
	}

	results := eng.Scan("a", "c")
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
}

// TestEndToEndCrashRecovery 测试崩溃恢复流程
func TestEndToEndCrashRecovery(t *testing.T) {
	dir, err := os.MkdirTemp("", "e2e_recovery_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	// 第一个引擎实例：写入数据
	eng1, err := storage.NewEngine(storage.EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}

	_ = eng1.Write("key1", map[string]common.Value{colVal: common.NewInt64(42)})
	_ = eng1.Write("key2", map[string]common.Value{colVal: common.NewInt64(84)})

	// 模拟崩溃：不调用 Close，直接放弃引擎
	// WAL 中的数据应该已经 Sync

	// 第二个引擎实例：恢复数据
	eng2, err := storage.NewEngine(storage.EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng2.Close() }()

	row, ok := eng2.Get("key1")
	if !ok {
		t.Fatal("expected to find key1 after recovery")
	}
	if v, exists := row.Columns[colVal]; !exists || v.Int64 != 42 {
		t.Errorf("key1: expected val=42, got %d", v.Int64)
	}

	row, ok = eng2.Get("key2")
	if !ok {
		t.Fatal("expected to find key2 after recovery")
	}
	if v, exists := row.Columns[colVal]; !exists || v.Int64 != 84 {
		t.Errorf("key2: expected val=84, got %d", v.Int64)
	}
}
