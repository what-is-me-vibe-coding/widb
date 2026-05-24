# Storage 模块详细设计

## 1. 职责

负责数据的持久化与读取，包括 WAL（预写日志）、MemTable（内存表）、Segment（不可变列式数据块）、Block（列数据块）以及编码/压缩。向上层提供写入、读取、刷盘、Compaction 接口。

## 2. 外部依赖

| 依赖 | 来源 | 用途 |
|------|------|------|
| `github.com/klauspost/compress/zstd` | 第三方 | Block 级 ZSTD 压缩与解压 |

自研：字典编码、RLE、位图（Bitmap）、WAL 格式、Segment 文件格式、MemTable（跳表）。

## 3. 核心结构

### 3.1 WAL

```go
type WAL struct {
    file   *os.File
    path   string
    offset int64
    mu     sync.Mutex
}
```

- 每条记录：`[4:len][1:type][N:payload][4:crc32]`，len 包含 type + payload + crc32。
- 类型：Write、Commit、Checkpoint。
- 写满阈值（如 64MB）后切分新文件，旧文件异步归档删除。
- 恢复：顺序读取，校验 CRC，回放 Write 记录到 MemTable。

### 3.2 MemTable

```go
type MemTable struct {
    tree  *skipmap.StringMap   // key -> Row
    size  int64                 // 估算内存占用
    mu    sync.RWMutex
}

type Row struct {
    Version uint64
    Columns map[string]Value
}
```

- 键：主键字符串（宽表允许复合主键拼接）。
- 值：版本号 + 列值映射。
- 写入：先写 WAL，再写 MemTable（WAL 成功即返回）。
- 快照：读取时持有 RLock，获取 tree 当前状态的引用，保证一致性。
- 刷盘阈值：默认 32MB，达到后冻结为 ImmutableMemTable，后台生成 Segment。

### 3.3 Segment

```go
type Segment struct {
    ID        uint64
    MinKey    string
    MaxKey    string
    RowCount  uint32
    Columns   []ColumnBlock
    Footer    SegmentFooter
    FilePath  string
}

type SegmentFooter struct {
    ColumnStats []ColumnStat   // 每列 Min/Max/NullCount
    BloomFilter []byte         // 主键布隆过滤器序列化数据
    IndexOffset int64          // Footer 在文件中的偏移
}
```

- 文件格式：`[Magic:4][Version:2][ColumnBlocks...][FooterLen:4][Footer][FooterOffset:8]`。
- Magic：`0x57494442`（"WIDB"）。
- 读取时先读尾部 8 字节获取 FooterOffset，再读 Footer，最后按需读取 ColumnBlock。

### 3.4 ColumnBlock

```go
type ColumnBlock struct {
    ColumnID    uint32
    Encoding    EncodingType   // Plain / Dict / RLE / Bitmap
    Compressed  bool
    Data        []byte         // 编码后数据
    Offsets     []uint32       // 变长数据（字符串）的行偏移
}
```

- **Plain**：原始字节数组，定长类型直接紧凑排列。
- **Dict**：字典表 + 索引数组（索引位宽按字典大小选择 uint8/16/32）。
- **RLE**：`[value][count]` 对序列，适合有序或重复度高的列。
- **Bitmap**：BOOL 类型专用，1 bit 表示一行。
- 编码后使用 ZSTD 压缩，压缩级别默认 `SpeedDefault`（3）。

## 4. 写入流程

```
Client → WAL.Append(record) → MemTable.Put(key, row)
                ↓                      ↓
           刷盘（异步）          达到阈值 → ImmutableMemTable
                                           ↓
                                      SegmentBuilder.Flush()
                                           ↓
                                      CompactionScheduler
```

1. 写入请求序列化为 WAL 记录，fsync 策略可配置（每次/每 N 条/定时）。
2. WAL 成功后更新 MemTable。
3. MemTable 达到阈值后变为只读，新写入切换到新 MemTable。
4. 后台将 ImmutableMemTable 按列拆分、编码、压缩，生成 Segment 文件。
5. 生成完毕后更新 Catalog 的 Segment 列表，删除对应 WAL 文件。

## 5. 读取流程

```
点查：
  key → Index.Primary → SegmentIDs → BloomFilter.Test(key)
  → Segment.LoadColumnBlock(columnID) → Decompress → Decode → Value

范围扫描：
  [start, end] → Index.Primary → SegmentIDs（Min/Max 剪枝）
  → 并行扫描各 Segment → MergeIterator → 返回
```

- 优先查 MemTable，未命中再查 Segment。
- Segment 读取时先查 BlockCache，未命中则读文件并解压。

## 6. Compaction

- **触发条件**：Segment 数量超过层级阈值，或总大小超过限制。
- **Tiered 策略**：
  - L0：ImmutableMemTable 直接生成，允许重叠。
  - L1+：每层 Segment 大小呈倍数增长，同一层内键范围不重叠。
- **过程**：选择相邻层满足条件的 Segment 集合，合并排序后重写为新 Segment，更新 Catalog，删除旧文件。
- **列级 TTL**：Compaction 时检查列的时间戳元数据，过期数据置为 NULL 或不写入新文件。

## 7. 接口定义

```go
type StorageEngine interface {
    Write(key string, row Row) error
    Get(key string, columns []string) (Row, error)
    Scan(start, end string, columns []string) (Iterator, error)
    Flush() error
    Compact() error
    Close() error
}
```
