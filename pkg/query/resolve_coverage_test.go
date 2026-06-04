package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// unsupportedExpr 已在 analyzer_error_test.go 中定义，此处直接使用。

// TestResolveBinaryExprLeftError 测试 resolveBinaryExpr 左子表达式解析失败时返回错误。
func TestResolveBinaryExprLeftError(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	table, _ := testCatalog().GetTable(testTableUsers)

	// 左子表达式引用不存在的列，应返回错误
	expr := &BinaryExpr{
		Op:    OpAnd,
		Left:  &ColumnExpr{Name: testColNonexistent},
		Right: &LiteralExpr{Value: common.NewInt64(1)},
	}

	_, err := analyzer.resolveBinaryExpr(expr, table)
	if err == nil {
		t.Fatal("期望 resolveBinaryExpr 左子表达式失败时返回错误，但得到了 nil")
	}
}

// TestResolveBinaryExprRightError 测试 resolveBinaryExpr 右子表达式解析失败时返回错误。
func TestResolveBinaryExprRightError(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	table, _ := testCatalog().GetTable(testTableUsers)

	// 右子表达式引用不存在的列，应返回错误
	expr := &BinaryExpr{
		Op:    OpAnd,
		Left:  &LiteralExpr{Value: common.NewInt64(1)},
		Right: &ColumnExpr{Name: testColNonexistent},
	}

	_, err := analyzer.resolveBinaryExpr(expr, table)
	if err == nil {
		t.Fatal("期望 resolveBinaryExpr 右子表达式失败时返回错误，但得到了 nil")
	}
}

// TestResolveBinaryExprNoTableLeftError 测试 resolveBinaryExprNoTable 左子表达式解析失败时返回错误。
func TestResolveBinaryExprNoTableLeftError(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())

	// 左子表达式为 ColumnExpr，在无表上下文中应返回错误
	expr := &BinaryExpr{
		Op:    OpOr,
		Left:  &ColumnExpr{Name: testColAge},
		Right: &LiteralExpr{Value: common.NewInt64(1)},
	}

	_, err := analyzer.resolveBinaryExprNoTable(expr)
	if err == nil {
		t.Fatal("期望 resolveBinaryExprNoTable 左子表达式失败时返回错误，但得到了 nil")
	}
}

// TestResolveBinaryExprNoTableRightError 测试 resolveBinaryExprNoTable 右子表达式解析失败时返回错误。
func TestResolveBinaryExprNoTableRightError(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())

	// 右子表达式为 ColumnExpr，在无表上下文中应返回错误
	expr := &BinaryExpr{
		Op:    OpOr,
		Left:  &LiteralExpr{Value: common.NewInt64(1)},
		Right: &ColumnExpr{Name: testColAge},
	}

	_, err := analyzer.resolveBinaryExprNoTable(expr)
	if err == nil {
		t.Fatal("期望 resolveBinaryExprNoTable 右子表达式失败时返回错误，但得到了 nil")
	}
}

// TestResolveUnaryExprInnerError 测试 resolveUnaryExpr 内部表达式解析失败时返回错误。
func TestResolveUnaryExprInnerError(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	table, _ := testCatalog().GetTable(testTableUsers)

	// 内部表达式引用不存在的列，应返回错误
	expr := &UnaryExpr{
		Op:   OpNot,
		Expr: &ColumnExpr{Name: testColNonexistent},
	}

	_, err := analyzer.resolveUnaryExpr(expr, table)
	if err == nil {
		t.Fatal("期望 resolveUnaryExpr 内部表达式失败时返回错误，但得到了 nil")
	}
}

// TestResolveColumnRefNotExist 测试 resolveColumnRef 列不存在时返回错误。
func TestResolveColumnRefNotExist(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	table, _ := testCatalog().GetTable(testTableUsers)

	col := &ColumnExpr{Name: testColNonexistent}
	_, err := analyzer.resolveColumnRef(col, table)
	if err == nil {
		t.Fatal("期望 resolveColumnRef 列不存在时返回错误，但得到了 nil")
	}
}

// TestResolveExprUnsupportedType 测试 resolveExpr 遇到不支持的表达式类型时返回错误。
func TestResolveExprUnsupportedType(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	table, _ := testCatalog().GetTable(testTableUsers)

	_, err := analyzer.resolveExpr(&unsupportedExpr{}, table)
	if err == nil {
		t.Fatal("期望 resolveExpr 不支持的类型返回错误，但得到了 nil")
	}
}

// TestResolveExprNoTableColumnExpr 测试 resolveExprNoTable 遇到 ColumnExpr 时返回错误。
func TestResolveExprNoTableColumnExpr(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())

	_, err := analyzer.resolveExprNoTable(&ColumnExpr{Name: testColAge})
	if err == nil {
		t.Fatal("期望 resolveExprNoTable ColumnExpr 返回错误，但得到了 nil")
	}
}

// TestResolveExprNoTableUnsupportedType 测试 resolveExprNoTable 遇到不支持的表达式类型时返回错误。
func TestResolveExprNoTableUnsupportedType(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())

	_, err := analyzer.resolveExprNoTable(&unsupportedExpr{})
	if err == nil {
		t.Fatal("期望 resolveExprNoTable 不支持的类型返回错误，但得到了 nil")
	}
}

// TestResolveFuncExprArgError 测试 resolveFuncExpr 参数解析失败时返回错误。
func TestResolveFuncExprArgError(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	table, _ := testCatalog().GetTable(testTableUsers)

	// 函数参数引用不存在的列，应返回错误
	expr := &FuncExpr{
		Name: testFuncAbs,
		Args: []Expression{&ColumnExpr{Name: testColNonexistent}},
	}

	_, err := analyzer.resolveFuncExpr(expr, table)
	if err == nil {
		t.Fatal("期望 resolveFuncExpr 参数解析失败时返回错误，但得到了 nil")
	}
}

// TestResolveFuncExprNoTableArgError 测试 resolveFuncExprNoTable 参数解析失败时返回错误。
func TestResolveFuncExprNoTableArgError(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())

	// 函数参数为 ColumnExpr，在无表上下文中应返回错误
	expr := &FuncExpr{
		Name: testFuncAbs,
		Args: []Expression{&ColumnExpr{Name: testColAge}},
	}

	_, err := analyzer.resolveFuncExprNoTable(expr)
	if err == nil {
		t.Fatal("期望 resolveFuncExprNoTable 参数解析失败时返回错误，但得到了 nil")
	}
}

// TestConvertBinaryExprLeftError 测试 convertBinaryExpr 左表达式转换失败时返回错误。
// 使用 IS NULL（sqlparser.IsExpr）作为 AND 的左子表达式，
// IsExpr 不在 convertExpr 支持的类型中，会触发 default 分支返回错误，
// 从而覆盖 convertBinaryExpr 中 left 转换失败的路径。
func TestConvertBinaryExprLeftError(t *testing.T) {
	p := NewParser()
	_, err := p.Parse("SELECT 1 FROM dual WHERE name IS NULL AND id = 1")
	if err == nil {
		t.Fatal("期望 IS NULL AND 表达式解析失败，但得到了 nil")
	}
}

// TestConvertBinaryExprRightError 测试 convertBinaryExpr 右表达式转换失败时返回错误。
// 使用 BETWEEN（sqlparser.RangeCond）作为 OR 的左子表达式，
// RangeCond 不在 convertExpr 支持的类型中，会触发 default 分支返回错误，
// 从而覆盖 convertBinaryExpr 中 right 转换失败的路径。
func TestConvertBinaryExprRightError(t *testing.T) {
	p := NewParser()
	_, err := p.Parse("SELECT 1 FROM dual WHERE id = 1 OR age BETWEEN 10 AND 20")
	if err == nil {
		t.Fatal("期望 OR BETWEEN 表达式解析失败，但得到了 nil")
	}
}

// TestConvertComparisonExprUnsupportedOp 测试 convertComparisonOp 不支持的比较运算符返回错误。
// 通过直接调用 convertComparisonOp 来触发 default 分支。
func TestConvertComparisonExprUnsupportedOp(t *testing.T) {
	p := NewParser()
	_, err := p.convertComparisonOp("NOTREGEX")
	if err == nil {
		t.Fatal("期望 convertComparisonOp 不支持的运算符返回错误，但得到了 nil")
	}
}

// TestConvertComparisonExprLeftError 测试 convertComparisonExpr 左表达式转换失败时返回错误。
// 使用 NOT REGEXP 运算符，sqlparser 会将其解析为 ComparisonExpr，
// 其运算符不在支持列表中，会触发 convertComparisonOp 错误，
// 从而覆盖 convertComparisonExpr 中的错误路径。
func TestConvertComparisonExprLeftError(t *testing.T) {
	p := NewParser()
	_, err := p.Parse("SELECT 1 FROM dual WHERE name NOT REGEXP 'a'")
	if err == nil {
		t.Fatal("期望 NOT REGEXP 运算符解析失败，但得到了 nil")
	}
}

// TestConvertComparisonExprRightError 测试 convertComparisonExpr 右表达式转换失败时返回错误。
// 使用 IN 运算符，sqlparser 会解析为 ComparisonExpr，
// 但 IN 不是我们支持的运算符，会触发 convertComparisonOp 错误。
func TestConvertComparisonExprRightError(t *testing.T) {
	p := NewParser()
	_, err := p.Parse("SELECT 1 FROM dual WHERE id IN (1, 2)")
	if err == nil {
		t.Fatal("期望 IN 运算符解析失败，但得到了 nil")
	}
}
