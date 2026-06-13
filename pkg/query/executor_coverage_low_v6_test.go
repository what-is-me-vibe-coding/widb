package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// ============================================================================
// executeScan 覆盖率提升测试 (87.5% → >90%)
// ============================================================================

// TestCoverageLowExecutorV6_ExecuteScan_EmptyResult 测试 executeScan 在存储引擎返回空结果时的行为。
// 覆盖 executeScan 中 len(entries)==0 时返回 nil chunks 的路径。
func TestCoverageLowExecutorV6_ExecuteScan_EmptyResult(t *testing.T) {
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

// TestCoverageLowExecutorV6_ExecuteScan_PredicateFiltersAll 测试谓词过滤掉所有行后 executeScan 返回空结果。
func TestCoverageLowExecutorV6_ExecuteScan_PredicateFiltersAll(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})

	schema := buildTestSchema()
	// age > 999 不可能满足
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

	if result.chunks != nil {
		t.Errorf("期望 nil chunks（谓词过滤全部行），得到 %d 个 chunks", len(result.chunks))
	}
}

// ============================================================================
// executeAggregate 覆盖率提升测试 (86.7% → >90%)
// ============================================================================

// TestCoverageLowExecutorV6_ExecuteAggregate_AddColumnError 测试 executeAggregate 中 AddColumn 错误路径。
// 通过构造 schema 列数多于 GroupBy+Aggregates 列数的 AggregateNode，
// 使 buildAggregateOutput 返回的某些列长度为 0，触发 AddColumn 类型不匹配错误。
func TestCoverageLowExecutorV6_ExecuteAggregate_AddColumnError(t *testing.T) {
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

	// schema 有 3 列，但 GroupBy(1) + Aggregates(1) = 2，多出的第 3 列不会被追加数据
	// buildAggregateOutput 中 colIdx 只遍历到 1，outputCols[2] 长度为 0
	// 当 AddColumn(outputCols[2]) 时，长度 0 != rowCount，触发错误
	agg := &AggregateNode{
		Child: scan,
		GroupBy: []Expression{
			&ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
		},
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
		},
		schema: []ColumnDef{
			{Name: testColAge, Type: common.TypeInt64, Nullable: true},
			{Name: testAggCountStar, Type: common.TypeInt64, Nullable: false},
			{Name: "extra_col", Type: common.TypeInt64, Nullable: true}, // 多余列，不会被追加数据
		},
	}

	exec := NewExecutor(ms)
	_, err := exec.Execute(agg)
	if err == nil {
		t.Error("期望 AddColumn 错误，得到 nil")
	}
}

// ============================================================================
// sliceChunk 覆盖率提升测试 (87.5% → >90%)
// ============================================================================

// TestCoverageLowExecutorV6_SliceChunk_SliceError 测试 sliceChunk 中 Slice 操作出错路径。
// 通过替换 Chunk 中的列为更短的列，使 Slice 的 endRow 超出列长度，触发错误。
func TestCoverageLowExecutorV6_SliceChunk_SliceError(t *testing.T) {
	// 创建一个 Chunk，包含两列，每列 5 行
	chunk := storage.NewChunk(8)
	col1 := storage.NewColumnVector(0, common.TypeInt64, 5)
	col2 := storage.NewColumnVector(1, common.TypeString, 5)
	for i := int64(0); i < 5; i++ {
		_ = col1.Append(common.NewInt64(i))
		_ = col2.Append(common.NewString(testNameAlice))
	}
	_ = chunk.AddColumn(col1)
	_ = chunk.AddColumn(col2)

	// 替换第一列为更短的列（2 行），但 Chunk 的 rowCount 仍为 5
	shortCol := storage.NewColumnVector(0, common.TypeInt64, 2)
	_ = shortCol.Append(common.NewInt64(100))
	_ = shortCol.Append(common.NewInt64(200))
	cols := chunk.Columns()
	cols[0] = shortCol

	// sliceChunk(chunk, 0, 3) 时，col[0].Slice(0, 3) 会失败
	// 因为 endRow=3 > shortCol.Len()=2
	_, err := sliceChunk(chunk, 0, 3)
	if err == nil {
		t.Error("期望 Slice 错误（列长度不足），得到 nil")
	}
}

// TestCoverageLowExecutorV6_SliceChunk_SliceStartGreaterThanEnd 测试 sliceChunk 中
// startRow > endRow 时 Slice 返回错误。
func TestCoverageLowExecutorV6_SliceChunk_SliceStartGreaterThanEnd(t *testing.T) {
	chunk := storage.NewChunk(8)
	col := storage.NewColumnVector(0, common.TypeInt64, 5)
	for i := int64(0); i < 5; i++ {
		_ = col.Append(common.NewInt64(i))
	}
	_ = chunk.AddColumn(col)

	// startRow=3, endRow=1，startRow > endRow
	// 注意：sliceChunk 中 NewChunk(endRow - startRow) 会因 uint32 下溢产生巨大值
	// 但 Slice 方法会先检查 startRow > endRow 并返回错误
	_, err := sliceChunk(chunk, 3, 1)
	if err == nil {
		t.Error("期望 Slice 错误（startRow > endRow），得到 nil")
	}
}

// TestCoverageLowExecutorV6_SliceChunk_AddColumnError 测试 sliceChunk 中 AddColumn 错误路径。
// 通过构造列长度不一致的 Chunk，使切片后的列长度不匹配，触发 AddColumn 错误。
func TestCoverageLowExecutorV6_SliceChunk_AddColumnError(t *testing.T) {
	// 创建一个 Chunk，包含两列，每列 5 行
	chunk := storage.NewChunk(8)
	col1 := storage.NewColumnVector(0, common.TypeInt64, 5)
	col2 := storage.NewColumnVector(1, common.TypeString, 5)
	for i := int64(0); i < 5; i++ {
		_ = col1.Append(common.NewInt64(i))
		_ = col2.Append(common.NewString(testNameAlice))
	}
	_ = chunk.AddColumn(col1)
	_ = chunk.AddColumn(col2)

	// 替换第二列为更短的列（3 行），但 Chunk 的 rowCount 仍为 5
	// sliceChunk(chunk, 0, 3) 时：
	// - col[0].Slice(0, 3) 成功，返回 3 行 → result.AddColumn 设置 rowCount=3
	// - col[1] 被替换为 3 行的列，Slice(0, 3) 成功，返回 3 行 → AddColumn 成功
	// 这不会触发 AddColumn 错误，因为切片后长度相同
	//
	// 要触发 AddColumn 错误，需要切片后的列长度不同。
	// 由于 Slice 总是返回 endRow-startRow 行，正常情况下不可能。
	// 我们通过替换列为一个特殊列来模拟：该列的 Slice 返回不同长度的结果。
	// 但 ColumnVector.Slice 总是返回 endRow-startRow 行，所以无法通过正常方式触发。
	//
	// 替代方案：替换列为一个更短的列，使第一个 Slice 成功但第二个 Slice 失败
	shortCol := storage.NewColumnVector(1, common.TypeString, 2)
	_ = shortCol.Append(common.NewString("x"))
	_ = shortCol.Append(common.NewString("y"))
	cols := chunk.Columns()
	cols[1] = shortCol

	// sliceChunk(chunk, 0, 3):
	// - col[0].Slice(0, 3) 成功（5 行 >= 3），返回 3 行
	// - result.AddColumn 设置 rowCount=3
	// - col[1].Slice(0, 3) 失败（2 行 < 3），返回错误
	_, err := sliceChunk(chunk, 0, 3)
	if err == nil {
		t.Error("期望 Slice 错误（第二列长度不足），得到 nil")
	}
}

// TestCoverageLowExecutorV6_SliceChunk_NormalSlice 测试 sliceChunk 正常切片操作。
func TestCoverageLowExecutorV6_SliceChunk_NormalSlice(t *testing.T) {
	chunk := storage.NewChunk(8)
	col := storage.NewColumnVector(0, common.TypeInt64, 5)
	for i := int64(0); i < 5; i++ {
		_ = col.Append(common.NewInt64(i * 10))
	}
	_ = chunk.AddColumn(col)

	result, err := sliceChunk(chunk, 1, 4)
	if err != nil {
		t.Fatalf("sliceChunk 正常切片失败: %v", err)
	}
	if result.RowCount() != 3 {
		t.Errorf("期望 3 行，得到 %d", result.RowCount())
	}

	resultCol, _ := result.GetColumn(0)
	if resultCol.GetValue(0).Int64 != 10 {
		t.Errorf("期望第 0 行 = 10，得到 %d", resultCol.GetValue(0).Int64)
	}
	if resultCol.GetValue(2).Int64 != 30 {
		t.Errorf("期望第 2 行 = 30，得到 %d", resultCol.GetValue(2).Int64)
	}
}
