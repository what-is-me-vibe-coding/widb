# 公共模块详解

## 1. 概述

公共模块（`pkg/common/`）是 WiDB 的基础工具库，为存储引擎、查询引擎、索引等上层模块提供统一的数据类型、错误码、位图与内存池等基础设施。所有模块共享同一套类型定义与错误语义，确保跨层交互的一致性。

```
pkg/common/
├── types.go    # DataType 与 Value 定义
├── errors.go   # 预定义错误码
├── bitmap.go   # 高性能位图
├── pool.go     # 字节切片复用池
└── hash.go     # 哈希工具
```

## 2. 数据类型系统

### 2.1 DataType 枚举

`DataType` 定义了 WiDB 支持的全部列数据类型：

```go
type DataType int

const (
    TypeNull      DataType = iota  // NULL
    TypeBool                       // BOOL
    TypeInt64                      // INT64
    TypeFloat64                    // FLOAT64
    TypeString                     // STRING / VARCHAR
    TypeTimestamp                  // TIMESTAMP
)
```

#### 类型大小

| 类型 | `Size()` 返回值 | 说明 |
|------|-----------------|------|
| `TypeNull` | 0 | 空类型，无数据 |
| `TypeBool` | 1 | 单字节布尔 |
| `TypeInt64` | 8 | 64 位有符号整数 |
| `TypeFloat64` | 8 | 64 位双精度浮点 |
| `TypeTimestamp` | 8 | 时间戳（内部存储为 int64） |
| `TypeString` | -1 | 变长字符串，无固定大小 |

> **设计要点**：`Size()` 对变长类型返回 `-1`，上层模块可据此区分定长与变长编码策略——定长类型使用 Plain 编码，变长类型使用 Dictionary / RLE 等编码。

#### String() 方法

```go
TypeBool.String()      // "BOOL"
TypeInt64.String()     // "INT64"
TypeFloat64.String()   // "FLOAT64"
TypeString.String()    // "STRING"
TypeTimestamp.String() // "TIMESTAMP"
```

### 2.2 Value 结构体

`Value` 是统一的列值表示，所有模块通过它传递数据：

```go
type Value struct {
    Typ     DataType
    Valid   bool      // false 表示 NULL
    Int64   int64     // BOOL / INT64 / TIMESTAMP 共用
    Float64 float64   // FLOAT64
    Str     string    // STRING
    Time    time.Time // TIMESTAMP
}
```

**字段复用说明**：

- `Int64` 字段被 `TypeBool`、`TypeInt64`、`TypeTimestamp` 三种类型共用
- `TypeBool` 用 `Int64` 存储 0/1 表示 false/true
- `TypeTimestamp` 的 `Time` 字段用于时间比较，`Int64` 用于排序

**NULL 语义**：`Valid=false` 表示 NULL 值。NULL 参与比较时遵循 SQL 三值逻辑——任何值与 NULL 的比较结果均为 false。

### 2.3 Value 方法

#### IsNull()

判断值是否为 NULL：

```go
v := NewNull()
v.IsNull()  // true

v2 := NewInt64(42)
v2.IsNull() // false
```

#### Equal()

比较两个 Value 是否相等：

```go
NewInt64(10).Equal(NewInt64(10))  // true
NewInt64(10).Equal(NewInt64(20))  // false
NewInt64(10).Equal(NewFloat64(10)) // false — 类型不同
NewNull().Equal(NewNull())        // true — NULL 相等
NewInt64(10).Equal(NewNull())     // false — 一方为 NULL
```

#### Less()

比较 Value 是否小于另一个：

```go
NewInt64(10).Less(NewInt64(20))    // true
NewInt64(10).Less(NewString("a"))  // false — 类型不同
NewNull().Less(NewInt64(10))       // false — NULL 参与比较返回 false
```

> **注意**：`Less()` 在类型不同或任一操作数为 NULL 时返回 false，上层排序逻辑需额外处理 NULL 值的排序位置。

#### String()

返回可读字符串表示：

```go
NewBool(true).String()       // "true"
NewInt64(42).String()        // "42"
NewFloat64(3.14).String()    // "3.14"
NewString("hello").String()  // "hello"
NewTimestamp(t).String()     // RFC3339Nano 格式
NewNull().String()           // "NULL"
```

### 2.4 构造函数

| 构造函数 | 类型 | 示例 |
|----------|------|------|
| `NewNull()` | TypeNull | `NewNull()` |
| `NewBool(v bool)` | TypeBool | `NewBool(true)` |
| `NewInt64(v int64)` | TypeInt64 | `NewInt64(100)` |
| `NewFloat64(v float64)` | TypeFloat64 | `NewFloat64(3.14)` |
| `NewString(v string)` | TypeString | `NewString("hello")` |
| `NewTimestamp(v time.Time)` | TypeTimestamp | `NewTimestamp(time.Now())` |

使用示例：

```go
// 插入一行数据
values := []common.Value{
    common.NewInt64(1),          // id
    common.NewString("Alice"),   // name
    common.NewFloat64(95.5),     // score
    common.NewBool(true),        // active
    common.NewNull(),            // nullable column
}
```

## 3. 错误处理

### 3.1 预定义错误码

`pkg/common/errors.go` 定义了全项目统一的错误码，各模块通过 `errors.Is()` 判断错误类型：

```go
var (
    ErrKeyNotFound    = errors.New("key not found")
    ErrTableNotExist  = errors.New("table does not exist")
    ErrColumnNotExist = errors.New("column does not exist")
    ErrTypeMismatch   = errors.New("type mismatch")
    ErrCorruptedData  = errors.New("corrupted data")
    ErrInvalidSchema  = errors.New("invalid schema")
    ErrDuplicateKey   = errors.New("duplicate key")
    ErrReadOnly       = errors.New("read only")
)
```

### 3.2 错误码分类

| 错误码 | 触发场景 | 典型来源 |
|--------|----------|----------|
| `ErrKeyNotFound` | 主键查找未命中 | PrimaryIndex.Lookup() |
| `ErrTableNotExist` | 操作不存在的表 | Catalog |
| `ErrColumnNotExist` | 引用不存在的列 | Analyzer |
| `ErrTypeMismatch` | 类型转换/比较不兼容 | Executor 表达式求值 |
| `ErrCorruptedData` | 数据校验失败 | Segment 解码、WAL 回放 |
| `ErrInvalidSchema` | Schema 定义非法 | CREATE TABLE |
| `ErrDuplicateKey` | 插入重复主键 | Engine.Put() |
| `ErrReadOnly` | 只读模式下写入 | Server |

### 3.3 使用模式

```go
// 检查特定错误
val, err := engine.Get(key)
if errors.Is(err, common.ErrKeyNotFound) {
    // 键不存在，返回默认值
    return NewNull()
}

// 区分错误类型
err := catalog.CreateTable(schema)
if errors.Is(err, common.ErrInvalidSchema) {
    // Schema 非法
} else if errors.Is(err, common.ErrTableNotExist) {
    // 表不存在
}
```

## 4. Bitmap 详解

### 4.1 数据结构

Bitmap 使用 `uint64` 数组作为底层存储，每个 bit 表示一个布尔值：

```go
type Bitmap struct {
    bits []uint64  // 底层 word 数组
    len  uint32    // 逻辑长度（位数）
}
```

**内存布局**：

```
len = 200
bits = [word0, word1, word2, word3]

word0: bit 0  ~ bit 63
word1: bit 64 ~ bit 127
word2: bit 128 ~ bit 191
word3: bit 192 ~ bit 199 (高位未使用)
```

**容量计算**：`words = (length + 63) / 64`，即向上取整到 64 的倍数。

### 4.2 构造方法

#### NewBitmap(length)

创建指定位数的空位图（所有位为 0）：

```go
bm := common.NewBitmap(1000)  // 1000 位，底层 16 个 uint64
```

#### NewBitmapFromBytes(data)

从字节切片反序列化位图，使用 **word-at-a-time** 转换：

```go
data := []byte{0xFF, 0x00, 0x0F, 0xF0, ...}
bm := common.NewBitmapFromBytes(data)
```

**性能优势**：相比逐 bit 处理，word-at-a-time 一次转换 8 字节（64 bit），速度提升约 8 倍。

### 4.3 基本操作

| 方法 | 说明 | 时间复杂度 |
|------|------|------------|
| `Set(i)` | 将第 i 位设为 1 | O(1) |
| `Clear(i)` | 将第 i 位设为 0 | O(1) |
| `Get(i)` | 获取第 i 位的值 | O(1) |
| `Count()` | 统计 1 的个数 | O(words) |
| `Reset()` | 清零所有位 | O(words) |
| `IsEmpty()` | 判断是否全为 0 | O(words) |
| `Flip(i)` | 翻转第 i 位 | O(1) |

```go
bm := common.NewBitmap(64)
bm.Set(3)       // 第 3 位设为 1
bm.Set(7)       // 第 7 位设为 1
bm.Get(3)       // true
bm.Get(4)       // false
bm.Flip(3)      // 第 3 位翻转为 0
bm.Get(3)       // false
bm.Count()      // 1（只有第 7 位为 1）
bm.Reset()      // 全部清零
bm.IsEmpty()    // true
```

### 4.4 位运算

| 方法 | 说明 | 行为 |
|------|------|------|
| `And(other)` | 按位与 | 超出 other 长度的位清零 |
| `Or(other)` | 按位或 | 自动扩展到较大长度 |
| `Xor(other)` | 按位异或 | 自动扩展到较大长度 |
| `Not()` | 按位取反 | 原地取反，不改变长度 |

```go
a := common.NewBitmap(8)
a.Set(0); a.Set(2); a.Set(4)  // a = 00010101

b := common.NewBitmap(8)
b.Set(0); b.Set(1); b.Set(4)  // b = 00010011

a.And(b)  // a = 00010001（bit 0 和 bit 4）
a.Or(b)   // a = 00010111（bit 0, 1, 2, 4）
a.Xor(b)  // a = 00000110（bit 1, 2）
a.Not()   // a = 11111001
```

### 4.5 比较与序列化

#### Equals(other)

逐 word 比较两个位图是否完全相同：

```go
a.Equals(b)  // 长度相同且所有 word 相等时返回 true
```

#### ToBytes()

将位图序列化为字节切片，使用 word-at-a-time 转换。与 `NewBitmapFromBytes()` 互为逆操作：

```go
// 序列化 → 反序列化 往返
data := bm.ToBytes()
bm2 := common.NewBitmapFromBytes(data)
bm.Equals(bm2)  // true
```

### 4.6 复制操作

#### Clone()

创建位图的深拷贝：

```go
copy := bm.Clone()
copy.Set(0)       // 不影响原始 bm
```

#### CopyFrom(src, srcStart, count)

从源位图复制指定范围的位到当前位图的起始位置：

```go
src := common.NewBitmap(200)
src.Set(64); src.Set(65); src.Set(66)

dst := common.NewBitmap(100)
dst.CopyFrom(src, 64, 3)  // 复制 src[64..66] 到 dst[0..2]
dst.Get(0)  // true
dst.Get(1)  // true
dst.Get(2)  // true
```

**性能优化**：CopyFrom 按 word 批量拷贝，比逐位 Get/Set 快约 **64 倍**。核心优化策略：

1. **对齐快速路径**：当源起始位对齐到 word 边界（`srcBitOff == 0`）时，直接拷贝整个 word
2. **跨 word 拼接**：未对齐时，通过移位和或运算从两个相邻 word 拼出目标 word
3. **尾部截断**：最后一轮使用掩码只更新有效位，保留目标 word 中超出范围的原值

```
对齐情况 (srcBitOff = 0):
src: [word0][word1][word2]...
dst: [word0][word1]...         ← 直接拷贝

未对齐情况 (srcBitOff = 3):
src: [word0][word1][word2]...
         ↑ srcStart
dst word0 = src.word0 >> 3 | src.word1 << 61
dst word1 = src.word1 >> 3 | src.word2 << 61
```

### 4.7 扩展与遍历

#### Grow(newLen)

将位图扩展到新长度，保留已有位的值：

```go
bm := common.NewBitmap(64)
bm.Set(0)
bm.Grow(128)   // 扩展到 128 位，第 0 位仍为 1
```

**性能优化**：直接拷贝底层 word 数组，比 NewBitmap + 逐位复制快约 **64 倍**。

#### ForEach(fn)

遍历所有为 1 的位，调用回调函数：

```go
bm := common.NewBitmap(200)
bm.Set(3); bm.Set(7); bm.Set(100)
bm.ForEach(func(idx uint32) {
    fmt.Println(idx)  // 输出: 3, 7, 100
})
```

**性能优化**：使用 `bits.TrailingZeros64` 快速定位非零位，跳过零位。时间复杂度为 **O(popcount)** 而非 O(n)：

```
传统遍历:  对每个 bit 检查 → O(n)
优化遍历:  只访问非零位   → O(popcount)

示例: word = 0b000100010
  bits.TrailingZeros64 → 4  (第一个 1 的位置)
  清除 bit 4 后 → 0b000000010
  bits.TrailingZeros64 → 1  (下一个 1 的位置)
  清除 bit 1 后 → 0
  共 2 次迭代，而非 64 次
```

### 4.8 Bitmap 性能优化总结

| 优化点 | 技术手段 | 加速比 |
|--------|----------|--------|
| 序列化/反序列化 | word-at-a-time 转换 | ~8x |
| 批量复制 | CopyFrom 按 word 拷贝 | ~64x |
| 扩展 | Grow 直接拷贝 word 数组 | ~64x |
| 遍历 | TrailingZeros64 跳零 | O(popcount) vs O(n) |
| 位运算 | 按 word 批量操作 | ~64x |

**核心思想**：所有操作都尽量以 word（64 bit）为单位处理，而非逐 bit 操作。这利用了 CPU 64 位寄存器的天然优势，一次操作处理 64 个布尔值。

## 5. 内存池详解

### 5.1 BufferPool 结构

BufferPool 基于 `sync.Pool` 实现字节切片的复用，减少频繁分配带来的 GC 压力：

```go
type BufferPool struct {
    pool sync.Pool
}
```

**默认配置**：

| 参数 | 值 | 说明 |
|------|----|------|
| 默认缓冲区容量 | 4096 字节 | 新建切片的初始容量 |
| 全局实例 | `defaultPool` | 通过 `GetDefaultBufferPool()` 获取 |

### 5.2 方法说明

#### Get()

从池中获取一个字节切片，长度为 0，容量通常为 4096：

```go
pool := common.GetDefaultBufferPool()
buf := pool.Get()   // len=0, cap=4096
// 使用 buf...
pool.Put(buf)       // 归还
```

#### Put(b)

将字节切片放回池中。注意：归还后不可再使用该切片：

```go
pool.Put(buf)
buf = nil  // 避免误用
```

> **安全性**：`Put()` 会检查 `cap(b) > 0`，防止零容量切片进入池中。

#### GetSize(size)

获取指定容量的缓冲区。若池中切片容量不足，则新建：

```go
buf := pool.GetSize(8192)  // 确保 cap >= 8192
// 若池中切片 cap < 8192，新建 make([]byte, 0, 8192)
```

### 5.3 使用场景

BufferPool 主要用于存储引擎的编码/解码过程，避免频繁分配临时字节切片：

```
写入路径:  Value → 编码 → []byte (从池获取) → 压缩 → 写入文件 → 归还
读取路径:  读取文件 → 解压 → []byte (从池获取) → 解码 → Value → 归还
```

### 5.4 注意事项

1. **生命周期**：从 `Get()` 到 `Put()` 之间，切片归调用者所有；`Put()` 后必须停止使用
2. **容量保留**：`Get()` 返回的切片 `len=0` 但保留容量，可直接 `append` 使用
3. **大小不敏感**：`sync.Pool` 不按大小分类，大尺寸切片归还后可能被小请求复用（浪费内存），或被 GC 回收（下次需重新分配）。对大小敏感的场景可考虑多级池
4. **并发安全**：`sync.Pool` 本身并发安全，无需额外加锁
