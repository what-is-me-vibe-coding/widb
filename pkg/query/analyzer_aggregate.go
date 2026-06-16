package query

import (
	"strings"

	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
)

func (a *Analyzer) hasAggregateFuncs(cols []SelectColumn) bool {
	for _, col := range cols {
		if a.exprHasAggregate(col.Expr) {
			return true
		}
	}
	return false
}

func (a *Analyzer) exprHasAggregate(expr Expression) bool {
	switch e := expr.(type) {
	case *FuncExpr:
		if isAggregateFunc(e.Name) {
			return true
		}
		for _, arg := range e.Args {
			if a.exprHasAggregate(arg) {
				return true
			}
		}
	case *BinaryExpr:
		return a.exprHasAggregate(e.Left) || a.exprHasAggregate(e.Right)
	case *UnaryExpr:
		return a.exprHasAggregate(e.Expr)
	}
	return false
}

func isAggregateFunc(name string) bool {
	switch strings.ToLower(name) {
	case aggNameCount, aggNameSum, aggNameMin, aggNameMax, aggNameAvg:
		return true
	}
	return false
}

func (a *Analyzer) buildAggregateNode(sel *SelectStatement, table *catalog.Table, child PlanNode) (*AggregateNode, error) {
	groupBy := make([]Expression, len(sel.GroupBy))
	for i, gb := range sel.GroupBy {
		resolved, err := a.resolveExpr(gb, table)
		if err != nil {
			return nil, err
		}
		groupBy[i] = resolved
	}

	var aggregates []AggregateExpr
	for _, col := range sel.Columns {
		a.collectAggregates(col.Expr, &aggregates)
	}

	// 解析聚合参数表达式，确保类型信息正确（解析器产出的 ColumnExpr 无类型信息）
	for i := range aggregates {
		if aggregates[i].Arg == nil {
			continue
		}
		if resolved, err := a.resolveExpr(aggregates[i].Arg, table); err == nil {
			aggregates[i].Arg = resolved
		}
	}

	schema := make([]ColumnDef, 0, len(groupBy)+len(aggregates))
	for _, gb := range groupBy {
		schema = append(schema, ColumnDef{
			Name:     gb.String(),
			Type:     exprReturnType(gb),
			Nullable: true,
		})
	}
	for _, agg := range aggregates {
		schema = append(schema, ColumnDef{
			Name:     agg.String(),
			Type:     inferAggReturnType(agg),
			Nullable: true,
		})
	}

	return &AggregateNode{
		Child:      child,
		GroupBy:    groupBy,
		Aggregates: aggregates,
		schema:     schema,
	}, nil
}

func (a *Analyzer) collectAggregates(expr Expression, aggs *[]AggregateExpr) {
	switch e := expr.(type) {
	case *FuncExpr:
		if isAggregateFunc(e.Name) {
			var arg Expression
			if len(e.Args) > 0 {
				arg = e.Args[0]
			}
			*aggs = append(*aggs, AggregateExpr{
				Func: parseAggFunc(e.Name),
				Arg:  arg,
			})
			return
		}
		for _, arg := range e.Args {
			a.collectAggregates(arg, aggs)
		}
	case *BinaryExpr:
		a.collectAggregates(e.Left, aggs)
		a.collectAggregates(e.Right, aggs)
	case *UnaryExpr:
		a.collectAggregates(e.Expr, aggs)
	}
}

func parseAggFunc(name string) AggregateFunc {
	switch strings.ToLower(name) {
	case aggNameCount:
		return AggCount
	case aggNameSum:
		return AggSum
	case aggNameMin:
		return AggMin
	case aggNameMax:
		return AggMax
	case aggNameAvg:
		return AggAvg
	default:
		return AggUnknown
	}
}

// remapAggregateRefs 将投影表达式中的聚合函数调用替换为对 AggregateNode 输出列的引用。
// AggregateNode 已经完成聚合计算，ProjectNode 不应重复求值聚合函数。
// 匹配键为聚合表达式的小写字符串形式，兼容解析器的小写函数名与 AggregateFunc 的大写名。
func remapAggregateRefs(exprs []Expression, agg *AggregateNode) []Expression {
	aggMap := make(map[string]AggregateExpr, len(agg.Aggregates))
	for _, aggExpr := range agg.Aggregates {
		aggMap[strings.ToLower(aggExpr.String())] = aggExpr
	}

	result := make([]Expression, len(exprs))
	for i, expr := range exprs {
		if fe, ok := expr.(*FuncExpr); ok && isAggregateFunc(fe.Name) {
			if aggExpr, found := aggMap[strings.ToLower(fe.String())]; found {
				result[i] = &ResolvedColumnExpr{
					Name: aggExpr.String(),
					typ:  inferAggReturnType(aggExpr),
				}
				continue
			}
		}
		result[i] = expr
	}
	return result
}
