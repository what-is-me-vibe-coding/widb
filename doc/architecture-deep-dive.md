# WiDB 架构深度解析

本文档面向希望深入理解 WiDB 内部实现、二次开发或性能调优的工程师，是 [architecture.md](architecture.md) 的代码级补充。架构总览参见前者；模块细节参见 [storage.md](storage.md) / [query.md](query.md) / [index.md](index.md) / [server.md](server.md) / [pgwire.md](pgwire.md)。

## 1. 阅读指引

- 第 2-4 节覆盖三条核心数据流（写入 / 点查 / 范围扫描），含端到端函数调用链
- 第 5-6 节覆盖后台任务（Flush / Compaction）与崩溃恢复
- 第 7-8 节覆盖内存模型与磁盘格式（直接读源码时用于核对 magic number / 字段偏移）
- 第 9-10 节覆盖常见故障与性能调优切入点

所有函数与字段引用基于 `main` 分支；引用格式为 `pkg/storage/iterator.go#setCurrentFromHeapTop`，可在 IDE 中一键跳转。

## 2. 端到端写入路径

### 2.1 时序概览

```
Client                 Server                 Engine              MemTable          WAL                GroupCommitter
  │  POST /write        │                       │                    │                │                    │
  ├────────────────────▶│                       │                    │                │                    │
  │                     │ handleWrite()          │                    │                │                    │
  │                     │   ├─ Catalog.GetTable │                    │                │                    │
  │                     │   ├─ convertWriteRow  │                    │                │                    │
  │                     │   └─ Engine.WriteBatch│                    │                │                    │
  │                     │                       │ nextVersion.Add    │                │                    │
  │                     │                       │ (atomic, no lock)  │                │                    │
  │                     │                       │ serializeWrite     │                │                    │
  │                     │                       │ (CPU, no lock)     │                │                    │
  │                     │                       │ AppendBatch        │                │                    │
  │                     │                       ├───────────────────▶│                │                    │
  │                     │                       │                    │ Serialize+CRC  │                    │
  │                     │                       │                    │ BufferedWrite  │                    │
  │                     │                       │                    │ (mutex)        │                    │
  │                     │                       │ submitWALSync      │                │                    │
  │                     │                       │ (RLock)            │                │                    │
  │                     │                       ├─ (GroupCommit) ────────────────────────────────────▶│ Submit
  │                     │                       │  return syncCh     │                │                    │
  │                     │                       │ e.mu.Lock()        │                │                    │
  │                     │                       │ activeMem.Put × N  │                │                    │
  │                     │                       │ (ShouldFlush?) ───▶│ Freeze         │                    │
  │                     │                       │ e.mu.Unlock()      │                │                    │
  │                     │                       │ <-syncCh (block)   │                │                    │
  │                     │                       │                    │                │ doSync (bg)        │
  │                     │                       │                    │                │ fsync ─────────────▶│
  │  200 OK             │                       │                    │                │ notify all waiters │
  │◀────────────────────│                       │                    │                │                    │
```

### 2.2 函数调用链（关键路径）

```
server/handlers_dml.go#handleWrite
  └─ server/converter.go#convertWriteRow            (JSON → common.Value)
      └─ storage.Engine.WriteBatch
          ├─ e.nextVersion.Add(N)                   (atomic, 无锁)
          ├─ storage/wal_record.go#serializeBatchWriteRecord   (二进制序列化)
          ├─ e.wal.AppendBatch                      (WAL 内部 mu 串行化)
          ├─ e.submitWALSync
          │   ├─ (GroupCommit) gc.Submit → 返回 syncCh
          │   └─ (SyncEvery) e.wal.Sync → fsync
          ├─ e.mu.Lock
          ├─ e.activeMem.Put × N                    (跳表插入)
          ├─ e.rotateMemTable (若 ShouldFlush)      (冻结 + 新建 activeMem)
          └─ e.mu.Unlock
          └─ <-syncCh                                (GroupCommit 模式阻塞)
```

### 2.3 关键设计点

| 设计 | 位置 | 收益 |
|------|------|------|
| 版本号原子分配 | `engine.go:60` `nextVersion atomic.Uint64` | 写路径无锁，避免 RWMutex 竞争 |
| WAL I/O 在引擎锁外 | `engine_ops.go:114-143` | fsync 期间不阻塞并发读 |
| GroupCommit | `group_committer.go:60` Submit + `run` 循环 | N 次 fsync 摊销为 1 次，吞吐 ≈ 2× |
| 批量写入共享 sync | `engine_ops.go:114-122` | 单次网络往返 + 一次 fsync |
| MemTable 旋转 | `engine_ops.go:51-58` | 写入不阻塞刷盘决策 |
| 锁粒度 | 引擎 `RWMutex` + WAL `Mutex` + MemTable 跳表内部同步 | 细粒度，最小化临界区 |

### 2.4 错误处理矩阵

| 失败点 | 行为 | 后果 |
|--------|------|------|
| `serializeWriteRecord` 失败 | 立即返回 | 客户端收到 500；MemTable 无副作用 |
| `wal.AppendBatch` 失败 | 立即返回 | 客户端收到 500；下一次 Write 仍会重试 |
| `wal.Sync` 失败 | 立即返回 | 客户端收到 500；WAL 文件可能未 fsync |
| `gc.Submit` 成功但 sync 失败 | `<-syncCh` 阻塞直至重试成功 | 默认 GroupCommitter 会持续重试，最多丢弃最旧的 4096 个请求 |
| `activeMem.Put` 失败 | `e.mu.Unlock` + 立即返回 | 客户端收到 500；WAL 已写入但 MemTable 缺记录（异常路径，由上层保证） |

## 3. 端到端点查路径

### 3.1 时序概览

```
Client            Server                Engine             MemTable           PrimaryIndex       BloomIndex         Segment
  │  GET /query    │                      │                    │                    │                   │                │
  ├────────────────▶│                      │                    │                    │                   │                │
  │                 │ handleQuery          │                    │                    │                   │                │
  │                 │  └─ parser+analyzer  │                    │                    │                   │                │
  │                 │  └─ executor         │                    │                    │                   │                │
  │                 │      └─ Engine.Get   │                    │                    │                   │                │
  │                 │          ├─ e.mu.RLock                    │                    │                   │                │
  │                 │          ├─ activeMem.Get (skiplist)      │                    │                   │                │
  │                 │          │  hit? → return (skip 3-5)      │                    │                   │                │
  │                 │          ├─ immutable[i].Get (新→旧)       │                    │                   │                │
  │                 │          ├─ getFromSegments               │                    │                   │                │
  │                 │          │  └─ primaryIndex.Lookup ───────▶│                   │                │
  │                 │          │  └─ (sort segIDs if > 1)        │                   │                │
  │                 │          │  └─ for each segID (新→旧):    │                   │                │
  │                 │          │     ├─ bloomIndex.MayContain ────────────────────▶│                │
  │                 │          │     │  miss → continue         │                   │                │
  │                 │          │     ├─ seg.FindRowByKey (二分)  │                   │                │
  │                 │          │     └─ fetchColumnsFromSegment │                   │                │
  │                 │          │        └─ blockCache.get        │                   │                │
  │                 │          │           miss → seg.GetColumnValue (decode+cache)
  │                 │          └─ e.mu.RUnlock                   │                   │                │
  │  200 OK         │                      │                    │                    │                   │                │
  │◀────────────────│                      │                    │                    │                   │                │
```

### 3.2 函数调用链

```
pkg/server/handlers_dml.go#handleQuery
  └─ pkg/query/executor.go#execute
      └─ pkg/storage/engine.go#Engine.Get
          ├─ e.mu.RLock
          ├─ e.activeMem.Get                          (skiplist O(log N))
          ├─ for i := len(immutable)-1; i >= 0; i--  (新→旧)
          │   └─ e.immutable[i].Get
          └─ e.getFromSegments
              ├─ e.primaryIndex.Lookup                (返回 segID 列表)
              ├─ if len > 1: sort.Slice
              └─ for i := len-1; i >= 0; i--
                  ├─ e.bloomIndex.MayContainString    (O(K) 哈希检查)
                  ├─ e.findSegmentByID                (map O(1))
                  ├─ seg.FindRowByKey                 (binary search O(log N))
                  └─ e.fetchColumnsFromSegment
                      └─ for col in columns
                          ├─ blockCache.get           (LRU O(1))
                          └─ seg.GetColumnValue        (decode + blockCache.put)
```

### 3.3 关键设计点

- **多级索引剪枝**：PrimaryIndex（主键 → segID 列表）→ BloomFilter（segID + key → bool）→ Segment 二分（segID + key → rowIdx）→ BlockCache（segID + colIdx → decodedColumn）
- **优先级排序**：`getFromSegments` 从最新 segment 开始读，与 MergeIterator 的「高 index = 高优先级」一致
- **缓存分层**：BlockCache 缓存解压后的列数据（避免重复解码），IndexCache 缓存列统计（避免重复计算 Min/Max）
- **延迟物化**：`iterator.go#segmentIterator.buildRowMap` 在 `Entry()` 被调用时才构造 `map[string]Value`，扫描时可先比 Key 再物化

### 3.4 性能特征

| 场景 | 关键路径 | 典型延迟 | 瓶颈 |
|------|----------|----------|------|
| 命中 MemTable | `activeMem.Get` (skiplist) | < 10µs | 跳表节点分配（GC） |
| 命中 BlockCache | `blockCache.get` + `extractValue` | < 100µs | map 分配（columns map） |
| 命中 Segment（未缓存） | `bloomIndex` + `seg.FindRowByKey` + 列解码 | < 1ms | ZSTD 解压 + Row 物化 |
| 范围扫描（选择性高） | `ScanRangeWithPruning` + 列裁剪 | 取决于命中行 | 多段 MergeIterator + 列解码 |
| 范围扫描（全表） | `ScanRange` + 多段全量读取 | 取决于总行数 | 列解码 + Row 物化 |

## 4. 端到端范围扫描路径

### 4.1 时序概览

```
SQL: SELECT * FROM t WHERE id BETWEEN 100 AND 200
   │
   ▼
Parser → AST
   │
   ▼
Analyzer (resolve column, type check)
   │
   ▼
Optimizer
   ├─ 列裁剪: 移除未引用的列
   ├─ 谓词下推: 范围条件从 Filter 下推到 Scan
   └─ 常量折叠: 折叠字面量表达式
   │
   ▼
Executor.execute → ScanNode
   │
   ├─ Engine.ScanRangeWithPruning(start, end, predicates)
   │   ├─ buildScanIteratorsWithPruning
   │   │   ├─ 对每个 segment:
   │   │   │   ├─ key range 裁剪: seg.MinKey > end || seg.MaxKey < start → skip
   │   │   │   └─ 谓词裁剪: sparseIndex.CanSkip(segID, colID, op, value) → skip
   │   │   └─ 对每个 immutable memtable: 构造 memTableIterator
   │   │   └─ 对 active memtable: 构造 memTableIterator
   │   ├─ NewMergeIterator(iters...)  (按 seg index 升序，高 index = 新 = 高优先级)
   │   └─ loop:
   │       ├─ mi.Next()  (heap pop, 推进堆顶迭代器)
   │       └─ mi.Entry() → skip tombstone → append to results
   │
   ▼
FilterNode: 向量化过滤 (chunk-wise, 1024 rows/batch)
   │
   ▼
ProjectNode: 列投影
   │
   ▼
LimitNode: 截断
   │
   ▼
Chunk → formatter → response
```

### 4.2 关键优化

- **列裁剪** (`optimizer_column_pruning.go`)：只读 SQL 引用的列，Segment 解码时只解这些列的 Block
- **谓词下推** (`optimizer_predicate_pushdown.go`)：范围条件 / 等值条件从 Filter 下推到 Scan
- **段裁剪** (`buildScanIteratorsWithPruning`)：用列统计 Min/Max 在解码前跳过不满足的段
- **延迟物化** (`iterator.go#buildRowMap`)：Key 比较阶段不分配 columns map，Entry() 才分配
- **结果集预分配** (`sumIterCounts` + `capScanPrealloc`)：用 MemTable 精确计数 + 上限保护避免浪费
- **MergeIterator 堆排序** (`iterator.go#mergeHeap`)：O(N log K) 归并 K 个有序迭代器

## 5. 后台任务

### 5.1 Scheduler 调度周期

`pkg/storage/scheduler.go` 用单 goroutine + ticker 实现，默认三个任务（`config` 配置）：

| 任务 | 默认间隔 | 触发条件 | 关键函数 |
|------|----------|----------|----------|
| Flush | 5s | `len(immutable) > 0` | `Engine.tryFlush` → `Flusher.Flush` |
| Compact | 10s | `l0SegmentCount >= defaultL0CompactionThreshold (4)` | `Engine.tryCompact` → `Compactor.Compact` |
| WAL Clean | 30s | `wal.size() >= walCleanThreshold` | `Engine.cleanWAL` → `wal.Rotate` |

### 5.2 Flush 流程

```
tryFlush (engine.go)
  ├─ e.mu.Lock
  ├─ 取出 immutable[0]（队首 = 最早冻结的）
  ├─ e.mu.Unlock
  ├─ flusher.Flush(mem, cols)
  │   ├─ mem.All()                            (O(N) 收集所有行)
  │   ├─ buildSegment
  │   │   ├─ keys 数组构造
  │   │   ├─ 对每列: 选择编码 (Plain / Dict / RLE / Bitmap)
  │   │   ├─ 对每列: ZSTD 压缩
  │   │   ├─ Footer: ColumnMeta + Min/Max + NullCount + BloomFilter
  │   │   └─ 构造 Segment 对象
  │   └─ writeSegmentFile                     (顺序写文件)
  ├─ e.mu.Lock
  ├─ addSegment(seg, level=0)                 (注册到索引)
  ├─ e.immutable = e.immutable[1:]            (移除已刷盘的 memtable)
  ├─ e.mu.Unlock
  └─ wal.AppendCommit + wal.AppendCheckpoint  (标记 Checkpoint)
```

### 5.3 Compaction 流程

```
tryCompact
  ├─ 选择 L0 中待合并的 segments（默认 4 个）
  ├─ compactor.Compact(segs, cols)
  │   ├─ mergeSegments (K-way merge via heap)
  │   │   ├─ 为每个 seg: decode 所有列 → segReader
  │   │   ├─ 堆按 key 升序排序，高 segIdx 优先（去重时新版胜出）
  │   │   └─ 流式归并：每次从堆顶 pop → 取当前行 → 输出
  │   ├─ buildSegment (同 Flush 路径)
  │   └─ writeSegmentFile
  ├─ e.mu.Lock
  ├─ addSegment(seg, level=1)
  ├─ for oldSeg in segs:
  │   ├─ unregisterSegmentIndexes (从所有索引移除)
  │   └─ os.Remove(oldSeg.FilePath)             (删除旧 segment 文件)
  └─ e.mu.Unlock
```

**关键优化**：流式归并（`compaction.go#segReader`），不再预物化所有行到 `[]memRow`，峰值内存从 O(总行数 × 列数) 降至 O(段数 + 输出行数 × 列数)。

### 5.4 写入失败处理

- Flush 失败：保留 immutable，下次重试；不更新 lastFlushedVersion
- Compact 失败：保留旧 segments，不删除；下次重试
- WAL Clean 失败：保留旧 WAL，不旋转；下次重试

后台任务失败不影响前台写入路径（独立 goroutine + 各自 mu）。

## 6. 崩溃恢复

### 6.1 启动流程

```
NewEngine (engine.go)
  ├─ loadSegments (engine.go)
  │   ├─ glob "{dataDir}/*.seg"
  │   └─ for each file: deserialize → addSegment(level=0)
  │       （启动时假设所有 segment 都在 L0，第一次 Compaction 后重分布）
  ├─ initWAL
  │   ├─ OpenWAL (wal.go)
  │   │   ├─ os.OpenFile
  │   │   ├─ replayWAL: 顺序读 records，遇到 CRC 失败/截断 → 截断到上一个 valid offset
  │   │   └─ f.Truncate(validOffset)              (丢弃半写记录)
  │   └─ replayWALRecords
  │       └─ for each record: deserialize + activeMem.Put (按 WAL 顺序回放，跳过已 Checkpoint 的版本)
  └─ 启动 GroupCommitter (若配置)
```

### 6.2 关键不变式

| 不变式 | 保证机制 |
|--------|----------|
| 已 Checkpoint 的 version ≤ 已刷盘 segment 的最大 version | Flusher 刷盘成功后写 Checkpoint（含 LastFlushedVersion） |
| 未 Checkpoint 的数据可由 WAL 恢复 | WAL 是顺序追加的，回放按 record 顺序 |
| 半写记录不会污染启动 | `replayWAL` 验证 CRC + length，失败时截断到上一个 valid offset |
| Segment 文件损坏不会传播 | `loadSegments` 解析失败时整个文件跳过（记录 warning log） |

### 6.3 恢复后状态

- MemTable：包含 WAL 中所有未 Checkpoint 的写入
- Segments：包含所有已 Checkpoint 的历史数据
- 索引：基于 segments + memtable 重建

启动后即可对外服务，Get 路径自动合并多源（memtable + segments）。

## 7. 内存模型

### 7.1 分配热路径

| 位置 | 分配对象 | 频率 | 优化 |
|------|----------|------|------|
| `engine_ops.go#Write` | `serializeWriteRecord` 内部 `[]byte` | 每次写 | 预计算 size 一次性 make，避免 append 扩容 |
| `engine_ops.go#Get` | `fetchColumnsFromSegment` 返回的 `map[string]Value` | 每次点查 | 预分配 map 容量 = 列数；考虑改用切片 (column index → Value) 避免 map hash |
| `iterator.go#buildRowMap` | 每行的 `map[string]Value` | 每行一次 | 不可避免（API 契约），但延迟到 Entry() 才分配 |
| `compaction.go#segReader.currentRow` | 每行的 `[]common.Value` | 每行一次 | 流式归并，无法预分配 |
| `query/executor.go` | Chunk（1024 行批次） | 每个算子 | 算子间传递 Chunk 引用，避免大块拷贝 |
| `common/pool.go` | 通用 buffer pool | 频繁 | 用 `sync.Pool` 复用 byte slice |

### 7.2 GC 压力点

- 每秒 10 万次写入 → 10 万次 `map[string]Value` 分配（fetchColumnsFromSegment）
- 范围扫描每行一次 `map[string]Value` 分配
- 缓解：使用 `sync.Pool` 复用 map（但需注意 map 不能直接放回 pool，需 clear 后复用）

### 7.3 常驻内存

| 结构 | 大小估算（默认配置） | 备注 |
|------|---------------------|------|
| 活跃 MemTable | 64MB（maxMemTableSize） | 跳表节点 ~32B/key |
| Immutable MemTable × N | N × 64MB | 刷盘前常驻 |
| BlockCache | 256MB | LRU，单条 ≤ 1MB |
| IndexCache | 1000 条目 | 列统计 ~KB/条 |
| 索引 | PrimaryIndex + BloomIndex + SparseIndex | 与段数 × 列数线性相关 |

## 8. 磁盘格式

### 8.1 WAL 文件格式

```
┌──────────────────────────────────────────────────────────────┐
│ Record 1                                                      │
│  ┌────────────┬────────┬──────────┬──────────┬─────────────┐ │
│  │Length (4B) │Type(1B)│Payload(N)│ CRC32(4B)│              │ │
│  └────────────┴────────┴──────────┴──────────┴─────────────┘ │
│ Record 2                                                       │
│  ...                                                            │
│ (截断 / 损坏尾部被 OpenWAL 自动丢弃)                            │
└──────────────────────────────────────────────────────────────┘
```

字段定义（`pkg/storage/wal.go`）：
- Magic：无（隐式，依赖文件路径约定）
- Length：uint32 LE，记录 Type+Payload+CRC 的总字节数（不含 Length 自身）
- Type：1=Write, 2=Commit, 3=Checkpoint, 4=BatchWrite
- CRC32C（Castagnoli）：覆盖 Type + Payload（不含 Length）

写入路径（`wal.go#AppendBatch`）：
1. `mu.Lock` 串行化所有 append
2. `binary.LittleEndian.PutUint32` 写 Length
3. 写 Type + Payload
4. `crc32.Castagnoli` 算 CRC
5. 写 CRC
6. `mu.Unlock`

读取路径（`wal.go#replayWAL`）：
1. 顺序读 4B → Length
2. 读 Length 字节 → Type+Payload+CRC
3. 校验 CRC：失败/截断 → 截断文件到此位置，停止回放
4. 解析 Type，分发到 Write / Commit / Checkpoint

### 8.2 Segment 文件格式

```
┌─────────────────────────────────────────────────────────────┐
│ Header (固定 6B)                                             │
│  Magic (4B, 0x57494442 "WIDB") | Version (2B)                │
├─────────────────────────────────────────────────────────────┤
│ Column Block 1                                                │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ colID (4B) | Encoding (1B) | Compressed (1B) |         │   │
│  │ Type (1B) | RowCount (4B) |                            │   │
│  │ NullsLen (4B) | Nulls |                                │   │
│  │ DataLen (4B) | Data |                                  │   │
│  │ OffsetsLen (4B) | Offsets (4B each) |                  │   │
│  │ DictLen (4B) | DictEntries (len+data)                  │   │
│  └──────────────────────────────────────────────────────┘   │
│ Column Block 2                                                │
│  ...                                                           │
│ Column Block N                                                │
├─────────────────────────────────────────────────────────────┤
│ Footer Length (4B)                                            │
│ Footer Bytes (变长)                                           │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ ColumnStatCount (4B)                                   │   │
│  │ for each: colID (4B) + MinLen+Min + MaxLen+Max +       │   │
│  │          NullCount (4B)                                │   │
│  │ BloomFilterLen (4B) + BloomFilter                      │   │
│  │ RawKeysLen (4B) + RawKeys (顺序主键拼接)                │   │
│  │ IndexOffset (8B)                                       │   │
│  └──────────────────────────────────────────────────────┘   │
├─────────────────────────────────────────────────────────────┤
│ Footer Offset (8B) — 从文件头到 Footer 起始位置的字节偏移     │
└─────────────────────────────────────────────────────────────┘
```

字段定义（`pkg/storage/segment_codec.go` / `segment_serialize.go`）：
- Magic：固定 0x57494442（"WIDB"），位于文件头 4B，用于快速校验文件完整性
- 编码类型：0=Plain, 1=Dictionary, 2=RLE, 3=Bitmap
- Footer 起始位置由文件末尾的 FooterOffset（8B）定位

读取路径（`pkg/storage/segment_serialize.go#DeserializeSegment`）：
1. 读前 4B Magic，校验为 0x57494442
2. 读 Version（2B）
3. 按 FooterOffset 跳到 Footer，读 FooterLength（4B）+ FooterBytes，反序列化 Footer
4. 从 Footer 解析 ColumnStats / BloomFilter / RawKeys / IndexOffset
5. 顺序读各 Column Block（`DeserializeColumnBlock`），按 colID 排序
6. 懒加载：列数据按需解压，缓存到 BlockCache

### 8.3 编码格式

| 编码 | 适用 | 格式 | 典型压缩率 |
|------|------|------|------------|
| Plain | 高基数、随机数据 | 8B 固定长度/值（FLOAT64/INT64）或变长（STRING） | 1.0×（无压缩） |
| Dictionary | 低基数（≤256 不同值） | uint32 索引数组 + 值字典 | 5-100× |
| RLE | 连续重复 | (值, 重复次数) 对 | 10-1000× |
| Bitmap | BOOL / 二值 | 位数组 | 8×（vs 单字节存储） |

选择策略（`flusher.go#buildEncodedColumn`）：
1. 计算每个编码的估算大小
2. 选择最小者
3. 若 Plain 最优，强制使用 Plain（避免小数据集字典开销）

所有编码后数据再经过 ZSTD 压缩（Block 级别），进一步减小体积 2-5×。

## 9. 常见故障模式

| 症状 | 根因 | 定位 | 修复 |
|------|------|------|------|
| 写入 hang | GroupCommitter 卡死 | `runtime.Stack` → `group_committer.run` 阻塞 | 检查 `wal.Sync` 是否 hang（磁盘满 / IO 错误） |
| 读取返回旧数据 | MemTable 轮转后旧 immutable 未刷盘但 activeMem 已更新 | 检查 `e.immutable` 切片 | 正常：Get 会查所有 immutable |
| 范围扫描丢数据 | Segment 时间戳错乱（version 倒序） | `e.segments` 列表 vs `e.segmentLevels` | 触发一次强制 Flush+Compact |
| Compaction 后查询变慢 | 新 L1 Segment 统计信息未更新 | `sparseIndex.LoadFromSegment` | 强制重新 LoadFromSegment |
| WAL 文件无限增长 | 刷盘失败 → Checkpoint 未写入 → WAL 不被清理 | 检查 Flusher 日志 | 修复刷盘失败原因 |
| OOM | 大量 Immutable MemTable 未刷盘 | 检查 `len(e.immutable)` | 增大 Flush 频率或减小 maxMemtable |

## 10. 性能调优切入点

### 10.1 写入优化

- **GroupCommit** (`SyncGroupCommit`)：吞吐翻倍，崩溃可能丢最近 1ms 数据
- **批量写入** (`WriteBatch`)：N 行共享 1 次 fsync
- **MemTable 大小** (`-max-memtable`)：增大可减少刷盘频率，但内存占用增加

### 10.2 读取优化

- **BlockCache 大小** (`-block-cache-size`)：256MB 是经验值，热数据集可增大到 1-2GB
- **IndexCache 大小** (`-index-cache-size`)：1000 条目，列多时可增大
- **列裁剪 / 谓词下推**：在 SQL 中只 SELECT 必要列，WHERE 条件尽量用主键或索引列

### 10.3 诊断工具

- Prometheus 指标：`/metrics` 端点暴露 `widb_*` 指标
  - `widb_query_duration_seconds` 分位数：识别慢查询
  - `widb_write_duration_seconds` 分位数：识别慢写入
  - `widb_memtable_size_bytes`：监控 MemTable 占用
  - `widb_segment_count` / `widb_l0_segment_count`：监控 Compaction 健康度
- `pprof`：通过 `_ "net/http/pprof"` 暴露 goroutine / heap profile
- Benchmark：`go test -bench=. -benchmem ./pkg/storage/`、`./pkg/query/`

## 11. 扩展点

新增功能时优先复用现有接口：

| 需求 | 扩展点 |
|------|--------|
| 新存储引擎 | 实现 `TableEngine` 接口（`pkg/server/table_engine.go`），在 `routingAdapter` 注册 |
| 新编码 | 实现 `Encoder` 接口（`pkg/storage/encoding.go`），在 `flusher.buildEncodedColumn` 中加入选择 |
| 新协议 | 在 `pkg/server/` 新增协议包，复用 `routingAdapter` + `Execute` 路径 |
| 新聚合函数 | 在 `pkg/query/executor_aggregate.go` 添加 case 分支 |
| 新索引 | 在 `pkg/index/` 新增索引类型，在 `Engine.registerSegmentIndexes` 注册 |
| 新 DML | 在 `pkg/server/handlers_dml.go` 添加 handler，调用对应的 Engine 方法 |

## 12. 进一步阅读

| 主题 | 文档 |
|------|------|
| 存储引擎模块 | [storage.md](storage.md) |
| 查询引擎模块 | [query.md](query.md) |
| 索引模块 | [index.md](index.md) |
| 元数据模块 | [catalog.md](catalog.md) |
| 公共类型 | [common.md](common.md) |
| 服务层 | [server.md](server.md) |
| PG wire 协议 | [pgwire.md](pgwire.md) |
| SQL 语法 | [sql-reference.md](sql-reference.md) |
| API 参考 | [api.md](api.md) |
| 性能调优 | [performance.md](performance.md) |
| 运维手册 | [operations.md](operations.md) |
| 故障排查 | [troubleshooting.md](troubleshooting.md) |
| 入门 | [getting-started.md](getting-started.md) |
| 教程 | [tutorial.md](tutorial.md) |
| 食谱 | [cookbook.md](cookbook.md) |
| 开发指南 | [development.md](development.md) |
