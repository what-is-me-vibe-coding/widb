package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// TestAppendValueSafeNormalAppend 测试 appendValueSafe 正常追加路径。
func TestAppendValueSafeNormalAppend(t *testing.T) {
	col := storage.NewColumnVector(0, common.TypeInt64, 1)
	appendValueSafe(col, common.NewInt64(42), common.TypeInt64)
	if col.Len() != 1 {
		t.Errorf("expected 1 value, got %d", col.Len())
	}
}

// TestAppendValueSafeCoercionPath 测试 appendValueSafe 类型转换路径。
// Int64 值追加到 Float64 列时，第一次 Append 失败，coerceValue 转换后成功。
func TestAppendValueSafeCoercionPath(t *testing.T) {
	col := storage.NewColumnVector(0, common.TypeFloat64, 1)
	appendValueSafe(col, common.NewInt64(42), common.TypeFloat64)
	if col.Len() != 1 {
		t.Errorf("expected 1 value after coercion, got %d", col.Len())
	}
}

// TestAppendValueSafeNullFallback 测试 appendValueSafe NULL 回退路径。
// String 值追加到 Int64 列时，Append 和 coerceValue 都失败，最终用 NULL 填充。
func TestAppendValueSafeNullFallback(t *testing.T) {
	col := storage.NewColumnVector(0, common.TypeInt64, 1)
	appendValueSafe(col, common.NewString("not-a-number"), common.TypeInt64)
	if col.Len() != 1 {
		t.Errorf("expected 1 value (NULL fallback), got %d", col.Len())
	}
	val := col.GetValue(0)
	if val.Valid {
		t.Error("expected NULL value from fallback path, got valid value")
	}
}

// TestAppendValueSafeNullInput 测试 appendValueSafe 输入为 NULL 的情况。
func TestAppendValueSafeNullInput(t *testing.T) {
	col := storage.NewColumnVector(0, common.TypeInt64, 1)
	appendValueSafe(col, common.NewNull(), common.TypeInt64)
	if col.Len() != 1 {
		t.Errorf("expected 1 value, got %d", col.Len())
	}
}

// TestBuildChunksFromEntriesEmptySchema 测试 buildChunksFromEntries 空 schema。
func TestBuildChunksFromEntriesEmptySchema(t *testing.T) {
	entries := []storage.ScanEntry{
		{Key: "a", Value: storage.Row{Columns: map[string]common.Value{testColID: common.NewInt64(1)}}},
	}
	chunks, err := buildChunksFromEntries(entries, nil, defaultChunkSize)
	if err != nil {
		t.Fatalf("buildChunksFromEntries: %v", err)
	}
	if chunks != nil {
		t.Errorf("expected nil for empty schema, got %d chunks", len(chunks))
	}
}

// TestBuildChunksFromEntriesEmptyEntries 测试 buildChunksFromEntries 空 entries。
func TestBuildChunksFromEntriesEmptyEntries(t *testing.T) {
	schema := []ColumnDef{{Name: testColID, Type: common.TypeInt64}}
	chunks, err := buildChunksFromEntries(nil, schema, defaultChunkSize)
	if err != nil {
		t.Fatalf("buildChunksFromEntries: %v", err)
	}
	if chunks != nil {
		t.Errorf("expected nil for empty entries, got %d chunks", len(chunks))
	}
}

// TestBuildChunksFromEntriesMissingColumn 测试 buildChunksFromEntries 列缺失时用 NULL 填充。
func TestBuildChunksFromEntriesMissingColumn(t *testing.T) {
	schema := []ColumnDef{
		{Name: testColID, Type: common.TypeInt64},
		{Name: testColName, Type: common.TypeString},
	}
	entries := []storage.ScanEntry{
		{Key: "a", Value: storage.Row{Columns: map[string]common.Value{testColID: common.NewInt64(1)}}},
	}
	chunks, err := buildChunksFromEntries(entries, schema, defaultChunkSize)
	if err != nil {
		t.Fatalf("buildChunksFromEntries: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].RowCount() != 1 {
		t.Errorf("expected 1 row, got %d", chunks[0].RowCount())
	}
	// 第二列应该为 NULL
	nameCol, _ := chunks[0].GetColumn(1)
	if nameCol.GetValue(0).Valid {
		t.Error("expected NULL for missing column, got valid value")
	}
}

// TestExecuteAggregateEmptyInput 测试 executeAggregate 在无输入数据时的行为。
// 验证空输入时 aggregateRows 创建默认空分组，聚合结果为 NULL。
func TestExecuteAggregateEmptyInput(t *testing.T) {
	ms := newMockStorage() // no entries

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
		t.Fatalf("executeAggregate empty input: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatal("expected at least 1 chunk for empty aggregate")
	}

	// 空输入时 SUM 应该返回 NULL
	col, _ := chunks[0].GetColumn(0)
	val := col.GetValue(0)
	if val.Valid {
		t.Errorf("expected NULL for SUM with no input, got %v", val)
	}
}

// TestExecuteAggregateCountWithNulls 测试 COUNT 聚合在有 NULL 值时的行为。
func TestExecuteAggregateCountWithNulls(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
	})
	ms.addEntry("b", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewNull(),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName},
		schema:  []ColumnDef{{Name: testColID, Type: common.TypeInt64}, {Name: testColName, Type: common.TypeString}},
	}

	agg := &AggregateNode{
		Child: scan,
		Aggregates: []AggregateExpr{
			{Func: AggCount},
		},
		schema: []ColumnDef{{Name: "count_star", Type: common.TypeInt64}},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(agg)
	if err != nil {
		t.Fatalf("executeAggregate COUNT with nulls: %v", err)
	}

	col, _ := chunks[0].GetColumn(0)
	count := col.GetValue(0).Int64
	if count != 2 {
		t.Errorf("COUNT(*) with nulls: expected 2, got %d", count)
	}
}

// TestBuildGroupKeyMultipleColumns 测试 buildGroupKey 多列分组键。
func TestBuildGroupKeyMultipleColumns(t *testing.T) {
	row := map[string]common.Value{
		testColName: common.NewString("alice"),
		testColAge:  common.NewInt64(30),
	}
	colIdxMap := map[string]int{testColName: 0, testColAge: 1}

	key := buildGroupKey([]Expression{
		&ResolvedColumnExpr{Name: testColName, Idx: 0, typ: common.TypeString},
		&ResolvedColumnExpr{Name: testColAge, Idx: 1, typ: common.TypeInt64},
	}, row, colIdxMap)

	if key != "alice\x0030" {
		t.Errorf("buildGroupKey multiple columns: got %q, want 'alice\\x0030'", key)
	}
}

// TestBuildGroupKeyEmpty 测试 buildGroupKey 空分组键。
func TestBuildGroupKeyEmpty(t *testing.T) {
	key := buildGroupKey(nil, nil, nil)
	if key != "" {
		t.Errorf("buildGroupKey empty: got %q, want empty string", key)
	}
}

// TestCoerceValueSameType 测试 coerceValue 类型相同时直接返回。
func TestCoerceValueSameType(t *testing.T) {
	val := common.NewInt64(42)
	result := coerceValue(val, common.TypeInt64)
	if result.Int64 != 42 {
		t.Errorf("coerceValue same type: got %d, want 42", result.Int64)
	}
}

// TestCoerceValueInt64ToFloat64 测试 coerceValue Int64 转 Float64。
func TestCoerceValueInt64ToFloat64(t *testing.T) {
	result := coerceValue(common.NewInt64(42), common.TypeFloat64)
	if result.Float64 != 42.0 {
		t.Errorf("coerceValue int64->float64: got %g, want 42.0", result.Float64)
	}
}

// TestCoerceValueToBool 测试 coerceValue 转换为 Bool 类型。
func TestCoerceValueToBool(t *testing.T) {
	result := coerceValue(common.NewInt64(1), common.TypeBool)
	if !result.Valid || result.Int64 != 1 {
		t.Errorf("coerceValue int64->bool: got %v", result)
	}
}

// TestCoerceValueUnsupported 测试 coerceValue 不支持的类型转换返回原值。
func TestCoerceValueUnsupported(t *testing.T) {
	result := coerceValue(common.NewString("unsupported-coerce-test"), common.TypeInt64)
	if result.Str != "unsupported-coerce-test" {
		t.Errorf("coerceValue unsupported: got %v, want original string", result)
	}
}
