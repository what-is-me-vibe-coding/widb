package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestOptimizerConstantFolding(t *testing.T) {
	rule := &ConstantFoldingRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{"id"},
		schema: []ColumnDef{
			{Name: "id", Type: common.TypeInt64, Nullable: false},
		},
		Predicate: &BinaryExpr{
			Op:    OpEq,
			Left:  &LiteralExpr{Value: common.NewInt64(1)},
			Right: &LiteralExpr{Value: common.NewInt64(1)},
		},
	}

	result := rule.Apply(scan)

	resultScan, ok := result.(*ScanNode)
	if !ok {
		t.Fatalf("expected ScanNode, got %T", result)
	}

	lit, ok := resultScan.Predicate.(*LiteralExpr)
	if !ok {
		t.Fatalf("expected LiteralExpr after folding, got %T", resultScan.Predicate)
	}
	if !lit.Value.Valid || lit.Value.Int64 != 1 {
		t.Errorf("expected folded literal true (1), got %v", lit.Value)
	}
}

func TestOptimizerConstantFoldingComparison(t *testing.T) {
	tests := []struct {
		name     string
		op       BinaryOp
		left     common.Value
		right    common.Value
		expected int64
	}{
		{"5 < 10 = true", OpLt, common.NewInt64(5), common.NewInt64(10), 1},
		{"10 < 5 = false", OpLt, common.NewInt64(10), common.NewInt64(5), 0},
		{"5 <= 5 = true", OpLe, common.NewInt64(5), common.NewInt64(5), 1},
		{"5 > 10 = false", OpGt, common.NewInt64(5), common.NewInt64(10), 0},
		{"10 >= 10 = true", OpGe, common.NewInt64(10), common.NewInt64(10), 1},
		{"5 != 10 = true", OpNe, common.NewInt64(5), common.NewInt64(10), 1},
		{"5 = 5 = true", OpEq, common.NewInt64(5), common.NewInt64(5), 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := &ConstantFoldingRule{}
			scan := &ScanNode{
				Table:   "t",
				Columns: []string{"id"},
				schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
				Predicate: &BinaryExpr{
					Op:    tt.op,
					Left:  &LiteralExpr{Value: tt.left},
					Right: &LiteralExpr{Value: tt.right},
				},
			}
			result := rule.Apply(scan).(*ScanNode)
			lit, ok := result.Predicate.(*LiteralExpr)
			if !ok {
				t.Fatalf("expected LiteralExpr, got %T", result.Predicate)
			}
			if lit.Value.Int64 != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, lit.Value.Int64)
			}
		})
	}
}

func TestOptimizerConstantFoldingFilterNode(t *testing.T) {
	rule := &ConstantFoldingRule{}

	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}

	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpEq, Left: &LiteralExpr{Value: common.NewInt64(1)}, Right: &LiteralExpr{Value: common.NewInt64(1)}},
	}

	result := rule.Apply(filter)

	resultFilter, ok := result.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode, got %T", result)
	}

	lit, ok := resultFilter.Condition.(*LiteralExpr)
	if !ok {
		t.Fatalf("expected LiteralExpr after folding, got %T", resultFilter.Condition)
	}
	if !lit.Value.Valid || lit.Value.Int64 != 1 {
		t.Errorf("expected folded literal true, got %v", lit.Value)
	}
}

func TestOptimizerConstantFoldingProjectNode(t *testing.T) {
	rule := &ConstantFoldingRule{}

	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}

	proj := &ProjectNode{
		Child: scan,
		Expressions: []Expression{
			&BinaryExpr{Op: OpAdd, Left: &LiteralExpr{Value: common.NewInt64(1)}, Right: &LiteralExpr{Value: common.NewInt64(2)}},
		},
		Aliases: []string{""},
		schema:  []ColumnDef{{Name: testStrCol1, Type: common.TypeInt64}},
	}

	result := rule.Apply(proj)

	resultProj, ok := result.(*ProjectNode)
	if !ok {
		t.Fatalf("expected ProjectNode, got %T", result)
	}
	if len(resultProj.Expressions) != 1 {
		t.Fatalf("expected 1 expression, got %d", len(resultProj.Expressions))
	}
}

func TestOptimizerConstantFoldingAggregateNode(t *testing.T) {
	rule := &ConstantFoldingRule{}

	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}

	agg := &AggregateNode{
		Child:      scan,
		GroupBy:    []Expression{&ColumnExpr{Name: "id"}},
		Aggregates: []AggregateExpr{{Func: AggCount}},
		schema:     []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}

	result := rule.Apply(agg)

	resultAgg, ok := result.(*AggregateNode)
	if !ok {
		t.Fatalf("expected AggregateNode, got %T", result)
	}
	if len(resultAgg.GroupBy) != 1 {
		t.Errorf("expected 1 group by, got %d", len(resultAgg.GroupBy))
	}
}

func TestOptimizerConstantFoldingLimitNode(t *testing.T) {
	rule := &ConstantFoldingRule{}

	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}

	limit := &LimitNode{
		Child:  scan,
		Count:  10,
		Offset: 0,
	}

	result := rule.Apply(limit)

	resultLimit, ok := result.(*LimitNode)
	if !ok {
		t.Fatalf("expected LimitNode, got %T", result)
	}
	if resultLimit.Count != 10 {
		t.Errorf("expected count 10, got %d", resultLimit.Count)
	}
}

func TestOptimizerConstantFoldingAnd(t *testing.T) {
	rule := &ConstantFoldingRule{}

	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
		Predicate: &BinaryExpr{
			Op:    OpAnd,
			Left:  &LiteralExpr{Value: common.NewBool(true)},
			Right: &LiteralExpr{Value: common.NewBool(false)},
		},
	}

	result := rule.Apply(scan).(*ScanNode)
	lit, ok := result.Predicate.(*LiteralExpr)
	if !ok {
		t.Fatalf("expected LiteralExpr after folding, got %T", result.Predicate)
	}
	if !lit.Value.Valid || lit.Value.Int64 != 0 {
		t.Errorf("expected true AND false = false, got %v", lit.Value)
	}
}

func TestOptimizerConstantFoldingOr(t *testing.T) {
	rule := &ConstantFoldingRule{}

	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
		Predicate: &BinaryExpr{
			Op:    OpOr,
			Left:  &LiteralExpr{Value: common.NewBool(false)},
			Right: &LiteralExpr{Value: common.NewBool(true)},
		},
	}

	result := rule.Apply(scan).(*ScanNode)
	lit, ok := result.Predicate.(*LiteralExpr)
	if !ok {
		t.Fatalf("expected LiteralExpr after folding, got %T", result.Predicate)
	}
	if !lit.Value.Valid || lit.Value.Int64 != 1 {
		t.Errorf("expected false OR true = true, got %v", lit.Value)
	}
}

func TestOptimizerConstantFoldingNot(t *testing.T) {
	rule := &ConstantFoldingRule{}

	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
		Predicate: &UnaryExpr{
			Op:   OpNot,
			Expr: &LiteralExpr{Value: common.NewBool(true)},
		},
	}

	result := rule.Apply(scan).(*ScanNode)
	lit, ok := result.Predicate.(*LiteralExpr)
	if !ok {
		t.Fatalf("expected LiteralExpr after folding NOT, got %T", result.Predicate)
	}
	if lit.Value.Int64 != 0 {
		t.Errorf("expected NOT true = false, got %v", lit.Value)
	}
}

func TestOptimizerRuleNames(t *testing.T) {
	rules := []OptimizeRule{
		&PredicatePushdownRule{},
		&ConstantFoldingRule{},
		&ColumnPruningRule{},
	}

	names := []string{"PredicatePushdown", "ConstantFolding", "ColumnPruning"}
	for i, rule := range rules {
		if rule.Name() != names[i] {
			t.Errorf("expected rule name %q, got %q", names[i], rule.Name())
		}
	}
}

func TestOptimizerConstantFoldingNonLiteralExpr(t *testing.T) {
	rule := &ConstantFoldingRule{}

	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
		Predicate: &BinaryExpr{
			Op:    OpEq,
			Left:  &ColumnExpr{Name: "id"},
			Right: &LiteralExpr{Value: common.NewInt64(1)},
		},
	}

	result := rule.Apply(scan).(*ScanNode)
	bin, ok := result.Predicate.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr (not fully foldable), got %T", result.Predicate)
	}
	if _, ok := bin.Left.(*ColumnExpr); !ok {
		t.Error("expected left side to remain ColumnExpr")
	}
}

func TestOptimizerConstantFoldingNullValues(t *testing.T) {
	rule := &ConstantFoldingRule{}

	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
		Predicate: &BinaryExpr{
			Op:    OpEq,
			Left:  &LiteralExpr{Value: common.NewNull()},
			Right: &LiteralExpr{Value: common.NewInt64(1)},
		},
	}

	result := rule.Apply(scan).(*ScanNode)
	bin, ok := result.Predicate.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr (NULL comparison not folded), got %T", result.Predicate)
	}
	if bin.Op != OpEq {
		t.Error("expected OpEq to remain unchanged for NULL comparison")
	}
}

func TestEndToEndAnalyzeAndOptimize(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()
	optimizer := NewOptimizer()

	tests := []struct {
		name string
		sql  string
	}{
		{"simple select", "SELECT id, name FROM users WHERE age > 20"},
		{"select star", "SELECT * FROM users"},
		{"select with limit", "SELECT id FROM users LIMIT 5"},
		{"select with group by", "SELECT age, COUNT(*) FROM users GROUP BY age"},
		{"select no from", "SELECT 1"},
		{"select with alias", "SELECT id AS user_id FROM users"},
		{"select with and", "SELECT id FROM users WHERE age > 20 AND score < 90.0"},
		{"select sum avg", "SELECT SUM(score), AVG(score) FROM users"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := parser.Parse(tt.sql)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}

			plan, err := analyzer.Analyze(stmt)
			if err != nil {
				t.Fatalf("analyze error: %v", err)
			}

			optimized := optimizer.Optimize(plan)
			if optimized == nil {
				t.Error("optimized plan should not be nil")
			}
		})
	}
}
