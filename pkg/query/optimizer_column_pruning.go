package query

// ColumnPruningRule removes unnecessary columns from scan nodes
// to reduce the amount of data processed.
//
// 必要性收集：
//   - Project：其表达式引用的列
//   - Filter：谓词引用的列
//   - Aggregate：GroupBy 列 + 聚合函数参数列
//   - Limit：递归子节点
//   - Scan：终止符，扫描节点持有的列按 needed 集合裁剪
//
// 同一 Project / Aggregate 内重新计算 childNeeded 是为了在多层投影
// 中只把当前节点真实需要的列下传，避免上游冗余列被误保留。
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

// buildPrunedSchema 根据保留的列名重建 ScanNode 的 schema。
// 列按 scan.Columns 顺序排列，便于上游按索引访问。
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
