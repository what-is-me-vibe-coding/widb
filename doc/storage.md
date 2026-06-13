# 存储引擎详解

## 1. 概述

存储引擎是 WiDB 的核心模块（`pkg/storage/`），负责数据的持久化存储、读取与生命周期管理。基于 LSM-Tree 变体架构，写入先落 WAL，再进入内存 MemTable，后台异步刷盘生成不可变 Segment，并通过 Compaction 合并优化。

## 2. 核心组件

### 2.1 Engine

Engine 是存储引擎的顶层结构，协调所有子组件：

```go
type Engine struct {
    activeMem      *MemTable       // 当前活跃的写入 MemTable
    immutable      []*MemTable     // 已冻结等待刷盘的 MemTable
    wal            *WAL            // 预写日志
    flusher        *Flusher        // MemTable → Segment 刷写器
    compactor      *Compactor      // Segment 合并器
    segments       []*Segment      // 所有 Segment
    primaryIndex   *index.PrimaryIndex
    bloomIndex     *index.BloomIndex
    sparseIndex    *index.SparseIndex
    blockCache     *BlockCache     // 列 Block 缓存
    indexCache     *IndexCache     // 索引缓存
    scheduler      *Scheduler      // 后台任务调度器
    groupCommitter *GroupCommitter // 组提交器
}
```

### 2.2 WAL（预写日志）

WAL 保证写入数据的持久性，崩溃后可通过回放恢复。

- **写入模式**：顺序追加写（Append-Only）
- **记录类型**：Write（数据写入）、Checkpoint（刷盘标记）
- **同步模式**：
  - `SyncEveryWrite`：每次写入后 fsync（最安全，默认）
  - `SyncGroupCommit`：多个写入共享一次 fsync（高吞吐）
- **文件格式**：自定义二进制格式，含校验和
- **清理策略**：Scheduler 定期检查，超过阈值时清理 Checkpoint 之前的旧记录

### 2.3 MemTable

MemTable 是内存中的有序数据结构，支持并发写入。

- **实现**：基于跳表（Skip List），按主键排序
- **生命周期**：
  1. Active：接收写入
  2. Frozen：达到容量阈值后冻结，变为 immutable
  3. Flushed：刷盘为 Segment 后释放
- **容量控制**：`MaxMemTableSize` 配置，默认 64MB
- **快照读**：冻结后提供一致性快照，读写不阻塞

### 2.4 Segment

Segment 是磁盘上的不可变列式存储单元。

- **结构**：Header + Column Blocks + Footer
- **列式存储**：每列独立存储为一个 Block
- **编码**：Plain / Dictionary / RLE / Bitmap
- **压缩**：ZSTD（Block 级别）
- **索引**：Footer 包含列级 Min/Max 统计和布隆过滤器
- **层级**：L0（刷盘产生的小 Segment）→ L1（Compaction 合并的大 Segment）

### 2.5 Flusher

Flusher 负责将 MemTable 刷写为 Segment 文件。

- **流程**：
  1. 遍历 MemTable 的有序数据
  2. 按列编码（选择最优编码方式）
  3. ZSTD 压缩每个列 Block
  4. 写入 Segment 文件
  5. 构建索引信息写入 Footer

### 2.6 Compactor

Compactor 负责合并 L0 小 Segment 为 L1 大 Segment。

- **策略**：Tiered Compaction
- **流程**：
  1. 选取 L0 Segment 集合
  2. 多路归并读取（按主键排序）
  3. 去重（保留最新版本）
  4. 重新编码写入新 Segment
  5. 注册新 Segment 到索引
  6. 删除旧 Segment 文件

### 2.7 Scheduler

后台任务调度器，定时执行刷盘、Compaction 和 WAL 清理。

- **配置项**：
  - `FlushInterval`：刷盘检查间隔（默认 5s）
  - `CompactInterval`：Compaction 检查间隔（默认 10s）
  - `WALCleanInterval`：WAL 清理间隔（默认 30s）
  - `WALCleanThreshold`：WAL 文件大小阈值（默认 64MB）

### 2.8 GroupCommitter

批量合并 WAL sync 操作，将多次 fsync 摊销为一次。

- **工作原理**：
  1. 写入者通过 `Submit()` 提交 sync 请求
  2. 后台 goroutine 在有写入时立即触发 sync
  3. sync 执行期间到达的新写入合并到下一批
  4. 定时器兜底确保写入不会等待太久
- **适用场景**：高并发写入，可接受极小概率的数据丢失（最近 1ms 内）

## 3. 编码详解

### 3.1 Plain 编码

直接存储原始值，适用于数据分布随机、无规律的列。

### 3.2 Dictionary 编码

将唯一值构建字典，存储字典 ID 序列。适用于低基数列（如枚举、状态码）。

```
原始数据: ["apple", "banana", "apple", "cherry", "banana"]
字典:     {0: "apple", 1: "banana", 2: "cherry"}
编码结果: [0, 1, 0, 2, 1]
```

### 3.3 RLE 编码

将连续重复值编码为 (值, 重复次数) 对。适用于排序后的列。

```
原始数据: [1, 1, 1, 2, 2, 3, 3, 3, 3]
编码结果: [(1, 3), (2, 2), (3, 4)]
```

### 3.4 Bitmap 编码

用位图表示值的存在性，1 bit/值。适用于 BOOL 列和低基数列。

## 4. 缓存

### 4.1 BlockCache

缓存解压后的列 Block 数据，减少重复解压开销。

- **容量**：默认 256MB
- **淘汰策略**：LRU
- **最大单条目**：1MB（超过不缓存，防止冷数据污染）
- **键格式**：`SegmentID:ColumnName:BlockIndex`

### 4.2 IndexCache

缓存 Segment 级稀疏索引与布隆过滤器。

- **容量**：默认 1000 条目
- **淘汰策略**：LRU

## 5. 配置参考

### EngineConfig

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `DataDir` | string | - | 数据存储目录 |
| `MaxMemTableSize` | int64 | 4MB | MemTable 最大容量（字节） |
| `BlockCacheSize` | int64 | 256MB | BlockCache 容量（字节），<=0 不缓存 |
| `BlockCacheMaxEntrySize` | int64 | 1MB | 单条目最大大小（字节） |
| `IndexCacheSize` | int | 1000 | IndexCache 容量（条目数），<=0 不缓存 |
| `SyncMode` | SyncMode | SyncEveryWrite | WAL 同步模式 |
| `SyncInterval` | Duration | 1ms | GroupCommit 同步间隔 |
