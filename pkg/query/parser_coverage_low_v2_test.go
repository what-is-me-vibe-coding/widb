package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// --- convertTableExprs: subquery in FROM clause (unsupported table source type) ---

func TestConvertTableExprs_SubqueryInFrom(t *testing.T) {
	p := NewParser()
	// Subquery in FROM produces a non-TableName source in AliasedTableExpr
	_, err := p.Parse("SELECT * FROM (SELECT id FROM users) AS sub")
	if err == nil {
		t.Error("expected error for subquery in FROM, got nil")
	}
}

// --- convertTableExprs: JOIN in FROM clause (unsupported table expr type) ---

func TestConvertTableExprs_JoinInFrom(t *testing.T) {
	p := NewParser()
	// JOIN produces a JoinTableExpr, not an AliasedTableExpr
	_, err := p.Parse("SELECT id FROM users JOIN orders ON users.id = orders.user_id")
	if err == nil {
		t.Error("expected error for JOIN in FROM, got nil")
	}
}

// --- convertExpr: IS NULL expression (unsupported expr type) ---

func TestConvertExpr_IsNull(t *testing.T) {
	p := NewParser()
	// IS NULL produces an IsNullExpr, not handled by convertExpr
	_, err := p.Parse("SELECT id FROM t WHERE name IS NULL")
	if err == nil {
		t.Error("expected error for IS NULL expression, got nil")
	}
}

// --- convertExpr: IN expression (unsupported comparison operator via convertExpr path) ---

func TestConvertExpr_InOperator(t *testing.T) {
	p := NewParser()
	// IN produces a ComparisonExpr with operator "in", not handled by convertComparisonOp
	_, err := p.Parse("SELECT id FROM t WHERE id IN (1, 2, 3)")
	if err == nil {
		t.Error("expected error for IN expression, got nil")
	}
}

// --- convertFuncExpr: function with column argument (AliasedExpr path) ---

func TestConvertFuncExpr_ColumnArgument(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT MAX(id) FROM t")
	if err != nil {
		t.Fatalf("Parse MAX(id): %v", err)
	}
	sel := stmt.(*SelectStatement)
	fn, ok := sel.Columns[0].Expr.(*FuncExpr)
	if !ok {
		t.Fatalf("expected FuncExpr, got %T", sel.Columns[0].Expr)
	}
	if fn.Name != testFuncMax {
		t.Errorf("expected func name 'max', got %q", fn.Name)
	}
	if len(fn.Args) != 1 {
		t.Fatalf("expected 1 arg, got %d", len(fn.Args))
	}
	col, ok := fn.Args[0].(*ColumnExpr)
	if !ok {
		t.Errorf("expected ColumnExpr arg, got %T", fn.Args[0])
	}
	if col.Name != testColID {
		t.Errorf("expected column 'id', got %q", col.Name)
	}
}

// --- convertFuncExpr: function with multiple column arguments ---

func TestConvertFuncExpr_MultipleColumnArgs(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT COALESCE(id, name) FROM t")
	if err != nil {
		t.Fatalf("Parse COALESCE: %v", err)
	}
	sel := stmt.(*SelectStatement)
	fn, ok := sel.Columns[0].Expr.(*FuncExpr)
	if !ok {
		t.Fatalf("expected FuncExpr, got %T", sel.Columns[0].Expr)
	}
	if fn.Name != "coalesce" { //nolint:goconst
		t.Errorf("expected func name 'coalesce', got %q", fn.Name)
	}
	if len(fn.Args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(fn.Args))
	}
}

// --- convertSelectExprs: function with alias ---

func TestConvertSelectExprs_FuncWithAlias(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT COUNT(*) AS total FROM t")
	if err != nil {
		t.Fatalf("Parse COUNT(*) AS total: %v", err)
	}
	sel := stmt.(*SelectStatement)
	if len(sel.Columns) != 1 {
		t.Fatalf("expected 1 column, got %d", len(sel.Columns))
	}
	if sel.Columns[0].Alias != "total" {
		t.Errorf("expected alias 'total', got %q", sel.Columns[0].Alias)
	}
	fn, ok := sel.Columns[0].Expr.(*FuncExpr)
	if !ok {
		t.Fatalf("expected FuncExpr, got %T", sel.Columns[0].Expr)
	}
	if fn.Name != testFuncCount {
		t.Errorf("expected func name 'count', got %q", fn.Name)
	}
}

// --- convertGroupBy: GROUP BY with function expression ---

func TestConvertGroupBy_FunctionExpression(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT COUNT(*) FROM t GROUP BY id")
	if err != nil {
		t.Fatalf("Parse GROUP BY id: %v", err)
	}
	sel := stmt.(*SelectStatement)
	if len(sel.GroupBy) != 1 {
		t.Fatalf("expected 1 GROUP BY expr, got %d", len(sel.GroupBy))
	}
	col, ok := sel.GroupBy[0].(*ColumnExpr)
	if !ok {
		t.Errorf("expected ColumnExpr in GROUP BY, got %T", sel.GroupBy[0])
	}
	if col.Name != testColID {
		t.Errorf("expected GROUP BY id, got %q", col.Name)
	}
}

// --- convertGroupBy: GROUP BY with unsupported expression (BETWEEN) ---

func TestConvertGroupBy_UnsupportedExpr(t *testing.T) {
	p := NewParser()
	// BETWEEN in GROUP BY triggers unsupported expr type error through convertGroupBy
	_, err := p.Parse("SELECT id FROM t GROUP BY id BETWEEN 1 AND 10")
	if err == nil {
		t.Error("expected error for BETWEEN in GROUP BY, got nil")
	}
}

// --- convertSelect: SELECT FROM dual (exercises dual table check) ---

func TestConvertSelect_FromDual(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT 1 FROM dual")
	if err != nil {
		t.Fatalf("Parse SELECT 1 FROM dual: %v", err)
	}
	sel := stmt.(*SelectStatement)
	// FROM dual should set From to nil
	if sel.From != nil {
		t.Errorf("expected nil From for dual table, got %v", sel.From)
	}
}

// --- convertSelect: error in convertSelectExprs via unsupported expression ---

func TestConvertSelect_SelectExprError(t *testing.T) {
	p := NewParser()
	// Subquery in SELECT list produces an unsupported expression type
	_, err := p.Parse("SELECT (SELECT 1) FROM t")
	if err == nil {
		t.Error("expected error for subquery in SELECT list, got nil")
	}
}

// --- convertInsert: error in convertValues via unsupported expression ---

func TestConvertInsert_ValuesWithUnsupportedExpr(t *testing.T) {
	p := NewParser()
	// CASE expression in VALUES is not handled by convertExpr
	_, err := p.Parse("INSERT INTO t (id) VALUES (CASE WHEN 1=1 THEN 1 END)")
	if err == nil {
		t.Error("expected error for CASE expression in INSERT VALUES, got nil")
	}
}

// --- convertSelect: WHERE with IS NOT NULL (unsupported expr type) ---

func TestConvertSelect_IsNotNull(t *testing.T) {
	p := NewParser()
	// IS NOT NULL produces an IsNullExpr with Negated=true, still not handled
	_, err := p.Parse("SELECT id FROM t WHERE name IS NOT NULL")
	if err == nil {
		t.Error("expected error for IS NOT NULL expression, got nil")
	}
}

// --- convertExpr: ParenExpr wrapping a comparison ---

func TestConvertExpr_ParenExprWithComparison(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM t WHERE (age = 30)")
	if err != nil {
		t.Fatalf("Parse parenthesized comparison: %v", err)
	}
	sel := stmt.(*SelectStatement)
	binExpr, ok := sel.Where.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", sel.Where)
	}
	if binExpr.Op != OpEq {
		t.Errorf("expected OpEq, got %v", binExpr.Op)
	}
}

// --- convertExpr: nested NOT expression ---

func TestConvertExpr_NestedNot(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM t WHERE NOT NOT active = 1")
	if err != nil {
		t.Fatalf("Parse nested NOT: %v", err)
	}
	sel := stmt.(*SelectStatement)
	notExpr, ok := sel.Where.(*UnaryExpr)
	if !ok || notExpr.Op != OpNot {
		t.Fatalf("expected outer NOT, got %v", sel.Where)
	}
	innerNot, ok := notExpr.Expr.(*UnaryExpr)
	if !ok || innerNot.Op != OpNot {
		t.Fatalf("expected inner NOT, got %v", notExpr.Expr)
	}
}

// --- convertSelect: SELECT with WHERE, GROUP BY, and LIMIT combined ---

func TestConvertSelect_WhereGroupByLimit(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT age, COUNT(*) FROM t WHERE age > 10 GROUP BY age LIMIT 5")
	if err != nil {
		t.Fatalf("Parse SELECT with WHERE+GROUP BY+LIMIT: %v", err)
	}
	sel := stmt.(*SelectStatement)
	if sel.Where == nil {
		t.Error("expected WHERE clause")
	}
	if len(sel.GroupBy) != 1 {
		t.Errorf("expected 1 GROUP BY expr, got %d", len(sel.GroupBy))
	}
	if sel.Limit == nil || sel.Limit.Count != 5 {
		t.Errorf("expected LIMIT 5, got %v", sel.Limit)
	}
}

// --- convertInsert: INSERT with multiple rows and mixed types ---

func TestConvertInsert_MultipleRowsMixedTypes(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("INSERT INTO t (id, name) VALUES (1, 'alice'), (2, 'bob')")
	if err != nil {
		t.Fatalf("Parse INSERT with multiple rows: %v", err)
	}
	ins := stmt.(*InsertStatement)
	if len(ins.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(ins.Rows))
	}
	// Verify first row
	lit0, ok := ins.Rows[0][0].(*LiteralExpr)
	if !ok || lit0.Value.Int64 != 1 {
		t.Errorf("row 0 col 0: expected int64 1, got %v", ins.Rows[0][0])
	}
	lit1, ok := ins.Rows[0][1].(*LiteralExpr)
	if !ok || lit1.Value.Str != testNameAlice {
		t.Errorf("row 0 col 1: expected string 'alice', got %v", ins.Rows[0][1])
	}
	// Verify second row
	lit2, ok := ins.Rows[1][0].(*LiteralExpr)
	if !ok || lit2.Value.Int64 != 2 {
		t.Errorf("row 1 col 0: expected int64 2, got %v", ins.Rows[1][0])
	}
}

// --- convertSelectExprs: column with alias ---

func TestConvertSelectExprs_ColumnWithAlias(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id AS user_id, name AS user_name FROM t")
	if err != nil {
		t.Fatalf("Parse SELECT with column aliases: %v", err)
	}
	sel := stmt.(*SelectStatement)
	if len(sel.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(sel.Columns))
	}
	if sel.Columns[0].Alias != testColUserID {
		t.Errorf("col 0 alias = %q, want %q", sel.Columns[0].Alias, testColUserID)
	}
	if sel.Columns[1].Alias != "user_name" {
		t.Errorf("col 1 alias = %q, want 'user_name'", sel.Columns[1].Alias)
	}
}

// --- convertExpr: NOT with comparison expression ---

func TestConvertExpr_NotWithComparison(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM t WHERE NOT age = 30")
	if err != nil {
		t.Fatalf("Parse NOT with comparison: %v", err)
	}
	sel := stmt.(*SelectStatement)
	notExpr, ok := sel.Where.(*UnaryExpr)
	if !ok || notExpr.Op != OpNot {
		t.Fatalf("expected NOT UnaryExpr, got %v", sel.Where)
	}
	inner, ok := notExpr.Expr.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr inside NOT, got %T", notExpr.Expr)
	}
	if inner.Op != OpEq {
		t.Errorf("expected OpEq inside NOT, got %v", inner.Op)
	}
}

// --- convertSelect: SELECT with float literal in WHERE ---

func TestConvertSelect_FloatInWhere(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM t WHERE score > 3.14")
	if err != nil {
		t.Fatalf("Parse float in WHERE: %v", err)
	}
	sel := stmt.(*SelectStatement)
	binExpr, ok := sel.Where.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr, got %T", sel.Where)
	}
	lit, ok := binExpr.Right.(*LiteralExpr)
	if !ok {
		t.Fatalf("expected LiteralExpr, got %T", binExpr.Right)
	}
	if lit.Value.Typ != common.TypeFloat64 {
		t.Errorf("expected float64 type, got %v", lit.Value.Typ)
	}
}

// --- convertSelect: SELECT with string literal in WHERE ---

func TestConvertSelect_StringInWhere(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM t WHERE name = 'bob'")
	if err != nil {
		t.Fatalf("Parse string in WHERE: %v", err)
	}
	sel := stmt.(*SelectStatement)
	binExpr, ok := sel.Where.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr, got %T", sel.Where)
	}
	lit, ok := binExpr.Right.(*LiteralExpr)
	if !ok {
		t.Fatalf("expected LiteralExpr, got %T", binExpr.Right)
	}
	if lit.Value.Typ != common.TypeString {
		t.Errorf("expected string type, got %v", lit.Value.Typ)
	}
	if lit.Value.Str != testNameBob {
		t.Errorf("expected 'bob', got %q", lit.Value.Str)
	}
}
