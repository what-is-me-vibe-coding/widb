package query

import (
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// ============================================================================
// queryOpToIndexOpFlip 覆盖率提升测试（原 0%）
// ============================================================================

// TestQueryOpToIndexOpFlipLt 测试 OpLt 翻转为 OpGreater。
func TestQueryOpToIndexOpFlipLt(t *testing.T) {
	op, ok := queryOpToIndexOpFlip(OpLt)
	if !ok {
		t.Fatal("期望 OpLt 可翻转")
	}
	if op != index.OpGreater {
		t.Errorf("OpLt 翻转后期望 OpGreater，得到 %v", op)
	}
}

// TestQueryOpToIndexOpFlipLe 测试 OpLe 翻转为 OpGreaterEqual。
func TestQueryOpToIndexOpFlipLe(t *testing.T) {
	op, ok := queryOpToIndexOpFlip(OpLe)
	if !ok {
		t.Fatal("期望 OpLe 可翻转")
	}
	if op != index.OpGreaterEqual {
		t.Errorf("OpLe 翻转后期望 OpGreaterEqual，得到 %v", op)
	}
}

// TestQueryOpToIndexOpFlipGt 测试 OpGt 翻转为 OpLess。
func TestQueryOpToIndexOpFlipGt(t *testing.T) {
	op, ok := queryOpToIndexOpFlip(OpGt)
	if !ok {
		t.Fatal("期望 OpGt 可翻转")
	}
	if op != index.OpLess {
		t.Errorf("OpGt 翻转后期望 OpLess，得到 %v", op)
	}
}

// TestQueryOpToIndexOpFlipGe 测试 OpGe 翻转为 OpLessEqual。
func TestQueryOpToIndexOpFlipGe(t *testing.T) {
	op, ok := queryOpToIndexOpFlip(OpGe)
	if !ok {
		t.Fatal("期望 OpGe 可翻转")
	}
	if op != index.OpLessEqual {
		t.Errorf("OpGe 翻转后期望 OpLessEqual，得到 %v", op)
	}
}

// TestQueryOpToIndexOpFlipEq 测试 OpEq 翻转仍为 OpEqual。
func TestQueryOpToIndexOpFlipEq(t *testing.T) {
	op, ok := queryOpToIndexOpFlip(OpEq)
	if !ok {
		t.Fatal("期望 OpEq 可翻转")
	}
	if op != index.OpEqual {
		t.Errorf("OpEq 翻转后期望 OpEqual，得到 %v", op)
	}
}

// TestQueryOpToIndexOpFlipNe 测试 OpNe 翻转仍为 OpNotEqual。
func TestQueryOpToIndexOpFlipNe(t *testing.T) {
	op, ok := queryOpToIndexOpFlip(OpNe)
	if !ok {
		t.Fatal("期望 OpNe 可翻转")
	}
	if op != index.OpNotEqual {
		t.Errorf("OpNe 翻转后期望 OpNotEqual，得到 %v", op)
	}
}

// TestQueryOpToIndexOpFlipUnsupported 测试不支持的运算符翻转返回 false。
func TestQueryOpToIndexOpFlipUnsupported(t *testing.T) {
	_, ok := queryOpToIndexOpFlip(BinaryOp(99))
	if ok {
		t.Error("期望不支持的运算符翻转返回 false")
	}
}

// ============================================================================
// queryOpToIndexOp 覆盖率提升测试（原 75%）
// ============================================================================

// TestQueryOpToIndexOpNe 测试 OpNe 映射为 OpNotEqual。
func TestQueryOpToIndexOpNe(t *testing.T) {
	op, ok := queryOpToIndexOp(OpNe)
	if !ok {
		t.Fatal("期望 OpNe 可映射")
	}
	if op != index.OpNotEqual {
		t.Errorf("OpNe 映射后期望 OpNotEqual，得到 %v", op)
	}
}

// TestQueryOpToIndexOpAllOps 测试所有支持的运算符映射。
func TestQueryOpToIndexOpAllOps(t *testing.T) {
	tests := []struct {
		op     BinaryOp
		want   index.PredicateOp
		wantOK bool
	}{
		{OpEq, index.OpEqual, true},
		{OpNe, index.OpNotEqual, true},
		{OpLt, index.OpLess, true},
		{OpLe, index.OpLessEqual, true},
		{OpGt, index.OpGreater, true},
		{OpGe, index.OpGreaterEqual, true},
		{BinaryOp(99), 0, false},
	}
	for _, tt := range tests {
		op, ok := queryOpToIndexOp(tt.op)
		if ok != tt.wantOK || (ok && op != tt.want) {
			t.Errorf("queryOpToIndexOp(%v) = (%v, %v), want (%v, %v)", tt.op, op, ok, tt.want, tt.wantOK)
		}
	}
}

// ============================================================================
// binaryExprToColumnPredicate 覆盖率提升测试（原 52.4%）
// ============================================================================

// TestBinaryExprToColumnPredicate_LiteralOpColumn_Lt 测试 "literal < column" 形式。
// 覆盖 queryOpToIndexOpFlip 路径。
func TestBinaryExprToColumnPredicate_LiteralOpColumn_Lt(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	// 5 < age 等价于 age > 5
	bin := &BinaryExpr{
		Op:    OpLt,
		Left:  &LiteralExpr{Value: common.NewInt64(5)},
		Right: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
	}
	pred, ok := exec.binaryExprToColumnPredicate(bin)
	if !ok {
		t.Fatal("期望 '5 < age' 可转换为列谓词")
	}
	if pred.ColumnName != testColAge {
		t.Errorf("期望列名 %q，得到 %q", testColAge, pred.ColumnName)
	}
	if pred.Op != index.OpGreater {
		t.Errorf("期望 OpGreater（5 < age 等价于 age > 5），得到 %v", pred.Op)
	}
	if pred.Value.Int64 != 5 {
		t.Errorf("期望值 5，得到 %d", pred.Value.Int64)
	}
}

// TestBinaryExprToColumnPredicate_LiteralOpColumn_Gt 测试 "literal > column" 形式。
func TestBinaryExprToColumnPredicate_LiteralOpColumn_Gt(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	// 100 > age 等价于 age < 100
	bin := &BinaryExpr{
		Op:    OpGt,
		Left:  &LiteralExpr{Value: common.NewInt64(100)},
		Right: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
	}
	pred, ok := exec.binaryExprToColumnPredicate(bin)
	if !ok {
		t.Fatal("期望 '100 > age' 可转换为列谓词")
	}
	if pred.Op != index.OpLess {
		t.Errorf("期望 OpLess（100 > age 等价于 age < 100），得到 %v", pred.Op)
	}
}

// TestBinaryExprToColumnPredicate_LiteralOpColumn_Le 测试 "literal <= column" 形式。
func TestBinaryExprToColumnPredicate_LiteralOpColumn_Le(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	bin := &BinaryExpr{
		Op:    OpLe,
		Left:  &LiteralExpr{Value: common.NewInt64(30)},
		Right: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
	}
	pred, ok := exec.binaryExprToColumnPredicate(bin)
	if !ok {
		t.Fatal("期望 '30 <= age' 可转换为列谓词")
	}
	if pred.Op != index.OpGreaterEqual {
		t.Errorf("期望 OpGreaterEqual，得到 %v", pred.Op)
	}
}

// TestBinaryExprToColumnPredicate_LiteralOpColumn_Ge 测试 "literal >= column" 形式。
func TestBinaryExprToColumnPredicate_LiteralOpColumn_Ge(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	bin := &BinaryExpr{
		Op:    OpGe,
		Left:  &LiteralExpr{Value: common.NewInt64(30)},
		Right: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
	}
	pred, ok := exec.binaryExprToColumnPredicate(bin)
	if !ok {
		t.Fatal("期望 '30 >= age' 可转换为列谓词")
	}
	if pred.Op != index.OpLessEqual {
		t.Errorf("期望 OpLessEqual，得到 %v", pred.Op)
	}
}

// TestBinaryExprToColumnPredicate_LiteralOpColumn_Eq 测试 "literal = column" 形式。
func TestBinaryExprToColumnPredicate_LiteralOpColumn_Eq(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	bin := &BinaryExpr{
		Op:    OpEq,
		Left:  &LiteralExpr{Value: common.NewInt64(25)},
		Right: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
	}
	pred, ok := exec.binaryExprToColumnPredicate(bin)
	if !ok {
		t.Fatal("期望 '25 = age' 可转换为列谓词")
	}
	if pred.Op != index.OpEqual {
		t.Errorf("期望 OpEqual，得到 %v", pred.Op)
	}
}

// TestBinaryExprToColumnPredicate_LiteralOpColumn_Ne 测试 "literal != column" 形式。
func TestBinaryExprToColumnPredicate_LiteralOpColumn_Ne(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	bin := &BinaryExpr{
		Op:    OpNe,
		Left:  &LiteralExpr{Value: common.NewInt64(25)},
		Right: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
	}
	pred, ok := exec.binaryExprToColumnPredicate(bin)
	if !ok {
		t.Fatal("期望 '25 != age' 可转换为列谓词")
	}
	if pred.Op != index.OpNotEqual {
		t.Errorf("期望 OpNotEqual，得到 %v", pred.Op)
	}
}

// TestBinaryExprToColumnPredicate_NullLiteral 测试 NULL 字面值不可转换为列谓词。
func TestBinaryExprToColumnPredicate_NullLiteral(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	// column op NULL
	bin1 := &BinaryExpr{
		Op:    OpEq,
		Left:  &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
		Right: &LiteralExpr{Value: common.NewNull()},
	}
	if _, ok := exec.binaryExprToColumnPredicate(bin1); ok {
		t.Error("期望 'age = NULL' 不可转换为列谓词")
	}

	// NULL op column
	bin2 := &BinaryExpr{
		Op:    OpEq,
		Left:  &LiteralExpr{Value: common.NewNull()},
		Right: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
	}
	if _, ok := exec.binaryExprToColumnPredicate(bin2); ok {
		t.Error("期望 'NULL = age' 不可转换为列谓词")
	}
}

// TestBinaryExprToColumnPredicate_UnsupportedOp 测试不支持的运算符不可转换。
func TestBinaryExprToColumnPredicate_UnsupportedOp(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	bin := &BinaryExpr{
		Op:    OpAdd,
		Left:  &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
		Right: &LiteralExpr{Value: common.NewInt64(1)},
	}
	if _, ok := exec.binaryExprToColumnPredicate(bin); ok {
		t.Error("期望 'age + 1' 不可转换为列谓词")
	}
}

// TestBinaryExprToColumnPredicate_NonColumnNonLiteral 测试非列非字面值不可转换。
func TestBinaryExprToColumnPredicate_NonColumnNonLiteral(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	// 两侧都不是列或字面值
	bin := &BinaryExpr{
		Op:    OpEq,
		Left:  &StarExpr{},
		Right: &StarExpr{},
	}
	if _, ok := exec.binaryExprToColumnPredicate(bin); ok {
		t.Error("期望两侧都不是列/字面值时不可转换")
	}
}

// ============================================================================
// extractColumnPredicates 覆盖率提升测试（原 81.8%）
// ============================================================================

// TestExtractColumnPredicates_NonBinaryConjunct 测试非二元表达式合取项被跳过。
func TestExtractColumnPredicates_NonBinaryConjunct(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	// age > 25 AND (非二元表达式)
	pred := &BinaryExpr{
		Op: OpAnd,
		Left: &BinaryExpr{
			Op:    OpGt,
			Left:  &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
			Right: &LiteralExpr{Value: common.NewInt64(25)},
		},
		Right: &StarExpr{}, // 非二元表达式
	}
	preds := exec.extractColumnPredicates(pred)
	if len(preds) != 1 {
		t.Fatalf("期望 1 个列谓词（跳过 StarExpr），得到 %d 个", len(preds))
	}
	if preds[0].ColumnName != testColAge {
		t.Errorf("期望列名 %q，得到 %q", testColAge, preds[0].ColumnName)
	}
}

// TestExtractColumnPredicates_MixedConjuncts 测试混合合取项（可转换和不可转换）。
func TestExtractColumnPredicates_MixedConjuncts(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	// age > 25 AND name = 'alice' AND id + 1 > 0
	pred := &BinaryExpr{
		Op: OpAnd,
		Left: &BinaryExpr{
			Op: OpAnd,
			Left: &BinaryExpr{
				Op:    OpGt,
				Left:  &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
				Right: &LiteralExpr{Value: common.NewInt64(25)},
			},
			Right: &BinaryExpr{
				Op:    OpEq,
				Left:  &ResolvedColumnExpr{Name: testColName, Idx: 1, typ: common.TypeString},
				Right: &LiteralExpr{Value: common.NewString(testNameAlice)},
			},
		},
		Right: &BinaryExpr{
			Op:    OpGt,
			Left:  &BinaryExpr{Op: OpAdd, Left: &ResolvedColumnExpr{Name: testColID, Idx: 0, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(1)}},
			Right: &LiteralExpr{Value: common.NewInt64(0)},
		},
	}
	preds := exec.extractColumnPredicates(pred)
	// 只有 age > 25 和 name = 'alice' 可转换，id + 1 > 0 不可转换（左侧不是 ResolvedColumnExpr）
	if len(preds) != 2 {
		t.Errorf("期望 2 个列谓词，得到 %d 个", len(preds))
	}
}

// TestExtractColumnPredicates_EmptyResult 测试无可转换谓词时返回空。
func TestExtractColumnPredicates_EmptyResult(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	// 仅包含不可转换的表达式
	pred := &BinaryExpr{
		Op:    OpAdd,
		Left:  &ResolvedColumnExpr{Name: testColID, Idx: 0, typ: common.TypeInt64},
		Right: &LiteralExpr{Value: common.NewInt64(1)},
	}
	preds := exec.extractColumnPredicates(pred)
	if len(preds) != 0 {
		t.Errorf("期望 0 个列谓词，得到 %d 个", len(preds))
	}
}

// ============================================================================
// evalComparisonOp 覆盖率提升测试（原 87.5%）
// ============================================================================

// TestEvalComparisonOpNe 测试 OpNe 比较运算。
func TestEvalComparisonOpNe(t *testing.T) {
	val, ok := evalComparisonOp(OpNe, common.NewInt64(1), common.NewInt64(2))
	if !ok {
		t.Fatal("期望 OpNe 可比较")
	}
	if !val.Valid || val.Int64 != 1 {
		t.Errorf("期望 1 != 2 = true，得到 %v", val)
	}

	val2, ok := evalComparisonOp(OpNe, common.NewInt64(1), common.NewInt64(1))
	if !ok {
		t.Fatal("期望 OpNe 可比较")
	}
	if !val2.Valid || val2.Int64 != 0 {
		t.Errorf("期望 1 != 1 = false，得到 %v", val2)
	}
}

// TestEvalComparisonOpLe 测试 OpLe 比较运算。
func TestEvalComparisonOpLe(t *testing.T) {
	val, ok := evalComparisonOp(OpLe, common.NewInt64(1), common.NewInt64(2))
	if !ok {
		t.Fatal("期望 OpLe 可比较")
	}
	if !val.Valid || val.Int64 != 1 {
		t.Errorf("期望 1 <= 2 = true，得到 %v", val)
	}

	val2, ok := evalComparisonOp(OpLe, common.NewInt64(3), common.NewInt64(2))
	if !ok {
		t.Fatal("期望 OpLe 可比较")
	}
	if !val2.Valid || val2.Int64 != 0 {
		t.Errorf("期望 3 <= 2 = false，得到 %v", val2)
	}

	val3, ok := evalComparisonOp(OpLe, common.NewInt64(2), common.NewInt64(2))
	if !ok {
		t.Fatal("期望 OpLe 可比较")
	}
	if !val3.Valid || val3.Int64 != 1 {
		t.Errorf("期望 2 <= 2 = true，得到 %v", val3)
	}
}

// TestEvalComparisonOpGe 测试 OpGe 比较运算。
func TestEvalComparisonOpGe(t *testing.T) {
	val, ok := evalComparisonOp(OpGe, common.NewInt64(2), common.NewInt64(1))
	if !ok {
		t.Fatal("期望 OpGe 可比较")
	}
	if !val.Valid || val.Int64 != 1 {
		t.Errorf("期望 2 >= 1 = true，得到 %v", val)
	}

	val2, ok := evalComparisonOp(OpGe, common.NewInt64(1), common.NewInt64(2))
	if !ok {
		t.Fatal("期望 OpGe 可比较")
	}
	if !val2.Valid || val2.Int64 != 0 {
		t.Errorf("期望 1 >= 2 = false，得到 %v", val2)
	}

	val3, ok := evalComparisonOp(OpGe, common.NewInt64(2), common.NewInt64(2))
	if !ok {
		t.Fatal("期望 OpGe 可比较")
	}
	if !val3.Valid || val3.Int64 != 1 {
		t.Errorf("期望 2 >= 2 = true，得到 %v", val3)
	}
}

// TestEvalComparisonOpUnsupported 测试不支持的运算符返回 false。
func TestEvalComparisonOpUnsupported(t *testing.T) {
	_, ok := evalComparisonOp(OpAdd, common.NewInt64(1), common.NewInt64(2))
	if ok {
		t.Error("期望 OpAdd 不可作为比较运算")
	}
}

// TestEvalComparisonOpStringComparison 测试字符串比较运算。
func TestEvalComparisonOpStringComparison(t *testing.T) {
	val, ok := evalComparisonOp(OpLt, common.NewString("a"), common.NewString("b"))
	if !ok {
		t.Fatal("期望 OpLt 可比较字符串")
	}
	if !val.Valid || val.Int64 != 1 {
		t.Errorf("期望 'a' < 'b' = true，得到 %v", val)
	}
}

// TestEvalComparisonOpFloatComparison 测试浮点数比较运算。
func TestEvalComparisonOpFloatComparison(t *testing.T) {
	val, ok := evalComparisonOp(OpGt, common.NewFloat64(3.14), common.NewFloat64(2.71))
	if !ok {
		t.Fatal("期望 OpGt 可比较浮点数")
	}
	if !val.Valid || val.Int64 != 1 {
		t.Errorf("期望 3.14 > 2.71 = true，得到 %v", val)
	}
}

// ============================================================================
// appendValueSafe 覆盖率提升测试（原 85.7%）
// ============================================================================

// TestAppendValueSafe_NullFallback 测试类型不匹配且强制转换也失败时用 NULL 填充。
func TestAppendValueSafe_NullFallback(t *testing.T) {
	col := storage.NewColumnVector(0, common.TypeInt64, 4)
	// 尝试向 INT64 列追加 STRING 值：Append 失败 -> coerceValue 失败 -> 追加 NULL
	appendValueSafe(col, common.NewString("not_a_number"), common.TypeInt64)
	// 应该追加了 NULL
	if col.Len() != 1 {
		t.Fatalf("期望列长度 1，得到 %d", col.Len())
	}
	val := col.GetValue(0)
	if val.Valid {
		t.Errorf("期望 NULL（字符串无法转为 INT64），得到 %v", val)
	}
}

// TestAppendValueSafe_DirectAppend 测试直接追加成功路径。
func TestAppendValueSafe_DirectAppend(t *testing.T) {
	col := storage.NewColumnVector(0, common.TypeInt64, 4)
	appendValueSafe(col, common.NewInt64(42), common.TypeInt64)
	if col.Len() != 1 {
		t.Fatalf("期望列长度 1，得到 %d", col.Len())
	}
	val := col.GetValue(0)
	if val.Int64 != 42 {
		t.Errorf("期望值 42，得到 %d", val.Int64)
	}
}

// TestAppendValueSafe_CoerceSuccess 测试强制转换成功路径。
func TestAppendValueSafe_CoerceSuccess(t *testing.T) {
	col := storage.NewColumnVector(0, common.TypeFloat64, 4)
	// INT64 追加到 FLOAT64 列：Append 失败 -> coerceValue 成功
	appendValueSafe(col, common.NewInt64(42), common.TypeFloat64)
	if col.Len() != 1 {
		t.Fatalf("期望列长度 1，得到 %d", col.Len())
	}
	val := col.GetValue(0)
	if val.Float64 != 42.0 {
		t.Errorf("期望值 42.0，得到 %g", val.Float64)
	}
}

// ============================================================================
// executeScan 覆盖率提升测试（原 87.5%）
// ============================================================================

// TestExecuteScan_WithColumnPredicatePruning 测试带列谓词的扫描路径。
// 覆盖 scanWithPredicate 中 len(columnPreds) > 0 的分支。
func TestExecuteScan_WithColumnPredicatePruning(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})
	ms.addEntry("b", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
		testColAge: common.NewInt64(25), testColScore: common.NewFloat64(88.0),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		Predicate: &BinaryExpr{
			Op:    OpGt,
			Left:  &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
			Right: &LiteralExpr{Value: common.NewInt64(28)},
		},
		schema: buildTestSchema(),
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(scan)
	if err != nil {
		t.Fatalf("带列谓词扫描: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 1 {
		t.Errorf("期望 1 行（age > 28），得到 %d", totalRows)
	}
}

// TestExecuteScan_LiteralOpColumnPredicate 测试 "literal op column" 形式的谓词。
// 覆盖 scanWithPredicate 中 queryOpToIndexOpFlip 路径。
func TestExecuteScan_LiteralOpColumnPredicate(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})
	ms.addEntry("b", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
		testColAge: common.NewInt64(25), testColScore: common.NewFloat64(88.0),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		// 28 < age 等价于 age > 28
		Predicate: &BinaryExpr{
			Op:    OpLt,
			Left:  &LiteralExpr{Value: common.NewInt64(28)},
			Right: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
		},
		schema: buildTestSchema(),
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(scan)
	if err != nil {
		t.Fatalf("literal op column 扫描: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 1 {
		t.Errorf("期望 1 行（28 < age 即 age > 28），得到 %d", totalRows)
	}
}

// ============================================================================
// extractKeyRange 覆盖率提升测试
// ============================================================================

// TestExtractKeyRange_EqKeyRange 测试 OpEq 缩小主键范围。
func TestExtractKeyRange_EqKeyRange(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	pred := &BinaryExpr{
		Op:    OpEq,
		Left:  &ResolvedColumnExpr{Name: testColID, Idx: 0, typ: common.TypeInt64},
		Right: &LiteralExpr{Value: common.NewString("b")},
	}
	kr := exec.extractKeyRange(pred)
	if kr.start != "b" {
		t.Errorf("期望 start='b'，得到 %q", kr.start)
	}
	if kr.end != "b" {
		t.Errorf("期望 end='b'，得到 %q", kr.end)
	}
}

// TestExtractKeyRange_NonKeyColumn 测试非主键列不影响键范围。
func TestExtractKeyRange_NonKeyColumn(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	pred := &BinaryExpr{
		Op:    OpGt,
		Left:  &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
		Right: &LiteralExpr{Value: common.NewInt64(25)},
	}
	kr := exec.extractKeyRange(pred)
	if kr.start != "" {
		t.Errorf("期望 start=''（非主键列不影响范围），得到 %q", kr.start)
	}
	if kr.end != testDefaultKeyRangeEnd {
		t.Errorf("期望 end=默认最大值，得到 %q", kr.end)
	}
}

// TestExtractKeyRange_NullLiteral 测试 NULL 字面值不影响键范围。
func TestExtractKeyRange_NullLiteral(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	pred := &BinaryExpr{
		Op:    OpGt,
		Left:  &ResolvedColumnExpr{Name: testColID, Idx: 0, typ: common.TypeInt64},
		Right: &LiteralExpr{Value: common.NewNull()},
	}
	kr := exec.extractKeyRange(pred)
	if kr.start != "" {
		t.Errorf("期望 start=''（NULL 不影响范围），得到 %q", kr.start)
	}
}

// TestExtractKeyRange_NonBinaryExpr 测试非二元表达式不影响键范围。
func TestExtractKeyRange_NonBinaryExpr(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	// AND 连接中包含非二元表达式
	pred := &BinaryExpr{
		Op:    OpAnd,
		Left:  &StarExpr{},
		Right: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColID, Idx: 0, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewString("a")}},
	}
	kr := exec.extractKeyRange(pred)
	if kr.start != "a" {
		t.Errorf("期望 start='a'，得到 %q", kr.start)
	}
}

// ============================================================================
// common.Value.Less 覆盖率提升测试（原 90%）
// ============================================================================

// TestValueLess_Timestamp 测试 Timestamp 类型的比较。
func TestValueLess_Timestamp(t *testing.T) {
	t1 := common.NewTimestamp(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	t2 := common.NewTimestamp(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	if !t1.Less(t2) {
		t.Error("期望 t1 < t2")
	}
	if t2.Less(t1) {
		t.Error("不期望 t2 < t1")
	}
	if t1.Less(t1) {
		t.Error("不期望 t1 < t1（相等）")
	}
}

// TestValueLess_DifferentTypes 测试不同类型比较的语义。
// FLOAT64 与整数族跨类型按 float64 数值比较；非数值跨类型返回 false。
func TestValueLess_DifferentTypes(t *testing.T) {
	if !common.NewInt64(1).Less(common.NewFloat64(2.0)) {
		t.Error("期望 INT64(1) < FLOAT64(2.0) 跨类型数值比较返回 true")
	}
	if common.NewString("a").Less(common.NewInt64(1)) {
		t.Error("期望 STRING 与 INT64 非数值跨类型比较返回 false")
	}
}

// TestValueLess_UnknownType 测试未知类型比较返回 false。
func TestValueLess_UnknownType(t *testing.T) {
	v1 := common.Value{Typ: common.DataType(99), Valid: true}
	v2 := common.Value{Typ: common.DataType(99), Valid: true}
	if v1.Less(v2) {
		t.Error("期望未知类型比较返回 false")
	}
}

// ============================================================================
// isTruthyValue 覆盖率提升测试
// ============================================================================

// TestIsTruthyValue_Bool 测试布尔值的真假判断。
func TestIsTruthyValue_Bool(t *testing.T) {
	if !isTruthyValue(common.NewBool(true)) {
		t.Error("期望 true 为 truthy")
	}
	if isTruthyValue(common.NewBool(false)) {
		t.Error("期望 false 不为 truthy")
	}
}

// TestIsTruthyValue_Float64 测试浮点数的真假判断。
func TestIsTruthyValue_Float64(t *testing.T) {
	if !isTruthyValue(common.NewFloat64(1.0)) {
		t.Error("期望 1.0 为 truthy")
	}
	if isTruthyValue(common.NewFloat64(0.0)) {
		t.Error("期望 0.0 不为 truthy")
	}
}

// TestIsTruthyValue_String 测试字符串的真假判断。
func TestIsTruthyValue_String(t *testing.T) {
	if !isTruthyValue(common.NewString("hello")) {
		t.Error("期望非空字符串为 truthy")
	}
	if isTruthyValue(common.NewString("")) {
		t.Error("期望空字符串不为 truthy")
	}
}

// TestIsTruthyValue_Null 测试 NULL 不为 truthy。
func TestIsTruthyValue_Null(t *testing.T) {
	if isTruthyValue(common.NewNull()) {
		t.Error("期望 NULL 不为 truthy")
	}
}
