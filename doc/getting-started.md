# WiDB 快速入门指南

本指南将帮助你从零开始安装、启动和使用 WiDB。

## 1. 项目简介

WiDB 是一个面向分析型负载（OLAP）的单机宽表数据库，核心特性包括：

- **高吞吐写入**：基于 LSM-Tree + WAL，支持 GroupCommit，写入吞吐 >= 100k rows/s
- **低延迟查询**：主键索引 + 布隆过滤器 + 稀疏索引多级加速，点查 P99 < 10ms
- **列式存储**：每列独立编码（字典编码/RLE/Bitmap）+ ZSTD 压缩，支持 10,000+ 列
- **SQL 支持**：SELECT/INSERT/UPDATE/DELETE/CREATE TABLE，WHERE/GROUP BY/LIMIT/LIKE
- **多协议接入**：TCP 自定义协议 + HTTP REST API + PostgreSQL wire 协议（JDBC/psql）
- **双存储引擎**：LSM 引擎（持久化）+ 内存引擎（`ENGINE=memory`）
- **可观测性**：内置 Prometheus 指标
- **崩溃恢复**：WAL 顺序写 + 回放恢复

## 2. 环境要求

| 要求 | 说明 |
|------|------|
| Go 版本 | 1.25 及以上 |
| 操作系统 | Linux / macOS |

验证 Go 环境：

```bash
go version
# 应输出 go version go1.25.x ... 或更高版本
```

## 3. 构建与安装

克隆项目后，在项目根目录下执行：

```bash
# 构建服务器
go build -o widb-server ./cmd/server

# 构建命令行客户端
go build -o widb-cli ./cmd/cli

# 构建一键启动二进制（同进程 server + CLI）
go build -o widb ./cmd/widb
```

构建成功后，当前目录下会生成 `widb-server`、`widb-cli` 与 `widb` 三个可执行文件。

## 4. 一键启动（推荐体验）

`widb` 二进制在同进程内同时启动 server 与交互式 CLI：主 goroutine 运行 CLI REPL，另一 goroutine 启动 server（TCP+HTTP 监听）。CLI 通过进程内调用执行 SQL，零网络开销，外部客户端仍可通过 TCP/HTTP 连接。

### 4.1 交互模式

```bash
./widb
```

进入 REPL 后输入 SQL（以分号结尾）执行查询，`\q` 退出并关闭服务：

```
widb> SELECT * FROM sensor LIMIT 10;
10 行:
[...]
widb> \q
再见!
```

### 4.2 单条 SQL 执行

```bash
./widb -e "SELECT * FROM sensor LIMIT 10"
```

### 4.3 配置文件

`widb` 与 `widb-server` 共用同一套配置参数，支持 `-config`、`-gen-config` 及所有覆盖参数：

```bash
./widb -gen-config widb.yaml   # 生成带注释的配置模板
./widb -config widb.yaml       # 使用配置文件启动
```

### 4.4 REPL 内置命令

| 命令 | 说明 |
|------|------|
| `\q` | 退出并关闭服务 |
| `\h` | 显示帮助 |
| `\status` | 显示服务状态 |
| `\addrs` | 显示监听地址 |

## 5. 启动服务器（独立模式）

### 5.1 基本启动

```bash
./widb-server -tcp 0.0.0.0:9000 -http 0.0.0.0:8080 -pg 0.0.0.0:5432 -data ./data
```

启动后，服务器将监听：
- TCP 端口 `9000`（高性能自定义协议，widb-cli 默认使用）
- HTTP 端口 `8080`（REST API）
- PostgreSQL wire 端口 `5432`（JDBC/psql 直连，留空 `-pg ""` 可禁用）

### 5.2 服务器配置参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-tcp` | `0.0.0.0:9000` | TCP 监听地址 |
| `-http` | `0.0.0.0:8080` | HTTP 监听地址 |
| `-pg` | `0.0.0.0:5432` | PostgreSQL wire 协议监听地址（留空禁用） |
| `-data` | `./data` | 数据存储目录 |
| `-max-memtable` | `67108864`（64MB） | MemTable 最大字节数 |
| `-scheduler` | `true` | 启用后台调度器 |
| `-scheduler.flush-interval` | `5s` | 自动刷盘检查间隔 |
| `-scheduler.compact-interval` | `10s` | 自动 Compaction 检查间隔 |
| `-scheduler.wal-clean-interval` | `30s` | WAL 清理检查间隔 |
| `-scheduler.wal-clean-threshold` | `67108864`（64MB） | WAL 文件大小阈值 |

### 5.3 停止服务器

使用 `Ctrl+C` 发送中断信号，服务器会优雅关闭。

## 6. 使用 CLI 客户端

### 6.1 交互模式

```bash
# 默认使用 TCP 协议连接
./widb-cli

# 使用 HTTP 协议连接
./widb-cli -mode http
```

进入交互模式后，提示符为 `widb>`，输入 SQL 语句并以分号 `;` 结尾即可执行：

```
widb> CREATE TABLE sensor (id INT64, temp FLOAT64, PRIMARY KEY(id));
成功

widb> SELECT * FROM sensor LIMIT 10;
0 行:
[]
```

### 6.2 单行执行模式

使用 `-e` 参数执行单条 SQL 后自动退出：

```bash
./widb-cli -e "CREATE TABLE sensor (id INT64, temp FLOAT64, PRIMARY KEY(id))"
```

### 6.3 CLI 启动参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-tcp` | `127.0.0.1:9000` | 服务器 TCP 地址 |
| `-http` | `127.0.0.1:8080` | 服务器 HTTP 地址 |
| `-mode` | `tcp` | 连接模式：`tcp` 或 `http` |
| `-e` | - | 执行单条 SQL 后退出 |

### 6.4 CLI 内置命令

| 命令 | 说明 |
|------|------|
| `\q` | 退出客户端 |
| `\h` | 显示帮助 |
| `\status` | 检查服务器状态 |
| `\use TCP` | 切换到 TCP 模式 |
| `\use HTTP` | 切换到 HTTP 模式 |
| `\format <格式>` | 切换输出格式：`pretty`（ClickHouse 风格表格，默认）/`vertical`/`json`/`csv` |

### 6.5 多行输入

SQL 以分号 `;` 结尾，支持多行输入：

```
widb> SELECT id, name
  ...> FROM sensor
  ...> WHERE id = 1;
```

## 7. 使用 HTTP API

### 7.1 健康检查

```bash
curl http://localhost:8080/health
```

响应示例：

```json
{
  "status": "ok",
  "timestamp": "2026-06-14T12:00:00.000Z",
  "scheduler": {
    "flush_count": 0,
    "compact_count": 0,
    "wal_clean_count": 0,
    "last_error": ""
  }
}
```

### 7.2 SQL 查询

```bash
curl -X POST http://localhost:8080/query \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT * FROM sensor WHERE id = 1"}'
```

成功响应：

```json
{
  "code": 0,
  "data": [{"id": 1, "name": "sensor-1", "temperature": 23.5}],
  "rows": 1
}
```

### 7.3 批量写入

```bash
curl -X POST http://localhost:8080/write \
  -H "Content-Type: application/json" \
  -d '{"table": "sensor", "rows": [{"id": 1, "name": "sensor-1", "temperature": 23.5, "active": true}]}'
```

成功响应：

```json
{
  "code": 0,
  "rows": 1
}
```

### 7.4 API 端点一览

| 端点 | 方法 | 说明 |
|------|------|------|
| `/query` | POST | 执行 SQL 查询 |
| `/write` | POST | 批量写入数据 |
| `/health` | GET | 健康检查 |
| `/metrics` | GET | Prometheus 指标 |

## 8. SQL 示例

### 8.1 建表

```sql
-- 默认 LSM 引擎（持久化 + 崩溃恢复）
CREATE TABLE sensor (
  id INT64,
  name STRING,
  temperature FLOAT64,
  active BOOL,
  PRIMARY KEY (id)
);

-- 内存引擎（零 I/O 延迟，重启后数据丢失，适合临时表/维度表）
CREATE TABLE cache_dim (
  id INT32,
  label STRING,
  PRIMARY KEY (id)
) ENGINE=memory;
```

> **注意**：每张表必须指定 `PRIMARY KEY`。`ENGINE=memory` 可选，默认为 LSM 引擎。

### 8.2 写入数据

通过 HTTP API 批量写入：

```bash
curl -X POST http://localhost:8080/write \
  -H "Content-Type: application/json" \
  -d '{
    "table": "sensor",
    "rows": [
      {"id": 1, "name": "sensor-1", "temperature": 23.5, "active": true},
      {"id": 2, "name": "sensor-2", "temperature": 18.2, "active": false},
      {"id": 3, "name": "sensor-3", "temperature": 25.0, "active": true}
    ]
  }'
```

### 8.3 更新与删除

```sql
-- 更新：按 WHERE 过滤后对匹配行应用 SET 赋值并重新写入
UPDATE sensor SET temperature = 24.0, active = true WHERE id = 1;

-- 删除：按 WHERE 过滤后删除匹配行（无 WHERE 则清空全表数据，保留表结构）
DELETE FROM sensor WHERE active = false;
```

> UPDATE 若导致主键变更且新主键已存在，返回冲突错误。详见 [dml.md](dml.md)。

### 8.4 查询数据

```sql
-- 点查
SELECT id, name, temperature FROM sensor WHERE id = 1;

-- 聚合查询
SELECT name, AVG(temperature) FROM sensor GROUP BY name;

-- LIKE 模糊匹配
SELECT * FROM sensor WHERE name LIKE '%al%';

-- 限制结果数
SELECT * FROM sensor LIMIT 10;
```

## 9. 数据类型

WiDB 支持 10 种数据类型，整数族（INT8/16/32/64/UINT64/DATE）统一以 int64 存储，共享编码与索引路径：

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
| `DATE` | 日期（无时间，自 1970-01-01 起的天数） | 8 字节* | DATE |
| `TIMESTAMP` | 时间戳 | 8 字节 | TIMESTAMP, DATETIME |

> \* 整数族统一以 int64 字段存储，故内存固定 8 字节。完整类型说明见 [types.md](types.md)。

## 10. PostgreSQL wire 协议接入

WiDB 内置 PG wire（v3）服务端，可被 `psql`、JDBC 驱动等标准 PostgreSQL 客户端直连。默认监听 `0.0.0.0:5432`，可在配置文件 `server.pg_addr` 或 `-pg` 参数中修改，留空则不启用。

```bash
# 用 psql 连接执行查询
psql -h 127.0.0.1 -p 5432 -c "SELECT id, name FROM sensor WHERE id = 1"

# 用 psql 进入交互模式
psql -h 127.0.0.1 -p 5432
```

JDBC 连接字符串示例：`jdbc:postgresql://127.0.0.1:5432/`。当前为 trust 认证，支持 Simple Query 与 Extended Query 协议（pgx、psql、DBeaver、DataGrip、Navicat 等真实客户端均可直连），类型映射与安全注意事项详见 [pgwire.md](pgwire.md)。

## 11. 配置参数详解

### 11.1 服务器命令行参数

见 [第 5.2 节](#52-服务器配置参数)。

### 11.2 EngineConfig 编程配置

通过 Go 代码嵌入 WiDB 时，可使用 `EngineConfig` 进行细粒度配置：

```go
cfg := storage.EngineConfig{
    DataDir:                "./data",
    MaxMemTableSize:        64 * 1024 * 1024,   // 64MB
    BlockCacheSize:         256 * 1024 * 1024,  // 256MB
    BlockCacheMaxEntrySize: 1024 * 1024,        // 1MB
    IndexCacheSize:         1000,
    SyncMode:               storage.SyncEveryWrite,
    SyncInterval:           time.Millisecond,
}
```

| 字段 | 默认值 | 说明 |
|------|--------|------|
| `DataDir` | `./data` | 数据存储目录 |
| `MaxMemTableSize` | 64MB | MemTable 最大容量 |
| `BlockCacheSize` | 256MB | BlockCache 容量，<=0 不缓存 |
| `BlockCacheMaxEntrySize` | 1MB | 单个缓存条目最大大小，超过不缓存 |
| `IndexCacheSize` | 1000 | IndexCache 条目数，<=0 不缓存 |
| `SyncMode` | SyncEveryWrite | WAL 同步模式 |
| `SyncInterval` | 1ms | GroupCommit 同步间隔 |

## 11. 监控

WiDB 内置 Prometheus 指标，通过 `GET /metrics` 端点暴露（命名空间 `widb_`）。

### 11.1 指标列表

| 指标 | 类型 | 说明 |
|------|------|------|
| `widb_queries_total` | Counter | 查询总数（标签 type: success/parse_error/analyze_error/execute_error） |
| `widb_query_duration_seconds` | Histogram | 查询耗时分布 |
| `widb_writes_total` | Counter | 写入总数（标签 result: success/table_not_found/convert_error/write_error） |
| `widb_write_duration_seconds` | Histogram | 写入耗时分布 |
| `widb_memtable_size_bytes` | Gauge | 当前 MemTable 大小 |
| `widb_segment_count` | Gauge | Segment 数量（标签 level: l0/l1） |
| `widb_l0_segment_count` | Gauge | L0 Segment 数量 |
| `widb_wal_size_bytes` | Gauge | WAL 文件大小 |
| `widb_active_connections` | Gauge | 当前活跃连接数 |
| `widb_flush_total` | Counter | 刷盘总次数 |
| `widb_compact_total` | Counter | Compaction 总次数 |
| `widb_wal_clean_total` | Counter | WAL 清理总次数 |
| `widb_cache_hits_total` | Counter | 缓存命中次数（标签 cache: block/index） |
| `widb_cache_misses_total` | Counter | 缓存未命中次数（标签 cache: block/index） |
| `widb_cache_size_bytes` | Gauge | 缓存占用字节数（标签 cache: block/index） |
| `widb_cache_entries` | Gauge | 缓存条目数（标签 cache: block/index） |

### 11.2 Prometheus 集成

在 Prometheus 配置中添加：

```yaml
scrape_configs:
  - job_name: 'widb'
    static_configs:
      - targets: ['localhost:8080']
    metrics_path: '/metrics'
```

## 12. 完整示例：从零开始

以下是一个从安装到查询的完整流程：

```bash
# 1. 构建项目
go build -o widb-server ./cmd/server
go build -o widb-cli ./cmd/cli

# 2. 启动服务器（新终端）
./widb-server -tcp 0.0.0.0:9000 -http 0.0.0.0:8080 -pg 0.0.0.0:5432 -data ./data

# 3. 健康检查
curl http://localhost:8080/health

# 4. 创建表
./widb-cli -e "CREATE TABLE sensor (id INT64, name STRING, temperature FLOAT64, active BOOL, PRIMARY KEY (id))"

# 5. 写入数据
curl -X POST http://localhost:8080/write \
  -H "Content-Type: application/json" \
  -d '{"table": "sensor", "rows": [{"id": 1, "name": "sensor-1", "temperature": 23.5, "active": true}, {"id": 2, "name": "sensor-2", "temperature": 18.2, "active": false}]}'

# 6. 查询数据
./widb-cli -e "SELECT id, name, temperature FROM sensor WHERE id = 1"

# 7. 聚合查询
./widb-cli -e "SELECT name, AVG(temperature) FROM sensor GROUP BY name"

# 8. 更新与删除
./widb-cli -e "UPDATE sensor SET temperature = 24.0 WHERE id = 1"
./widb-cli -e "DELETE FROM sensor WHERE active = false"
```

## 13. 常见问题解答（FAQ）

### Q: WiDB 支持哪些操作系统？

A: 目前支持 Linux 和 macOS。

### Q: WiDB 支持 UPDATE 和 DELETE 语句吗？

A: 支持。WiDB 支持 SELECT、INSERT、UPDATE、DELETE 和 CREATE TABLE 语句。UPDATE 按 WHERE 过滤后对匹配行应用 SET 赋值并重新写入；DELETE 按 WHERE 过滤后删除匹配行（无 WHERE 则清空全表数据，保留表结构）。详见 [dml.md](dml.md)。

### Q: 建表时必须指定 PRIMARY KEY 吗？

A: 是的，每张表必须指定 `PRIMARY KEY`，它是主键索引的基础，也是点查加速的关键。

### Q: 支持哪些数据类型？

A: 支持 10 种数据类型：BOOL、INT8、INT16、INT32、INT64、UINT64、FLOAT64、STRING、DATE、TIMESTAMP。整数族（INT8/16/32/64/UINT64/DATE）统一以 int64 存储。详见 [types.md](types.md)。

### Q: TCP、HTTP 和 PostgreSQL wire 协议有什么区别？

A: TCP 自定义协议性能更高，适合生产环境的高频查询（widb-cli 默认使用）；HTTP REST API 更易用，适合调试和快速集成；PostgreSQL wire 协议（默认 5432 端口）使标准 PG 客户端（psql、JDBC 驱动、BI 工具）可直接连接。三者功能一致，均走完整的 SQL 执行管线。

### Q: 数据存储在哪里？

A: 默认存储在 `./data` 目录下，可通过 `-data` 参数指定其他路径。目录中包含 WAL 文件和 Segment 文件。注意：`ENGINE=memory` 的内存表不落盘，重启后数据丢失。

### Q: WiDB 支持分布式部署吗？

A: WiDB 是单机数据库，不支持分布式部署。

### Q: 写入数据后多久可以查到？

A: 写入是同步的，写入成功后立即可查。数据先写入 WAL 和 MemTable，查询时会同时扫描 MemTable 和已持久化的 Segment。

### Q: 如何调整写入性能？

A: 可通过调整 `-max-memtable` 参数增大 MemTable 容量，减少刷盘频率；EngineConfig 中的 `SyncMode` 和 `SyncInterval` 可控制 WAL 同步策略。对可重建的高频小表，使用 `ENGINE=memory` 可获得零 I/O 延迟。

### Q: 服务器异常退出后数据会丢失吗？

A: LSM 引擎表不会丢失。WAL 保证已确认写入的数据零丢失，服务器重启时会自动回放 WAL 进行恢复。但 `ENGINE=memory` 的内存表不持久化，重启后丢失。

## 14. 故障排查

### 服务器启动失败

**现象**：运行 `./widb-server` 报错退出。

**排查步骤**：

1. 检查端口是否被占用：
   ```bash
   # 检查 9000 端口
   lsof -i :9000
   # 检查 8080 端口
   lsof -i :8080
   ```
   若端口被占用，使用 `-tcp` 和 `-http` 参数指定其他端口。

2. 检查数据目录权限：
   ```bash
   ls -la ./data
   ```
   确保当前用户对数据目录有读写权限。

3. 检查 Go 版本：
   ```bash
   go version
   ```
   确保版本 >= 1.25。

### CLI 连接失败

**现象**：`./widb-cli` 提示连接失败。

**排查步骤**：

1. 确认服务器已启动：
   ```bash
   curl http://localhost:8080/health
   ```

2. 确认地址和端口正确：
   - CLI 默认连接 `127.0.0.1:9000`（TCP）或 `127.0.0.1:8080`（HTTP）
   - 如果服务器使用了自定义端口，需通过 `-tcp` 或 `-http` 参数指定

3. 检查防火墙设置，确保端口未被阻止。

### SQL 执行报错

**现象**：执行 SQL 返回错误。

**常见错误及解决方法**：

| 错误消息 | 原因 | 解决方法 |
|----------|------|----------|
| `SQL 解析错误` | SQL 语法不正确 | 检查 SQL 语法，确保语句以分号结尾 |
| `SQL 分析错误: 表不存在` | 引用了不存在的表 | 先执行 CREATE TABLE 创建表 |
| `SQL 分析错误: 列不存在` | 引用了表中不存在的列 | 检查列名拼写，确认列在建表时已定义 |
| `表不存在` | 写入时引用了不存在的表 | 先创建表再写入数据 |
| `行数据转换错误` | 写入数据类型不匹配 | 检查写入数据的类型是否与表定义一致 |

### 查询性能慢

**排查步骤**：

1. 查看 Prometheus 指标，确认缓存命中率：
   ```bash
   curl http://localhost:8080/metrics | grep widb_cache
   ```
   如果缓存命中率低，考虑增大 `BlockCacheSize` 或 `IndexCacheSize`。

2. 检查 L0 Segment 数量：
   ```bash
   curl http://localhost:8080/metrics | grep widb_l0_segment
   ```
   L0 Segment 过多会影响查询性能，确认 Compaction 调度器正常运行（`-scheduler` 参数默认启用）。

3. 确保查询使用了主键索引，点查性能最优。

### 数据目录占用空间过大

**排查步骤**：

1. 检查 WAL 文件大小：
   ```bash
   curl http://localhost:8080/metrics | grep widb_wal_size
   ```
   如果 WAL 文件持续增长，检查调度器的 WAL 清理是否正常（`-scheduler.wal-clean-interval` 和 `-scheduler.wal-clean-threshold`）。

2. 手动触发清理：确保 `-scheduler` 参数为 `true`（默认），调度器会定期清理过期的 WAL 文件。

## 15. 文档索引

| 文档 | 说明 |
|------|------|
| [architecture.md](architecture.md) | 系统架构 |
| [storage.md](storage.md) | 存储引擎详解 |
| [query.md](query.md) | 查询引擎详解 |
| [index.md](index.md) | 索引模块详解 |
| [catalog.md](catalog.md) | 元数据管理详解 |
| [common.md](common.md) | 公共模块详解 |
| [server.md](server.md) | 服务层模块详解 |
| [pgwire.md](pgwire.md) | PostgreSQL wire 协议详解 |
| [types.md](types.md) | 数据类型参考 |
| [dml.md](dml.md) | DML（INSERT/UPDATE/DELETE）详解 |
| [api.md](api.md) | API 参考 |
| [performance.md](performance.md) | 性能调优指南 |
| [tutorial.md](tutorial.md) | 端到端上手教程（场景化示例） |
| [sql-reference.md](sql-reference.md) | SQL 语法权威参考 |
| [cookbook.md](cookbook.md) | 常见 SQL 套路与最佳实践 |
| [troubleshooting.md](troubleshooting.md) | 故障排查指南 |
