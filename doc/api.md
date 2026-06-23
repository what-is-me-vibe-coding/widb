# API 参考

## 1. HTTP REST API

### 1.1 查询

**POST /query**

执行 SQL 查询语句。

请求：
```json
{
  "sql": "SELECT id, name FROM sensor WHERE id = 1"
}
```

成功响应（HTTP 200）：
```json
{
  "code": 0,
  "data": [
    {"id": 1, "name": "sensor-1"}
  ],
  "rows": 1
}
```

错误响应（HTTP 400）：
```json
{
  "code": -1,
  "message": "SQL 解析错误: ..."
}
```

### 1.2 写入

**POST /write**

批量写入数据到指定表。

请求：
```json
{
  "table": "sensor",
  "rows": [
    {"id": 1, "name": "sensor-1", "temperature": 23.5, "active": true},
    {"id": 2, "name": "sensor-2", "temperature": 18.2, "active": false}
  ]
}
```

成功响应（HTTP 200）：
```json
{
  "code": 0,
  "rows": 2
}
```

错误响应（HTTP 400）：
```json
{
  "code": -1,
  "message": "表不存在: ..."
}
```

### 1.3 健康检查

**GET /health**

返回服务健康状态。

响应（HTTP 200）：
```json
{
  "status": "ok",
  "timestamp": "2026-06-13T12:00:00.000Z",
  "scheduler": {
    "flush_count": 42,
    "compact_count": 10,
    "wal_clean_count": 5,
    "last_error": ""
  }
}
```

### 1.4 Prometheus 指标

**GET /metrics**

返回 Prometheus 格式的监控指标。详见 README 中的监控指标表。

### 1.5 管理与运维端点

`/admin/*` 系列端点用于运维介入，**不参与业务请求**。请求与返回格式均使用 `code/message/...` 包装。

| 端点 | 方法 | 用途 | 成功响应 | 失败语义 |
|------|------|------|----------|----------|
| `/admin/flush` | POST | 把每张 LSM 表的活跃/不可变 MemTable 立即落盘 | `{"code":0,"message":"强制 flush 成功","affected":N}` | 任一表失败 500；非 POST 405 |
| `/admin/compact` | POST | 立即尝试对每张 LSM 表做一次 Compaction | `{"code":0,"message":"强制 compact 成功","affected":N}` | 同上 |
| `/admin/stats` | GET | 全库 + 每张表的元信息与运行时统计 | 见下 | 非 GET 405 |

`/admin/stats` 响应字段：

| 字段 | 类型 | 含义 |
|------|------|------|
| `summary.total_tables` | int | 全库表总数 |
| `summary.lsm_tables` | int | LSM 表数量 |
| `summary.memory_tables` | int | 内存表数量 |
| `summary.total_segments` | int | 所有 LSM 表的 Segment 总数 |
| `summary.total_rows` | int64 | 所有表存活行数之和 |
| `tables[].name` | string | 表名 |
| `tables[].engine` | string | `lsm` 或 `memory` |
| `tables[].columns` | int | 列数 |
| `tables[].primary_key` | []string | 主键列名 |
| `tables[].row_count` | int64 | 存活行数（LSM=MemTable+Segment；memory=字典 keys） |
| `tables[].segment_count` | int（仅 LSM） | Segment 总数 |
| `tables[].l0_segment_count` | int（仅 LSM） | L0 层 Segment 数；持续偏高表示 Compaction 滞后 |
| `tables[].immutable_count` | int（仅 LSM） | 不可变 MemTable 数 |
| `tables[].memtable_size` | int64（仅 LSM） | 活跃 MemTable 当前字节数 |
| `tables[].active_row_count` | int64（仅 LSM） | 活跃 MemTable 中行数 |
| `tables[].immutable_row_count` | int64（仅 LSM） | 不可变 MemTable 行数之和 |

完整示例与运维场景见 [doc/operations.md §10.2](operations.md#102-数据库统计端点)。

## 2. TCP 协议

### 2.1 协议格式

```
┌──────────────────────────────────────────┐
│              Packet Header (11 字节)     │
│  Magic (4B) | Version (2B) | Type (1B)  │
│  Length (4B)                              │
├──────────────────────────────────────────┤
│              Payload (变长, JSON)        │
│  最大 16MB                               │
└──────────────────────────────────────────┘
```

| 字段 | 大小 | 说明 |
|------|------|------|
| Magic | 4B | 固定值 `0x57494442` ("WIDB") |
| Version | 2B | 协议版本，当前为 `1` |
| Type | 1B | 包类型 |
| Length | 4B | Payload 长度（字节） |
| Payload | 变长 | JSON 格式的请求/响应体 |

字节序：大端序 (Big-Endian)

### 2.2 包类型

| 类型 | 值 | 方向 | Payload |
|------|-----|------|---------|
| PacketQuery | 1 | C→S | `{"sql": "..."}` |
| PacketWrite | 2 | C→S | `{"table": "...", "rows": [...]}` |
| PacketPing | 3 | C→S | 空 |
| PacketResponse | 10 | S→C | `{"code": 0, "data": ..., "rows": ..., "message": ...}` |

### 2.3 连接管理

- **超时**：读超时 30s，写超时 10s
- **最大连接数**：通过 `MaxConnections` 配置，0 表示不限制
- **长连接**：支持在一个 TCP 连接上发送多个请求

## 3. CLI 命令

### 3.1 启动选项

```bash
widb-cli [选项]
```

| 选项 | 默认值 | 说明 |
|------|--------|------|
| `-tcp` | `127.0.0.1:9000` | 服务器 TCP 地址 |
| `-http` | `127.0.0.1:8080` | 服务器 HTTP 地址 |
| `-mode` | `tcp` | 连接模式：`tcp` 或 `http` |
| `-e` | - | 执行单条 SQL 后退出 |

### 3.2 交互命令

| 命令 | 说明 |
|------|------|
| `\q` / `\quit` | 退出客户端 |
| `\h` / `\help` | 显示帮助 |
| `\status` | 检查服务器连通性 |
| `\use TCP` | 切换到 TCP 模式 |
| `\use HTTP` | 切换到 HTTP 模式 |

### 3.3 多行输入

SQL 语句以分号 `;` 结尾，支持多行输入：

```
widb> SELECT id, name
  ...> FROM sensor
  ...> WHERE id = 1;
```

## 4. 服务器配置

### 4.1 命令行参数

```bash
widb-server [选项]
```

| 选项 | 默认值 | 说明 |
|------|--------|------|
| `-tcp` | `0.0.0.0:9000` | TCP 监听地址 |
| `-http` | `0.0.0.0:8080` | HTTP 监听地址 |
| `-data` | `./data` | 数据存储目录 |
| `-max-memtable` | `67108864` (64MB) | MemTable 最大字节数 |
| `-scheduler` | `true` | 启用后台调度器 |
| `-scheduler.flush-interval` | `5s` | 刷盘检查间隔 |
| `-scheduler.compact-interval` | `10s` | Compaction 检查间隔 |
| `-scheduler.wal-clean-interval` | `30s` | WAL 清理检查间隔 |
| `-scheduler.wal-clean-threshold` | `67108864` (64MB) | WAL 文件大小阈值 |

### 4.2 EngineConfig 编程配置

```go
cfg := storage.EngineConfig{
    DataDir:                "./data",
    MaxMemTableSize:        64 * 1024 * 1024,  // 64MB
    BlockCacheSize:         256 * 1024 * 1024,  // 256MB
    BlockCacheMaxEntrySize: 1024 * 1024,        // 1MB
    IndexCacheSize:         1000,
    SyncMode:               storage.SyncEveryWrite,
    SyncInterval:           time.Millisecond,
}
```

### 4.3 ServerConfig 编程配置

```go
cfg := server.Config{
    TCPAddr:         "0.0.0.0:9000",
    HTTPAddr:        "0.0.0.0:8080",
    DataDir:         "./data",
    MaxMemTableSize: 64 * 1024 * 1024,
    MaxConnections:  0,  // 不限制
    EnableScheduler: true,
    SchedulerConfig: storage.SchedulerConfig{
        FlushInterval:     5 * time.Second,
        CompactInterval:   10 * time.Second,
        WALCleanInterval:  30 * time.Second,
        WALCleanThreshold: 64 << 20,
    },
}
```

## 5. 错误码

| Code | 含义 |
|------|------|
| 0 | 成功 |
| -1 | 错误（见 `message` 字段获取详情） |

常见错误消息：
- `SQL 解析错误: ...` — SQL 语法不正确
- `SQL 分析错误: ...` — 语义错误（如表不存在、列不存在）
- `SQL 执行错误: ...` — 执行时错误
- `表不存在: ...` — 写入时引用了不存在的表
- `行数据转换错误: ...` — 写入数据类型不匹配
- `写入错误: ...` — 存储引擎写入失败
