package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// ==================== projectChunk 覆盖率测试 ====================

// TestProjectTypeCoercionIntToFloat 测试投影时 int 到 float 的类型强制转换。
func TestProjectTypeCoercionIntToFloat(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(42), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	// 将 int 列投影为 float 类型（触发 coerceValue int->float）
	project := &ProjectNode{
		Child: scan,
		Expressions: []Expression{
			&ResolvedColumnExpr{Name: testColID, Idx: 0, typ: common.TypeInt64},
		},
		Aliases: []string{"id_as_float"},
		schema: []ColumnDef{
			{Name: "id_as_float", Type: common.TypeFloat64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(project)
	if err != nil {
		t.Fatalf("project int to float: %v", err)
	}

	if len(chunks) == 0 || chunks[0].RowCount() == 0 {
		t.Fatal("expected at least 1 row")
	}

	col, _ := chunks[0].GetColumn(0)
	val := col.GetValue(0)
	if val.Float64 != 42.0 {
		t.Errorf("expected 42.0 (int coerced to float), got %g", val.Float64)
	}
}

// TestProjectTypeCoercionFloatToInt 测试投影时 float 到 int 的类型强制转换。
func TestProjectTypeCoercionFloatToInt(t *testing.T) {
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

	// 将 float 列投影为 int 类型（触发 coerceValue float->int）
	project := &ProjectNode{
		Child: scan,
		Expressions: []Expression{
			&ResolvedColumnExpr{Name: testColScore, Idx: 3, typ: common.TypeFloat64},
		},
		Aliases: []string{"score_as_int"},
		schema: []ColumnDef{
			{Name: "score_as_int", Type: common.TypeInt64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(project)
	if err != nil {
		t.Fatalf("project float to int: %v", err)
	}

	if len(chunks) == 0 || chunks[0].RowCount() == 0 {
		t.Fatal("expected at least 1 row")
	}

	col, _ := chunks[0].GetColumn(0)
	val := col.GetValue(0)
	if val.Int64 != 95 {
		t.Errorf("expected 95 (float coerced to int), got %d", val.Int64)
	}
}

// TestProjectTypeCoercionBoolToInt 测试投影时 bool 到 int 的类型强制转换。
func TestProjectTypeCoercionBoolToInt(t *testing.T) {
	// 直接测试 coerceValue 函数的 bool->int 路径
	val := coerceValue(common.NewBool(true), common.TypeInt64)
	if val.Int64 != 1 {
		t.Errorf("expected bool(true) coerced to int=1, got %d", val.Int64)
	}

	val = coerceValue(common.NewBool(false), common.TypeInt64)
	if val.Int64 != 0 {
		t.Errorf("expected bool(false) coerced to int=0, got %d", val.Int64)
	}
}

// TestProjectTypeCoercionBoolToFloat 测试投影时 bool 到 float 的类型强制转换。
func TestProjectTypeCoercionBoolToFloat(t *testing.T) {
	val := coerceValue(common.NewBool(true), common.TypeFloat64)
	if val.Float64 != 1.0 {
		t.Errorf("expected bool(true) coerced to float=1.0, got %g", val.Float64)
	}

	val = coerceValue(common.NewBool(false), common.TypeFloat64)
	if val.Float64 != 0.0 {
		t.Errorf("expected bool(false) coerced to float=0.0, got %g", val.Float64)
	}
}

// TestProjectTypeCoercionToBool 测试投影时值到 bool 的类型强制转换。
func TestProjectTypeCoercionToBool(t *testing.T) {
	// 非零 int -> true
	val := coerceValue(common.NewInt64(42), common.TypeBool)
	if !val.Valid || val.Int64 != 1 {
		t.Errorf("expected int64(42) coerced to bool=true, got valid=%v int64=%d", val.Valid, val.Int64)
	}

	// 零 int -> false
	val = coerceValue(common.NewInt64(0), common.TypeBool)
	if !val.Valid || val.Int64 != 0 {
		t.Errorf("expected int64(0) coerced to bool=false, got valid=%v int64=%d", val.Valid, val.Int64)
	}

	// 非零 float -> true
	val = coerceValue(common.NewFloat64(3.14), common.TypeBool)
	if !val.Valid || val.Int64 != 1 {
		t.Errorf("expected float64(3.14) coerced to bool=true, got valid=%v int64=%d", val.Valid, val.Int64)
	}

	// 零 float -> false
	val = coerceValue(common.NewFloat64(0.0), common.TypeBool)
	if !val.Valid || val.Int64 != 0 {
		t.Errorf("expected float64(0) coerced to bool=false, got valid=%v int64=%d", val.Valid, val.Int64)
	}
}

// TestProjectWithNullValues 测试投影包含 NULL 值的表达式。
// 验证 NULL 值在投影中被正确保留。
func TestProjectWithNullValues(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewNull(),
		testColAge: common.NewNull(), testColScore: common.NewFloat64(95.5),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	project := &ProjectNode{
		Child: scan,
		Expressions: []Expression{
			&ResolvedColumnExpr{Name: testColName, Idx: 1, typ: common.TypeString},
			&ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
			&ResolvedColumnExpr{Name: testColScore, Idx: 3, typ: common.TypeFloat64},
		},
		Aliases: []string{testColName, testColAge, testColScore},
		schema: []ColumnDef{
			{Name: testColName, Type: common.TypeString, Nullable: true},
			{Name: testColAge, Type: common.TypeInt64, Nullable: true},
			{Name: testColScore, Type: common.TypeFloat64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(project)
	if err != nil {
		t.Fatalf("project with null values: %v", err)
	}

	if len(chunks) == 0 || chunks[0].RowCount() == 0 {
		t.Fatal("expected at least 1 row")
	}

	// name 列应为 NULL
	nameCol, _ := chunks[0].GetColumn(0)
	nameVal := nameCol.GetValue(0)
	if nameVal.Valid {
		t.Errorf("expected NULL for name column, got %v", nameVal)
	}

	// age 列应为 NULL
	ageCol, _ := chunks[0].GetColumn(1)
	ageVal := ageCol.GetValue(0)
	if ageVal.Valid {
		t.Errorf("expected NULL for age column, got %v", ageVal)
	}

	// score 列应为 95.5
	scoreCol, _ := chunks[0].GetColumn(2)
	scoreVal := scoreCol.GetValue(0)
	if scoreVal.Float64 != 95.5 {
		t.Errorf("expected score=95.5, got %g", scoreVal.Float64)
	}
}

// TestProjectCoerceNullValue 测试 coerceValue 对 NULL 值的处理。
// NULL 值应始终返回 NULL，不进行类型转换。
func TestProjectCoerceNullValue(t *testing.T) {
	nullVal := common.NewNull()

	// NULL 转换为任何类型都应保持 NULL
	result := coerceValue(nullVal, common.TypeInt64)
	if result.Valid {
		t.Error("expected NULL after coercing NULL to int64")
	}

	result = coerceValue(nullVal, common.TypeFloat64)
	if result.Valid {
		t.Error("expected NULL after coercing NULL to float64")
	}

	result = coerceValue(nullVal, common.TypeBool)
	if result.Valid {
		t.Error("expected NULL after coercing NULL to bool")
	}
}

// TestProjectExpressionError 测试投影中表达式求值出错时的行为。
// 验证 projectChunk 正确返回错误。
func TestProjectExpressionError(t *testing.T) {
	inputSchema := []ColumnDef{
		{Name: testColID, Type: common.TypeInt64, Nullable: false},
	}

	// 构建包含数据的 chunk
	chunk := storage.NewChunk(defaultChunkSize)
	col0 := storage.NewColumnVector(0, common.TypeInt64, 1)
	_ = col0.Append(common.NewInt64(1))
	_ = chunk.AddColumn(col0)

	outputSchema := []ColumnDef{
		{Name: "result", Type: common.TypeInt64, Nullable: true},
	}

	colIdxMap := buildColIdxMapFromSchema(inputSchema)

	// 使用 FuncExpr 作为投影表达式，evalExpr 会返回错误
	exprs := []Expression{
		&FuncExpr{Name: testFuncUnknownFunc, Args: nil},
	}

	_, err := projectChunk(chunk, exprs, inputSchema, outputSchema, colIdxMap)
	if err == nil {
		t.Error("expected error from projectChunk with FuncExpr, got nil")
	}
}
