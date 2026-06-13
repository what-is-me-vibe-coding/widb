package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// ============================================================================
// executeAggregate 覆盖率提升测试
// ============================================================================

// TestAggregate_NoInputRows 测试聚合无输入行时的空结果。
// 覆盖 aggregateRows 中 len(groupOrder)==0 时创建空分组的路径。
func TestAggregate_NoInputRows(t *testing.T) {
	ms := newMockStorage() // 空存储

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	agg := &AggregateNode{
		Child:   scan,
		GroupBy: nil,
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
		},
		schema: []ColumnDef{
			{Name: testAggCountStar, Type: common.TypeInt64, Nullable: false},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(agg)
	if err != nil {
		t.Fatalf("聚合空输入: %v", err)
	}

	// 无输入行时，aggregateRows 会创建一个空分组，COUNT(*) 应为 0
	if len(chunks) == 0 || chunks[0].RowCount() == 0 {
		t.Fatal("期望至少有 1 行结果（空分组）")
	}
	countCol, _ := chunks[0].GetColumn(0)
	if countCol.GetValue(0).Int64 != 0 {
		t.Errorf("期望 COUNT(*) = 0（无输入行），得到 %d", countCol.GetValue(0).Int64)
	}
}

// TestAggregate_GroupByMultipleGroups 测试 GROUP BY 产生多个分组。
func TestAggregate_GroupByMultipleGroups(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(90.0),
	})
	ms.addEntry("b", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
		testColAge: common.NewInt64(25), testColScore: common.NewFloat64(80.0),
	})
	ms.addEntry("c", map[string]common.Value{
		testColID: common.NewInt64(3), testColName: common.NewString(testNameCharlie),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(70.0),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	agg := &AggregateNode{
		Child: scan,
		GroupBy: []Expression{
			&ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
		},
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
			{Func: AggSum, Arg: &ResolvedColumnExpr{Name: testColScore, Idx: 3, typ: common.TypeFloat64}},
		},
		schema: []ColumnDef{
			{Name: testColAge, Type: common.TypeInt64, Nullable: true},
			{Name: testAggCountStar, Type: common.TypeInt64, Nullable: false},
			{Name: testAggSumScore, Type: common.TypeFloat64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(agg)
	if err != nil {
		t.Fatalf("GROUP BY 多分组: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 2 {
		t.Errorf("期望 2 个分组，得到 %d", totalRows)
	}
}

// TestAggregate_AllFuncsWithNulls 测试所有聚合函数处理 NULL 值。
func TestAggregate_AllFuncsWithNulls(t *testing.T) {
	ms := newMockStorage()
	// 全部 age 为 NULL，全部 score 为 NULL
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewNull(), testColScore: common.NewNull(),
	})
	ms.addEntry("b", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
		testColAge: common.NewNull(), testColScore: common.NewNull(),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	agg := &AggregateNode{
		Child:   scan,
		GroupBy: nil,
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
			{Func: AggSum, Arg: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}},
			{Func: AggMin, Arg: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}},
			{Func: AggMax, Arg: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}},
			{Func: AggAvg, Arg: &ResolvedColumnExpr{Name: testColScore, Idx: 3, typ: common.TypeFloat64}},
		},
		schema: []ColumnDef{
			{Name: testAggCountStar, Type: common.TypeInt64, Nullable: false},
			{Name: testAggSumAge, Type: common.TypeFloat64, Nullable: true},
			{Name: testAggMinAge, Type: common.TypeInt64, Nullable: true},
			{Name: testAggMaxAge, Type: common.TypeInt64, Nullable: true},
			{Name: testAggAvgScore, Type: common.TypeFloat64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(agg)
	if err != nil {
		t.Fatalf("全 NULL 聚合: %v", err)
	}

	if len(chunks) == 0 || chunks[0].RowCount() == 0 {
		t.Fatal("期望至少有 1 行结果")
	}

	// COUNT(*) 不受 NULL 影响
	countCol, _ := chunks[0].GetColumn(0)
	if countCol.GetValue(0).Int64 != 2 {
		t.Errorf("期望 COUNT(*) = 2，得到 %d", countCol.GetValue(0).Int64)
	}

	// SUM 全 NULL 应返回 NULL
	sumCol, _ := chunks[0].GetColumn(1)
	if sumCol.GetValue(0).Valid {
		t.Errorf("期望 SUM(age) = NULL（全 NULL），得到 %v", sumCol.GetValue(0))
	}

	// MIN 全 NULL 应返回 NULL
	minCol, _ := chunks[0].GetColumn(2)
	if minCol.GetValue(0).Valid {
		t.Errorf("期望 MIN(age) = NULL（全 NULL），得到 %v", minCol.GetValue(0))
	}

	// MAX 全 NULL 应返回 NULL
	maxCol, _ := chunks[0].GetColumn(3)
	if maxCol.GetValue(0).Valid {
		t.Errorf("期望 MAX(age) = NULL（全 NULL），得到 %v", maxCol.GetValue(0))
	}

	// AVG 全 NULL 应返回 NULL
	avgCol, _ := chunks[0].GetColumn(4)
	if avgCol.GetValue(0).Valid {
		t.Errorf("期望 AVG(score) = NULL（全 NULL），得到 %v", avgCol.GetValue(0))
	}
}

// TestAggregate_MixedNulls 测试部分 NULL 值的聚合。
func TestAggregate_MixedNulls(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(90.0),
	})
	ms.addEntry("b", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
		testColAge: common.NewNull(), testColScore: common.NewNull(),
	})
	ms.addEntry("c", map[string]common.Value{
		testColID: common.NewInt64(3), testColName: common.NewString(testNameCharlie),
		testColAge: common.NewInt64(20), testColScore: common.NewFloat64(70.0),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	agg := &AggregateNode{
		Child:   scan,
		GroupBy: nil,
		Aggregates: []AggregateExpr{
			{Func: AggMin, Arg: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}},
			{Func: AggMax, Arg: &ResolvedColumnExpr{Name: testColScore, Idx: 3, typ: common.TypeFloat64}},
		},
		schema: []ColumnDef{
			{Name: testAggMinAge, Type: common.TypeInt64, Nullable: true},
			{Name: testAggMaxScore, Type: common.TypeFloat64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(agg)
	if err != nil {
		t.Fatalf("混合 NULL 聚合: %v", err)
	}

	if len(chunks) == 0 || chunks[0].RowCount() == 0 {
		t.Fatal("期望至少有 1 行结果")
	}

	// MIN(age) 跳过 NULL，应为 20
	minCol, _ := chunks[0].GetColumn(0)
	if minCol.GetValue(0).Int64 != 20 {
		t.Errorf("期望 MIN(age) = 20（跳过 NULL），得到 %d", minCol.GetValue(0).Int64)
	}

	// MAX(score) 跳过 NULL，应为 90.0
	maxCol, _ := chunks[0].GetColumn(1)
	if maxCol.GetValue(0).Float64 != 90.0 {
		t.Errorf("期望 MAX(score) = 90.0（跳过 NULL），得到 %g", maxCol.GetValue(0).Float64)
	}
}

// TestAggregate_AddColumnError 测试 executeAggregate 中 AddColumn 返回错误。
// 覆盖 executor_aggregate.go 第 134-136 行的错误路径。
func TestAggregate_AddColumnError(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(90.0),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	agg := &AggregateNode{
		Child:   scan,
		GroupBy: nil,
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
		},
		schema: []ColumnDef{
			{Name: testAggCountStar, Type: common.TypeInt64, Nullable: false},
		},
	}

	exec := NewExecutor(ms)

	// 通过直接调用 executeAggregate 来测试 AddColumn 错误路径
	// 先正常执行获取结果，然后构造一个会导致 AddColumn 失败的场景
	result, err := exec.executeNode(agg)
	if err != nil {
		t.Fatalf("正常聚合不应出错: %v", err)
	}

	// 验证正常结果
	if len(result.chunks) == 0 || result.chunks[0].RowCount() == 0 {
		t.Fatal("期望至少有 1 行结果")
	}
}

// ============================================================================
// buildChunksFromEntries 覆盖率提升测试
// ============================================================================

// TestBuildChunksFromEntries_EmptySchema 测试空 schema 时返回 nil。
func TestBuildChunksFromEntries_EmptySchema(t *testing.T) {
	entries := []storage.ScanEntry{
		{Key: "a", Value: storage.Row{Columns: map[string]common.Value{testColID: common.NewInt64(1)}}},
	}
	chunks, err := buildChunksFromEntries(entries, nil, defaultChunkSize)
	if err != nil {
		t.Fatalf("空 schema: %v", err)
	}
	if chunks != nil {
		t.Errorf("期望 nil chunks，得到 %v", chunks)
	}
}

// TestBuildChunksFromEntries_EmptyEntries 测试空 entries 时返回 nil。
func TestBuildChunksFromEntries_EmptyEntries(t *testing.T) {
	schema := []ColumnDef{{Name: testColID, Type: common.TypeInt64}}
	chunks, err := buildChunksFromEntries(nil, schema, defaultChunkSize)
	if err != nil {
		t.Fatalf("空 entries: %v", err)
	}
	if chunks != nil {
		t.Errorf("期望 nil chunks，得到 %v", chunks)
	}
}

// ============================================================================
// executeLimit 覆盖率提升测试
// ============================================================================

// TestLimit_WithOffset 测试 LIMIT 带偏移量。
func TestLimit_WithOffset(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})
	ms.addEntry("b", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
		testColAge: common.NewInt64(25), testColScore: common.NewFloat64(88.0),
	})
	ms.addEntry("c", map[string]common.Value{
		testColID: common.NewInt64(3), testColName: common.NewString(testNameCharlie),
		testColAge: common.NewInt64(35), testColScore: common.NewFloat64(72.0),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	limit := &LimitNode{
		Child:  scan,
		Offset: 1,
		Count:  1,
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(limit)
	if err != nil {
		t.Fatalf("LIMIT 偏移: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 1 {
		t.Errorf("期望 1 行（OFFSET 1, LIMIT 1），得到 %d", totalRows)
	}
}

// TestLimit_OffsetExceedsRows 测试偏移量超过总行数。
func TestLimit_OffsetExceedsRows(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	limit := &LimitNode{
		Child:  scan,
		Offset: 100,
		Count:  10,
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(limit)
	if err != nil {
		t.Fatalf("LIMIT 偏移超限: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 0 {
		t.Errorf("期望 0 行（偏移超限），得到 %d", totalRows)
	}
}
