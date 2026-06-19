package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// 本文件补充 query 包中供 server 层 UPDATE/DELETE 使用的行级表达式求值函数
// （EvalRowPredicate / EvalExprOnRow）的直接单元测试。
// 这两个导出函数此前仅被 pkg/server 测试间接调用，在 query 包内覆盖率为 0%。
// 未支持表达式类型 unsupportedTestExpr 复用 executor_expr_coverage_test.go 中的定义。

// --- EvalExprOnRow ---

func TestEvalExprOnRowLiteral(t *testing.T) {
	got, err := EvalExprOnRow(&LiteralExpr{Value: common.NewInt64(42)}, nil)
	if err != nil {
		t.Fatalf("未期望错误: %v", err)
	}
	if got.Typ != common.TypeInt64 || got.Int64 != 42 {
		t.Errorf("EvalExprOnRow(字面量) = %+v, 期望 Int64=42", got)
	}
}

func TestEvalExprOnRowColumnRef(t *testing.T) {
	row := map[string]common.Value{"id": common.NewInt64(7)}
	got, err := EvalExprOnRow(&ColumnExpr{Name: "id"}, row)
	if err != nil {
		t.Fatalf("未期望错误: %v", err)
	}
	if got.Int64 != 7 {
		t.Errorf("EvalExprOnRow(列引用) = %+v, 期望 Int64=7", got)
	}
}

func TestEvalExprOnRowMissingColumnReturnsNull(t *testing.T) {
	row := map[string]common.Value{"id": common.NewInt64(7)}
	got, err := EvalExprOnRow(&ColumnExpr{Name: "missing"}, row)
	if err != nil {
		t.Fatalf("未期望错误: %v", err)
	}
	if got.Valid {
		t.Errorf("缺失列应返回 NULL, 得到 %+v", got)
	}
}

func TestEvalExprOnRowUnsupportedReturnsError(t *testing.T) {
	if _, err := EvalExprOnRow(&unsupportedTestExpr{}, nil); err == nil {
		t.Error("未支持表达式应返回错误")
	}
}

// --- EvalRowPredicate ---

func TestEvalRowPredicateNilExprMatchesAll(t *testing.T) {
	if !EvalRowPredicate(nil, nil) {
		t.Error("nil 谓词应匹配所有行")
	}
}

func TestEvalRowPredicateComparison(t *testing.T) {
	row := map[string]common.Value{"age": common.NewInt64(30)}

	eq := &BinaryExpr{Op: OpEq, Left: &ColumnExpr{Name: "age"}, Right: &LiteralExpr{Value: common.NewInt64(30)}}
	if !EvalRowPredicate(eq, row) {
		t.Error("age = 30 应匹配")
	}

	ne := &BinaryExpr{Op: OpNe, Left: &ColumnExpr{Name: "age"}, Right: &LiteralExpr{Value: common.NewInt64(30)}}
	if EvalRowPredicate(ne, row) {
		t.Error("age != 30 不应匹配")
	}

	gt := &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: "age"}, Right: &LiteralExpr{Value: common.NewInt64(40)}}
	if EvalRowPredicate(gt, row) {
		t.Error("age > 40 不应匹配")
	}
}

func TestEvalRowPredicateLogicalAndOr(t *testing.T) {
	row := map[string]common.Value{"age": common.NewInt64(30)}

	// (age > 20) AND (age < 40) → true
	cond := &BinaryExpr{
		Op:    OpAnd,
		Left:  &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: "age"}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
		Right: &BinaryExpr{Op: OpLt, Left: &ColumnExpr{Name: "age"}, Right: &LiteralExpr{Value: common.NewInt64(40)}},
	}
	if !EvalRowPredicate(cond, row) {
		t.Error("(age>20 AND age<40) 应匹配 age=30")
	}

	// (age > 40) OR (age < 35) → true
	cond = &BinaryExpr{
		Op:    OpOr,
		Left:  &BinaryExpr{Op: OpGt, Left: &ColumnExpr{Name: "age"}, Right: &LiteralExpr{Value: common.NewInt64(40)}},
		Right: &BinaryExpr{Op: OpLt, Left: &ColumnExpr{Name: "age"}, Right: &LiteralExpr{Value: common.NewInt64(35)}},
	}
	if !EvalRowPredicate(cond, row) {
		t.Error("(age>40 OR age<35) 应匹配 age=30")
	}
}

func TestEvalRowPredicateErrorExprIsFalsy(t *testing.T) {
	// 未支持表达式触发错误，谓词应返回 false
	if EvalRowPredicate(&unsupportedTestExpr{}, nil) {
		t.Error("求值出错时谓词应返回 false")
	}
}

func TestEvalRowPredicateTruthyNonBool(t *testing.T) {
	// 非布尔字面量按真值判断：非零整数视为真
	row := map[string]common.Value{}
	if !EvalRowPredicate(&LiteralExpr{Value: common.NewInt64(5)}, row) {
		t.Error("非零整数字面量应视为真")
	}
	if EvalRowPredicate(&LiteralExpr{Value: common.NewInt64(0)}, row) {
		t.Error("零值整数字面量应视为假")
	}
}

func TestEvalRowPredicateNullIsFalsy(t *testing.T) {
	if EvalRowPredicate(&LiteralExpr{Value: common.NewNull()}, nil) {
		t.Error("NULL 字面量应视为假")
	}
}
