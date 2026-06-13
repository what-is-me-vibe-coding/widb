package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// --- LiteralExpr.String() ---

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

// --- SelectStatement.String() ---

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

// --- CreateTableStatement.String() ---

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

// --- LimitClause.String() ---

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

// --- UnaryOp.String() ---

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

// ---------------------------------------------------------------------------
// sliceChunk: 成功、全量切片、越界错误、空切片
// ---------------------------------------------------------------------------

// buildTestChunkForStability 创建一个包含两列（Int64, String）共 5 行的 Chunk
func buildTestChunkForStability(t *testing.T) *storage.Chunk {
	t.Helper()
	chunk := storage.NewChunk(8)

	col1 := storage.NewColumnVector(0, common.TypeInt64, 8)
	for i := int64(10); i < 15; i++ {
		if err := col1.Append(common.NewInt64(i)); err != nil {
			t.Fatalf("Append Int64 失败: %v", err)
		}
	}

	col2 := storage.NewColumnVector(1, common.TypeString, 8)
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		if err := col2.Append(common.NewString(s)); err != nil {
			t.Fatalf("Append String 失败: %v", err)
		}
	}

	if err := chunk.AddColumn(col1); err != nil {
		t.Fatalf("AddColumn Int64 失败: %v", err)
	}
	if err := chunk.AddColumn(col2); err != nil {
		t.Fatalf("AddColumn String 失败: %v", err)
	}
	return chunk
}

func TestCoverageStabilitySliceChunkSuccess(t *testing.T) {
	chunk := buildTestChunkForStability(t)

	result, err := sliceChunk(chunk, 1, 4)
	if err != nil {
		t.Fatalf("sliceChunk 失败: %v", err)
	}
	if result.RowCount() != 3 {
		t.Errorf("RowCount = %d, want 3", result.RowCount())
	}
	if result.ColumnCount() != 2 {
		t.Errorf("ColumnCount = %d, want 2", result.ColumnCount())
	}

	col0, _ := result.GetColumn(0)
	v := col0.GetValue(0)
	if v.Int64 != 11 {
		t.Errorf("col0 row0 = %d, want 11", v.Int64)
	}

	col1, _ := result.GetColumn(1)
	v2 := col1.GetValue(2)
	if v2.Str != "d" {
		t.Errorf("col1 row2 = %q, want %q", v2.Str, "d")
	}
}

func TestCoverageStabilitySliceChunkFull(t *testing.T) {
	chunk := buildTestChunkForStability(t)

	result, err := sliceChunk(chunk, 0, 5)
	if err != nil {
		t.Fatalf("sliceChunk 全量切片失败: %v", err)
	}
	if result.RowCount() != 5 {
		t.Errorf("RowCount = %d, want 5", result.RowCount())
	}
}

func TestCoverageStabilitySliceChunkOutOfRange(t *testing.T) {
	chunk := buildTestChunkForStability(t)

	_, err := sliceChunk(chunk, 0, 100)
	if err == nil {
		t.Error("越界切片应返回错误")
	}
}

func TestCoverageStabilitySliceChunkEmpty(t *testing.T) {
	chunk := buildTestChunkForStability(t)

	result, err := sliceChunk(chunk, 2, 2)
	if err != nil {
		t.Fatalf("空切片不应返回错误: %v", err)
	}
	if result.RowCount() != 0 {
		t.Errorf("RowCount = %d, want 0", result.RowCount())
	}
}

// ---------------------------------------------------------------------------
// buildGroupKey: 分隔符碰撞测试
// ---------------------------------------------------------------------------

// TestBuildGroupKeySeparatorNoCollision 验证 buildGroupKey 使用 '\x00' 分隔符时，
// 列值中包含 '|' 字符不会导致分组键碰撞。
// 修复前使用 '|' 作为分隔符，"a|b"|"c" 和 "a"|"b|c" 会产生相同的键。
func TestBuildGroupKeySeparatorNoCollision(t *testing.T) {
	t.Parallel()

	colIdxMap := map[string]int{testStrCol1: 0, testStrCol2: 1}

	// 情况 1: col1="a|b", col2="c"
	row1 := map[string]common.Value{
		testStrCol1: common.NewString("a|b"),
		testStrCol2: common.NewString("c"),
	}
	groupBy1 := []Expression{
		&ResolvedColumnExpr{Name: testStrCol1, Idx: 0, typ: common.TypeString},
		&ResolvedColumnExpr{Name: testStrCol2, Idx: 1, typ: common.TypeString},
	}
	key1 := buildGroupKey(groupBy1, row1, colIdxMap)

	// 情况 2: col1="a", col2="b|c"
	row2 := map[string]common.Value{
		testStrCol1: common.NewString("a"),
		testStrCol2: common.NewString("b|c"),
	}
	key2 := buildGroupKey(groupBy1, row2, colIdxMap)

	if key1 == key2 {
		t.Errorf("分组键碰撞: key1=%q, key2=%q，不同列值应产生不同分组键", key1, key2)
	}
}

// TestBuildGroupKeySeparatorWithNullChar 验证使用 '\x00' 分隔符时，
// 即使列值包含 '\x00' 也不会产生碰撞（因为 '\x00' 在正常文本中极少出现）。
func TestBuildGroupKeySeparatorWithNullChar(t *testing.T) {
	t.Parallel()

	colIdxMap := map[string]int{testStrCol1: 0, testStrCol2: 1}

	groupBy := []Expression{
		&ResolvedColumnExpr{Name: testStrCol1, Idx: 0, typ: common.TypeString},
		&ResolvedColumnExpr{Name: testStrCol2, Idx: 1, typ: common.TypeString},
	}

	// 情况 1: col1="a", col2="b"
	row1 := map[string]common.Value{
		testStrCol1: common.NewString("a"),
		testStrCol2: common.NewString("b"),
	}
	key1 := buildGroupKey(groupBy, row1, colIdxMap)

	// 情况 2: col1="a\x00b", col2="" — 不同的值组合
	row2 := map[string]common.Value{
		testStrCol1: common.NewString("a\x00b"),
		testStrCol2: common.NewString(""),
	}
	key2 := buildGroupKey(groupBy, row2, colIdxMap)

	if key1 == key2 {
		t.Errorf("分组键碰撞: key1=%q, key2=%q", key1, key2)
	}
}

// TestBuildGroupKeyEmptyGroupBy 验证空 GROUP BY 返回空字符串。
func TestBuildGroupKeyEmptyGroupBy(t *testing.T) {
	t.Parallel()

	key := buildGroupKey(nil, nil, nil)
	if key != "" {
		t.Errorf("expected empty key for empty GROUP BY, got %q", key)
	}
}
