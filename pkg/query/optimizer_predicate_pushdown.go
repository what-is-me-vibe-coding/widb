package query

// PredicatePushdownRule pushes filter predicates down the plan tree
// to reduce intermediate result sizes as early as possible.
//
// 实现说明：本规则在以下算子之间下推谓词：
//   - Filter → Scan：直接合并到 Scan.Predicate
//   - Filter → Filter：合并相邻 Filter 后递归下推
//   - Filter → Project：仅当 Project 输出列包含谓词列时下推，否则留在 Project 上方
//   - Filter → Aggregate：按聚合列/分组列拆分谓词，能下推部分下推，剩余留在 Aggregate 上方
//   - Project / Aggregate / Limit 等「穿透型」节点：递归下推到子节点
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

// canPushThroughProject 判断谓词是否能下推到 Project 之下。
// 仅当 Project 的输入 schema 包含谓词引用的全部列时下推成功，
// 否则谓词保留在 Project 上方以避免引用 Project 未输出的列。
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

// splitPredicateByAggregate 按聚合节点拆分子句：仅引用「聚合输出列」之外的
// 子句可下推，引用聚合输出列的子句必须保留在 Aggregate 之上。
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
