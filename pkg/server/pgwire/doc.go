// Package pgwire 实现 PostgreSQL wire 协议（v3）服务端，使 WiDB 可被
// JDBC 驱动（如 pgJDBC）及 psql 等标准 PostgreSQL 客户端直接连接。
//
// 协议流程：
//   - 客户端发送 StartupMessage（或先发 SSLRequest 协商 SSL）
//   - 服务端回复 AuthenticationOk + ParameterStatus + BackendKeyData + ReadyForQuery
//   - 客户端发送 Query（Simple Query 协议）
//   - 服务端执行 SQL，回复 RowDescription + DataRow* + CommandComplete + ReadyForQuery
//   - 客户端发送 Terminate 断开连接
//
// 当前实现支持 trust 认证（无密码），仅处理 Simple Query 协议。
// 类型映射：BOOL→16, INT64→int8(20), FLOAT64→float8(701), STRING→text(25), TIMESTAMP→1114。
package pgwire
