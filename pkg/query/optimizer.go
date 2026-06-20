package query

// OptimizeRule defines the interface for query optimization rules.
type OptimizeRule interface {
	Apply(node PlanNode) PlanNode
	Name() string
}

// Optimizer applies a set of optimization rules to a query plan.
//
// 各规则的具体实现按关注点拆分至独立文件：
//   - optimizer_predicate_pushdown.go：PredicatePushdownRule
//   - optimizer_constant_folding.go：ConstantFoldingRule
//   - optimizer_column_pruning.go：ColumnPruningRule
//   - optimizer_helpers.go：规则共用的列引用收集 / 合取拆分合并工具
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
