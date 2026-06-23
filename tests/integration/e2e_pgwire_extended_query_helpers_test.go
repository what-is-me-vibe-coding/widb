// 单元测试 pgExtResult 辅助函数（cell / cellIsNull / findRow / rowCount / columnCount）
// 在负数索引、空结果、列不存在等边界场景下的鲁棒性。
//
// 这是对 PR #237 review 中"helper 健壮性"意见的回归覆盖：从主测试文件
// e2e_pgwire_extended_query_test.go 拆出独立文件，避免单文件超过 CI 强制
// 800 行上限；同 package 复用其中的 pgExtResult 类型与构造。
package integration

import "testing"

// TestPGExtResultHelpers 入口：拆成 5 个最小子测试，每个仅 1-3 个分支，
// 避免单函数循环/认知复杂度过高。
func TestPGExtResultHelpers(t *testing.T) {
	t.Run("CellNormalAndBoundary", testPGExtCellCases)
	t.Run("CellIsNullGuards", testPGExtCellIsNullCases)
	t.Run("FindRowHitMissMissing", testPGExtFindRowCases)
	t.Run("CountsOnTwoRowResult", testPGExtCounts)
	t.Run("EmptyResultGuards", testPGExtEmptyResultGuards)
}

func testPGExtCellCases(t *testing.T) {
	r := newTwoRowResult()
	cases := []struct {
		rowIdx int
		col    string
		wantV  string
		wantOK bool
	}{
		{0, "name", "alpha", true},
		{1, "id", "2", true},
		{99, "id", "", false},     // 行越界
		{-1, "id", "", false},     // 负数行索引：必须返回 zero value 而非 panic
		{0, "missing", "", false}, // 列不存在
	}
	for _, c := range cases {
		got, ok := r.cell(c.rowIdx, c.col)
		if got != c.wantV || ok != c.wantOK {
			t.Errorf("cell(%d, %q) = (%q, %v), want (%q, %v)",
				c.rowIdx, c.col, got, ok, c.wantV, c.wantOK)
		}
	}
}

func testPGExtCellIsNullCases(t *testing.T) {
	r := newTwoRowResult()
	if r.cellIsNull(-1, "id") {
		t.Error("cellIsNull(-1, id) 期望 false")
	}
	if r.cellIsNull(0, "missing") {
		t.Error("cellIsNull(0, missing) 期望 false")
	}
	if r.cellIsNull(0, "id") {
		t.Error("cellIsNull(0, id) 期望 false（非 NULL 列）")
	}
}

func testPGExtFindRowCases(t *testing.T) {
	r := newTwoRowResult()
	if idx := r.findRow("name", "beta"); idx != 1 {
		t.Errorf("findRow(name, beta) = %d, want 1", idx)
	}
	if idx := r.findRow("name", "nope"); idx != -1 {
		t.Errorf("findRow(name, nope) = %d, want -1", idx)
	}
	if idx := r.findRow("missing", "x"); idx != -1 {
		t.Errorf("findRow(missing, x) = %d, want -1", idx)
	}
}

func testPGExtCounts(t *testing.T) {
	r := newTwoRowResult()
	if r.rowCount() != 2 {
		t.Errorf("rowCount = %d, want 2", r.rowCount())
	}
	if r.columnCount() != 2 {
		t.Errorf("columnCount = %d, want 2", r.columnCount())
	}
}

func testPGExtEmptyResultGuards(t *testing.T) {
	empty := &pgExtResult{columns: []string{}, rows: nil, nilMask: nil}
	if v, ok := empty.cell(0, "any"); ok || v != "" {
		t.Errorf("空结果 cell = (%q, %v), want (\"\", false)", v, ok)
	}
	if empty.cellIsNull(0, "any") {
		t.Error("空结果 cellIsNull 期望 false")
	}
	if empty.findRow("any", "x") != -1 {
		t.Error("空结果 findRow 期望 -1")
	}
}

// newTwoRowResult 构造一个 2 行 2 列、均非 NULL 的 pgExtResult，供单元测试复用。
func newTwoRowResult() *pgExtResult {
	return &pgExtResult{
		columns: []string{"id", "name"},
		rows: [][]string{
			{"1", "alpha"},
			{"2", "beta"},
		},
		nilMask: []bool{false, false, false, false},
	}
}
