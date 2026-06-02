package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestOptimizerPredicatePushdown(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()
	optimizer := NewOptimizer()

	stmt, err := parser.Parse("SELECT id, name FROM users WHERE age > 20")
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
	if scan.Predicate == nil {
		t.Error("expected predicate to be pushed down to scan node")
	}
}

func TestOptimizerPredicatePushdownEliminatesFilter(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{"id", testColName},
		schema: []ColumnDef{
			{Name: "id", Type: common.TypeInt64, Nullable: false},
			{Name: testColName, Type: common.TypeString, Nullable: true},
		},
	}

	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(0)}},
	}

	result := rule.Apply(filter)

	resultScan, ok := result.(*ScanNode)
	if !ok {
		t.Fatalf("expected FilterNode to be eliminated and ScanNode returned, got %T", result)
	}
	if resultScan.Predicate == nil {
		t.Error("expected predicate to be pushed into scan")
	}
}

func TestOptimizerMergeFilters(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{"id", testColAge},
		schema: []ColumnDef{
			{Name: "id", Type: common.TypeInt64},
			{Name: testColAge, Type: common.TypeInt64},
		},
	}

	innerFilter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
	}

	outerFilter := &FilterNode{
		Child:     innerFilter,
		Condition: &BinaryExpr{Op: OpLt, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(100)}},
	}

	result := rule.Apply(outerFilter)

	resultScan, ok := result.(*ScanNode)
	if !ok {
		t.Fatalf("expected merged filters into ScanNode, got %T", result)
	}
	if resultScan.Predicate == nil {
		t.Error("expected merged predicate in scan")
	}
}

func TestOptimizerPushdownThroughProject(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{"id", testColName, testColAge},
		schema: []ColumnDef{
			{Name: "id", Type: common.TypeInt64},
			{Name: testColName, Type: common.TypeString},
			{Name: testColAge, Type: common.TypeInt64},
		},
	}

	proj := &ProjectNode{
		Child:       scan,
		Expressions: []Expression{&ColumnExpr{Name: "id"}, &ColumnExpr{Name: testColName}},
		Aliases:     []string{"", ""},
		schema: []ColumnDef{
			{Name: "id", Type: common.TypeInt64},
			{Name: testColName, Type: common.TypeString},
		},
	}

	filter := &FilterNode{
		Child:     proj,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(0)}},
	}

	result := rule.Apply(filter)

	resultProj, ok := result.(*ProjectNode)
	if !ok {
		t.Fatalf("expected ProjectNode, got %T", result)
	}

	innerFilter, ok := resultProj.Child.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode under Project, got %T", resultProj.Child)
	}
	if innerFilter.Condition == nil {
		t.Error("expected pushed-down filter condition")
	}
}

func TestOptimizerPushdownThroughAggregate(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{"id", testColAge},
		schema: []ColumnDef{
			{Name: "id", Type: common.TypeInt64},
			{Name: testColAge, Type: common.TypeInt64},
		},
	}

	agg := &AggregateNode{
		Child:   scan,
		GroupBy: []Expression{&ColumnExpr{Name: testColAge}},
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
		},
		schema: []ColumnDef{
			{Name: testColAge, Type: common.TypeInt64},
			{Name: testAggCountStar, Type: common.TypeInt64},
		},
	}

	filter := &FilterNode{
		Child:     agg,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testAggCountStar}, Right: &LiteralExpr{Value: common.NewInt64(5)}},
	}

	result := rule.Apply(filter)

	resultFilter, ok := result.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode (remaining after aggregate), got %T", result)
	}
	if resultFilter.Condition == nil {
		t.Error("expected remaining filter condition above aggregate")
	}
}
