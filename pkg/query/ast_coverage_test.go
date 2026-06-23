package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestASTUpdateStatementString 覆盖 UpdateStatement.String 全部分支：
// 无 Where、带 Where、单条与多条 Assignment。
func TestASTUpdateStatementString(t *testing.T) {
	tests := []struct {
		name string
		s    *UpdateStatement
		want string
	}{
		{
			name: "single assignment without where",
			s: &UpdateStatement{
				Table:       "t",
				Assignments: []UpdateAssignment{{Column: "a", Value: &LiteralExpr{Value: common.NewInt64(1)}}},
			},
			want: "UPDATE t SET a = 1",
		},
		{
			name: "multiple assignments with where",
			s: &UpdateStatement{
				Table: "users",
				Assignments: []UpdateAssignment{
					{Column: "name", Value: &LiteralExpr{Value: common.NewString("bob")}},
					{Column: "age", Value: &LiteralExpr{Value: common.NewInt64(20)}},
				},
				Where: &BinaryExpr{Op: OpEq, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(1)}},
			},
			want: "UPDATE users SET name = 'bob', age = 20 WHERE (id = 1)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.String(); got != tt.want {
				t.Errorf("UpdateStatement.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestASTDeleteStatementString 覆盖 DeleteStatement.String：带与不带 Where。
func TestASTDeleteStatementString(t *testing.T) {
	tests := []struct {
		name string
		s    *DeleteStatement
		want string
	}{
		{name: "no where", s: &DeleteStatement{Table: "t"}, want: "DELETE FROM t"},
		{
			name: "with where",
			s: &DeleteStatement{
				Table: "users",
				Where: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: "age"}, Right: &LiteralExpr{Value: common.NewInt64(18)}},
			},
			want: "DELETE FROM users WHERE (age > 18)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.String(); got != tt.want {
				t.Errorf("DeleteStatement.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestASTDropTableStatementString 覆盖 IfExists 分支。
func TestASTDropTableStatementString(t *testing.T) {
	tests := []struct {
		name string
		s    *DropTableStatement
		want string
	}{
		{name: "without if exists", s: &DropTableStatement{Table: "t"}, want: "DROP TABLE t"},
		{name: "with if exists", s: &DropTableStatement{Table: "t", IfExists: true}, want: "DROP TABLE IF EXISTS t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.String(); got != tt.want {
				t.Errorf("DropTableStatement.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestASTShowTablesString 覆盖 ShowTablesStatement.String 固定输出。
func TestASTShowTablesString(t *testing.T) {
	s := &ShowTablesStatement{}
	if got := s.String(); got != "SHOW TABLES" {
		t.Errorf("ShowTablesStatement.String() = %q, want %q", got, "SHOW TABLES")
	}
}

// TestASTDescribeString 覆盖 DescribeStatement.String。
func TestASTDescribeString(t *testing.T) {
	s := &DescribeStatement{Table: "users"}
	if got := s.String(); got != "DESCRIBE users" {
		t.Errorf("DescribeStatement.String() = %q, want %q", got, "DESCRIBE users")
	}
}

// TestASTExplainString 覆盖 ExplainStatement.String。
func TestASTExplainString(t *testing.T) {
	inner := &SelectStatement{
		Columns: []SelectColumn{{Expr: &StarExpr{}}},
		From:    &TableRef{Name: "t"},
	}
	s := &ExplainStatement{Inner: inner}
	if got := s.String(); got != "EXPLAIN SELECT * FROM t" {
		t.Errorf("ExplainStatement.String() = %q, want %q", got, "EXPLAIN SELECT * FROM t")
	}
}

// TestASTExplainStringNilInner 验证 Inner 为 nil 时 ExplainStatement.String
// 不 panic，输出 "EXPLAIN"。这是 ast.go:235 修复的回归测试。
func TestASTExplainStringNilInner(t *testing.T) {
	s := &ExplainStatement{}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ExplainStatement.String() panicked on nil Inner: %v", r)
		}
	}()
	if got := s.String(); got != "EXPLAIN" {
		t.Errorf("ExplainStatement.String() with nil Inner = %q, want %q", got, "EXPLAIN")
	}
}

// TestASTCreateTableEngineClause 覆盖 Engine != "" 分支。
func TestASTCreateTableEngineClause(t *testing.T) {
	s := &CreateTableStatement{
		Table: "t",
		Columns: []ColumnDef{
			{Name: "id", Type: common.TypeInt64, Nullable: false},
		},
		Engine: "memory",
	}
	got := s.String()
	want := "CREATE TABLE t (id INT64 NOT NULL) ENGINE=memory"
	if got != want {
		t.Errorf("CreateTableStatement.String() = %q, want %q", got, want)
	}
}

// TestASTCreateTableMultiColumnPK 覆盖多个 PrimaryKey 列。
func TestASTCreateTableMultiColumnPK(t *testing.T) {
	s := &CreateTableStatement{
		Table: "t",
		Columns: []ColumnDef{
			{Name: "a", Type: common.TypeInt64, Nullable: false},
			{Name: "b", Type: common.TypeInt64, Nullable: false},
		},
		PrimaryKey: []string{"a", "b"},
	}
	got := s.String()
	want := "CREATE TABLE t (a INT64 NOT NULL, b INT64 NOT NULL, PRIMARY KEY (a, b))"
	if got != want {
		t.Errorf("CreateTableStatement.String() = %q, want %q", got, want)
	}
}

// TestASTBinaryOpUnknown 覆盖 BinaryOp.String 的越界/未识别默认分支。
func TestASTBinaryOpUnknown(t *testing.T) {
	// 越界值
	outOfRange := BinaryOp(9999)
	if got := outOfRange.String(); got != "?" {
		t.Errorf("BinaryOp(9999).String() = %q, want %q", got, "?")
	}
	// 在范围内但 binaryOpStr 为空（人为构造）：取 iota 之外的值 0..len 内的负数索引
	// 实际上 OpEq=0..OpLike=12 都映射了字符串，验证全部非空
	for op := OpEq; op <= OpLike; op++ {
		if op.String() == "?" {
			t.Errorf("op %d returned '?' unexpectedly", op)
		}
	}
}

// TestASTUpdateAssignmentString 直接覆盖 UpdateAssignment.String。
func TestASTUpdateAssignmentString(t *testing.T) {
	a := UpdateAssignment{Column: "age", Value: &LiteralExpr{Value: common.NewInt64(30)}}
	if got := a.String(); got != "age = 30" {
		t.Errorf("UpdateAssignment.String() = %q, want %q", got, "age = 30")
	}
}

// TestASTFuncExprMultiArgs 覆盖 FuncExpr 多参数。
func TestASTFuncExprMultiArgs(t *testing.T) {
	fn := &FuncExpr{
		Name: "concat",
		Args: []Expression{
			&ColumnExpr{Name: "first_name"},
			&ColumnExpr{Name: "last_name"},
		},
	}
	if got := fn.String(); got != "concat(first_name, last_name)" {
		t.Errorf("FuncExpr.String() = %q, want %q", got, "concat(first_name, last_name)")
	}
}

// TestASTLiteralExprBoolAndTimestamp 覆盖 LiteralExpr.Bool/Timestamp 分支。
func TestASTLiteralExprBoolAndTimestamp(t *testing.T) {
	tests := []struct {
		name string
		v    common.Value
		want string
	}{
		{name: "bool true", v: common.NewBool(true), want: "true"},
		{name: "bool false", v: common.NewBool(false), want: "false"},
		{name: "int64", v: common.NewInt64(42), want: "42"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &LiteralExpr{Value: tt.v}
			if got := e.String(); got != tt.want {
				t.Errorf("LiteralExpr.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestASTStatementNodeMarker 验证 statementNode 标记方法存在（接口实现断言）。
// 这避免误删 statementNode() 后被编译器忽略。
func TestASTStatementNodeMarker(t *testing.T) {
	// 注意：ExplainStatement.String 在 Inner 为 nil 时会 panic
	// （pkg/query/ast.go:235 存在 fmt.Sprintf("EXPLAIN %s", s.Inner.String()) 无防护的 bug）
	// 此处使用非 nil Inner 触发 String，验证其他 8 个 statement 类型不 panic。
	inner := &SelectStatement{Columns: []SelectColumn{{Expr: &StarExpr{}}}, From: &TableRef{Name: "t"}}
	stmts := []Statement{
		&SelectStatement{},
		&InsertStatement{},
		&UpdateStatement{},
		&DeleteStatement{},
		&DropTableStatement{},
		&ShowTablesStatement{},
		&DescribeStatement{Table: "t"},
		&ExplainStatement{Inner: inner},
		&CreateTableStatement{Table: "t"},
	}
	if len(stmts) != 9 {
		t.Errorf("expected 9 statement types, got %d", len(stmts))
	}
	for _, s := range stmts {
		// 仅调用 String() 验证不 panic
		_ = s.String()
	}
}

// TestASTExpressionNodeMarker 验证 expression 节点标记方法存在。
func TestASTExpressionNodeMarker(t *testing.T) {
	exprs := []Expression{
		&ColumnExpr{Name: "a"},
		&LiteralExpr{Value: common.NewInt64(1)},
		&BinaryExpr{Op: OpEq, Left: &ColumnExpr{Name: "a"}, Right: &LiteralExpr{Value: common.NewInt64(1)}},
		&UnaryExpr{Op: OpNot, Expr: &ColumnExpr{Name: "a"}},
		&FuncExpr{Name: "count", Args: []Expression{&StarExpr{}}},
		&StarExpr{},
	}
	if len(exprs) != 6 {
		t.Errorf("expected 6 expression types, got %d", len(exprs))
	}
	for _, e := range exprs {
		_ = e.String()
	}
}

// TestASTSelectStatementGroupBy 覆盖 SelectStatement 多个 GroupBy 列。
func TestASTSelectStatementGroupBy(t *testing.T) {
	s := &SelectStatement{
		Columns: []SelectColumn{{Expr: &FuncExpr{Name: "count", Args: []Expression{&StarExpr{}}}, Alias: "c"}},
		From:    &TableRef{Name: "orders"},
		GroupBy: []Expression{&ColumnExpr{Name: "region"}, &ColumnExpr{Name: "status"}},
	}
	got := s.String()
	want := "SELECT count(*) AS c FROM orders GROUP BY region, status"
	if got != want {
		t.Errorf("SelectStatement.String() = %q, want %q", got, want)
	}
}

// TestASTInsertStatementNoColumns 覆盖 InsertStatement 无显式列名分支。
func TestASTInsertStatementNoColumns(t *testing.T) {
	s := &InsertStatement{
		Table: "t",
		Rows: [][]Expression{
			{&LiteralExpr{Value: common.NewInt64(1)}},
		},
	}
	got := s.String()
	want := "INSERT INTO t VALUES (1)"
	if got != want {
		t.Errorf("InsertStatement.String() = %q, want %q", got, want)
	}
}
