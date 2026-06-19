package storage

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// 本文件补充 LSM 引擎 SetColumnMeta 的直接单元测试。
// 该方法此前仅由 pkg/server 在建表时调用，在 storage 包内覆盖率为 0%。

// TestEngineSetColumnMeta 验证 SetColumnMeta 设置列元数据后 ColumnMeta 返回一致，
// 且 SetColumnMeta 与 ColumnMeta 均做拷贝，外部修改不影响引擎内部状态。
func TestEngineSetColumnMeta(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	input := []ColumnMeta{
		{ID: 0, Name: "id", Type: common.TypeInt64},
		{ID: 1, Name: "name", Type: common.TypeString},
	}
	eng.SetColumnMeta(input)

	got := eng.ColumnMeta()
	if len(got) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(got))
	}
	if got[0].Name != "id" || got[1].Name != "name" {
		t.Errorf("unexpected column meta: %+v", got)
	}

	// 修改返回的切片不应影响引擎内部状态
	got[0].Name = "mutated"
	again := eng.ColumnMeta()
	if again[0].Name != "id" {
		t.Errorf("ColumnMeta 应返回副本, 修改后再次查询得到 %q", again[0].Name)
	}

	// 修改输入切片不应影响引擎内部状态
	input[1].Name = "mutated_input"
	if eng.ColumnMeta()[1].Name != "name" {
		t.Errorf("SetColumnMeta 应拷贝输入, 修改输入后得到 %q", eng.ColumnMeta()[1].Name)
	}
}

// TestEngineSetColumnMetaReplace 验证重复调用 SetColumnMeta 替换而非追加列元数据。
func TestEngineSetColumnMetaReplace(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	eng.SetColumnMeta([]ColumnMeta{{ID: 0, Name: "a", Type: common.TypeInt64}})
	eng.SetColumnMeta([]ColumnMeta{
		{ID: 0, Name: "x", Type: common.TypeString},
		{ID: 1, Name: "y", Type: common.TypeFloat64},
	})
	got := eng.ColumnMeta()
	if len(got) != 2 || got[0].Name != "x" || got[1].Name != "y" {
		t.Errorf("SetColumnMeta 应替换旧元数据, 得到 %+v", got)
	}
}
