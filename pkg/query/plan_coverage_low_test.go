package query

import (
	"strings"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestAggregateNodeStringWithGroupBy_V7 测试 AggregateNode.String() 包含 GroupBy 表达式。
func TestAggregateNodeStringWithGroupBy_V7(t *testing.T) {
	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id", benchColScore},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}, {Name: benchColScore, Type: common.TypeInt64}},
	}
	agg := &AggregateNode{
		Child:      scan,
		GroupBy:    []Expression{&ColumnExpr{Name: "id"}},
		Aggregates: []AggregateExpr{{Func: AggSum, Arg: &ColumnExpr{Name: benchColScore}}},
		schema:     []ColumnDef{{Name: "id", Type: common.TypeInt64}, {Name: "SUM(score)", Type: common.TypeInt64}},
	}

	s := agg.String()
	if !strings.Contains(s, "GroupBy:") {
		t.Errorf("期望包含 'GroupBy:'，实际 %q", s)
	}
	if !strings.Contains(s, "id") {
		t.Errorf("期望包含 'id'，实际 %q", s)
	}
	if !strings.Contains(s, "SUM(score)") {
		t.Errorf("期望包含 'SUM(score)'，实际 %q", s)
	}
}

// TestAggregateNodeStringNoGroupBy_V7 测试 AggregateNode.String() 无 GroupBy 时不包含 GroupBy。
func TestAggregateNodeStringNoGroupBy_V7(t *testing.T) {
	scan := &ScanNode{
		Table:   "t",
		Columns: []string{benchColScore},
		schema:  []ColumnDef{{Name: benchColScore, Type: common.TypeInt64}},
	}
	agg := &AggregateNode{
		Child:      scan,
		GroupBy:    nil,
		Aggregates: []AggregateExpr{{Func: AggCount, Arg: nil}},
		schema:     []ColumnDef{{Name: "COUNT(*)", Type: common.TypeInt64}},
	}

	s := agg.String()
	if strings.Contains(s, "GroupBy:") {
		t.Errorf("不期望包含 'GroupBy:'，实际 %q", s)
	}
	if !strings.Contains(s, "COUNT(*)") {
		t.Errorf("期望包含 'COUNT(*)'，实际 %q", s)
	}
}

// TestAggregateNodeStringMultipleGroupBy_V7 测试多个 GroupBy 表达式。
func TestAggregateNodeStringMultipleGroupBy_V7(t *testing.T) {
	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"a", "b"},
		schema:  []ColumnDef{{Name: "a", Type: common.TypeInt64}, {Name: "b", Type: common.TypeString}},
	}
	agg := &AggregateNode{
		Child:      scan,
		GroupBy:    []Expression{&ColumnExpr{Name: "a"}, &ColumnExpr{Name: "b"}},
		Aggregates: []AggregateExpr{{Func: AggCount, Arg: nil}},
		schema:     []ColumnDef{{Name: "a", Type: common.TypeInt64}},
	}

	s := agg.String()
	if !strings.Contains(s, "a") || !strings.Contains(s, "b") {
		t.Errorf("期望包含 'a' 和 'b'，实际 %q", s)
	}
}

// TestLimitNodeString_V7 测试 LimitNode.String() 的输出格式。
func TestLimitNodeString_V7(t *testing.T) {
	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}
	limit := &LimitNode{Child: scan, Offset: 5, Count: 10}

	s := limit.String()
	if !strings.Contains(s, "Offset: 5") {
		t.Errorf("期望包含 'Offset: 5'，实际 %q", s)
	}
	if !strings.Contains(s, "Count: 10") {
		t.Errorf("期望包含 'Count: 10'，实际 %q", s)
	}
}

// TestFilterNodeString_V7 测试 FilterNode.String() 的输出格式。
func TestFilterNodeString_V7(t *testing.T) {
	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}
	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(0)}},
	}

	s := filter.String()
	if !strings.Contains(s, "Filter(") {
		t.Errorf("期望包含 'Filter('，实际 %q", s)
	}
	if !strings.Contains(s, "Condition:") {
		t.Errorf("期望包含 'Condition:'，实际 %q", s)
	}
}

// TestProjectNodeStringWithAlias_V7 测试 ProjectNode.String() 带别名的表达式。
func TestProjectNodeStringWithAlias_V7(t *testing.T) {
	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}
	proj := &ProjectNode{
		Child:       scan,
		Expressions: []Expression{&ColumnExpr{Name: "id"}},
		Aliases:     []string{"user_id"},
		schema:      []ColumnDef{{Name: "user_id", Type: common.TypeInt64}},
	}

	s := proj.String()
	if !strings.Contains(s, "AS user_id") {
		t.Errorf("期望包含 'AS user_id'，实际 %q", s)
	}
}

// TestProjectNodeStringNoAlias_V7 测试 ProjectNode.String() 无别名的表达式。
func TestProjectNodeStringNoAlias_V7(t *testing.T) {
	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}
	proj := &ProjectNode{
		Child:       scan,
		Expressions: []Expression{&ColumnExpr{Name: "id"}},
		Aliases:     []string{""},
		schema:      []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}

	s := proj.String()
	if strings.Contains(s, "AS ") {
		t.Errorf("不期望包含 'AS '，实际 %q", s)
	}
}

// TestScanNodeStringWithPredicate_V7 测试 ScanNode.String() 带谓词。
func TestScanNodeStringWithPredicate_V7(t *testing.T) {
	scan := &ScanNode{
		Table:     "users",
		Columns:   []string{"id", benchColName},
		Predicate: &BinaryExpr{Op: OpEq, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(1)}},
		schema:    []ColumnDef{{Name: "id", Type: common.TypeInt64}, {Name: benchColName, Type: common.TypeString}},
	}

	s := scan.String()
	if !strings.Contains(s, "Predicate:") {
		t.Errorf("期望包含 'Predicate:'，实际 %q", s)
	}
}

// TestScanNodeStringNoPredicate_V7 测试 ScanNode.String() 无谓词。
func TestScanNodeStringNoPredicate_V7(t *testing.T) {
	scan := &ScanNode{
		Table:   "users",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}

	s := scan.String()
	if strings.Contains(s, "Predicate:") {
		t.Errorf("不期望包含 'Predicate:'，实际 %q", s)
	}
}

// TestAggregateFuncUnknownString_V7 测试未知聚合函数的 String() 返回 "UNKNOWN"。
func TestAggregateFuncUnknownString_V7(t *testing.T) {
	f := AggregateFunc(99)
	if f.String() != aggNameUnknown {
		t.Errorf("期望 %q，实际 %q", aggNameUnknown, f.String())
	}
}

// TestAggregateExprStringWithArg_V7 测试 AggregateExpr.String() 带参数。
func TestAggregateExprStringWithArg_V7(t *testing.T) {
	agg := AggregateExpr{Func: AggMax, Arg: &ColumnExpr{Name: "price"}}
	s := agg.String()
	if s != "MAX(price)" {
		t.Errorf("期望 'MAX(price)'，实际 %q", s)
	}
}
