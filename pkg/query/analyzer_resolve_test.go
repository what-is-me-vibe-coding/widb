package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestResolveBinaryExpr 测试 resolveBinaryExpr 正常解析二元表达式。
func TestResolveBinaryExpr(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	table, _ := testCatalog().GetTable(testTableUsers)

	// age > 20 AND score < 90.0
	expr := &BinaryExpr{
		Op: OpAnd,
		Left: &BinaryExpr{
			Op:    OpGt,
			Left:  &ColumnExpr{Name: testColAge},
			Right: &LiteralExpr{Value: common.NewInt64(20)},
		},
		Right: &BinaryExpr{
			Op:    OpLt,
			Left:  &ColumnExpr{Name: testColScore},
			Right: &LiteralExpr{Value: common.NewFloat64(90.0)},
		},
	}

	resolved, err := analyzer.resolveBinaryExpr(expr, table)
	if err != nil {
		t.Fatalf("resolveBinaryExpr error: %v", err)
	}
	if resolved.Op != OpAnd {
		t.Errorf("expected OpAnd, got %v", resolved.Op)
	}
	// 验证左右子表达式已解析为 ResolvedColumnExpr
	leftBin, ok := resolved.Left.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected left *BinaryExpr, got %T", resolved.Left)
	}
	if _, ok := leftBin.Left.(*ResolvedColumnExpr); !ok {
		t.Errorf("expected left left *ResolvedColumnExpr, got %T", leftBin.Left)
	}
	rightBin, ok := resolved.Right.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected right *BinaryExpr, got %T", resolved.Right)
	}
	if _, ok := rightBin.Left.(*ResolvedColumnExpr); !ok {
		t.Errorf("expected right left *ResolvedColumnExpr, got %T", rightBin.Left)
	}
}

// TestResolveBinaryExprNoTable 测试 resolveBinaryExprNoTable 在无表上下文中解析二元表达式。
func TestResolveBinaryExprNoTable(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())

	// 1 = 1 OR 2 = 2
	expr := &BinaryExpr{
		Op:    OpOr,
		Left:  &BinaryExpr{Op: OpEq, Left: &LiteralExpr{Value: common.NewInt64(1)}, Right: &LiteralExpr{Value: common.NewInt64(1)}},
		Right: &BinaryExpr{Op: OpEq, Left: &LiteralExpr{Value: common.NewInt64(2)}, Right: &LiteralExpr{Value: common.NewInt64(2)}},
	}

	resolved, err := analyzer.resolveBinaryExprNoTable(expr)
	if err != nil {
		t.Fatalf("resolveBinaryExprNoTable error: %v", err)
	}
	if resolved.Op != OpOr {
		t.Errorf("expected OpOr, got %v", resolved.Op)
	}
}

// TestResolveUnaryExpr 测试 resolveUnaryExpr 正常解析一元表达式。
func TestResolveUnaryExpr(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	table, _ := testCatalog().GetTable(testTableUsers)

	// NOT (age > 20)
	expr := &UnaryExpr{
		Op:   OpNot,
		Expr: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
	}

	resolved, err := analyzer.resolveUnaryExpr(expr, table)
	if err != nil {
		t.Fatalf("resolveUnaryExpr error: %v", err)
	}
	if resolved.Op != OpNot {
		t.Errorf("expected OpNot, got %v", resolved.Op)
	}
	// 验证内部表达式已解析
	inner, ok := resolved.Expr.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected inner *BinaryExpr, got %T", resolved.Expr)
	}
	if _, ok := inner.Left.(*ResolvedColumnExpr); !ok {
		t.Errorf("expected inner left *ResolvedColumnExpr, got %T", inner.Left)
	}
}

// TestBuildScanSchema 测试 buildScanSchema 从表中解析列 schema。
func TestBuildScanSchema(t *testing.T) {
	table, _ := testCatalog().GetTable(testTableUsers)

	// 测试从表中解析存在的列
	colNames := []string{"id", testColName, testColAge, testColScore}
	schema := buildScanSchema(colNames, table)

	if len(schema) != 4 {
		t.Fatalf("expected 4 columns, got %d", len(schema))
	}
	// 验证 id 列
	if schema[0].Name != "id" || schema[0].Type != common.TypeInt64 {
		t.Errorf("expected id INT64, got %v", schema[0])
	}
	// 验证 name 列
	if schema[1].Name != testColName || schema[1].Type != common.TypeString {
		t.Errorf("expected name STRING, got %v", schema[1])
	}
	// 验证 age 列
	if schema[2].Name != testColAge || schema[2].Type != common.TypeInt64 {
		t.Errorf("expected age INT64, got %v", schema[2])
	}
	// 验证 score 列
	if schema[3].Name != testColScore || schema[3].Type != common.TypeFloat64 {
		t.Errorf("expected score FLOAT64, got %v", schema[3])
	}
}

// TestBuildScanSchemaWithUnknownColumn 测试 buildScanSchema 处理不存在的列。
func TestBuildScanSchemaWithUnknownColumn(t *testing.T) {
	table, _ := testCatalog().GetTable(testTableUsers)

	colNames := []string{"id", "nonexistent"}
	schema := buildScanSchema(colNames, table)

	if len(schema) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(schema))
	}
	// 验证存在的列正常解析
	if schema[0].Name != "id" || schema[0].Type != common.TypeInt64 {
		t.Errorf("expected id INT64, got %v", schema[0])
	}
	// 验证不存在的列回退为 TypeNull
	if schema[1].Name != "nonexistent" || schema[1].Type != common.TypeNull {
		t.Errorf("expected nonexistent NULL, got %v", schema[1])
	}
}

// --- analyzer error paths ---

func TestAnalyzerTableNotExist(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("SELECT id FROM nonexistent")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	_, err = analyzer.Analyze(stmt)
	if err == nil {
		t.Fatal("expected error for nonexistent table")
	}
}

func TestAnalyzerColumnNotExist(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("SELECT nonexistent_col FROM users")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	_, err = analyzer.Analyze(stmt)
	if err == nil {
		t.Fatal("expected error for nonexistent column")
	}
}

func TestAnalyzerInsertInvalidColumn(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("INSERT INTO users (id, nonexistent) VALUES (1, 'test')")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	_, err = analyzer.Analyze(stmt)
	if err == nil {
		t.Fatal("expected error for nonexistent column in INSERT")
	}
}

func TestAnalyzerSelectNoFromColumnRef(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())

	stmt := &SelectStatement{
		Columns: []SelectColumn{
			{Expr: &ColumnExpr{Name: "id"}},
		},
	}

	_, err := analyzer.Analyze(stmt)
	if err == nil {
		t.Fatal("expected error for column reference without table context")
	}
}

func TestAnalyzerSelectNoFromUnsupportedExpr(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())

	stmt := &SelectStatement{
		Columns: []SelectColumn{
			{Expr: &unsupportedExpr{}},
		},
	}

	_, err := analyzer.Analyze(stmt)
	if err == nil {
		t.Fatal("expected error for unsupported expression type")
	}
}

func TestAnalyzerUnsupportedStatement(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())

	_, err := analyzer.Analyze(&unsupportedStmt{})
	if err == nil {
		t.Fatal("expected error for unsupported statement type")
	}
}

type unsupportedStmt struct{}

func (s *unsupportedStmt) statementNode() {}
func (s *unsupportedStmt) String() string { return "UNSUPPORTED" }

type unsupportedExpr struct{}

func (e *unsupportedExpr) exprNode()      {}
func (e *unsupportedExpr) String() string { return "UNSUPPORTED" }
