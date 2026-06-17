package query

import (
	"cmp"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
)

// compareValues 对两个有效（非 NULL）值执行比较运算，返回比较结果。
// 这是查询引擎中所有比较运算的唯一实现源头，executor 与 optimizer 共享，
// 避免比较语义（NULL 处理、类型 coercion 等）在多处复制后产生漂移。
// 支持的 op：OpEq/OpNe/OpLt/OpLe/OpGt/OpGe；其他 op 返回 false。
func compareValues(op BinaryOp, left, right common.Value) bool {
	switch op {
	case OpEq:
		return left.Equal(right)
	case OpNe:
		return !left.Equal(right)
	case OpLt:
		return left.Less(right)
	case OpGt:
		return right.Less(left)
	case OpLe:
		return !right.Less(left)
	case OpGe:
		return !left.Less(right)
	}
	return false
}

// compareOrdered 对有序标量类型（int64/float64/string）执行比较运算。
// 用于过滤快速路径中直接对列底层数组元素进行比较，跳过 Value 构造与
// compareValues 的方法分发。语义与 compareValues 在同类型非 NULL 场景一致。
// 编译器对每个具现化类型单态化，无运行时分发开销。
func compareOrdered[T cmp.Ordered](op BinaryOp, left, right T) bool {
	switch op {
	case OpEq:
		return left == right
	case OpNe:
		return left != right
	case OpLt:
		return left < right
	case OpLe:
		return left <= right
	case OpGt:
		return left > right
	case OpGe:
		return left >= right
	}
	return false
}

// isComparisonOp 判断 op 是否为比较运算符（OpEq/OpNe/OpLt/OpLe/OpGt/OpGe）。
func isComparisonOp(op BinaryOp) bool {
	switch op {
	case OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
		return true
	}
	return false
}

// opToIndexOp 将查询层 BinaryOp 映射为索引层 PredicateOp 的正向映射表。
// queryOpToIndexOp 与 queryOpToIndexOpFlip 共享此表，避免两份独立的 switch 漂移。
var opToIndexOp = map[BinaryOp]index.PredicateOp{
	OpEq: index.OpEqual,
	OpNe: index.OpNotEqual,
	OpLt: index.OpLess,
	OpLe: index.OpLessEqual,
	OpGt: index.OpGreater,
	OpGe: index.OpGreaterEqual,
}

// flipComparisonOp 返回运算符交换左右操作数后等价的运算符。
// 例如 "a < b" 交换操作数后等价于 "b > a"，故 OpLt 翻转为 OpGt。
// OpEq/OpNe 翻转后不变；非比较运算符返回 (0, false)。
func flipComparisonOp(op BinaryOp) (BinaryOp, bool) {
	switch op {
	case OpLt:
		return OpGt, true
	case OpLe:
		return OpGe, true
	case OpGt:
		return OpLt, true
	case OpGe:
		return OpLe, true
	case OpEq, OpNe:
		return op, true
	}
	return 0, false
}
