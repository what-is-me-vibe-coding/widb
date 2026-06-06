package integration

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// verifyMultiColRow 验证多类型列行的数据正确性
func verifyMultiColRow(t *testing.T, row storage.Row, ts time.Time, suffix string) {
	t.Helper()
	if v := row.Columns["id"]; v.Int64 != 42 {
		t.Errorf("id%s: expected 42, got %d", suffix, v.Int64)
	}
	if v := row.Columns[colScore]; v.Float64 != 3.14 {
		t.Errorf("score%s: expected 3.14, got %g", suffix, v.Float64)
	}
	if v := row.Columns["label"]; v.Str != "hello" {
		t.Errorf("label%s: expected hello, got %s", suffix, v.Str)
	}
	if v := row.Columns["active"]; v.Int64 != 1 {
		t.Errorf("active%s: expected true (1), got %d", suffix, v.Int64)
	}
	if v := row.Columns["created"]; !v.Time.Equal(ts) {
		t.Errorf("created%s: expected %v, got %v", suffix, ts, v.Time)
	}
}

// TestEndToEndMultiColumnTypes 测试在单行中写入和读取多种列类型
func TestEndToEndMultiColumnTypes(t *testing.T) {
	dir, err := os.MkdirTemp("", "e2e_multi_col_types")
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
		{ID: 0, Name: "id", Type: common.TypeInt64},
		{ID: 1, Name: colScore, Type: common.TypeFloat64},
		{ID: 2, Name: "label", Type: common.TypeString},
		{ID: 3, Name: "active", Type: common.TypeBool},
		{ID: 4, Name: "created", Type: common.TypeTimestamp},
	}

	ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	_ = eng.Write("row1", map[string]common.Value{
		"id": common.NewInt64(42), colScore: common.NewFloat64(3.14),
		"label": common.NewString("hello"), "active": common.NewBool(true),
		"created": common.NewTimestamp(ts),
	})

	// 刷盘前验证
	row, ok := eng.Get("row1")
	if !ok {
		t.Fatal("expected to find row1 before flush")
	}
	verifyMultiColRow(t, row, ts, " before flush")

	// 刷盘后再次验证
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}
	row, ok = eng.Get("row1")
	if !ok {
		t.Fatal("expected to find row1 after flush")
	}
	verifyMultiColRow(t, row, ts, " after flush")
}

// TestEndToEndBatchWriteAndScan 测试 WriteBatch 后使用 ScanRange 验证批量写入是否正确持久化
func TestEndToEndBatchWriteAndScan(t *testing.T) {
	dir, err := os.MkdirTemp("", "e2e_batch_write")
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
		{ID: 1, Name: colValue, Type: common.TypeInt64},
	}

	batch := []storage.WriteRow{
		{Key: "batch1", Values: map[string]common.Value{colName: common.NewString("alpha"), colValue: common.NewInt64(10)}},
		{Key: "batch2", Values: map[string]common.Value{colName: common.NewString("beta"), colValue: common.NewInt64(20)}},
		{Key: "batch3", Values: map[string]common.Value{colName: common.NewString("gamma"), colValue: common.NewInt64(30)}},
	}

	if err := eng.WriteBatch(batch); err != nil {
		t.Fatalf("WriteBatch failed: %v", err)
	}

	// 刷盘前验证
	results := eng.Scan("batch1", "batch3")
	if len(results) != 3 {
		t.Fatalf("expected 3 results before flush, got %d", len(results))
	}

	// 刷盘后验证
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}
	results = eng.Scan("batch1", "batch3")
	if len(results) != 3 {
		t.Fatalf("expected 3 results after flush, got %d", len(results))
	}

	resultMap := make(map[string]map[string]common.Value)
	for _, r := range results {
		resultMap[r.Key] = r.Value.Columns
	}
	if v := resultMap["batch1"][colName]; v.Str != "alpha" {
		t.Errorf("batch1 name: expected alpha, got %s", v.Str)
	}
	if v := resultMap["batch2"][colValue]; v.Int64 != 20 {
		t.Errorf("batch2 value: expected 20, got %d", v.Int64)
	}
}

// TestEndToEndMultipleFlushCycles 测试多次写入-刷盘循环后所有数据是否可访问
func TestEndToEndMultipleFlushCycles(t *testing.T) {
	dir, err := os.MkdirTemp("", "e2e_multi_flush")
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

	// 三轮写入-刷盘
	for round := 0; round < 3; round++ {
		k1 := fmt.Sprintf("k%d", round*2+1)
		k2 := fmt.Sprintf("k%d", round*2+2)
		_ = eng.Write(k1, map[string]common.Value{colVal: common.NewInt64(int64(round*2 + 1))})
		_ = eng.Write(k2, map[string]common.Value{colVal: common.NewInt64(int64(round*2 + 2))})
		if err := eng.Flush(cols); err != nil {
			t.Fatalf("flush %d failed: %v", round+1, err)
		}
	}

	// 验证所有数据都可访问
	results := eng.Scan("k1", "k6")
	if len(results) != 6 {
		t.Fatalf("expected 6 results, got %d", len(results))
	}
	for i, r := range results {
		expectedKey := fmt.Sprintf("k%d", i+1)
		if r.Key != expectedKey {
			t.Errorf("result[%d]: expected key %s, got %s", i, expectedKey, r.Key)
		}
	}
}

// writeAndFlush 写入一行并执行刷盘
func writeAndFlush(t *testing.T, eng *storage.Engine, key string, val int64, cols []storage.ColumnMeta) {
	t.Helper()
	_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(val)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush failed for key %s: %v", key, err)
	}
}

// TestEndToEndCompactionDedup 测试跨多次刷盘写入同一 key 后，Compaction 正确去重并返回最新值
func TestEndToEndCompactionDedup(t *testing.T) {
	dir, err := os.MkdirTemp("", "e2e_compact_dedup")
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

	// 三次覆盖 key "dup"，同时写入不同的 other key
	writeAndFlush(t, eng, "dup", 1, cols)
	writeAndFlush(t, eng, "other1", 10, cols)
	writeAndFlush(t, eng, "dup", 2, cols)
	writeAndFlush(t, eng, "other2", 20, cols)
	writeAndFlush(t, eng, "dup", 3, cols)
	writeAndFlush(t, eng, "other3", 30, cols)

	// 执行 Compaction
	if err := eng.Compact(cols); err != nil {
		t.Fatalf("compact failed: %v", err)
	}

	// 验证 "dup" 返回最新值
	row, ok := eng.Get("dup")
	if !ok {
		t.Fatal("expected to find key dup after compaction")
	}
	if v := row.Columns[colVal]; v.Int64 != 3 {
		t.Errorf("key dup: expected val=3 (latest), got %d", v.Int64)
	}

	// 验证 Scan 结果没有重复 key
	results := eng.Scan("dup", "other3")
	keyCount := make(map[string]int)
	for _, r := range results {
		keyCount[r.Key]++
	}
	for key, count := range keyCount {
		if count > 1 {
			t.Errorf("key %s appeared %d times after compaction, expected 1", key, count)
		}
	}
}

// TestEndToEndConcurrentWriteRead 测试并发读写，验证线程安全性
func TestEndToEndConcurrentWriteRead(t *testing.T) {
	dir, err := os.MkdirTemp("", "e2e_concurrent")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	eng, err := storage.NewEngine(storage.EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	const numWriters = 4
	const numKeysPerWriter = 50
	var wg sync.WaitGroup

	// 并发写入
	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for k := 0; k < numKeysPerWriter; k++ {
				key := fmt.Sprintf("w%d_k%d", writerID, k)
				_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(writerID*numKeysPerWriter + k))})
			}
		}(w)
	}

	// 并发读取
	for r := 0; r < numWriters; r++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			for k := 0; k < numKeysPerWriter; k++ {
				_, _ = eng.Get(fmt.Sprintf("w%d_k%d", readerID, k))
			}
		}(r)
	}

	wg.Wait()

	// 写入完成后验证所有数据可读
	cols := []storage.ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}
	for w := 0; w < numWriters; w++ {
		for k := 0; k < numKeysPerWriter; k++ {
			key := fmt.Sprintf("w%d_k%d", w, k)
			if _, ok := eng.Get(key); !ok {
				t.Errorf("expected to find key %s after flush", key)
			}
		}
	}
}

// TestEndToEndEngineRecoveryAfterMultipleFlushes 测试多次刷盘后的崩溃恢复
func TestEndToEndEngineRecoveryAfterMultipleFlushes(t *testing.T) {
	dir, err := os.MkdirTemp("", "e2e_recovery_multi_flush")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cols := []storage.ColumnMeta{
		{ID: 0, Name: colName, Type: common.TypeString},
		{ID: 1, Name: colValue, Type: common.TypeInt64},
	}

	// 第一个引擎实例：多次写入和刷盘
	eng1, err := storage.NewEngine(storage.EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}

	// 三轮刷盘
	for i := 1; i <= 5; i++ {
		_ = eng1.Write(fmt.Sprintf("key%d", i), map[string]common.Value{
			colName: common.NewString(fmt.Sprintf("val%d", i)), colValue: common.NewInt64(int64(i)),
		})
		if i%2 == 0 {
			if err := eng1.Flush(cols); err != nil {
				t.Fatalf("flush failed: %v", err)
			}
		}
	}
	// 最后一次刷盘确保所有 segment 数据落盘
	if err := eng1.Flush(cols); err != nil {
		t.Fatalf("final flush failed: %v", err)
	}

	// 写入一条不刷盘的数据（在 WAL 中）
	_ = eng1.Write("key6", map[string]common.Value{
		colName: common.NewString("val6"), colValue: common.NewInt64(6),
	})

	// 模拟崩溃：不调用 Close
	eng2, err := storage.NewEngine(storage.EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng2.Close() }()

	// 验证所有数据都恢复
	for i := 1; i <= 6; i++ {
		key := fmt.Sprintf("key%d", i)
		row, ok := eng2.Get(key)
		if !ok {
			t.Errorf("expected to find key %s after recovery", key)
			continue
		}
		if v := row.Columns[colName]; v.Str != fmt.Sprintf("val%d", i) {
			t.Errorf("key %s name: expected val%d, got %s", key, i, v.Str)
		}
		if v := row.Columns[colValue]; v.Int64 != int64(i) {
			t.Errorf("key %s value: expected %d, got %d", key, i, v.Int64)
		}
	}
}
