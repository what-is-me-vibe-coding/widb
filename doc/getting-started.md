# WiDB 快速入门指南

本指南将帮助你从零开始安装、启动和使用 WiDB。

## 1. 项目简介

WiDB 是一个面向分析型负载（OLAP）的单机宽表数据库，核心特性包括：

- **高吞吐写入**：基于 LSM-Tree + WAL，支持 GroupCommit，写入吞吐 >= 100k rows/s
- **低延迟查询**：主键索引 + 布隆过滤器 + 稀疏索引多级加速，点查 P99 < 10ms
- **列式存储**：每列独立编码（字典编码/RLE/Bitmap）+ ZSTD 压缩，支持 10,000+ 列
- **SQL 支持**：SELECT/INSERT/CREATE TABLE，WHERE/GROUP BY/LIMIT
- **双协议接入**：TCP 自定义协议 + HTTP REST API
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
```

构建成功后，当前目录下会生成 `widb-server` 和 `widb-cli` 两个可执行文件。

## 4. 启动服务器

### 4.1 基本启动

```bash
./widb-server -tcp 0.0.0.0:9000 -http 0.0.0.0:8080 -data ./data
```

启动后，服务器将监听：
- TCP 端口 `9000`（高性能自定义协议）
- HTTP 端口 `8080`（REST API）

### 4.2 服务器配置参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-tcp` | `0.0.0.0:9000` | TCP 监听地址 |
| `-http` | `0.0.0.0:8080` | HTTP 监听地址 |
| `-data` | `./data` | 数据存储目录 |
| `-max-memtable` | `67108864`（64MB） | MemTable 最大字节数 |
| `-scheduler` | `true` | 启用后台调度器 |
| `-scheduler.flush-interval` | `5s` | 自动刷盘检查间隔 |
| `-scheduler.compact-interval` | `10s` | 自动 Compaction 检查间隔 |
| `-scheduler.wal-clean-interval` | `30s` | WAL 清理检查间隔 |
| `-scheduler.wal-clean-threshold` | `67108864`（64MB） | WAL 文件大小阈值 |

### 4.3 停止服务器

使用 `Ctrl+C` 发送中断信号，服务器会优雅关闭。

## 5. 使用 CLI 客户端

### 5.1 交互模式

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

### 5.2 单行执行模式

使用 `-e` 参数执行单条 SQL 后自动退出：

```bash
./widb-cli -e "CREATE TABLE sensor (id INT64, temp FLOAT64, PRIMARY KEY(id))"
```

### 5.3 CLI 启动参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-tcp` | `127.0.0.1:9000` | 服务器 TCP 地址 |
| `-http` | `127.0.0.1:8080` | 服务器 HTTP 地址 |
| `-mode` | `tcp` | 连接模式：`tcp` 或 `http` |
| `-e` | - | 执行单条 SQL 后退出 |

### 5.4 CLI 内置命令

| 命令 | 说明 |
|------|------|
| `\q` | 退出客户端 |
| `\h` | 显示帮助 |
| `\status` | 检查服务器状态 |
| `\use TCP` | 切换到 TCP 模式 |
| `\use HTTP` | 切换到 HTTP 模式 |

### 5.5 多行输入

SQL 以分号 `;` 结尾，支持多行输入：

```
widb> SELECT id, name
  ...> FROM sensor
  ...> WHERE id = 1;
```

## 6. 使用 HTTP API

### 6.1 健康检查

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

### 6.2 SQL 查询

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

### 6.3 批量写入

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

### 6.4 API 端点一览

| 端点 | 方法 | 说明 |
|------|------|------|
| `/query` | POST | 执行 SQL 查询 |
| `/write` | POST | 批量写入数据 |
| `/health` | GET | 健康检查 |
| `/metrics` | GET | Prometheus 指标 |

## 7. SQL 示例

### 7.1 建表

```sql
CREATE TABLE sensor (
  id INT64,
  name STRING,
  temperature FLOAT64,
  active BOOL,
  PRIMARY KEY (id)
);
```

> **注意**：每张表必须指定 `PRIMARY KEY`。

### 7.2 写入数据

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

### 7.3 查询数据

```sql
-- 点查
SELECT id, name, temperature FROM sensor WHERE id = 1;

-- 聚合查询
SELECT name, AVG(temperature) FROM sensor GROUP BY name;

-- 限制结果数
SELECT * FROM sensor LIMIT 10;
```

## 8. 数据类型

| 类型 | 说明 | 内存大小 |
|------|------|----------|
| `BOOL` | 布尔值 | 1 字节 |
| `INT64` | 64 位整数 | 8 字节 |
| `FLOAT64` | 64 位浮点数 | 8 字节 |
| `STRING` | 变长字符串 | 变长 |
| `TIMESTAMP` | 时间戳 | 8 字节 |

## 9. 配置参数详解

### 9.1 服务器命令行参数

见 [第 4.2 节](#42-服务器配置参数)。

### 9.2 EngineConfig 编程配置

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

## 10. 监控

WiDB 内置 Prometheus 指标，通过 `GET /metrics` 端点暴露（命名空间 `widb_`）。

### 10.1 指标列表

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

### 10.2 Prometheus 集成

在 Prometheus 配置中添加：

```yaml
scrape_configs:
  - job_name: 'widb'
    static_configs:
      - targets: ['localhost:8080']
    metrics_path: '/metrics'
```

## 11. 完整示例：从零开始

以下是一个从安装到查询的完整流程：

```bash
# 1. 构建项目
go build -o widb-server ./cmd/server
go build -o widb-cli ./cmd/cli

# 2. 启动服务器（新终端）
./widb-server -tcp 0.0.0.0:9000 -http 0.0.0.0:8080 -data ./data

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
```

## 12. 常见问题解答（FAQ）

### Q: WiDB 支持哪些操作系统？

A: 目前支持 Linux 和 macOS。

### Q: WiDB 支持 UPDATE 和 DELETE 语句吗？

A: 当前版本仅支持 SELECT、INSERT 和 CREATE TABLE 语句，暂不支持 UPDATE 和 DELETE。

### Q: 建表时必须指定 PRIMARY KEY 吗？

A: 是的，每张表必须指定 `PRIMARY KEY`，它是主键索引的基础，也是点查加速的关键。

### Q: 支持哪些数据类型？

A: 支持 BOOL、INT64、FLOAT64、STRING、TIMESTAMP 五种数据类型。

### Q: TCP 和 HTTP 协议有什么区别？

A: TCP 自定义协议性能更高，适合生产环境的高频查询；HTTP REST API 更易用，适合调试和快速集成。两者功能完全一致。

### Q: 数据存储在哪里？

A: 默认存储在 `./data` 目录下，可通过 `-data` 参数指定其他路径。目录中包含 WAL 文件和 Segment 文件。

### Q: WiDB 支持分布式部署吗？

A: WiDB 是单机数据库，不支持分布式部署。

### Q: 写入数据后多久可以查到？

A: 写入是同步的，写入成功后立即可查。数据先写入 WAL 和 MemTable，查询时会同时扫描 MemTable 和已持久化的 Segment。

### Q: 如何调整写入性能？

A: 可通过调整 `-max-memtable` 参数增大 MemTable 容量，减少刷盘频率；EngineConfig 中的 `SyncMode` 和 `SyncInterval` 可控制 WAL 同步策略。

### Q: 服务器异常退出后数据会丢失吗？

A: 不会。WAL 保证已确认写入的数据零丢失，服务器重启时会自动回放 WAL 进行恢复。

## 13. 故障排查

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

## 14. 文档索引

| 文档 | 说明 |
|------|------|
| [architecture.md](architecture.md) | 系统架构 |
| [storage.md](storage.md) | 存储引擎详解 |
| [query.md](query.md) | 查询引擎详解 |
| [index.md](index.md) | 索引模块详解 |
| [catalog.md](catalog.md) | 元数据管理详解 |
| [common.md](common.md) | 公共模块详解 |
| [api.md](api.md) | API 参考 |
| [performance.md](performance.md) | 性能调优指南 |
