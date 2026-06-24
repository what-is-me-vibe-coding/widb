# Server 模块详细设计

## 1. 职责

提供网络接入层，处理客户端连接，解析请求协议，路由到 QueryEngine 或 StorageEngine，返回结果。支持 TCP 自定义协议与 HTTP REST 接口。

## 2. 外部依赖

| 依赖 | 来源 | 用途 |
|------|------|------|
| `net/http` | 标准库 | HTTP REST API |
| `net` | 标准库 | TCP 自定义协议 |

自研：协议编解码、连接管理、请求路由。

## 3. 核心结构

### 3.1 TCP 协议

```go
type Packet struct {
    Magic    uint32   // 0x57494442
    Version  uint16   // 协议版本
    Type     uint8    // Query / Write / Ping
    Length   uint32   // Payload 长度
    Payload  []byte   // JSON 或自定义二进制
}
```

- 固定头部 11 字节，Payload 为 JSON 格式（简化初期实现）。
- Query 请求：`{"sql": "SELECT ..."}`
- Write 请求：`{"table": "t", "rows": [{"id": 1, "name": "a"}]}`
- 响应：`{"code": 0, "data": [...], "rows": 10}`

### 3.2 HTTP REST

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/query` | 执行 SQL 查询 |
| POST | `/write` | 批量写入 |
| GET  | `/health` | 健康检查 |
| GET  | `/metrics` | Prometheus 格式指标 |

- `/query` 请求体：`{"sql": "SELECT ..."}`
- `/write` 请求体：`{"table": "t", "rows": [...]}`

### 3.3 连接管理

```go
type Server struct {
    tcpAddr    string
    httpAddr   string
    engine     *QueryEngine
    storage    *StorageEngine
    wg         sync.WaitGroup
}
```

- TCP 服务端：每个连接一个 goroutine，使用 `bufio.Reader` 解析 Packet。
- HTTP 服务端：标准 `http.Server`，注册 Handler。
- 优雅关闭：捕获 SIGINT/SIGTERM，关闭 Listener，等待活跃请求完成。

## 4. 批量写入优化

- `/write` 接口接收多行数据，一次性写入 WAL，再更新 MemTable。
- 客户端 SDK 提供本地缓冲，满批（如 1000 行）或超时（如 100ms）后发送。

## 5. 监控指标

- `widb_queries_total`：查询总数（按类型标签）。
- `widb_query_duration_seconds`：查询耗时直方图。
- `widb_writes_total`：写入总数。
- `widb_memtable_size_bytes`：当前 MemTable 大小。
- `widb_segment_count`：Segment 数量（按层级标签）。
- `widb_cache_hit_ratio`：BlockCache 命中率。
- `widb_slow_queries_total`：慢查询总数（按来源协议），与 `widb_query_duration_seconds` 互补。配套运维端点 `GET /admin/slow-queries` 提供样本原文（环形缓冲）。详见 [doc/operations.md §10.3](../doc/operations.md#103-慢查询日志端点)。

## 6. 接口定义

```go
type Server interface {
    Start() error
    Stop() error
}
```
