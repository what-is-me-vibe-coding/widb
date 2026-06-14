# 元数据管理模块详解

## 1. 概述

元数据管理模块（`pkg/catalog/`）负责数据库 Schema 的定义、变更与持久化。Catalog 作为元数据的唯一入口，提供表创建/删除、列增删、Segment 注册等操作，并通过读写锁保证并发安全，通过原子写入保证持久化的可靠性。

```
┌──────────────────────────────────────────────────┐
│                    Catalog                        │
│  ┌──────────┐  ┌──────────┐  ┌───────────────┐  │
│  │ Schema   │  │ 持久化    │  │ 并发控制       │  │
│  │ 管理     │  │ (JSON)   │  │ (sync.RWMutex)│  │
│  └──────────┘  └──────────┘  └───────────────┘  │
│         │            │              │             │
│         ▼            ▼              ▼             │
│  ┌─────────────────────────────────────────────┐ │
│  │              Database                       │ │
│  │   ┌─────────┐  ┌─────────┐  ┌─────────┐   │ │
│  │   │ Table A │  │ Table B │  │ Table N │   │ │
│  │   │ - Columns│  │ - Columns│  │ - Columns│  │ │
│  │   │ - PK    │  │ - PK    │  │ - PK    │   │ │
│  │   │ - Segs  │  │ - Segs  │  │ - Segs  │   │ │
│  │   └─────────┘  └─────────┘  └─────────┘   │ │
│  └─────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────┘
```

## 2. 核心数据结构

### 2.1 Catalog

Catalog 是元数据管理的顶层结构，协调 Schema 变更与持久化：

```go
type Catalog struct {
    mu   sync.RWMutex  // 读写锁，保护并发访问
    db   *Database     // 数据库元数据
    path string        // 持久化文件路径，空则不持久化
}
```

- **并发控制**：使用 `sync.RWMutex`，写操作（CreateTable、DropTable 等）获取写锁，读操作（GetTable、Snapshot 等）获取读锁
- **持久化路径**：`path` 为空时 Catalog 仅在内存中运行，不落盘

### 2.2 Database

Database 是所有表定义的容器：

```go
type Database struct {
    Version   uint64             // 全局版本号，每次变更递增
    Tables    map[string]*Table  // 表名 → 表定义
    CreatedAt time.Time          // 创建时间
}
```

- **版本号**：每次 Schema 变更（建表、删表、加列等）均递增 `Version`，可用于判断元数据是否发生变化
- **初始版本**：`NewDatabase()` 创建时 `Version` 为 1

### 2.3 Table

Table 定义一张宽表的完整结构：

```go
type Table struct {
    Name        string
    Columns     []ColumnDef     // 列定义
    PrimaryKey  []string        // 复合主键列名
    SegmentList []SegmentRef    // 表关联的所有 Segment
    Options     TableOptions    // 表级配置
    Version     uint64          // 表结构版本号
    CreatedAt   time.Time

    colTypeMap map[string]common.DataType  // 列名→类型缓存（延迟初始化）
}
```

Table 提供以下辅助方法：

| 方法 | 说明 |
|------|------|
| `ColTypeMap()` | 返回列名到数据类型的映射，首次调用时构建并缓存，后续直接返回 |
| `ColumnIndex(name)` | 返回列在 `Columns` 切片中的索引，不存在返回 `-1` 和 `ErrColumnNotExist` |
| `GetColumn(name)` | 按名称获取列定义指针 |
| `HasColumn(name)` | 判断列是否存在 |

`ColTypeMap()` 采用延迟初始化策略，避免每次请求都重建 map，减少热点路径上的内存分配。深拷贝 Table 时会将 `colTypeMap` 置为 `nil`，使其在下次访问时重建。

### 2.4 ColumnDef

```go
type ColumnDef struct {
    Name     string
    Type     common.DataType
    Nullable bool
    Default  common.Value
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `Name` | string | 列名 |
| `Type` | common.DataType | 数据类型（INT64 / FLOAT64 / STRING / BOOL / TIMESTAMP） |
| `Nullable` | bool | 是否允许 NULL 值 |
| `Default` | common.Value | 列默认值 |

### 2.5 TableOptions

```go
type TableOptions struct {
    MaxSegmentSize  int64  // 单个 Segment 的最大字节数
    MaxMemTableSize int64  // MemTable 刷盘阈值
}
```

### 2.6 SegmentRef

SegmentRef 引用一个已持久化的 Segment，记录其元信息用于查询路由：

```go
type SegmentRef struct {
    ID       uint64
    Level    uint8
    MinKey   string
    MaxKey   string
    Size     int64
    RowCount uint32
}
```

| 字段 | 说明 |
|------|------|
| `ID` | Segment 唯一标识 |
| `Level` | 层级（L0 = 刷盘产生，L1 = Compaction 合并） |
| `MinKey` / `MaxKey` | 主键范围，用于范围扫描过滤 |
| `Size` | Segment 文件大小（字节） |
| `RowCount` | 行数 |

## 3. Schema 管理

### 3.1 创建表

```go
cat := catalog.NewCatalog("data/catalog.json")

err := cat.CreateTable("users",
    []catalog.ColumnDef{
        {Name: "id", Type: common.INT64, Nullable: false},
        {Name: "name", Type: common.STRING, Nullable: true},
        {Name: "age", Type: common.INT64, Nullable: true},
    },
    []string{"id"},  // 主键
    catalog.TableOptions{
        MaxSegmentSize:  128 * 1024 * 1024,
        MaxMemTableSize: 64 * 1024 * 1024,
    },
)
```

创建表的校验规则：

1. 表名不能重复，否则返回 `table "xxx" already exists`
2. 至少需要一个列，否则返回 `ErrInvalidSchema`
3. 必须指定主键，否则返回 `ErrInvalidSchema`
4. 主键列必须存在于列定义中，否则返回 `ErrInvalidSchema`

创建成功后：
- 表版本号 `Version` 初始化为 1
- `SegmentList` 初始化为空切片
- 数据库版本号递增
- 自动持久化到文件

### 3.2 删除表

```go
err := cat.DropTable("users")
```

- 表不存在时返回 `ErrTableNotExist`
- 删除后数据库版本号递增并持久化

### 3.3 添加列

```go
err := cat.AddColumn("users", "email", catalog.ColumnDef{
    Name: "email",
    Type: common.STRING,
})
```

- 表不存在时返回 `ErrTableNotExist`
- 列名重复时返回错误
- **新列强制设置 `Nullable = true`**，确保已有数据不会因缺少值而违反约束
- 表版本号和数据库版本号均递增

### 3.4 删除列

```go
err := cat.DropColumn("users", "age")
```

- 表不存在时返回 `ErrTableNotExist`
- 列不存在时返回 `ErrColumnNotExist`
- **不允许删除主键列**，否则返回 `cannot drop primary key column "xxx"`
- 表版本号和数据库版本号均递增

### 3.5 Segment 注册与移除

```go
// 注册 Segment（Flush 或 Compaction 完成后调用）
err := cat.RegisterSegment("users", catalog.SegmentRef{
    ID:       1,
    Level:    0,
    MinKey:   "1",
    MaxKey:   "1000",
    Size:     4 * 1024 * 1024,
    RowCount: 10000,
})

// 移除 Segment（Compaction 合并旧 Segment 后调用）
err := cat.UnregisterSegment("users", 1)
```

- 注册时检查 Segment ID 是否重复，重复则返回错误
- 移除时检查 Segment ID 是否存在，不存在则返回错误
- 两者均递增数据库版本号并持久化

### 3.6 查询操作

```go
// 获取表定义（返回深拷贝）
tbl, err := cat.GetTable("users")

// 获取数据库快照
snap := cat.Snapshot()

// 获取当前版本号
ver := cat.Version()
```

`GetTable` 返回深拷贝，避免外部代码意外修改内部状态。拷贝内容包括 `Columns`、`PrimaryKey`、`SegmentList`，并将 `colTypeMap` 置为 `nil` 以触发延迟重建。

`Snapshot` 返回整个 Database 的深拷贝，适用于需要一致性视图的场景（如查询规划）。

## 4. 持久化机制

### 4.1 原子写入

持久化采用"先写临时文件再 Rename"的策略，保证文件始终处于完整状态：

```
写入流程：

  ┌─────────────┐
  │ JSON 序列化  │     json.MarshalIndent
  └──────┬──────┘
         │
         ▼
  ┌─────────────┐
  │ 写入临时文件  │     catalog.json.tmp
  └──────┬──────┘
         │
         ▼
  ┌─────────────┐
  │ 原子 Rename  │     catalog.json.tmp → catalog.json
  └──────┬──────┘
         │
         ▼
  ┌─────────────┐
  │   写入完成   │
  └─────────────┘
```

1. 将 `Database` 序列化为格式化 JSON（`json.MarshalIndent`）
2. 写入临时文件 `catalog.json.tmp`
3. 调用 `os.Rename` 将临时文件重命名为目标文件

`os.Rename` 在同一文件系统上是原子操作，确保即使进程在写入过程中崩溃，目标文件也不会处于半写状态。

### 4.2 加载逻辑

```go
cat, err := catalog.LoadCatalog("data/catalog.json")
```

加载流程：

1. 若路径为空，直接创建空 Catalog（纯内存模式）
2. 读取文件内容
3. 文件不存在 → 返回空 `Database`（`Version=1`，无表）
4. 文件为空 → 同上
5. JSON 反序列化为 `Database`
6. 若 `Tables` 为 `nil`，初始化为空 map

### 4.3 持久化触发时机

所有写操作（CreateTable、DropTable、AddColumn、DropColumn、RegisterSegment、UnregisterSegment）均在持有写锁的情况下执行变更后自动调用 `persist()`。若 `path` 为空，`persist()` 直接返回，不执行 I/O。

## 5. API 参考

### Catalog 方法

| 方法 | 锁类型 | 说明 |
|------|--------|------|
| `NewCatalog(path)` | - | 创建空 Catalog |
| `LoadCatalog(path)` | - | 从文件加载 Catalog |
| `CreateTable(name, columns, primaryKey, opts)` | 写锁 | 创建表 |
| `DropTable(name)` | 写锁 | 删除表 |
| `AddColumn(table, column, def)` | 写锁 | 添加列（强制 Nullable） |
| `DropColumn(table, column)` | 写锁 | 删除列（禁止删主键列） |
| `RegisterSegment(table, seg)` | 写锁 | 注册 Segment |
| `UnregisterSegment(table, segID)` | 写锁 | 移除 Segment |
| `GetTable(name)` | 读锁 | 获取表定义（深拷贝） |
| `Snapshot()` | 读锁 | 获取 Database 一致快照 |
| `Version()` | 读锁 | 获取当前版本号 |

### Table 方法

| 方法 | 说明 |
|------|------|
| `ColTypeMap()` | 返回列名→类型映射（延迟缓存） |
| `ColumnIndex(name)` | 返回列索引位置 |
| `GetColumn(name)` | 返回列定义指针 |
| `HasColumn(name)` | 判断列是否存在 |

### 错误码

| 错误 | 触发场景 |
|------|----------|
| `ErrTableNotExist` | 操作不存在的表 |
| `ErrColumnNotExist` | 操作不存在的列 |
| `ErrInvalidSchema` | Schema 校验失败（无列、无主键、主键列缺失） |

## 6. 并发安全设计

```
写操作（写锁）                    读操作（读锁）
┌─────────────────┐              ┌─────────────────┐
│ CreateTable     │              │ GetTable        │
│ DropTable       │   互斥       │ Snapshot        │
│ AddColumn       │ ◄──────────► │ Version         │
│ DropColumn      │              └─────────────────┘
│ RegisterSegment │
│ UnregisterSegment│
└─────────────────┘
```

- 写操作之间互斥，同一时刻只有一个 goroutine 可以修改 Schema
- 读操作之间并发，多个 goroutine 可同时读取元数据
- 读写互斥，写操作进行时读操作阻塞，保证读到的一致性
- `GetTable` 和 `Snapshot` 返回深拷贝，确保返回后外部修改不影响内部状态
