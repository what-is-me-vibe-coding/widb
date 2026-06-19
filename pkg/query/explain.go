package query

import (
	"fmt"
	"strings"
)

// PlanRow 表示 EXPLAIN 输出中的一行，描述查询计划树中的一个节点。
type PlanRow struct {
	ID        int // 节点序号，按深度优先前序遍历自 1 起递增
	Depth     int // 节点在计划树中的深度，根节点为 0
	Operation string
	Detail    string
}

// explain 列名常量，避免 goconst 重复字符串告警。
const (
	explainColID        = "id"
	explainColDepth     = "depth"
	explainColOperation = "operation"
	explainColDetail    = "detail"
)

// ExplainPlanColumns 返回 EXPLAIN 输出的列名，固定为 id/depth/operation/detail。
func ExplainPlanColumns() []string {
	return []string{explainColID, explainColDepth, explainColOperation, explainColDetail}
}

// ExplainPlan 遍历查询计划树，返回按深度优先前序排列的 PlanRow 列表。
// 每个节点输出其操作类型与关键属性（表名、谓词、投影表达式等），
// 便于用户理解查询将如何执行而不实际运行它。
func ExplainPlan(node PlanNode) []PlanRow {
	var rows []PlanRow
	walkPlan(node, 0, &rows)
	return rows
}

// walkPlan 递归遍历计划树，按深度优先前序填充 rows。
// depth 为当前节点深度，id 由 rows 长度推导（自 1 起）。
func walkPlan(node PlanNode, depth int, rows *[]PlanRow) {
	if node == nil {
		return
	}
	op, detail := describePlanNode(node)
	*rows = append(*rows, PlanRow{
		ID:        len(*rows) + 1,
		Depth:     depth,
		Operation: op,
		Detail:    detail,
	})
	for _, child := range node.Children() {
		walkPlan(child, depth+1, rows)
	}
}

// describePlanNode 提取单个计划节点的操作名与详情文本。
func describePlanNode(node PlanNode) (string, string) {
	switch n := node.(type) {
	case *ScanNode:
		return explainOpScan, describeScanNode(n)
	case *FilterNode:
		return explainOpFilter, describeFilterNode(n)
	case *ProjectNode:
		return explainOpProject, describeProjectNode(n)
	case *AggregateNode:
		return explainOpAggregate, describeAggregateNode(n)
	case *LimitNode:
		return explainOpLimit, describeLimitNode(n)
	default:
		return explainOpUnknown, fmt.Sprintf("%T", node)
	}
}

// explain 操作名常量。
const (
	explainOpScan      = "Scan"
	explainOpFilter    = "Filter"
	explainOpProject   = "Project"
	explainOpAggregate = "Aggregate"
	explainOpLimit     = "Limit"
	explainOpUnknown   = "Unknown"
)

// describeScanNode 描述扫描节点：表名、扫描列与可选谓词。
func describeScanNode(n *ScanNode) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Table: %s, Columns: %v", n.Table, n.Columns)
	if n.Predicate != nil {
		fmt.Fprintf(&b, ", Predicate: %s", n.Predicate.String())
	}
	return b.String()
}

// describeFilterNode 描述过滤节点的条件表达式。
func describeFilterNode(n *FilterNode) string {
	return fmt.Sprintf("Condition: %s", n.Condition.String())
}

// describeProjectNode 描述投影节点的输出表达式与别名。
func describeProjectNode(n *ProjectNode) string {
	parts := make([]string, len(n.Expressions))
	for i, e := range n.Expressions {
		if n.Aliases[i] != "" {
			parts[i] = fmt.Sprintf("%s AS %s", e.String(), n.Aliases[i])
		} else {
			parts[i] = e.String()
		}
	}
	return fmt.Sprintf("Expressions: [%s]", strings.Join(parts, ", "))
}

// describeAggregateNode 描述聚合节点的分组列与聚合函数。
func describeAggregateNode(n *AggregateNode) string {
	var b strings.Builder
	if len(n.GroupBy) > 0 {
		gbs := make([]string, len(n.GroupBy))
		for i, g := range n.GroupBy {
			gbs[i] = g.String()
		}
		fmt.Fprintf(&b, "GroupBy: [%s], ", strings.Join(gbs, ", "))
	}
	aggs := make([]string, len(n.Aggregates))
	for i, a := range n.Aggregates {
		aggs[i] = a.String()
	}
	fmt.Fprintf(&b, "Aggregates: [%s]", strings.Join(aggs, ", "))
	return b.String()
}

// describeLimitNode 描述 Limit 节点的偏移与行数。
func describeLimitNode(n *LimitNode) string {
	return fmt.Sprintf("Offset: %d, Count: %d", n.Offset, n.Count)
}
