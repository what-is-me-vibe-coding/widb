package query

import "github.com/what-is-me-vibe-coding/test-db/pkg/common"

// ConstantFoldingRule evaluates constant expressions at optimization time
// to reduce runtime computation.
//
// 折叠策略：
//   - 比较运算：左右均为字面量时直接求值（NULL 比较保留原表达式以保证三值语义）
//   - 逻辑运算：左右均为字面量时按 true/false 求值（同样保留 NULL）
//   - NOT 运算：操作数为字面量时直接取反
//   - 非字面量子表达式：递归下钻后保留
type ConstantFoldingRule struct{}

// Name returns the name of the ConstantFoldingRule.
func (r *ConstantFoldingRule) Name() string { return "ConstantFolding" }

// Apply applies constant folding optimization to the given plan node.
func (r *ConstantFoldingRule) Apply(node PlanNode) PlanNode {
	return r.foldNode(node)
}

func (r *ConstantFoldingRule) foldNode(node PlanNode) PlanNode {
	switch n := node.(type) {
	case *ScanNode:
		if n.Predicate != nil {
			n.Predicate = r.foldExpr(n.Predicate)
		}
		return n
	case *FilterNode:
		n.Child = r.foldNode(n.Child)
		n.Condition = r.foldExpr(n.Condition)
		return n
	case *ProjectNode:
		n.Child = r.foldNode(n.Child)
		for i, e := range n.Expressions {
			n.Expressions[i] = r.foldExpr(e)
		}
		return n
	case *AggregateNode:
		n.Child = r.foldNode(n.Child)
		return n
	case *LimitNode:
		n.Child = r.foldNode(n.Child)
		return n
	}
	return node
}

func (r *ConstantFoldingRule) foldExpr(expr Expression) Expression {
	switch e := expr.(type) {
	case *BinaryExpr:
		e.Left = r.foldExpr(e.Left)
		e.Right = r.foldExpr(e.Right)
		return r.foldBinaryExpr(e)
	case *UnaryExpr:
		e.Expr = r.foldExpr(e.Expr)
		return r.foldUnaryExpr(e)
	case *FuncExpr:
		for i, arg := range e.Args {
			e.Args[i] = r.foldExpr(arg)
		}
	}
	return expr
}

// foldBinaryExpr 折叠 BinaryExpr：仅当左右均为「有效」字面量时才尝试求值。
// NULL 字面量不参与比较/逻辑折叠（保留三值逻辑的不确定性）。
func (r *ConstantFoldingRule) foldBinaryExpr(e *BinaryExpr) Expression {
	leftLit, leftIsLit := e.Left.(*LiteralExpr)
	rightLit, rightIsLit := e.Right.(*LiteralExpr)

	if !leftIsLit || !rightIsLit {
		return e
	}

	if !leftLit.Value.Valid || !rightLit.Value.Valid {
		return e
	}

	if result := r.foldComparisonOps(leftLit, rightLit, e.Op); result != nil {
		return result
	}
	if result := r.foldLogicalOps(leftLit, rightLit, e.Op); result != nil {
		return result
	}
	return e
}

func (r *ConstantFoldingRule) foldComparisonOps(leftLit, rightLit *LiteralExpr, op BinaryOp) Expression {
	if isComparisonOp(op) {
		return &LiteralExpr{Value: common.NewBool(compareValues(op, leftLit.Value, rightLit.Value))}
	}
	return nil
}

func (r *ConstantFoldingRule) foldLogicalOps(leftLit, rightLit *LiteralExpr, op BinaryOp) Expression {
	switch op {
	case OpAnd:
		lb := isTruthyValue(leftLit.Value)
		rb := isTruthyValue(rightLit.Value)
		return &LiteralExpr{Value: common.NewBool(lb && rb)}
	case OpOr:
		lb := isTruthyValue(leftLit.Value)
		rb := isTruthyValue(rightLit.Value)
		return &LiteralExpr{Value: common.NewBool(lb || rb)}
	}
	return nil
}

func (r *ConstantFoldingRule) foldUnaryExpr(e *UnaryExpr) Expression {
	if e.Op == OpNot {
		if lit, ok := e.Expr.(*LiteralExpr); ok && lit.Value.Valid {
			return &LiteralExpr{Value: common.NewBool(!isTruthyValue(lit.Value))}
		}
	}
	return e
}
