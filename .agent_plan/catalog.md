# Catalog 模块详细设计

## 1. 职责

管理数据库的元数据，包括表定义、列定义、数据类型、分区信息、Segment 列表。提供原子性的 Schema 变更与持久化。

## 2. 外部依赖

无第三方依赖。使用标准库 `encoding/json` 或 `google.golang.org/protobuf`（若引入 Protobuf）。

## 3. 核心结构

### 3.1 Schema

```go
type Database struct {
    Version   uint64            // 全局版本号，每次变更递增
    Tables    map[string]*Table
    CreatedAt time.Time
}

type Table struct {
    Name        string
    Columns     []ColumnDef
    PrimaryKey  []string          // 支持复合主键
    SegmentList []SegmentRef      // 当前表的所有 Segment
    Options     TableOptions
}

type ColumnDef struct {
    Name     string
    Type     DataType
    Nullable bool
    Default  Value
}

type SegmentRef struct {
    ID       uint64
    Level    uint8
    MinKey   string
    MaxKey   string
    Size     int64
}
```

### 3.2 数据类型系统

```go
type DataType int

const (
    TypeNull DataType = iota
    TypeBool
    TypeInt64
    TypeFloat64
    TypeString
    TypeTimestamp
)

type Value struct {
    Typ   DataType
    Valid bool          // NULL 时 Valid=false
    Int64 int64
    Float64 float64
    String string
    Time  time.Time
}
```

- 所有列值统一用 Value 结构体传递，内部根据 Type 选择字段。
- 宽表场景下允许动态加列（Schema Evolution）：新列默认 Nullable=true。

## 4. 持久化

- 文件路径：`${data_dir}/catalog.json`。
- 写入策略：先写临时文件 `catalog.json.tmp`，再 `Rename` 覆盖原文件，保证原子性。
- 内容：Database 结构的 JSON 序列化，包含所有表定义和 Segment 列表。
- 恢复：启动时读取 catalog.json，若损坏则扫描数据目录重建（兜底策略）。

## 5. Schema 变更

```go
type Catalog interface {
    CreateTable(def TableDef) error
    DropTable(name string) error
    AddColumn(table, column string, def ColumnDef) error
    DropColumn(table, column string) error
    RegisterSegment(table string, seg SegmentRef) error
    UnregisterSegment(table string, segID uint64) error
    GetTable(name string) (*Table, error)
    Snapshot() (*Database, error)   // 返回当前一致快照
}
```

- Schema 变更持有写锁，阻塞并发变更；查询持有读锁，允许并发。
- 加列操作：仅修改 Catalog，已有 Segment 中不存在的列视为 NULL。
- 删列操作：从 Catalog 移除列定义，Segment 中的旧数据在 Compaction 时清理。

## 6. 版本控制

- 每个 SegmentRef 包含所属表的 Version 范围（MinVersion, MaxVersion）。
- 查询时根据当前 Catalog Version 选择可见的 Segment。
- 简化实现：单机场景下 Catalog 全局串行化，无需复杂 MVCC。
