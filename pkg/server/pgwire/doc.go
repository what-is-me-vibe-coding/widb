// Package pgwire 实现 PostgreSQL wire 协议（v3）服务端，使 WiDB 可被
// JDBC 驱动（如 pgJDBC）及 psql 等标准 PostgreSQL 客户端直接连接。
//
// # 协议流程
//
// 客户端发送 StartupMessage（或先发 SSLRequest 协商 SSL），服务端回复
// AuthenticationOk + ParameterStatus + BackendKeyData + ReadyForQuery 完
// 成握手。之后进入查询循环：
//
//   - Simple Query：客户端发 Query('Q')，服务端回复 RowDescription +
//     DataRow* + CommandComplete + ReadyForQuery。同步、阻塞式，最简。
//   - Extended Query：客户端发 Parse → Bind → Describe → Execute → Sync，
//     服务端按阶段回复 ParseComplete / BindComplete / ParameterDescription /
//     RowDescription / NoData / DataRow* / CommandComplete / ReadyForQuery。
//     支持预编译语句（prepared statement）与 portal 复用，便于 BI 驱动
//     （pgx、psql、DBeaver、DataGrip、Navicat 等）走标准 JDBC 路径。
//
// 当前实现支持 trust 认证（无密码）。类型映射：BOOL→16, INT8/16→int2(21),
// INT32→int4(23), INT64/UINT64→int8(20), FLOAT64→float8(701),
// STRING→text(25), DATE→date(1082), TIMESTAMP→1114。
//
// # Extended Query 简化说明
//
//  1. 占位符（$1/$2/...）不被解析，Bind 携带的参数值被忽略。客户端通常
//     在 server-side prepared statement 路径下发送完整 SQL 而非参数化形式。
//  2. Describe('S') / Describe('P') 一律返回 NoData；RowDescription 在
//     Execute 阶段按实际结果集补发，符合 PG 协议对 late-binding 的要求。
//  3. Parse / Bind / Describe 失败进入错误状态：后续消息被吸收，直到
//     Sync 消息清除状态。Execute 错误不进错误状态（PG 规范）。
//  4. 同一连接内 prepared statement / portal 命名空间相互隔离（按连接
//     持有 map），不跨连接共享。
//
// # 安全与资源保护
//
// 当前认证方式为 trust（AuthenticationOk），任何能连上监听端口的客户端均可执行 SQL。
// 在不可信网络中部署时，应通过下列方式限制访问：
//   - 将监听地址绑定到回环或内网（如 127.0.0.1），避免暴露到公网；
//   - 在网络边界（防火墙/安全组）限制端口访问来源；
//   - 通过反向代理或带认证的网关前置 pgwire 端口。
//
// 为防止恶意客户端通过大量连接或长空闲连接耗尽 goroutine，NewServer 默认启用：
//   - 最大并发连接数（defaultMaxConns=256），超限连接立即关闭；
//   - 单次读取空闲超时（defaultIdleTimeout=5m），空闲连接自动断开；
//   - 单次写入超时（defaultWriteTimeout=30s），慢客户端不会阻塞 goroutine。
//
// 以上参数可通过 WithMaxConns / WithIdleTimeout / WithWriteTimeout 选项覆盖。
package pgwire
