package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestEvalExprStarExpr 测试 evalExpr 对 StarExpr 的求值返回 NULL
func TestEvalExprStarExpr(t *testing.T) {
	row := map[string]common.Value{testColID: common.NewInt64(1)}
	colIdxMap := map[string]int{testColID: 0}

	val, err := evalExpr(&StarExpr{}, row, colIdxMap)
	if err != nil {
		t.Fatalf("evalExpr(StarExpr) returned error: %v", err)
	}
	if val.Valid {
		t.Errorf("expected NULL from StarExpr, got %v", val)
	}
}

// TestEvalExprUnsupportedType 测试 evalExpr 对不支持的表达式类型返回错误
type unsupportedTestExpr struct{}

func (e *unsupportedTestExpr) exprNode()      {}
func (e *unsupportedTestExpr) String() string { return "unsupported" }

func TestEvalExprUnsupportedType(t *testing.T) {
	row := map[string]common.Value{testColID: common.NewInt64(1)}
	colIdxMap := map[string]int{testColID: 0}

	val, err := evalExpr(&unsupportedTestExpr{}, row, colIdxMap)
	if err == nil {
		t.Fatal("expected error for unsupported expression type, got nil")
	}
	if val.Valid {
		t.Errorf("expected NULL value for unsupported type, got %v", val)
	}
}

// TestEvalExprColumnExprMissingColumn 测试 evalExpr 对 ColumnExpr 引用不存在的列返回 NULL
func TestEvalExprColumnExprMissingColumn(t *testing.T) {
	row := map[string]common.Value{testColID: common.NewInt64(1)}
	colIdxMap := map[string]int{testColID: 0}

	val, err := evalExpr(&ColumnExpr{Name: testColNonexistent}, row, colIdxMap)
	if err != nil {
		t.Fatalf("evalExpr(ColumnExpr with missing column) returned error: %v", err)
	}
	if val.Valid {
		t.Errorf("expected NULL for missing column, got %v", val)
	}
}

// TestEvalExprResolvedColumnExprMissingColumn 测试 evalExpr 对 ResolvedColumnExpr 引用不存在的列返回 NULL
func TestEvalExprResolvedColumnExprMissingColumn(t *testing.T) {
	row := map[string]common.Value{testColID: common.NewInt64(1)}
	colIdxMap := map[string]int{testColID: 0}

	val, err := evalExpr(&ResolvedColumnExpr{Name: testColNonexistent, Idx: 99, typ: common.TypeInt64}, row, colIdxMap)
	if err != nil {
		t.Fatalf("evalExpr(ResolvedColumnExpr with missing column) returned error: %v", err)
	}
	if val.Valid {
		t.Errorf("expected NULL for missing column, got %v", val)
	}
}

// TestEvalBinaryExprAndRightError 测试 AND 运算符右表达式求值出错
func TestEvalBinaryExprAndRightError(t *testing.T) {
	row := map[string]common.Value{testColID: common.NewInt64(1)}
	colIdxMap := map[string]int{testColID: 0}

	// 左侧为 truthy，右侧为不支持的表达式类型
	expr := &BinaryExpr{
		Op:    OpAnd,
		Left:  &LiteralExpr{Value: common.NewInt64(1)},
		Right: &unsupportedTestExpr{},
	}
	val, err := evalExpr(expr, row, colIdxMap)
	if err == nil {
		t.Fatal("expected error from AND with unsupported right expression, got nil")
	}
	if val.Valid {
		t.Errorf("expected NULL value, got %v", val)
	}
}

// TestEvalBinaryExprOrRightError 测试 OR 运算符右表达式求值出错
func TestEvalBinaryExprOrRightError(t *testing.T) {
	row := map[string]common.Value{testColID: common.NewInt64(0)}
	colIdxMap := map[string]int{testColID: 0}

	// 左侧为 falsy，右侧为不支持的表达式类型
	expr := &BinaryExpr{
		Op:    OpOr,
		Left:  &LiteralExpr{Value: common.NewInt64(0)},
		Right: &unsupportedTestExpr{},
	}
	val, err := evalExpr(expr, row, colIdxMap)
	if err == nil {
		t.Fatal("expected error from OR with unsupported right expression, got nil")
	}
	if val.Valid {
		t.Errorf("expected NULL value, got %v", val)
	}
}

// TestEvalBinaryExprLeftError 测试二元表达式左子表达式求值出错
func TestEvalBinaryExprLeftError(t *testing.T) {
	row := map[string]common.Value{testColID: common.NewInt64(1)}
	colIdxMap := map[string]int{testColID: 0}

	expr := &BinaryExpr{
		Op:    OpAdd,
		Left:  &unsupportedTestExpr{},
		Right: &LiteralExpr{Value: common.NewInt64(1)},
	}
	val, err := evalExpr(expr, row, colIdxMap)
	if err == nil {
		t.Fatal("expected error from binary expr with unsupported left expression, got nil")
	}
	if val.Valid {
		t.Errorf("expected NULL value, got %v", val)
	}
}

// TestEvalBinaryExprRightError 测试二元表达式右子表达式求值出错
func TestEvalBinaryExprRightError(t *testing.T) {
	row := map[string]common.Value{testColID: common.NewInt64(1)}
	colIdxMap := map[string]int{testColID: 0}

	expr := &BinaryExpr{
		Op:    OpAdd,
		Left:  &LiteralExpr{Value: common.NewInt64(1)},
		Right: &unsupportedTestExpr{},
	}
	val, err := evalExpr(expr, row, colIdxMap)
	if err == nil {
		t.Fatal("expected error from binary expr with unsupported right expression, got nil")
	}
	if val.Valid {
		t.Errorf("expected NULL value, got %v", val)
	}
}

// TestEvalUnaryExprUnsupportedOp 测试不支持的一元运算符返回错误
func TestEvalUnaryExprUnsupportedOp(t *testing.T) {
	row := map[string]common.Value{testColID: common.NewInt64(1)}
	colIdxMap := map[string]int{testColID: 0}

	// 使用一个无效的一元运算符（BinaryOp 被误用为 UnaryOp）
	expr := &UnaryExpr{
		Op:   UnaryOp(99), // 无效的一元运算符
		Expr: &LiteralExpr{Value: common.NewInt64(1)},
	}
	val, err := evalExpr(expr, row, colIdxMap)
	if err == nil {
		t.Fatal("expected error for unsupported unary op, got nil")
	}
	if val.Valid {
		t.Errorf("expected NULL value, got %v", val)
	}
}

// TestEvalUnaryExprInnerError 测试一元表达式内部求值出错
func TestEvalUnaryExprInnerError(t *testing.T) {
	row := map[string]common.Value{testColID: common.NewInt64(1)}
	colIdxMap := map[string]int{testColID: 0}

	expr := &UnaryExpr{
		Op:   OpNot,
		Expr: &unsupportedTestExpr{},
	}
	val, err := evalExpr(expr, row, colIdxMap)
	if err == nil {
		t.Fatal("expected error from unary expr with unsupported inner expression, got nil")
	}
	if val.Valid {
		t.Errorf("expected NULL value, got %v", val)
	}
}

// TestEvalUnaryNegNonNumeric 测试对非数值类型取反
func TestEvalUnaryNegNonNumeric(t *testing.T) {
	row := map[string]common.Value{testColID: common.NewInt64(1)}
	colIdxMap := map[string]int{testColID: 0}

	// 对字符串取反应该返回错误
	expr := &UnaryExpr{
		Op:   OpNeg,
		Expr: &LiteralExpr{Value: common.NewString("hello")},
	}
	val, err := evalExpr(expr, row, colIdxMap)
	if err == nil {
		t.Fatal("expected error for negating non-numeric type, got nil")
	}
	if val.Valid {
		t.Errorf("expected NULL value, got %v", val)
	}
}

// TestEvalArithmeticUnsupportedOp 测试不支持的算术运算符返回错误
func TestEvalArithmeticUnsupportedOp(t *testing.T) {
	row := map[string]common.Value{testColID: common.NewInt64(1)}
	colIdxMap := map[string]int{testColID: 0}

	// 使用一个无效的二元运算符
	expr := &BinaryExpr{
		Op:    BinaryOp(99), // 不支持的运算符
		Left:  &LiteralExpr{Value: common.NewInt64(1)},
		Right: &LiteralExpr{Value: common.NewInt64(2)},
	}
	val, err := evalExpr(expr, row, colIdxMap)
	if err == nil {
		t.Fatal("expected error for unsupported binary op, got nil")
	}
	if val.Valid {
		t.Errorf("expected NULL value, got %v", val)
	}
}

// TestEvalExprFuncExprError 测试 evalExpr 对 FuncExpr 返回错误
func TestEvalExprFuncExprError(t *testing.T) {
	row := map[string]common.Value{testColID: common.NewInt64(1)}
	colIdxMap := map[string]int{testColID: 0}

	expr := &FuncExpr{Name: "unknown_func", Args: nil}
	val, err := evalExpr(expr, row, colIdxMap)
	if err == nil {
		t.Fatal("expected error for FuncExpr in row eval, got nil")
	}
	if val.Valid {
		t.Errorf("expected NULL value, got %v", val)
	}
}

// TestEvalBinaryExprNullOperands 测试二元表达式中 NULL 操作数的比较
func TestEvalBinaryExprNullOperands(t *testing.T) {
	row := map[string]common.Value{testColID: common.NewInt64(1)}
	colIdxMap := map[string]int{testColID: 0}

	// 左侧为 NULL 的比较
	expr := &BinaryExpr{
		Op:    OpEq,
		Left:  &LiteralExpr{Value: common.NewNull()},
		Right: &LiteralExpr{Value: common.NewInt64(1)},
	}
	val, err := evalExpr(expr, row, colIdxMap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val.Valid {
		t.Errorf("expected NULL for comparison with NULL operand, got %v", val)
	}
}

// TestEvalIntDivByZero 测试整数除以零返回 NULL
func TestEvalIntDivByZero(t *testing.T) {
	row := map[string]common.Value{testColID: common.NewInt64(1)}
	colIdxMap := map[string]int{testColID: 0}

	expr := &BinaryExpr{
		Op:    OpDiv,
		Left:  &LiteralExpr{Value: common.NewInt64(10)},
		Right: &LiteralExpr{Value: common.NewInt64(0)},
	}
	val, err := evalExpr(expr, row, colIdxMap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val.Valid {
		t.Errorf("expected NULL for integer division by zero, got %v", val)
	}
}

// TestEvalFloatDivByZero 测试浮点数除以零返回 NULL
func TestEvalFloatDivByZero(t *testing.T) {
	row := map[string]common.Value{testColID: common.NewInt64(1)}
	colIdxMap := map[string]int{testColID: 0}

	expr := &BinaryExpr{
		Op:    OpDiv,
		Left:  &LiteralExpr{Value: common.NewFloat64(10.0)},
		Right: &LiteralExpr{Value: common.NewFloat64(0.0)},
	}
	val, err := evalExpr(expr, row, colIdxMap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val.Valid {
		t.Errorf("expected NULL for float division by zero, got %v", val)
	}
}
