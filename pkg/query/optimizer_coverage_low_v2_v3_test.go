package query

import (
	"strings"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ---------------------------------------------------------------------------
// pushFilterDown through AggregateNode: predicates referencing GROUP BY
// columns remain as FilterNode above Aggregate, while other predicates
// are pushed below.
// ---------------------------------------------------------------------------

func TestPushFilterDownThroughAggregate_SplitPredicate(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColAge, testColScore},
		schema: []ColumnDef{
			{Name: testColID, Type: common.TypeInt64},
			{Name: testColAge, Type: common.TypeInt64},
			{Name: testColScore, Type: common.TypeFloat64},
		},
	}

	agg := &AggregateNode{
		Child:   scan,
		GroupBy: []Expression{&ColumnExpr{Name: testColAge}},
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
		},
		schema: []ColumnDef{
			{Name: testColAge, Type: common.TypeInt64},
			{Name: testAggCountStar, Type: common.TypeInt64},
		},
	}

	// Condition: age > 20 AND COUNT(*) > 5
	// age > 20 references a GROUP BY column -> stays above aggregate
	// COUNT(*) > 5 references an aggregate column -> stays above aggregate
	// But wait: splitPredicateByAggregate checks if cols are in aggCols.
	// "age" is in GroupBy, so it's in aggCols -> stays as remaining.
	// "COUNT(*)" is in Aggregates, so it's in aggCols -> stays as remaining.
	// So both stay above the aggregate.
	filter := &FilterNode{
		Child: agg,
		Condition: &BinaryExpr{
			Op:    OpAnd,
			Left:  &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
			Right: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testAggCountStar}, Right: &LiteralExpr{Value: common.NewInt64(5)}},
		},
	}

	result := rule.Apply(filter)

	// Both predicates reference GROUP BY / aggregate columns, so they remain
	// as a FilterNode above the AggregateNode
	resultFilter, ok := result.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode above Aggregate, got %T", result)
	}
	if resultFilter.Condition == nil {
		t.Error("expected remaining filter condition above aggregate")
	}

	// The child should be an AggregateNode
	resultAgg, ok := resultFilter.Child.(*AggregateNode)
	if !ok {
		t.Fatalf("expected AggregateNode under Filter, got %T", resultFilter.Child)
	}
	// No predicate should have been pushed below the aggregate
	resultAggChildScan, ok := resultAgg.Child.(*ScanNode)
	if !ok {
		t.Fatalf("expected ScanNode under Aggregate, got %T", resultAgg.Child)
	}
	if resultAggChildScan.Predicate != nil {
		t.Errorf("expected no predicate pushed to ScanNode, got %v", resultAggChildScan.Predicate)
	}
}

// TestPushFilterDownThroughAggregate_PushablePredicate tests that a predicate
// referencing a non-GROUP BY, non-aggregate column is pushed below the aggregate.
func TestPushFilterDownThroughAggregate_PushablePredicate(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColAge, testColScore},
		schema: []ColumnDef{
			{Name: testColID, Type: common.TypeInt64},
			{Name: testColAge, Type: common.TypeInt64},
			{Name: testColScore, Type: common.TypeFloat64},
		},
	}

	agg := &AggregateNode{
		Child:   scan,
		GroupBy: []Expression{&ColumnExpr{Name: testColAge}},
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
		},
		schema: []ColumnDef{
			{Name: testColAge, Type: common.TypeInt64},
			{Name: testAggCountStar, Type: common.TypeInt64},
		},
	}

	// Condition: score > 90.0 AND COUNT(*) > 5
	// score > 90.0 does NOT reference a GROUP BY or aggregate column -> pushed below
	// COUNT(*) > 5 references an aggregate column -> stays above
	filter := &FilterNode{
		Child: agg,
		Condition: &BinaryExpr{
			Op:    OpAnd,
			Left:  &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColScore}, Right: &LiteralExpr{Value: common.NewFloat64(90.0)}},
			Right: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testAggCountStar}, Right: &LiteralExpr{Value: common.NewInt64(5)}},
		},
	}

	result := rule.Apply(filter)

	// There should be a FilterNode remaining above the AggregateNode
	// for the COUNT(*) > 5 predicate
	resultFilter, ok := result.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode above Aggregate, got %T", result)
	}
	if resultFilter.Condition == nil {
		t.Error("expected remaining filter condition above aggregate")
	}

	// The child should be an AggregateNode
	resultAgg, ok := resultFilter.Child.(*AggregateNode)
	if !ok {
		t.Fatalf("expected AggregateNode under Filter, got %T", resultFilter.Child)
	}

	// The pushable predicate (score > 90.0) should have been pushed below the aggregate
	_, isFilter := resultAgg.Child.(*FilterNode)
	if !isFilter {
		// It might be a ScanNode if the filter was pushed all the way through
		if scanNode, ok2 := resultAgg.Child.(*ScanNode); ok2 {
			if scanNode.Predicate == nil {
				t.Error("expected pushable predicate to be pushed below aggregate")
			}
		} else {
			t.Fatalf("expected FilterNode or ScanNode under Aggregate, got %T", resultAgg.Child)
		}
	}
}

// --- pushFilterDown with AggregateNode: split predicates (pushable + remaining) ---

// TestPushFilterDown_AggregateSplitPredicate tests pushFilterDown with an AggregateNode
// where some predicates can be pushed down (reference non-aggregate columns) and
// some must remain (reference aggregate columns).
func TestPushFilterDown_AggregateSplitPredicate(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColAge, testColScore},
		schema: []ColumnDef{
			{Name: testColID, Type: common.TypeInt64},
			{Name: testColAge, Type: common.TypeInt64},
			{Name: testColScore, Type: common.TypeFloat64},
		},
	}

	agg := &AggregateNode{
		Child:   scan,
		GroupBy: []Expression{&ColumnExpr{Name: testColAge}},
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
		},
		schema: []ColumnDef{
			{Name: testColAge, Type: common.TypeInt64},
			{Name: testAggCountStar, Type: common.TypeInt64},
		},
	}

	// Condition: score > 50 AND COUNT(*) > 5
	// "score > 50" references a column NOT in GROUP BY or aggregates -> CAN be pushed
	// "COUNT(*) > 5" references an aggregate column -> CANNOT be pushed
	condition := &BinaryExpr{
		Op: OpAnd,
		Left: &BinaryExpr{
			Op:    OpGt,
			Left:  &ColumnExpr{Name: testColScore},
			Right: &LiteralExpr{Value: common.NewFloat64(50.0)},
		},
		Right: &BinaryExpr{
			Op:    OpGt,
			Left:  &ColumnExpr{Name: testAggCountStar},
			Right: &LiteralExpr{Value: common.NewInt64(5)},
		},
	}

	filter := &FilterNode{
		Child:     agg,
		Condition: condition,
	}

	result := rule.Apply(filter)

	// The result should be a FilterNode above the AggregateNode
	// because COUNT(*) > 5 cannot be pushed down, but score > 50 can be
	resultFilter, ok := result.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode (remaining predicate), got %T", result)
	}
	if resultFilter.Condition == nil {
		t.Error("expected remaining filter condition above aggregate")
	}

	// Verify the filter is above the aggregate
	_, ok = resultFilter.Child.(*AggregateNode)
	if !ok {
		t.Fatalf("expected AggregateNode under filter, got %T", resultFilter.Child)
	}

	// Verify the pushable predicate was pushed below the aggregate
	resultAgg := resultFilter.Child.(*AggregateNode)
	innerFilter, ok := resultAgg.Child.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode under aggregate (pushed predicate), got %T", resultAgg.Child)
	}
	if innerFilter.Condition == nil {
		t.Error("expected pushed-down filter condition under aggregate")
	}
}

// TestPushFilterDown_AggregateAllPushable tests pushFilterDown with an AggregateNode
// where all predicates reference non-aggregate/non-GROUP BY columns and can be pushed.
func TestPushFilterDown_AggregateAllPushable(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColAge, testColScore},
		schema: []ColumnDef{
			{Name: testColID, Type: common.TypeInt64},
			{Name: testColAge, Type: common.TypeInt64},
			{Name: testColScore, Type: common.TypeFloat64},
		},
	}

	agg := &AggregateNode{
		Child:   scan,
		GroupBy: []Expression{&ColumnExpr{Name: testColAge}},
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
		},
		schema: []ColumnDef{
			{Name: testColAge, Type: common.TypeInt64},
			{Name: testAggCountStar, Type: common.TypeInt64},
		},
	}

	// Condition: score > 50 (score is NOT in GROUP BY or aggregates, so it can be pushed)
	condition := &BinaryExpr{
		Op:    OpGt,
		Left:  &ColumnExpr{Name: testColScore},
		Right: &LiteralExpr{Value: common.NewFloat64(50.0)},
	}

	filter := &FilterNode{
		Child:     agg,
		Condition: condition,
	}

	result := rule.Apply(filter)

	// Since all predicates can be pushed, the filter should be eliminated
	// and the predicate should be pushed below the aggregate
	resultAgg, ok := result.(*AggregateNode)
	if !ok {
		t.Fatalf("expected AggregateNode (filter fully pushed), got %T", result)
	}
	innerFilter, ok := resultAgg.Child.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode under Aggregate, got %T", resultAgg.Child)
	}
	if innerFilter.Condition == nil {
		t.Error("expected pushed-down filter condition under aggregate")
	}
}

// ---------------------------------------------------------------------------
// pushFilterDown with existing ScanNode predicate: predicates are merged with AND
// ---------------------------------------------------------------------------

func TestPushFilterDown_ScanNodeExistingPredicate(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:     testTableUsers,
		Columns:   []string{testColID, testColAge},
		schema:    []ColumnDef{{Name: testColID, Type: common.TypeInt64}, {Name: testColAge, Type: common.TypeInt64}},
		Predicate: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(18)}},
	}

	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpLt, Left: &ColumnExpr{Name: testColID}, Right: &LiteralExpr{Value: common.NewInt64(100)}},
	}

	result := rule.Apply(filter)

	resultScan, ok := result.(*ScanNode)
	if !ok {
		t.Fatalf("expected ScanNode with merged predicate, got %T", result)
	}
	if resultScan.Predicate == nil {
		t.Fatal("expected merged predicate in ScanNode")
	}

	// The merged predicate should be an AND expression
	binExpr, ok := resultScan.Predicate.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr for merged predicate, got %T", resultScan.Predicate)
	}
	if binExpr.Op != OpAnd {
		t.Errorf("expected OpAnd for merged predicate, got %v", binExpr.Op)
	}
}

// ---------------------------------------------------------------------------
// pushFilterDown through ProjectNode (cannot push): filter references a column
// not in the project's child schema, so filter is NOT pushed down
// ---------------------------------------------------------------------------

func TestPushFilterDownThroughProject_CannotPushMissingColumn(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName},
		schema: []ColumnDef{
			{Name: testColID, Type: common.TypeInt64},
			{Name: testColName, Type: common.TypeString},
		},
	}

	// Project only id and name
	proj := &ProjectNode{
		Child:       scan,
		Expressions: []Expression{&ColumnExpr{Name: testColID}, &ColumnExpr{Name: testColName}},
		Aliases:     []string{"", ""},
		schema: []ColumnDef{
			{Name: testColID, Type: common.TypeInt64},
			{Name: testColName, Type: common.TypeString},
		},
	}

	// Filter references "score" which does NOT exist in proj.Child.Schema()
	filter := &FilterNode{
		Child:     proj,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColScore}, Right: &LiteralExpr{Value: common.NewFloat64(90.0)}},
	}

	result := rule.Apply(filter)

	// Filter should NOT be pushed down; it should remain as FilterNode above ProjectNode
	resultFilter, ok := result.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode (not pushed through project), got %T", result)
	}
	if resultFilter.Condition == nil {
		t.Error("expected filter condition to remain")
	}

	// The child of the filter should be a ProjectNode
	resultProj, ok := resultFilter.Child.(*ProjectNode)
	if !ok {
		t.Fatalf("expected ProjectNode under Filter, got %T", resultFilter.Child)
	}
	// The ProjectNode's child should be a ScanNode (no filter pushed below)
	_, ok = resultProj.Child.(*ScanNode)
	if !ok {
		t.Fatalf("expected ScanNode under Project, got %T", resultProj.Child)
	}
}

// --- pushFilterDown with ProjectNode that can't be pushed through ---

// TestPushFilterDown_ProjectCannotPush tests pushFilterDown with a ProjectNode
// where the filter references a column not in the project's child schema,
// so the filter cannot be pushed through.
func TestPushFilterDown_ProjectCannotPush(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName},
		schema: []ColumnDef{
			{Name: testColID, Type: common.TypeInt64},
			{Name: testColName, Type: common.TypeString},
		},
	}

	proj := &ProjectNode{
		Child:       scan,
		Expressions: []Expression{&ColumnExpr{Name: testColID}},
		Aliases:     []string{""},
		schema: []ColumnDef{
			{Name: testColID, Type: common.TypeInt64},
		},
	}

	// Filter references "age" which is NOT in the project's child schema
	filter := &FilterNode{
		Child: proj,
		Condition: &BinaryExpr{
			Op:    OpGt,
			Left:  &ColumnExpr{Name: testColAge},
			Right: &LiteralExpr{Value: common.NewInt64(20)},
		},
	}

	result := rule.Apply(filter)

	// The filter should remain above the project since "age" is not in the project schema
	resultFilter, ok := result.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode (cannot push through project), got %T", result)
	}
	if resultFilter.Condition == nil {
		t.Error("expected filter condition to remain above project")
	}
}

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
