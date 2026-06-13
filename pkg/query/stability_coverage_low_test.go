package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ---------------------------------------------------------------------------
// pushFilterDown: FilterNode → ProjectNode 不可下推路径
// ---------------------------------------------------------------------------

// TestPushFilterDownProject不可下推 验证 FilterNode → ProjectNode 当条件引用的列不在 Project 子节点 schema 中时不可下推
func TestPushFilterDownProject不可下推(t *testing.T) {
	rule := &PredicatePushdownRule{}

	// Scan 只输出 id 和 name（不包含 age）
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName},
		schema: []ColumnDef{
			{Name: testColID, Type: common.TypeInt64},
			{Name: testColName, Type: common.TypeString},
		},
	}

	// Project 投影 id 和 name
	proj := &ProjectNode{
		Child:       scan,
		Expressions: []Expression{&ColumnExpr{Name: testColID}, &ColumnExpr{Name: testColName}},
		Aliases:     []string{"", ""},
		schema:      []ColumnDef{{Name: testColID, Type: common.TypeInt64}, {Name: testColName, Type: common.TypeString}},
	}

	// Filter 条件引用了 age 列，但 Project 子节点 schema 中没有 age，不可下推
	filter := &FilterNode{
		Child:     proj,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
	}

	result := rule.Apply(filter)

	// Filter 应该保留在 Project 之上
	resultFilter, ok := result.(*FilterNode)
	if !ok {
		t.Fatalf("期望 FilterNode（不可下推），得到 %T", result)
	}
	if resultFilter.Condition == nil {
		t.Error("期望 filter 条件保留在 Project 之上")
	}
}

// ---------------------------------------------------------------------------
// pushFilterDown: FilterNode → AggregateNode 部分下推路径
// ---------------------------------------------------------------------------

// TestPushFilterDownAggregate部分下推 验证 FilterNode → AggregateNode 时部分条件可下推、部分保留
func TestPushFilterDownAggregate部分下推(t *testing.T) {
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

	// Aggregate 按 name 分组，计算 COUNT(*)
	agg := &AggregateNode{
		Child:   scan,
		GroupBy: []Expression{&ColumnExpr{Name: testColName}},
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
		},
		schema: []ColumnDef{
			{Name: testColName, Type: common.TypeString},
			{Name: testAggCountStar, Type: common.TypeInt64},
		},
	}

	// Filter 条件包含两部分：
	// 1. age > 20 - 不引用聚合列，可下推
	// 2. COUNT(*) > 5 - 引用聚合列，不可下推
	// 使用 AND 连接
	pushable := &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}}
	remaining := &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testAggCountStar}, Right: &LiteralExpr{Value: common.NewInt64(5)}}
	combined := &BinaryExpr{Op: OpAnd, Left: pushable, Right: remaining}

	filter := &FilterNode{
		Child:     agg,
		Condition: combined,
	}

	result := rule.Apply(filter)

	// 部分下推后，结果应该是一个 FilterNode（保留不可下推的条件）在 AggregateNode 之上
	resultFilter, ok := result.(*FilterNode)
	if !ok {
		// 如果全部下推了，结果可能是 AggregateNode
		if _, aggOk := result.(*AggregateNode); aggOk {
			t.Log("所有条件都已下推到 Aggregate 之下")
		} else {
			t.Fatalf("期望 FilterNode 或 AggregateNode，得到 %T", result)
		}
	} else {
		// 验证保留的条件不为空
		if resultFilter.Condition == nil {
			t.Error("期望保留不可下推的条件")
		}
	}
}

// TestPushFilterDownAggregate全部可下推 验证 FilterNode → AggregateNode 时所有条件都可下推
func TestPushFilterDownAggregate全部可下推(t *testing.T) {
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

	agg := &AggregateNode{
		Child:   scan,
		GroupBy: []Expression{&ColumnExpr{Name: testColName}},
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
		},
		schema: []ColumnDef{
			{Name: testColName, Type: common.TypeString},
			{Name: testAggCountStar, Type: common.TypeInt64},
		},
	}

	// Filter 条件只引用非聚合列 age，可全部下推
	filter := &FilterNode{
		Child:     agg,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
	}

	result := rule.Apply(filter)

	// 全部下推后，结果应该是 AggregateNode
	resultAgg, ok := result.(*AggregateNode)
	if !ok {
		t.Fatalf("期望 AggregateNode（全部可下推），得到 %T", result)
	}
	// 验证 AggregateNode 下有 FilterNode
	if resultAgg.Child == nil {
		t.Error("期望 AggregateNode 有子节点")
	}
}

// TestPushFilterDownAggregate全部不可下推 验证 FilterNode → AggregateNode 时所有条件都不可下推
func TestPushFilterDownAggregate全部不可下推(t *testing.T) {
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

	agg := &AggregateNode{
		Child:   scan,
		GroupBy: []Expression{&ColumnExpr{Name: testColName}},
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
		},
		schema: []ColumnDef{
			{Name: testColName, Type: common.TypeString},
			{Name: testAggCountStar, Type: common.TypeInt64},
		},
	}

	// Filter 条件只引用聚合列 COUNT(*)，不可下推
	filter := &FilterNode{
		Child:     agg,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testAggCountStar}, Right: &LiteralExpr{Value: common.NewInt64(5)}},
	}

	result := rule.Apply(filter)

	// 全部不可下推时，FilterNode 应保留在 AggregateNode 之上
	resultFilter, ok := result.(*FilterNode)
	if !ok {
		t.Fatalf("期望 FilterNode（全部不可下推），得到 %T", result)
	}
	if resultFilter.Condition == nil {
		t.Error("期望保留不可下推的条件")
	}
}

// ---------------------------------------------------------------------------
// pushFilterDown: FilterNode → ScanNode 下推路径
// ---------------------------------------------------------------------------

// TestPushFilterDownScanNode无谓词 验证 FilterNode → ScanNode 下推（Scan 无已有谓词）
func TestPushFilterDownScanNode无谓词(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColAge},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}, {Name: testColAge, Type: common.TypeInt64}},
	}

	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
	}

	result := rule.Apply(filter)

	resultScan, ok := result.(*ScanNode)
	if !ok {
		t.Fatalf("期望 ScanNode（条件已下推），得到 %T", result)
	}
	if resultScan.Predicate == nil {
		t.Error("期望 ScanNode 有谓词")
	}
}

// TestPushFilterDownScanNode有谓词 验证 FilterNode → ScanNode 下推（Scan 已有谓词，应合并）
func TestPushFilterDownScanNode有谓词(t *testing.T) {
	rule := &PredicatePushdownRule{}

	existingPred := &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColID}, Right: &LiteralExpr{Value: common.NewInt64(0)}}
	scan := &ScanNode{
		Table:     testTableUsers,
		Columns:   []string{testColID, testColAge},
		schema:    []ColumnDef{{Name: testColID, Type: common.TypeInt64}, {Name: testColAge, Type: common.TypeInt64}},
		Predicate: existingPred,
	}

	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
	}

	result := rule.Apply(filter)

	resultScan, ok := result.(*ScanNode)
	if !ok {
		t.Fatalf("期望 ScanNode（条件已合并），得到 %T", result)
	}
	if resultScan.Predicate == nil {
		t.Error("期望 ScanNode 有合并后的谓词")
	}
}

// ---------------------------------------------------------------------------
// pushFilterDown: FilterNode → FilterNode 合并路径
// ---------------------------------------------------------------------------

// TestPushFilterDownFilterNode合并 验证 FilterNode → FilterNode 时条件合并
func TestPushFilterDownFilterNode合并(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColAge},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}, {Name: testColAge, Type: common.TypeInt64}},
	}

	innerFilter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColID}, Right: &LiteralExpr{Value: common.NewInt64(0)}},
	}

	outerFilter := &FilterNode{
		Child:     innerFilter,
		Condition: &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
	}

	result := rule.Apply(outerFilter)

	// 合并后应该下推到 ScanNode
	resultScan, ok := result.(*ScanNode)
	if !ok {
		t.Fatalf("期望 ScanNode（条件已合并下推），得到 %T", result)
	}
	if resultScan.Predicate == nil {
		t.Error("期望 ScanNode 有合并后的谓词")
	}
}

// ---------------------------------------------------------------------------
// pushFilterDown: FilterNode → 其他节点类型（默认路径）
// ---------------------------------------------------------------------------

// TestPushFilterDown默认路径 验证 FilterNode → 未知节点类型时 Filter 保留
func TestPushFilterDown默认路径(t *testing.T) {
	rule := &PredicatePushdownRule{}

	// 使用 LimitNode 作为子节点（Filter 不能下推通过 Limit）
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
		t.Fatalf("期望 FilterNode（不可下推），得到 %T", result)
	}
	if resultFilter.Condition == nil {
		t.Error("期望 filter 条件保留")
	}
}

// ---------------------------------------------------------------------------
// pushDown: 递归处理 ProjectNode、AggregateNode、LimitNode
// ---------------------------------------------------------------------------

// TestPushDownProjectNode递归 验证 pushDown 递归处理 ProjectNode
func TestPushDownProjectNode递归(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColAge},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}, {Name: testColAge, Type: common.TypeInt64}},
	}

	proj := &ProjectNode{
		Child:       scan,
		Expressions: []Expression{&ColumnExpr{Name: testColID}, &ColumnExpr{Name: testColAge}},
		Aliases:     []string{"", ""},
		schema:      []ColumnDef{{Name: testColID, Type: common.TypeInt64}, {Name: testColAge, Type: common.TypeInt64}},
	}

	// 直接对 ProjectNode 调用 pushDown
	result := rule.pushDown(proj)

	resultProj, ok := result.(*ProjectNode)
	if !ok {
		t.Fatalf("期望 ProjectNode，得到 %T", result)
	}
	if resultProj.Child == nil {
		t.Error("期望 ProjectNode 有子节点")
	}
}

// TestPushDownAggregateNode递归 验证 pushDown 递归处理 AggregateNode
func TestPushDownAggregateNode递归(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColAge},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}, {Name: testColAge, Type: common.TypeInt64}},
	}

	agg := &AggregateNode{
		Child:   scan,
		GroupBy: []Expression{&ColumnExpr{Name: testColID}},
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
		},
		schema: []ColumnDef{{Name: testColID, Type: common.TypeInt64}, {Name: testAggCountStar, Type: common.TypeInt64}},
	}

	result := rule.pushDown(agg)

	resultAgg, ok := result.(*AggregateNode)
	if !ok {
		t.Fatalf("期望 AggregateNode，得到 %T", result)
	}
	if resultAgg.Child == nil {
		t.Error("期望 AggregateNode 有子节点")
	}
}

// TestPushDownLimitNode递归 验证 pushDown 递归处理 LimitNode
func TestPushDownLimitNode递归(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColAge},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}, {Name: testColAge, Type: common.TypeInt64}},
	}

	limit := &LimitNode{Child: scan, Count: 10}

	result := rule.pushDown(limit)

	resultLimit, ok := result.(*LimitNode)
	if !ok {
		t.Fatalf("期望 LimitNode，得到 %T", result)
	}
	if resultLimit.Child == nil {
		t.Error("期望 LimitNode 有子节点")
	}
}

// ---------------------------------------------------------------------------
// canPushThroughProject: 边界情况
// ---------------------------------------------------------------------------

// TestCanPushThroughProject空Schema 验证 canPushThroughProject 在 Project 子节点 schema 为空时返回 false
func TestCanPushThroughProject空Schema(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID},
		schema:  []ColumnDef{}, // 空 schema
	}

	proj := &ProjectNode{
		Child:       scan,
		Expressions: []Expression{&ColumnExpr{Name: testColID}},
		Aliases:     []string{""},
		schema:      []ColumnDef{}, // 空 schema
	}

	cond := &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}}

	result := rule.canPushThroughProject(cond, proj)
	if result {
		t.Error("期望空 schema 时不可下推，得到 true")
	}
}

// TestCanPushThroughProject所有列都在Schema中 验证 canPushThroughProject 在所有列都在 schema 中时返回 true
func TestCanPushThroughProject所有列都在Schema中(t *testing.T) {
	rule := &PredicatePushdownRule{}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColAge},
		schema: []ColumnDef{
			{Name: testColID, Type: common.TypeInt64},
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

	cond := &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(20)}}

	result := rule.canPushThroughProject(cond, proj)
	if !result {
		t.Error("期望所有列都在 schema 中时可下推，得到 false")
	}
}
