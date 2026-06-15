package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// ---------------------------------------------------------------------------
// executeScan 边界情况
// ---------------------------------------------------------------------------

// TestExecuteScan_EmptyResult 验证 scan 结果为空时返回 nil chunks。
func TestExecuteScan_EmptyResult(t *testing.T) {
	ms := newMockStorage()
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}, {Name: testColName, Type: common.TypeString}},
	}
	exec := NewExecutor(ms)
	result, err := exec.Execute(scan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil chunks for empty scan, got %v", result)
	}
}

// TestExecuteScan_NullValues 验证 scan 结果包含 NULL 值时正确处理。
func TestExecuteScan_NullValues(t *testing.T) {
	ms := newMockStorage()
	// 添加一行，其中 name 列缺失（相当于 NULL）
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1),
		// testColName 缺失 -> NULL
	})
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}, {Name: testColName, Type: common.TypeString}},
	}
	exec := NewExecutor(ms)
	chunks, err := exec.Execute(scan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	// name 列（第 2 列）第 0 行应为 NULL
	nameCol := chunks[0].Columns()[1]
	if !nameCol.IsNull(0) {
		t.Error("expected NULL for missing column value")
	}
}

// TestExecuteScan_TypeMismatchCoerce 验证 scan 结果包含类型不匹配的值时触发 coerceValue。
func TestExecuteScan_TypeMismatchCoerce(t *testing.T) {
	ms := newMockStorage()
	// score 列定义为 Float64，但存入 Int64 值，需要 coerceValue 转换
	ms.addEntry("a", map[string]common.Value{
		testColID:    common.NewInt64(1),
		testColScore: common.NewInt64(100), // Int64 -> Float64
	})
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColScore},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}, {Name: testColScore, Type: common.TypeFloat64}},
	}
	exec := NewExecutor(ms)
	chunks, err := exec.Execute(scan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	scoreCol := chunks[0].Columns()[1]
	if scoreCol.IsNull(0) {
		t.Fatal("expected non-NULL after coercion")
	}
	got := scoreCol.GetValue(0)
	if got.Typ != common.TypeFloat64 {
		t.Errorf("expected Float64 type after coercion, got %v", got.Typ)
	}
	if got.Float64 != 100.0 {
		t.Errorf("expected 100.0 after Int64->Float64 coercion, got %g", got.Float64)
	}
}

// ---------------------------------------------------------------------------
// appendValueSafe 边界情况
// ---------------------------------------------------------------------------

// TestAppendValueSafe_NormalValue 验证正常追加值。
func TestAppendValueSafe_NormalValue(t *testing.T) {
	col := storage.NewColumnVector(0, common.TypeString, 4)
	appendValueSafe(col, common.NewString("hello"), common.TypeString)
	if col.Len() != 1 {
		t.Fatalf("expected len 1, got %d", col.Len())
	}
	got := col.GetValue(0)
	if got.Str != "hello" {
		t.Errorf("expected 'hello', got %q", got.Str)
	}
}

// TestAppendValueSafe_CoerceSucceeds 验证类型不匹配但转换成功。
func TestAppendValueSafe_CoerceSucceeds(t *testing.T) {
	col := storage.NewColumnVector(0, common.TypeFloat64, 4)
	// Bool -> Float64: coerceValue 将 Bool 转为 Float64
	appendValueSafe(col, common.NewBool(true), common.TypeFloat64)
	if col.Len() != 1 {
		t.Fatalf("expected len 1, got %d", col.Len())
	}
	got := col.GetValue(0)
	if got.Typ != common.TypeFloat64 {
		t.Errorf("expected Float64 type, got %v", got.Typ)
	}
	if got.Float64 != 1.0 {
		t.Errorf("expected 1.0 after Bool->Float64 coercion, got %g", got.Float64)
	}
}

// TestAppendValueSafe_CoerceFailsFallsToNull 验证转换后仍不匹配，回退到 NULL。
func TestAppendValueSafe_CoerceFailsFallsToNull(t *testing.T) {
	// String -> Int64: coerceValue 不支持此转换，返回原值
	// 原值类型仍为 String，Append 到 Int64 列失败 -> NULL
	intCol := storage.NewColumnVector(0, common.TypeInt64, 4)
	appendValueSafe(intCol, common.NewString("not_a_number"), common.TypeInt64)
	if intCol.Len() != 1 {
		t.Fatalf("expected len 1, got %d", intCol.Len())
	}
	if !intCol.IsNull(0) {
		t.Error("expected NULL when both Append and coerceValue fail")
	}
}

// ---------------------------------------------------------------------------
// coerceValue 边界情况
// ---------------------------------------------------------------------------

// TestCoerceValue_NullValue 验证 NULL 值转换返回 NULL。
func TestCoerceValue_NullValue(t *testing.T) {
	result := coerceValue(common.NewNull(), common.TypeInt64)
	if result.Valid {
		t.Error("expected NULL value from NULL coercion")
	}
}

// TestCoerceValue_SameType 验证相同类型转换返回原值。
func TestCoerceValue_SameType(t *testing.T) {
	orig := common.NewInt64(42)
	result := coerceValue(orig, common.TypeInt64)
	if result.Int64 != 42 {
		t.Errorf("expected 42, got %d", result.Int64)
	}
	if result.Typ != common.TypeInt64 {
		t.Errorf("expected Int64 type, got %v", result.Typ)
	}
}

// TestCoerceValue_Int64ToFloat64 验证 Int64 -> Float64 转换。
func TestCoerceValue_Int64ToFloat64(t *testing.T) {
	result := coerceValue(common.NewInt64(42), common.TypeFloat64)
	if result.Typ != common.TypeFloat64 {
		t.Errorf("expected Float64 type, got %v", result.Typ)
	}
	if result.Float64 != 42.0 {
		t.Errorf("expected 42.0, got %g", result.Float64)
	}
}

// TestCoerceValue_Float64ToInt64 验证 Float64 -> Int64 转换。
func TestCoerceValue_Float64ToInt64(t *testing.T) {
	result := coerceValue(common.NewFloat64(3.7), common.TypeInt64)
	if result.Typ != common.TypeInt64 {
		t.Errorf("expected Int64 type, got %v", result.Typ)
	}
	if result.Int64 != 3 {
		t.Errorf("expected 3 (truncated), got %d", result.Int64)
	}
}

// TestCoerceValue_BoolToInt64 验证 Bool -> Int64 转换。
func TestCoerceValue_BoolToInt64(t *testing.T) {
	result := coerceValue(common.NewBool(true), common.TypeInt64)
	if result.Typ != common.TypeInt64 {
		t.Errorf("expected Int64 type, got %v", result.Typ)
	}
	if result.Int64 != 1 {
		t.Errorf("expected 1, got %d", result.Int64)
	}

	resultFalse := coerceValue(common.NewBool(false), common.TypeInt64)
	if resultFalse.Int64 != 0 {
		t.Errorf("expected 0 for false, got %d", resultFalse.Int64)
	}
}

// TestCoerceValue_BoolToFloat64 验证 Bool -> Float64 转换。
func TestCoerceValue_BoolToFloat64(t *testing.T) {
	result := coerceValue(common.NewBool(true), common.TypeFloat64)
	if result.Typ != common.TypeFloat64 {
		t.Errorf("expected Float64 type, got %v", result.Typ)
	}
	if result.Float64 != 1.0 {
		t.Errorf("expected 1.0, got %g", result.Float64)
	}
}

// TestCoerceValue_ToBool 验证各种类型 -> Bool 转换。
func TestCoerceValue_ToBool(t *testing.T) {
	tests := []struct {
		name string
		val  common.Value
		want bool
	}{
		{"Int64非零转Bool", common.NewInt64(42), true},
		{"Int64零转Bool", common.NewInt64(0), false},
		{"Float64非零转Bool", common.NewFloat64(3.14), true},
		{"Float64零转Bool", common.NewFloat64(0), false},
		{"String非空转Bool", common.NewString("hello"), true},
		{"String空转Bool", common.NewString(""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := coerceValue(tt.val, common.TypeBool)
			if result.Typ != common.TypeBool {
				t.Errorf("expected Bool type, got %v", result.Typ)
			}
			got := result.Int64 != 0
			if got != tt.want {
				t.Errorf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

// TestCoerceValue_UnsupportedConversion 验证不支持的类型转换返回原值。
func TestCoerceValue_UnsupportedConversion(t *testing.T) {
	// String -> Int64: 不支持，应返回原值
	orig := common.NewString("hello")
	result := coerceValue(orig, common.TypeInt64)
	if result.Typ != common.TypeString {
		t.Errorf("expected String type (original), got %v", result.Typ)
	}
	if result.Str != "hello" {
		t.Errorf("expected original value 'hello', got %q", result.Str)
	}
}

// ---------------------------------------------------------------------------
// sliceChunk 边界情况
// ---------------------------------------------------------------------------

// TestSliceChunk_NormalSlice 验证正常切片。
func TestSliceChunk_NormalSlice(t *testing.T) {
	chunk := buildTestChunk(t, 5)
	result, err := sliceChunk(chunk, 1, 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RowCount() != 3 {
		t.Errorf("expected 3 rows, got %d", result.RowCount())
	}
}

// TestSliceChunk_StartEqualsEnd 验证 startRow == endRow 时返回空切片。
func TestSliceChunk_StartEqualsEnd(t *testing.T) {
	chunk := buildTestChunk(t, 5)
	result, err := sliceChunk(chunk, 2, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RowCount() != 0 {
		t.Errorf("expected 0 rows for empty slice, got %d", result.RowCount())
	}
}

// TestSliceChunk_FullSlice 验证 startRow=0, endRow=RowCount 时返回完整切片。
func TestSliceChunk_FullSlice(t *testing.T) {
	chunk := buildTestChunk(t, 5)
	result, err := sliceChunk(chunk, 0, chunk.RowCount())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RowCount() != chunk.RowCount() {
		t.Errorf("expected %d rows for full slice, got %d", chunk.RowCount(), result.RowCount())
	}
}

// buildTestChunk 构建包含 n 行的测试 Chunk。
func buildTestChunk(t *testing.T, n int) *storage.Chunk {
	t.Helper()
	chunk := storage.NewChunk(uint32(n))
	idCol := storage.NewColumnVector(0, common.TypeInt64, uint32(n))
	nameCol := storage.NewColumnVector(1, common.TypeString, uint32(n))
	for i := 0; i < n; i++ {
		if err := idCol.Append(common.NewInt64(int64(i))); err != nil {
			t.Fatalf("append id: %v", err)
		}
		if err := nameCol.Append(common.NewString("name")); err != nil {
			t.Fatalf("append name: %v", err)
		}
	}
	if err := chunk.AddColumn(idCol); err != nil {
		t.Fatalf("add id column: %v", err)
	}
	if err := chunk.AddColumn(nameCol); err != nil {
		t.Fatalf("add name column: %v", err)
	}
	return chunk
}

// ---------------------------------------------------------------------------
// buildChunksFromEntries 边界情况
// ---------------------------------------------------------------------------

// TestBuildChunksFromEntriesCov_EmptyEntries 验证空 entries 返回 nil。
func TestBuildChunksFromEntriesCov_EmptyEntries(t *testing.T) {
	schema := []ColumnDef{{Name: "col", Type: common.TypeInt64}}
	chunks, err := buildChunksFromEntries(nil, schema, defaultChunkSize)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chunks != nil {
		t.Errorf("expected nil chunks for empty entries, got %v", chunks)
	}
}

// TestBuildChunksFromEntriesCov_EmptySchema 验证空 schema 返回 nil。
func TestBuildChunksFromEntriesCov_EmptySchema(t *testing.T) {
	entries := []storage.ScanEntry{
		{Key: "a", Value: storage.Row{Columns: map[string]common.Value{"col": common.NewInt64(1)}}},
	}
	chunks, err := buildChunksFromEntries(entries, nil, defaultChunkSize)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chunks != nil {
		t.Errorf("expected nil chunks for empty schema, got %v", chunks)
	}
}

// TestBuildChunksFromEntries_ExceedsChunkSize 验证 entries 数量超过 defaultChunkSize 时分多个 Chunk。
func TestBuildChunksFromEntries_ExceedsChunkSize(t *testing.T) {
	schema := []ColumnDef{{Name: "id", Type: common.TypeInt64}}
	const chunkSize = 5
	const totalEntries = 12
	entries := make([]storage.ScanEntry, totalEntries)
	for i := 0; i < totalEntries; i++ {
		entries[i] = storage.ScanEntry{
			Key:   fmtKey(i),
			Value: storage.Row{Columns: map[string]common.Value{"id": common.NewInt64(int64(i))}},
		}
	}

	chunks, err := buildChunksFromEntries(entries, schema, chunkSize)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedChunks := (totalEntries + chunkSize - 1) / chunkSize
	if len(chunks) != expectedChunks {
		t.Fatalf("expected %d chunks, got %d", expectedChunks, len(chunks))
	}

	totalRows := countRows(chunks)
	if totalRows != totalEntries {
		t.Errorf("expected %d total rows, got %d", totalEntries, totalRows)
	}

	// 验证最后一个 chunk 的行数
	lastChunkRows := totalEntries - (expectedChunks-1)*chunkSize
	if chunks[len(chunks)-1].RowCount() != uint32(lastChunkRows) {
		t.Errorf("expected last chunk with %d rows, got %d", lastChunkRows, chunks[len(chunks)-1].RowCount())
	}
}

// ---------------------------------------------------------------------------
// updateAccumulators 边界情况
// ---------------------------------------------------------------------------

// errExpr 是一个求值必定失败的表达式，用于测试 evalExpr 错误路径。
type errExpr struct{}

func (e *errExpr) exprNode()      {}
func (e *errExpr) String() string { return "<error>" }

// TestUpdateAccumulators_EvalErrorSkipsUpdate 验证表达式求值失败时跳过更新。
func TestUpdateAccumulators_EvalErrorSkipsUpdate(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	aggs := []AggregateExpr{
		{Func: AggCount, Arg: &errExpr{}}, // 求值失败
		{Func: AggSum, Arg: &LiteralExpr{Value: common.NewInt64(10)}},
	}
	accs := newAccumulators(aggs)
	rowVals := map[string]common.Value{"col": common.NewInt64(1)}
	colIdxMap := map[string]int{"col": 0}

	exec.updateAccumulators(accs, aggs, rowVals, colIdxMap)

	// COUNT 的累加器不应被更新（求值失败跳过）
	if accs[0].count != 0 {
		t.Errorf("expected count=0 (skipped due to eval error), got %d", accs[0].count)
	}
	// SUM 的累加器应正常更新
	if accs[1].count != 1 {
		t.Errorf("expected sum count=1, got %d", accs[1].count)
	}
	if accs[1].sum != 10.0 {
		t.Errorf("expected sum=10.0, got %g", accs[1].sum)
	}
}

// TestUpdateAccumulators_CountStarNoArg 验证 COUNT(*) 无参数聚合函数。
func TestUpdateAccumulators_CountStarNoArg(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	aggs := []AggregateExpr{
		{Func: AggCount, Arg: nil}, // COUNT(*) 无参数
	}
	accs := newAccumulators(aggs)
	rowVals := map[string]common.Value{"col": common.NewInt64(1)}
	colIdxMap := map[string]int{"col": 0}

	exec.updateAccumulators(accs, aggs, rowVals, colIdxMap)

	if accs[0].count != 1 {
		t.Errorf("expected count=1 for COUNT(*), got %d", accs[0].count)
	}
}

// ---------------------------------------------------------------------------
// buildGroupKey 边界情况
// ---------------------------------------------------------------------------

// TestBuildGroupKey_EmptyGroupBy 验证空 groupBy 返回空字符串。
func TestBuildGroupKey_EmptyGroupBy(t *testing.T) {
	rowVals := map[string]common.Value{"col": common.NewInt64(1)}
	colIdxMap := map[string]int{"col": 0}

	key := buildGroupKey(nil, rowVals, colIdxMap)
	if key != "" {
		t.Errorf("expected empty string for empty groupBy, got %q", key)
	}
}

// TestBuildGroupKey_EvalError 验证表达式求值失败时使用 "<error>" 占位。
func TestBuildGroupKey_EvalError(t *testing.T) {
	rowVals := map[string]common.Value{"col": common.NewInt64(1)}
	colIdxMap := map[string]int{"col": 0}

	groupBy := []Expression{&errExpr{}}
	key := buildGroupKey(groupBy, rowVals, colIdxMap)
	if key != "<error>" {
		t.Errorf("expected '<error>' for eval error, got %q", key)
	}
}

// TestBuildGroupKey_MultipleExprsWithError 验证多个 groupBy 表达式时部分求值失败。
func TestBuildGroupKey_MultipleExprsWithError(t *testing.T) {
	rowVals := map[string]common.Value{"col": common.NewInt64(42)}
	colIdxMap := map[string]int{"col": 0}

	groupBy := []Expression{
		&ColumnExpr{Name: "col"}, // 正常
		&errExpr{},               // 失败
	}
	key := buildGroupKey(groupBy, rowVals, colIdxMap)
	// 期望 "42\x00<error>"
	expected := "42\x00<error>"
	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

// ---------------------------------------------------------------------------
// fillColumnValues 额外边界情况
// ---------------------------------------------------------------------------

// TestFillColumnValues_NullValueInRow 验证行中包含 NULL 值时的处理。
func TestFillColumnValues_NullValueInRow(t *testing.T) {
	col := storage.NewColumnVector(0, common.TypeInt64, 1)
	batch := []storage.ScanEntry{
		{Key: "a", Value: storage.Row{Columns: map[string]common.Value{
			colNameVal: common.NewNull(),
		}}},
	}
	colDef := ColumnDef{Name: colNameVal, Type: common.TypeInt64}

	fillColumnValues(col, batch, colDef)

	if !col.IsNull(0) {
		t.Error("expected NULL for NULL value in row")
	}
}

// TestFillColumnValues_CoerceBoolToInt64 验证 Bool -> Int64 的 coerceValue 路径。
func TestFillColumnValues_CoerceBoolToInt64(t *testing.T) {
	col := storage.NewColumnVector(0, common.TypeInt64, 1)
	batch := []storage.ScanEntry{
		{Key: "a", Value: storage.Row{Columns: map[string]common.Value{
			colNameVal: common.NewBool(true),
		}}},
	}
	colDef := ColumnDef{Name: colNameVal, Type: common.TypeInt64}

	fillColumnValues(col, batch, colDef)

	if col.IsNull(0) {
		t.Fatal("expected non-NULL value after Bool->Int64 coercion")
	}
	got := col.GetValue(0)
	if got.Int64 != 1 {
		t.Errorf("expected 1 after Bool->Int64 coercion, got %d", got.Int64)
	}
}

// ---------------------------------------------------------------------------
// coerceValue 额外边界情况
// ---------------------------------------------------------------------------

// TestCoerceValue_Int64ToBool 验证 Int64 -> Bool 转换。
func TestCoerceValue_Int64ToBool(t *testing.T) {
	result := coerceValue(common.NewInt64(1), common.TypeBool)
	if result.Typ != common.TypeBool {
		t.Errorf("expected Bool type, got %v", result.Typ)
	}
	if result.Int64 == 0 {
		t.Error("expected truthy bool for Int64(1)")
	}

	resultZero := coerceValue(common.NewInt64(0), common.TypeBool)
	if resultZero.Int64 != 0 {
		t.Error("expected falsy bool for Int64(0)")
	}
}

// TestCoerceValue_Float64ToBool 验证 Float64 -> Bool 转换。
func TestCoerceValue_Float64ToBool(t *testing.T) {
	result := coerceValue(common.NewFloat64(2.5), common.TypeBool)
	if result.Typ != common.TypeBool {
		t.Errorf("expected Bool type, got %v", result.Typ)
	}
	if result.Int64 == 0 {
		t.Error("expected truthy bool for Float64(2.5)")
	}
}

// TestCoerceValue_StringToBool 验证 String -> Bool 转换。
func TestCoerceValue_StringToBool(t *testing.T) {
	result := coerceValue(common.NewString("hello"), common.TypeBool)
	if result.Typ != common.TypeBool {
		t.Errorf("expected Bool type, got %v", result.Typ)
	}
	if result.Int64 == 0 {
		t.Error("expected truthy bool for non-empty string")
	}

	resultEmpty := coerceValue(common.NewString(""), common.TypeBool)
	if resultEmpty.Int64 != 0 {
		t.Error("expected falsy bool for empty string")
	}
}

// TestCoerceValue_StringToInt64Unsupported 验证 String -> Int64 不支持，返回原值。
func TestCoerceValue_StringToInt64Unsupported(t *testing.T) {
	orig := common.NewString("42")
	result := coerceValue(orig, common.TypeInt64)
	// String -> Int64 不在 coerceValue 的 switch 中，返回原值
	if result.Typ != common.TypeString {
		t.Errorf("expected String type (unsupported conversion returns original), got %v", result.Typ)
	}
}

// TestCoerceValue_Int64ToStringUnsupported 验证 Int64 -> String 不支持，返回原值。
func TestCoerceValue_Int64ToStringUnsupported(t *testing.T) {
	orig := common.NewInt64(42)
	result := coerceValue(orig, common.TypeString)
	if result.Typ != common.TypeInt64 {
		t.Errorf("expected Int64 type (unsupported conversion returns original), got %v", result.Typ)
	}
}

// ---------------------------------------------------------------------------
// sliceChunk 错误路径
// ---------------------------------------------------------------------------

// TestSliceChunk_EndExceedsLength 验证 endRow 超过行数时返回错误。
func TestSliceChunk_EndExceedsLength(t *testing.T) {
	chunk := buildTestChunk(t, 3)
	_, err := sliceChunk(chunk, 0, 10)
	if err == nil {
		t.Error("expected error when endRow exceeds chunk length")
	}
}

// TestSliceChunk_StartGreaterThanEnd 验证 startRow > endRow 时返回错误。
func TestSliceChunk_StartGreaterThanEnd(t *testing.T) {
	chunk := buildTestChunk(t, 5)
	_, err := sliceChunk(chunk, 3, 1)
	if err == nil {
		t.Error("expected error when startRow > endRow")
	}
}

// ---------------------------------------------------------------------------
// buildGroupKey 正常路径
// ---------------------------------------------------------------------------

// TestBuildGroupKey_NormalGroupBy 验证正常 groupBy 表达式构建分组键。
func TestBuildGroupKey_NormalGroupBy(t *testing.T) {
	rowVals := map[string]common.Value{"col": common.NewInt64(42)}
	colIdxMap := map[string]int{"col": 0}

	groupBy := []Expression{&ColumnExpr{Name: "col"}}
	key := buildGroupKey(groupBy, rowVals, colIdxMap)
	if key != "42" {
		t.Errorf("expected '42', got %q", key)
	}
}

// TestBuildGroupKey_MultipleNormalExprs 验证多个正常 groupBy 表达式。
func TestBuildGroupKey_MultipleNormalExprs(t *testing.T) {
	rowVals := map[string]common.Value{
		"a": common.NewInt64(1),
		"b": common.NewString("hello"),
	}
	colIdxMap := map[string]int{"a": 0, "b": 1}

	groupBy := []Expression{
		&ColumnExpr{Name: "a"},
		&ColumnExpr{Name: "b"},
	}
	key := buildGroupKey(groupBy, rowVals, colIdxMap)
	expected := "1\x00hello"
	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

// ---------------------------------------------------------------------------
// updateAccumulators 多种聚合函数
// ---------------------------------------------------------------------------

// TestUpdateAccumulators_MultipleAggFuncs 验证多种聚合函数同时更新。
func TestUpdateAccumulators_MultipleAggFuncs(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	aggs := []AggregateExpr{
		{Func: AggCount, Arg: nil},
		{Func: AggSum, Arg: &LiteralExpr{Value: common.NewFloat64(10.5)}},
		{Func: AggMin, Arg: &LiteralExpr{Value: common.NewInt64(5)}},
		{Func: AggMax, Arg: &LiteralExpr{Value: common.NewInt64(20)}},
		{Func: AggAvg, Arg: &LiteralExpr{Value: common.NewFloat64(8.0)}},
	}
	accs := newAccumulators(aggs)
	rowVals := map[string]common.Value{}
	colIdxMap := map[string]int{}

	exec.updateAccumulators(accs, aggs, rowVals, colIdxMap)

	if accs[0].count != 1 {
		t.Errorf("COUNT: expected count=1, got %d", accs[0].count)
	}
	if accs[1].sum != 10.5 {
		t.Errorf("SUM: expected sum=10.5, got %g", accs[1].sum)
	}
	if !accs[2].hasValue || accs[2].minVal.Int64 != 5 {
		t.Errorf("MIN: expected min=5, got %v", accs[2].minVal)
	}
	if !accs[3].hasValue || accs[3].maxVal.Int64 != 20 {
		t.Errorf("MAX: expected max=20, got %v", accs[3].maxVal)
	}
	if accs[4].count != 1 || accs[4].sum != 8.0 {
		t.Errorf("AVG: expected count=1 sum=8.0, got count=%d sum=%g", accs[4].count, accs[4].sum)
	}
}

// ---------------------------------------------------------------------------
// appendValueSafe: NULL 值追加
// ---------------------------------------------------------------------------

// TestAppendValueSafe_NullValue 验证追加 NULL 值。
func TestAppendValueSafe_NullValue(t *testing.T) {
	col := storage.NewColumnVector(0, common.TypeInt64, 4)
	appendValueSafe(col, common.NewNull(), common.TypeInt64)
	if col.Len() != 1 {
		t.Fatalf("expected len 1, got %d", col.Len())
	}
	if !col.IsNull(0) {
		t.Error("expected NULL value")
	}
}

// ---------------------------------------------------------------------------
// buildChunksFromEntries: 包含 NULL 和类型不匹配的 entries
// ---------------------------------------------------------------------------

// TestBuildChunksFromEntries_WithNullAndMismatch 验证包含 NULL 和类型不匹配值的 entries。
func TestBuildChunksFromEntries_WithNullAndMismatch(t *testing.T) {
	schema := []ColumnDef{
		{Name: "id", Type: common.TypeInt64},
		{Name: "score", Type: common.TypeFloat64},
	}
	entries := []storage.ScanEntry{
		{
			Key: "a",
			Value: storage.Row{Columns: map[string]common.Value{
				"id":    common.NewInt64(1),
				"score": common.NewInt64(100), // Int64 -> Float64 需要转换
			}},
		},
		{
			Key: "b",
			Value: storage.Row{Columns: map[string]common.Value{
				"id": common.NewInt64(2),
				// score 缺失 -> NULL
			}},
		},
	}

	chunks, err := buildChunksFromEntries(entries, schema, defaultChunkSize)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}

	// 验证第一行 score 被正确转换
	scoreCol := chunks[0].Columns()[1]
	got := scoreCol.GetValue(0)
	if got.Typ != common.TypeFloat64 {
		t.Errorf("expected Float64 type, got %v", got.Typ)
	}
	if got.Float64 != 100.0 {
		t.Errorf("expected 100.0, got %g", got.Float64)
	}

	// 验证第二行 score 为 NULL
	if !scoreCol.IsNull(1) {
		t.Error("expected NULL for missing score in second row")
	}
}
