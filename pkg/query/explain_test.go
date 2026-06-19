package query

import (
	"strings"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestExplainPlanColumns 验证 EXPLAIN 输出列名固定为 id/depth/operation/detail。
func TestExplainPlanColumns(t *testing.T) {
	cols := ExplainPlanColumns()
	want := []string{"id", "depth", "operation", "detail"}
	if len(cols) != len(want) {
		t.Fatalf("列数 = %d, 期望 %d", len(cols), len(want))
	}
	for i, c := range cols {
		if c != want[i] {
			t.Errorf("列[%d] = %q, 期望 %q", i, c, want[i])
		}
	}
}

// TestExplainPlanScanOnly 验证单节点 Scan 计划的 EXPLAIN 输出。
func TestExplainPlanScanOnly(t *testing.T) {
	scan := &ScanNode{
		Table:   "users",
		Columns: []string{"id", "name"},
		schema: []ColumnDef{
			{Name: "id", Type: common.TypeInt64},
			{Name: "name", Type: common.TypeString},
		},
	}
	rows := ExplainPlan(scan)
	if len(rows) != 1 {
		t.Fatalf("行数 = %d, 期望 1", len(rows))
	}
	r := rows[0]
	if r.ID != 1 {
		t.Errorf("ID = %d, 期望 1", r.ID)
	}
	if r.Depth != 0 {
		t.Errorf("Depth = %d, 期望 0", r.Depth)
	}
	if r.Operation != "Scan" {
		t.Errorf("Operation = %q, 期望 %q", r.Operation, "Scan")
	}
	if !strings.Contains(r.Detail, "users") {
		t.Errorf("Detail %q 应包含表名 users", r.Detail)
	}
}

// TestExplainPlanTree 验证多节点计划树的深度优先前序遍历与 depth 计算。
// 计划树：Limit -> Project -> Scan
func TestExplainPlanTree(t *testing.T) {
	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}
	project := &ProjectNode{
		Child:       scan,
		Expressions: []Expression{&ResolvedColumnExpr{Name: "id", Idx: 0}},
		Aliases:     []string{""},
		schema:      []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}
	limit := &LimitNode{Child: project, Offset: 0, Count: 10}

	rows := ExplainPlan(limit)
	if len(rows) != 3 {
		t.Fatalf("行数 = %d, 期望 3", len(rows))
	}

	wantOps := []string{"Limit", "Project", "Scan"}
	wantDepths := []int{0, 1, 2}
	for i, r := range rows {
		if r.ID != i+1 {
			t.Errorf("行 %d: ID = %d, 期望 %d", i, r.ID, i+1)
		}
		if r.Operation != wantOps[i] {
			t.Errorf("行 %d: Operation = %q, 期望 %q", i, r.Operation, wantOps[i])
		}
		if r.Depth != wantDepths[i] {
			t.Errorf("行 %d: Depth = %d, 期望 %d", i, r.Depth, wantDepths[i])
		}
	}
}

// TestExplainPlanFilterAndAggregate 验证 Filter 与 Aggregate 节点的详情输出。
func TestExplainPlanFilterAndAggregate(t *testing.T) {
	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id", "score"},
		schema: []ColumnDef{
			{Name: "id", Type: common.TypeInt64},
			{Name: "score", Type: common.TypeFloat64},
		},
	}
	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(0)}},
	}
	agg := &AggregateNode{
		Child:      filter,
		GroupBy:    []Expression{&ResolvedColumnExpr{Name: "id"}},
		Aggregates: []AggregateExpr{{Func: AggCount, Arg: &StarExpr{}}},
		schema:     []ColumnDef{{Name: "count", Type: common.TypeInt64}},
	}

	rows := ExplainPlan(agg)
	if len(rows) != 3 {
		t.Fatalf("行数 = %d, 期望 3", len(rows))
	}
	if rows[0].Operation != "Aggregate" {
		t.Errorf("行 0: Operation = %q, 期望 Aggregate", rows[0].Operation)
	}
	if !strings.Contains(rows[0].Detail, "GroupBy") {
		t.Errorf("行 0: Detail %q 应包含 GroupBy", rows[0].Detail)
	}
	if !strings.Contains(rows[0].Detail, "COUNT(*)") {
		t.Errorf("行 0: Detail %q 应包含 COUNT(*)", rows[0].Detail)
	}
	if rows[1].Operation != "Filter" {
		t.Errorf("行 1: Operation = %q, 期望 Filter", rows[1].Operation)
	}
	if !strings.Contains(rows[1].Detail, "Condition") {
		t.Errorf("行 1: Detail %q 应包含 Condition", rows[1].Detail)
	}
	if rows[2].Operation != "Scan" {
		t.Errorf("行 2: Operation = %q, 期望 Scan", rows[2].Operation)
	}
}

// TestExplainPlanNilNode 验证 nil 节点返回空结果。
func TestExplainPlanNilNode(t *testing.T) {
	rows := ExplainPlan(nil)
	if len(rows) != 0 {
		t.Errorf("nil 节点应返回空结果, got %d 行", len(rows))
	}
}

// TestExplainPlanScanWithPredicate 验证带谓词的 Scan 节点详情包含谓词文本。
func TestExplainPlanScanWithPredicate(t *testing.T) {
	scan := &ScanNode{
		Table:     "t",
		Columns:   []string{"id"},
		Predicate: &BinaryExpr{Op: OpEq, Left: &ResolvedColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(0)}},
		schema:    []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}
	rows := ExplainPlan(scan)
	if len(rows) != 1 {
		t.Fatalf("行数 = %d, 期望 1", len(rows))
	}
	if !strings.Contains(rows[0].Detail, "Predicate") {
		t.Errorf("Detail %q 应包含 Predicate", rows[0].Detail)
	}
}

// TestExplainPlanProjectWithAlias 验证带别名的 Project 节点详情包含 "AS" 文本。
func TestExplainPlanProjectWithAlias(t *testing.T) {
	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}
	project := &ProjectNode{
		Child:       scan,
		Expressions: []Expression{&ResolvedColumnExpr{Name: "id", Idx: 0}},
		Aliases:     []string{"uid"},
		schema:      []ColumnDef{{Name: "uid", Type: common.TypeInt64}},
	}
	rows := ExplainPlan(project)
	if len(rows) != 2 {
		t.Fatalf("行数 = %d, 期望 2", len(rows))
	}
	if rows[0].Operation != "Project" {
		t.Fatalf("行 0: Operation = %q, 期望 Project", rows[0].Operation)
	}
	if !strings.Contains(rows[0].Detail, "AS uid") {
		t.Errorf("行 0: Detail %q 应包含 'AS uid'", rows[0].Detail)
	}
}
