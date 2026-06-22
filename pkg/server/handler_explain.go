package server

import (
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/query"
)

// handleExplain 执行 EXPLAIN 语句：对内部 SELECT 进行语义分析与优化，
// 返回查询计划树的结构描述而不实际执行查询。
//
// 输出列为 id/depth/operation/detail，每行对应计划树中的一个节点，
// 按深度优先前序排列。depth 表示节点在树中的层级（根节点为 0），
// 便于客户端按缩进渲染计划树。
func (s *Server) handleExplain(exp *query.ExplainStatement) (*Response, error) {
	plan, err := s.analyzer.Analyze(exp.Inner)
	if err != nil {
		return s.queryErrResp(MetricQueryAnalyzeError, "SQL 分析错误: %v", err), nil
	}

	optimized := s.optimizer.Optimize(plan)

	rows := query.ExplainPlan(optimized)
	data := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		data = append(data, map[string]any{
			"id":        r.ID,
			"depth":     r.Depth,
			"operation": r.Operation,
			"detail":    r.Detail,
		})
	}

	s.querySuccessInc()
	return &Response{
		Code:        0,
		Columns:     query.ExplainPlanColumns(),
		ColumnTypes: []common.DataType{common.TypeInt64, common.TypeInt64, common.TypeString, common.TypeString},
		Data:        data,
		Rows:        len(data),
	}, nil
}
