package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// --- 7. LiteralExpr.String() ---

func TestLiteralExprString(t *testing.T) {
	tests := []struct {
		name string
		expr *LiteralExpr
		want string
	}{
		{"null", &LiteralExpr{Value: common.NewNull()}, "NULL"},
		{"string", &LiteralExpr{Value: common.NewString("hi")}, "'hi'"},
		{testTypeFloat64, &LiteralExpr{Value: common.NewFloat64(2.5)}, "2.5"},
		{"int64_uses_default_branch", &LiteralExpr{Value: common.NewInt64(42)}, "42"},
		{"bool_uses_default_branch", &LiteralExpr{Value: common.NewBool(true)}, "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.expr.String(); got != tt.want {
				t.Errorf("LiteralExpr.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- 8. SelectStatement.String() ---

func TestSelectStatementString(t *testing.T) {
	tests := []struct {
		name string
		stmt *SelectStatement
		want string
	}{
		{
			name: "columns_only",
			stmt: &SelectStatement{
				Columns: []SelectColumn{{Expr: &ColumnExpr{Name: "id"}}},
			},
			want: "SELECT id",
		},
		{
			name: "with_from",
			stmt: &SelectStatement{
				Columns: []SelectColumn{{Expr: &ColumnExpr{Name: "id"}}},
				From:    &TableRef{Name: testTableUsers},
			},
			want: "SELECT id FROM users",
		},
		{
			name: "with_from_and_where",
			stmt: &SelectStatement{
				Columns: []SelectColumn{{Expr: &ColumnExpr{Name: "id"}}},
				From:    &TableRef{Name: testTableUsers},
				Where:   &BinaryExpr{Op: OpEq, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(1)}},
			},
			want: "SELECT id FROM users WHERE (id = 1)",
		},
		{
			name: "with_group_by",
			stmt: &SelectStatement{
				Columns: []SelectColumn{{Expr: &ColumnExpr{Name: testColAge}}},
				From:    &TableRef{Name: testTableUsers},
				GroupBy: []Expression{&ColumnExpr{Name: testColAge}},
			},
			want: "SELECT age FROM users GROUP BY age",
		},
		{
			name: "with_limit",
			stmt: &SelectStatement{
				Columns: []SelectColumn{{Expr: &StarExpr{}}},
				From:    &TableRef{Name: testTableUsers},
				Limit:   &LimitClause{Count: 10},
			},
			want: "SELECT * FROM users LIMIT 10",
		},
		{
			name: "full_select",
			stmt: &SelectStatement{
				Columns: []SelectColumn{{Expr: &ColumnExpr{Name: testColAge}}, {Expr: &FuncExpr{Name: testFuncCount, Args: []Expression{&StarExpr{}}}}},
				From:    &TableRef{Name: testTableUsers},
				Where:   &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
				GroupBy: []Expression{&ColumnExpr{Name: testColAge}},
				Limit:   &LimitClause{Offset: 5, Count: 10},
			},
			want: "SELECT age, count(*) FROM users WHERE (age > 20) GROUP BY age LIMIT 5, 10",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.stmt.String(); got != tt.want {
				t.Errorf("SelectStatement.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- 9. CreateTableStatement.String() ---

func TestCreateTableStatementString(t *testing.T) {
	tests := []struct {
		name string
		stmt *CreateTableStatement
		want string
	}{
		{
			name: "basic_no_if_not_exists_no_pk",
			stmt: &CreateTableStatement{
				Table:   "t",
				Columns: []ColumnDef{{Name: "id", Type: common.TypeInt64, Nullable: false}},
			},
			want: "CREATE TABLE t (id INT64 NOT NULL)",
		},
		{
			name: "with_if_not_exists",
			stmt: &CreateTableStatement{
				Table:       "t",
				IfNotExists: true,
				Columns:     []ColumnDef{{Name: "id", Type: common.TypeInt64, Nullable: false}},
			},
			want: "CREATE TABLE IF NOT EXISTS t (id INT64 NOT NULL)",
		},
		{
			name: "with_primary_key",
			stmt: &CreateTableStatement{
				Table:      "t",
				Columns:    []ColumnDef{{Name: "id", Type: common.TypeInt64, Nullable: false}},
				PrimaryKey: []string{"id"},
			},
			want: "CREATE TABLE t (id INT64 NOT NULL, PRIMARY KEY (id))",
		},
		{
			name: "with_if_not_exists_and_primary_key",
			stmt: &CreateTableStatement{
				Table:       testTableUsers,
				IfNotExists: true,
				Columns: []ColumnDef{
					{Name: "id", Type: common.TypeInt64, Nullable: false},
					{Name: testColName, Type: common.TypeString, Nullable: true},
				},
				PrimaryKey: []string{"id"},
			},
			want: "CREATE TABLE IF NOT EXISTS users (id INT64 NOT NULL, name STRING NULL, PRIMARY KEY (id))",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.stmt.String(); got != tt.want {
				t.Errorf("CreateTableStatement.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- 10. LimitClause.String() ---

func TestLimitClauseString(t *testing.T) {
	tests := []struct {
		name  string
		limit *LimitClause
		want  string
	}{
		{"no_offset", &LimitClause{Count: 10}, "LIMIT 10"},
		{"with_offset", &LimitClause{Offset: 5, Count: 10}, "LIMIT 5, 10"},
		{"zero_offset", &LimitClause{Offset: 0, Count: 20}, "LIMIT 20"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.limit.String(); got != tt.want {
				t.Errorf("LimitClause.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- 11. UnaryOp.String() ---

func TestUnaryOpString(t *testing.T) {
	tests := []struct {
		name string
		op   UnaryOp
		want string
	}{
		{"not", OpNot, strNot},
		{"neg", OpNeg, "-"},
		{"unknown_default", UnaryOp(99), "?"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.op.String(); got != tt.want {
				t.Errorf("UnaryOp(%d).String() = %q, want %q", tt.op, got, tt.want)
			}
		})
	}
}
