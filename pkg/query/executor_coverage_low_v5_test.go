package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// ---------------------------------------------------------------------------
// executeScan: 空结果、谓词过滤全部行（87.5% → >90%）
// ---------------------------------------------------------------------------

// TestExecuteScan_EmptyScanResultsV5 测试 executeScan 在存储引擎返回空结果时的行为。
func TestExecuteScan_EmptyScanResultsV5(t *testing.T) {
	ms := newMockStorage() // 无数据

	schema := buildTestSchema()
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  schema,
	}

	exec := NewExecutor(ms)
	result, err := exec.executeScan(scan)
	if err != nil {
		t.Fatalf("executeScan 空结果失败: %v", err)
	}
	// 空 entries 路径返回 nil chunks 和有效 schema
	if result.chunks != nil {
		t.Errorf("期望 nil chunks，得到 %d 个 chunks", len(result.chunks))
	}
	if len(result.schema) != len(schema) {
		t.Errorf("期望 schema 长度 %d，得到 %d", len(schema), len(result.schema))
	}
}

// TestExecuteScan_PredicateFiltersAllRowsV5 测试 executeScan 谓词过滤掉所有行时的行为。
func TestExecuteScan_PredicateFiltersAllRowsV5(t *testing.T) {
	ms := newMockStorage()
	// 添加数据
	ms.addEntry("1", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})
	ms.addEntry("2", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
		testColAge: common.NewInt64(25), testColScore: common.NewFloat64(88.0),
	})

	schema := buildTestSchema()
	// 使用一个不可能满足的谓词（age > 999）
	scan := &ScanNode{
		Table:     testTableUsers,
		Columns:   []string{testColID, testColName, testColAge, testColScore},
		Predicate: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(999)}},
		schema:    schema,
	}

	exec := NewExecutor(ms)
	result, err := exec.executeScan(scan)
	if err != nil {
		t.Fatalf("executeScan 谓词过滤全部行失败: %v", err)
	}
	// 谓词过滤后 entries 为空，应返回 nil chunks
	if result.chunks != nil {
		t.Errorf("期望 nil chunks（谓词过滤全部行），得到 %d 个 chunks", len(result.chunks))
	}
}

// TestExecuteScan_WithPredicateMatchesSomeRowsV5 测试 executeScan 谓词过滤部分行。
func TestExecuteScan_WithPredicateMatchesSomeRowsV5(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("1", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})
	ms.addEntry("2", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
		testColAge: common.NewInt64(25), testColScore: common.NewFloat64(88.0),
	})

	schema := buildTestSchema()
	// age > 27 只匹配 alice (age=30)
	scan := &ScanNode{
		Table:     testTableUsers,
		Columns:   []string{testColID, testColName, testColAge, testColScore},
		Predicate: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(27)}},
		schema:    schema,
	}

	exec := NewExecutor(ms)
	result, err := exec.executeScan(scan)
	if err != nil {
		t.Fatalf("executeScan 谓词过滤部分行失败: %v", err)
	}
	if result.chunks == nil {
		t.Fatal("期望非 nil chunks，得到 nil")
	}
	totalRows := countRows(result.chunks)
	if totalRows != 1 {
		t.Errorf("期望 1 行，得到 %d", totalRows)
	}
}

// ---------------------------------------------------------------------------
// executeAggregate: 空输入、GROUP BY 多组（86.7% → >90%）
// ---------------------------------------------------------------------------

// TestExecuteAggregate_EmptyInputV5 测试 executeAggregate 在无输入数据时的行为。
// 空输入时 aggregateRows 创建默认空分组，聚合结果为 NULL。
func TestExecuteAggregate_EmptyInputV5(t *testing.T) {
	ms := newMockStorage() // 无数据

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}},
	}

	agg := &AggregateNode{
		Child: scan,
		Aggregates: []AggregateExpr{
			{Func: AggSum, Arg: &ResolvedColumnExpr{Name: testColID, Idx: 0, typ: common.TypeInt64}},
		},
		schema: []ColumnDef{{Name: "sum_id", Type: common.TypeFloat64}},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(agg)
	if err != nil {
		t.Fatalf("executeAggregate 空输入失败: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatal("期望至少 1 个 chunk（空输入聚合也应返回结果）")
	}

	// 空输入时 SUM 应该返回 NULL
	col, _ := chunks[0].GetColumn(0)
	val := col.GetValue(0)
	if val.Valid {
		t.Errorf("期望 NULL（空输入 SUM），得到 %v", val)
	}
}

// TestExecuteAggregate_GroupByMultipleGroupsV5 测试 executeAggregate GROUP BY 产生多个分组。
func TestExecuteAggregate_GroupByMultipleGroupsV5(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("1", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})
	ms.addEntry("2", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
		testColAge: common.NewInt64(25), testColScore: common.NewFloat64(88.0),
	})
	ms.addEntry("3", map[string]common.Value{
		testColID: common.NewInt64(3), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(35), testColScore: common.NewFloat64(92.0),
	})

	scanSchema := []ColumnDef{
		{Name: testColID, Type: common.TypeInt64, Nullable: false},
		{Name: testColName, Type: common.TypeString, Nullable: true},
		{Name: testColAge, Type: common.TypeInt64, Nullable: true},
		{Name: testColScore, Type: common.TypeFloat64, Nullable: true},
	}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  scanSchema,
	}

	// GROUP BY name, COUNT(*)
	agg := &AggregateNode{
		Child: scan,
		GroupBy: []Expression{
			&ResolvedColumnExpr{Name: testColName, Idx: 1, typ: common.TypeString},
		},
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
		},
		schema: []ColumnDef{
			{Name: testColName, Type: common.TypeString, Nullable: true},
			{Name: testAggCountStar, Type: common.TypeInt64, Nullable: false},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(agg)
	if err != nil {
		t.Fatalf("executeAggregate GROUP BY 多组失败: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatal("期望至少 1 个 chunk")
	}

	// 应产生 2 个分组（alice 和 bob）
	totalRows := countRows(chunks)
	if totalRows != 2 {
		t.Errorf("期望 2 个分组，得到 %d 行", totalRows)
	}
}

// TestExecuteAggregate_SingleGroupNoGroupByV5 测试 executeAggregate 无 GROUP BY 时的单组聚合。
func TestExecuteAggregate_SingleGroupNoGroupByV5(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("1", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})
	ms.addEntry("2", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
		testColAge: common.NewInt64(25), testColScore: common.NewFloat64(88.0),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	// 无 GROUP BY，仅 COUNT(*)
	agg := &AggregateNode{
		Child: scan,
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
		t.Fatalf("executeAggregate 无 GROUP BY 失败: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatal("期望至少 1 个 chunk")
	}

	col, _ := chunks[0].GetColumn(0)
	count := col.GetValue(0).Int64
	if count != 2 {
		t.Errorf("COUNT(*) = %d，期望 2", count)
	}
}

// ---------------------------------------------------------------------------
// sliceChunk: startRow == endRow 空结果、完整范围（87.5% → >90%）
// ---------------------------------------------------------------------------

// TestSliceChunk_StartEqualsEndV5 测试 sliceChunk 在 startRow == endRow 时返回空结果。
func TestSliceChunk_StartEqualsEndV5(t *testing.T) {
	chunk := storage.NewChunk(defaultChunkSize)
	col := storage.NewColumnVector(0, common.TypeInt64, 5)
	for i := int64(0); i < 5; i++ {
		_ = col.Append(common.NewInt64(i))
	}
	_ = chunk.AddColumn(col)

	result, err := sliceChunk(chunk, 2, 2)
	if err != nil {
		t.Fatalf("sliceChunk 空切片失败: %v", err)
	}
	if result.RowCount() != 0 {
		t.Errorf("期望 0 行（start == end），得到 %d", result.RowCount())
	}
}

// TestSliceChunk_FullRangeV5 测试 sliceChunk 切片完整范围。
func TestSliceChunk_FullRangeV5(t *testing.T) {
	chunk := storage.NewChunk(defaultChunkSize)
	col1 := storage.NewColumnVector(0, common.TypeInt64, 3)
	col2 := storage.NewColumnVector(1, common.TypeString, 3)
	for i := int64(0); i < 3; i++ {
		_ = col1.Append(common.NewInt64(i))
		_ = col2.Append(common.NewString(testNameAlice))
	}
	_ = chunk.AddColumn(col1)
	_ = chunk.AddColumn(col2)

	result, err := sliceChunk(chunk, 0, 3)
	if err != nil {
		t.Fatalf("sliceChunk 完整范围切片失败: %v", err)
	}
	if result.RowCount() != 3 {
		t.Errorf("期望 3 行，得到 %d", result.RowCount())
	}
	if result.ColumnCount() != 2 {
		t.Errorf("期望 2 列，得到 %d", result.ColumnCount())
	}

	// 验证数据正确
	col0, _ := result.GetColumn(0)
	val := col0.GetValue(0)
	if val.Int64 != 0 {
		t.Errorf("期望第一行值为 0，得到 %d", val.Int64)
	}
	val = col0.GetValue(2)
	if val.Int64 != 2 {
		t.Errorf("期望第三行值为 2，得到 %d", val.Int64)
	}
}

// TestSliceChunk_PartialRangeV5 测试 sliceChunk 部分范围切片。
func TestSliceChunk_PartialRangeV5(t *testing.T) {
	chunk := storage.NewChunk(defaultChunkSize)
	col := storage.NewColumnVector(0, common.TypeFloat64, 5)
	for i := int64(0); i < 5; i++ {
		_ = col.Append(common.NewFloat64(float64(i) * 1.5))
	}
	_ = chunk.AddColumn(col)

	result, err := sliceChunk(chunk, 1, 4)
	if err != nil {
		t.Fatalf("sliceChunk 部分范围切片失败: %v", err)
	}
	if result.RowCount() != 3 {
		t.Errorf("期望 3 行，得到 %d", result.RowCount())
	}

	col0, _ := result.GetColumn(0)
	val := col0.GetValue(0)
	if val.Float64 != 1.5 {
		t.Errorf("期望第一行值为 1.5，得到 %g", val.Float64)
	}
	val = col0.GetValue(2)
	if val.Float64 != 4.5 {
		t.Errorf("期望第三行值为 4.5，得到 %g", val.Float64)
	}
}

// ---------------------------------------------------------------------------
// executeAggregate: 多种聚合函数（AVG, MIN, MAX）与空输入
// ---------------------------------------------------------------------------

// TestExecuteAggregate_AvgEmptyInputV5 测试 AVG 聚合在空输入时返回 NULL。
func TestExecuteAggregate_AvgEmptyInputV5(t *testing.T) {
	ms := newMockStorage()

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColScore},
		schema:  []ColumnDef{{Name: testColScore, Type: common.TypeFloat64}},
	}

	agg := &AggregateNode{
		Child: scan,
		Aggregates: []AggregateExpr{
			{Func: AggAvg, Arg: &ResolvedColumnExpr{Name: testColScore, Idx: 0, typ: common.TypeFloat64}},
		},
		schema: []ColumnDef{{Name: "avg_score", Type: common.TypeFloat64}},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(agg)
	if err != nil {
		t.Fatalf("executeAggregate AVG 空输入失败: %v", err)
	}

	col, _ := chunks[0].GetColumn(0)
	val := col.GetValue(0)
	if val.Valid {
		t.Errorf("期望 NULL（空输入 AVG），得到 %v", val)
	}
}

// TestExecuteAggregate_MinMaxWithGroupByV5 测试 MIN/MAX 聚合与 GROUP BY 一起使用。
func TestExecuteAggregate_MinMaxWithGroupByV5(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("1", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})
	ms.addEntry("2", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
		testColAge: common.NewInt64(25), testColScore: common.NewFloat64(88.0),
	})
	ms.addEntry("3", map[string]common.Value{
		testColID: common.NewInt64(3), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(35), testColScore: common.NewFloat64(72.0),
	})

	scanSchema := []ColumnDef{
		{Name: testColID, Type: common.TypeInt64, Nullable: false},
		{Name: testColName, Type: common.TypeString, Nullable: true},
		{Name: testColAge, Type: common.TypeInt64, Nullable: true},
		{Name: testColScore, Type: common.TypeFloat64, Nullable: true},
	}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  scanSchema,
	}

	// GROUP BY name, MIN(age), MAX(age)
	agg := &AggregateNode{
		Child: scan,
		GroupBy: []Expression{
			&ResolvedColumnExpr{Name: testColName, Idx: 1, typ: common.TypeString},
		},
		Aggregates: []AggregateExpr{
			{Func: AggMin, Arg: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}},
			{Func: AggMax, Arg: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}},
		},
		schema: []ColumnDef{
			{Name: testColName, Type: common.TypeString, Nullable: true},
			{Name: "min_age", Type: common.TypeInt64, Nullable: true},
			{Name: "max_age", Type: common.TypeInt64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(agg)
	if err != nil {
		t.Fatalf("executeAggregate MIN/MAX GROUP BY 失败: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 2 {
		t.Errorf("期望 2 个分组，得到 %d 行", totalRows)
	}
}
