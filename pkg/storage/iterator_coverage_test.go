package storage

import (
	"fmt"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// --- scanRangeUnlocked 和迭代器测试 ---

// TestScanRangeUnlockedEmpty_V7 测试 scanRangeUnlocked 在没有迭代器时返回 nil。
func TestScanRangeUnlockedEmpty_V7(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 空引擎，无数据，scanRangeUnlocked 应返回空结果
	eng.mu.RLock()
	results := eng.scanRangeUnlocked("a", "z")
	eng.mu.RUnlock()

	if len(results) != 0 {
		t.Errorf("期望 0 条结果，实际 %d 条", len(results))
	}
}

// TestScanRangeWithMultipleSegments_V7 测试 scanRangeUnlocked 跨多个 segment 的扫描。
func TestScanRangeWithMultipleSegments_V7(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// 写入第一批数据并刷盘
	for i := 0; i < 5; i++ {
		key := string(rune('a' + i))
		_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))})
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 1: %v", err)
	}

	// 写入第二批数据并刷盘
	for i := 5; i < 10; i++ {
		key := string(rune('a' + i))
		_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))})
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 2: %v", err)
	}

	// 扫描全部范围
	results := eng.ScanRange("a", "z")
	if len(results) != 10 {
		t.Errorf("期望 10 条结果，实际 %d", len(results))
	}
}

// TestMergeIteratorErrorPropagation_V7 测试 MergeIterator 传播迭代器错误。
func TestMergeIteratorErrorPropagation_V7(t *testing.T) {
	// 创建一个返回错误的迭代器
	errIt := &errIterV7{err: fmt.Errorf("迭代错误")}

	mi := NewMergeIterator(errIt)
	defer mi.Close()

	// Next 应返回 false
	if mi.Next() {
		t.Error("期望 Next 返回 false")
	}
	// Err 应返回迭代器的错误
	if mi.Err() == nil {
		t.Error("期望错误，实际 nil")
	}
}

// errIterV7 是一个总是返回错误的 ScanIterator 实现。
type errIterV7 struct {
	err error
}

func (it *errIterV7) Next() bool       { return false }
func (it *errIterV7) Entry() ScanEntry { return ScanEntry{} }
func (it *errIterV7) Err() error       { return it.err }
func (it *errIterV7) Close()           {}

// TestSliceIteratorEntryBeforeNext_V7 测试 sliceIterator 在 Next 之前调用 Entry 返回空。
func TestSliceIteratorEntryBeforeNext_V7(t *testing.T) {
	entries := []ScanEntry{
		{Key: "a", Value: Row{Columns: map[string]common.Value{"v": common.NewInt64(1)}}},
	}
	it := newSliceIterator(entries)
	defer it.Close()

	// 未调用 Next 时 Entry 应返回空
	entry := it.Entry()
	if entry.Key != "" {
		t.Errorf("期望空 Key，实际 %q", entry.Key)
	}
}

// TestSliceIteratorExhausted_V7 测试 sliceIterator 超出范围后返回 false。
func TestSliceIteratorExhausted_V7(t *testing.T) {
	entries := []ScanEntry{
		{Key: "a", Value: Row{Columns: map[string]common.Value{"v": common.NewInt64(1)}}},
	}
	it := newSliceIterator(entries)

	if !it.Next() {
		t.Fatal("期望第一个 Next 返回 true")
	}
	if it.Next() {
		t.Error("期望第二个 Next 返回 false（已耗尽）")
	}
	it.Close()
}

// TestMemTableIteratorEntryOutOfRange_V7 测试 memTableIterator 越界访问。
func TestMemTableIteratorEntryOutOfRange_V7(t *testing.T) {
	mem := NewMemTable()
	it := newMemTableIterator(mem, "a", "z")
	defer it.Close()

	// 空 memtable，Next 返回 false，Entry 应返回空
	entry := it.Entry()
	if entry.Key != "" {
		t.Errorf("期望空 Key，实际 %q", entry.Key)
	}
}

// TestSegmentIteratorEntryBeforeStart_V7 测试 segmentIterator 未开始时 Entry 返回空。
func TestSegmentIteratorEntryBeforeStart_V7(t *testing.T) {
	seg := buildTestSegment(t, []string{"a", "b"}, []int64{1, 2})
	colMeta := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	it := newSegmentIterator(seg, colMeta, "a", "b", nil)

	// 未调用 Next 时 Entry 应返回空
	entry := it.Entry()
	if entry.Key != "" {
		t.Errorf("期望空 Key，实际 %q", entry.Key)
	}
	it.Close()
}

// TestMergeIteratorAdvanceNext_V7 测试 MergeIterator 的 advanceNext 路径。
func TestMergeIteratorAdvanceNext_V7(t *testing.T) {
	it1 := newSliceIterator([]ScanEntry{
		{Key: "a", Value: Row{Columns: map[string]common.Value{"v": common.NewInt64(1)}}},
		{Key: "c", Value: Row{Columns: map[string]common.Value{"v": common.NewInt64(3)}}},
	})
	it2 := newSliceIterator([]ScanEntry{
		{Key: "a", Value: Row{Columns: map[string]common.Value{"v": common.NewInt64(10)}}},
		{Key: "b", Value: Row{Columns: map[string]common.Value{"v": common.NewInt64(20)}}},
	})

	mi := NewMergeIterator(it1, it2)
	defer mi.Close()

	var keys []string
	for mi.Next() {
		keys = append(keys, mi.Entry().Key)
	}

	expected := []string{"a", "b", "c"}
	if len(keys) != len(expected) {
		t.Fatalf("期望 %d 个 key，实际 %d: %v", len(expected), len(keys), keys)
	}
	for i, k := range keys {
		if k != expected[i] {
			t.Errorf("key[%d]: 期望 %q, 实际 %q", i, expected[i], k)
		}
	}
}

// ---------------------------------------------------------------------------
// scanRangeUnlocked 测试：各种数据源组合
// ---------------------------------------------------------------------------

// TestScanRangeActiveMemTableOnly 测试仅从活跃 memtable 扫描数据。
func TestScanRangeActiveMemTableOnly(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 仅写入活跃 memtable，不 Flush
	if err := eng.Write("scan_a", map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if err := eng.Write("scan_b", map[string]common.Value{colVal: common.NewInt64(2)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if err := eng.Write("scan_c", map[string]common.Value{colVal: common.NewInt64(3)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}

	results := eng.ScanRange("scan_a", "scan_c")
	if len(results) != 3 {
		t.Fatalf("期望 3 条结果，实际 %d", len(results))
	}

	// 验证结果按键排序
	expectedKeys := []string{"scan_a", "scan_b", "scan_c"}
	for i, entry := range results {
		if entry.Key != expectedKeys[i] {
			t.Errorf("索引 %d: 期望 key %q，实际 %q", i, expectedKeys[i], entry.Key)
		}
	}
}

// TestScanRangeMemTableAndSegment 测试同时从 memtable 和 segment 扫描数据。
func TestScanRangeMemTableAndSegment(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// 写入数据并 Flush 到 segment
	if err := eng.Write("ms_a", map[string]common.Value{colVal: common.NewInt64(10)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if err := eng.Write("ms_b", map[string]common.Value{colVal: common.NewInt64(20)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	// 再写入活跃 memtable 的数据
	if err := eng.Write("ms_c", map[string]common.Value{colVal: common.NewInt64(30)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if err := eng.Write("ms_d", map[string]common.Value{colVal: common.NewInt64(40)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}

	results := eng.ScanRange("ms_a", "ms_d")
	if len(results) != 4 {
		t.Fatalf("期望 4 条结果，实际 %d", len(results))
	}

	expectedKeys := []string{"ms_a", "ms_b", "ms_c", "ms_d"}
	for i, entry := range results {
		if entry.Key != expectedKeys[i] {
			t.Errorf("索引 %d: 期望 key %q，实际 %q", i, expectedKeys[i], entry.Key)
		}
	}
}

// TestScanRangeEmptyRange 测试扫描范围无匹配键。
func TestScanRangeEmptyRange(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// 写入数据
	if err := eng.Write("er_a", map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if err := eng.Write("er_z", map[string]common.Value{colVal: common.NewInt64(2)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	// 扫描不包含任何键的范围
	results := eng.ScanRange("er_m", "er_n")
	if len(results) != 0 {
		t.Errorf("期望 0 条结果，实际 %d", len(results))
	}
}

// TestScanRangeFullRange 测试扫描完整范围。
func TestScanRangeFullRange(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// 写入多批数据并 Flush
	for i := 0; i < 5; i++ {
		key := string(rune('a' + i))
		if err := eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))}); err != nil {
			t.Fatalf("Write 失败: %v", err)
		}
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	// 再写入一些数据到 memtable
	if err := eng.Write("f", map[string]common.Value{colVal: common.NewInt64(5)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if err := eng.Write("g", map[string]common.Value{colVal: common.NewInt64(6)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}

	// 扫描完整范围
	results := eng.ScanRange("a", "z")
	if len(results) != 7 {
		t.Fatalf("期望 7 条结果，实际 %d", len(results))
	}

	// 验证按键排序
	for i, entry := range results {
		expected := string(rune('a' + i))
		if entry.Key != expected {
			t.Errorf("索引 %d: 期望 key %q，实际 %q", i, expected, entry.Key)
		}
	}
}

// TestScanRangeKeyDeduplication 测试跨 memtable 和 segment 的键去重。
func TestScanRangeKeyDeduplication(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// 写入旧版本数据并 Flush
	if err := eng.Write("dedup_key", map[string]common.Value{colVal: common.NewInt64(100)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	// 写入相同键的新版本到 memtable（新数据优先）
	if err := eng.Write("dedup_key", map[string]common.Value{colVal: common.NewInt64(999)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}

	results := eng.ScanRange("dedup_key", "dedup_key")
	if len(results) != 1 {
		t.Fatalf("期望 1 条结果（去重），实际 %d", len(results))
	}

	// 新数据应优先（来自 memtable，优先级更高）
	if results[0].Value.Columns[colVal].Int64 != 999 {
		t.Errorf("期望值 999（新数据），实际 %d", results[0].Value.Columns[colVal].Int64)
	}
}

// TestScanRangeWithImmutableMemTable 测试包含 immutable memtable 的扫描。
func TestScanRangeWithImmutableMemTable(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir:         dir,
		MaxMemTableSize: 128,
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入足够数据触发 memtable 轮转，产生 immutable memtable
	for i := 0; i < 50; i++ {
		key := string(rune('a' + i%26))
		if err := eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))}); err != nil {
			t.Fatalf("Write %d 失败: %v", i, err)
		}
	}

	// 不 Flush，让 immutable memtable 保留在内存中
	eng.mu.RLock()
	immutableCount := len(eng.immutable)
	eng.mu.RUnlock()

	if immutableCount == 0 {
		t.Log("未产生 immutable memtable，跳过验证")
	}

	// 扫描应包含所有数据源
	results := eng.ScanRange("a", "z")
	if len(results) == 0 {
		t.Error("期望非空结果")
	}
}

// TestScanRangeSegmentRangeFiltering 测试 segment 的范围过滤。
func TestScanRangeSegmentRangeFiltering(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// 写入一批数据并 Flush（segment 范围 a-e）
	for i := 0; i < 5; i++ {
		key := string(rune('a' + i))
		if err := eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))}); err != nil {
			t.Fatalf("Write 失败: %v", err)
		}
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	// 写入另一批数据并 Flush（segment 范围 f-j）
	for i := 5; i < 10; i++ {
		key := string(rune('a' + i))
		if err := eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))}); err != nil {
			t.Fatalf("Write 失败: %v", err)
		}
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	// 扫描仅第一个 segment 的范围
	results := eng.ScanRange("a", "e")
	if len(results) != 5 {
		t.Fatalf("期望 5 条结果，实际 %d", len(results))
	}

	// 验证结果都在 a-e 范围内
	for _, entry := range results {
		if entry.Key < "a" || entry.Key > "e" {
			t.Errorf("结果 key %q 超出范围 [a, e]", entry.Key)
		}
	}
}
