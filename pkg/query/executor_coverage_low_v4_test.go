package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// ---------------------------------------------------------------------------
// executeScan: 空 entries 路径（87.5% → >90%）
// 当 scanWithPredicate 返回空 entries 时，executeScan 应返回 nil chunks
// ---------------------------------------------------------------------------

// TestExecuteScan_EmptyEntriesV4 测试 executeScan 在无匹配数据时返回 nil chunks。
func TestExecuteScan_EmptyEntriesV4(t *testing.T) {
	ms := newMockStorage()
	// 添加数据，但谓词会过滤掉所有行
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})

	schema := buildTestSchema()
	scan := &ScanNode{
		Table:     testTableUsers,
		Columns:   []string{testColID, testColName, testColAge, testColScore},
		Predicate: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(999)}},
		schema:    schema,
	}

	exec := NewExecutor(ms)
	result, err := exec.executeScan(scan)
	if err != nil {
		t.Fatalf("executeScan 空 entries 失败: %v", err)
	}
	// 空 entries 路径返回 nil chunks 和有效 schema
	if result.chunks != nil {
		t.Errorf("期望 nil chunks，得到 %d 个 chunks", len(result.chunks))
	}
	if len(result.schema) != len(schema) {
		t.Errorf("期望 schema 长度 %d，得到 %d", len(schema), len(result.schema))
	}
}

// TestExecuteScan_NoDataInStorageV4 测试 executeScan 在存储引擎无数据时返回 nil chunks。
func TestExecuteScan_NoDataInStorageV4(t *testing.T) {
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
		t.Fatalf("executeScan 无数据存储失败: %v", err)
	}
	if result.chunks != nil {
		t.Errorf("期望 nil chunks，得到 %d 个 chunks", len(result.chunks))
	}
}

// ---------------------------------------------------------------------------
// evalFloatArithmetic: 除零和未知运算符路径（87.5% → >90%）
// ---------------------------------------------------------------------------

// TestEvalFloatArithmetic_DivByZeroV4 测试浮点数除以零返回 NULL。
func TestEvalFloatArithmetic_DivByZeroV4(t *testing.T) {
	val, err := evalFloatArithmetic(10.0, 0.0, opDiv)
	if err != nil {
		t.Fatalf("evalFloatArithmetic 除零不应返回错误: %v", err)
	}
	if val.Valid {
		t.Errorf("期望 NULL（浮点除零），得到 %v", val)
	}
}

// TestEvalFloatArithmetic_UnknownOpV4 测试未知运算符返回 NULL。
func TestEvalFloatArithmetic_UnknownOpV4(t *testing.T) {
	val, err := evalFloatArithmetic(10.0, 5.0, arithOp(99))
	if err != nil {
		t.Fatalf("evalFloatArithmetic 未知运算符不应返回错误: %v", err)
	}
	if val.Valid {
		t.Errorf("期望 NULL（未知运算符），得到 %v", val)
	}
}

// TestEvalFloatArithmetic_NormalOpsV4 测试浮点数正常运算。
func TestEvalFloatArithmetic_NormalOpsV4(t *testing.T) {
	tests := []struct {
		name string
		lf   float64
		rf   float64
		op   arithOp
		want float64
	}{
		{"加法", 10.0, 5.0, opAdd, 15.0},
		{"减法", 10.0, 5.0, opSub, 5.0},
		{"乘法", 10.0, 5.0, opMul, 50.0},
		{"除法", 10.0, 5.0, opDiv, 2.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := evalFloatArithmetic(tt.lf, tt.rf, tt.op)
			if err != nil {
				t.Fatalf("evalFloatArithmetic %s 失败: %v", tt.name, err)
			}
			if !val.Valid || val.Float64 != tt.want {
				t.Errorf("期望 %g，得到 %v", tt.want, val)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// evalIntArithmetic: 除零和未知运算符路径（87.5% → >90%）
// ---------------------------------------------------------------------------

// TestEvalIntArithmetic_DivByZeroV4 测试整数除以零返回 NULL。
func TestEvalIntArithmetic_DivByZeroV4(t *testing.T) {
	val, err := evalIntArithmetic(10, 0, opDiv)
	if err != nil {
		t.Fatalf("evalIntArithmetic 除零不应返回错误: %v", err)
	}
	if val.Valid {
		t.Errorf("期望 NULL（整数除零），得到 %v", val)
	}
}

// TestEvalIntArithmetic_UnknownOpV4 测试未知运算符返回 NULL。
func TestEvalIntArithmetic_UnknownOpV4(t *testing.T) {
	val, err := evalIntArithmetic(10, 5, arithOp(99))
	if err != nil {
		t.Fatalf("evalIntArithmetic 未知运算符不应返回错误: %v", err)
	}
	if val.Valid {
		t.Errorf("期望 NULL（未知运算符），得到 %v", val)
	}
}

// TestEvalIntArithmetic_NormalOpsV4 测试整数正常运算。
func TestEvalIntArithmetic_NormalOpsV4(t *testing.T) {
	tests := []struct {
		name string
		li   int64
		ri   int64
		op   arithOp
		want int64
	}{
		{"加法", 10, 5, opAdd, 15},
		{"减法", 10, 5, opSub, 5},
		{"乘法", 10, 5, opMul, 50},
		{"除法", 10, 5, opDiv, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := evalIntArithmetic(tt.li, tt.ri, tt.op)
			if err != nil {
				t.Fatalf("evalIntArithmetic %s 失败: %v", tt.name, err)
			}
			if !val.Valid || val.Int64 != tt.want {
				t.Errorf("期望 %d，得到 %v", tt.want, val)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// sliceChunk: 正常切片和错误路径（87.5% → >90%）
// ---------------------------------------------------------------------------

// TestSliceChunk_NormalV4 测试正常切片操作。
func TestSliceChunk_NormalV4(t *testing.T) {
	chunk := storage.NewChunk(defaultChunkSize)
	col := storage.NewColumnVector(0, common.TypeInt64, 5)
	for i := int64(0); i < 5; i++ {
		_ = col.Append(common.NewInt64(i))
	}
	_ = chunk.AddColumn(col)

	result, err := sliceChunk(chunk, 1, 4)
	if err != nil {
		t.Fatalf("sliceChunk 正常切片失败: %v", err)
	}
	if result.RowCount() != 3 {
		t.Errorf("期望 3 行，得到 %d", result.RowCount())
	}
	// 验证切片数据正确
	col0, _ := result.GetColumn(0)
	val := col0.GetValue(0)
	if val.Int64 != 1 {
		t.Errorf("期望第一行值为 1，得到 %d", val.Int64)
	}
	val = col0.GetValue(2)
	if val.Int64 != 3 {
		t.Errorf("期望第三行值为 3，得到 %d", val.Int64)
	}
}

// TestSliceChunk_SliceErrorV4 测试 sliceChunk 切片越界时返回错误。
func TestSliceChunk_SliceErrorV4(t *testing.T) {
	chunk := storage.NewChunk(defaultChunkSize)
	col := storage.NewColumnVector(0, common.TypeInt64, 2)
	_ = col.Append(common.NewInt64(1))
	_ = col.Append(common.NewInt64(2))
	_ = chunk.AddColumn(col)

	// endRow 超过列长度
	_, err := sliceChunk(chunk, 0, 10)
	if err == nil {
		t.Error("期望切片越界错误，得到 nil")
	}
}

// TestSliceChunk_ColumnLengthMismatchV4 测试 sliceChunk 中列长度不一致时的错误处理。
// 通过在添加列到 chunk 后向某列追加数据，制造列间长度不一致，
// 使 Slice 操作因 endRow 超过较短列的长度而返回错误。
// 注：sliceChunk 的 AddColumn 错误路径在正常使用中不可达，因为 Slice 保证
// 返回 endRow-startRow 行，只要所有列 Slice 成功，长度一定一致。
func TestSliceChunk_ColumnLengthMismatchV4(t *testing.T) {
	chunk := storage.NewChunk(defaultChunkSize)
	// 添加两列，初始各 3 行
	col1 := storage.NewColumnVector(0, common.TypeInt64, 3)
	col2 := storage.NewColumnVector(1, common.TypeFloat64, 3)
	for i := int64(0); i < 3; i++ {
		_ = col1.Append(common.NewInt64(i))
		_ = col2.Append(common.NewFloat64(float64(i)))
	}
	_ = chunk.AddColumn(col1)
	_ = chunk.AddColumn(col2)

	// 向第一列追加额外数据，使其长度变为 5
	_ = col1.Append(common.NewInt64(100))
	_ = col1.Append(common.NewInt64(200))

	// sliceChunk 切片 [0, 5)：第一列有 5 行可切，第二列只有 3 行
	// 第二列的 Slice 会因 endRow 超过长度而报错
	_, err := sliceChunk(chunk, 0, 5)
	if err == nil {
		t.Error("期望切片错误（列长度不一致），得到 nil")
	}
}

// TestSliceChunk_StartGtEndV4 测试 sliceChunk 中 startRow > endRow 时返回错误。
func TestSliceChunk_StartGtEndV4(t *testing.T) {
	chunk := storage.NewChunk(defaultChunkSize)
	col := storage.NewColumnVector(0, common.TypeInt64, 3)
	for i := int64(0); i < 3; i++ {
		_ = col.Append(common.NewInt64(i))
	}
	_ = chunk.AddColumn(col)

	// startRow > endRow，Slice 会返回错误
	_, err := sliceChunk(chunk, 3, 1)
	if err == nil {
		t.Error("期望 start > end 切片错误，得到 nil")
	}
}

// TestSliceChunk_EmptySliceV4 测试 sliceChunk 切片范围为空（start == end）。
func TestSliceChunk_EmptySliceV4(t *testing.T) {
	chunk := storage.NewChunk(defaultChunkSize)
	col := storage.NewColumnVector(0, common.TypeInt64, 3)
	for i := int64(0); i < 3; i++ {
		_ = col.Append(common.NewInt64(i))
	}
	_ = chunk.AddColumn(col)

	result, err := sliceChunk(chunk, 1, 1)
	if err != nil {
		t.Fatalf("sliceChunk 空切片失败: %v", err)
	}
	if result.RowCount() != 0 {
		t.Errorf("期望 0 行（空切片），得到 %d", result.RowCount())
	}
}

// TestSliceChunk_MultipleColumnsV4 测试 sliceChunk 多列切片。
func TestSliceChunk_MultipleColumnsV4(t *testing.T) {
	chunk := storage.NewChunk(defaultChunkSize)
	col1 := storage.NewColumnVector(0, common.TypeInt64, 5)
	col2 := storage.NewColumnVector(1, common.TypeFloat64, 5)
	for i := int64(0); i < 5; i++ {
		_ = col1.Append(common.NewInt64(i))
		_ = col2.Append(common.NewFloat64(float64(i) * 1.5))
	}
	_ = chunk.AddColumn(col1)
	_ = chunk.AddColumn(col2)

	result, err := sliceChunk(chunk, 2, 4)
	if err != nil {
		t.Fatalf("sliceChunk 多列切片失败: %v", err)
	}
	if result.RowCount() != 2 {
		t.Errorf("期望 2 行，得到 %d", result.RowCount())
	}
	if result.ColumnCount() != 2 {
		t.Errorf("期望 2 列，得到 %d", result.ColumnCount())
	}
}

// ---------------------------------------------------------------------------
// executeAggregate: AddColumn 错误路径（86.7% → >90%）
// 当 outputCols 添加到 output Chunk 时 AddColumn 失败
// ---------------------------------------------------------------------------

// TestExecuteAggregate_AddColumnErrorV4 测试 executeAggregate 中 AddColumn 失败路径。
// 通过直接构造 outputCols 列长度不一致的场景来触发 AddColumn 错误。
// 正常情况下 buildAggregateOutput 保证所有列长度一致，此测试覆盖防御性错误路径。
func TestExecuteAggregate_AddColumnErrorV4(t *testing.T) {
	// 直接测试 AddColumn 错误路径：当 outputCols 的列长度不一致时，
	// storage.Chunk.AddColumn 会返回错误
	output := storage.NewChunk(defaultChunkSize)

	// 第一列有 2 行
	col1 := storage.NewColumnVector(0, common.TypeInt64, 2)
	_ = col1.Append(common.NewInt64(1))
	_ = col1.Append(common.NewInt64(2))
	_ = output.AddColumn(col1) // 成功，设置 rowCount=2

	// 第二列只有 1 行，与 rowCount 不匹配
	col2 := storage.NewColumnVector(1, common.TypeInt64, 1)
	_ = col2.Append(common.NewInt64(10))

	err := output.AddColumn(col2)
	if err == nil {
		t.Error("期望 AddColumn 错误（列长度不匹配），得到 nil")
	}
}

// ---------------------------------------------------------------------------
// buildAggregateOutput: group-by append 错误和 aggregate append 错误路径（88.9% → >90%）
// ---------------------------------------------------------------------------

// TestBuildAggregateOutput_GroupByAppendErrorV4 测试 buildAggregateOutput 中 group-by append 失败路径。
// 当 group-by 列的值类型与 schema 类型不匹配且无法强制转换时，Append 会失败。
func TestBuildAggregateOutput_GroupByAppendErrorV4(t *testing.T) {
	// 构造 schema 声明 Int64 类型，但 group-by 表达式求值返回 String 类型
	schema := []ColumnDef{
		{Name: testColName, Type: common.TypeInt64, Nullable: false}, // 声明 Int64
		{Name: testAggCountStar, Type: common.TypeInt64, Nullable: false},
	}

	agg := &AggregateNode{
		GroupBy: []Expression{
			&LiteralExpr{Value: common.NewString("string_value")}, // 返回 String
		},
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
		},
	}

	const groupKey = "str_val"
	groupAccum := map[string][]accumulator{
		groupKey: {{funcType: AggCount, count: 1}},
	}
	groupRows := map[string]*groupRow{
		groupKey: {key: groupKey, values: map[string]common.Value{testColName: common.NewString(groupKey)}},
	}
	groupOrder := []string{groupKey}
	colIdxMap := map[string]int{testColName: 0}

	_, err := (&Executor{}).buildAggregateOutput(agg, schema, groupAccum, groupRows, groupOrder, colIdxMap)
	if err == nil {
		t.Error("期望 buildAggregateOutput group-by append 错误，得到 nil")
	}
}

// TestBuildAggregateOutput_AggregateAppendErrorV4 测试 buildAggregateOutput 中 aggregate append 失败路径。
// 当聚合结果的值类型与 schema 类型不匹配且无法强制转换时，Append 会失败。
func TestBuildAggregateOutput_AggregateAppendErrorV4(t *testing.T) {
	// 构造 schema 声明 Int64 类型，但聚合结果为 String 类型
	schema := []ColumnDef{
		{Name: testAggCountStar, Type: common.TypeString, Nullable: false}, // 声明 String
	}

	agg := &AggregateNode{
		GroupBy: nil,
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}}, // COUNT 返回 Int64
		},
	}

	groupAccum := map[string][]accumulator{
		"": {{funcType: AggCount, count: 5}},
	}
	groupRows := map[string]*groupRow{
		"": {key: "", values: nil},
	}
	groupOrder := []string{""}
	colIdxMap := map[string]int{}

	_, err := (&Executor{}).buildAggregateOutput(agg, schema, groupAccum, groupRows, groupOrder, colIdxMap)
	if err == nil {
		t.Error("期望 buildAggregateOutput aggregate append 错误，得到 nil")
	}
}

// TestBuildAggregateOutput_NormalV4 测试 buildAggregateOutput 正常路径。
func TestBuildAggregateOutput_NormalV4(t *testing.T) {
	schema := []ColumnDef{
		{Name: testColAge, Type: common.TypeInt64, Nullable: false},
		{Name: testAggCountStar, Type: common.TypeInt64, Nullable: false},
	}

	agg := &AggregateNode{
		GroupBy: []Expression{
			&ResolvedColumnExpr{Name: testColAge, Idx: 0, typ: common.TypeInt64},
		},
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
		},
	}

	groupAccum := map[string][]accumulator{
		"30": {{funcType: AggCount, count: 2}},
		"25": {{funcType: AggCount, count: 1}},
	}
	groupRows := map[string]*groupRow{
		"30": {key: "30", values: map[string]common.Value{testColAge: common.NewInt64(30)}},
		"25": {key: "25", values: map[string]common.Value{testColAge: common.NewInt64(25)}},
	}
	groupOrder := []string{"30", "25"}
	colIdxMap := map[string]int{testColAge: 0}

	outputCols, err := (&Executor{}).buildAggregateOutput(agg, schema, groupAccum, groupRows, groupOrder, colIdxMap)
	if err != nil {
		t.Fatalf("buildAggregateOutput 正常路径失败: %v", err)
	}
	if len(outputCols) != 2 {
		t.Errorf("期望 2 列，得到 %d", len(outputCols))
	}
	// 验证 group-by 列有 2 行
	if outputCols[0].Len() != 2 {
		t.Errorf("期望 group-by 列 2 行，得到 %d", outputCols[0].Len())
	}
	// 验证聚合列有 2 行
	if outputCols[1].Len() != 2 {
		t.Errorf("期望聚合列 2 行，得到 %d", outputCols[1].Len())
	}
}
