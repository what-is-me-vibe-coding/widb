package memory

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// TestEngineStatsEmpty 验证空引擎的 Stats 返回全零值。
func TestEngineStatsEmpty(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	stats := e.Stats()
	if stats.RowCount != 0 {
		t.Errorf("空引擎 RowCount = %d, 期望 0", stats.RowCount)
	}
	if stats.ColumnCount != 0 {
		t.Errorf("空引擎 ColumnCount = %d, 期望 0", stats.ColumnCount)
	}
}

// TestEngineStatsAfterWrite 验证写入后 Stats 正确反映行数与列数。
func TestEngineStatsAfterWrite(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	// SetColumnMeta 是 columnMeta 的标准入口；直接 Write 不会触发。
	cols := []storage.ColumnMeta{
		{ID: 0, Name: "v1", Type: common.TypeInt64},
		{ID: 1, Name: "v2", Type: common.TypeString},
	}
	e.SetColumnMeta(cols)

	if err := e.Write("k1", map[string]common.Value{
		"v1": intVal(1),
		"v2": strVal("x"),
	}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if err := e.Write("k2", map[string]common.Value{
		"v1": intVal(2),
		"v2": strVal("y"),
	}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}

	stats := e.Stats()
	if stats.RowCount != 2 {
		t.Errorf("RowCount = %d, 期望 2", stats.RowCount)
	}
	if stats.ColumnCount != 2 {
		t.Errorf("ColumnCount = %d, 期望 2", stats.ColumnCount)
	}
}

// TestEngineStatsAfterDelete 验证 Delete 后行数不立即下降（被墓碑覆盖）。
// 内存引擎为简单字典结构，Delete 直接移除键，Stats 反映真实存活行数。
func TestEngineStatsAfterDelete(t *testing.T) {
	e := New()
	defer func() { _ = e.Close() }()

	_ = e.Write("k1", map[string]common.Value{"v": intVal(1)})
	_ = e.Write("k2", map[string]common.Value{"v": intVal(2)})

	stats := e.Stats()
	if stats.RowCount != 2 {
		t.Errorf("写两行后 RowCount = %d, 期望 2", stats.RowCount)
	}

	if err := e.Delete("k1"); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}

	stats = e.Stats()
	if stats.RowCount != 1 {
		t.Errorf("Delete 后 RowCount = %d, 期望 1", stats.RowCount)
	}
}
