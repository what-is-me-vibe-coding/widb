package query

import (
	"strings"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// --- pushFilterDown with nested FilterNode (merge case) ---

// TestPushFilterDown_NestedFilterMerge tests that nested FilterNodes are merged
// when the inner filter cannot be pushed down (e.g., above a LimitNode).
func TestPushFilterDown_NestedFilterMerge(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColAge},
		schema: []ColumnDef{
			{Name: testColID, Type: common.TypeInt64},
			{Name: testColAge, Type: common.TypeInt64},
		},
	}

	limit := &LimitNode{Child: scan, Count: 10}

	// Inner filter above limit - cannot be pushed down
	innerFilter := &FilterNode{
		Child: limit,
		Condition: &BinaryExpr{
			Op:    OpGt,
			Left:  &ColumnExpr{Name: testColAge},
			Right: &LiteralExpr{Value: common.NewInt64(20)},
		},
	}

	// Outer filter above inner filter
	outerFilter := &FilterNode{
		Child: innerFilter,
		Condition: &BinaryExpr{
			Op:    OpLt,
			Left:  &ColumnExpr{Name: testColID},
			Right: &LiteralExpr{Value: common.NewInt64(100)},
		},
	}

	result := rule.Apply(outerFilter)

	// The outer filter should be merged with the inner filter
	// (both remain above the limit since they can't be pushed through)
	resultFilter, ok := result.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode (merged filters), got %T", result)
	}
	if resultFilter.Condition == nil {
		t.Error("expected merged filter condition")
	}
}

// --- pushFilterDown with LimitNode child ---

// TestPushFilterDown_LimitChild tests that a filter above a LimitNode cannot
// be pushed through and remains as a FilterNode.
func TestPushFilterDown_LimitChild(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColAge},
		schema: []ColumnDef{
			{Name: testColID, Type: common.TypeInt64},
			{Name: testColAge, Type: common.TypeInt64},
		},
	}

	limit := &LimitNode{Child: scan, Count: 10}

	filter := &FilterNode{
		Child: limit,
		Condition: &BinaryExpr{
			Op:    OpGt,
			Left:  &ColumnExpr{Name: testColAge},
			Right: &LiteralExpr{Value: common.NewInt64(20)},
		},
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

// --- pushDown with unknown node type ---

// TestPushDown_UnknownNodeType tests that pushDown returns the node unchanged
// when it encounters an unknown node type.
func TestPushDown_UnknownNodeType(t *testing.T) {
	rule := &PredicatePushdownRule{}

	// Create a custom PlanNode that is not one of the known types
	unknownNode := &testUnknownPlanNode{
		schema: []ColumnDef{{Name: testColID, Type: common.TypeInt64}},
	}

	result := rule.Apply(unknownNode)
	if result != unknownNode {
		t.Error("expected unknown node to be returned unchanged")
	}
}

// testUnknownPlanNode is a PlanNode implementation not recognized by the optimizer.
type testUnknownPlanNode struct {
	schema []ColumnDef
}

func (n *testUnknownPlanNode) planNode()            {}
func (n *testUnknownPlanNode) Schema() []ColumnDef  { return n.schema }
func (n *testUnknownPlanNode) Children() []PlanNode { return nil }
func (n *testUnknownPlanNode) String() string       { return "UnknownNode" }

// --- pushFilterDown with unknown child type ---

// TestPushFilterDown_UnknownChild tests pushFilterDown when the child is
// an unknown PlanNode type (not Scan, Filter, Project, Aggregate).
func TestPushFilterDown_UnknownChild(t *testing.T) {
	rule := &PredicatePushdownRule{}

	unknownChild := &testUnknownPlanNode{
		schema: []ColumnDef{{Name: testColID, Type: common.TypeInt64}},
	}

	filter := &FilterNode{
		Child: unknownChild,
		Condition: &BinaryExpr{
			Op:    OpGt,
			Left:  &ColumnExpr{Name: testColID},
			Right: &LiteralExpr{Value: common.NewInt64(0)},
		},
	}

	result := rule.Apply(filter)

	// Filter should remain above the unknown child
	resultFilter, ok := result.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode, got %T", result)
	}
	if resultFilter.Condition == nil {
		t.Error("expected filter condition to remain")
	}
}

// ---------------------------------------------------------------------------
// PlanNode.String() methods: ScanNode with/without predicate, FilterNode,
// ProjectNode, AggregateNode, LimitNode
// ---------------------------------------------------------------------------

func TestScanNodeString_WithPredicate(t *testing.T) {
	scan := &ScanNode{
		Table:     testTableUsers,
		Columns:   []string{testColID, testColAge},
		schema:    []ColumnDef{{Name: testColID, Type: common.TypeInt64}, {Name: testColAge, Type: common.TypeInt64}},
		Predicate: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
	}
	s := scan.String()
	if s == "" {
		t.Error("expected non-empty string representation")
	}
	if !strings.Contains(s, "Predicate") {
		t.Errorf("expected string to contain 'Predicate', got %q", s)
	}
	if !strings.Contains(s, testTableUsers) {
		t.Errorf("expected string to contain table name %q, got %q", testTableUsers, s)
	}
}

func TestScanNodeString_WithoutPredicate_V2(t *testing.T) {
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}},
	}
	s := scan.String()
	if s == "" {
		t.Error("expected non-empty string representation")
	}
	if strings.Contains(s, "Predicate") {
		t.Errorf("expected string NOT to contain 'Predicate' when no predicate, got %q", s)
	}
}

func TestFilterNodeString_V2(t *testing.T) {
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}},
	}
	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColID}, Right: &LiteralExpr{Value: common.NewInt64(0)}},
	}
	s := filter.String()
	if s == "" {
		t.Error("expected non-empty string representation")
	}
	if !strings.Contains(s, "Filter") {
		t.Errorf("expected string to contain 'Filter', got %q", s)
	}
}

func TestProjectNodeString_V2(t *testing.T) {
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
	if !strings.Contains(s, "Project") {
		t.Errorf("expected string to contain 'Project', got %q", s)
	}
	if !strings.Contains(s, "AS") {
		t.Errorf("expected string to contain 'AS' for alias, got %q", s)
	}
}

func TestAggregateNodeString_V2(t *testing.T) {
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColAge},
		schema:  []ColumnDef{{Name: testColAge, Type: common.TypeInt64}},
	}
	agg := &AggregateNode{
		Child:      scan,
		GroupBy:    []Expression{&ColumnExpr{Name: testColAge}},
		Aggregates: []AggregateExpr{{Func: AggCount, Arg: &StarExpr{}}},
		schema:     []ColumnDef{{Name: testColAge, Type: common.TypeInt64}, {Name: testAggCountStar, Type: common.TypeInt64}},
	}
	s := agg.String()
	if s == "" {
		t.Error("expected non-empty string representation")
	}
	if !strings.Contains(s, "Aggregate") {
		t.Errorf("expected string to contain 'Aggregate', got %q", s)
	}
	if !strings.Contains(s, "GroupBy") {
		t.Errorf("expected string to contain 'GroupBy', got %q", s)
	}
}

func TestLimitNodeString_V2(t *testing.T) {
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}},
	}
	limit := &LimitNode{
		Child:  scan,
		Offset: 5,
		Count:  10,
	}
	s := limit.String()
	if s == "" {
		t.Error("expected non-empty string representation")
	}
	if !strings.Contains(s, "Limit") {
		t.Errorf("expected string to contain 'Limit', got %q", s)
	}
	if !strings.Contains(s, "5") || !strings.Contains(s, "10") {
		t.Errorf("expected string to contain offset 5 and count 10, got %q", s)
	}
}

// --- PlanNode String methods for additional coverage ---

// TestLimitNodeString tests LimitNode.String().
func TestLimitNodeString(t *testing.T) {
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}},
	}
	limit := &LimitNode{Child: scan, Offset: 5, Count: 10}
	s := limit.String()
	if s == "" {
		t.Error("expected non-empty string representation")
	}
}

// TestFilterNodeString tests FilterNode.String().
func TestFilterNodeString(t *testing.T) {
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}},
	}
	filter := &FilterNode{
		Child: scan,
		Condition: &BinaryExpr{
			Op:    OpGt,
			Left:  &ColumnExpr{Name: testColID},
			Right: &LiteralExpr{Value: common.NewInt64(0)},
		},
	}
	s := filter.String()
	if s == "" {
		t.Error("expected non-empty string representation")
	}
}

// TestAggregateNodeString_WithGroupBy tests AggregateNode.String() with GROUP BY.
func TestAggregateNodeString_WithGroupBy(t *testing.T) {
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColAge},
		schema: []ColumnDef{
			{Name: testColID, Type: common.TypeInt64},
			{Name: testColAge, Type: common.TypeInt64},
		},
	}
	agg := &AggregateNode{
		Child:      scan,
		GroupBy:    []Expression{&ColumnExpr{Name: testColAge}},
		Aggregates: []AggregateExpr{{Func: AggCount, Arg: &StarExpr{}}},
		schema:     []ColumnDef{{Name: testColAge, Type: common.TypeInt64}, {Name: testAggCountStar, Type: common.TypeInt64}},
	}
	s := agg.String()
	if s == "" {
		t.Error("expected non-empty string representation")
	}
}
