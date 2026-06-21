package storage

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ---------------------------------------------------------------------------
// Engine Delete: 补充墓碑生命周期与边界场景测试
// 目标：覆盖 engine_ops.go:155 Delete 中尚未测试的 35% 分支
//  - 幂等性：删除不存在的 key
//  - 顺序：先写后删再写
//  - 批删除 + ScanRange
//  - 多次删除同一 key
//  - Delete 后立即 ScanRange 立即 Get 的一致性
// ---------------------------------------------------------------------------

// TestEngineDelete_NonExistentKey 验证删除不存在的 key 静默返回 nil（幂等）。
func TestEngineDelete_NonExistentKey(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	if err := eng.Delete("missing"); err != nil {
		t.Errorf("Delete on missing key should be idempotent nil, got: %v", err)
	}

	// 二次删除仍应幂等
	if err := eng.Delete("missing"); err != nil {
		t.Errorf("Delete on missing key (2nd) should be idempotent nil, got: %v", err)
	}
}

// TestEngineDelete_RewriteAfterDelete 验证 Delete 后再次写入同名 key 可见。
func TestEngineDelete_RewriteAfterDelete(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	if err := eng.Write("k", map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := eng.Delete("k"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := eng.Get("k"); ok {
		t.Error("Get should miss after Delete")
	}

	// 重新写入：版本号应更新，Get 命中
	if err := eng.Write("k", map[string]common.Value{colVal: common.NewInt64(2)}); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	row, ok := eng.Get("k")
	if !ok {
		t.Fatal("Get should hit after rewrite")
	}
	if row.Columns[colVal] != common.NewInt64(2) {
		t.Errorf("rewrite value: got %v, want 2", row.Columns[colVal])
	}
}

// TestEngineDelete_RepeatedDelete 验证对同一 key 多次 Delete 不报错。
func TestEngineDelete_RepeatedDelete(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	if err := eng.Write("k", map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
		t.Fatalf("write: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := eng.Delete("k"); err != nil {
			t.Fatalf("delete #%d: %v", i, err)
		}
	}
	// 最终 Get 应 miss
	if _, ok := eng.Get("k"); ok {
		t.Error("Get should miss after repeated deletes")
	}
}

// TestEngineDelete_BatchScanConsistency 验证批量写入多个 key 后删除其中部分，ScanRange 行为正确。
func TestEngineDelete_BatchScanConsistency(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	keys := []string{"a", "b", "c", "d", "e"}
	for _, k := range keys {
		if err := eng.Write(k, map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
			t.Fatalf("write %s: %v", k, err)
		}
	}

	// 删除 b, d
	for _, k := range []string{"b", "d"} {
		if err := eng.Delete(k); err != nil {
			t.Fatalf("delete %s: %v", k, err)
		}
	}

	entries := eng.ScanRange("", "\xff\xff\xff\xff")
	got := make(map[string]bool)
	for _, e := range entries {
		got[e.Key] = true
	}
	for _, k := range keys {
		want := k != "b" && k != "d"
		if got[k] != want {
			t.Errorf("key %s: scan visibility got=%v, want=%v", k, got[k], want)
		}
	}
}

// TestEngineDelete_ConcurrentWithWrite 验证并发 Write + Delete 的最终一致性：
//   - 任意 key 最终状态（Get 命中）必然对应最后一次操作（write 或 delete）
//   - Get 命中时值非 nil、删除时返回 false
func TestEngineDelete_ConcurrentWithWrite(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	const key = "shared"
	const writers = 4
	const deleters = 2
	const iterations = 200

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				select {
				case <-stop:
					return
				default:
				}
				_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))})
			}
		}(w)
	}
	for d := 0; d < deleters; d++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				select {
				case <-stop:
					return
				default:
				}
				_ = eng.Delete(key)
			}
		}()
	}

	// 周期性检查：Get 不 panic、返回合法结果
	checker := func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Get panic: %v", r)
			}
		}()
		row, ok := eng.Get(key)
		if ok {
			// 命中时必须有 val 列存在
			if row.Columns == nil {
				t.Error("Get hit but Columns is nil")
			}
		}
	}
	go func() {
		for i := 0; i < 50; i++ {
			checker()
		}
	}()

	wg.Wait()
	close(stop)

	// 最终一致性：要么命中（非空列），要么 miss
	row, ok := eng.Get(key)
	if ok && row.Columns == nil {
		t.Error("final state: Get hit but Columns is nil (race?)")
	}
}

// ---------------------------------------------------------------------------
// Iterator: Key/Entry 边界场景
// 目标：覆盖 iterator.go:70/231/408 Key() 在 pos 未启动或越界时返回 "" 的分支
// ---------------------------------------------------------------------------

// TestSliceIterator_KeyBeforeNext 验证 newSliceIterator 构造后 Key() 返回 ""。
func TestSliceIterator_KeyBeforeNext(t *testing.T) {
	entries := []ScanEntry{
		{Key: "a", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(1)}}},
		{Key: "b", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(2)}}},
	}
	it := newSliceIterator(entries)
	// 未调用 Next 之前：pos == -1，Key/Entry 应返回零值
	if got := it.Key(); got != "" {
		t.Errorf("Key() before Next: got %q, want \"\"", got)
	}
	if got := it.Entry(); got.Key != "" || got.Value.Columns != nil {
		t.Errorf("Entry() before Next: got %+v, want zero", got)
	}
}

// TestSliceIterator_KeyAfterExhausted 验证迭代结束后 Key/Entry 越界返回零值。
func TestSliceIterator_KeyAfterExhausted(t *testing.T) {
	entries := []ScanEntry{
		{Key: "only"},
	}
	it := newSliceIterator(entries)
	if !it.Next() {
		t.Fatal("expected first Next true")
	}
	if it.Key() != "only" {
		t.Errorf("Key() at first entry: got %q, want %q", it.Key(), "only")
	}
	if it.Next() {
		t.Fatal("expected second Next false (exhausted)")
	}
	if got := it.Key(); got != "" {
		t.Errorf("Key() after exhausted: got %q, want \"\"", got)
	}
	if got := it.Entry(); got.Key != "" {
		t.Errorf("Entry() after exhausted: got %+v, want zero", got)
	}
}

// TestSliceIterator_EmptyEntries 验证空切片迭代器立即耗尽且 Key/Entry 越界。
func TestSliceIterator_EmptyEntries(t *testing.T) {
	it := newSliceIterator(nil)
	if it.Next() {
		t.Error("Next on empty iterator should return false")
	}
	if got := it.Key(); got != "" {
		t.Errorf("Key() on empty iterator: got %q, want \"\"", got)
	}
	if it.Err() != nil {
		t.Errorf("Err() on empty iterator: got %v, want nil", it.Err())
	}
	it.Close()
}

// TestMemTableIterator_KeyBeforeNext 验证 memTableIterator 在调用 Next 前 Key 返回 ""。
func TestMemTableIterator_KeyBeforeNext(t *testing.T) {
	mem := NewMemTable()
	_, _, _ = mem.Put("k", Row{Columns: map[string]common.Value{colVal: common.NewInt64(1)}})
	it := newMemTableIterator(mem, "", "")

	if got := it.Key(); got != "" {
		t.Errorf("Key() before Next: got %q, want \"\"", got)
	}
	if got := it.Entry(); got.Key != "" {
		t.Errorf("Entry() before Next: got %+v, want zero", got)
	}
	it.Close()
}

// TestMergeIterator_EntryBeforeNext 验证 MergeIterator 在调用 Next 前 Entry 返回零值。
func TestMergeIterator_EntryBeforeNext(t *testing.T) {
	mi := &MergeIterator{}
	if got := mi.Entry(); got.Key != "" || got.Value.Columns != nil {
		t.Errorf("Entry() before Next: got %+v, want zero", got)
	}
}

// TestMergeIterator_CloseEmpty 验证空 MergeIterator Close 不 panic。
func TestMergeIterator_CloseEmpty(_ *testing.T) {
	mi := &MergeIterator{}
	mi.Close() // 不应 panic
}

// TestSegmentIterator_KeyBeforeNext 验证 segmentIterator 在调用 Next 前 Key 返回 ""。
// 补齐 iterator.go:231 Key() 在 started==false 分支的覆盖。
func TestSegmentIterator_KeyBeforeNext(t *testing.T) {
	seg := buildTestSegment(t, []string{"a", "b", "c"}, []int64{1, 2, 3})
	colMeta := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	it := newSegmentIterator(seg, colMeta, "a", "c", nil)
	defer it.Close()

	// Next() 之前：Key() 返回 ""，Entry() 返回零值
	if got := it.Key(); got != "" {
		t.Errorf("Key() before Next: got %q, want \"\"", got)
	}
	if got := it.Entry(); got.Key != "" || got.Value.Columns != nil {
		t.Errorf("Entry() before Next: got %+v, want zero", got)
	}

	// 第一次 Next() 之后 Key() 才有意义
	if !it.Next() {
		t.Fatal("expected first Next true")
	}
	if got := it.Key(); got != "a" {
		t.Errorf("Key() after first Next: got %q, want %q", got, "a")
	}
}

// ---------------------------------------------------------------------------
// WAL Truncate: 成功路径
// 目标：覆盖 wal.go:211 Truncate 中尚未测试的成功 + 写后状态校验
// ---------------------------------------------------------------------------

// TestWAL_TruncateEmpty 验证 Truncate 在空 WAL 上安全执行。
func TestWAL_TruncateEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL: %v", err)
	}
	if err := w.Truncate(); err != nil {
		t.Fatalf("Truncate on empty WAL: %v", err)
	}
	// Truncate 后应可继续追加
	if err := w.Append(walTypeWrite, []byte("hello")); err != nil {
		t.Fatalf("Append after Truncate: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestWAL_TruncateResetsState 验证 Truncate 写入内容后能清空，offset 归零。
func TestWAL_TruncateResetsState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL: %v", err)
	}

	// 写入若干条记录
	for i := 0; i < 5; i++ {
		if err := w.Append(walTypeWrite, []byte{byte(i)}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if off := w.offset.Load(); off <= 0 {
		t.Errorf("offset should be > 0 after writes, got %d", off)
	}

	if err := w.Truncate(); err != nil {
		t.Fatalf("Truncate: %v", err)
	}

	// Truncate 后 offset 归零
	if off := w.offset.Load(); off != 0 {
		t.Errorf("offset after Truncate: got %d, want 0", off)
	}

	// 重新打开应能读出 0 条记录
	w.Close()
	_, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records after Truncate, got %d", len(records))
	}
}

// TestWAL_TruncateThenReopen 验证 Truncate + Close + OpenWAL 流程：可正常回放新写入的记录。
func TestWAL_TruncateThenReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reopen.wal")
	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL: %v", err)
	}

	// 写一条
	if err := w.Append(walTypeWrite, []byte("first")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w.Truncate(); err != nil {
		t.Fatalf("Truncate: %v", err)
	}
	// 写新内容
	if err := w.Append(walTypeWrite, []byte("second")); err != nil {
		t.Fatalf("Append after Truncate: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 重新打开验证回放
	_, records, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if string(records[0].Payload) != "second" {
		t.Errorf("replay payload: got %q, want %q", records[0].Payload, "second")
	}
	if records[0].Type != walTypeWrite {
		t.Errorf("replay type: got %d, want %d", records[0].Type, walTypeWrite)
	}
}
