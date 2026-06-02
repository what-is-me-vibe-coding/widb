package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const (
	testTableUsers = "users"
	testColName    = "name"
	testColAge     = "age"
)

func TestParseSelectBasic(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id, name FROM users")
	if err != nil {
		t.Fatalf("Parse SELECT: %v", err)
	}
	sel, ok := stmt.(*SelectStatement)
	if !ok {
		t.Fatalf("expected *SelectStatement, got %T", stmt)
	}
	if sel.From == nil || sel.From.Name != testTableUsers {
		t.Errorf("expected From=users, got %v", sel.From)
	}
	if len(sel.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(sel.Columns))
	}
	if colExpr(sel.Columns[0].Expr) != "id" {
		t.Errorf("expected column id, got %v", sel.Columns[0].Expr)
	}
	if colExpr(sel.Columns[1].Expr) != testColName {
		t.Errorf("expected column name, got %v", sel.Columns[1].Expr)
	}
}

func TestParseSelectStar(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT * FROM t")
	if err != nil {
		t.Fatalf("Parse SELECT *: %v", err)
	}
	sel := stmt.(*SelectStatement)
	if len(sel.Columns) != 1 {
		t.Fatalf("expected 1 column, got %d", len(sel.Columns))
	}
	if _, ok := sel.Columns[0].Expr.(*StarExpr); !ok {
		t.Errorf("expected StarExpr, got %T", sel.Columns[0].Expr)
	}
}

func TestParseSelectWithAlias(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id AS user_id, name FROM users")
	if err != nil {
		t.Fatalf("Parse SELECT with alias: %v", err)
	}
	sel := stmt.(*SelectStatement)
	if sel.Columns[0].Alias != "user_id" {
		t.Errorf("expected alias user_id, got %q", sel.Columns[0].Alias)
	}
	if sel.Columns[1].Alias != "" {
		t.Errorf("expected no alias, got %q", sel.Columns[1].Alias)
	}
}

func TestParseSelectWithWhere(t *testing.T) {
	tests := []struct {
		name  string
		sql   string
		op    BinaryOp
		left  string
		right int64
	}{
		{"eq", "SELECT id FROM t WHERE age = 30", OpEq, testColAge, 30},
		{"ne", "SELECT id FROM t WHERE age != 30", OpNe, testColAge, 30},
		{"lt", "SELECT id FROM t WHERE age < 30", OpLt, testColAge, 30},
		{"le", "SELECT id FROM t WHERE age <= 30", OpLe, testColAge, 30},
		{"gt", "SELECT id FROM t WHERE age > 30", OpGt, testColAge, 30},
		{"ge", "SELECT id FROM t WHERE age >= 30", OpGe, testColAge, 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewParser()
			stmt, err := p.Parse(tt.sql)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			sel := stmt.(*SelectStatement)
			binExpr, ok := sel.Where.(*BinaryExpr)
			if !ok {
				t.Fatalf("expected *BinaryExpr, got %T", sel.Where)
			}
			if binExpr.Op != tt.op {
				t.Errorf("expected op %v, got %v", tt.op, binExpr.Op)
			}
			col, ok := binExpr.Left.(*ColumnExpr)
			if !ok || col.Name != tt.left {
				t.Errorf("expected left column %s, got %v", tt.left, binExpr.Left)
			}
			lit, ok := binExpr.Right.(*LiteralExpr)
			if !ok || lit.Value.Int64 != tt.right {
				t.Errorf("expected right literal %d, got %v", tt.right, binExpr.Right)
			}
		})
	}
}

func TestParseSelectWithAndOr(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM t WHERE a = 1 AND b = 2")
	if err != nil {
		t.Fatalf("Parse AND: %v", err)
	}
	sel := stmt.(*SelectStatement)
	andExpr, ok := sel.Where.(*BinaryExpr)
	if !ok || andExpr.Op != OpAnd {
		t.Fatalf("expected AND, got %v", sel.Where)
	}
	p = NewParser()
	stmt, err = p.Parse("SELECT id FROM t WHERE a = 1 OR b = 2")
	if err != nil {
		t.Fatalf("Parse OR: %v", err)
	}
	sel = stmt.(*SelectStatement)
	orExpr, ok := sel.Where.(*BinaryExpr)
	if !ok || orExpr.Op != OpOr {
		t.Fatalf("expected OR, got %v", sel.Where)
	}
}

func TestParseSelectWithNot(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM t WHERE NOT active = 1")
	if err != nil {
		t.Fatalf("Parse NOT: %v", err)
	}
	sel := stmt.(*SelectStatement)
	notExpr, ok := sel.Where.(*UnaryExpr)
	if !ok || notExpr.Op != OpNot {
		t.Fatalf("expected NOT, got %T", sel.Where)
	}
}

func TestParseSelectWithLimit(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM t LIMIT 10")
	if err != nil {
		t.Fatalf("Parse LIMIT: %v", err)
	}
	sel := stmt.(*SelectStatement)
	if sel.Limit == nil || sel.Limit.Count != 10 || sel.Limit.Offset != 0 {
		t.Errorf("unexpected LIMIT: %v", sel.Limit)
	}
}

func TestParseSelectWithLimitOffset(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM t LIMIT 5, 10")
	if err != nil {
		t.Fatalf("Parse LIMIT offset: %v", err)
	}
	sel := stmt.(*SelectStatement)
	if sel.Limit.Offset != 5 || sel.Limit.Count != 10 {
		t.Errorf("unexpected LIMIT: %v", sel.Limit)
	}
}

func TestParseSelectWithGroupBy(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT age, COUNT(*) FROM t GROUP BY age")
	if err != nil {
		t.Fatalf("Parse GROUP BY: %v", err)
	}
	sel := stmt.(*SelectStatement)
	if len(sel.GroupBy) != 1 {
		t.Fatalf("expected 1 GROUP BY expr, got %d", len(sel.GroupBy))
	}
	col, ok := sel.GroupBy[0].(*ColumnExpr)
	if !ok || col.Name != testColAge {
		t.Errorf("expected GROUP BY age, got %v", sel.GroupBy[0])
	}
}

func TestParseSelectWithFuncExpr(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT COUNT(*) FROM t")
	if err != nil {
		t.Fatalf("Parse COUNT(*): %v", err)
	}
	sel := stmt.(*SelectStatement)
	fn, ok := sel.Columns[0].Expr.(*FuncExpr)
	if !ok || fn.Name != "count" {
		t.Fatalf("expected count, got %v", sel.Columns[0].Expr)
	}
	if _, ok := fn.Args[0].(*StarExpr); !ok {
		t.Errorf("expected StarExpr arg, got %T", fn.Args[0])
	}
}

func TestParseSelectWithStringLiteral(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT name FROM t WHERE name = 'alice'")
	if err != nil {
		t.Fatalf("Parse string literal: %v", err)
	}
	sel := stmt.(*SelectStatement)
	binExpr, ok := sel.Where.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", sel.Where)
	}
	lit, ok := binExpr.Right.(*LiteralExpr)
	if !ok || lit.Value.Str != "alice" {
		t.Errorf("expected string 'alice', got %v", binExpr.Right)
	}
}

func TestParseSelectWithFloatLiteral(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT price FROM t WHERE price > 9.99")
	if err != nil {
		t.Fatalf("Parse float literal: %v", err)
	}
	sel := stmt.(*SelectStatement)
	binExpr, ok := sel.Where.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", sel.Where)
	}
	lit, ok := binExpr.Right.(*LiteralExpr)
	if !ok || lit.Value.Float64 != 9.99 {
		t.Errorf("expected float 9.99, got %v", binExpr.Right)
	}
}

func TestParseSelectWithNullLiteral(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM t WHERE name = null")
	if err != nil {
		t.Fatalf("Parse null literal: %v", err)
	}
	sel := stmt.(*SelectStatement)
	binExpr, ok := sel.Where.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", sel.Where)
	}
	lit, ok := binExpr.Right.(*LiteralExpr)
	if !ok || !lit.Value.IsNull() {
		t.Errorf("expected NULL, got %v", binExpr.Right)
	}
}

func TestParseInsertBasic(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("INSERT INTO users (id, name) VALUES (1, 'alice')")
	if err != nil {
		t.Fatalf("Parse INSERT: %v", err)
	}
	ins, ok := stmt.(*InsertStatement)
	if !ok {
		t.Fatalf("expected *InsertStatement, got %T", stmt)
	}
	if ins.Table != testTableUsers {
		t.Errorf("expected table users, got %s", ins.Table)
	}
	if len(ins.Columns) != 2 {
		t.Errorf("expected 2 columns, got %d", len(ins.Columns))
	}
	lit0, ok := ins.Rows[0][0].(*LiteralExpr)
	if !ok || lit0.Value.Int64 != 1 {
		t.Errorf("expected first value 1, got %v", ins.Rows[0][0])
	}
	lit1, ok := ins.Rows[0][1].(*LiteralExpr)
	if !ok || lit1.Value.Str != "alice" {
		t.Errorf("expected second value 'alice', got %v", ins.Rows[0][1])
	}
}

func TestParseInsertMultipleRows(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("INSERT INTO t (id) VALUES (1), (2), (3)")
	if err != nil {
		t.Fatalf("Parse INSERT multiple rows: %v", err)
	}
	ins := stmt.(*InsertStatement)
	if len(ins.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(ins.Rows))
	}
	for i, row := range ins.Rows {
		lit, ok := row[0].(*LiteralExpr)
		if !ok || lit.Value.Int64 != int64(i+1) {
			t.Errorf("row %d: expected %d, got %v", i, i+1, row[0])
		}
	}
}

func TestParseCreateTableBasic(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("CREATE TABLE users (id INT64 NOT NULL, name STRING NULL)")
	if err != nil {
		t.Fatalf("Parse CREATE TABLE: %v", err)
	}
	ct, ok := stmt.(*CreateTableStatement)
	if !ok {
		t.Fatalf("expected *CreateTableStatement, got %T", stmt)
	}
	if ct.Table != testTableUsers {
		t.Errorf("expected table users, got %s", ct.Table)
	}
	if len(ct.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(ct.Columns))
	}
	if ct.Columns[0].Type != common.TypeInt64 || ct.Columns[0].Nullable {
		t.Errorf("expected id INT64 NOT NULL, got %v", ct.Columns[0])
	}
	if ct.Columns[1].Type != common.TypeString || !ct.Columns[1].Nullable {
		t.Errorf("expected name STRING NULL, got %v", ct.Columns[1])
	}
}

func TestParseCreateTableWithPrimaryKey(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("CREATE TABLE t (id INT64 NOT NULL PRIMARY KEY, name STRING)")
	if err != nil {
		t.Fatalf("Parse CREATE TABLE with PK: %v", err)
	}
	ct := stmt.(*CreateTableStatement)
	if len(ct.PrimaryKey) != 1 || ct.PrimaryKey[0] != "id" {
		t.Errorf("expected primary key [id], got %v", ct.PrimaryKey)
	}
}

func TestParseCreateTableWithCompositePK(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("CREATE TABLE t (a INT64 NOT NULL, b STRING NOT NULL, PRIMARY KEY (a, b))")
	if err != nil {
		t.Fatalf("Parse CREATE TABLE with composite PK: %v", err)
	}
	ct := stmt.(*CreateTableStatement)
	if len(ct.PrimaryKey) != 2 || ct.PrimaryKey[0] != "a" || ct.PrimaryKey[1] != "b" {
		t.Errorf("expected primary key [a, b], got %v", ct.PrimaryKey)
	}
}

func TestParseCreateTableIfNotExists(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("CREATE TABLE IF NOT EXISTS t (id INT64 NOT NULL)")
	if err != nil {
		t.Fatalf("Parse CREATE TABLE IF NOT EXISTS: %v", err)
	}
	ct := stmt.(*CreateTableStatement)
	if !ct.IfNotExists {
		t.Error("expected IfNotExists=true")
	}
}

func TestParseUnsupportedStatement(t *testing.T) {
	p := NewParser()
	_, err := p.Parse("UPDATE t SET a = 1")
	if err == nil {
		t.Error("expected error for unsupported statement")
	}
}

func TestParseInvalidSQL(t *testing.T) {
	p := NewParser()
	_, err := p.Parse("NOT VALID SQL !!!")
	if err == nil {
		t.Error("expected error for invalid SQL")
	}
}

func TestParseUnsupportedDDL(t *testing.T) {
	p := NewParser()
	_, err := p.Parse("DROP TABLE t")
	if err == nil {
		t.Error("expected error for DROP TABLE")
	}
}

func TestParseSelectWithLike(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT name FROM t WHERE name LIKE '%alice%'")
	if err != nil {
		t.Fatalf("Parse LIKE: %v", err)
	}
	sel := stmt.(*SelectStatement)
	binExpr, ok := sel.Where.(*BinaryExpr)
	if !ok || binExpr.Op != OpLike {
		t.Fatalf("expected LIKE, got %v", sel.Where)
	}
}

func TestParseSelectWithTableAlias(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM users AS u")
	if err != nil {
		t.Fatalf("Parse table alias: %v", err)
	}
	sel := stmt.(*SelectStatement)
	if sel.From.Alias != "u" {
		t.Errorf("expected alias u, got %s", sel.From.Alias)
	}
}

func TestParseSelectNoFrom(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT 1")
	if err != nil {
		t.Fatalf("Parse SELECT 1: %v", err)
	}
	sel := stmt.(*SelectStatement)
	if sel.From != nil {
		t.Errorf("expected nil From, got %v", sel.From)
	}
}

func TestParseSelectWithParenthesizedWhere(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("SELECT id FROM t WHERE (a = 1 OR b = 2) AND c = 3")
	if err != nil {
		t.Fatalf("Parse parenthesized WHERE: %v", err)
	}
	sel := stmt.(*SelectStatement)
	andExpr, ok := sel.Where.(*BinaryExpr)
	if !ok || andExpr.Op != OpAnd {
		t.Fatalf("expected AND, got %v", sel.Where)
	}
}

func TestParseCreateTableColumnTypes(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want common.DataType
	}{
		{"int64", "CREATE TABLE t (c INT64 NOT NULL)", common.TypeInt64},
		{"bigint", "CREATE TABLE t (c BIGINT NOT NULL)", common.TypeInt64},
		{"float64", "CREATE TABLE t (c FLOAT64 NOT NULL)", common.TypeFloat64},
		{"double", "CREATE TABLE t (c DOUBLE NOT NULL)", common.TypeFloat64},
		{"string", "CREATE TABLE t (c STRING NOT NULL)", common.TypeString},
		{"varchar", "CREATE TABLE t (c VARCHAR(255) NOT NULL)", common.TypeString},
		{"bool", "CREATE TABLE t (c BOOL NOT NULL)", common.TypeBool},
		{"boolean", "CREATE TABLE t (c BOOLEAN NOT NULL)", common.TypeBool},
		{"timestamp", "CREATE TABLE t (c TIMESTAMP NOT NULL)", common.TypeTimestamp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewParser()
			stmt, err := p.Parse(tt.sql)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			ct := stmt.(*CreateTableStatement)
			if ct.Columns[0].Type != tt.want {
				t.Errorf("expected %v, got %v", tt.want, ct.Columns[0].Type)
			}
		})
	}
}

func TestParseCreateTableUnsupportedType(t *testing.T) {
	p := NewParser()
	_, err := p.Parse("CREATE TABLE t (c GEOMETRY NOT NULL)")
	if err == nil {
		t.Error("expected error for unsupported column type")
	}
}

func TestParseSelectWithMultipleFrom(t *testing.T) {
	p := NewParser()
	_, err := p.Parse("SELECT id FROM t1, t2")
	if err == nil {
		t.Error("expected error for multiple FROM tables")
	}
}

func colExpr(e Expression) string {
	if ce, ok := e.(*ColumnExpr); ok {
		return ce.Name
	}
	return ""
}
