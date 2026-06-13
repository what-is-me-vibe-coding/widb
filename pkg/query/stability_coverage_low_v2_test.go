package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/xwb1989/sqlparser"
)

// ---------------------------------------------------------------------------
// convertFuncExpr: 不支持的 func arg 类型路径
// ---------------------------------------------------------------------------

// TestConvertFuncExpr不支持参数类型 验证 convertFuncExpr 在遇到不支持的参数类型时返回错误
func TestConvertFuncExpr不支持参数类型(t *testing.T) {
	p := NewParser()

	// 构造一个 FuncExpr，其中 Exprs 包含非 AliasedExpr 和非 StarExpr 的类型
	// sqlparser.SelectExpr 接口的实现有 AliasedExpr 和 StarExpr，
	// 但我们可以通过构造一个 mock 类型来触发错误路径
	fn := &sqlparser.FuncExpr{
		Name: sqlparser.NewColIdent("test_func"),
		Exprs: []sqlparser.SelectExpr{
			&sqlparser.Nextval{}, // Nextval 不是 AliasedExpr 也不是 StarExpr
		},
	}

	_, err := p.convertFuncExpr(fn)
	if err == nil {
		t.Error("期望不支持的参数类型返回错误，得到 nil")
	}
}

// TestConvertFuncExpr正常路径 验证 convertFuncExpr 正常处理函数表达式
func TestConvertFuncExpr正常路径(t *testing.T) {
	p := NewParser()

	tests := []struct {
		name     string
		funcName string
		args     []sqlparser.SelectExpr
		wantErr  bool
	}{
		{
			name:     testAggCountStar,
			funcName: aggNameCount,
			args: []sqlparser.SelectExpr{
				&sqlparser.StarExpr{},
			},
			wantErr: false,
		},
		{
			name:     "ABS(age)",
			funcName: "abs",
			args: []sqlparser.SelectExpr{
				&sqlparser.AliasedExpr{Expr: &sqlparser.ColName{Name: sqlparser.NewColIdent("age")}},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := &sqlparser.FuncExpr{
				Name:  sqlparser.NewColIdent(tt.funcName),
				Exprs: tt.args,
			}
			result, err := p.convertFuncExpr(fn)
			if tt.wantErr && err == nil {
				t.Error("期望返回错误，得到 nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("不期望错误，得到: %v", err)
			}
			if !tt.wantErr && result == nil {
				t.Error("期望非 nil 结果")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// convertFuncExpr: StarExpr 参数路径
// ---------------------------------------------------------------------------

// TestConvertFuncExprStarArg 验证 convertFuncExpr 正确处理 StarExpr 参数
func TestConvertFuncExprStarArg(t *testing.T) {
	p := NewParser()

	fn := &sqlparser.FuncExpr{
		Name: sqlparser.NewColIdent("count"),
		Exprs: []sqlparser.SelectExpr{
			&sqlparser.StarExpr{},
		},
	}

	result, err := p.convertFuncExpr(fn)
	if err != nil {
		t.Fatalf("convertFuncExpr 失败: %v", err)
	}
	if result.Name != "count" {
		t.Errorf("期望 Name=count，得到 %q", result.Name)
	}
	if len(result.Args) != 1 {
		t.Fatalf("期望 1 个参数，得到 %d", len(result.Args))
	}
	if _, ok := result.Args[0].(*StarExpr); !ok {
		t.Errorf("期望 StarExpr 参数，得到 %T", result.Args[0])
	}
}

// ---------------------------------------------------------------------------
// collectColumnRefs: 边界情况
// ---------------------------------------------------------------------------

// TestCollectColumnRefsBinaryExpr 验证 collectColumnRefs 正确收集 BinaryExpr 中的列引用
func TestCollectColumnRefsBinaryExpr(t *testing.T) {
	expr := &BinaryExpr{
		Op:    OpAnd,
		Left:  &ColumnExpr{Name: testColID},
		Right: &ColumnExpr{Name: testColAge},
	}
	cols := collectColumnRefs(expr)
	if len(cols) != 2 {
		t.Errorf("期望 2 个列引用，得到 %d", len(cols))
	}
}

// TestCollectColumnRefsLiteralExpr 验证 collectColumnRefs 对 LiteralExpr 不收集任何列
func TestCollectColumnRefsLiteralExpr(t *testing.T) {
	expr := &LiteralExpr{Value: common.NewInt64(42)}
	cols := collectColumnRefs(expr)
	if len(cols) != 0 {
		t.Errorf("期望 0 个列引用，得到 %d", len(cols))
	}
}

// ---------------------------------------------------------------------------
// splitPredicateByAggregate: 边界情况
// ---------------------------------------------------------------------------

// TestSplitPredicateByAggregate非AND条件 验证 splitPredicateByAggregate 处理非 AND 条件
func TestSplitPredicateByAggregate非AND条件(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColAge},
		schema: []ColumnDef{
			{Name: testColID, Type: common.TypeInt64},
			{Name: testColAge, Type: common.TypeInt64},
		},
	}

	agg := &AggregateNode{
		Child:   scan,
		GroupBy: []Expression{&ColumnExpr{Name: testColID}},
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
		},
		schema: []ColumnDef{{Name: testColID, Type: common.TypeInt64}, {Name: testAggCountStar, Type: common.TypeInt64}},
	}

	// 非 AND 条件（单个比较表达式）
	cond := &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}}

	pushable, remaining := rule.splitPredicateByAggregate(cond, agg)
	if pushable == nil {
		t.Error("期望 age > 20 可下推（不引用聚合列），得到 nil pushable")
	}
	if remaining != nil {
		t.Error("期望无剩余条件，得到非 nil remaining")
	}
}

// TestSplitPredicateByAggregate全部引用聚合列 验证 splitPredicateByAggregate 处理全部引用聚合列的条件
func TestSplitPredicateByAggregate全部引用聚合列(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColAge},
		schema: []ColumnDef{
			{Name: testColID, Type: common.TypeInt64},
			{Name: testColAge, Type: common.TypeInt64},
		},
	}

	agg := &AggregateNode{
		Child:   scan,
		GroupBy: []Expression{&ColumnExpr{Name: testColID}},
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
		},
		schema: []ColumnDef{{Name: testColID, Type: common.TypeInt64}, {Name: testAggCountStar, Type: common.TypeInt64}},
	}

	// 条件引用聚合列 COUNT(*)
	cond := &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testAggCountStar}, Right: &LiteralExpr{Value: common.NewInt64(5)}}

	pushable, remaining := rule.splitPredicateByAggregate(cond, agg)
	if pushable != nil {
		t.Error("期望不可下推，得到非 nil pushable")
	}
	if remaining == nil {
		t.Error("期望有剩余条件，得到 nil remaining")
	}
}

// ---------------------------------------------------------------------------
// PredicatePushdownRule: 完整优化流程
// ---------------------------------------------------------------------------

// TestPredicatePushdownRule完整流程 验证 PredicatePushdownRule 完整的优化流程
func TestPredicatePushdownRule完整流程(t *testing.T) {
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
		schema: []ColumnDef{
			{Name: testColID, Type: common.TypeInt64},
			{Name: testColAge, Type: common.TypeInt64},
		},
	}

	filter := &FilterNode{
		Child:     proj,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
	}

	result := rule.Apply(filter)

	// Filter 应该被下推到 Project 之下
	resultProj, ok := result.(*ProjectNode)
	if !ok {
		t.Fatalf("期望 ProjectNode，得到 %T", result)
	}

	innerFilter, ok := resultProj.Child.(*FilterNode)
	if !ok {
		t.Fatalf("期望 ProjectNode 下有 FilterNode，得到 %T", resultProj.Child)
	}
	if innerFilter.Condition == nil {
		t.Error("期望下推的 filter 条件")
	}
}

// ---------------------------------------------------------------------------
// Optimizer: 完整优化流程
// ---------------------------------------------------------------------------

// TestOptimizer完整流程 验证 Optimizer 完整的优化流程
func TestOptimizer完整流程(t *testing.T) {
	opt := NewOptimizer()

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColAge},
		schema: []ColumnDef{
			{Name: testColID, Type: common.TypeInt64},
			{Name: testColAge, Type: common.TypeInt64},
		},
	}

	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
	}

	result := opt.Optimize(filter)
	if result == nil {
		t.Fatal("期望非 nil 结果")
	}
}

// ---------------------------------------------------------------------------
// splitConjuncts / mergeConjuncts: 边界情况
// ---------------------------------------------------------------------------

// TestSplitConjuncts嵌套AND 验证 splitConjuncts 正确拆分嵌套 AND 表达式
func TestSplitConjuncts嵌套AND(t *testing.T) {
	// (a AND b) AND c
	expr := &BinaryExpr{
		Op: OpAnd,
		Left: &BinaryExpr{
			Op:    OpAnd,
			Left:  &ColumnExpr{Name: "a"},
			Right: &ColumnExpr{Name: "b"},
		},
		Right: &ColumnExpr{Name: "c"},
	}

	conjuncts := splitConjuncts(expr)
	if len(conjuncts) != 3 {
		t.Errorf("期望 3 个合取式，得到 %d", len(conjuncts))
	}
}

// TestMergeConjuncts空列表 验证 mergeConjuncts 处理空列表
func TestMergeConjuncts空列表(t *testing.T) {
	result := mergeConjuncts(nil)
	if result != nil {
		t.Errorf("期望 nil，得到 %v", result)
	}
}

// TestMergeConjuncts多个元素 验证 mergeConjuncts 合并多个元素
func TestMergeConjuncts多个元素(t *testing.T) {
	a := &ColumnExpr{Name: "a"}
	b := &ColumnExpr{Name: "b"}
	c := &ColumnExpr{Name: "c"}

	result := mergeConjuncts([]Expression{a, b, c})
	if result == nil {
		t.Fatal("期望非 nil 结果")
	}

	// 验证结构是 ((a AND b) AND c)
	bin, ok := result.(*BinaryExpr)
	if !ok {
		t.Fatalf("期望 BinaryExpr，得到 %T", result)
	}
	if bin.Op != OpAnd {
		t.Errorf("期望 OpAnd，得到 %v", bin.Op)
	}
}
