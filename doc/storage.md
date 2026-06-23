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

## 5. 延迟物化优化 (Lazy Materialization)

在扫描迭代过程中，并非所有场景都需要完整的行数据。例如归并排序的堆排序和去重仅需 key，此时构建完整的 `map[string]Value` 会产生不必要的内存分配和 CPU 开销。延迟物化优化将行数据的构建推迟到真正需要的时刻。

### 5.1 ScanIterator 接口变更

```go
type ScanIterator interface {
    Next() bool
    Key() string    // 仅返回 key，不触发列数据物化
    Entry() Entry   // 返回完整行数据（含列值 map），触发物化
    Error() error
}
```

- `Key()`：仅返回 key，不触发列数据物化，开销极低
- `Entry()`：返回完整行数据（含 `map[string]Value`），触发物化

调用方应优先使用 `Key()` 获取 key，仅在需要完整行数据时才调用 `Entry()`，以避免不必要的 map 分配。

### 5.2 segmentIterator 延迟构建

`segmentIterator.Next()` 现在仅记录 key 和行索引，不再构建 `map[string]Value`：

```go
func (it *segmentIterator) Next() bool {
    // 仅记录 key 和 rowIndex，不构建 map
    it.key = ...
    it.rowIndex = ...
}
```

行数据通过 `buildRowMap()` 按需构建：

```go
func (it *segmentIterator) Entry() Entry {
    if it.rowData == nil {
        it.rowData = it.buildRowMap()  // 延迟物化
    }
    return Entry{Key: it.key, Value: it.rowData}
}
```

### 5.3 MergeIterator 优化

`MergeIterator` 在堆排序和去重时使用 `Key()` 替代 `Entry().Key`，避免对被跳过的行触发物化：

```go
// 优化前：每次堆操作都会触发 Entry() 物化
heap.Push(&h, &item{key: it.Entry().Key, ...})

// 优化后：仅获取 key，不物化
heap.Push(&h, &item{key: it.Key(), ...})
```

### 5.4 性能提升

| 指标 | 改善幅度 |
|------|----------|
| EngineScanRange 延迟 | 降低 16.3% |
| 内存分配次数 | 减少 8% |

## 6. 原子化版本号分配 (Atomic Version Allocation)

### 6.1 变更说明

`Engine.nextVersion` 从 `uint64` 改为 `atomic.Uint64`：

```go
// 优化前
type Engine struct {
    nextVersion uint64
    mu          sync.Mutex
}

func (e *Engine) allocVersion() uint64 {
    e.mu.Lock()
    v := e.nextVersion
    e.nextVersion++
    e.mu.Unlock()
    return v
}

// 优化后
type Engine struct {
    nextVersion atomic.Uint64
}

func (e *Engine) allocVersion() uint64 {
    return e.nextVersion.Add(1) - 1
}
```

### 6.2 效果

- `Write` / `WriteBatch` 不再需要为版本号分配获取互斥锁
- 写路径减少一次 `Lock` / `Unlock` 操作，降低并发写入延迟

## 7. skipNode 内联 forward + Slab 分配器 (Inline forward + Slab)

### 7.1 背景与问题

旧版 `skipNode` 由「节点 + 独立堆分配 forward 切片」两个对象组成，配合
`sync.Pool` 复用：

```go
type skipNode struct {
    key     string
    value   Row
    forward []*skipNode  // 24B slice header + 128B heap-allocated slice
}
```

虽然 pool 缓解了 GC 压力，但有两个固有缺陷：

1. **Pool 命中低**：节点被 put 后即加入 `skipList`，永不被归还（`delete`
   时才会归还），因此稳态写入时 pool 命中率近 0，每次 put 都触发
   `pool.New()`，产生 2 次堆分配（`*skipNode` + `[]*skipNode`）。
2. **扫描缓存命中率低**：节点与 forward 切片在堆上分离，扫描阶段需要 2 次
   指针追踪才能跳到下一节点。

### 7.2 实现方式

新方案将 `forward` 内联到节点中，并引入 slab 批量分配器：

```go
const skipNodeSlabSize = 256

type skipNodeSlab struct {
    nodes [skipNodeSlabSize]skipNode
}

type skipNode struct {
    key     string
    value   Row
    forward [maxLevel]*skipNode  // 16*8=128B 内联
}

type skipList struct {
    head        *skipNode
    level       int
    size        int
    prev        []*skipNode
    nodeSlabs   []*skipNodeSlab
    nextNodeIdx int
}

func (sl *skipList) allocNode() *skipNode {
    if sl.nextNodeIdx >= skipNodeSlabSize {
        sl.nodeSlabs = append(sl.nodeSlabs, &skipNodeSlab{})
        sl.nextNodeIdx = 0
    }
    lastSlab := sl.nodeSlabs[len(sl.nodeSlabs)-1]
    node := &lastSlab.nodes[sl.nextNodeIdx]
    sl.nextNodeIdx++
    return node
}
```

### 7.3 效果

- `MemTable.Put` 的稳态分配数从 2 降至 0（slab 节点本身通过 bump-alloc 获取，无堆分配）
- `Engine.WriteBatch` 分配数从 393 降至 193（减少 51%）
- 扫描路径节点与 forward 指针位于同一对象，缓存命中率提升
- slab 整体由 `skipList` 持有引用，MemTable 冻结后随 slab 整体 GC，回收粒度从
  「单节点」变为「256 节点一块」，GC 标记成本下降

代价：低层节点（多数 level ≤ 3）的 `forward` 槽位部分浪费，但总体内存与旧方案持平
（旧方案 64B 节点 + 128B 独立切片 = 192B；新方案 192B 单对象）。

## 8. 段裁剪优化 (Segment Pruning)

### 8.1 概述

新增 `ScanRangeWithPruning` 方法，利用稀疏索引的列级 Min/Max 统计信息跳过不可能包含匹配数据的 Segment，减少 I/O、CPU 和内存开销。

### 8.2 ColumnPredicate 类型

```go
type ColumnPredicate struct {
    Column string      // 列名
    Op     Operator    // 操作符：=, !=, <, <=, >, >=
    Value  Value       // 比较值
}
```

### 8.3 谓词提取

```go
// 从查询谓词中提取列级条件
func extractColumnPredicates(pred Predicate) []ColumnPredicate

// 将二元表达式转换为列谓词
func binaryExprToColumnPredicate(expr *BinaryExpr) (*ColumnPredicate, bool)
```

`extractColumnPredicates` 递归遍历查询谓词，将可识别的列条件提取为 `ColumnPredicate` 列表；`binaryExprToColumnPredicate` 将 `BinaryExpr`（如 `age > 30`）转换为 `ColumnPredicate`。

### 8.4 裁剪逻辑

`ScanRangeWithPruning` 对每个 Segment 执行：

1. 获取该 Segment 的列级 Min/Max 统计
2. 对每个 `ColumnPredicate`，检查是否与 Min/Max 范围相交
3. 若任一谓词与范围不相交，则跳过该 Segment

### 8.5 优化效果

假设查询涉及 N 个 Segment，其中 M 个匹配：

| 维度 | 优化效果 |
|------|----------|
| I/O | 跳过 N-M 个 Segment 的磁盘读取 |
| CPU | 避免解码和过滤被跳过的 Segment |
| 内存 | 避免缓存被跳过 Segment 的解码数据 |

该优化对宽表选择性查询（少量列、少量匹配 Segment）尤其有效。

## 9. 空 key 校验 (Empty Key Validation)

### 9.1 Write 校验

`Write` 方法拒绝空 key，返回清晰的错误信息：

```go
func (e *Engine) Write(key string, value map[string]Value) error {
    if key == "" {
        return fmt.Errorf("write: empty key is not allowed")
    }
    // ...
}
```

### 9.2 WriteBatch 校验

`WriteBatch` 在写入前校验所有行 key，并指明空 key 所在的行号：

```go
func (e *Engine) WriteBatch(rows []Row) error {
    for i, row := range rows {
        if row.Key == "" {
            return fmt.Errorf("writebatch: empty key at row %d is not allowed", i)
        }
    }
    // ...
}
```

### 9.3 目的

防止空 key 进入跳表和 Segment key range 逻辑，避免下游异常。

## 10. 资源泄漏修复 (Resource Leak Fixes)

### 10.1 flushImmutable 泄漏修复

当 `registerSegmentIndexes` 失败时，需要回滚已创建的 Segment 数据并清理已刷写的 Segment 文件：

```go
func (e *Engine) flushImmutable() error {
    // ...刷写 Segment 文件...
    if err := e.registerSegmentIndexes(newSeg); err != nil {
        // 回滚 Segment 数据
        e.segments = e.segments[:origLen]
        delete(e.segmentMap, newSeg.ID)
        e.segmentLevels[0] = e.segmentLevels[0][:origL0Len]
        e.l0SegmentCount = origL0Count
        // 清理已刷写的 Segment 文件
        cleanupSegmentFile(newSeg)
        return err
    }
}
```

### 10.2 Compact 泄漏修复

当 `registerSegmentIndexes` 失败时，需要清理已生成的 Segment 文件：

```go
func (e *Engine) Compact() error {
    // ...合并写入新 Segment...
    if err := e.registerSegmentIndexes(newSeg); err != nil {
        cleanupSegmentFile(newSeg)
        return err
    }
}
```

### 10.3 cleanupSegmentFile 辅助函数

新增 `cleanupSegmentFile` 辅助函数，安全清理 Segment 文件，处理以下边界情况：

- Segment 为 nil
- 路径为空
- 文件不存在

```go
func cleanupSegmentFile(seg *Segment) {
    if seg == nil || seg.Path == "" {
        return
    }
    if err := os.Remove(seg.Path); err != nil && !os.IsNotExist(err) {
        log.Warn("failed to cleanup segment file", "path", seg.Path, "err", err)
    }
}
```

## 11. 配置参考

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
