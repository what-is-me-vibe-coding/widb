package query

import (
	"fmt"
	"math"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// buildRowValues 从 Chunk 构建指定行的列名到值的映射。
// 使用传入的 buf map 复用，减少每行分配；buf 为 nil 时创建新 map。
func buildRowValues(chunk *storage.Chunk, schema []ColumnDef, row uint32, buf map[string]common.Value) map[string]common.Value {
	if buf == nil {
		buf = make(map[string]common.Value, len(schema))
	} else {
		// 清空复用 map
		for k := range buf {
			delete(buf, k)
		}
	}
	for i, col := range chunk.Columns() {
		if i < len(schema) {
			buf[schema[i].Name] = col.GetValue(row)
		}
	}
	return buf
}

// evalExpr 在给定行数据上求值表达式。
func evalExpr(expr Expression, row map[string]common.Value, colIdxMap map[string]int) (common.Value, error) {
	switch e := expr.(type) {
	case *LiteralExpr:
		return e.Value, nil
	case *ResolvedColumnExpr:
		val, ok := row[e.Name]
		if !ok {
			return common.NewNull(), nil
		}
		return val, nil
	case *ColumnExpr:
		val, ok := row[e.Name]
		if !ok {
			return common.NewNull(), nil
		}
		return val, nil
	case *BinaryExpr:
		return evalBinaryExpr(e, row, colIdxMap)
	case *UnaryExpr:
		return evalUnaryExpr(e, row, colIdxMap)
	case *FuncExpr:
		return evalFuncExpr(e, row, colIdxMap)
	case *StarExpr:
		return common.NewNull(), nil
	default:
		return common.NewNull(), fmt.Errorf("executor: unsupported expression type %T", expr)
	}
}

func evalBinaryExpr(e *BinaryExpr, row map[string]common.Value, colIdxMap map[string]int) (common.Value, error) {
	left, err := evalExpr(e.Left, row, colIdxMap)
	if err != nil {
		return common.NewNull(), err
	}

	if result, ok, err := evalLogicalOp(e, left, row, colIdxMap); ok {
		return result, err
	}

	right, err := evalExpr(e.Right, row, colIdxMap)
	if err != nil {
		return common.NewNull(), err
	}

	if !left.Valid || !right.Valid {
		return common.NewNull(), nil
	}

	if result, ok := evalComparisonOp(e.Op, left, right); ok {
		return result, nil
	}

	return evalArithmeticOp(e.Op, left, right)
}

func evalLogicalOp(e *BinaryExpr, left common.Value, row map[string]common.Value, colIdxMap map[string]int) (common.Value, bool, error) {
	switch e.Op {
	case OpAnd:
		if !isTruthyValue(left) {
			return common.NewBool(false), true, nil
		}
		right, err := evalExpr(e.Right, row, colIdxMap)
		if err != nil {
			return common.NewNull(), true, err
		}
		return common.NewBool(isTruthyValue(right)), true, nil
	case OpOr:
		if isTruthyValue(left) {
			return common.NewBool(true), true, nil
		}
		right, err := evalExpr(e.Right, row, colIdxMap)
		if err != nil {
			return common.NewNull(), true, err
		}
		return common.NewBool(isTruthyValue(right)), true, nil
	}
	return common.NewNull(), false, nil
}

func evalComparisonOp(op BinaryOp, left, right common.Value) (common.Value, bool) {
	if isComparisonOp(op) {
		return common.NewBool(compareValues(op, left, right)), true
	}
	return common.NewNull(), false
}

func evalArithmeticOp(op BinaryOp, left, right common.Value) (common.Value, error) {
	switch op {
	case OpAdd:
		return evalArithmetic(left, right, opAdd)
	case OpSub:
		return evalArithmetic(left, right, opSub)
	case OpMul:
		return evalArithmetic(left, right, opMul)
	case OpDiv:
		return evalArithmetic(left, right, opDiv)
	}
	return common.NewNull(), fmt.Errorf("executor: unsupported binary op %v", op)
}

func evalUnaryExpr(e *UnaryExpr, row map[string]common.Value, colIdxMap map[string]int) (common.Value, error) {
	val, err := evalExpr(e.Expr, row, colIdxMap)
	if err != nil {
		return common.NewNull(), err
	}

	switch e.Op {
	case OpNot:
		return common.NewBool(!isTruthyValue(val)), nil
	case OpNeg:
		if !val.Valid {
			return common.NewNull(), nil
		}
		if val.Typ.IsIntFamily() {
			return common.NewIntFamilyValue(val.Typ, -val.Int64), nil
		}
		if val.Typ == common.TypeFloat64 {
			return common.NewFloat64(-val.Float64), nil
		}
	}

	return common.NewNull(), fmt.Errorf("executor: unsupported unary op %v", e.Op)
}

func evalFuncExpr(e *FuncExpr, _ map[string]common.Value, _ map[string]int) (common.Value, error) {
	return common.NewNull(), fmt.Errorf("executor: scalar function %q not supported in row eval", e.Name)
}

type arithOp int

const (
	opAdd arithOp = iota
	opSub
	opMul
	opDiv
)

func evalArithmetic(left, right common.Value, op arithOp) (common.Value, error) {
	if left.Typ == common.TypeFloat64 || right.Typ == common.TypeFloat64 {
		return evalFloatArithmetic(toFloat64(left), toFloat64(right), op)
	}
	return evalIntArithmetic(left.Int64, right.Int64, op)
}

func evalFloatArithmetic(lf, rf float64, op arithOp) (common.Value, error) {
	switch op {
	case opAdd:
		return common.NewFloat64(lf + rf), nil
	case opSub:
		return common.NewFloat64(lf - rf), nil
	case opMul:
		return common.NewFloat64(lf * rf), nil
	case opDiv:
		if rf == 0 {
			return common.NewNull(), nil
		}
		return common.NewFloat64(lf / rf), nil
	}
	return common.NewNull(), nil
}

func evalIntArithmetic(li, ri int64, op arithOp) (common.Value, error) {
	switch op {
	case opAdd:
		return evalIntAdd(li, ri)
	case opSub:
		return evalIntSub(li, ri)
	case opMul:
		return evalIntMul(li, ri)
	case opDiv:
		if ri == 0 {
			return common.NewNull(), nil
		}
		return common.NewInt64(li / ri), nil
	}
	return common.NewNull(), nil
}

func evalIntAdd(li, ri int64) (common.Value, error) {
	if (ri > 0 && li > math.MaxInt64-ri) || (ri < 0 && li < math.MinInt64-ri) {
		return common.NewNull(), fmt.Errorf("executor: integer overflow in addition")
	}
	return common.NewInt64(li + ri), nil
}

func evalIntSub(li, ri int64) (common.Value, error) {
	if (ri < 0 && li > math.MaxInt64+ri) || (ri > 0 && li < math.MinInt64+ri) {
		return common.NewNull(), fmt.Errorf("executor: integer overflow in subtraction")
	}
	return common.NewInt64(li - ri), nil
}

func evalIntMul(li, ri int64) (common.Value, error) {
	if ri != 0 && mulOverflows(li, ri) {
		return common.NewNull(), fmt.Errorf("executor: integer overflow in multiplication")
	}
	return common.NewInt64(li * ri), nil
}

func mulOverflows(li, ri int64) bool {
	if ri > 0 {
		return li > math.MaxInt64/ri || li < math.MinInt64/ri
	}
	if ri < 0 {
		// 特殊处理 ri == -1：MinInt64 / -1 在 Go 中会导致整数除法溢出（运行时 panic）
		if ri == -1 {
			return li == math.MinInt64
		}
		return li > math.MinInt64/ri || li < math.MaxInt64/ri
	}
	return false
}

func toFloat64(v common.Value) float64 {
	if v.Typ == common.TypeFloat64 {
		return v.Float64
	}
	if v.Typ.IsIntFamily() {
		return float64(v.Int64)
	}
	return 0
}

// isTruthyValue 判断值是否为真。
func isTruthyValue(v common.Value) bool {
	if !v.Valid {
		return false
	}
	switch v.Typ {
	case common.TypeBool:
		return v.Int64 != 0
	case common.TypeInt64, common.TypeInt8, common.TypeInt16,
		common.TypeInt32, common.TypeUint64, common.TypeDate:
		return v.Int64 != 0
	case common.TypeFloat64:
		return v.Float64 != 0
	case common.TypeString:
		return v.Str != ""
	}
	return true
}
