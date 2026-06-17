package memory

import (
	"fmt"
	"sync"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

func intVal(v int64) common.Value { return common.NewInt64(v) }

func strVal(v string) common.Value { return common.NewString(v) }

// TestEngineWriteAndGet 验证单行写入与点查。
func TestEngineWriteAndGet(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	if err := e.Write("k1", map[string]common.Value{"v": intVal(1)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}

	row, ok := e.Get("k1")
	if !ok {
		t.Fatal("Get 未找到 k1")
	}
	if v, ok := row.Columns["v"]; !ok || v.Int64 != 1 {
		t.Errorf("期望 v=1，得到 %v", v)
	}

	if _, ok := e.Get("missing"); ok {
		t.Error("不存在的 key 应返回 false")
	}
}

// TestEngineWriteEmptyKey 验证空 key 被拒绝。
func TestEngineWriteEmptyKey(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	if err := e.Write("", map[string]common.Value{"v": intVal(1)}); err == nil {
		t.Error("空 key 应返回错误")
	}

	if err := e.WriteBatch([]storage.WriteRow{{Key: "", Values: map[string]common.Value{"v": intVal(1)}}}); err == nil {
		t.Error("批量写入空 key 应返回错误")
	}
}

// TestEngineWriteOverwrite 验证同 key 重复写入覆盖旧值。
func TestEngineWriteOverwrite(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	_ = e.Write("k1", map[string]common.Value{"v": intVal(1)})
	_ = e.Write("k1", map[string]common.Value{"v": intVal(2)})

	row, ok := e.Get("k1")
	if !ok {
		t.Fatal("Get 未找到 k1")
	}
	if v := row.Columns["v"].Int64; v != 2 {
		t.Errorf("覆盖后期望 v=2，得到 %d", v)
	}
	if e.RowCount() != 1 {
		t.Errorf("覆盖后行数应为 1，得到 %d", e.RowCount())
	}
}

// TestEngineWriteBatch 验证批量写入与顺序。
func TestEngineWriteBatch(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	rows := []storage.WriteRow{
		{Key: "c", Values: map[string]common.Value{"v": intVal(3)}},
		{Key: "a", Values: map[string]common.Value{"v": intVal(1)}},
		{Key: "b", Values: map[string]common.Value{"v": intVal(2)}},
	}
	if err := e.WriteBatch(rows); err != nil {
		t.Fatalf("WriteBatch 失败: %v", err)
	}

	if e.RowCount() != 3 {
		t.Fatalf("行数应为 3，得到 %d", e.RowCount())
	}

	// 验证扫描结果按 key 升序
	entries := e.ScanRange("", "")
	if len(entries) != 3 {
		t.Fatalf("扫描行数应为 3，得到 %d", len(entries))
	}
	want := []string{"a", "b", "c"}
	for i, w := range want {
		if entries[i].Key != w {
			t.Errorf("第 %d 行 key 期望 %s，得到 %s", i, w, entries[i].Key)
		}
	}
}

// TestEngineWriteBatchEmpty 验证空批量写入为空操作。
func TestEngineWriteBatchEmpty(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	if err := e.WriteBatch(nil); err != nil {
		t.Errorf("空批量写入不应返回错误: %v", err)
	}
	if e.RowCount() != 0 {
		t.Errorf("空批量写入后行数应为 0，得到 %d", e.RowCount())
	}
}

// TestEngineWriteBatchDedup 验证批量写入的去重与 last-wins 语义（review #2 优化回归）。
// 覆盖：批内重复 key、与已存在 key 重复、结果保持有序且版本号为批内最新。
func TestEngineWriteBatchDedup(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	// 预置一行
	if err := e.Write("a", map[string]common.Value{"v": intVal(0)}); err != nil {
		t.Fatalf("预置写入失败: %v", err)
	}

	// 批量写入：a(覆盖预置)、b(批内重复，后写胜出)、c(新)、d(新)
	rows := []storage.WriteRow{
		{Key: "a", Values: map[string]common.Value{"v": intVal(1)}},
		{Key: "b", Values: map[string]common.Value{"v": intVal(10)}},
		{Key: "b", Values: map[string]common.Value{"v": intVal(20)}}, // 批内重复
		{Key: "c", Values: map[string]common.Value{"v": intVal(3)}},
		{Key: "d", Values: map[string]common.Value{"v": intVal(4)}},
	}
	if err := e.WriteBatch(rows); err != nil {
		t.Fatalf("WriteBatch 失败: %v", err)
	}

	if e.RowCount() != 4 {
		t.Fatalf("去重后行数应为 4，得到 %d", e.RowCount())
	}

	entries := e.ScanRange("", "")
	want := []struct {
		key string
		v   int64
	}{
		{"a", 1}, {"b", 20}, {"c", 3}, {"d", 4},
	}
	if len(entries) != len(want) {
		t.Fatalf("扫描行数期望 %d，得到 %d", len(want), len(entries))
	}
	for i, w := range want {
		if entries[i].Key != w.key {
			t.Errorf("第 %d 行 key 期望 %s，得到 %s", i, w.key, entries[i].Key)
		}
		if got := entries[i].Value.Columns["v"].Int64; got != w.v {
			t.Errorf("第 %d 行 v 期望 %d，得到 %d", i, w.v, got)
		}
	}

	// 版本号应单调递增（按写入顺序）
	for i := 1; i < len(entries); i++ {
		if entries[i].Value.Version < entries[i-1].Value.Version {
			t.Errorf("版本号未随写入顺序递增: %d < %d", entries[i].Value.Version, entries[i-1].Value.Version)
		}
	}
}

// TestEngineScanRange 验证范围扫描的边界条件。
func TestEngineScanRange(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	for _, k := range []string{"a", "b", "c", "d", "e"} {
		_ = e.Write(k, map[string]common.Value{"v": strVal(k)})
	}

	tests := []struct {
		name       string
		start, end string
		wantKeys   []string
	}{
		{"全范围", "", "", []string{"a", "b", "c", "d", "e"}},
		{"闭区间", "b", "d", []string{"b", "c", "d"}},
		{"单点", "c", "c", []string{"c"}},
		{"左开右闭", "aa", "c", []string{"b", "c"}},
		{"超出范围", "f", "z", nil},
		{"空起点", "", "b", []string{"a", "b"}},
		{"空终点", "d", "", []string{"d", "e"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := e.ScanRange(tt.start, tt.end)
			if len(entries) != len(tt.wantKeys) {
				t.Fatalf("行数期望 %d，得到 %d", len(tt.wantKeys), len(entries))
			}
			for i, w := range tt.wantKeys {
				if entries[i].Key != w {
					t.Errorf("第 %d 行 key 期望 %s，得到 %s", i, w, entries[i].Key)
				}
			}
		})
	}
}

// TestEngineScanRangeWithPruning 验证内存引擎忽略谓词直接返回范围内全部行。
func TestEngineScanRangeWithPruning(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("k%d", i)
		_ = e.Write(key, map[string]common.Value{"v": intVal(int64(i))})
	}

	preds := []storage.ColumnPredicate{
		{ColumnName: "v", Op: 0, Value: intVal(2)},
	}
	entries := e.ScanRangeWithPruning("k1", "k3", preds)
	if len(entries) != 3 {
		t.Fatalf("期望 3 行（内存引擎不做段裁剪），得到 %d", len(entries))
	}
}

// TestEngineColumnMeta 验证列元数据的设置与读取。
func TestEngineColumnMeta(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	if meta := e.ColumnMeta(); meta != nil {
		t.Errorf("初始 ColumnMeta 应为 nil，得到 %v", meta)
	}

	cols := []storage.ColumnMeta{
		{ID: 0, Name: "id", Type: common.TypeInt64},
		{ID: 1, Name: "name", Type: common.TypeString},
	}
	e.SetColumnMeta(cols)

	meta := e.ColumnMeta()
	if len(meta) != 2 {
		t.Fatalf("期望 2 列元数据，得到 %d", len(meta))
	}
	if meta[0].Name != "id" {
		t.Errorf("第 0 列期望 id，得到 %s", meta[0].Name)
	}

	// 修改返回的副本不应影响内部状态
	meta[0].Name = "modified"
	again := e.ColumnMeta()
	if again[0].Name != "id" {
		t.Errorf("内部状态被外部修改影响: %s", again[0].Name)
	}
}

// TestEngineFlush 验证 Flush 设置列元数据且不报错。
func TestEngineFlush(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	cols := []storage.ColumnMeta{{ID: 0, Name: "id", Type: common.TypeInt64}}
	if err := e.Flush(cols); err != nil {
		t.Fatalf("Flush 不应失败: %v", err)
	}
	if meta := e.ColumnMeta(); len(meta) != 1 {
		t.Errorf("Flush 后 ColumnMeta 应有 1 列，得到 %d", len(meta))
	}
}

// TestEngineNilIndexes 验证 PrimaryIndex 与 SparseIndex 返回 nil。
func TestEngineNilIndexes(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	if e.PrimaryIndex() != nil {
		t.Error("PrimaryIndex 应返回 nil")
	}
	if e.SparseIndex() != nil {
		t.Error("SparseIndex 应返回 nil")
	}
}

// TestEngineClose 验证关闭后操作被拒绝。
func TestEngineClose(t *testing.T) {
	e := New()

	if err := e.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	if err := e.Write("k", map[string]common.Value{"v": intVal(1)}); err == nil {
		t.Error("关闭后 Write 应返回错误")
	}
	if err := e.WriteBatch([]storage.WriteRow{{Key: "k", Values: map[string]common.Value{"v": intVal(1)}}}); err == nil {
		t.Error("关闭后 WriteBatch 应返回错误")
	}
	if entries := e.ScanRange("", ""); entries != nil {
		t.Error("关闭后 ScanRange 应返回 nil")
	}
	if _, ok := e.Get("k"); ok {
		t.Error("关闭后 Get 应返回 false")
	}

	// 多次 Close 安全
	if err := e.Close(); err != nil {
		t.Errorf("多次 Close 不应失败: %v", err)
	}
}

// TestEngineConcurrent 验证并发读写不产生数据竞争且结果正确。
func TestEngineConcurrent(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	const goroutines = 16
	const writesPerG = 100

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < writesPerG; i++ {
				key := fmt.Sprintf("g%d-%d", gid, i)
				_ = e.Write(key, map[string]common.Value{"v": intVal(int64(i))})
			}
		}(g)
	}

	// 并发读
	var rwg sync.WaitGroup
	for r := 0; r < 4; r++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for i := 0; i < 50; i++ {
				_ = e.ScanRange("", "")
			}
		}()
	}

	wg.Wait()
	rwg.Wait()

	want := goroutines * writesPerG
	if e.RowCount() != want {
		t.Errorf("期望 %d 行，得到 %d", want, e.RowCount())
	}

	// 验证扫描结果有序
	entries := e.ScanRange("", "")
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Key > entries[i].Key {
			t.Fatalf("扫描结果未排序: %s > %s", entries[i-1].Key, entries[i].Key)
		}
	}
}

// TestEngineVersionMonotonic 验证版本号单调递增。
func TestEngineVersionMonotonic(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	_ = e.Write("a", map[string]common.Value{"v": intVal(1)})
	row1, _ := e.Get("a")
	_ = e.Write("b", map[string]common.Value{"v": intVal(2)})
	row2, _ := e.Get("b")

	if row2.Version <= row1.Version {
		t.Errorf("版本号应单调递增: v1=%d, v2=%d", row1.Version, row2.Version)
	}
}

// TestEngineStartScheduler 验证 StartScheduler 为空操作不 panic。
func TestEngineStartScheduler(_ *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	e.StartScheduler(storage.SchedulerConfig{})
}
