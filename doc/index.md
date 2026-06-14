# 索引模块详解

## 1. 概述

索引模块（`pkg/index/`）是存储引擎的核心加速组件，负责在查询时快速定位目标 Segment 并跳过无关数据。模块提供三种互补的索引结构：

- **PrimaryIndex**：主键索引，基于键范围映射定位候选 Segment
- **BloomIndex**：布隆过滤器索引，快速判断主键是否可能存在于 Segment 中
- **SparseIndex**：稀疏索引，基于列级 Min/Max 统计跳过不满足谓词条件的 Segment

三种索引在查询路径上逐层过滤，大幅减少实际 I/O 量：

```
查询请求
    │
    ▼
┌──────────────┐
│ PrimaryIndex │  键范围过滤：定位可能包含目标键的 Segment 集合
└──────┬───────┘
       │
       ▼
┌──────────────┐
│  BloomIndex  │  存在性过滤：排除一定不包含目标键的 Segment
└──────┬───────┘
       │
       ▼
┌──────────────┐
│  SparseIndex │  谓词过滤：根据列 Min/Max 跳过不满足条件的 Segment
└──────┬───────┘
       │
       ▼
  实际 I/O 扫描
```

## 2. 核心组件

### 2.1 PrimaryIndex（主键索引）

PrimaryIndex 维护每个 Segment 的键范围到 SegmentID 的映射，支持点查与范围查询。

```go
type SegmentMeta struct {
    ID     uint64  // Segment 唯一标识
    MinKey string  // Segment 中最小主键
    MaxKey string  // Segment 中最大主键
    Level  int     // Segment 所在层级（L0 / L1+）
}

type PrimaryIndex struct {
    mu       sync.RWMutex
    segments []SegmentMeta  // 按 MinKey 排序
}
```

**设计要点**：

- **按 MinKey 排序**：所有 Segment 按 MinKey 升序排列，利用二分查找加速查询
- **层级语义**：L0 层允许键范围重叠（刷盘产生的小 Segment 可能交叉），L1+ 层不允许重叠（Compaction 合并后保证有序）
- **并发安全**：读写操作通过 `sync.RWMutex` 保护，读操作可并发执行

**键范围示意**：

```
L0 层（允许重叠）：
  Seg-1: [a, f]
  Seg-2: [c, h]    ← 与 Seg-1 重叠 [c, f]
  Seg-3: [m, p]

L1 层（不重叠）：
  Seg-10: [a, d]
  Seg-11: [e, k]
  Seg-12: [l, p]
```

### 2.2 BloomIndex（布隆过滤器索引）

BloomIndex 为每个 Segment 维护一个布隆过滤器，用于主键存在性的快速判断。

```go
const DefaultBloomFPRate = 0.01  // 默认误判率 1%

type SegmentBloom struct {
    SegID  uint64
    Filter *bloom.BloomFilter
}

type BloomIndex struct {
    mu      sync.RWMutex
    blooms  map[uint64]*bloom.BloomFilter  // segID → filter
    hitCnt  uint64  // 命中计数（可能存在）
    missCnt uint64  // 未命中计数（一定不存在）
}
```

**设计要点**：

- **概率数据结构**：布隆过滤器可能产生假阳性（误判），但不会产生假阴性
- **零分配查询**：`MayContainString` 使用 `unsafe.Slice` 将 `string` 转为 `[]byte`，避免堆分配
- **原子计数器**：命中/未命中统计使用 `atomic` 操作，不阻塞查询路径
- **底层库**：`github.com/bits-and-blooms/bloom/v3`

**布隆过滤器原理**：

```
插入 key "hello"：
  hash1("hello") = 3  →  bit[3] = 1
  hash2("hello") = 7  →  bit[7] = 1
  hash3("hello") = 11 →  bit[11] = 1

查询 key "world"：
  hash1("world") = 3  →  bit[3] = 1  ✓
  hash2("world") = 5  →  bit[5] = 0  ✗ → 一定不存在，可跳过
```

### 2.3 SparseIndex（稀疏索引）

SparseIndex 维护每个 Segment 中每列的 Min/Max 统计信息，用于谓词过滤。

```go
type PredicateOp int

const (
    OpEqual        PredicateOp = iota  // 等于
    OpNotEqual                          // 不等于
    OpLess                              // 小于
    OpLessEqual                         // 小于等于
    OpGreater                           // 大于
    OpGreaterEqual                      // 大于等于
)

type ColumnSparseStat struct {
    MinValue  common.Value  // 列最小值
    MaxValue  common.Value  // 列最大值
    NullCount uint32        // NULL 值数量
    HasValues bool          // 是否有非 NULL 值
}

type SparseIndex struct {
    mu    sync.RWMutex
    stats map[colStatKey]ColumnSparseStat  // (segID, colID) → 统计
}
```

**设计要点**：

- **列级粒度**：统计信息以 (SegmentID, ColumnID) 为键，支持按列独立过滤
- **多类型支持**：通过 `bytesToValue` 支持将原始字节还原为 Int64、Float64、Bool、Timestamp、String 等类型
- **NULL 感知**：统计中包含 NullCount，构建时跳过 NULL 值

## 3. 查询路径

### 3.1 点查路径（主键等值查询）

以 `SELECT * FROM t WHERE id = 'key123'` 为例：

```
  查询 key = "key123"
        │
        ▼
  ┌─────────────────────────────┐
  │ 1. PrimaryIndex.Lookup()    │  二分查找 MinKey 排序的 segments
  │    定位候选 Segment 集合      │  返回键范围包含 "key123" 的所有 SegID
  └──────────┬──────────────────┘
             │ 候选: [Seg-1, Seg-3, Seg-5]
             ▼
  ┌─────────────────────────────┐
  │ 2. BloomIndex.MayContain()  │  对每个候选 Segment 检查布隆过滤器
  │    排除一定不存在的 Segment   │  "key123" 不在 Seg-3 → 移除
  └──────────┬──────────────────┘
             │ 剩余: [Seg-1, Seg-5]
             ▼
  ┌─────────────────────────────┐
  │ 3. SparseIndex.CanSkip()    │  对 WHERE 中的非主键列条件检查
  │    根据列 Min/Max 跳过       │  Seg-5 的 age 列 max=20 < 条件 age=30 → 跳过
  └──────────┬──────────────────┘
             │ 最终: [Seg-1]
             ▼
  ┌─────────────────────────────┐
  │ 4. 实际 I/O 扫描 Seg-1      │  只读取 1 个 Segment 的数据
  └─────────────────────────────┘
```

### 3.2 范围查询路径

以 `SELECT * FROM t WHERE id >= 'b' AND id <= 'e'` 为例：

```
  查询范围 ["b", "e"]
        │
        ▼
  ┌─────────────────────────────┐
  │ 1. PrimaryIndex.Range()     │  二分查找 MinKey > "e" 的位置
  │    定位键范围有交集的 Segment │  扫描 MinKey <= "e" 的 segments
  └──────────┬──────────────────┘
             │ 候选: [Seg-1, Seg-2, Seg-3]
             ▼
  ┌─────────────────────────────┐
  │ 2. SparseIndex.CanSkip()    │  对范围查询中的附加列条件过滤
  │    根据列 Min/Max 跳过       │
  └──────────┬──────────────────┘
             │ 最终: [Seg-1, Seg-3]
             ▼
  ┌─────────────────────────────┐
  │ 3. 实际 I/O 扫描            │  多路归并读取候选 Segments
  └─────────────────────────────┘
```

### 3.3 三级索引协作总结

| 层级 | 索引 | 过滤维度 | 过滤效果 | 代价 |
|------|------|----------|----------|------|
| 第一级 | PrimaryIndex | 主键键范围 | 缩小到键范围有交集的 Segment | 内存二分查找，O(log N) |
| 第二级 | BloomIndex | 主键存在性 | 排除一定不包含目标键的 Segment | 内存位运算，O(k) hash |
| 第三级 | SparseIndex | 列值 Min/Max | 跳过列值不满足谓词的 Segment | 内存比较，O(1) |

## 4. API 参考

### 4.1 PrimaryIndex

| 方法 | 签名 | 说明 |
|------|------|------|
| `NewPrimaryIndex` | `() *PrimaryIndex` | 创建主键索引 |
| `RegisterSegment` | `(seg SegmentMeta) error` | 注册 Segment，校验 ID 非零且 MinKey ≤ MaxKey |
| `UnregisterSegment` | `(segID uint64) error` | 移除 Segment，不存在时返回错误 |
| `Lookup` | `(key string) []uint64` | 点查，返回包含 key 的所有 Segment ID |
| `Range` | `(start, end string) []uint64` | 范围查询，返回与 [start, end] 有交集的所有 Segment ID |
| `SegmentCount` | `() int` | 返回已注册 Segment 数量 |
| `GetSegment` | `(segID uint64) (SegmentMeta, bool)` | 获取指定 ID 的 Segment 元数据 |
| `Clear` | `()` | 清空所有索引 |

**Lookup 二分查找逻辑**：

1. 二分查找第一个 `MinKey > key` 的位置 `idx`
2. 从 `idx-1` 向前扫描，检查 key 是否在 `[MinKey, MaxKey]` 范围内（L0 重叠，不能提前终止）
3. 从 `idx` 向后扫描，检查 `MinKey == key` 的 Segment

**Range 二分查找逻辑**：

1. 二分查找第一个 `MinKey > end` 的位置 `idx`
2. 扫描 `[0, idx)` 范围内的 Segment，检查键范围是否有交集
3. 交集条件：`start <= maxKey && end >= minKey`

### 4.2 BloomIndex

| 方法 | 签名 | 说明 |
|------|------|------|
| `NewBloomIndex` | `() *BloomIndex` | 创建布隆过滤器索引 |
| `Register` | `(segID uint64, filter *bloom.BloomFilter) error` | 注册布隆过滤器，filter 不能为 nil |
| `RegisterFromBytes` | `(segID uint64, data []byte) error` | 从序列化字节注册布隆过滤器 |
| `Unregister` | `(segID uint64)` | 移除 Segment 的布隆过滤器 |
| `MayContain` | `(segID uint64, key []byte) bool` | 检查 key 是否可能存在，false 表示一定不存在 |
| `MayContainString` | `(segID uint64, key string) bool` | 同上，接受 string 参数，零堆分配 |
| `Stats` | `() (hit, miss uint64)` | 返回命中/未命中统计 |
| `Len` | `() int` | 返回已注册布隆过滤器数量 |
| `Clear` | `()` | 清空所有布隆过滤器并重置统计 |
| `BuildFromKeys` | `(keys []string, fpRate float64) ([]byte, error)` | 根据主键集合构建布隆过滤器，返回序列化字节 |
| `BuildAndRegister` | `(segID uint64, keys []string, fpRate float64) error` | 构建布隆过滤器并直接注册，避免序列化开销 |

**注意事项**：

- `MayContain` / `MayContainString` 在 Segment 未注册布隆过滤器时返回 `true`（保守策略，不跳过）
- `MayContainString` 使用 `unsafe.Slice(unsafe.StringData(key), len(key))` 将 string 转为只读 `[]byte`，调用方不得修改返回的切片
- `BuildFromKeys` 是包级函数，`BuildAndRegister` 是方法

### 4.3 SparseIndex

| 方法 | 签名 | 说明 |
|------|------|------|
| `NewSparseIndex` | `() *SparseIndex` | 创建稀疏索引 |
| `RegisterColumnStat` | `(segID, colID uint64/uint32, minVal, maxVal []byte, nullCount uint32, dataType DataType)` | 注册列统计信息 |
| `GetColumnStat` | `(segID uint64, colID uint32) (ColumnSparseStat, bool)` | 获取列统计信息 |
| `UnregisterSegment` | `(segID uint64)` | 注销 Segment 的所有列统计 |
| `CanSkip` | `(segID uint64, colID uint32, op PredicateOp, value Value) bool` | 判断是否可以跳过扫描 |
| `LoadFromSegment` | `(seg SegmentStats, _, _ string, _ int)` | 从 SegmentStats 接口加载所有列统计 |
| `BuildFromColumnVector` | `(segID uint64, colID uint32, cv ColumnVectorReader)` | 从列向量构建统计信息 |
| `StatCount` | `() int` | 返回已注册列统计数量 |
| `Clear` | `()` | 清空所有统计信息 |

### 4.4 CanSkip 谓词过滤逻辑

| 操作符 | 条件 | 跳过条件 | 说明 |
|--------|------|----------|------|
| `OpEqual` | `col = value` | `value < min \|\| value > max` | 值不在 [min, max] 范围内 |
| `OpNotEqual` | `col != value` | 永不跳过 | 不等于无法通过 Min/Max 排除 |
| `OpLess` | `col < value` | `min >= value` | 最小值已不小于 value，所有值都不满足 |
| `OpLessEqual` | `col <= value` | `value < min` | 最小值大于 value，所有值都不满足 |
| `OpGreater` | `col > value` | `max <= value` | 最大值不大于 value，所有值都不满足 |
| `OpGreaterEqual` | `col >= value` | `max < value` | 最大值小于 value，所有值都不满足 |

**图示**（以 `OpEqual` 为例）：

```
列值范围:    |-------- [min, max] --------|
                                    ↑
                              value 在范围外 → 跳过

列值范围:           |---- [min, max] ----|
                         ↑
                   value 在范围内 → 不可跳过
```

## 5. 索引生命周期

索引数据随 Segment 的创建和删除而更新：

```
Segment 创建（Flush / Compaction）
    │
    ├─→ PrimaryIndex.RegisterSegment(segMeta)
    ├─→ BloomIndex.BuildAndRegister(segID, keys, fpRate)
    └─→ SparseIndex.LoadFromSegment(segStats)
          或
          SparseIndex.BuildFromColumnVector(segID, colID, cv)

Segment 删除（Compaction 合并后清理旧 Segment）
    │
    ├─→ PrimaryIndex.UnregisterSegment(segID)
    ├─→ BloomIndex.Unregister(segID)
    └─→ SparseIndex.UnregisterSegment(segID)
```

## 6. 配置参数

### BloomIndex 参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `DefaultBloomFPRate` | 0.01 (1%) | 布隆过滤器默认误判率 |
| `fpRate` 参数 | 0.01 | `BuildFromKeys` / `BuildAndRegister` 可自定义，有效范围 (0, 1) |

误判率与空间开销的关系：

```
误判率    每元素位数    100 万键内存
─────────────────────────────────
1%        9.6 bits     ~1.2 MB
0.1%      14.4 bits    ~1.8 MB
0.01%     19.2 bits    ~2.4 MB
```

### PrimaryIndex 参数

PrimaryIndex 无独立配置参数，其内存占用与 Segment 数量线性相关。

### SparseIndex 参数

SparseIndex 无独立配置参数，每列统计信息固定开销（两个 Value + 一个 uint32 + 一个 bool）。

## 7. 代码示例

### 7.1 注册 Segment 索引

```go
// Flush 完成后注册索引
segMeta := index.SegmentMeta{
    ID:     segID,
    MinKey: "user_0001",
    MaxKey: "user_5000",
    Level:  0,
}
primaryIndex.RegisterSegment(segMeta)

// 构建并注册布隆过滤器
bloomIndex.BuildAndRegister(segID, keys, 0.01)

// 加载列统计
sparseIndex.LoadFromSegment(segStats, "", "", 0)
```

### 7.2 查询时使用索引

```go
// 点查：主键等值查询
segIDs := primaryIndex.Lookup("user_1234")

// 用布隆过滤器进一步过滤
var candidates []uint64
for _, id := range segIDs {
    if bloomIndex.MayContainString(id, "user_1234") {
        candidates = append(candidates, id)
    }
}

// 用稀疏索引过滤非主键列条件
var final []uint64
for _, id := range candidates {
    if !sparseIndex.CanSkip(id, ageColID, index.OpGreater, common.NewInt64(30)) {
        final = append(final, id)
    }
}

// 仅扫描 final 中的 Segment
```

### 7.3 范围查询

```go
// 范围查询
segIDs := primaryIndex.Range("user_0100", "user_0200")

// 稀疏索引过滤附加条件
for _, id := range segIDs {
    if sparseIndex.CanSkip(id, statusColID, index.OpEqual, common.NewString("active")) {
        continue // 跳过 status 列不含 "active" 的 Segment
    }
    // 扫描该 Segment
}
```

### 7.4 从序列化数据恢复布隆过滤器

```go
// 从 Segment Footer 读取布隆过滤器字节
data := readBloomFilterFromFooter(segFile)

// 注册到索引
if err := bloomIndex.RegisterFromBytes(segID, data); err != nil {
    log.Fatalf("恢复布隆过滤器失败: %v", err)
}
```

### 7.5 从列向量构建稀疏统计

```go
// 逐列构建统计
for colID, cv := range columnVectors {
    sparseIndex.BuildFromColumnVector(segID, uint32(colID), cv)
}
```
