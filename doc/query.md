# 查询引擎详解

## 1. 概述

查询引擎（`pkg/query/`）负责 SQL 语句的解析、分析、优化与执行。采用 Volcano/Batch 模型的向量化执行，一次处理一批行（默认 1024 行），提升 CPU 缓存命中率与执行效率。

## 2. 处理流程

```
SQL 字符串
    │
    ▼
┌──────────┐
│  Parser   │  SQL → AST
└────┬─────┘
     │
     ▼
┌──────────┐
│ Analyzer │  AST → 逻辑计划
└────┬─────┘
     │
     ▼
┌──────────┐
│ Optimizer│  逻辑计划 → 物理计划（优化）
└────┬─────┘
     │
     ▼
┌──────────┐
│ Executor │  执行计划 → Chunk 结果流
└──────────┘
```

## 3. Parser（解析器）

将 SQL 字符串解析为项目内部的 AST。

- **底层**：基于 `github.com/xwb1989/sqlparser`（MySQL 方言）
- **预处理**：将自定义类型映射为 MySQL 兼容类型
  - `INT64` → `BIGINT`
  - `FLOAT64` → `DOUBLE`
  - `STRING` → `TEXT`
  - `BOOL` / `BOOLEAN` → `TINYINT`
- **支持语句**：
  - `SELECT`：含 WHERE、GROUP BY、LIMIT
  - `INSERT`：批量插入
  - `CREATE TABLE`：含列定义、主键

### AST 结构

```go
type Statement interface {
    statementNode()
    String() string
}

// SELECT 语句
type SelectStatement struct {
    Columns []SelectColumn   // 选择列
    From    *TableRef        // 表引用
    Where   Expression       // WHERE 条件
    GroupBy []Expression     // GROUP BY 列
    Limit   *LimitClause     // LIMIT 子句
}
```

## 4. Analyzer（分析器）

对 AST 进行语义分析，生成逻辑查询计划。

- **职责**：
  - 表名解析与验证
  - 列名解析与类型推导
  - 聚合函数识别
  - 生成逻辑计划节点

## 5. Optimizer（优化器）

基于规则的优化器（RBO），对逻辑计划进行等价变换。

- **优化规则**：
  - **列裁剪**：移除查询不需要的列，减少 I/O
  - **谓词下推**：将 Filter 尽可能下推到 Scan 节点，减少数据处理量
  - **常量折叠**：编译期计算常量表达式

## 6. Executor（执行器）

执行物理查询计划，返回 Chunk 结果流。

### 执行模型

向量化执行（Batch 模式），每次处理一批行：

```go
const defaultChunkSize = 1024

type Chunk struct {
    Columns []ColumnVector  // 每列一个向量
    Len     int             // 当前行数
}
```

### 算子

| 算子 | 说明 |
|------|------|
| `ScanNode` | 全表扫描或索引扫描，从存储引擎读取数据 |
| `FilterNode` | 向量化过滤，批量评估 WHERE 条件 |
| `ProjectNode` | 列投影，选择需要的列 |
| `AggregateNode` | 聚合计算，支持 COUNT/SUM/MIN/MAX/AVG |
| `LimitNode` | 结果截断，返回前 N 行 |

### 执行流程

1. **ScanNode**：
   - 若有主键等值条件 → `PrimaryIndex.Lookup()` 点查
   - 若有主键范围条件 → `ScanRange()` 范围扫描
   - 否则 → 全表扫描
   - 扫描时利用 `SparseIndex` 过滤不满足条件的 Segment
   - 利用 `BloomIndex` 过滤不存在的键

2. **FilterNode**：
   - 批量评估 WHERE 条件
   - 生成位图标记满足条件的行
   - 压缩 Chunk，移除不满足条件的行

3. **ProjectNode**：
   - 从 Chunk 中提取所需列
   - 支持表达式计算（如 `a + b`）

4. **AggregateNode**：
   - 按 GROUP BY 键分组
   - 对每组计算聚合函数
   - 支持多阶段聚合（部分聚合 + 最终合并）

5. **LimitNode**：
   - 截断结果到指定行数

## 7. 支持的 SQL 子集

### SELECT

```sql
SELECT col1, col2, AGG(col3) AS alias
FROM table_name
WHERE condition
GROUP BY col1, col2
LIMIT n
```

支持的聚合函数：
- `COUNT(*)` / `COUNT(col)`
- `SUM(col)`
- `MIN(col)`
- `MAX(col)`
- `AVG(col)`

WHERE 支持的比较运算符：
- `=` / `!=` / `<>`
- `>` / `>=` / `<` / `<=`

WHERE 支持的逻辑运算符：
- `AND` / `OR`

### INSERT

```sql
INSERT INTO table_name (col1, col2) VALUES (val1, val2), (val3, val4)
```

### CREATE TABLE

```sql
CREATE TABLE table_name (
  col1 INT64,
  col2 STRING,
  col3 FLOAT64,
  col4 BOOL,
  col5 TIMESTAMP,
  PRIMARY KEY (col1)
)
```

## 8. StorageProvider 接口

查询引擎通过 `StorageProvider` 接口与存储引擎解耦：

```go
type StorageProvider interface {
    ScanRange(start, end string) []storage.ScanEntry
    ColumnMeta() []storage.ColumnMeta
    PrimaryIndex() *index.PrimaryIndex
    SparseIndex() *index.SparseIndex
}
```

Server 层通过 `storageAdapter` 适配 `storage.Engine` 实现此接口。
