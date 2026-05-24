# Query 模块详细设计

## 1. 职责

解析 SQL 语句，生成执行计划，通过向量化算子执行查询，返回结果集。涵盖解析、优化、执行三阶段。

## 2. 外部依赖

| 依赖 | 来源 | 用途 |
|------|------|------|
| `github.com/xwb1989/sqlparser` | 第三方 | SQL 词法/语法解析（轻量，兼容 MySQL 方言） |

自研：执行计划、向量化算子、表达式求值、优化器规则。

> 备选：`github.com/moomou/xsqlparser`（sqlparser-rs 的 Go 移植，更现代但生态较小）。初期使用 `xwb1989/sqlparser`，若遇到解析缺陷再迁移。

## 3. 核心结构

### 3.1 执行计划

```go
type PlanNode interface {
    Schema() []ColumnDef
    Children() []PlanNode
}

type ScanNode struct {
    Table      string
    Columns    []string       // 列裁剪后保留的列
    Predicate  Expression     // 下推的谓词
}

type FilterNode struct {
    Child     PlanNode
    Condition Expression
}

type ProjectNode struct {
    Child      PlanNode
    Expressions []Expression
}

type AggregateNode struct {
    Child       PlanNode
    GroupBy     []Expression
    Aggregates  []AggregateExpr
}

type LimitNode struct {
    Child  PlanNode
    Offset uint64
    Limit  uint64
}
```

### 3.2 向量化 Chunk

```go
type Chunk struct {
    Columns []ColumnVector
    Rows    uint32
}

type ColumnVector struct {
    Typ      DataType
    Nulls    Bitmap
    Data     []byte         // 定长类型直接存储
    Offsets  []uint32       // 变长类型（字符串）偏移
}
```

- 默认批次大小：1024 行。
- 每个算子一次处理一个 Chunk，输出一个 Chunk。
- NULL 使用独立的 Bitmap 表示，每行 1 bit。

### 3.3 表达式

```go
type Expression interface {
    Eval(chunk *Chunk, row uint32) (Value, error)
    ReturnType() DataType
}

type ColumnExpr struct { Name string; Idx int }
type LiteralExpr struct { Value Value }
type BinaryExpr struct { Op Operator; Left, Right Expression }
type FuncExpr struct { Name string; Args []Expression }
```

- 常量折叠：优化阶段将 LiteralExpr 的运算提前计算。
- 短路求值：AND/OR 表达式在第一个操作数能决定结果时跳过第二个。

## 4. 查询处理流程

```
SQL String
    ↓
SQL Parser (sqlparser) → AST
    ↓
Analyzer: 语义检查、表/列解析、类型推导
    ↓
Optimizer (RBO):
  - 列裁剪
  - 谓词下推（Filter → Scan）
  - 分区裁剪（基于主键索引）
  - 常量折叠
    ↓
Physical Plan: 选择算子实现（向量化）
    ↓
Executor: 递归调用 NextChunk() 直到 EOF
    ↓
Result Set
```

## 5. 算子实现

### 5.1 Scan

- 根据 Predicate 调用 IndexManager 获取 Segment 列表。
- 对每个 Segment，加载所需 ColumnBlock，解压解码为 ColumnVector。
- 输出 Chunk，每批最多 1024 行。

### 5.2 Filter

- 对输入 Chunk 的每一行求值 Condition（向量化：批量比较生成 SelectionVector）。
- 输出仅包含满足条件的行的 Chunk。
- SIMD 友好：INT64/FLOAT64 的比较可用 `bytes.Compare` 或未来引入 SIMD 指令。

### 5.3 Project

- 对输入 Chunk 逐行计算 Expressions，生成新的 ColumnVector。
- 列裁剪在计划阶段完成，Project 通常只需重组/计算派生列。

### 5.4 Aggregate

- **HashAggregate**：适用于 GroupBy，使用 map[GroupKey]Accumulator。
- **StreamAggregate**：适用于无 GroupBy 或已排序输入，单遍扫描即可。
- 支持函数：COUNT、SUM、MIN、MAX、AVG。

### 5.5 Limit / TopN

- Limit：维护已输出行数计数，达到阈值后返回 EOF。
- TopN：使用最小堆（或最大堆），扫描完毕后输出排序结果。

## 6. 支持的 SQL 子集（Phase 2）

```sql
CREATE TABLE t (id INT64 PRIMARY KEY, name STRING, age INT64);
INSERT INTO t (id, name, age) VALUES (1, 'a', 20), (2, 'b', 30);
SELECT id, name, age FROM t WHERE age > 20 LIMIT 10;
SELECT age, COUNT(*) FROM t GROUP BY age;
```

暂不支持的特性（后续扩展）：JOIN、子查询、窗口函数、UNION、DELETE/UPDATE。

## 7. 接口定义

```go
type QueryEngine interface {
    Execute(sql string) (ResultSet, error)
    Prepare(sql string) (PreparedStmt, error)
}

type ResultSet struct {
    Schema []ColumnDef
    Chunks chan *Chunk
}
```
