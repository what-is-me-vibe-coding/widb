# WiDB 性能调优指南

本指南介绍 WiDB 的性能特征、关键配置参数和调优策略，帮助你在不同工作负载下获得最佳性能。

## 1. 性能基准

WiDB 在默认配置下的参考性能指标：

| 指标 | 参考值 | 说明 |
|------|--------|------|
| 写入吞吐 | >= 100k rows/s | 单线程批量写入，SyncEveryWrite 模式 |
| 写入吞吐（GroupCommit） | >= 200k rows/s | 高并发写入，SyncGroupCommit 模式 |
| 点查延迟 P99 | < 10ms | 主键等值查询，数据在缓存中 |
| 范围扫描 | 取决于数据量 | 利用稀疏索引跳过无关 Segment |

> 以上数据为参考值，实际性能取决于硬件配置、数据特征和查询模式。

## 2. 写入性能调优

### 2.1 WAL 同步模式

WAL 同步模式是影响写入吞吐的最关键参数：

| 模式 | 配置值 | 吞吐 | 数据安全性 | 适用场景 |
|------|--------|------|------------|----------|
| `SyncEveryWrite` | 默认 | 基准 | 最高，零丢失 | 金融、交易等对数据持久性要求极高的场景 |
| `SyncGroupCommit` | `SyncGroupCommit` | 2x+ | 崩溃时可能丢失最近 1ms 数据 | 日志、监控、IoT 等可容忍极小丢失的场景 |

**配置方式**：

```go
// 命令行：暂无直接参数，需通过编程配置
// 编程配置：
cfg := storage.EngineConfig{
    SyncMode:     storage.SyncGroupCommit,
    SyncInterval: time.Millisecond, // GroupCommit 最大同步间隔
}
```

**GroupCommit 工作原理**：

```
写入者1 ──Submit()──► 等待 ◄────┐
写入者2 ──Submit()──► 等待 ◄────┤── 一次 fsync ──► 通知所有等待者
写入者3 ──Submit()──► 等待 ◄────┘
```

多个并发写入共享一次 `fsync`，将 N 次磁盘同步摊销为 1 次。后台 goroutine 在有写入时立即触发 sync，同时用定时器兜底确保不超过 `SyncInterval`。

### 2.2 MemTable 大小

MemTable 大小影响刷盘频率和写入放大：

| 参数 | 默认值 | 影响 |
|------|--------|------|
| `-max-memtable` | 64MB | 值越大，刷盘频率越低，写入吞吐越高，但内存占用更大 |

**调优建议**：

- **高吞吐场景**：增大到 128MB~256MB，减少刷盘频率
- **内存受限场景**：保持 32MB~64MB，避免 OOM
- **宽表场景**（10,000+ 列）：适当增大，因为每行占用更多内存

```bash
# 增大 MemTable 到 128MB
./widb-server -max-memtable 134217728
```

### 2.3 批量写入

WiDB 支持通过 HTTP API 批量写入，单次请求可包含多行数据：

```bash
# 批量写入多行（推荐）
curl -X POST http://localhost:8080/write \
  -H "Content-Type: application/json" \
  -d '{"table": "sensor", "rows": [
    {"id": 1, "temp": 23.5},
    {"id": 2, "temp": 18.2},
    {"id": 3, "temp": 25.0}
  ]}'
```

**最佳实践**：
- 单次批量写入 100~1000 行，减少网络往返和锁竞争
- 避免单行写入，吞吐会显著下降
- TCP 协议的批量写入性能优于 HTTP

### 2.4 WAL AppendBatch 优化

WAL 的 `AppendBatch` 方法将多条记录合并为单次 `Write` 系统调用，减少 I/O 开销：

```
逐条写入：  Write(rec1) → Write(rec2) → Write(rec3)  (3 次系统调用)
批量写入：  Write(rec1 + rec2 + rec3)                  (1 次系统调用)
```

此优化在 Engine 的 `WriteBatch` 中自动生效，无需额外配置。

## 3. 查询性能调优

### 3.1 缓存配置

WiDB 提供两级缓存，对查询性能至关重要：

| 缓存 | 默认容量 | 缓存对象 | 影响 |
|------|----------|----------|------|
| BlockCache | 256MB | 解压后的列 Block 数据 | 减少重复解压开销 |
| IndexCache | 1000 条目 | Segment 级稀疏索引与布隆过滤器 | 减少索引查找开销 |

**BlockCache 调优**：

```go
cfg := storage.EngineConfig{
    BlockCacheSize:         512 * 1024 * 1024,  // 增大到 512MB
    BlockCacheMaxEntrySize: 2 * 1024 * 1024,    // 增大到 2MB
}
```

- **缓存命中率高**（>80%）：当前配置合理
- **缓存命中率低**：增大 `BlockCacheSize` 或检查查询模式是否过于随机
- **宽表场景**：增大 `BlockCacheMaxEntrySize`，因为宽表单列 Block 可能较大

**监控缓存命中率**：

```bash
curl -s http://localhost:8080/metrics | grep widb_cache
```

```
widb_cache_hits_total{cache="block"} 12345
widb_cache_misses_total{cache="block"} 678
widb_cache_hits_total{cache="index"} 8901
widb_cache_misses_total{cache="index"} 12
```

命中率 = hits / (hits + misses)，建议维持在 80% 以上。

### 3.2 索引加速

WiDB 提供三级索引加速查询，自动生效：

```
查询请求
    │
    ▼
PrimaryIndex ── 键范围过滤 ──► 缩小到键范围有交集的 Segment
    │
    ▼
BloomIndex ── 存在性过滤 ──► 排除一定不包含目标键的 Segment
    │
    ▼
SparseIndex ── 谓词过滤 ──► 根据列 Min/Max 跳过不满足条件的 Segment
    │
    ▼
实际 I/O 扫描
```

**最佳实践**：

- **点查**：始终使用主键等值条件（`WHERE id = ?`），可利用全部三级索引
- **范围查询**：使用主键范围条件（`WHERE id >= ? AND id <= ?`），可利用 PrimaryIndex + SparseIndex
- **非主键过滤**：SparseIndex 会根据列 Min/Max 自动跳过不满足条件的 Segment，无需额外配置

### 3.3 Compaction 调优

L0 Segment 过多会降低查询性能，Compaction 负责合并小 Segment：

| 参数 | 默认值 | 影响 |
|------|--------|------|
| `-scheduler.compact-interval` | 10s | Compaction 检查间隔 |

**调优建议**：

- **写入密集场景**：缩短到 5s，加快 L0 合并
- **读取密集场景**：保持 10s 或更长，减少 Compaction 对 I/O 的干扰

**监控 L0 Segment 数量**：

```bash
curl -s http://localhost:8080/metrics | grep widb_l0_segment
```

L0 Segment 数量持续增长（>20）说明 Compaction 跟不上写入速度，需要缩短间隔或增大 MemTable。

### 3.4 列裁剪与谓词下推

WiDB 的查询优化器自动执行以下优化：

1. **列裁剪**：只读取查询需要的列，减少 I/O
2. **谓词下推**：将 Filter 尽可能下推到 Scan 节点，减少数据处理量
3. **常量折叠**：编译期计算常量表达式

```sql
-- 好：只查需要的列，利用主键索引
SELECT name, temperature FROM sensor WHERE id = 1;

-- 差：SELECT * 且无主键条件，触发全表扫描
SELECT * FROM sensor;
```

## 4. 存储优化

### 4.1 编码选择

WiDB 自动为每列选择最优编码方式：

| 编码 | 适用场景 | 压缩效果 |
|------|----------|----------|
| Plain | 随机数据 | 无压缩 |
| Dictionary | 低基数列（枚举、状态码） | 高压缩比 |
| RLE | 连续重复值（排序后的列） | 极高压缩比 |
| Bitmap | BOOL / 低基数 | 极高压缩比 |

**最佳实践**：

- 数据按主键有序写入时，非主键列的 RLE 编码效果最佳
- 低基数字符串列（如状态、类型）会自动使用 Dictionary 编码
- 编码选择是自动的，无需手动配置

### 4.2 ZSTD 压缩

所有编码后的列 Block 使用 ZSTD 压缩进一步减小体积：

- **压缩级别**：`SpeedDefault`（平衡速度与压缩比）
- **压缩器池化**：Encoder/Decoder 通过 `sync.Pool` 复用，避免重复初始化
- **缓冲区池化**：压缩输出缓冲区通过 `sync.Pool` 复用，减少堆分配

> 当前压缩级别为 `SpeedDefault`，如需更高压缩比可修改源码中的 `zstd.WithEncoderLevel` 参数。可选值：`SpeedFastest`（最快）、`SpeedDefault`（默认）、`SpeedBetterCompression`（更好压缩）、`SpeedBestCompression`（最佳压缩，最慢）。

### 4.3 WAL 清理

WAL 文件持续增长会占用磁盘空间，Scheduler 定期清理：

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-scheduler.wal-clean-interval` | 30s | WAL 清理检查间隔 |
| `-scheduler.wal-clean-threshold` | 64MB | WAL 文件大小阈值 |

**调优建议**：

- **磁盘空间紧张**：减小阈值到 32MB，更频繁清理
- **写入密集场景**：增大阈值到 128MB，减少清理频率对 I/O 的影响

```bash
# 更频繁清理 WAL
./widb-server -scheduler.wal-clean-interval 15s -scheduler.wal-clean-threshold 33554432
```

## 5. 内存调优

### 5.1 内存占用估算

WiDB 的主要内存消耗：

| 组件 | 默认占用 | 说明 |
|------|----------|------|
| MemTable（活跃） | <= 64MB | 由 `-max-memtable` 控制 |
| MemTable（冻结） | <= 64MB × N | 等待刷盘的 immutable MemTable |
| BlockCache | <= 256MB | 由 `BlockCacheSize` 控制 |
| IndexCache | ~数 MB | 由 `IndexCacheSize` 控制 |
| Segment 元数据 | ~数 MB | 与 Segment 数量线性相关 |

**总内存估算**：约 `MaxMemTableSize × 3 + BlockCacheSize + 100MB`

### 5.2 BufferPool

WiDB 使用 `sync.Pool` 复用字节切片，减少 GC 压力：

- **默认缓冲区容量**：4096 字节
- **使用场景**：编码/解码过程中的临时缓冲区
- **生命周期**：`Get()` → 使用 → `Put()`，归还后不可继续使用

### 5.3 减少内存分配

WiDB 内部采用了多种减少内存分配的优化：

- **ZSTD 编码器/解码器池化**：避免每次压缩/解压都创建新对象
- **压缩输出缓冲区池化**：复用 `[]byte`，仅缓存不超过 1MB 的缓冲区
- **批量编码**：`encodeUint64Batch` / `encodeFloat64Batch` 一次编码多个值，减少内存分配次数
- **布隆过滤器零分配查询**：`MayContainString` 使用 `unsafe.Slice` 将 `string` 转为 `[]byte`，避免堆分配

## 6. 并发调优

### 6.1 连接管理

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `MaxConnections` | 0（不限制） | TCP 最大连接数 |

**调优建议**：

- **生产环境**：设置合理上限（如 1000），防止连接数耗尽文件描述符
- **高并发短查询**：增大上限，或使用连接池复用连接

```go
cfg := server.Config{
    MaxConnections: 1000,
}
```

### 6.2 并发安全策略

| 组件 | 并发策略 | 性能影响 |
|------|----------|----------|
| Engine | RWMutex | 写操作互斥，读操作并发 |
| MemTable | 内部同步（跳表） | 并发写入无锁竞争 |
| WAL | 内部序列化 | 顺序追加写，天然串行化 |
| Catalog | RWMutex | 读多写少，读锁不互斥 |
| PrimaryIndex | RWMutex | 读操作可并发 |
| BlockCache / IndexCache | 内部同步 | 高并发下缓存操作无锁竞争 |
| GroupCommitter | Mutex + Channel | 批量合并 sync，减少锁持有时间 |

### 6.3 读写分离建议

WiDB 的 RWMutex 设计天然支持读写并发：

- 写操作（INSERT）获取写锁，互斥
- 读操作（SELECT）获取读锁，可并发
- 写入不会阻塞读操作（除非写操作正在修改 MemTable 指针）

**最佳实践**：
- 读多写少场景：无需特殊处理，RWMutex 自动优化
- 写多读少场景：使用 GroupCommit 减少写锁持有时间

## 7. 监控与诊断

### 7.1 关键监控指标

| 指标 | 类型 | 用途 |
|------|------|------|
| `widb_query_duration_seconds` | Histogram | 查询延迟分布，识别慢查询 |
| `widb_write_duration_seconds` | Histogram | 写入延迟分布 |
| `widb_cache_hits_total` / `widb_cache_misses_total` | Counter | 缓存命中率，判断缓存配置是否合理 |
| `widb_memtable_size_bytes` | Gauge | MemTable 内存占用，判断是否需要增大 |
| `widb_l0_segment_count` | Gauge | L0 Segment 数量，判断 Compaction 是否跟上 |
| `widb_wal_size_bytes` | Gauge | WAL 文件大小，判断清理是否正常 |
| `widb_active_connections` | Gauge | 活跃连接数，判断是否需要限制 |

### 7.2 性能诊断流程

```
查询慢？
  │
  ├─ 检查缓存命中率
  │   └─ 命中率低 → 增大 BlockCacheSize / 检查查询模式
  │
  ├─ 检查 L0 Segment 数量
  │   └─ L0 过多 → 缩短 Compaction 间隔 / 增大 MemTable
  │
  ├─ 检查是否使用了索引
  │   └─ 全表扫描 → 添加主键条件 / 检查 WHERE 子句
  │
  └─ 检查 WAL 大小
      └─ WAL 过大 → 检查 WAL 清理配置

写入慢？
  │
  ├─ 检查 SyncMode
  │   └─ SyncEveryWrite → 考虑切换到 SyncGroupCommit
  │
  ├─ 检查 MemTable 大小
  │   └─ 频繁刷盘 → 增大 MaxMemTableSize
  │
  └─ 检查写入批量大小
      └─ 单行写入 → 改为批量写入
```

### 7.3 Prometheus 集成

```yaml
scrape_configs:
  - job_name: 'widb'
    static_configs:
      - targets: ['localhost:8080']
    metrics_path: '/metrics'
    scrape_interval: 15s
```

推荐 Grafana 面板关注：
- 查询/写入 P50/P99 延迟趋势
- 缓存命中率趋势
- L0 Segment 数量趋势
- WAL 文件大小趋势

## 8. 场景化调优建议

### 8.1 IoT 时序数据场景

特征：高吞吐写入、按时间范围查询、低基数标签列

```bash
./widb-server \
  -max-memtable 134217728 \          # 128MB，减少刷盘
  -scheduler.flush-interval 3s \      # 更快刷盘
  -scheduler.compact-interval 5s \    # 更快 Compaction
  -scheduler.wal-clean-threshold 134217728  # 128MB WAL 阈值
```

编程配置：
```go
cfg := storage.EngineConfig{
    MaxMemTableSize:        128 << 20,
    BlockCacheSize:         512 << 20,  // 512MB 缓存
    BlockCacheMaxEntrySize: 2 << 20,    // 2MB 单条目
    IndexCacheSize:         2000,
    SyncMode:               storage.SyncGroupCommit,  // 可容忍极小丢失
    SyncInterval:           time.Millisecond,
}
```

### 8.2 用户画像查询场景

特征：宽表（10,000+ 列）、点查为主、低延迟要求

```go
cfg := storage.EngineConfig{
    MaxMemTableSize:        64 << 20,   // 64MB 足够
    BlockCacheSize:         1024 << 20, // 1GB 大缓存
    BlockCacheMaxEntrySize: 4 << 20,    // 4MB 单条目（宽表列 Block 较大）
    IndexCacheSize:         5000,       // 更多索引缓存
    SyncMode:               storage.SyncEveryWrite, // 数据不能丢
}
```

### 8.3 日志分析场景

特征：高吞吐写入、范围扫描、对数据丢失可容忍

```go
cfg := storage.EngineConfig{
    MaxMemTableSize:        256 << 20,  // 256MB 大 MemTable
    BlockCacheSize:         256 << 20,  // 256MB 缓存
    IndexCacheSize:         1000,
    SyncMode:               storage.SyncGroupCommit,  // 高吞吐优先
    SyncInterval:           5 * time.Millisecond,     // 稍大间隔
}
```

## 9. 性能测试

WiDB 包含内置的性能测试，可用于验证调优效果：

```bash
# 运行存储引擎基准测试
go test -bench=Benchmark -benchmem ./pkg/storage/

# 运行集成测试中的 YCSB 基准
go test -v -run TestYCSB ./tests/integration/

# 运行查询引擎基准测试
go test -bench=Benchmark -benchmem ./pkg/server/
```

建议在调优前后分别运行基准测试，量化优化效果。

## 10. 调优参数速查表

| 参数 | 默认值 | 调优方向 | 影响 |
|------|--------|----------|------|
| `MaxMemTableSize` | 64MB | 增大 → 减少刷盘频率 | 写入吞吐 ↑，内存 ↑ |
| `BlockCacheSize` | 256MB | 增大 → 提高缓存命中率 | 查询延迟 ↓，内存 ↑ |
| `BlockCacheMaxEntrySize` | 1MB | 增大 → 缓存更大的 Block | 宽表查询 ↓，内存 ↑ |
| `IndexCacheSize` | 1000 | 增大 → 缓存更多索引 | 索引查找 ↓，内存 ↑ |
| `SyncMode` | SyncEveryWrite | GroupCommit → 高吞吐 | 写入吞吐 ↑，安全性 ↓ |
| `SyncInterval` | 1ms | 增大 → 合并更多 sync | 吞吐 ↑，延迟 ↑ |
| `FlushInterval` | 5s | 缩短 → 更快刷盘 | 内存 ↓，I/O ↑ |
| `CompactInterval` | 10s | 缩短 → 更快合并 | L0 数量 ↓，I/O ↑ |
| `WALCleanThreshold` | 64MB | 增大 → 减少清理频率 | 磁盘 ↑，I/O ↓ |
| `MaxConnections` | 0（不限） | 设置上限 → 防止资源耗尽 | 稳定性 ↑ |

## 11. 文档索引

| 文档 | 说明 |
|------|------|
| [getting-started.md](getting-started.md) | 快速入门指南 |
| [architecture.md](architecture.md) | 系统架构 |
| [storage.md](storage.md) | 存储引擎详解 |
| [query.md](query.md) | 查询引擎详解 |
| [index.md](index.md) | 索引模块详解 |
| [catalog.md](catalog.md) | 元数据管理详解 |
| [common.md](common.md) | 公共模块详解 |
| [api.md](api.md) | API 参考 |
