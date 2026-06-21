package memory

import (
	"strconv"
	"sync"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// TestScanRangeWithPruning_PassesThroughPredicates 验证 ScanRangeWithPruning 与 ScanRange
// 行为一致：内存引擎无 Segment 概念，不做段裁剪，列谓词由查询执行器处理。
func TestScanRangeWithPruning_PassesThroughPredicates(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	// 写若干行
	rows := []struct {
		key string
		v   int64
	}{
		{"a", 1}, {"b", 2}, {"c", 3}, {"d", 4}, {"e", 5},
	}
	for _, r := range rows {
		if err := e.Write(r.key, map[string]common.Value{"v": intVal(r.v)}); err != nil {
			t.Fatalf("write %s: %v", r.key, err)
		}
	}

	// 传入非空列谓词：内存引擎应忽略并返回范围内全部行
	predicates := []storage.ColumnPredicate{
		{ColumnName: "v", Op: index.OpGreater, Value: intVal(2)},
	}
	got := e.ScanRangeWithPruning("b", "d", predicates)
	if len(got) != 3 {
		t.Fatalf("expected 3 entries in [b,d], got %d: %+v", len(got), keysOf(got))
	}
	for i, k := range []string{"b", "c", "d"} {
		if got[i].Key != k {
			t.Errorf("entry %d: key=%q, want %q", i, got[i].Key, k)
		}
	}
}

// TestOperationsAfterClose_ReturnError 验证引擎 Close 后所有操作按规约返回（不 panic）。
func TestOperationsAfterClose_ReturnError(t *testing.T) {
	e := New()
	// 在 Close 前写一行，便于验证 Get 在 Close 后返回 false
	if err := e.Write("k", map[string]common.Value{"v": intVal(1)}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 关闭后 Write/Delete 必须返回 errEngineClosed
	if err := e.Write("k2", map[string]common.Value{"v": intVal(2)}); err == nil {
		t.Error("Write after Close should error")
	}
	if err := e.Delete("k"); err == nil {
		t.Error("Delete after Close should error")
	}
	// 关闭后 Get/ScanRange 静默返回零值（不 panic）
	if _, ok := e.Get("k"); ok {
		t.Error("Get after Close should miss")
	}
	if got := e.ScanRange("", ""); got != nil {
		t.Errorf("ScanRange after Close should be nil, got %d entries", len(got))
	}
	// 再次 Close 应安全
	if err := e.Close(); err != nil {
		t.Errorf("double Close: %v", err)
	}
}

// TestColumnMeta_RoundTrip 验证 SetColumnMeta / ColumnMeta 正确往返。
func TestColumnMeta_RoundTrip(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	cols := []storage.ColumnMeta{
		{ID: 0, Name: "id", Type: common.TypeInt64},
		{ID: 1, Name: "name", Type: common.TypeString},
	}
	e.SetColumnMeta(cols)

	got := e.ColumnMeta()
	if len(got) != len(cols) {
		t.Fatalf("len: got %d, want %d", len(got), len(cols))
	}
	for i := range cols {
		if got[i] != cols[i] {
			t.Errorf("col %d: got %+v, want %+v", i, got[i], cols[i])
		}
	}

	// 修改返回切片不应影响引擎内部
	if len(got) > 0 {
		got[0].Name = "MUTATED"
	}
	again := e.ColumnMeta()
	if again[0].Name != "id" {
		t.Errorf("ColumnMeta result should be a copy; internal mutated to %q", again[0].Name)
	}
}

// TestColumnMeta_EmptyWhenUnset 验证未设置时 ColumnMeta 返回 nil。
func TestColumnMeta_EmptyWhenUnset(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	if got := e.ColumnMeta(); got != nil {
		t.Errorf("expected nil, got %d entries", len(got))
	}
}

// TestRowCount 验证 RowCount 随写入/删除正确变化。
func TestRowCount(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	if e.RowCount() != 0 {
		t.Errorf("initial RowCount: got %d, want 0", e.RowCount())
	}
	_ = e.Write("a", map[string]common.Value{"v": intVal(1)})
	_ = e.Write("b", map[string]common.Value{"v": intVal(2)})
	_ = e.Write("c", map[string]common.Value{"v": intVal(3)})
	if e.RowCount() != 3 {
		t.Errorf("after 3 writes: got %d, want 3", e.RowCount())
	}
	_ = e.Delete("b")
	if e.RowCount() != 2 {
		t.Errorf("after delete: got %d, want 2", e.RowCount())
	}
	// 覆盖已存在 key 不增加行数
	_ = e.Write("a", map[string]common.Value{"v": intVal(99)})
	if e.RowCount() != 2 {
		t.Errorf("after overwrite: got %d, want 2", e.RowCount())
	}
}

// TestScanRange_EmptyRange 验证空范围返回空切片。
func TestScanRange_EmptyRange(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	// 空引擎扫描
	if got := e.ScanRange("", ""); len(got) != 0 {
		t.Errorf("empty engine: got %d entries, want empty", len(got))
	}

	_ = e.Write("a", map[string]common.Value{"v": intVal(1)})
	_ = e.Write("b", map[string]common.Value{"v": intVal(2)})
	// 范围小于最小 key
	if got := e.ScanRange("aa", "az"); len(got) != 0 {
		t.Errorf("range above min: got %d entries, want empty", len(got))
	}
	// 范围大于最大 key
	if got := e.ScanRange("c", "z"); len(got) != 0 {
		t.Errorf("range above max: got %d entries, want empty", len(got))
	}
}

// TestConcurrentReadWrite_NoRace 验证并发读写不触发竞态（-race 模式由 CI 覆盖）。
func TestConcurrentReadWrite_NoRace(_ *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	const writers = 4
	const readers = 4
	const ops = 200

	var wg sync.WaitGroup
	// writer
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				key := keyFor(w, i)
				_ = e.Write(key, map[string]common.Value{"v": intVal(int64(i))})
			}
		}(w)
	}
	// reader
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				key := keyFor(r%writers, i)
				_, _ = e.Get(key)
				_ = e.ScanRange("", "z")
			}
		}(r)
	}
	wg.Wait()
}

func keyFor(w, i int) string {
	return string(rune('a'+w)) + "_" + strconv.Itoa(i)
}

func keysOf(entries []storage.ScanEntry) []string {
	keys := make([]string, len(entries))
	for i, e := range entries {
		keys[i] = e.Key
	}
	return keys
}
