package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const testColTotal = "total"

var usersInt64AgeSchema = []ColumnDef{{Name: "id", Type: common.TypeInt64}, {Name: testColAge, Type: common.TypeInt64}}

func TestPushFilterDownScanNode(t *testing.T) {
	rule := &PredicatePushdownRule{}
	scan := &ScanNode{Table: testTableUsers, Columns: []string{"id", testColAge}, schema: usersInt64AgeSchema}
	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
	}
	result := rule.Apply(filter)
	resultScan, ok := result.(*ScanNode)
	if !ok {
		t.Fatalf("expected ScanNode (filter eliminated), got %T", result)
	}
	if resultScan.Predicate == nil {
		t.Error("expected predicate pushed into scan")
	}
}

func TestPushFilterDownScanNodeExistingPredicate(t *testing.T) {
	rule := &PredicatePushdownRule{}
	scan := &ScanNode{
		Table: testTableUsers, Columns: []string{"id", testColAge}, schema: usersInt64AgeSchema,
		Predicate: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(0)}},
	}
	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpLt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(100)}},
	}
	result := rule.Apply(filter)
	resultScan, ok := result.(*ScanNode)
	if !ok {
		t.Fatalf("expected ScanNode, got %T", result)
	}
	bin, ok := resultScan.Predicate.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr (merged predicate), got %T", resultScan.Predicate)
	}
	if bin.Op != OpAnd {
		t.Errorf("expected OpAnd for merged predicates, got %v", bin.Op)
	}
}

func TestPushFilterDownFilterNode(t *testing.T) {
	rule := &PredicatePushdownRule{}
	scan := &ScanNode{Table: testTableUsers, Columns: []string{"id", testColAge}, schema: usersInt64AgeSchema}
	innerFilter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
	}
	outerFilter := &FilterNode{
		Child:     innerFilter,
		Condition: &BinaryExpr{Op: OpLt, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(100)}},
	}
	result := rule.Apply(outerFilter)
	resultScan, ok := result.(*ScanNode)
	if !ok {
		t.Fatalf("expected ScanNode (filters merged and pushed), got %T", result)
	}
	if resultScan.Predicate == nil {
		t.Error("expected merged predicate in scan")
	}
}

func TestPushFilterDownProjectNodeCanPush(t *testing.T) {
	rule := &PredicatePushdownRule{}
	scan := &ScanNode{
		Table: testTableUsers, Columns: []string{"id", testColName, testColAge},
		schema: []ColumnDef{
			{Name: "id", Type: common.TypeInt64}, {Name: testColName, Type: common.TypeString}, {Name: testColAge, Type: common.TypeInt64},
		},
	}
	proj := &ProjectNode{
		Child: scan, Expressions: []Expression{&ColumnExpr{Name: "id"}, &ColumnExpr{Name: testColName}},
		Aliases: []string{"", ""},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}, {Name: testColName, Type: common.TypeString}},
	}
	filter := &FilterNode{
		Child:     proj,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(0)}},
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

func TestPushFilterDownProjectNodeCannotPush(t *testing.T) {
	rule := &PredicatePushdownRule{}
	scan := &ScanNode{
		Table: testTableUsers, Columns: []string{"id", testColName},
		schema: []ColumnDef{{Name: "id", Type: common.TypeInt64}, {Name: testColName, Type: common.TypeString}},
	}
	proj := &ProjectNode{
		Child: scan,
		Expressions: []Expression{
			&ColumnExpr{Name: "id"}, &ColumnExpr{Name: testColName},
			&BinaryExpr{Op: OpAdd, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(1)}},
		},
		Aliases: []string{"", "", testColTotal},
		schema:  []ColumnDef{{Name: "id", Type: common.TypeInt64}, {Name: testColName, Type: common.TypeString}, {Name: testColTotal, Type: common.TypeInt64}},
	}
	// testColTotal is a computed column not in the scan (project's child) schema
	filter := &FilterNode{
		Child:     proj,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColTotal}, Right: &LiteralExpr{Value: common.NewInt64(5)}},
	}
	result := rule.Apply(filter)
	resultFilter, ok := result.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode (cannot push through project), got %T", result)
	}
	resultProj, ok := resultFilter.Child.(*ProjectNode)
	if !ok {
		t.Fatalf("expected ProjectNode as child of Filter, got %T", resultFilter.Child)
	}
	if _, isFilter := resultProj.Child.(*FilterNode); isFilter {
		t.Error("filter should not have been pushed through project")
	}
}

func TestPushFilterDownAggregateNodeSplit(t *testing.T) {
	rule := &PredicatePushdownRule{}
	scan := &ScanNode{
		Table: testTableUsers, Columns: []string{"id", testColAge, testColScore},
		schema: []ColumnDef{
			{Name: "id", Type: common.TypeInt64}, {Name: testColAge, Type: common.TypeInt64}, {Name: testColScore, Type: common.TypeFloat64},
		},
	}
	agg := &AggregateNode{
		Child: scan, GroupBy: []Expression{&ColumnExpr{Name: testColAge}},
		Aggregates: []AggregateExpr{{Func: AggCount, Arg: &StarExpr{}}},
		schema:     []ColumnDef{{Name: testColAge, Type: common.TypeInt64}, {Name: testAggCountStar, Type: common.TypeInt64}},
	}
	// "id > 0" is pushable (not in groupBy/aggregates), "COUNT(*) > 5" is not
	cond := &BinaryExpr{
		Op:    OpAnd,
		Left:  &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(0)}},
		Right: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testAggCountStar}, Right: &LiteralExpr{Value: common.NewInt64(5)}},
	}
	filter := &FilterNode{Child: agg, Condition: cond}
	result := rule.Apply(filter)
	resultFilter, ok := result.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode (remaining above aggregate), got %T", result)
	}
	if resultFilter.Condition == nil {
		t.Error("expected remaining filter condition above aggregate")
	}
	resultAgg, ok := resultFilter.Child.(*AggregateNode)
	if !ok {
		t.Fatalf("expected AggregateNode under Filter, got %T", resultFilter.Child)
	}
	pushedFilter, ok := resultAgg.Child.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode pushed below Aggregate, got %T", resultAgg.Child)
	}
	if pushedFilter.Condition == nil {
		t.Error("expected pushed-down filter condition below aggregate")
	}
}

func TestPushFilterDownAggregateNodeAllPushable(t *testing.T) {
	rule := &PredicatePushdownRule{}
	scan := &ScanNode{
		Table: testTableUsers, Columns: []string{"id", testColAge, testColScore},
		schema: []ColumnDef{
			{Name: "id", Type: common.TypeInt64}, {Name: testColAge, Type: common.TypeInt64}, {Name: testColScore, Type: common.TypeFloat64},
		},
	}
	agg := &AggregateNode{
		Child: scan, GroupBy: []Expression{&ColumnExpr{Name: testColAge}},
		Aggregates: []AggregateExpr{{Func: AggCount, Arg: &StarExpr{}}},
		schema:     []ColumnDef{{Name: testColAge, Type: common.TypeInt64}, {Name: testAggCountStar, Type: common.TypeInt64}},
	}
	filter := &FilterNode{
		Child:     agg,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(0)}},
	}
	result := rule.Apply(filter)
	resultAgg, ok := result.(*AggregateNode)
	if !ok {
		t.Fatalf("expected AggregateNode (filter fully pushed), got %T", result)
	}
	pushedFilter, ok := resultAgg.Child.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode below Aggregate, got %T", resultAgg.Child)
	}
	if pushedFilter.Condition == nil {
		t.Error("expected pushed-down filter condition")
	}
}

func TestPushFilterDownAggregateNodeNonePushable(t *testing.T) {
	rule := &PredicatePushdownRule{}
	scan := &ScanNode{Table: testTableUsers, Columns: []string{"id", testColAge}, schema: usersInt64AgeSchema}
	agg := &AggregateNode{
		Child: scan, GroupBy: []Expression{&ColumnExpr{Name: testColAge}},
		Aggregates: []AggregateExpr{{Func: AggCount, Arg: &StarExpr{}}},
		schema:     []ColumnDef{{Name: testColAge, Type: common.TypeInt64}, {Name: testAggCountStar, Type: common.TypeInt64}},
	}
	cond := &BinaryExpr{
		Op:    OpAnd,
		Left:  &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
		Right: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testAggCountStar}, Right: &LiteralExpr{Value: common.NewInt64(5)}},
	}
	filter := &FilterNode{Child: agg, Condition: cond}
	result := rule.Apply(filter)
	resultFilter, ok := result.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode (nothing pushable), got %T", result)
	}
	if resultFilter.Condition == nil {
		t.Error("expected filter condition to remain above aggregate")
	}
	resultAgg, ok := resultFilter.Child.(*AggregateNode)
	if !ok {
		t.Fatalf("expected AggregateNode under Filter, got %T", resultFilter.Child)
	}
	if _, isFilter := resultAgg.Child.(*FilterNode); isFilter {
		t.Error("no filter should have been pushed below aggregate")
	}
}

func TestPushFilterDownDefaultCase(t *testing.T) {
	rule := &PredicatePushdownRule{}
	scan := &ScanNode{Table: testTableUsers, Columns: []string{"id"}, schema: []ColumnDef{{Name: "id", Type: common.TypeInt64}}}
	limit := &LimitNode{Child: scan, Count: 10}
	filter := &FilterNode{
		Child:     limit,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(0)}},
	}
	result := rule.Apply(filter)
	resultFilter, ok := result.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode (cannot push through limit), got %T", result)
	}
	if resultFilter.Condition == nil {
		t.Error("expected filter condition to remain")
	}
}

func TestFoldUnaryExpr(t *testing.T) {
	tests := []struct {
		name       string
		op         UnaryOp
		expr       Expression
		wantFolded bool
		wantInt64  int64
	}{
		{"NOT true => false", OpNot, &LiteralExpr{Value: common.NewBool(true)}, true, 0},
		{"NOT false => true", OpNot, &LiteralExpr{Value: common.NewBool(false)}, true, 1},
		{"NOT 5 (truthy int64) => false", OpNot, &LiteralExpr{Value: common.NewInt64(5)}, true, 0},
		{"NOT 0 (falsy int64) => true", OpNot, &LiteralExpr{Value: common.NewInt64(0)}, true, 1},
		{"NOT 3.14 (truthy f64) => false", OpNot, &LiteralExpr{Value: common.NewFloat64(3.14)}, true, 0},
		{"NOT 0.0 (falsy f64) => true", OpNot, &LiteralExpr{Value: common.NewFloat64(0.0)}, true, 1},
		{"NOT 'hello' (truthy str) => false", OpNot, &LiteralExpr{Value: common.NewString("hello")}, true, 0},
		{"NOT '' (falsy str) => true", OpNot, &LiteralExpr{Value: common.NewString("")}, true, 1},
		{"NOT null => not folded", OpNot, &LiteralExpr{Value: common.NewNull()}, false, 0},
		{"NOT column => not folded", OpNot, &ColumnExpr{Name: "id"}, false, 0},
		{"NEG 5 => not folded", OpNeg, &LiteralExpr{Value: common.NewInt64(5)}, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := &ConstantFoldingRule{}
			scan := &ScanNode{
				Table: "t", Columns: []string{"id"}, schema: []ColumnDef{{Name: "id", Type: common.TypeInt64}},
				Predicate: &UnaryExpr{Op: tt.op, Expr: tt.expr},
			}
			result := rule.Apply(scan).(*ScanNode)
			if tt.wantFolded {
				lit, ok := result.Predicate.(*LiteralExpr)
				if !ok {
					t.Fatalf("expected LiteralExpr, got %T", result.Predicate)
				}
				if lit.Value.Int64 != tt.wantInt64 {
					t.Errorf("expected Int64=%d, got %d", tt.wantInt64, lit.Value.Int64)
				}
			} else {
				unary, ok := result.Predicate.(*UnaryExpr)
				if !ok {
					t.Fatalf("expected UnaryExpr (not folded), got %T", result.Predicate)
				}
				if unary.Op != tt.op {
					t.Errorf("expected Op=%v, got %v", tt.op, unary.Op)
				}
			}
		})
	}
}

func TestIsTruthyAllTypes(t *testing.T) {
	tests := []struct {
		name  string
		input common.Value
		want  bool
	}{
		{"null returns false", common.NewNull(), false},
		{"bool true", common.NewBool(true), true},
		{"bool false", common.NewBool(false), false},
		{"int64 nonzero", common.NewInt64(42), true},
		{"int64 zero", common.NewInt64(0), false},
		{"int64 negative", common.NewInt64(-1), true},
		{"float64 nonzero", common.NewFloat64(3.14), true},
		{"float64 zero", common.NewFloat64(0.0), false},
		{"float64 negative", common.NewFloat64(-0.5), true},
		{"string nonempty", common.NewString("hello"), true},
		{"string empty", common.NewString(""), false},
		{"timestamp returns true", common.NewTimestamp(common.NewInt64(0).Time), true},
		{"TypeNull valid=false", common.Value{Typ: common.TypeNull, Valid: false}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTruthyValue(tt.input); got != tt.want {
				t.Errorf("isTruthyValue(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestOptimizerPipelinePredicatePushdown(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()
	optimizer := NewOptimizer()
	stmt, err := parser.Parse("SELECT id, name FROM users WHERE age > 20")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}
	optimized := optimizer.Optimize(plan)
	scan := findScanNode(optimized)
	if scan == nil {
		t.Fatal("expected scan node in optimized plan")
	}
	if scan.Predicate == nil {
		t.Error("expected predicate pushed down to scan node")
	}
}

func TestOptimizerPipelineWithConstantFoldingAndPushdown(t *testing.T) {
	scan := &ScanNode{Table: testTableUsers, Columns: []string{"id", testColAge}, schema: usersInt64AgeSchema}
	filter := &FilterNode{
		Child: scan,
		Condition: &BinaryExpr{
			Op:    OpAnd,
			Left:  &BinaryExpr{Op: OpEq, Left: &LiteralExpr{Value: common.NewInt64(1)}, Right: &LiteralExpr{Value: common.NewInt64(1)}},
			Right: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
		},
	}
	result := (&PredicatePushdownRule{}).Apply(filter)
	result = (&ConstantFoldingRule{}).Apply(result)
	resultScan, ok := result.(*ScanNode)
	if !ok {
		if f, isFilter := result.(*FilterNode); isFilter {
			if s := findScanNode(f); s != nil && s.Predicate != nil {
				return
			}
		}
		t.Fatalf("expected ScanNode (filter eliminated), got %T", result)
	}
	if resultScan.Predicate == nil {
		t.Error("expected predicate in scan")
	}
}

func TestOptimizerPipelineMergeFiltersAndPushdown(t *testing.T) {
	rule := &PredicatePushdownRule{}
	scan := &ScanNode{Table: testTableUsers, Columns: []string{"id", testColAge}, schema: usersInt64AgeSchema}
	innerFilter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
	}
	outerFilter := &FilterNode{
		Child:     innerFilter,
		Condition: &BinaryExpr{Op: OpLt, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(100)}},
	}
	result := rule.Apply(outerFilter)
	resultScan, ok := result.(*ScanNode)
	if !ok {
		t.Fatalf("expected ScanNode (merged and pushed), got %T", result)
	}
	if resultScan.Predicate == nil {
		t.Error("expected merged predicate in scan")
	}
	bin, ok := resultScan.Predicate.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr for merged predicate, got %T", resultScan.Predicate)
	}
	if bin.Op != OpAnd {
		t.Errorf("expected OpAnd, got %v", bin.Op)
	}
}

// TestPushDownRecursion verifies that pushDown recurses into ProjectNode,
// AggregateNode, and LimitNode children, pushing filters to the scan.
func TestPushDownRecursion(t *testing.T) {
	rule := &PredicatePushdownRule{}
	scan := &ScanNode{Table: testTableUsers, Columns: []string{"id", testColAge}, schema: usersInt64AgeSchema}
	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
	}
	t.Run("through ProjectNode", func(t *testing.T) {
		proj := &ProjectNode{
			Child: filter, Expressions: []Expression{&ColumnExpr{Name: "id"}, &ColumnExpr{Name: testColAge}},
			Aliases: []string{"", ""},
			schema:  usersInt64AgeSchema,
		}
		result := rule.Apply(proj)
		resultProj, ok := result.(*ProjectNode)
		if !ok {
			t.Fatalf("expected ProjectNode, got %T", result)
		}
		resultScan := findScanNode(resultProj)
		if resultScan == nil || resultScan.Predicate == nil {
			t.Error("expected predicate pushed into scan through project")
		}
	})
	t.Run("through AggregateNode", func(t *testing.T) {
		innerScan := &ScanNode{Table: testTableUsers, Columns: []string{"id", testColAge}, schema: usersInt64AgeSchema}
		innerFilter := &FilterNode{
			Child:     innerScan,
			Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: "id"}, Right: &LiteralExpr{Value: common.NewInt64(0)}},
		}
		agg := &AggregateNode{
			Child: innerFilter, GroupBy: []Expression{&ColumnExpr{Name: testColAge}},
			Aggregates: []AggregateExpr{{Func: AggCount, Arg: &StarExpr{}}},
			schema:     []ColumnDef{{Name: testColAge, Type: common.TypeInt64}, {Name: testAggCountStar, Type: common.TypeInt64}},
		}
		result := rule.Apply(agg)
		resultAgg, ok := result.(*AggregateNode)
		if !ok {
			t.Fatalf("expected AggregateNode, got %T", result)
		}
		resultScan := findScanNode(resultAgg)
		if resultScan == nil || resultScan.Predicate == nil {
			t.Error("expected predicate pushed into scan through aggregate")
		}
	})
	t.Run("through LimitNode", func(t *testing.T) {
		innerScan := &ScanNode{Table: testTableUsers, Columns: []string{"id", testColAge}, schema: usersInt64AgeSchema}
		innerFilter := &FilterNode{
			Child:     innerScan,
			Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
		}
		limit := &LimitNode{Child: innerFilter, Count: 10}
		result := rule.Apply(limit)
		resultLimit, ok := result.(*LimitNode)
		if !ok {
			t.Fatalf("expected LimitNode, got %T", result)
		}
		resultScan := findScanNode(resultLimit)
		if resultScan == nil || resultScan.Predicate == nil {
			t.Error("expected predicate pushed into scan through limit")
		}
	})
}
