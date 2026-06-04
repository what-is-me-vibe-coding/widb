package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestParseInsertWithFloatAndNull(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("INSERT INTO t (id, price) VALUES (1, 9.99)")
	if err != nil {
		t.Fatalf("Parse INSERT with float: %v", err)
	}
	ins := stmt.(*InsertStatement)
	lit, ok := ins.Rows[0][1].(*LiteralExpr)
	if !ok || lit.Value.Typ != common.TypeFloat64 {
		t.Errorf("expected float64 value, got %v", ins.Rows[0][1])
	}
}

func TestParseInsertWithNullValue(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("INSERT INTO t (id, name) VALUES (1, null)")
	if err != nil {
		t.Fatalf("Parse INSERT null: %v", err)
	}
	ins := stmt.(*InsertStatement)
	lit, ok := ins.Rows[0][1].(*LiteralExpr)
	if !ok || !lit.Value.IsNull() {
		t.Errorf("expected NULL, got %v", ins.Rows[0][1])
	}
}

func TestParseInsertWithoutColumns(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("INSERT INTO t VALUES (1, 'alice')")
	if err != nil {
		t.Fatalf("Parse INSERT without columns: %v", err)
	}
	ins := stmt.(*InsertStatement)
	if len(ins.Columns) != 0 {
		t.Errorf("expected 0 columns, got %d", len(ins.Columns))
	}
}

func TestParseSelectWithFuncArgs(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT MAX(score), MIN(score), AVG(score) FROM t")
	if err != nil {
		t.Fatalf("Parse func args: %v", err)
	}
	sel := stmt.(*SelectStatement)
	for i, name := range []string{testFuncMax, testFuncMin, testFuncAvg} {
		fn, ok := sel.Columns[i].Expr.(*FuncExpr)
		if !ok || fn.Name != name {
			t.Errorf("col %d: expected %s, got %v", i, name, sel.Columns[i].Expr)
		}
	}
}

func TestParseSelectWithMultipleFuncArgs(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT COALESCE(a, 0) FROM t")
	if err != nil {
		t.Fatalf("Parse COALESCE: %v", err)
	}
	sel := stmt.(*SelectStatement)
	fn, ok := sel.Columns[0].Expr.(*FuncExpr)
	if !ok || fn.Name != "coalesce" || len(fn.Args) != 2 {
		t.Errorf("expected coalesce with 2 args, got %v", sel.Columns[0].Expr)
	}
}

func TestParseSelectWithGroupByMultiple(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT a, b, COUNT(*) FROM t GROUP BY a, b")
	if err != nil {
		t.Fatalf("Parse GROUP BY multiple: %v", err)
	}
	sel := stmt.(*SelectStatement)
	if len(sel.GroupBy) != 2 {
		t.Fatalf("expected 2 GROUP BY exprs, got %d", len(sel.GroupBy))
	}
}

func TestParseSelectWithIntLiteral(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT 42")
	if err != nil {
		t.Fatalf("Parse int literal: %v", err)
	}
	sel := stmt.(*SelectStatement)
	lit, ok := sel.Columns[0].Expr.(*LiteralExpr)
	if !ok || lit.Value.Int64 != 42 {
		t.Errorf("expected 42, got %v", sel.Columns[0].Expr)
	}
}

func TestParseSelectWithFloatLiteralNoFrom(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT 3.14")
	if err != nil {
		t.Fatalf("Parse float literal: %v", err)
	}
	sel := stmt.(*SelectStatement)
	lit, ok := sel.Columns[0].Expr.(*LiteralExpr)
	if !ok || lit.Value.Float64 != 3.14 {
		t.Errorf("expected 3.14, got %v", sel.Columns[0].Expr)
	}
}

func TestParseSelectWithStringLiteralColumn(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT 'hello' FROM t")
	if err != nil {
		t.Fatalf("Parse string column: %v", err)
	}
	sel := stmt.(*SelectStatement)
	lit, ok := sel.Columns[0].Expr.(*LiteralExpr)
	if !ok || lit.Value.Str != "hello" {
		t.Errorf("expected 'hello', got %v", sel.Columns[0].Expr)
	}
}

func TestParseSelectWithNullColumn(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT null FROM t")
	if err != nil {
		t.Fatalf("Parse null column: %v", err)
	}
	sel := stmt.(*SelectStatement)
	lit, ok := sel.Columns[0].Expr.(*LiteralExpr)
	if !ok || !lit.Value.IsNull() {
		t.Errorf("expected NULL, got %v", sel.Columns[0].Expr)
	}
}

func TestParseSelectComplexWhere(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM t WHERE a = 1 AND b = 2 AND c = 3")
	if err != nil {
		t.Fatalf("Parse complex WHERE: %v", err)
	}
	sel := stmt.(*SelectStatement)
	if sel.Where == nil {
		t.Fatal("expected WHERE")
	}
}

func TestParseSelectWithNegativeValue(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM t WHERE age > -1")
	if err != nil {
		t.Fatalf("Parse negative: %v", err)
	}
	sel := stmt.(*SelectStatement)
	if sel.Where == nil {
		t.Fatal("expected WHERE")
	}
}

func TestPreprocessSQL(t *testing.T) {
	p := NewParser()
	tests := []struct {
		input string
		want  string
	}{
		{"CREATE TABLE t (c INT64 NOT NULL)", "CREATE TABLE t (c BIGINT NOT NULL)"},
		{"CREATE TABLE t (c FLOAT64 NOT NULL)", "CREATE TABLE t (c DOUBLE NOT NULL)"},
		{"CREATE TABLE t (c STRING NOT NULL)", "CREATE TABLE t (c TEXT NOT NULL)"},
		{"CREATE TABLE t (c BOOL NOT NULL)", "CREATE TABLE t (c TINYINT NOT NULL)"},
		{"SELECT * FROM t", "SELECT * FROM t"},
	}
	for _, tt := range tests {
		got := p.preprocessSQL(tt.input)
		if got != tt.want {
			t.Errorf("preprocessSQL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestParseSelectWithORExpr 测试 OR 表达式解析（覆盖 convertBinaryExpr）。
func TestParseSelectWithORExpr(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM t WHERE a = 1 OR b = 2")
	if err != nil {
		t.Fatalf("Parse OR: %v", err)
	}
	sel := stmt.(*SelectStatement)
	orExpr, ok := sel.Where.(*BinaryExpr)
	if !ok || orExpr.Op != OpOr {
		t.Fatalf("expected OR BinaryExpr, got %v", sel.Where)
	}
}

// TestParseSelectWithANDExpr 测试 AND 表达式解析（覆盖 convertBinaryExpr）。
func TestParseSelectWithANDExpr(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM t WHERE a = 1 AND b = 2")
	if err != nil {
		t.Fatalf("Parse AND: %v", err)
	}
	sel := stmt.(*SelectStatement)
	andExpr, ok := sel.Where.(*BinaryExpr)
	if !ok || andExpr.Op != OpAnd {
		t.Fatalf("expected AND BinaryExpr, got %v", sel.Where)
	}
}

// TestParseSelectWithFloatVal 测试浮点数字面量解析（覆盖 convertSQLVal FloatVal 分支）。
func TestParseSelectWithFloatVal(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT 3.14")
	if err != nil {
		t.Fatalf("Parse float val: %v", err)
	}
	sel := stmt.(*SelectStatement)
	lit, ok := sel.Columns[0].Expr.(*LiteralExpr)
	if !ok || lit.Value.Typ != common.TypeFloat64 || lit.Value.Float64 != 3.14 {
		t.Errorf("expected float64 3.14, got %v", sel.Columns[0].Expr)
	}
}

// TestParseSelectWithStrVal 测试字符串字面量解析（覆盖 convertSQLVal StrVal 分支）。
func TestParseSelectWithStrVal(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT 'hello'")
	if err != nil {
		t.Fatalf("Parse string val: %v", err)
	}
	sel := stmt.(*SelectStatement)
	lit, ok := sel.Columns[0].Expr.(*LiteralExpr)
	if !ok || lit.Value.Typ != common.TypeString || lit.Value.Str != "hello" {
		t.Errorf("expected string 'hello', got %v", sel.Columns[0].Expr)
	}
}

// TestParseSelectWithNeComparison 测试 != 比较运算符解析（覆盖 convertComparisonExpr）。
func TestParseSelectWithNeComparison(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM t WHERE age != 30")
	if err != nil {
		t.Fatalf("Parse != comparison: %v", err)
	}
	sel := stmt.(*SelectStatement)
	binExpr, ok := sel.Where.(*BinaryExpr)
	if !ok || binExpr.Op != OpNe {
		t.Fatalf("expected OpNe, got %v", sel.Where)
	}
}

// TestParseSelectWithLeComparison 测试 <= 比较运算符解析（覆盖 convertComparisonExpr）。
func TestParseSelectWithLeComparison(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM t WHERE age <= 30")
	if err != nil {
		t.Fatalf("Parse <= comparison: %v", err)
	}
	sel := stmt.(*SelectStatement)
	binExpr, ok := sel.Where.(*BinaryExpr)
	if !ok || binExpr.Op != OpLe {
		t.Fatalf("expected OpLe, got %v", sel.Where)
	}
}

// TestParseSelectWithGeComparison 测试 >= 比较运算符解析（覆盖 convertComparisonExpr）。
func TestParseSelectWithGeComparison(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM t WHERE age >= 30")
	if err != nil {
		t.Fatalf("Parse >= comparison: %v", err)
	}
	sel := stmt.(*SelectStatement)
	binExpr, ok := sel.Where.(*BinaryExpr)
	if !ok || binExpr.Op != OpGe {
		t.Fatalf("expected OpGe, got %v", sel.Where)
	}
}

// TestParseLimitWithOffset 测试 LIMIT 带偏移量解析（覆盖 parseUint64 的 offset 分支）。
func TestParseLimitWithOffset(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM t LIMIT 5, 10")
	if err != nil {
		t.Fatalf("Parse LIMIT with offset: %v", err)
	}
	sel := stmt.(*SelectStatement)
	if sel.Limit == nil {
		t.Fatal("expected LIMIT clause")
	}
	if sel.Limit.Offset != 5 {
		t.Errorf("expected offset 5, got %d", sel.Limit.Offset)
	}
	if sel.Limit.Count != 10 {
		t.Errorf("expected count 10, got %d", sel.Limit.Count)
	}
}
