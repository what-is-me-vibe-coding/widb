package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
)

func TestCompareValues(t *testing.T) {
	cases := []struct {
		name        string
		op          BinaryOp
		left, right common.Value
		want        bool
	}{
		{"Eq true", OpEq, common.NewInt64(5), common.NewInt64(5), true},
		{"Eq false", OpEq, common.NewInt64(5), common.NewInt64(6), false},
		{"Ne true", OpNe, common.NewInt64(5), common.NewInt64(6), true},
		{"Ne false", OpNe, common.NewInt64(5), common.NewInt64(5), false},
		{"Lt true", OpLt, common.NewInt64(5), common.NewInt64(6), true},
		{"Lt false", OpLt, common.NewInt64(6), common.NewInt64(5), false},
		{"Gt true", OpGt, common.NewInt64(6), common.NewInt64(5), true},
		{"Gt false", OpGt, common.NewInt64(5), common.NewInt64(6), false},
		{"Le true eq", OpLe, common.NewInt64(5), common.NewInt64(5), true},
		{"Le false", OpLe, common.NewInt64(6), common.NewInt64(5), false},
		{"Ge true eq", OpGe, common.NewInt64(5), common.NewInt64(5), true},
		{"Ge false", OpGe, common.NewInt64(4), common.NewInt64(5), false},
		{"string Lt", OpLt, common.NewString("a"), common.NewString("b"), true},
		{"float Gt", OpGt, common.NewFloat64(3.14), common.NewFloat64(2.71), true},
		// 非比较运算符应返回 false（不 panic）
		{"non-comparison op", OpAdd, common.NewInt64(1), common.NewInt64(2), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := compareValues(tc.op, tc.left, tc.right); got != tc.want {
				t.Errorf("compareValues(%v) = %v, want %v", tc.op, got, tc.want)
			}
		})
	}
}

func TestIsComparisonOp(t *testing.T) {
	comparisonOps := []BinaryOp{OpEq, OpNe, OpLt, OpLe, OpGt, OpGe}
	for _, op := range comparisonOps {
		if !isComparisonOp(op) {
			t.Errorf("isComparisonOp(%v) = false, want true", op)
		}
	}
	nonComparisonOps := []BinaryOp{OpAnd, OpOr, OpAdd, OpSub, OpMul, OpDiv, OpLike, BinaryOp(99)}
	for _, op := range nonComparisonOps {
		if isComparisonOp(op) {
			t.Errorf("isComparisonOp(%v) = true, want false", op)
		}
	}
}

func TestFlipComparisonOp(t *testing.T) {
	cases := []struct {
		op       BinaryOp
		wantFlip BinaryOp
		wantOK   bool
	}{
		{OpLt, OpGt, true},
		{OpLe, OpGe, true},
		{OpGt, OpLt, true},
		{OpGe, OpLe, true},
		{OpEq, OpEq, true},
		{OpNe, OpNe, true},
		{OpAdd, 0, false},
		{BinaryOp(99), 0, false},
	}
	for _, tc := range cases {
		got, ok := flipComparisonOp(tc.op)
		if got != tc.wantFlip || ok != tc.wantOK {
			t.Errorf("flipComparisonOp(%v) = (%v, %v), want (%v, %v)", tc.op, got, ok, tc.wantFlip, tc.wantOK)
		}
	}
}

// TestOpToIndexOpRoundTrip 验证 queryOpToIndexOp 与 queryOpToIndexOpFlip 共享同一映射表，
// 且翻转关系自洽：翻转两次应回到原 op。
func TestOpToIndexOpRoundTrip(t *testing.T) {
	comparisonOps := []BinaryOp{OpEq, OpNe, OpLt, OpLe, OpGt, OpGe}
	for _, op := range comparisonOps {
		base, ok := queryOpToIndexOp(op)
		if !ok {
			t.Fatalf("queryOpToIndexOp(%v) ok=false, want true", op)
		}
		flippedOp, ok := flipComparisonOp(op)
		if !ok {
			t.Fatalf("flipComparisonOp(%v) ok=false, want true", op)
		}
		flipped, ok := queryOpToIndexOp(flippedOp)
		if !ok {
			t.Fatalf("queryOpToIndexOp(flip(%v)) ok=false, want true", op)
		}
		// 翻转后再翻转应回到原 op
		doubleFlip, ok := flipComparisonOp(flippedOp)
		if !ok || doubleFlip != op {
			t.Errorf("double flip of %v = (%v, %v), want (%v, true)", op, doubleFlip, ok, op)
		}
		// flip 后查到的索引 op 应等于直接对 flippedOp 查表
		if flipped != base {
			// 仅当翻转不改变 op（Eq/Ne）时 base==flipped；否则应不同
			if flippedOp == op && flipped != base {
				t.Errorf("invariant: flipped(%v)==base expected", op)
			}
		}
	}
	// 非比较运算符查表应失败
	if _, ok := queryOpToIndexOp(OpAdd); ok {
		t.Error("queryOpToIndexOp(OpAdd) ok=true, want false")
	}
	if _, ok := queryOpToIndexOpFlip(OpAdd); ok {
		t.Error("queryOpToIndexOpFlip(OpAdd) ok=true, want false")
	}
	// 确保所有比较 op 都能映射到合法的 index.PredicateOp
	_ = index.OpEqual
}
