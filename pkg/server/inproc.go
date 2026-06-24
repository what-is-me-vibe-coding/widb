package server

// ExecuteQuery 在进程内直接执行 SQL 查询，跳过网络协议层。
// 供 cmd/widb 一键启动模式使用，零网络开销。
// 返回的 Response 与通过 TCP/HTTP 接口得到的结构一致。
// 慢查询日志中的 source 标记为 SlowQuerySourceInProc，便于与外部客户端区分。
func (s *Server) ExecuteQuery(sql string) (*Response, error) {
	return s.handleQuerySource(SlowQuerySourceInProc, &QueryRequest{SQL: sql})
}

// ExecuteWrite 在进程内直接批量写入数据，跳过网络协议层。
// 供 cmd/widb 一键启动模式使用，零网络开销。
// table 为目标表名，rows 为待写入的行数据（列名到值的映射）。
// 慢查询日志中的 source 标记为 SlowQuerySourceInProc，便于与外部客户端区分。
func (s *Server) ExecuteWrite(table string, rows []map[string]any) (*Response, error) {
	return s.handleWriteSource(SlowQuerySourceInProc, &WriteRequest{Table: table, Rows: rows})
}

// Ping 返回心跳响应字符串，用于进程内健康检查。
func (s *Server) Ping() string {
	return msgPong
}
