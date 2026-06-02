package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestOptimizerColumnPruning(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()
	optimizer := NewOptimizer()

	stmt, err := parser.Parse("SELECT name FROM users WHERE age > 20")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	optimized := optimizer.Optimize(plan)

	scan := findScanNode(optimized)
	if scan == nil {
		t.Fatal("expected scan node in optimized plan")
	}

	colSet := make(map[string]bool)
	for _, col := range scan.Columns {
		colSet[col] = true
	}

	if !colSet[testColName] {
		t.Errorf("expected 'name' column to be preserved")
	}
	if !colSet[testColAge] {
		t.Errorf("expected 'age' column to be preserved (used in WHERE)")
	}
}

func TestOptimizerColumnPruningNoPruningNeeded(t *testing.T) {
	rule := &ColumnPruningRule{}

	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}

	result := rule.Apply(scan)

	resultScan, ok := result.(*ScanNode)
	if !ok {
		t.Fatalf("expected ScanNode, got %T", result)
	}
	if len(resultScan.Columns) != 1 {
		t.Errorf("expected 1 column, got %d", len(resultScan.Columns))
	}
}

func TestOptimizerColumnPruningWithFilter(t *testing.T) {
	rule := &ColumnPruningRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{"id", testColName, testColAge},
		schema: []ColumnDef{
			{Name: "id", Type: common.TypeInt64},
			{Name: testColName, Type: common.TypeString},
			{Name: testColAge, Type: common.TypeInt64},
		},
	}

	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
	}

	proj := &ProjectNode{
		Child:       filter,
		Expressions: []Expression{&ColumnExpr{Name: testColName}},
		Aliases:     []string{""},
		schema:      []ColumnDef{{Name: testColName, Type: common.TypeString}},
	}

	result := rule.Apply(proj)

	resultProj, ok := result.(*ProjectNode)
	if !ok {
		t.Fatalf("expected ProjectNode, got %T", result)
	}

	resultScan := findScanNode(resultProj)
	if resultScan == nil {
		t.Fatal("expected scan node")
	}

	colSet := make(map[string]bool)
	for _, col := range resultScan.Columns {
		colSet[col] = true
	}
	if !colSet[testColName] {
		t.Error("expected 'name' column to be preserved")
	}
	if !colSet[testColAge] {
		t.Error("expected 'age' column to be preserved (used in filter)")
	}
}

func TestOptimizerColumnPruningWithAggregate(t *testing.T) {
	rule := &ColumnPruningRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{"id", testColName, testColAge, testColScore},
		schema: []ColumnDef{
			{Name: "id", Type: common.TypeInt64},
			{Name: testColName, Type: common.TypeString},
			{Name: testColAge, Type: common.TypeInt64},
			{Name: testColScore, Type: common.TypeFloat64},
		},
	}

	agg := &AggregateNode{
		Child:   scan,
		GroupBy: []Expression{&ColumnExpr{Name: testColAge}},
		Aggregates: []AggregateExpr{
			{Func: AggSum, Arg: &ColumnExpr{Name: testColScore}},
		},
		schema: []ColumnDef{
			{Name: testColAge, Type: common.TypeInt64},
			{Name: "SUM(score)", Type: common.TypeFloat64},
		},
	}

	result := rule.Apply(agg)

	resultAgg, ok := result.(*AggregateNode)
	if !ok {
		t.Fatalf("expected AggregateNode, got %T", result)
	}

	resultScan := findScanNode(resultAgg)
	if resultScan == nil {
		t.Fatal("expected scan node")
	}

	colSet := make(map[string]bool)
	for _, col := range resultScan.Columns {
		colSet[col] = true
	}
	if !colSet[testColAge] {
		t.Error("expected 'age' column to be preserved (group by)")
	}
	if !colSet[testColScore] {
		t.Error("expected 'score' column to be preserved (aggregate arg)")
	}
}

func TestOptimizerColumnPruningWithLimit(t *testing.T) {
	rule := &ColumnPruningRule{}

	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}

	limit := &LimitNode{Child: scan, Count: 10}

	result := rule.Apply(limit)

	resultLimit, ok := result.(*LimitNode)
	if !ok {
		t.Fatalf("expected LimitNode, got %T", result)
	}
	if resultLimit.Count != 10 {
		t.Errorf("expected count 10, got %d", resultLimit.Count)
	}
}
