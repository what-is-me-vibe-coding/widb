package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// --- pushFilterDown: filter push into join, filter on project (87.5%) ---

// TestPushFilterDownIntoLimit tests that a filter above a LimitNode cannot
// be pushed through, and remains as a FilterNode.
func TestPushFilterDownIntoLimit(t *testing.T) {
	rule := &PredicatePushdownRule{}
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColAge},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}, {Name: testColAge, Type: common.TypeInt64}},
	}
	limit := &LimitNode{Child: scan, Count: 10}
	filter := &FilterNode{
		Child:     limit,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
	}
	result := rule.Apply(filter)
	resultFilter, ok := result.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode (cannot push through limit), got %T", result)
	}
	if resultFilter.Condition == nil {
		t.Error("expected filter condition to remain above limit")
	}
}

// TestPushFilterDownProjectCanPush tests pushing a filter through a ProjectNode
// when all referenced columns exist in the project's child schema.
func TestPushFilterDownProjectCanPush(t *testing.T) {
	rule := &PredicatePushdownRule{}
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge},
		schema: []ColumnDef{
			{Name: testColID, Type: common.TypeInt64},
			{Name: testColName, Type: common.TypeString},
			{Name: testColAge, Type: common.TypeInt64},
		},
	}
	proj := &ProjectNode{
		Child:       scan,
		Expressions: []Expression{&ColumnExpr{Name: testColID}, &ColumnExpr{Name: testColAge}},
		Aliases:     []string{"", ""},
		schema:      []ColumnDef{{Name: testColID, Type: common.TypeInt64}, {Name: testColAge, Type: common.TypeInt64}},
	}
	filter := &FilterNode{
		Child:     proj,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
	}
	result := rule.Apply(filter)
	resultProj, ok := result.(*ProjectNode)
	if !ok {
		t.Fatalf("expected ProjectNode, got %T", result)
	}
	innerFilter, ok := resultProj.Child.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode under Project, got %T", resultProj.Child)
	}
	if innerFilter.Condition == nil {
		t.Error("expected pushed-down filter condition")
	}
}

// --- collectColumnRefsInto: aggregate expression column refs (87.5%) ---

// TestCollectColumnRefsIntoFuncExpr tests that collectColumnRefsInto correctly
// collects column references from function expression arguments.
func TestCollectColumnRefsIntoFuncExpr(t *testing.T) {
	seen := make(map[string]bool)
	expr := &FuncExpr{
		Name: testFuncAbs,
		Args: []Expression{&ColumnExpr{Name: testColAge}},
	}
	collectColumnRefsInto(expr, seen)
	if !seen[testColAge] {
		t.Errorf("expected 'age' to be collected from FuncExpr args, got %v", seen)
	}
}

// TestCollectColumnRefsIntoResolvedColumnExpr tests that collectColumnRefsInto
// correctly collects column references from ResolvedColumnExpr.
func TestCollectColumnRefsIntoResolvedColumnExpr(t *testing.T) {
	seen := make(map[string]bool)
	expr := &ResolvedColumnExpr{Name: testColID, Idx: 0, typ: common.TypeInt64}
	collectColumnRefsInto(expr, seen)
	if !seen[testColID] {
		t.Errorf("expected 'id' to be collected from ResolvedColumnExpr, got %v", seen)
	}
}

// TestCollectColumnRefsIntoUnaryExpr tests that collectColumnRefsInto correctly
// collects column references from UnaryExpr.
func TestCollectColumnRefsIntoUnaryExpr(t *testing.T) {
	seen := make(map[string]bool)
	expr := &UnaryExpr{Op: OpNot, Expr: &ColumnExpr{Name: testColAge}}
	collectColumnRefsInto(expr, seen)
	if !seen[testColAge] {
		t.Errorf("expected 'age' to be collected from UnaryExpr, got %v", seen)
	}
}

// --- extractKeyRange: non-comparison expression (87.0%) ---

// TestExtractKeyRangeNonComparisonExpr tests that extractKeyRange correctly
// skips non-BinaryExpr conjuncts.
func TestExtractKeyRangeNonComparisonExpr(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	// A UnaryExpr (NOT) is not a BinaryExpr, so extractKeyRange should skip it
	pred := &UnaryExpr{Op: OpNot, Expr: &LiteralExpr{Value: common.NewBool(true)}}
	kr := exec.extractKeyRange(pred)
	if kr.start != "" || kr.end != testDefaultKeyRangeEnd {
		t.Errorf("expected default key range for non-comparison expr, got start=%q end=%q", kr.start, kr.end)
	}
}

// TestExtractKeyRangeNonColumnLeft tests that extractKeyRange skips when
// the left side is not a ResolvedColumnExpr.
func TestExtractKeyRangeNonColumnLeft(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	pred := &BinaryExpr{
		Op:    OpEq,
		Left:  &LiteralExpr{Value: common.NewInt64(1)},
		Right: &LiteralExpr{Value: common.NewInt64(1)},
	}
	kr := exec.extractKeyRange(pred)
	if kr.start != "" || kr.end != testDefaultKeyRangeEnd {
		t.Errorf("expected default key range for non-column left, got start=%q end=%q", kr.start, kr.end)
	}
}

// TestExtractKeyRangeNonPrimaryKey tests that extractKeyRange skips when
// the column index is not 0 (not the primary key).
func TestExtractKeyRangeNonPrimaryKey(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	pred := &BinaryExpr{
		Op:    OpEq,
		Left:  &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
		Right: &LiteralExpr{Value: common.NewInt64(25)},
	}
	kr := exec.extractKeyRange(pred)
	if kr.start != "" || kr.end != testDefaultKeyRangeEnd {
		t.Errorf("expected default key range for non-PK column, got start=%q end=%q", kr.start, kr.end)
	}
}

// TestExtractKeyRangeNullLiteral tests that extractKeyRange skips when
// the right side literal is NULL.
func TestExtractKeyRangeNullLiteral(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	pred := &BinaryExpr{
		Op:    OpEq,
		Left:  &ResolvedColumnExpr{Name: testColID, Idx: 0, typ: common.TypeInt64},
		Right: &LiteralExpr{Value: common.NewNull()},
	}
	kr := exec.extractKeyRange(pred)
	if kr.start != "" || kr.end != testDefaultKeyRangeEnd {
		t.Errorf("expected default key range for NULL literal, got start=%q end=%q", kr.start, kr.end)
	}
}

// TestExtractKeyRangeNonLiteralRight tests that extractKeyRange skips when
// the right side is not a LiteralExpr.
func TestExtractKeyRangeNonLiteralRight(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	pred := &BinaryExpr{
		Op:    OpEq,
		Left:  &ResolvedColumnExpr{Name: testColID, Idx: 0, typ: common.TypeInt64},
		Right: &ColumnExpr{Name: testColAge},
	}
	kr := exec.extractKeyRange(pred)
	if kr.start != "" || kr.end != testDefaultKeyRangeEnd {
		t.Errorf("expected default key range for non-literal right, got start=%q end=%q", kr.start, kr.end)
	}
}

// --- String methods for plan nodes (83.3-88.9%) ---

// TestScanNodeStringNoPredicate tests ScanNode.String() without a predicate.
func TestScanNodeStringNoPredicate(t *testing.T) {
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}},
	}
	s := scan.String()
	if s == "" {
		t.Error("expected non-empty string representation")
	}
}

// TestProjectNodeStringNoAlias tests ProjectNode.String() without aliases.
func TestProjectNodeStringNoAlias(t *testing.T) {
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}},
	}
	proj := &ProjectNode{
		Child:       scan,
		Expressions: []Expression{&ColumnExpr{Name: testColID}},
		Aliases:     []string{""},
		schema:      []ColumnDef{{Name: testColID, Type: common.TypeInt64}},
	}
	s := proj.String()
	if s == "" {
		t.Error("expected non-empty string representation")
	}
}

// TestProjectNodeStringWithAlias tests ProjectNode.String() with aliases.
func TestProjectNodeStringWithAlias(t *testing.T) {
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}},
	}
	proj := &ProjectNode{
		Child:       scan,
		Expressions: []Expression{&ColumnExpr{Name: testColID}},
		Aliases:     []string{testColUserID},
		schema:      []ColumnDef{{Name: testColUserID, Type: common.TypeInt64}},
	}
	s := proj.String()
	if s == "" {
		t.Error("expected non-empty string representation")
	}
}

// TestAggregateNodeStringNoGroupBy tests AggregateNode.String() without GROUP BY.
func TestAggregateNodeStringNoGroupBy(t *testing.T) {
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}},
	}
	agg := &AggregateNode{
		Child:      scan,
		GroupBy:    nil,
		Aggregates: []AggregateExpr{{Func: AggCount, Arg: &StarExpr{}}},
		schema:     []ColumnDef{{Name: testAggCountStar, Type: common.TypeInt64}},
	}
	s := agg.String()
	if s == "" {
		t.Error("expected non-empty string representation")
	}
}

// --- inferAggReturnType: unsupported aggregate function (85.7%) ---

// TestInferAggReturnTypeUnsupportedAggregate tests inferAggReturnType with
// an aggregate expression whose argument is neither LiteralExpr nor ColumnExpr.
func TestInferAggReturnTypeUnsupportedAggregate(t *testing.T) {
	// For non-COUNT aggregates, if the arg is a StarExpr (not LiteralExpr or ColumnExpr),
	// inferAggReturnType returns TypeNull
	agg := AggregateExpr{Func: AggSum, Arg: &StarExpr{}}
	result := inferAggReturnType(agg)
	if result != common.TypeNull {
		t.Errorf("expected TypeNull for SUM(*) with StarExpr arg, got %v", result)
	}
}

// TestInferAggReturnTypeCountAlwaysInt64 tests that COUNT always returns TypeInt64.
func TestInferAggReturnTypeCountAlwaysInt64(t *testing.T) {
	agg := AggregateExpr{Func: AggCount, Arg: &StarExpr{}}
	result := inferAggReturnType(agg)
	if result != common.TypeInt64 {
		t.Errorf("expected TypeInt64 for COUNT, got %v", result)
	}
}

// --- inferBinaryReturnType: type mismatch error (88.9%) ---

// TestInferBinaryReturnTypeArithmeticInt tests that arithmetic on two Int64
// columns returns Int64.
func TestInferBinaryReturnTypeArithmeticInt(t *testing.T) {
	expr := &BinaryExpr{
		Op:    OpAdd,
		Left:  &ColumnExpr{Name: "a", typ: common.TypeInt64},
		Right: &ColumnExpr{Name: "b", typ: common.TypeInt64},
	}
	result := inferBinaryReturnType(expr)
	if result != common.TypeInt64 {
		t.Errorf("expected TypeInt64 for int+int, got %v", result)
	}
}

// TestInferBinaryReturnTypeArithmeticFloat tests that arithmetic with one
// Float64 column returns Float64.
func TestInferBinaryReturnTypeArithmeticFloat(t *testing.T) {
	expr := &BinaryExpr{
		Op:    OpAdd,
		Left:  &ColumnExpr{Name: "a", typ: common.TypeFloat64},
		Right: &ColumnExpr{Name: "b", typ: common.TypeInt64},
	}
	result := inferBinaryReturnType(expr)
	if result != common.TypeFloat64 {
		t.Errorf("expected TypeFloat64 for float+int, got %v", result)
	}
}

// TestInferBinaryReturnTypeUnknownOp tests that an unknown binary operator
// returns TypeNull.
func TestInferBinaryReturnTypeUnknownOp(t *testing.T) {
	expr := &BinaryExpr{
		Op:    BinaryOp(99),
		Left:  &LiteralExpr{Value: common.NewInt64(1)},
		Right: &LiteralExpr{Value: common.NewInt64(2)},
	}
	result := inferBinaryReturnType(expr)
	if result != common.TypeNull {
		t.Errorf("expected TypeNull for unknown op, got %v", result)
	}
}

// --- BinaryOp.String: unknown operator ---

// TestBinaryOpStringUnknown tests that an unknown BinaryOp returns "?".
func TestBinaryOpStringUnknown(t *testing.T) {
	op := BinaryOp(99)
	if op.String() != "?" {
		t.Errorf("expected '?' for unknown BinaryOp, got %q", op.String())
	}
}

// --- AggregateFunc.String: unknown aggregate ---

// TestAggregateFuncStringUnknown tests that an unknown AggregateFunc returns "UNKNOWN".
func TestAggregateFuncStringUnknown(t *testing.T) {
	f := AggregateFunc(99)
	if f.String() != aggNameUnknown {
		t.Errorf("expected %q for unknown AggregateFunc, got %q", aggNameUnknown, f.String())
	}
}

// --- exprReturnType: additional expression types ---

// TestExprReturnTypeNullLiteral tests exprReturnType with a null literal.
func TestExprReturnTypeNullLiteral(t *testing.T) {
	result := exprReturnType(&LiteralExpr{Value: common.NewNull()})
	if result != common.TypeNull {
		t.Errorf("expected TypeNull for null literal, got %v", result)
	}
}

// --- inferFuncReturnType: additional function types ---

// TestInferFuncReturnTypeUnknown tests inferFuncReturnType with an unknown function.
func TestInferFuncReturnTypeUnknown(t *testing.T) {
	result := inferFuncReturnType(&FuncExpr{Name: testFuncUnknown, Args: nil})
	if result != common.TypeNull {
		t.Errorf("expected TypeNull for unknown function, got %v", result)
	}
}

// TestInferFuncReturnTypeSumNoArgs tests inferFuncReturnType for SUM with no args.
func TestInferFuncReturnTypeSumNoArgs(t *testing.T) {
	result := inferFuncReturnType(&FuncExpr{Name: aggNameSum, Args: nil})
	if result != common.TypeNull {
		t.Errorf("expected TypeNull for SUM with no args, got %v", result)
	}
}

// TestInferFuncReturnTypeMinNoArgs tests inferFuncReturnType for MIN with no args.
func TestInferFuncReturnTypeMinNoArgs(t *testing.T) {
	result := inferFuncReturnType(&FuncExpr{Name: aggNameMin, Args: nil})
	if result != common.TypeNull {
		t.Errorf("expected TypeNull for MIN with no args, got %v", result)
	}
}

// --- ColumnPruningRule: edge cases ---

// TestColumnPruningNoNeededColumns tests that ColumnPruningRule returns the
// scan unchanged when no columns are needed.
func TestColumnPruningNoNeededColumns(t *testing.T) {
	rule := &ColumnPruningRule{}
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}},
	}
	result := rule.pruneNode(scan, map[string]bool{})
	resultScan, ok := result.(*ScanNode)
	if !ok {
		t.Fatalf("expected ScanNode, got %T", result)
	}
	if len(resultScan.Columns) != 1 {
		t.Errorf("expected 1 column (no pruning when needed is empty), got %d", len(resultScan.Columns))
	}
}

// --- splitConjuncts: non-AND expression ---

// TestSplitConjunctsNonAnd tests that splitConjuncts returns a single-element
// slice for a non-AND expression.
func TestSplitConjunctsNonAnd(t *testing.T) {
	expr := &BinaryExpr{Op: OpEq, Left: &ColumnExpr{Name: "a"}, Right: &LiteralExpr{Value: common.NewInt64(1)}}
	conjuncts := splitConjuncts(expr)
	if len(conjuncts) != 1 {
		t.Errorf("expected 1 conjunct for non-AND expr, got %d", len(conjuncts))
	}
}

// --- mergeConjuncts: single element ---

// TestMergeConjunctsSingle tests that mergeConjuncts with a single element
// returns that element directly.
func TestMergeConjunctsSingle(t *testing.T) {
	expr := &BinaryExpr{Op: OpEq, Left: &ColumnExpr{Name: "a"}, Right: &LiteralExpr{Value: common.NewInt64(1)}}
	result := mergeConjuncts([]Expression{expr})
	if result != expr {
		t.Error("expected single element to be returned directly")
	}
}

// --- buildColIdxMap / buildColIdxMapFromSchema ---

// TestBuildColIdxMap tests the buildColIdxMap helper function.
func TestBuildColIdxMap(t *testing.T) {
	m := buildColIdxMap([]string{testColID, testColName, testColAge})
	if m[testColID] != 0 || m[testColName] != 1 || m[testColAge] != 2 {
		t.Errorf("unexpected column index map: %v", m)
	}
}

// TestBuildColIdxMapFromSchemaBasic tests the buildColIdxMapFromSchema helper function.
func TestBuildColIdxMapFromSchemaBasic(t *testing.T) {
	schema := []ColumnDef{
		{Name: testColID, Type: common.TypeInt64},
		{Name: testColName, Type: common.TypeString},
	}
	m := buildColIdxMapFromSchema(schema)
	if m[testColID] != 0 || m[testColName] != 1 {
		t.Errorf("unexpected column index map: %v", m)
	}
}
