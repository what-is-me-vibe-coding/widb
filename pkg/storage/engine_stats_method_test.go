package storage

import (
	"fmt"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// newTestEngineForStats 构造一个用于 Stats 测试的 engine。
// 使用 t.TempDir 避免遗留状态；defer Close 由调用方处理。
func newTestEngineForStats(t *testing.T) *Engine {
	t.Helper()
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	return eng
}

// TestEngineStatsEmpty 验证空引擎的 Stats 返回全零值。
func TestEngineStatsEmpty(t *testing.T) {
	eng := newTestEngineForStats(t)
	defer func() { _ = eng.Close() }()

	stats := eng.Stats()
	if stats.RowCount != 0 {
		t.Errorf("空引擎 RowCount = %d, 期望 0", stats.RowCount)
	}
	if stats.SegmentCount != 0 {
		t.Errorf("空引擎 SegmentCount = %d, 期望 0", stats.SegmentCount)
	}
	if stats.L0SegmentCount != 0 {
		t.Errorf("空引擎 L0SegmentCount = %d, 期望 0", stats.L0SegmentCount)
	}
	if stats.ImmutableCount != 0 {
		t.Errorf("空引擎 ImmutableCount = %d, 期望 0", stats.ImmutableCount)
	}
	if stats.MemTableSize < 0 {
		t.Errorf("空引擎 MemTableSize = %d, 不应为负", stats.MemTableSize)
	}
	if stats.ActiveRowCount != 0 {
		t.Errorf("空引擎 ActiveRowCount = %d, 期望 0", stats.ActiveRowCount)
	}
	if stats.ImmutableRowCount != 0 {
		t.Errorf("空引擎 ImmutableRowCount = %d, 期望 0", stats.ImmutableRowCount)
	}
}

// TestEngineStatsAfterWrite 验证写入后 Stats 正确反映行数。
func TestEngineStatsAfterWrite(t *testing.T) {
	eng := newTestEngineForStats(t)
	defer func() { _ = eng.Close() }()

	// 写 5 行
	for i := int64(1); i <= 5; i++ {
		key := fmt.Sprintf("k%d", i)
		if err := eng.Write(key, map[string]common.Value{
			colName: common.NewString("x"),
		}); err != nil {
			t.Fatalf("Write 失败: %v", err)
		}
	}

	stats := eng.Stats()
	if stats.RowCount != 5 {
		t.Errorf("RowCount = %d, 期望 5", stats.RowCount)
	}
	if stats.ActiveRowCount != 5 {
		t.Errorf("ActiveRowCount = %d, 期望 5", stats.ActiveRowCount)
	}
	if stats.SegmentCount != 0 {
		t.Errorf("未 flush 时 SegmentCount = %d, 期望 0", stats.SegmentCount)
	}
	if stats.MemTableSize <= 0 {
		t.Errorf("MemTableSize = %d, 期望 > 0", stats.MemTableSize)
	}
}

// TestEngineStatsAfterFlush 验证 flush 后 SegmentCount >= 1 且 RowCount 仍正确。
func TestEngineStatsAfterFlush(t *testing.T) {
	eng := newTestEngineForStats(t)
	defer func() { _ = eng.Close() }()

	for i := int64(1); i <= 3; i++ {
		key := fmt.Sprintf("k%d", i)
		if err := eng.Write(key, map[string]common.Value{
			colName: common.NewString("v"),
		}); err != nil {
			t.Fatalf("Write 失败: %v", err)
		}
	}

	cols := []ColumnMeta{{ID: 0, Name: colName, Type: common.TypeString}}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	stats := eng.Stats()
	if stats.SegmentCount < 1 {
		t.Errorf("flush 后 SegmentCount = %d, 期望 >= 1", stats.SegmentCount)
	}
	if stats.L0SegmentCount < 1 {
		t.Errorf("flush 后 L0SegmentCount = %d, 期望 >= 1", stats.L0SegmentCount)
	}
	if stats.RowCount != 3 {
		t.Errorf("flush 后 RowCount = %d, 期望 3", stats.RowCount)
	}
}
