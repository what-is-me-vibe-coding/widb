package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// --- 1. maxStr / minStr ---

func TestMaxStr(t *testing.T) {
	tests := []struct {
		name, a, b, want string
	}{
		{"a_greater", "z", "a", "z"},
		{"b_greater", "a", "z", "z"},
		{"equal", "m", "m", "m"},
		{"empty_a", "", "b", "b"},
		{"empty_b", "a", "", "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maxStr(tt.a, tt.b); got != tt.want {
				t.Errorf("maxStr(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestMinStr(t *testing.T) {
	tests := []struct {
		name, a, b, want string
	}{
		{"a_smaller", "a", "z", "a"},
		{"b_smaller", "z", "a", "a"},
		{"equal", "m", "m", "m"},
		{"empty_a", "", "b", ""},
		{"empty_b", "a", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := minStr(tt.a, tt.b); got != tt.want {
				t.Errorf("minStr(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// --- 2. toFloat64 ---

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name string
		v    common.Value
		want float64
	}{
		{testTypeFloat64, common.NewFloat64(3.14), 3.14},
		{"int64", common.NewInt64(42), 42.0},
		{"null_returns_zero", common.NewNull(), 0},
		{"string_returns_zero", common.NewString("x"), 0},
		{"bool_returns_zero", common.NewBool(true), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toFloat64(tt.v); got != tt.want {
				t.Errorf("toFloat64(%v) = %v, want %v", tt.v, got, tt.want)
			}
		})
	}
}

// --- 3. buildScanSchema ---

func TestBuildScanSchema(t *testing.T) {
	cat := testCatalog()
	tbl, err := cat.GetTable(testTableUsers)
	if err != nil {
		t.Fatalf("failed to get users table: %v", err)
	}

	tests := []struct {
		name     string
		colNames []string
		want     []ColumnDef
	}{
		{
			name:     "existing_columns",
			colNames: []string{"id", testColName},
			want: []ColumnDef{
				{Name: "id", Type: common.TypeInt64, Nullable: false},
				{Name: testColName, Type: common.TypeString, Nullable: true},
			},
		},
		{
			name:     "nonexistent_column_gets_null_type",
			colNames: []string{"id", testColNonexistent},
			want: []ColumnDef{
				{Name: "id", Type: common.TypeInt64, Nullable: false},
				{Name: testColNonexistent, Type: common.TypeNull, Nullable: true},
			},
		},
		{
			name:     "all_nonexistent",
			colNames: []string{"foo", "bar"},
			want: []ColumnDef{
				{Name: "foo", Type: common.TypeNull, Nullable: true},
				{Name: "bar", Type: common.TypeNull, Nullable: true},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildScanSchema(tt.colNames, tbl)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i, g := range got {
				w := tt.want[i]
				if g.Name != w.Name || g.Type != w.Type || g.Nullable != w.Nullable {
					t.Errorf("[%d] = %+v, want %+v", i, g, w)
				}
			}
		})
	}
}

// --- 4. resolveBinaryExpr ---

func TestResolveBinaryExpr(t *testing.T) {
	cat := testCatalog()
	tbl, _ := cat.GetTable(testTableUsers)
	a := NewAnalyzer(cat)

	tests := []struct {
		name    string
		expr    *BinaryExpr
		wantErr bool
	}{
		{
			name:    "both_valid",
			expr:    &BinaryExpr{Op: OpEq, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(1)}},
			wantErr: false,
		},
		{
			name:    "left_invalid_column",
			expr:    &BinaryExpr{Op: OpEq, Left: &ColumnExpr{Name: testColNonexistent}, Right: &LiteralExpr{Value: common.NewInt64(1)}},
			wantErr: true,
		},
		{
			name:    "right_invalid_column",
			expr:    &BinaryExpr{Op: OpEq, Left: &LiteralExpr{Value: common.NewInt64(1)}, Right: &ColumnExpr{Name: testColNonexistent}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := a.resolveBinaryExpr(tt.expr, tbl)
			if (err != nil) != tt.wantErr {
				t.Errorf("resolveBinaryExpr() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// --- 5. resolveBinaryExprNoTable ---

func TestResolveBinaryExprNoTable(t *testing.T) {
	a := NewAnalyzer(testCatalog())

	tests := []struct {
		name    string
		expr    *BinaryExpr
		wantErr bool
	}{
		{
			name:    "both_literals_ok",
			expr:    &BinaryExpr{Op: OpAdd, Left: &LiteralExpr{Value: common.NewInt64(1)}, Right: &LiteralExpr{Value: common.NewInt64(2)}},
			wantErr: false,
		},
		{
			name:    "left_column_fails",
			expr:    &BinaryExpr{Op: OpAdd, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(1)}},
			wantErr: true,
		},
		{
			name:    "right_column_fails",
			expr:    &BinaryExpr{Op: OpAdd, Left: &LiteralExpr{Value: common.NewInt64(1)}, Right: &ColumnExpr{Name: "id"}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := a.resolveBinaryExprNoTable(tt.expr)
			if (err != nil) != tt.wantErr {
				t.Errorf("resolveBinaryExprNoTable() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// --- 6. resolveUnaryExpr ---

func TestResolveUnaryExpr(t *testing.T) {
	cat := testCatalog()
	tbl, _ := cat.GetTable(testTableUsers)
	a := NewAnalyzer(cat)

	tests := []struct {
		name    string
		expr    *UnaryExpr
		wantErr bool
	}{
		{
			name:    "valid_inner",
			expr:    &UnaryExpr{Op: OpNeg, Expr: &LiteralExpr{Value: common.NewInt64(5)}},
			wantErr: false,
		},
		{
			name:    "invalid_inner_column",
			expr:    &UnaryExpr{Op: OpNot, Expr: &ColumnExpr{Name: testColNonexistent}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := a.resolveUnaryExpr(tt.expr, tbl)
			if (err != nil) != tt.wantErr {
				t.Errorf("resolveUnaryExpr() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

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
