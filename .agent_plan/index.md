# Index 模块详细设计

## 1. 职责

维护主键到 Segment 的映射，以及每个 Segment 内各列的稀疏索引（Min/Max）和主键布隆过滤器。支持点查定位、范围扫描剪枝、存在性快速判断。

## 2. 外部依赖

| 依赖 | 来源 | 用途 |
|------|------|------|
| `github.com/bits-and-blooms/bloom/v3` | 第三方 | Segment 级主键布隆过滤器 |

自研：主键索引（BTree/跳表）、稀疏索引结构、内存与文件映射。

## 3. 核心结构

### 3.1 主键索引

```go
type PrimaryIndex struct {
    tree *btree.BTree // 或自研 BTree，item: KeyRange -> []SegmentID
}

type KeyRange struct {
    MinKey string
    MaxKey string
}
```

- 每个 Segment 注册其 MinKey/MaxKey 到索引树。
- 点查：在树中搜索包含该 key 的所有 KeyRange，返回对应 SegmentID 列表。
- 范围扫描：搜索与 [start, end] 相交的所有 KeyRange，返回 SegmentID 列表。
- L1+ 层 Segment 键范围不重叠，点查通常返回 1 个 Segment；L0 层可能返回多个。

### 3.2 稀疏索引

```go
type SparseIndex struct {
    SegmentID  uint64
    ColumnID   uint32
    MinValue   Value
    MaxValue   Value
    NullCount  uint32
}
```

- 每个 Segment 的 Footer 中持久化稀疏索引。
- 查询时加载到 IndexCache，谓词判断（如 `age > 30`）时若 Max < 30 或 Min > 30 则跳过整个 Segment。
- 宽表场景下，仅加载查询涉及的列的稀疏索引，避免元数据膨胀。

### 3.3 布隆过滤器

```go
type SegmentBloom struct {
    SegmentID uint64
    Filter    *bloom.BloomFilter
}
```

- 每个 Segment 构建时将所有主键写入布隆过滤器，序列化后存入 Footer。
- 点查时先测布隆过滤器：返回 false 则直接跳过该 Segment；返回 true 则继续读取 ColumnBlock。
- 参数：预期元素数 = Segment 行数，误判率默认 1%。

## 4. 查询路径

```
点查 key=X, column=C:
  1. PrimaryIndex.Lookup(X) → [seg1, seg2, ...]
  2. 对每个 seg:
     a. BloomFilter.Test(X) → false? skip
     b. SparseIndex[C].Min/Max 与查询条件比较 → 可跳过? skip
     c. BlockCache.Get(seg, C) → miss? 读文件 → decompress → decode
     d. 在 ColumnBlock 内定位具体行（通过行号或主键索引）
  3. 合并多 Segment 结果（L0 层可能有多版本，取最新）

范围扫描 [start, end], columns=[C1, C2]:
  1. PrimaryIndex.Range(start, end) → [seg1, seg2, ...]
  2. 对每个 seg，列裁剪：仅加载 C1、C2 的 ColumnBlock
  3. 并行解压解码（goroutine per segment）
  4. MergeIterator 按主键顺序归并输出
```

## 5. 持久化与恢复

- 主键索引不单独持久化，启动时通过扫描所有 Segment 的 Footer 重建。
- 重建过程：遍历数据目录，读取每个 Segment 文件的 Footer，提取 MinKey/MaxKey 和稀疏索引，插入内存索引树。
- 为加速重启，可定期写入 Checkpoint 文件（JSON/Protobuf），记录当前 Segment 列表及元数据摘要。

## 6. 接口定义

```go
type IndexManager interface {
    RegisterSegment(seg SegmentMeta) error
    UnregisterSegment(segID uint64) error
    Lookup(key string) ([]uint64, error)           // 点查 → SegmentIDs
    Range(start, end string) ([]uint64, error)     // 范围 → SegmentIDs
    MayContain(segID uint64, key string) bool      // 布隆过滤
    CanSkip(segID uint64, colID uint32, pred Predicate) bool // 稀疏索引剪枝
    Rebuild() error                                // 从 Segment 文件重建
}
```
