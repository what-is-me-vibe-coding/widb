package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// ==================== executeFilter 覆盖率测试 ====================

// TestFilterNoMatchAllRows 测试过滤条件不匹配任何行的场景。
// 验证返回空结果（无 chunks）。
func TestFilterNoMatchAllRows(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(20), testColScore: common.NewFloat64(50.0),
	})
	ms.addEntry("b", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
		testColAge: common.NewInt64(25), testColScore: common.NewFloat64(60.0),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	// age > 1000 不匹配任何行
	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(1000)}},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("filter no match: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 0 {
		t.Errorf("expected 0 rows when no rows match filter, got %d", totalRows)
	}
}

// TestFilterEvalExprError 测试过滤条件中表达式求值出错时的行为。
// 使用无效的列引用触发 evalExpr 错误，验证 filterChunk 中 continue 逻辑。
func TestFilterEvalExprError(t *testing.T) {
	// 直接构造 filterChunk 的输入参数来测试 evalExpr 错误路径
	inputSchema := []ColumnDef{
		{Name: testColID, Type: common.TypeInt64, Nullable: false},
		{Name: testColAge, Type: common.TypeInt64, Nullable: true},
	}

	// 构建一个包含数据的 chunk
	chunk := storage.NewChunk(defaultChunkSize)
	col0 := storage.NewColumnVector(0, common.TypeInt64, 2)
	_ = col0.Append(common.NewInt64(1))
	_ = col0.Append(common.NewInt64(2))
	_ = chunk.AddColumn(col0)

	col1 := storage.NewColumnVector(1, common.TypeInt64, 2)
	_ = col1.Append(common.NewInt64(30))
	_ = col1.Append(common.NewInt64(25))
	_ = chunk.AddColumn(col1)

	colIdxMap := buildColIdxMapFromSchema(inputSchema)

	// 使用一个会导致 evalExpr 错误的条件：FuncExpr 在 evalExpr 中会返回错误
	cond := &FuncExpr{Name: testFuncUnknownFunc, Args: nil}

	output, err := filterChunk(chunk, cond, inputSchema, colIdxMap)
	if err != nil {
		// filterChunk 中 evalExpr 出错会 continue，不应返回错误
		t.Fatalf("filterChunk should not return error for evalExpr errors, got: %v", err)
	}

	// 所有行都因 evalExpr 错误被跳过，结果应为空
	if output.RowCount() != 0 {
		t.Errorf("expected 0 rows when all rows have evalExpr errors, got %d", output.RowCount())
	}
}

// TestFilterOnEmptyChunk 测试对空 chunk 执行过滤。
// 验证返回空 chunk 且不报错。
func TestFilterOnEmptyChunk(t *testing.T) {
	inputSchema := []ColumnDef{
		{Name: testColID, Type: common.TypeInt64, Nullable: false},
	}

	// 构建一个空 chunk（0 行）
	emptyChunk := storage.NewChunk(defaultChunkSize)
	col0 := storage.NewColumnVector(0, common.TypeInt64, 0)
	_ = emptyChunk.AddColumn(col0)

	colIdxMap := buildColIdxMapFromSchema(inputSchema)
	cond := &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColID, Idx: 0, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(0)}}

	output, err := filterChunk(emptyChunk, cond, inputSchema, colIdxMap)
	if err != nil {
		t.Fatalf("filterChunk on empty chunk: %v", err)
	}

	if output.RowCount() != 0 {
		t.Errorf("expected 0 rows from empty chunk filter, got %d", output.RowCount())
	}
}

// ==================== executeLimit 覆盖率测试 ====================

// TestLimitOffsetLargerThanTotalRows 测试 offset 超过总行数时返回空结果。
func TestLimitOffsetLargerThanTotalRows(t *testing.T) {
	ms := newMockStorage()
	for i := 0; i < 5; i++ {
		key := string(rune('a' + i))
		ms.addEntry(key, map[string]common.Value{
			testColID:    common.NewInt64(int64(i)),
			testColName:  common.NewString(key),
			testColAge:   common.NewInt64(int64(20 + i)),
			testColScore: common.NewFloat64(float64(60 + i)),
		})
	}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	// offset=100 远超 5 行数据
	limit := &LimitNode{
		Child:  scan,
		Offset: 100,
		Count:  10,
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(limit)
	if err != nil {
		t.Fatalf("limit offset beyond data: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 0 {
		t.Errorf("expected 0 rows when offset > total rows, got %d", totalRows)
	}
}

// TestLimitCountZero 测试 LIMIT 0（返回 0 行）的场景。
func TestLimitCountZero(t *testing.T) {
	ms := newMockStorage()
	for i := 0; i < 5; i++ {
		key := string(rune('a' + i))
		ms.addEntry(key, map[string]common.Value{
			testColID:    common.NewInt64(int64(i)),
			testColName:  common.NewString(key),
			testColAge:   common.NewInt64(int64(20 + i)),
			testColScore: common.NewFloat64(float64(60 + i)),
		})
	}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	// LIMIT 0,0 应返回 0 行
	limit := &LimitNode{
		Child:  scan,
		Offset: 0,
		Count:  0,
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(limit)
	if err != nil {
		t.Fatalf("limit count=0: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 0 {
		t.Errorf("expected 0 rows when LIMIT 0, got %d", totalRows)
	}
}

// TestLimitSpanningMultipleChunks 测试 LIMIT 跨多个 chunk 的场景。
// 通过大量数据确保产生多个 chunk，然后验证 limit 正确截取。
func TestLimitSpanningMultipleChunks(t *testing.T) {
	ms := newMockStorage()
	// 添加足够多的数据以确保跨 chunk
	for i := 0; i < 50; i++ {
		key := fmtKey(i)
		ms.addEntry(key, map[string]common.Value{
			testColID:    common.NewInt64(int64(i)),
			testColName:  common.NewString(key),
			testColAge:   common.NewInt64(int64(20 + i%10)),
			testColScore: common.NewFloat64(float64(60 + i%40)),
		})
	}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	// 先用 offset=5 跳过前 5 行，再取 10 行
	limit := &LimitNode{
		Child:  scan,
		Offset: 5,
		Count:  10,
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(limit)
	if err != nil {
		t.Fatalf("limit spanning multiple chunks: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 10 {
		t.Errorf("expected 10 rows (LIMIT 5,10), got %d", totalRows)
	}

	// 验证返回的第一行是第 6 条数据（id=5）
	if len(chunks) > 0 && chunks[0].RowCount() > 0 {
		idCol, _ := chunks[0].GetColumn(0)
		firstVal := idCol.GetValue(0)
		if firstVal.Int64 != 5 {
			t.Errorf("expected first row id=5, got %d", firstVal.Int64)
		}
	}
}

// TestLimitOffsetAtChunkBoundary 测试 offset 恰好在 chunk 边界上的场景。
func TestLimitOffsetAtChunkBoundary(t *testing.T) {
	ms := newMockStorage()
	for i := 0; i < 20; i++ {
		key := fmtKey(i)
		ms.addEntry(key, map[string]common.Value{
			testColID:    common.NewInt64(int64(i)),
			testColName:  common.NewString(key),
			testColAge:   common.NewInt64(int64(20 + i)),
			testColScore: common.NewFloat64(float64(60 + i)),
		})
	}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	// offset=10 跳过前 10 行，取 5 行
	limit := &LimitNode{
		Child:  scan,
		Offset: 10,
		Count:  5,
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(limit)
	if err != nil {
		t.Fatalf("limit offset at chunk boundary: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 5 {
		t.Errorf("expected 5 rows (LIMIT 10,5), got %d", totalRows)
	}
}

// TestLimitOffsetZeroCountLarge 测试 offset=0 且 count 大于总行数的场景。
func TestLimitOffsetZeroCountLarge(t *testing.T) {
	ms := newMockStorage()
	for i := 0; i < 3; i++ {
		key := string(rune('a' + i))
		ms.addEntry(key, map[string]common.Value{
			testColID:    common.NewInt64(int64(i)),
			testColName:  common.NewString(key),
			testColAge:   common.NewInt64(int64(20 + i)),
			testColScore: common.NewFloat64(float64(60 + i)),
		})
	}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	limit := &LimitNode{
		Child:  scan,
		Offset: 0,
		Count:  1000,
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(limit)
	if err != nil {
		t.Fatalf("limit count larger than data: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 3 {
		t.Errorf("expected 3 rows (all data), got %d", totalRows)
	}
}

// TestCoerceValueSameTypeV13 测试 coerceValue 在类型相同时直接返回原值。
func TestCoerceValueSameTypeV13(t *testing.T) {
	// int -> int：类型相同，直接返回
	intVal := common.NewInt64(42)
	result := coerceValue(intVal, common.TypeInt64)
	if result.Int64 != 42 {
		t.Errorf("expected same type int64 to pass through, got %d", result.Int64)
	}

	// float -> float：类型相同
	floatVal := common.NewFloat64(3.14)
	result = coerceValue(floatVal, common.TypeFloat64)
	if result.Float64 != 3.14 {
		t.Errorf("expected same type float64 to pass through, got %g", result.Float64)
	}

	// string -> string：类型相同
	strVal := common.NewString("hello")
	result = coerceValue(strVal, common.TypeString)
	if result.Str != "hello" {
		t.Errorf("expected same type string to pass through, got %q", result.Str)
	}
}

// TestCoerceValueUnsupportedConversionV13 测试 coerceValue 不支持的类型转换。
// 不支持的转换应返回原值。
func TestCoerceValueUnsupportedConversionV13(t *testing.T) {
	// string -> int：不支持的转换，返回原值
	const testStrHello = "hello"
	strVal := common.NewString(testStrHello)
	result := coerceValue(strVal, common.TypeInt64)
	if result.Str != testStrHello {
		t.Errorf("expected unsupported conversion to return original value, got %v", result)
	}

	// int -> string：不支持的转换
	intVal := common.NewInt64(42)
	result = coerceValue(intVal, common.TypeString)
	if result.Int64 != 42 {
		t.Errorf("expected unsupported conversion to return original value, got %v", result)
	}
}
