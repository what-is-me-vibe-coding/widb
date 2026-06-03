package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestIsTruthy 测试 isTruthy 函数的各种输入。
func TestIsTruthy(t *testing.T) {
	tests := []struct {
		name  string
		input common.Value
		want  bool
	}{
		// 无效值（NULL）
		{"null值返回false", common.NewNull(), false},
		// Bool 类型
		{"bool true返回true", common.NewBool(true), true},
		{"bool false返回false", common.NewBool(false), false},
		// Int64 类型
		{"int64非零返回true", common.NewInt64(42), true},
		{"int64零返回false", common.NewInt64(0), false},
		{"int64负数返回true", common.NewInt64(-1), true},
		// Float64 类型
		{"float64非零返回true", common.NewFloat64(3.14), true},
		{"float64零返回false", common.NewFloat64(0.0), false},
		{"float64负数返回true", common.NewFloat64(-1.5), true},
		// String 类型
		{"非空字符串返回true", common.NewString("hello"), true},
		{"空字符串返回false", common.NewString(""), false},
		// 其他类型（TypeTimestamp 等）默认返回 true
		{"timestamp有效值返回true", common.NewTimestamp(common.NewInt64(0).Time), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTruthy(tt.input)
			if got != tt.want {
				t.Errorf("isTruthy(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestIsTruthyValue 测试 isTruthyValue 函数的各种输入。
func TestIsTruthyValue(t *testing.T) {
	tests := []struct {
		name  string
		input common.Value
		want  bool
	}{
		// 无效值（NULL）
		{"null值返回false", common.NewNull(), false},
		// Bool 类型
		{"bool true返回true", common.NewBool(true), true},
		{"bool false返回false", common.NewBool(false), false},
		// Int64 类型
		{"int64非零返回true", common.NewInt64(42), true},
		{"int64零返回false", common.NewInt64(0), false},
		// Float64 类型
		{"float64非零返回true", common.NewFloat64(3.14), true},
		{"float64零返回false", common.NewFloat64(0.0), false},
		// String 类型
		{"非空字符串返回true", common.NewString("hello"), true},
		{"空字符串返回false", common.NewString(""), false},
		// 其他类型默认返回 true
		{"timestamp有效值返回true", common.NewTimestamp(common.NewInt64(0).Time), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTruthyValue(tt.input)
			if got != tt.want {
				t.Errorf("isTruthyValue(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestCoerceValue 测试 coerceValue 函数的类型转换。
func TestCoerceValue(t *testing.T) {
	tests := []struct {
		name   string
		input  common.Value
		target common.DataType
		want   common.Value
	}{
		// NULL 值始终返回 NULL
		{"null转int64返回null", common.NewNull(), common.TypeInt64, common.NewNull()},
		// 相同类型直接返回
		{"int64转int64不变", common.NewInt64(42), common.TypeInt64, common.NewInt64(42)},
		{"float64转float64不变", common.NewFloat64(3.14), common.TypeFloat64, common.NewFloat64(3.14)},
		// Float64 -> Int64
		{"float64转int64", common.NewFloat64(42.7), common.TypeInt64, common.NewInt64(42)},
		// Bool -> Int64
		{"bool true转int64", common.NewBool(true), common.TypeInt64, common.NewInt64(1)},
		{"bool false转int64", common.NewBool(false), common.TypeInt64, common.NewInt64(0)},
		// Int64 -> Float64
		{"int64转float64", common.NewInt64(42), common.TypeFloat64, common.NewFloat64(42.0)},
		// Bool -> Float64
		{"bool true转float64", common.NewBool(true), common.TypeFloat64, common.NewFloat64(1.0)},
		{"bool false转float64", common.NewBool(false), common.TypeFloat64, common.NewFloat64(0.0)},
		// 任意类型 -> Bool（使用 isTruthyValue）
		{"int64非零转bool true", common.NewInt64(42), common.TypeBool, common.NewBool(true)},
		{"int64零转bool false", common.NewInt64(0), common.TypeBool, common.NewBool(false)},
		{"string非空转bool true", common.NewString("hi"), common.TypeBool, common.NewBool(true)},
		{"string空转bool false", common.NewString(""), common.TypeBool, common.NewBool(false)},
		{"null转bool返回null", common.NewNull(), common.TypeBool, common.NewNull()},
		// 不支持的转换返回原值
		{"string转int64不变", common.NewString("abc"), common.TypeInt64, common.NewString("abc")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := coerceValue(tt.input, tt.target)
			if !got.Equal(tt.want) {
				t.Errorf("coerceValue(%v, %v) = %v, want %v", tt.input, tt.target, got, tt.want)
			}
		})
	}
}

// TestExprHasAggregate 测试 exprHasAggregate 函数。
func TestExprHasAggregate(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())

	tests := []struct {
		name string
		expr Expression
		want bool
	}{
		// 聚合函数
		{"count函数返回true", &FuncExpr{Name: testFuncCount, Args: []Expression{&StarExpr{}}}, true},
		{"sum函数返回true", &FuncExpr{Name: aggNameSum, Args: []Expression{&ColumnExpr{Name: testColAge}}}, true},
		{"min函数返回true", &FuncExpr{Name: testFuncMin, Args: []Expression{&ColumnExpr{Name: testColAge}}}, true},
		{"max函数返回true", &FuncExpr{Name: aggNameMax, Args: []Expression{&ColumnExpr{Name: testColAge}}}, true},
		{"avg函数返回true", &FuncExpr{Name: testFuncAvg, Args: []Expression{&ColumnExpr{Name: testColAge}}}, true},
		// 非聚合函数
		{"非聚合函数返回false", &FuncExpr{Name: testFuncAbs, Args: []Expression{&ColumnExpr{Name: testColAge}}}, false},
		// 列表达式
		{"列表达式返回false", &ColumnExpr{Name: testColAge}, false},
		// 字面量
		{"字面量返回false", &LiteralExpr{Value: common.NewInt64(1)}, false},
		// 二元表达式包含聚合
		{"二元表达式包含聚合", &BinaryExpr{
			Op:    OpAdd,
			Left:  &FuncExpr{Name: testFuncCount, Args: []Expression{&StarExpr{}}},
			Right: &LiteralExpr{Value: common.NewInt64(1)},
		}, true},
		// 二元表达式不包含聚合
		{"二元表达式不含聚合", &BinaryExpr{
			Op:    OpAdd,
			Left:  &ColumnExpr{Name: testColAge},
			Right: &LiteralExpr{Value: common.NewInt64(1)},
		}, false},
		// 一元表达式包含聚合
		{"一元表达式包含聚合", &UnaryExpr{
			Op:   OpNeg,
			Expr: &FuncExpr{Name: aggNameSum, Args: []Expression{&ColumnExpr{Name: testColAge}}},
		}, true},
		// 一元表达式不含聚合
		{"一元表达式不含聚合", &UnaryExpr{
			Op:   OpNeg,
			Expr: &ColumnExpr{Name: testColAge},
		}, false},
		// 嵌套函数参数中的聚合
		{"嵌套函数参数中的聚合", &FuncExpr{
			Name: testFuncAbs,
			Args: []Expression{&FuncExpr{Name: testFuncCount, Args: []Expression{&StarExpr{}}}},
		}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analyzer.exprHasAggregate(tt.expr)
			if got != tt.want {
				t.Errorf("exprHasAggregate(%v) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

// collectAggTestCase 是 collectAggregates 测试用例。
type collectAggTestCase struct {
	name      string
	expr      Expression
	wantCount int
	wantFuncs []AggregateFunc
}

// makeCollectAggTests 返回 collectAggregates 的测试用例。
func makeCollectAggTests() []collectAggTestCase {
	return []collectAggTestCase{
		{"单个count",
			&FuncExpr{Name: testFuncCount, Args: []Expression{&StarExpr{}}},
			1, []AggregateFunc{AggCount}},
		{"单个sum",
			&FuncExpr{Name: aggNameSum, Args: []Expression{&ColumnExpr{Name: testColAge}}},
			1, []AggregateFunc{AggSum}},
		{"二元表达式两个聚合",
			&BinaryExpr{Op: OpAdd,
				Left:  &FuncExpr{Name: aggNameSum, Args: []Expression{&ColumnExpr{Name: testColAge}}},
				Right: &FuncExpr{Name: testFuncCount, Args: []Expression{&StarExpr{}}}},
			2, []AggregateFunc{AggSum, AggCount}},
		{"一元表达式包含聚合",
			&UnaryExpr{Op: OpNeg,
				Expr: &FuncExpr{Name: testFuncAvg, Args: []Expression{&ColumnExpr{Name: testColScore}}}},
			1, []AggregateFunc{AggAvg}},
		{"非聚合函数不收集",
			&FuncExpr{Name: testFuncAbs, Args: []Expression{&ColumnExpr{Name: testColAge}}},
			0, nil},
		{"嵌套函数中的聚合",
			&FuncExpr{Name: "coalesce",
				Args: []Expression{&FuncExpr{Name: testFuncMin, Args: []Expression{&ColumnExpr{Name: testColAge}}}}},
			1, []AggregateFunc{AggMin}},
		{"无聚合的列表达式",
			&ColumnExpr{Name: testColAge},
			0, nil},
	}
}

// TestCollectAggregates 测试 collectAggregates 函数。
func TestCollectAggregates(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	for _, tt := range makeCollectAggTests() {
		t.Run(tt.name, func(t *testing.T) {
			var aggs []AggregateExpr
			analyzer.collectAggregates(tt.expr, &aggs)
			if len(aggs) != tt.wantCount {
				t.Errorf("收集到 %d 个聚合, want %d", len(aggs), tt.wantCount)
				return
			}
			for i, agg := range aggs {
				if agg.Func != tt.wantFuncs[i] {
					t.Errorf("[%d].Func = %v, want %v", i, agg.Func, tt.wantFuncs[i])
				}
			}
		})
	}
}
