# WiDB - 宽表数据库

WiDB 是一个面向分析型负载（OLAP）的单机宽表数据库，支持高吞吐写入与低延迟点查/范围扫描，采用列式存储与 LSM-Tree 架构。

## 核心特性

- **高吞吐写入**：基于 LSM-Tree + WAL 的写入路径，支持 GroupCommit 批量同步，写入吞吐 >= 100k rows/s
- **低延迟查询**：主键索引 + 布隆过滤器 + 稀疏索引多级加速，点查 P99 < 10ms
- **列式存储**：每列独立编码（字典编码/RLE/Bitmap）+ ZSTD 压缩，支持 10,000+ 列的宽表
- **SQL 支持**：支持 SELECT/INSERT/CREATE TABLE 语句，含 WHERE 过滤、GROUP BY 聚合、LIMIT 等
- **双协议接入**：TCP 自定义协议（高性能）+ HTTP REST API（易用）
- **可观测性**：内置 Prometheus 指标，覆盖查询/写入/缓存/Compaction 等关键维度
- **崩溃恢复**：WAL 顺序写 + 回放恢复，保证数据零丢失

## 快速开始

### 构建

```bash
go build -o widb-server ./cmd/server
go build -o widb-cli ./cmd/cli
```

### 启动服务器

```bash
./widb-server [选项]
```

| 选项 | 默认值 | 说明 |
|------|--------|------|
| `-tcp` | `0.0.0.0:9000` | TCP 监听地址 |
| `-http` | `0.0.0.0:8080` | HTTP 监听地址 |
| `-data` | `./data` | 数据存储目录 |
| `-max-memtable` | `67108864` (64MB) | MemTable 最大字节数 |
| `-scheduler` | `true` | 启用后台调度器 |
| `-scheduler.flush-interval` | `5s` | 自动刷盘检查间隔 |
| `-scheduler.compact-interval` | `10s` | 自动 Compaction 检查间隔 |
| `-scheduler.wal-clean-interval` | `30s` | WAL 清理检查间隔 |
| `-scheduler.wal-clean-threshold` | `67108864` (64MB) | WAL 文件大小阈值 |

### 使用 CLI 客户端

```bash
# 交互模式（默认 TCP）
./widb-cli

# 指定 HTTP 模式
./widb-cli -mode http

# 执行单条 SQL
./widb-cli -e "CREATE TABLE sensor (id INT64, temp FLOAT64, PRIMARY KEY(id))"
```

CLI 内置命令：

| 命令 | 说明 |
|------|------|
| `\q` | 退出客户端 |
| `\h` | 显示帮助 |
| `\status` | 检查服务器状态 |
| `\use TCP` | 切换到 TCP 模式 |
| `\use HTTP` | 切换到 HTTP 模式 |

### SQL 示例

```sql
-- 建表
CREATE TABLE sensor (
  id INT64,
  name STRING,
  temperature FLOAT64,
  active BOOL,
  PRIMARY KEY (id)
);

-- 插入数据（通过 HTTP API）
-- POST /write
-- {"table": "sensor", "rows": [{"id": 1, "name": "sensor-1", "temperature": 23.5, "active": true}]}

-- 查询
SELECT id, name, temperature FROM sensor WHERE id = 1;
SELECT name, AVG(temperature) FROM sensor GROUP BY name;
SELECT * FROM sensor LIMIT 10;
```

## HTTP API

| 端点 | 方法 | 说明 |
|------|------|------|
| `/query` | POST | 执行 SQL 查询 |
| `/write` | POST | 批量写入数据 |
| `/health` | GET | 健康检查 |
| `/metrics` | GET | Prometheus 指标 |

### 查询请求

```bash
curl -X POST http://localhost:8080/query \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT * FROM sensor WHERE id = 1"}'
```

响应格式：
```json
{
  "code": 0,
  "data": [...],
  "rows": 1
}
```

### 写入请求

```bash
curl -X POST http://localhost:8080/write \
  -H "Content-Type: application/json" \
  -d '{"table": "sensor", "rows": [{"id": 1, "name": "sensor-1", "temperature": 23.5}]}'
```

响应格式：
```json
{
  "code": 0,
  "rows": 1
}
```

## 支持的数据类型

| 类型 | 说明 | 内存大小 |
|------|------|----------|
| `BOOL` | 布尔值 | 1 字节 |
| `INT64` | 64 位整数 | 8 字节 |
| `FLOAT64` | 64 位浮点数 | 8 字节 |
| `STRING` | 变长字符串 | 变长 |
| `TIMESTAMP` | 时间戳 | 8 字节 |

## 项目结构

```
test-db/
├── cmd/
│   ├── cli/            # 命令行客户端
│   └── server/         # 服务器入口
├── pkg/
│   ├── storage/        # 存储引擎（WAL/MemTable/Segment/Compaction/编码/压缩）
│   ├── index/          # 索引（主键索引/布隆过滤器/稀疏索引）
│   ├── query/          # 查询引擎（解析器/分析器/优化器/执行器）
│   ├── catalog/        # 元数据管理（Schema/表定义/持久化）
│   ├── server/         # 服务层（TCP/HTTP/协议/监控指标）
│   └── common/         # 公共类型（数据类型/错误码/Bitmap/内存池）
├── tests/
│   └── integration/    # 集成测试
├── doc/                # 详细文档
│   ├── getting-started.md # 快速入门指南
│   ├── architecture.md # 系统架构
│   ├── storage.md      # 存储引擎详解
│   ├── query.md        # 查询引擎详解
│   ├── index.md        # 索引模块详解
│   ├── catalog.md      # 元数据管理详解
│   ├── common.md       # 公共模块详解
│   └── api.md          # API 参考
└── .agent_plan/        # 开发设计文档
```

## 监控指标

WiDB 暴露以下 Prometheus 指标（命名空间 `widb_`）：

| 指标 | 类型 | 说明 |
|------|------|------|
| `widb_queries_total` | Counter | 查询总数（按类型：success/parse_error/analyze_error/execute_error） |
| `widb_query_duration_seconds` | Histogram | 查询耗时分布 |
| `widb_writes_total` | Counter | 写入总数（按结果：success/table_not_found/convert_error/write_error） |
| `widb_write_duration_seconds` | Histogram | 写入耗时分布 |
| `widb_memtable_size_bytes` | Gauge | 当前 MemTable 大小 |
| `widb_segment_count` | Gauge | Segment 数量（按层级：l0/l1） |
| `widb_l0_segment_count` | Gauge | L0 Segment 数量 |
| `widb_wal_size_bytes` | Gauge | WAL 文件大小 |
| `widb_active_connections` | Gauge | 当前活跃连接数 |
| `widb_flush_total` | Counter | 刷盘总次数 |
| `widb_compact_total` | Counter | Compaction 总次数 |
| `widb_wal_clean_total` | Counter | WAL 清理总次数 |
| `widb_cache_hits_total` | Counter | 缓存命中次数（按类型：block/index） |
| `widb_cache_misses_total` | Counter | 缓存未命中次数 |
| `widb_cache_size_bytes` | Gauge | 缓存占用字节数 |
| `widb_cache_entries` | Gauge | 缓存条目数 |

## 依赖

| 库 | 用途 |
|---|------|
| `github.com/klauspost/compress/zstd` | Block 级 ZSTD 压缩与解压 |
| `github.com/bits-and-blooms/bloom/v3` | 布隆过滤器 |
| `github.com/xwb1989/sqlparser` | SQL 词法/语法解析（MySQL 方言） |
| `github.com/cespare/xxhash/v2` | 高性能哈希 |
| `github.com/prometheus/client_golang` | Prometheus 监控指标 |

## 许可证

本项目为内部开发项目。
