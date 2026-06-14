package query

import (
	"strings"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestPlanNodeString(t *testing.T) {
	scan := &ScanNode{
		Table:     testTableUsers,
		Columns:   []string{"id", testColName},
		Predicate: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(0)}},
		schema:    []ColumnDef{{Name: "id", Type: common.TypeInt64}, {Name: testColName, Type: common.TypeString}},
	}

	s := scan.String()
	if s == "" {
		t.Error("expected non-empty string representation")
	}

	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpEq, Left: &ColumnExpr{Name: testColName}, Right: &LiteralExpr{Value: common.NewString("test")}},
	}
	s = filter.String()
	if s == "" {
		t.Error("expected non-empty string representation for FilterNode")
	}

	limit := &LimitNode{Child: filter, Offset: 0, Count: 10}
	s = limit.String()
	if s == "" {
		t.Error("expected non-empty string representation for LimitNode")
	}

	proj := &ProjectNode{
		Child:       scan,
		Expressions: []Expression{&ColumnExpr{Name: "id"}},
		Aliases:     []string{testColUserID},
		schema:      []ColumnDef{{Name: testColUserID, Type: common.TypeInt64}},
	}
	s = proj.String()
	if s == "" {
		t.Error("expected non-empty string representation for ProjectNode")
	}

	agg := &AggregateNode{
		Child:      scan,
		GroupBy:    []Expression{&ColumnExpr{Name: "id"}},
		Aggregates: []AggregateExpr{{Func: AggCount, Arg: &StarExpr{}}},
		schema:     []ColumnDef{{Name: "id", Type: common.TypeInt64}, {Name: testAggCountStar, Type: common.TypeInt64}},
	}
	s = agg.String()
	if s == "" {
		t.Error("expected non-empty string representation for AggregateNode")
	}
}

func TestAggregateFuncString(t *testing.T) {
	funcs := []AggregateFunc{AggCount, AggSum, AggMin, AggMax, AggAvg}
	expected := []string{"COUNT", "SUM", "MIN", "MAX", "AVG"}
	for i, f := range funcs {
		if f.String() != expected[i] {
			t.Errorf("expected %s, got %s", expected[i], f.String())
		}
	}
}

func TestAggregateExprString(t *testing.T) {
	agg := AggregateExpr{Func: AggCount, Arg: nil}
	if agg.String() != testAggCountStar {
		t.Errorf("expected 'COUNT(*)', got %q", agg.String())
	}

	agg = AggregateExpr{Func: AggSum, Arg: &ColumnExpr{Name: testColScore}}
	if agg.String() != "SUM(score)" {
		t.Errorf("expected 'SUM(score)', got %q", agg.String())
	}
}

func TestSplitConjuncts(t *testing.T) {
	expr := &BinaryExpr{
		Op:    OpAnd,
		Left:  &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: "a"}, Right: &LiteralExpr{Value: common.NewInt64(1)}},
		Right: &BinaryExpr{Op: OpLt, Left: &ColumnExpr{Name: "b"}, Right: &LiteralExpr{Value: common.NewInt64(10)}},
	}

	conjuncts := splitConjuncts(expr)
	if len(conjuncts) != 2 {
		t.Errorf("expected 2 conjuncts, got %d", len(conjuncts))
	}
}

func TestMergeConjuncts(t *testing.T) {
	conjuncts := []Expression{
		&BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: "a"}, Right: &LiteralExpr{Value: common.NewInt64(1)}},
		&BinaryExpr{Op: OpLt, Left: &ColumnExpr{Name: "b"}, Right: &LiteralExpr{Value: common.NewInt64(10)}},
	}

	result := mergeConjuncts(conjuncts)
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	bin, ok := result.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr, got %T", result)
	}
	if bin.Op != OpAnd {
		t.Errorf("expected AND operator, got %v", bin.Op)
	}
}

func TestMergeConjunctsEmpty(t *testing.T) {
	result := mergeConjuncts(nil)
	if result != nil {
		t.Errorf("expected nil for empty conjuncts, got %v", result)
	}
}

func TestCollectColumnRefs(t *testing.T) {
	expr := &BinaryExpr{
		Op:    OpAnd,
		Left:  &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: "a"}, Right: &LiteralExpr{Value: common.NewInt64(1)}},
		Right: &BinaryExpr{Op: OpLt, Left: &ColumnExpr{Name: "b"}, Right: &LiteralExpr{Value: common.NewInt64(10)}},
	}

	refs := collectColumnRefs(expr)
	if len(refs) != 2 {
		t.Errorf("expected 2 column refs, got %d", len(refs))
	}

	refSet := make(map[string]bool)
	for _, r := range refs {
		refSet[r] = true
	}
	if !refSet["a"] || !refSet["b"] {
		t.Errorf("expected refs 'a' and 'b', got %v", refs)
	}
}

func TestPlanNodeChildren(t *testing.T) {
	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}
	if len(scan.Children()) != 0 {
		t.Error("ScanNode should have no children")
	}

	filter := &FilterNode{Child: scan, Condition: &LiteralExpr{Value: common.NewBool(true)}}
	if len(filter.Children()) != 1 {
		t.Error("FilterNode should have 1 child")
	}

	proj := &ProjectNode{
		Child:       filter,
		Expressions: []Expression{&ColumnExpr{Name: "id"}},
		Aliases:     []string{""},
		schema:      []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}
	if len(proj.Children()) != 1 {
		t.Error("ProjectNode should have 1 child")
	}

	agg := &AggregateNode{
		Child:      scan,
		GroupBy:    []Expression{&ColumnExpr{Name: "id"}},
		Aggregates: []AggregateExpr{{Func: AggCount}},
		schema:     []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}
	if len(agg.Children()) != 1 {
		t.Error("AggregateNode should have 1 child")
	}

	limit := &LimitNode{Child: proj, Count: 10}
	if len(limit.Children()) != 1 {
		t.Error("LimitNode should have 1 child")
	}
}

func TestExprReturnType(t *testing.T) {
	tests := []struct {
		name     string
		expr     Expression
		expected common.DataType
	}{
		{"literal int", &LiteralExpr{Value: common.NewInt64(42)}, common.TypeInt64},
		{"literal float", &LiteralExpr{Value: common.NewFloat64(3.14)}, common.TypeFloat64},
		{"literal string", &LiteralExpr{Value: common.NewString("hello")}, common.TypeString},
		{"literal bool", &LiteralExpr{Value: common.NewBool(true)}, common.TypeBool},
		{"column", &ColumnExpr{Name: "id", typ: common.TypeInt64}, common.TypeInt64},
		{"resolved column", &ResolvedColumnExpr{Name: "id", Idx: 0, typ: common.TypeInt64}, common.TypeInt64},
		{"binary and", &BinaryExpr{Op: OpAnd, Left: &LiteralExpr{Value: common.NewBool(true)}, Right: &LiteralExpr{Value: common.NewBool(false)}}, common.TypeBool},
		{"binary or", &BinaryExpr{Op: OpOr, Left: &LiteralExpr{Value: common.NewBool(true)}, Right: &LiteralExpr{Value: common.NewBool(false)}}, common.TypeBool},
		{"binary eq", &BinaryExpr{Op: OpEq, Left: &ColumnExpr{Name: "id", typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(1)}}, common.TypeBool},
		{"binary lt", &BinaryExpr{Op: OpLt, Left: &ColumnExpr{Name: "id", typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(1)}}, common.TypeBool},
		{"binary add int", &BinaryExpr{Op: OpAdd, Left: &ColumnExpr{Name: "a", typ: common.TypeInt64}, Right: &ColumnExpr{Name: "b", typ: common.TypeInt64}}, common.TypeInt64},
		{"binary add float", &BinaryExpr{Op: OpAdd, Left: &ColumnExpr{Name: "a", typ: common.TypeFloat64}, Right: &ColumnExpr{Name: "b", typ: common.TypeInt64}}, common.TypeFloat64},
		{"unary not", &UnaryExpr{Op: OpNot, Expr: &LiteralExpr{Value: common.NewBool(true)}}, common.TypeBool},
		{"func count", &FuncExpr{Name: "COUNT", Args: []Expression{&StarExpr{}}}, common.TypeInt64},
		{"func sum int", &FuncExpr{Name: "SUM", Args: []Expression{&ColumnExpr{Name: "id", typ: common.TypeInt64}}}, common.TypeInt64},
		{"func avg", &FuncExpr{Name: testAggAVG, Args: []Expression{&ColumnExpr{Name: testColScore, typ: common.TypeFloat64}}}, common.TypeFloat64},
		{"func min", &FuncExpr{Name: "MIN", Args: []Expression{&ColumnExpr{Name: testColAge, typ: common.TypeInt64}}}, common.TypeInt64},
		{"func max", &FuncExpr{Name: "MAX", Args: []Expression{&ColumnExpr{Name: testColAge, typ: common.TypeInt64}}}, common.TypeInt64},
		{"star", &StarExpr{}, common.TypeNull},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := exprReturnType(tt.expr)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestInferAggReturnType(t *testing.T) {
	tests := []struct {
		name     string
		agg      AggregateExpr
		expected common.DataType
	}{
		{testFuncCount, AggregateExpr{Func: AggCount, Arg: nil}, common.TypeInt64},
		{"sum literal", AggregateExpr{Func: AggSum, Arg: &LiteralExpr{Value: common.NewInt64(1)}}, common.TypeInt64},
		{"sum column", AggregateExpr{Func: AggSum, Arg: &ColumnExpr{Name: testColScore, typ: common.TypeFloat64}}, common.TypeFloat64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := inferAggReturnType(tt.agg)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestParseAggFunc(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected AggregateFunc
	}{
		{aggNameCount, testFuncCount, AggCount},
		{"sum", aggNameSum, AggSum},
		{"min", testFuncMin, AggMin},
		{"max", testFuncMax, AggMax},
		{"avg", testFuncAvg, AggAvg},
		{"unknown", testFuncUnknown, AggUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseAggFunc(tt.input)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestScanNodeSchema(t *testing.T) {
	schema := []ColumnDef{
		{Name: "id", Type: common.TypeInt64, Nullable: false},
		{Name: testColName, Type: common.TypeString, Nullable: true},
	}
	scan := &ScanNode{Table: "t", Columns: []string{"id", testColName}, schema: schema}
	if len(scan.Schema()) != 2 {
		t.Errorf("expected 2 columns in schema, got %d", len(scan.Schema()))
	}
}

func TestFilterNodeSchema(t *testing.T) {
	schema := []ColumnDef{{Name: "id", Type: common.TypeInt64}}
	scan := &ScanNode{Table: "t", Columns: []string{"id"}, schema: schema}
	filter := &FilterNode{Child: scan, Condition: &LiteralExpr{Value: common.NewBool(true)}}
	if len(filter.Schema()) != 1 {
		t.Errorf("expected 1 column in filter schema, got %d", len(filter.Schema()))
	}
}

func TestLimitNodeSchema(t *testing.T) {
	schema := []ColumnDef{{Name: "id", Type: common.TypeInt64}}
	scan := &ScanNode{Table: "t", Columns: []string{"id"}, schema: schema}
	limit := &LimitNode{Child: scan, Count: 10}
	if len(limit.Schema()) != 1 {
		t.Errorf("expected 1 column in limit schema, got %d", len(limit.Schema()))
	}
}

// TestAggregateNodeStringWithGroupBy_V7 测试 AggregateNode.String() 包含 GroupBy 表达式。
func TestAggregateNodeStringWithGroupBy_V7(t *testing.T) {
	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id", benchColScore},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}, {Name: benchColScore, Type: common.TypeInt64}},
	}
	agg := &AggregateNode{
		Child:      scan,
		GroupBy:    []Expression{&ColumnExpr{Name: "id"}},
		Aggregates: []AggregateExpr{{Func: AggSum, Arg: &ColumnExpr{Name: benchColScore}}},
		schema:     []ColumnDef{{Name: "id", Type: common.TypeInt64}, {Name: "SUM(score)", Type: common.TypeInt64}},
	}

	s := agg.String()
	if !strings.Contains(s, "GroupBy:") {
		t.Errorf("期望包含 'GroupBy:'，实际 %q", s)
	}
	if !strings.Contains(s, "id") {
		t.Errorf("期望包含 'id'，实际 %q", s)
	}
	if !strings.Contains(s, "SUM(score)") {
		t.Errorf("期望包含 'SUM(score)'，实际 %q", s)
	}
}

// TestAggregateNodeStringNoGroupBy_V7 测试 AggregateNode.String() 无 GroupBy 时不包含 GroupBy。
func TestAggregateNodeStringNoGroupBy_V7(t *testing.T) {
	scan := &ScanNode{
		Table:   "t",
		Columns: []string{benchColScore},
		schema:  []ColumnDef{{Name: benchColScore, Type: common.TypeInt64}},
	}
	agg := &AggregateNode{
		Child:      scan,
		GroupBy:    nil,
		Aggregates: []AggregateExpr{{Func: AggCount, Arg: nil}},
		schema:     []ColumnDef{{Name: "COUNT(*)", Type: common.TypeInt64}},
	}

	s := agg.String()
	if strings.Contains(s, "GroupBy:") {
		t.Errorf("不期望包含 'GroupBy:'，实际 %q", s)
	}
	if !strings.Contains(s, "COUNT(*)") {
		t.Errorf("期望包含 'COUNT(*)'，实际 %q", s)
	}
}

// TestAggregateNodeStringMultipleGroupBy_V7 测试多个 GroupBy 表达式。
func TestAggregateNodeStringMultipleGroupBy_V7(t *testing.T) {
	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"a", "b"},
		schema:  []ColumnDef{{Name: "a", Type: common.TypeInt64}, {Name: "b", Type: common.TypeString}},
	}
	agg := &AggregateNode{
		Child:      scan,
		GroupBy:    []Expression{&ColumnExpr{Name: "a"}, &ColumnExpr{Name: "b"}},
		Aggregates: []AggregateExpr{{Func: AggCount, Arg: nil}},
		schema:     []ColumnDef{{Name: "a", Type: common.TypeInt64}},
	}

	s := agg.String()
	if !strings.Contains(s, "a") || !strings.Contains(s, "b") {
		t.Errorf("期望包含 'a' 和 'b'，实际 %q", s)
	}
}

// TestLimitNodeString_V7 测试 LimitNode.String() 的输出格式。
func TestLimitNodeString_V7(t *testing.T) {
	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}
	limit := &LimitNode{Child: scan, Offset: 5, Count: 10}

	s := limit.String()
	if !strings.Contains(s, "Offset: 5") {
		t.Errorf("期望包含 'Offset: 5'，实际 %q", s)
	}
	if !strings.Contains(s, "Count: 10") {
		t.Errorf("期望包含 'Count: 10'，实际 %q", s)
	}
}

// TestFilterNodeString_V7 测试 FilterNode.String() 的输出格式。
func TestFilterNodeString_V7(t *testing.T) {
	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}
	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(0)}},
	}

	s := filter.String()
	if !strings.Contains(s, "Filter(") {
		t.Errorf("期望包含 'Filter('，实际 %q", s)
	}
	if !strings.Contains(s, "Condition:") {
		t.Errorf("期望包含 'Condition:'，实际 %q", s)
	}
}

// TestProjectNodeStringWithAlias_V7 测试 ProjectNode.String() 带别名的表达式。
func TestProjectNodeStringWithAlias_V7(t *testing.T) {
	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}
	proj := &ProjectNode{
		Child:       scan,
		Expressions: []Expression{&ColumnExpr{Name: "id"}},
		Aliases:     []string{"user_id"},
		schema:      []ColumnDef{{Name: "user_id", Type: common.TypeInt64}},
	}

	s := proj.String()
	if !strings.Contains(s, "AS user_id") {
		t.Errorf("期望包含 'AS user_id'，实际 %q", s)
	}
}

// TestProjectNodeStringNoAlias_V7 测试 ProjectNode.String() 无别名的表达式。
func TestProjectNodeStringNoAlias_V7(t *testing.T) {
	scan := &ScanNode{
		Table:   "t",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}
	proj := &ProjectNode{
		Child:       scan,
		Expressions: []Expression{&ColumnExpr{Name: "id"}},
		Aliases:     []string{""},
		schema:      []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}

	s := proj.String()
	if strings.Contains(s, "AS ") {
		t.Errorf("不期望包含 'AS '，实际 %q", s)
	}
}

// TestScanNodeStringWithPredicate_V7 测试 ScanNode.String() 带谓词。
func TestScanNodeStringWithPredicate_V7(t *testing.T) {
	scan := &ScanNode{
		Table:     "users",
		Columns:   []string{"id", benchColName},
		Predicate: &BinaryExpr{Op: OpEq, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(1)}},
		schema:    []ColumnDef{{Name: "id", Type: common.TypeInt64}, {Name: benchColName, Type: common.TypeString}},
	}

	s := scan.String()
	if !strings.Contains(s, "Predicate:") {
		t.Errorf("期望包含 'Predicate:'，实际 %q", s)
	}
}

// TestScanNodeStringNoPredicate_V7 测试 ScanNode.String() 无谓词。
func TestScanNodeStringNoPredicate_V7(t *testing.T) {
	scan := &ScanNode{
		Table:   "users",
		Columns: []string{"id"},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}},
	}

	s := scan.String()
	if strings.Contains(s, "Predicate:") {
		t.Errorf("不期望包含 'Predicate:'，实际 %q", s)
	}
}

// TestAggregateFuncUnknownString_V7 测试未知聚合函数的 String() 返回 "UNKNOWN"。
func TestAggregateFuncUnknownString_V7(t *testing.T) {
	f := AggregateFunc(99)
	if f.String() != aggNameUnknown {
		t.Errorf("期望 %q，实际 %q", aggNameUnknown, f.String())
	}
}

// TestAggregateExprStringWithArg_V7 测试 AggregateExpr.String() 带参数。
func TestAggregateExprStringWithArg_V7(t *testing.T) {
	agg := AggregateExpr{Func: AggMax, Arg: &ColumnExpr{Name: "price"}}
	s := agg.String()
	if s != "MAX(price)" {
		t.Errorf("期望 'MAX(price)'，实际 %q", s)
	}
}
