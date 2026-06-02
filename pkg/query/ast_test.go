package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestASTSelectStatement(t *testing.T) {
	s := &SelectStatement{
		Columns: []SelectColumn{
			{Expr: &ColumnExpr{Name: "id"}},
			{Expr: &ColumnExpr{Name: testColName}, Alias: "n"},
		},
		From:  &TableRef{Name: testTableUsers},
		Where: &BinaryExpr{Op: OpEq, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(1)}},
		Limit: &LimitClause{Count: 10},
	}
	str := s.String()
	if str == "" {
		t.Error("expected non-empty string")
	}
}

func TestASTInsertStatement(t *testing.T) {
	s := &InsertStatement{
		Table:   testTableUsers,
		Columns: []string{"id", testColName},
		Rows: [][]Expression{
			{&LiteralExpr{Value: common.NewInt64(1)}, &LiteralExpr{Value: common.NewString("alice")}},
		},
	}
	str := s.String()
	if str == "" {
		t.Error("expected non-empty string")
	}
}

func TestASTCreateTableStatement(t *testing.T) {
	s := &CreateTableStatement{
		Table: testTableUsers,
		Columns: []ColumnDef{
			{Name: "id", Type: common.TypeInt64, Nullable: false},
			{Name: testColName, Type: common.TypeString, Nullable: true},
		},
		PrimaryKey:  []string{"id"},
		IfNotExists: true,
	}
	str := s.String()
	if str == "" {
		t.Error("expected non-empty string")
	}
}

func TestASTBinaryOp(t *testing.T) {
	ops := []BinaryOp{OpEq, OpNe, OpLt, OpLe, OpGt, OpGe, OpAnd, OpOr, OpAdd, OpSub, OpMul, OpDiv, OpLike}
	for _, op := range ops {
		if op.String() == "?" {
			t.Errorf("unexpected ? for op %d", op)
		}
	}
}

func TestASTUnaryOp(t *testing.T) {
	if OpNot.String() != "NOT " {
		t.Errorf("expected 'NOT ', got %q", OpNot.String())
	}
	if OpNeg.String() != "-" {
		t.Errorf("expected '-', got %q", OpNeg.String())
	}
}

func TestASTLiteralExpr(t *testing.T) {
	nullExpr := &LiteralExpr{Value: common.NewNull()}
	if nullExpr.String() != "NULL" {
		t.Errorf("expected NULL, got %s", nullExpr.String())
	}
	strExpr := &LiteralExpr{Value: common.NewString("hello")}
	if strExpr.String() != "'hello'" {
		t.Errorf("expected 'hello', got %s", strExpr.String())
	}
	floatExpr := &LiteralExpr{Value: common.NewFloat64(3.14)}
	if floatExpr.String() != "3.14" {
		t.Errorf("expected 3.14, got %s", floatExpr.String())
	}
}

func TestASTFuncExpr(t *testing.T) {
	fn := &FuncExpr{Name: "count", Args: []Expression{&StarExpr{}}}
	str := fn.String()
	if str != "count(*)" {
		t.Errorf("expected 'count(*)', got %s", str)
	}
}

func TestASTUnaryExpr(t *testing.T) {
	expr := &UnaryExpr{Op: OpNot, Expr: &ColumnExpr{Name: "active"}}
	str := expr.String()
	if str != "NOT active" {
		t.Errorf("expected 'NOT active', got %s", str)
	}
}

func TestASTLimitClause(t *testing.T) {
	limit := &LimitClause{Offset: 5, Count: 10}
	str := limit.String()
	if str != "LIMIT 5, 10" {
		t.Errorf("expected 'LIMIT 5, 10', got %s", str)
	}
	limitNoOffset := &LimitClause{Count: 10}
	str = limitNoOffset.String()
	if str != "LIMIT 10" {
		t.Errorf("expected 'LIMIT 10', got %s", str)
	}
}

func TestASTTableRef(t *testing.T) {
	ref := &TableRef{Name: testTableUsers, Alias: "u"}
	str := ref.String()
	if str != "users AS u" {
		t.Errorf("expected 'users AS u', got %s", str)
	}
	refNoAlias := &TableRef{Name: testTableUsers}
	str = refNoAlias.String()
	if str != testTableUsers {
		t.Errorf("expected 'users', got %s", str)
	}
}

func TestASTColumnDef(t *testing.T) {
	cd := ColumnDef{Name: "id", Type: common.TypeInt64, Nullable: false}
	str := cd.String()
	if str == "" {
		t.Error("expected non-empty string")
	}
}

func TestASTSelectColumn(t *testing.T) {
	sc := SelectColumn{Expr: &ColumnExpr{Name: "id"}, Alias: "user_id"}
	str := sc.String()
	if str != "id AS user_id" {
		t.Errorf("expected 'id AS user_id', got %s", str)
	}
	scNoAlias := SelectColumn{Expr: &ColumnExpr{Name: "id"}}
	str = scNoAlias.String()
	if str != "id" {
		t.Errorf("expected 'id', got %s", str)
	}
}

func TestASTStarExpr(t *testing.T) {
	e := &StarExpr{}
	if e.String() != "*" {
		t.Errorf("expected '*', got %s", e.String())
	}
}

func TestASTColumnExpr(t *testing.T) {
	e := &ColumnExpr{Name: "id"}
	if e.String() != "id" {
		t.Errorf("expected 'id', got %s", e.String())
	}
}

func TestASTBinaryExpr(t *testing.T) {
	e := &BinaryExpr{Op: OpEq, Left: &ColumnExpr{Name: "a"}, Right: &ColumnExpr{Name: "b"}}
	str := e.String()
	if str != "(a = b)" {
		t.Errorf("expected '(a = b)', got %s", str)
	}
}

func TestASTSelectStatementFull(t *testing.T) {
	s := &SelectStatement{
		Columns: []SelectColumn{{Expr: &StarExpr{}}},
		From:    &TableRef{Name: "t"},
		Where:   &BinaryExpr{Op: OpAnd, Left: &ColumnExpr{Name: "a"}, Right: &ColumnExpr{Name: "b"}},
		GroupBy: []Expression{&ColumnExpr{Name: "c"}},
		Limit:   &LimitClause{Offset: 5, Count: 10},
	}
	str := s.String()
	if str == "" {
		t.Error("expected non-empty string")
	}
}

func TestASTInsertStatementFull(t *testing.T) {
	s := &InsertStatement{
		Table:   "t",
		Columns: []string{"id"},
		Rows: [][]Expression{
			{&LiteralExpr{Value: common.NewInt64(1)}},
			{&LiteralExpr{Value: common.NewInt64(2)}},
		},
	}
	str := s.String()
	if str == "" {
		t.Error("expected non-empty string")
	}
}

func TestASTCreateTableStatementFull(t *testing.T) {
	s := &CreateTableStatement{
		Table: "t",
		Columns: []ColumnDef{
			{Name: "id", Type: common.TypeInt64, Nullable: false},
			{Name: testColName, Type: common.TypeString, Nullable: true},
		},
		PrimaryKey:  []string{"id"},
		IfNotExists: false,
	}
	str := s.String()
	if str == "" {
		t.Error("expected non-empty string")
	}
}
