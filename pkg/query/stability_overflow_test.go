package query

import (
	"math"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestIntOverflowAdd 验证整数加法溢出时返回 NULL 而非静默溢出
func TestIntOverflowAdd(t *testing.T) {
	tests := []struct {
		name     string
		a, b     int64
		wantNull bool
	}{
		{"normal_add", 1, 2, false},
		{"max_plus_one", math.MaxInt64, 1, true},
		{"max_plus_max", math.MaxInt64, math.MaxInt64, true},
		{"min_plus_minus_one", math.MinInt64, -1, true},
		{"min_plus_min", math.MinInt64, math.MinInt64, true},
		{"zero_add", 0, 0, false},
		{"negative_add", -10, -20, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := evalIntArithmetic(tt.a, tt.b, opAdd)
			if tt.wantNull {
				if val.Valid {
					t.Errorf("expected NULL for %d + %d, got %d", tt.a, tt.b, val.Int64)
				}
				if err == nil {
					t.Errorf("expected error for %d + %d overflow", tt.a, tt.b)
				}
			} else {
				if !val.Valid {
					t.Errorf("unexpected NULL for %d + %d", tt.a, tt.b)
				}
				if err != nil {
					t.Errorf("unexpected error for %d + %d: %v", tt.a, tt.b, err)
				}
			}
		})
	}
}

// TestIntOverflowSub 验证整数减法溢出时返回 NULL
func TestIntOverflowSub(t *testing.T) {
	tests := []struct {
		name     string
		a, b     int64
		wantNull bool
	}{
		{"normal_sub", 10, 3, false},
		{"min_sub_one", math.MinInt64, 1, true},
		{"max_sub_minus_one", math.MaxInt64, -1, true},
		{"zero_sub", 0, 0, false},
		{"negative_sub", -10, -20, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := evalIntArithmetic(tt.a, tt.b, opSub)
			if tt.wantNull {
				if val.Valid {
					t.Errorf("expected NULL for %d - %d, got %d", tt.a, tt.b, val.Int64)
				}
				if err == nil {
					t.Errorf("expected error for %d - %d overflow", tt.a, tt.b)
				}
			} else {
				if !val.Valid {
					t.Errorf("unexpected NULL for %d - %d", tt.a, tt.b)
				}
				if err != nil {
					t.Errorf("unexpected error for %d - %d: %v", tt.a, tt.b, err)
				}
			}
		})
	}
}

// TestIntOverflowMul 验证整数乘法溢出时返回 NULL
func TestIntOverflowMul(t *testing.T) {
	tests := []struct {
		name     string
		a, b     int64
		wantNull bool
	}{
		{"normal_mul", 6, 7, false},
		{"max_times_two", math.MaxInt64, 2, true},
		{"min_times_two", math.MinInt64, 2, true},
		{"max_times_max", math.MaxInt64, math.MaxInt64, true},
		{"zero_mul", 0, math.MaxInt64, false},
		{"one_mul", 1, math.MaxInt64, false},
		{"negative_mul", -3, 4, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := evalIntArithmetic(tt.a, tt.b, opMul)
			if tt.wantNull {
				if val.Valid {
					t.Errorf("expected NULL for %d * %d, got %d", tt.a, tt.b, val.Int64)
				}
				if err == nil {
					t.Errorf("expected error for %d * %d overflow", tt.a, tt.b)
				}
			} else {
				if !val.Valid {
					t.Errorf("unexpected NULL for %d * %d", tt.a, tt.b)
				}
				if err != nil {
					t.Errorf("unexpected error for %d * %d: %v", tt.a, tt.b, err)
				}
			}
		})
	}
}

// TestIntDivByZero 验证整数除以零返回 NULL
func TestIntDivByZero(t *testing.T) {
	val, err := evalIntArithmetic(10, 0, opDiv)
	if val.Valid {
		t.Errorf("expected NULL for division by zero, got %d", val.Int64)
	}
	if err != nil {
		t.Errorf("unexpected error for division by zero: %v", err)
	}
}

// TestIntNormalArithmetic 验证正常整数运算结果正确
func TestIntNormalArithmetic(t *testing.T) {
	tests := []struct {
		name string
		a, b int64
		op   arithOp
		want int64
	}{
		{"add", 10, 20, opAdd, 30},
		{"sub", 50, 20, opSub, 30},
		{"mul", 6, 5, opMul, 30},
		{"div", 30, 5, opDiv, 6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := evalIntArithmetic(tt.a, tt.b, tt.op)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !val.Valid {
				t.Fatalf("unexpected NULL")
			}
			if val.Int64 != tt.want {
				t.Errorf("expected %d, got %d", tt.want, val.Int64)
			}
		})
	}
}

// TestBuildGroupKeyWithError 验证 buildGroupKey 在表达式求值失败时不会 panic
func TestBuildGroupKeyWithError(t *testing.T) {
	row := map[string]common.Value{testStrCol1: common.NewInt64(42)}
	colIdxMap := map[string]int{testStrCol1: 0}

	// 正常情况
	key := buildGroupKey([]Expression{&ResolvedColumnExpr{Name: testStrCol1, Idx: 0, typ: common.TypeInt64}}, row, colIdxMap)
	if key != "42" {
		t.Errorf("expected group key '42', got %q", key)
	}

	// 不存在的列应返回 NULL 的字符串表示
	key = buildGroupKey([]Expression{&ResolvedColumnExpr{Name: testColNonexistent, Idx: 99, typ: common.TypeInt64}}, row, colIdxMap)
	if key == "" {
		t.Errorf("expected non-empty group key for missing column, got empty")
	}
}

// TestAggregateErrorHandling 验证聚合操作在表达式求值失败时不会崩溃
func TestAggregateErrorHandling(t *testing.T) {
	// 验证 COUNT 累加器：COUNT(*) 统计所有行（包括 NULL）
	countAcc := accumulator{funcType: AggCount}
	countAcc.update(common.NewNull()) // COUNT(*) 统计所有行
	countAcc.update(common.NewInt64(1))
	if countAcc.count != 2 {
		t.Errorf("expected count=2 for COUNT(*), got %d", countAcc.count)
	}

	// SUM 在 NULL 时跳过
	sumAcc := accumulator{funcType: AggSum}
	sumAcc.update(common.NewNull())
	sumAcc.update(common.NewInt64(10))
	if sumAcc.count != 1 {
		t.Errorf("expected sum count=1, got %d", sumAcc.count)
	}

	// MIN/MAX 在 NULL 时跳过
	minAcc := accumulator{funcType: AggMin}
	minAcc.update(common.NewNull())
	if minAcc.hasValue {
		t.Errorf("expected hasValue=false after NULL update for MIN")
	}
	minAcc.update(common.NewInt64(5))
	if !minAcc.hasValue {
		t.Errorf("expected hasValue=true after non-NULL update for MIN")
	}
}
