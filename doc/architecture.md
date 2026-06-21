# WiDB 系统架构

> 想了解端到端时序图、函数调用链、磁盘格式与内存模型？请阅读 [architecture-deep-dive.md](architecture-deep-dive.md)。

## 1. 整体架构

WiDB 采用分层架构，自底向上分为：公共层、存储引擎层、索引层、查询引擎层、元数据层、服务层。

```
┌─────────────────────────────────────────────────┐
│                   服务层 (Server)                │
│  ┌──────────────┐ ┌──────────────┐ ┌──────────┐ │
│  │  TCP Handler │ │ HTTP Handler │ │ PG wire │ │
│  └──────┬───────┘ └──────┬───────┘ └────┬─────┘ │
│         └────────┬────────┴──────────────┘      │
│                  ▼                               │
│           ┌─────────────┐                        │
│           │routingAdapter│（按表名路由 LSM/内存） │
│           └──────┬──────┘                        │
├──────────────────┼──────────────────────────────┤
│                  ▼        查询引擎 (Query)       │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐        │
│  │  Parser  │ │ Analyzer │ │Optimizer │        │
│  └────┬─────┘ └────┬─────┘ └────┬─────┘        │
│       └─────────────┼────────────┘              │
│                     ▼                            │
│              ┌──────────────┐                    │
│              │   Executor   │                    │
│              └──────┬───────┘                    │
├─────────────────────┼───────────────────────────┤
│                     ▼     元数据层 (Catalog)     │
│              ┌──────────────┐                    │
│              │   Catalog    │                    │
│              └──────┬───────┘                    │
├─────────────────────┼───────────────────────────┤
│                     ▼        索引层 (Index)      │
│  ┌──────────────┐ ┌──────────┐ ┌──────────────┐ │
│  │ PrimaryIndex │ │BloomIndex│ │  SparseIndex │ │
│  └──────┬───────┘ └────┬─────┘ └──────┬───────┘ │
│         └──────────────┼──────────────┘         │
├────────────────────────┼────────────────────────┤
│                        ▼   存储引擎 (Storage)   │
│  ┌──────────────────┐  ┌──────────────────────┐ │
│  │  LSM Engine      │  │  Memory Engine       │ │
│  │ WAL/MemTable/    │  │ (ENGINE=memory,      │ │
│  │ Segment/Compactor│  │  内存排序数组,无WAL)  │ │
│  └──────┬───────────┘  └──────────┬───────────┘ │
│         └──────────┬──────────────┘             │
│                    ▼                            │
│  ┌──────────────────────────────────────────────┐│
│  │         BlockCache / IndexCache              ││
│  └──────────────────────────────────────────────┘│
├─────────────────────────────────────────────────┤
│                  公共层 (Common)                 │
│  ┌──────┐ ┌──────┐ ┌───────┐ ┌──────┐          │
│  │Types │ │Errors│ │Bitmap │ │ Pool │          │
│  └──────┘ └──────┘ └───────┘ └──────┘          │
└─────────────────────────────────────────────────┘
```

## 2. 模块依赖关系

模块间遵循严格的单向依赖规则，禁止循环依赖：

```
common ← catalog ← storage ← index ← query ← server
```

| 模块 | 可依赖 | 职责 |
|------|--------|------|
| `common` | 无 | 基础类型（10 种 DataType）、错误码、Bitmap、内存池 |
| `catalog` | `common` | Schema 管理、元数据持久化、表级 Engine 标记 |
| `storage` | `common`, `catalog` | LSM 引擎（WAL/MemTable/Segment/编码/压缩）+ `storage/memory` 内存引擎 |
| `index` | `common`, `catalog`, `storage` | 主键索引、布隆过滤器、稀疏索引 |
| `query` | `common`, `catalog`, `index`, `storage` | SQL 解析、执行计划、向量化算子、DML（UPDATE/DELETE） |
| `server` | 所有 pkg | TCP/HTTP/PG wire 协议层、表路由（routingAdapter）、监控 |
| `config` | 无 | YAML 配置加载与带注释模板生成 |

## 3. 数据写入路径

```
客户端请求
    │
    ▼
Server.handleWrite()
    │
    ├─ 1. Catalog.GetTable() 获取表定义
    ├─ 2. convertWriteRow() 类型转换
    └─ 3. Engine.WriteBatch()
           │
           ├─ a. 分配版本号 (Engine.mu.Lock)
           ├─ b. 序列化 WAL 记录
           ├─ c. WAL.AppendWrite() 顺序写入
           ├─ d. WAL.Sync() 或 GroupCommit
           ├─ e. MemTable.Put() 写入内存
           │     └─ 若 MemTable 满则 rotateMemTable()
           └─ f. 等待 WAL sync 完成
```

关键设计：
- **锁分离**：WAL I/O 在引擎锁外执行，减少锁持有时间
- **GroupCommit**：多个写入共享一次 fsync，提升吞吐
- **MemTable 轮转**：活跃 MemTable 满后冻结为 immutable，新建活跃 MemTable

## 4. 数据读取路径

```
SQL 查询
    │
    ▼
Parser.Parse() → AST
    │
    ▼
Analyzer.Analyze() → 逻辑计划
    │
    ▼
Optimizer.Optimize() → 物理计划
    │  ├─ 列裁剪
    ├─ 谓词下推
    └─ 常量折叠
    │
    ▼
Executor.Execute()
    │
    ├─ ScanNode: 扫描数据
    │   ├─ PrimaryIndex.Lookup() 点查
    │   ├─ SparseIndex 过滤 Segment
    │   ├─ BloomIndex 过滤不存在的键
    │   └─ ScanRange() 范围扫描
    ├─ FilterNode: 向量化过滤
    ├─ ProjectNode: 列投影
    ├─ AggregateNode: 聚合计算
    └─ LimitNode: 结果截断
    │
    ▼
Chunk 结果流
```

## 5. 后台任务

Scheduler 调度器定时执行三个后台任务：

| 任务 | 默认间隔 | 说明 |
|------|----------|------|
| Flush | 5s | 将 immutable MemTable 刷写为 Segment |
| Compaction | 10s | 合并 L0 小 Segment 为 L1 大 Segment |
| WAL Clean | 30s | 清理超过阈值的旧 WAL 文件 |

### Compaction 流程

```
L0 Segments (多个小 Segment)
    │
    ▼  Compactor.Compact()
    │
    ├─ 1. 选取 L0 Segment 集合
    ├─ 2. 合并读取所有行（按主键排序，去重保留最新版本）
    ├─ 3. 重新编码写入新 Segment
    ├─ 4. 注册新 Segment 到索引
    ├─ 5. 删除旧 Segment 文件
    └─ 6. 更新 Catalog
```

## 6. 存储格式

### Segment 文件格式

```
┌────────────────────────────────────────┐
│              Segment Header            │
│  Magic (4B) | Version (2B) | ID (8B)  │
│  RowCount (8B) | ColumnCount (4B)     │
├────────────────────────────────────────┤
│           Column Block 1               │
│  ┌──────────────────────────────────┐  │
│  │ Encoding Type (1B)               │  │
│  │ Compressed Size (4B)             │  │
│  │ Uncompressed Size (4B)           │  │
│  │ Data (ZSTD compressed)           │  │
│  └──────────────────────────────────┘  │
├────────────────────────────────────────┤
│           Column Block 2               │
│              ...                       │
├────────────────────────────────────────┤
│           Column Block N               │
├────────────────────────────────────────┤
│              Segment Footer            │
│  Index Offset (8B)                     │
│  Column Meta (Min/Max/NullCount)       │
│  Bloom Filter Data                     │
│  Magic (4B)                            │
└────────────────────────────────────────┘
```

### 编码方式

| 编码 | 适用场景 | 原理 |
|------|----------|------|
| Plain | 随机数据 | 直接存储原始值 |
| Dictionary | 低基数列 | 值→字典ID映射，存储ID序列 |
| RLE | 连续重复值 | (值, 重复次数) 对 |
| Bitmap | BOOL / 低基数 | 位图表示，1 bit/值 |

所有编码后的数据使用 ZSTD 压缩进一步减小体积。

## 7. TCP 协议

TCP 协议采用固定包头 + 变长负载的格式：

```
┌──────────────────────────────────────────┐
│              Packet Header (11B)         │
│  Magic (4B) | Version (2B) | Type (1B)  │
│  Length (4B)                              │
├──────────────────────────────────────────┤
│              Payload (JSON)              │
│  最大 16MB                               │
└──────────────────────────────────────────┘
```

| 包类型 | 值 | 方向 | 说明 |
|--------|-----|------|------|
| PacketQuery | 1 | Client→Server | SQL 查询请求 |
| PacketWrite | 2 | Client→Server | 批量写入请求 |
| PacketPing | 3 | Client→Server | 心跳检测 |
| PacketResponse | 10 | Server→Client | 统一响应 |

- Magic: `0x57494442` ("WIDB")
- Version: `1`
- 字节序: 大端序 (Big-Endian)
- 超时: 读 30s / 写 10s

## 8. PostgreSQL wire 协议

WiDB 通过 `pkg/server/pgwire/` 实现 PostgreSQL wire 协议（v3）服务端，使标准 PG 客户端（psql、pgJDBC、BI 工具）可直连。默认监听 `0.0.0.0:5432`，由 `server.Config.PGAddr` 控制，留空不启用。

协议流程（Simple Query）：

```
客户端                          服务端
  │                               │
  │── StartupMessage ────────────▶│
  │◀─ AuthenticationOk ───────────│
  │◀─ ParameterStatus* ───────────│
  │◀─ BackendKeyData ─────────────│
  │◀─ ReadyForQuery ──────────────│
  │                               │
  │── Query("SELECT ...") ───────▶│
  │◀─ RowDescription ─────────────│
  │◀─ DataRow* ───────────────────│
  │◀─ CommandComplete ────────────│
  │◀─ ReadyForQuery ──────────────│
  │                               │
  │── Terminate ─────────────────▶│
```

类型映射（`pgwire/types.go`）：

| WiDB 类型 | PG OID | PG 类型 |
|-----------|--------|---------|
| BOOL | 16 | bool |
| INT8 / INT16 | 21 | int2 |
| INT32 | 23 | int4 |
| INT64 / UINT64 | 20 | int8 |
| FLOAT64 | 701 | float8 |
| STRING | 25 | text |
| DATE | 1082 | date |
| TIMESTAMP | 1114 | timestamp |

资源保护：默认最大并发连接 256、单次读取空闲超时 5 分钟、单次写入超时 30 秒，可通过 `WithMaxConns` / `WithIdleTimeout` / `WithWriteTimeout` 选项覆盖。当前为 trust 认证，生产部署应绑定回环/内网或前置认证网关。详见 [pgwire.md](pgwire.md)。

## 9. 多存储引擎路由

WiDB 支持按表选择存储引擎，由 `pkg/server/table_engine.go` 的 `routingAdapter` 实现：

```
SQL 请求
    │
    ▼
routingAdapter.engineForTable(table)
    │
    ├─ 表注册了内存引擎？─▶ MemoryEngine（pkg/storage/memory）
    │                       └─ 内存排序数组，二分查找，无 WAL/Compaction
    └─ 否 ───────────────▶ LSM Engine（pkg/storage.Engine）
                            └─ WAL + MemTable + Segment + Compaction
```

- 建表时 `CREATE TABLE ... ENGINE=memory` 选择内存引擎，默认为 LSM。
- `catalog.Table.Engine` 字段记录引擎类型，`TableEngine` 接口统一抽象两类引擎。
- 内存引擎适合临时表、维度表、元数据表等高频小表查询，零 I/O 延迟但不持久化。
- 两类引擎可在同一 Server 中按表共存，DML（INSERT/UPDATE/DELETE）与查询均经 `routingAdapter` 透明路由。

## 10. 缓存架构

### BlockCache

- 算法: LRU
- 默认容量: 256MB
- 最大单条目: 1MB（超过不缓存，防止冷数据污染）
- 缓存对象: 解压后的列 Block 数据

### IndexCache

- 算法: LRU
- 默认容量: 1000 条目
- 缓存对象: Segment 级稀疏索引与布隆过滤器

## 11. 崩溃恢复

WiDB 通过 WAL 保证崩溃恢复的数据一致性（仅适用于 LSM 引擎表，内存引擎表不持久化）：

1. **写入流程**：先写 WAL，再写 MemTable
2. **刷盘触发**：MemTable 满后冻结，刷写为不可变 Segment
3. **Checkpoint**：刷盘成功后写入 WAL Checkpoint 记录
4. **恢复流程**：
   - 加载已有 Segment 文件
   - 打开 WAL，回放 Checkpoint 之后的 Write 记录
   - 恢复 MemTable 中的未刷盘数据

## 12. 并发安全

| 组件 | 并发策略 |
|------|----------|
| LSM Engine | RWMutex（写操作用写锁，读操作用读锁） |
| Memory Engine | RWMutex（读操作持读锁，写操作持写锁，rows 保持有序） |
| MemTable | 内部同步（并发跳表） |
| WAL | 内部序列化（顺序追加写） |
| Catalog | RWMutex |
| PrimaryIndex | RWMutex |
| BlockCache / IndexCache | 内部同步（sync.Map / LRU 锁） |
| GroupCommitter | Mutex + Channel 通知 |
| Scheduler | Mutex + stopCh 控制 |
| routingAdapter | RWMutex（memEngines map 保护） |
| pgwire.Server | sync.WaitGroup + 信号量限流 + sync.Once 停机 |
