package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func testCatalog() *catalog.Database {
	db := catalog.NewDatabase()
	db.Tables[testTableUsers] = &catalog.Table{
		Name: testTableUsers,
		Columns: []catalog.ColumnDef{
			{Name: "id", Type: common.TypeInt64, Nullable: false},
			{Name: testColName, Type: common.TypeString, Nullable: true},
			{Name: testColAge, Type: common.TypeInt64, Nullable: true},
			{Name: testColScore, Type: common.TypeFloat64, Nullable: true},
		},
		PrimaryKey: []string{"id"},
	}
	db.Tables["orders"] = &catalog.Table{
		Name: "orders",
		Columns: []catalog.ColumnDef{
			{Name: "order_id", Type: common.TypeInt64, Nullable: false},
			{Name: testColUserID, Type: common.TypeInt64, Nullable: true},
			{Name: "amount", Type: common.TypeFloat64, Nullable: true},
		},
		PrimaryKey: []string{"order_id"},
	}
	return db
}

func TestAnalyzerSelectBasic(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("SELECT id, name FROM users WHERE age > 20")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	scan := findScanNode(plan)
	if scan == nil {
		t.Fatal("expected scan node in plan")
	}
	if scan.Table != testTableUsers {
		t.Errorf("expected scan table 'users', got %q", scan.Table)
	}
}

func TestAnalyzerSelectStar(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("SELECT * FROM users")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	scan := findScanNode(plan)
	if scan == nil {
		t.Fatal("expected scan node in plan")
	}
	if scan.Table != testTableUsers {
		t.Errorf("expected scan table 'users', got %q", scan.Table)
	}
}

func TestAnalyzerSelectWithLimit(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("SELECT id, name FROM users LIMIT 10")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	limit, ok := plan.(*LimitNode)
	if !ok {
		t.Fatalf("expected LimitNode, got %T", plan)
	}
	if limit.Count != 10 {
		t.Errorf("expected limit count 10, got %d", limit.Count)
	}
}

func TestAnalyzerSelectWithGroupBy(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("SELECT age, COUNT(*) FROM users GROUP BY age")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	agg := findAggregateNode(plan)
	if agg == nil {
		t.Fatal("expected AggregateNode in plan")
	}
	if len(agg.GroupBy) != 1 {
		t.Errorf("expected 1 group by column, got %d", len(agg.GroupBy))
	}
	if agg.Aggregates[0].Func != AggCount {
		t.Errorf("expected COUNT aggregate, got %v", agg.Aggregates[0].Func)
	}
}

func TestAnalyzerSelectWithAggregateNoGroupBy(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("SELECT COUNT(*) FROM users")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	agg := findAggregateNode(plan)
	if agg == nil {
		t.Fatal("expected AggregateNode for COUNT without GROUP BY")
	}
}

func TestAnalyzerSelectNoFrom(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("SELECT 1")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	proj, ok := plan.(*ProjectNode)
	if !ok {
		t.Fatalf("expected ProjectNode, got %T", plan)
	}
	if len(proj.Expressions) != 1 {
		t.Errorf("expected 1 expression, got %d", len(proj.Expressions))
	}
}

func TestAnalyzerInsert(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("INSERT INTO users (id, name, age) VALUES (1, 'Alice', 30)")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	scan, ok := plan.(*ScanNode)
	if !ok {
		t.Fatalf("expected ScanNode, got %T", plan)
	}
	if scan.Table != testTableUsers {
		t.Errorf("expected scan table 'users', got %q", scan.Table)
	}
}

func TestAnalyzerCreateTable(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("CREATE TABLE test (id INT64 PRIMARY KEY, name STRING)")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	scan, ok := plan.(*ScanNode)
	if !ok {
		t.Fatalf("expected ScanNode, got %T", plan)
	}
	if scan.Table != "test" {
		t.Errorf("expected scan table 'test', got %q", scan.Table)
	}
}

func TestAnalyzerSelectWithAlias(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("SELECT id AS user_id, name AS user_name FROM users")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	proj := findProjectNode(plan)
	if proj != nil && len(proj.Aliases) < 2 {
		t.Errorf("expected at least 2 aliases, got %d", len(proj.Aliases))
	}
}

func TestAnalyzerSelectWithSumAvg(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("SELECT SUM(score), AVG(score) FROM users")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	agg := findAggregateNode(plan)
	if agg == nil {
		t.Fatal("expected AggregateNode")
	}
	if len(agg.Aggregates) != 2 {
		t.Fatalf("expected 2 aggregates, got %d", len(agg.Aggregates))
	}
	if agg.Aggregates[0].Func != AggSum {
		t.Errorf("expected SUM, got %v", agg.Aggregates[0].Func)
	}
	if agg.Aggregates[1].Func != AggAvg {
		t.Errorf("expected AVG, got %v", agg.Aggregates[1].Func)
	}
}

func TestAnalyzerSelectWithAndOr(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("SELECT id FROM users WHERE age > 20 AND score < 90.0")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	scan := findScanNode(plan)
	if scan == nil {
		t.Fatal("expected scan node")
	}
}

func TestAnalyzerSelectWithNotExpr(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("SELECT id FROM users WHERE NOT (age > 20)")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	scan := findScanNode(plan)
	if scan == nil {
		t.Fatal("expected scan node")
	}
}

func TestAnalyzerSelectNoFromBinaryExpr(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())

	stmt := &SelectStatement{
		Columns: []SelectColumn{
			{
				Expr: &BinaryExpr{
					Op:    OpEq,
					Left:  &LiteralExpr{Value: common.NewInt64(1)},
					Right: &LiteralExpr{Value: common.NewInt64(1)},
				},
			},
		},
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	proj, ok := plan.(*ProjectNode)
	if !ok {
		t.Fatalf("expected ProjectNode, got %T", plan)
	}
	if len(proj.Expressions) != 1 {
		t.Errorf("expected 1 expression, got %d", len(proj.Expressions))
	}
}

func TestAnalyzerSelectNoFromFuncExpr(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())

	stmt := &SelectStatement{
		Columns: []SelectColumn{
			{
				Expr: &FuncExpr{
					Name: "ABS",
					Args: []Expression{&LiteralExpr{Value: common.NewInt64(-5)}},
				},
			},
		},
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	proj, ok := plan.(*ProjectNode)
	if !ok {
		t.Fatalf("expected ProjectNode, got %T", plan)
	}
	if len(proj.Expressions) != 1 {
		t.Errorf("expected 1 expression, got %d", len(proj.Expressions))
	}
}

func TestAnalyzerSelectNoFromUnaryExpr(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())

	stmt := &SelectStatement{
		Columns: []SelectColumn{
			{
				Expr: &UnaryExpr{
					Op:   OpNot,
					Expr: &LiteralExpr{Value: common.NewBool(true)},
				},
			},
		},
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	proj, ok := plan.(*ProjectNode)
	if !ok {
		t.Fatalf("expected ProjectNode, got %T", plan)
	}
	if len(proj.Expressions) != 1 {
		t.Errorf("expected 1 expression, got %d", len(proj.Expressions))
	}
}

func findScanNode(node PlanNode) *ScanNode {
	if node == nil {
		return nil
	}
	switch n := node.(type) {
	case *ScanNode:
		return n
	case *FilterNode:
		return findScanNode(n.Child)
	case *ProjectNode:
		return findScanNode(n.Child)
	case *AggregateNode:
		return findScanNode(n.Child)
	case *LimitNode:
		return findScanNode(n.Child)
	}
	return nil
}

func findAggregateNode(node PlanNode) *AggregateNode {
	if node == nil {
		return nil
	}
	switch n := node.(type) {
	case *AggregateNode:
		return n
	case *FilterNode:
		return findAggregateNode(n.Child)
	case *ProjectNode:
		return findAggregateNode(n.Child)
	case *LimitNode:
		return findAggregateNode(n.Child)
	}
	return nil
}

func findProjectNode(node PlanNode) *ProjectNode {
	if node == nil {
		return nil
	}
	switch n := node.(type) {
	case *ProjectNode:
		return n
	case *FilterNode:
		return findProjectNode(n.Child)
	case *AggregateNode:
		return findProjectNode(n.Child)
	case *LimitNode:
		return findProjectNode(n.Child)
	}
	return nil
}
