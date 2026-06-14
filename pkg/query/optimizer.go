package query

import "github.com/what-is-me-vibe-coding/test-db/pkg/common"

// OptimizeRule defines the interface for query optimization rules.
type OptimizeRule interface {
	Apply(node PlanNode) PlanNode
	Name() string
}

// Optimizer applies a set of optimization rules to a query plan.
type Optimizer struct {
	rules []OptimizeRule
}

// NewOptimizer creates a new Optimizer with the default set of optimization rules.
func NewOptimizer() *Optimizer {
	return &Optimizer{
		rules: []OptimizeRule{
			&PredicatePushdownRule{},
			&ConstantFoldingRule{},
			&ColumnPruningRule{},
		},
	}
}

// Optimize applies all registered optimization rules to the given plan node.
func (o *Optimizer) Optimize(node PlanNode) PlanNode {
	result := node
	for _, rule := range o.rules {
		result = rule.Apply(result)
	}
	return result
}

// PredicatePushdownRule pushes filter predicates down the plan tree
// to reduce intermediate result sizes as early as possible.
type PredicatePushdownRule struct{}

// Name returns the name of the PredicatePushdownRule.
func (r *PredicatePushdownRule) Name() string { return "PredicatePushdown" }

// Apply applies predicate pushdown optimization to the given plan node.
func (r *PredicatePushdownRule) Apply(node PlanNode) PlanNode {
	return r.pushDown(node)
}

func (r *PredicatePushdownRule) pushDown(node PlanNode) PlanNode {
	switch n := node.(type) {
	case *FilterNode:
		return r.pushFilterDown(n)
	case *ProjectNode:
		child := r.pushDown(n.Child)
		n.Child = child
		return n
	case *AggregateNode:
		child := r.pushDown(n.Child)
		n.Child = child
		return n
	case *LimitNode:
		child := r.pushDown(n.Child)
		n.Child = child
		return n
	}
	return node
}

func (r *PredicatePushdownRule) pushFilterDown(filter *FilterNode) PlanNode {
	child := r.pushDown(filter.Child)

	switch c := child.(type) {
	case *ScanNode:
		if c.Predicate == nil {
			c.Predicate = filter.Condition
		} else {
			c.Predicate = &BinaryExpr{Op: OpAnd, Left: c.Predicate, Right: filter.Condition}
		}
		return c

	case *FilterNode:
		merged := &BinaryExpr{Op: OpAnd, Left: filter.Condition, Right: c.Condition}
		newFilter := &FilterNode{Child: c.Child, Condition: merged}
		return r.pushFilterDown(newFilter)

	case *ProjectNode:
		if r.canPushThroughProject(filter.Condition, c) {
			c.Child = &FilterNode{Child: r.pushDown(c.Child), Condition: filter.Condition}
			return c
		}
		filter.Child = child
		return filter

	case *AggregateNode:
		pushable, remaining := r.splitPredicateByAggregate(filter.Condition, c)
		if pushable != nil {
			c.Child = &FilterNode{Child: r.pushDown(c.Child), Condition: pushable}
		}
		if remaining != nil {
			filter.Child = c
			filter.Condition = remaining
			return filter
		}
		return c
	}

	filter.Child = child
	return filter
}

func (r *PredicatePushdownRule) canPushThroughProject(cond Expression, proj *ProjectNode) bool {
	cols := collectColumnRefs(cond)
	projSchema := proj.Child.Schema()
	for _, col := range cols {
		found := false
		for _, sc := range projSchema {
			if sc.Name == col {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (r *PredicatePushdownRule) splitPredicateByAggregate(cond Expression, agg *AggregateNode) (pushable, remaining Expression) {
	conjuncts := splitConjuncts(cond)
	var pushConds, remainConds []Expression

	aggCols := make(map[string]bool)
	for _, gb := range agg.GroupBy {
		aggCols[gb.String()] = true
	}
	for _, a := range agg.Aggregates {
		aggCols[a.String()] = true
	}

	for _, c := range conjuncts {
		cols := collectColumnRefs(c)
		canPush := true
		for _, col := range cols {
			if aggCols[col] {
				canPush = false
				break
			}
		}
		if canPush {
			pushConds = append(pushConds, c)
		} else {
			remainConds = append(remainConds, c)
		}
	}

	pushable = mergeConjuncts(pushConds)
	remaining = mergeConjuncts(remainConds)
	return pushable, remaining
}

// ConstantFoldingRule evaluates constant expressions at optimization time
// to reduce runtime computation.
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
	switch op {
	case OpEq:
		return &LiteralExpr{Value: common.NewBool(leftLit.Value.Equal(rightLit.Value))}
	case OpNe:
		return &LiteralExpr{Value: common.NewBool(!leftLit.Value.Equal(rightLit.Value))}
	case OpLt:
		return &LiteralExpr{Value: common.NewBool(leftLit.Value.Less(rightLit.Value))}
	case OpGt:
		return &LiteralExpr{Value: common.NewBool(rightLit.Value.Less(leftLit.Value))}
	case OpLe:
		return &LiteralExpr{Value: common.NewBool(!rightLit.Value.Less(leftLit.Value))}
	case OpGe:
		return &LiteralExpr{Value: common.NewBool(!leftLit.Value.Less(rightLit.Value))}
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

// ColumnPruningRule removes unnecessary columns from scan nodes
// to reduce the amount of data processed.
type ColumnPruningRule struct{}

// Name returns the name of the ColumnPruningRule.
func (r *ColumnPruningRule) Name() string { return "ColumnPruning" }

// Apply applies column pruning optimization to the given plan node.
func (r *ColumnPruningRule) Apply(node PlanNode) PlanNode {
	needed := r.collectNeededColumns(node)
	return r.pruneNode(node, needed)
}

func (r *ColumnPruningRule) collectNeededColumns(node PlanNode) map[string]bool {
	needed := make(map[string]bool)
	r.gatherNeeded(node, needed)
	return needed
}

func (r *ColumnPruningRule) gatherNeeded(node PlanNode, needed map[string]bool) {
	switch n := node.(type) {
	case *ProjectNode:
		for _, e := range n.Expressions {
			collectColumnRefsInto(e, needed)
		}
		r.gatherNeeded(n.Child, needed)
	case *FilterNode:
		collectColumnRefsInto(n.Condition, needed)
		r.gatherNeeded(n.Child, needed)
	case *AggregateNode:
		for _, gb := range n.GroupBy {
			collectColumnRefsInto(gb, needed)
		}
		for _, agg := range n.Aggregates {
			if agg.Arg != nil {
				collectColumnRefsInto(agg.Arg, needed)
			}
		}
		r.gatherNeeded(n.Child, needed)
	case *LimitNode:
		r.gatherNeeded(n.Child, needed)
	case *ScanNode:
	}
}

func (r *ColumnPruningRule) pruneNode(node PlanNode, needed map[string]bool) PlanNode {
	switch n := node.(type) {
	case *ScanNode:
		return r.pruneScan(n, needed)
	case *FilterNode:
		collectColumnRefsInto(n.Condition, needed)
		n.Child = r.pruneNode(n.Child, needed)
		return n
	case *ProjectNode:
		childNeeded := make(map[string]bool)
		for _, e := range n.Expressions {
			collectColumnRefsInto(e, childNeeded)
		}
		n.Child = r.pruneNode(n.Child, childNeeded)
		return n
	case *AggregateNode:
		childNeeded := make(map[string]bool)
		for _, gb := range n.GroupBy {
			collectColumnRefsInto(gb, childNeeded)
		}
		for _, agg := range n.Aggregates {
			if agg.Arg != nil {
				collectColumnRefsInto(agg.Arg, childNeeded)
			}
		}
		n.Child = r.pruneNode(n.Child, childNeeded)
		return n
	case *LimitNode:
		n.Child = r.pruneNode(n.Child, needed)
		return n
	}
	return node
}

func (r *ColumnPruningRule) pruneScan(scan *ScanNode, needed map[string]bool) PlanNode {
	if len(needed) == 0 {
		return scan
	}

	pruned := make([]string, 0, len(scan.Columns))
	for _, col := range scan.Columns {
		if needed[col] {
			pruned = append(pruned, col)
		}
	}

	if len(pruned) < len(scan.Columns) {
		scan.Columns = pruned
		scan.schema = buildPrunedSchema(scan.schema, pruned)
	}

	return scan
}

func buildPrunedSchema(schema []ColumnDef, cols []string) []ColumnDef {
	result := make([]ColumnDef, 0, len(cols))
	for _, name := range cols {
		for _, s := range schema {
			if s.Name == name {
				result = append(result, s)
				break
			}
		}
	}
	return result
}

func collectColumnRefs(expr Expression) []string {
	seen := make(map[string]bool)
	collectColumnRefsInto(expr, seen)
	result := make([]string, 0, len(seen))
	for k := range seen {
		result = append(result, k)
	}
	return result
}

func collectColumnRefsInto(expr Expression, seen map[string]bool) {
	switch e := expr.(type) {
	case *ColumnExpr:
		seen[e.Name] = true
	case *ResolvedColumnExpr:
		seen[e.Name] = true
	case *BinaryExpr:
		collectColumnRefsInto(e.Left, seen)
		collectColumnRefsInto(e.Right, seen)
	case *UnaryExpr:
		collectColumnRefsInto(e.Expr, seen)
	case *FuncExpr:
		for _, arg := range e.Args {
			collectColumnRefsInto(arg, seen)
		}
	}
}

func splitConjuncts(expr Expression) []Expression {
	bin, ok := expr.(*BinaryExpr)
	if !ok || bin.Op != OpAnd {
		return []Expression{expr}
	}
	return append(splitConjuncts(bin.Left), splitConjuncts(bin.Right)...)
}

func mergeConjuncts(conjuncts []Expression) Expression {
	if len(conjuncts) == 0 {
		return nil
	}
	result := conjuncts[0]
	for _, c := range conjuncts[1:] {
		result = &BinaryExpr{Op: OpAnd, Left: result, Right: c}
	}
	return result
}
