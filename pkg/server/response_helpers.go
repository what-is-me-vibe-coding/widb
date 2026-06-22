package server

import "fmt"

// 查询结果类别的指标标签字符串，集中定义避免散落各处理函数中产生拼写错误。
const (
	// MetricQuerySuccess 标记一次查询（含 DML/DDL/SELECT）执行成功。
	MetricQuerySuccess = "success"
	// MetricQueryParseError 标记 SQL 解析阶段失败。
	MetricQueryParseError = "parse_error"
	// MetricQueryAnalyzeError 标记 SQL 语义分析阶段失败。
	MetricQueryAnalyzeError = "analyze_error"
	// MetricQueryExecuteError 标记 SQL 执行阶段失败（含执行器与各 DML/DDL 处理器）。
	MetricQueryExecuteError = "execute_error"
)

// 写入结果类别的指标标签字符串，集中定义避免散落各处理函数中产生拼写错误。
const (
	// MetricWriteSuccess 标记一次写入成功。
	MetricWriteSuccess = "success"
	// MetricWriteTableNotFound 标记写入目标表不存在。
	MetricWriteTableNotFound = "table_not_found"
	// MetricWriteConvertError 标记行数据转换失败。
	MetricWriteConvertError = "convert_error"
	// MetricWriteError 标记底层写入错误。
	MetricWriteError = "write_error"
)

// queryErrResp 构造一个查询类错误响应并递增对应类别的 QueriesTotal 指标。
//
// 该辅助集中处理"递增指标 + 返回 Code=-1 错误响应"这一在 DDL/DML/SELECT 处理器
// 中重复出现 30+ 次的模式，避免散落在各 handler 中的长字符串与重复 fmt.Sprintf。
// 调用方只需一行即可完成原本 2~3 行的样板代码。
//
// kind 取值应使用本文件预定义的 MetricQuery* 常量，避免引入新的标签维度。
// 调用方在 metric 递增失败（极罕见）时仍可继续返回响应；Prometheus 客户端不会
// 因标签不存在而返回 error，故此处不处理返回错误。
func (s *Server) queryErrResp(kind, format string, args ...any) *Response {
	s.metrics.QueriesTotal.WithLabelValues(kind).Inc()
	return &Response{Code: -1, Message: fmt.Sprintf(format, args...)}
}

// querySuccessInc 递增 QueriesTotal 指标的 success 计数。
// 与 queryErrResp 配对使用，保证成功与错误两条路径的指标语义对称。
func (s *Server) querySuccessInc() {
	s.metrics.QueriesTotal.WithLabelValues(MetricQuerySuccess).Inc()
}

// writeErrResp 构造一个写入类错误响应并递增对应类别的 WritesTotal 指标。
//
// kind 取值应使用本文件预定义的 MetricWrite* 常量，避免引入新的标签维度。
func (s *Server) writeErrResp(kind, format string, args ...any) *Response {
	s.metrics.WritesTotal.WithLabelValues(kind).Inc()
	return &Response{Code: -1, Message: fmt.Sprintf(format, args...)}
}

// writeSuccessInc 递增 WritesTotal 指标的 success 计数，按写入行数加权。
func (s *Server) writeSuccessInc(n int) {
	s.metrics.WritesTotal.WithLabelValues(MetricWriteSuccess).Add(float64(n))
}
