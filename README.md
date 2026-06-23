# WiDB - 宽表数据库

WiDB 是一个面向分析型负载（OLAP）的单机宽表数据库，支持高吞吐写入与低延迟点查/范围扫描，采用列式存储与 LSM-Tree 架构。

## 核心特性

- **高吞吐写入**：基于 LSM-Tree + WAL 的写入路径，支持 GroupCommit 批量同步，写入吞吐 >= 100k rows/s
- **低延迟查询**：主键索引 + 布隆过滤器 + 稀疏索引多级加速，点查 P99 < 10ms
- **列式存储**：每列独立编码（字典编码/RLE/Bitmap）+ ZSTD 压缩，支持 10,000+ 列的宽表
- **SQL 支持**：SELECT/INSERT/UPDATE/DELETE/CREATE TABLE，含 WHERE 过滤、GROUP BY 聚合、LIMIT、LIKE 等
- **多协议接入**：TCP 自定义协议（高性能）+ HTTP REST API（易用）+ PostgreSQL wire 协议（JDBC/psql 直连）
- **双存储引擎**：默认 LSM-Tree 引擎（持久化 + 崩溃恢复）+ 内存引擎（`ENGINE=memory`，零 I/O 延迟临时表）
- **丰富类型**：10 种类型，含整数族 INT8/INT16/INT32/INT64/UINT64、DATE、BOOL、FLOAT64、STRING、TIMESTAMP
- **可观测性**：内置 Prometheus 指标，覆盖查询/写入/缓存/Compaction 等关键维度
- **崩溃恢复**：WAL 顺序写 + 回放恢复，保证数据零丢失

## 快速开始

### 构建

```bash
go build -o widb-server ./cmd/server
go build -o widb-cli ./cmd/cli
go build -o widb ./cmd/widb      # 一键启动：同进程 server + CLI
```

### 一键启动（推荐体验）

`widb` 二进制在同进程内同时启动 server 与交互式 CLI，CLI 通过进程内调用执行 SQL，零网络开销，外部客户端仍可通过 TCP/HTTP 连接：

```bash
./widb
```

进入交互式 REPL 后输入 SQL（以分号结尾）执行查询，`\q` 退出并关闭服务。支持 `-e` 单条 SQL 执行、`-config` 配置文件、以及与 `widb-server` 一致的覆盖参数。

```bash
# 生成配置模板
./widb -gen-config widb.yaml

# 使用配置文件启动
./widb -config widb.yaml

# 执行单条 SQL 后退出
./widb -e "SELECT * FROM sensor LIMIT 10"
```

REPL 内置命令：

| 命令 | 说明 |
|------|------|
| `\q` | 退出并关闭服务 |
| `\h` | 显示帮助 |
| `\status` | 显示服务状态 |
| `\addrs` | 显示监听地址 |

### 启动服务器

```bash
./widb-server [选项]
```

支持两种配置方式：YAML 配置文件与命令行参数。命令行参数会覆盖配置文件中的同名项，配置采用分层覆盖：**默认值 < 配置文件 < 命令行参数**。

#### YAML 配置文件

未指定 `-config` 时，依次查找当前目录下的 `widb.yaml`、`config.yaml`；均不存在则使用默认值。可用 `-gen-config widb.yaml` 生成带注释的默认配置模板：

```bash
./widb-server -gen-config widb.yaml   # 生成模板后退出
./widb-server -config widb.yaml       # 使用指定配置文件启动
```

配置文件示例（完整字段见生成的模板）：

```yaml
server:
  tcp_addr: "0.0.0.0:9000"
  http_addr: "0.0.0.0:8080"
storage:
  data_dir: "./data"
  max_memtable_size: 67108864
scheduler:
  enabled: true
  flush_interval: 5s
  compact_interval: 10s
  wal_clean_interval: 30s
  wal_clean_threshold: 67108864
```

#### 命令行选项

| 选项 | 默认值 | 说明 |
|------|--------|------|
| `-config` | （空） | 配置文件路径（YAML），未指定时查找 `./widb.yaml`、`./config.yaml` |
| `-gen-config` | （空） | 生成带注释的默认配置模板到指定路径后退出 |
| `-tcp` | `0.0.0.0:9000` | TCP 监听地址（覆盖配置文件） |
| `-http` | `0.0.0.0:8080` | HTTP 监听地址（覆盖配置文件） |
| `-pg` | `0.0.0.0:5432` | PostgreSQL wire 协议监听地址（覆盖配置文件，留空禁用） |
| `-data` | `./data` | 数据存储目录（覆盖配置文件） |
| `-max-memtable` | `67108864` (64MB) | MemTable 最大字节数（覆盖配置文件） |
| `-scheduler` | `true` | 启用后台调度器（覆盖配置文件） |
| `-scheduler.flush-interval` | `5s` | 自动刷盘检查间隔（覆盖配置文件） |
| `-scheduler.compact-interval` | `10s` | 自动 Compaction 检查间隔（覆盖配置文件） |
| `-scheduler.wal-clean-interval` | `30s` | WAL 清理检查间隔（覆盖配置文件） |
| `-scheduler.wal-clean-threshold` | `67108864` (64MB) | WAL 文件大小阈值（覆盖配置文件） |

### 使用 CLI 客户端

```bash
# 交互模式（默认 TCP）
./widb-cli

# 指定 HTTP 模式
./widb-cli -mode http

# 执行单条 SQL
./widb-cli -e "CREATE TABLE sensor (id INT64, temp FLOAT64, PRIMARY KEY(id))"
```

CLI 支持四种结果输出格式，可在交互模式中用 `\format <格式>` 切换：

| 格式 | 说明 |
|------|------|
| `pretty` | ClickHouse 风格 Unicode 表格（默认） |
| `vertical` | 垂直行块（类似 `\G`） |
| `json` | JSON 数组 |
| `csv` | CSV |

CLI 内置命令：

| 命令 | 说明 |
|------|------|
| `\q` | 退出客户端 |
| `\h` | 显示帮助 |
| `\status` | 检查服务器状态 |
| `\use TCP` | 切换到 TCP 模式 |
| `\use HTTP` | 切换到 HTTP 模式 |
| `\format <格式>` | 切换输出格式（pretty/vertical/json/csv） |

### SQL 示例

```sql
-- 建表（默认 LSM 引擎，持久化）
CREATE TABLE sensor (
  id INT64,
  name STRING,
  temperature FLOAT64,
  active BOOL,
  PRIMARY KEY (id)
);

-- 建表（内存引擎，零 I/O 延迟，重启后数据丢失）
CREATE TABLE cache_dim (
  id INT32,
  label STRING,
  PRIMARY KEY (id)
) ENGINE=memory;

-- 插入数据（通过 HTTP API）
-- POST /write
-- {"table": "sensor", "rows": [{"id": 1, "name": "sensor-1", "temperature": 23.5, "active": true}]}

-- 更新数据
UPDATE sensor SET temperature = 24.0, active = true WHERE id = 1;

-- 删除数据
DELETE FROM sensor WHERE active = false;

-- 查询
SELECT id, name, temperature FROM sensor WHERE id = 1;
SELECT name, AVG(temperature) FROM sensor GROUP BY name;
SELECT * FROM sensor WHERE name LIKE '%al%' LIMIT 10;
```

## HTTP API

| 端点 | 方法 | 说明 |
|------|------|------|
| `/query` | POST | 执行 SQL 查询 |
| `/write` | POST | 批量写入数据 |
| `/health` | GET | 健康检查 |
| `/metrics` | GET | Prometheus 指标 |
| `/admin/flush` | POST | 强制 flush LSM MemTable |
| `/admin/compact` | POST | 强制触发一次 LSM Compaction |
| `/admin/stats` | GET | 全库 + 每张表实时统计（行数/Segment数/MemTable大小等） |

> 完整的 `/admin/*` 字段说明与运维示例见 [doc/operations.md §10](doc/operations.md#10-常见运维操作清单) 与 [doc/api.md §1.5](doc/api.md#15-管理与运维端点)。

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

## PostgreSQL wire 协议

WiDB 内置 PostgreSQL wire 协议（v3）服务端，可被 JDBC 驱动（如 pgJDBC）及 `psql` 等标准 PostgreSQL 客户端直接连接。默认监听 `0.0.0.0:5432`，可通过 `-pg` 参数或配置文件 `server.pg_addr` 修改，留空则不启用。

```bash
# 启动时启用 PG wire（默认配置模板已开启 5432）
./widb-server

# 用 psql 连接
psql -h 127.0.0.1 -p 5432 -c "SELECT id, name FROM sensor WHERE id = 1"
```

类型映射：BOOL→bool(16)、INT8/INT16→int2(21)、INT32→int4(23)、INT64/UINT64→int8(20)、FLOAT64→float8(701)、STRING→text(25)、DATE→date(1082)、TIMESTAMP→timestamp(1114)。

当前实现为 trust 认证（无密码），同时支持 Simple Query 与 Extended Query 协议（pgx、psql、DBeaver、DataGrip、Navicat 等真实客户端均可直连），适合内网与开发环境。生产部署请将监听地址绑定到回环/内网，或在网络边界限制访问。详见 [doc/pgwire.md](doc/pgwire.md)。

## 支持的数据类型

| 类型 | 说明 | 内存大小 | SQL 别名 |
|------|------|----------|----------|
| `BOOL` | 布尔值 | 1 字节 | BOOLEAN, TINYINT |
| `INT8` | 8 位整数 | 8 字节* | TINYINT UNSIGNED |
| `INT16` | 16 位整数 | 8 字节* | SMALLINT |
| `INT32` | 32 位整数 | 8 字节* | MEDIUMINT |
| `INT64` | 64 位整数 | 8 字节 | BIGINT, INT |
| `UINT64` | 无符号 64 位整数 | 8 字节 | BIGINT UNSIGNED |
| `FLOAT64` | 64 位浮点数 | 8 字节 | DOUBLE, FLOAT |
| `STRING` | 变长字符串 | 变长 | TEXT, VARCHAR, CHAR |
| `DATE` | 日期（无时间） | 8 字节* | DATE |
| `TIMESTAMP` | 时间戳 | 8 字节 | TIMESTAMP, DATETIME |

> \* 整数族（INT8/16/32/UINT64/DATE）统一以 int64 字段存储，故内存固定 8 字节；它们共享编码、索引与统计路径，仅在类型标签、显示与取值范围上存在差异。跨整数族类型按 int64 比较，使 `WHERE int8_col = 5`（字面量为 INT64）可正确命中。

## 项目结构

```
test-db/
├── cmd/
│   ├── cli/            # 命令行客户端（远程连接，多格式输出）
│   ├── server/         # 服务器入口
│   └── widb/           # 一键启动：同进程 server + CLI
├── pkg/
│   ├── storage/        # 存储引擎（WAL/MemTable/Segment/Compaction/编码/压缩）
│   │   └── memory/     # 内存引擎（ENGINE=memory）
│   ├── index/          # 索引（主键索引/布隆过滤器/稀疏索引）
│   ├── query/          # 查询引擎（解析器/分析器/优化器/执行器）
│   ├── catalog/        # 元数据管理（Schema/表定义/持久化）
│   ├── server/         # 服务层（TCP/HTTP/PG wire/协议/监控/表路由）
│   │   └── pgwire/     # PostgreSQL wire 协议（JDBC/psql 接入）
│   ├── config/         # YAML 配置加载与模板生成
│   └── common/         # 公共类型（数据类型/错误码/Bitmap/内存池）
├── tests/
│   └── integration/    # 集成测试（端到端 SQL/多客户端/内存引擎）
├── doc/                # 详细文档
│   ├── getting-started.md # 快速入门指南
│   ├── architecture.md # 系统架构
│   ├── storage.md      # 存储引擎详解
│   ├── query.md        # 查询引擎详解
│   ├── index.md        # 索引模块详解
│   ├── catalog.md      # 元数据管理详解
│   ├── common.md       # 公共模块详解
│   ├── server.md       # 服务层模块详解
│   ├── pgwire.md       # PostgreSQL wire 协议详解
│   ├── types.md        # 数据类型参考
│   ├── dml.md          # DML（INSERT/UPDATE/DELETE）详解
│   ├── api.md          # API 参考
│   ├── performance.md  # 性能调优指南
│   ├── tutorial.md     # 端到端上手教程（场景化示例）
│   ├── sql-reference.md  # SQL 语法权威参考
│   ├── cookbook.md     # 常见 SQL 套路与最佳实践
│   ├── troubleshooting.md # 故障排查指南
│   ├── operations.md   # 运维手册（部署/监控/备份/恢复/容量规划/升级）
│   ├── observability.md # 可观测性指南（指标参考 + PromQL + Grafana + 告警规则）
│   ├── development.md  # 开发与贡献指南
│   └── architecture-deep-dive.md # 架构深度解析（时序图/调用链/格式/内存模型）
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
| `widb_http_requests_total` | Counter | HTTP 请求总数（按端点/方法/状态类别 2xx/3xx/4xx/5xx） |
| `widb_http_request_duration_seconds` | Histogram | HTTP 请求耗时分布（按端点/方法） |

完整的指标参考、PromQL 模板、Grafana 仪表板蓝图与告警规则见 [doc/observability.md](doc/observability.md)。

## 依赖

| 库 | 用途 |
|---|------|
| `github.com/klauspost/compress/zstd` | Block 级 ZSTD 压缩与解压 |
| `github.com/bits-and-blooms/bloom/v3` | 布隆过滤器 |
| `github.com/xwb1989/sqlparser` | SQL 词法/语法解析（MySQL 方言） |
| `github.com/cespare/xxhash/v2` | 高性能哈希 |
| `github.com/prometheus/client_golang` | Prometheus 监控指标 |
| `github.com/jackc/pgproto3/v2` | PostgreSQL wire 协议（v3）消息编解码 |
| `github.com/jedib0t/go-pretty/v6` | CLI 表格渲染（ClickHouse 风格 Unicode 表格） |
| `go.yaml.in/yaml/v2` | YAML 配置文件解析 |

## 许可证

本项目为内部开发项目。
