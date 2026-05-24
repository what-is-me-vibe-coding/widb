# Common 模块详细设计

## 1. 职责

提供全项目共享的基础类型、工具函数、内存池、错误处理、日志封装。确保各模块间数据表示一致，减少重复代码。

## 2. 外部依赖

| 依赖 | 来源 | 用途 |
|------|------|------|
| `github.com/cespare/xxhash/v2` | 第三方 | 高性能哈希（BlockCache 键哈希、字典编码哈希） |
| `golang.org/x/exp/constraints` | 第三方 | 泛型约束（若需泛型工具函数） |

## 3. 核心内容

### 3.1 错误处理

```go
var (
    ErrKeyNotFound     = errors.New("key not found")
    ErrTableNotExist   = errors.New("table does not exist")
    ErrColumnNotExist  = errors.New("column does not exist")
    ErrTypeMismatch    = errors.New("type mismatch")
    ErrCorruptedData   = errors.New("corrupted data")
)
```

- 所有模块统一使用预定义错误，便于调用方判断。
- 内部错误包装：`fmt.Errorf("storage: read segment %d: %w", segID, err)`。

### 3.2 日志

```go
type Logger interface {
    Debug(msg string, keysAndValues ...interface{})
    Info(msg string, keysAndValues ...interface{})
    Warn(msg string, keysAndValues ...interface{})
    Error(msg string, keysAndValues ...interface{})
}
```

- 默认实现基于标准库 `log/slog`（Go 1.21+）。
- 结构化日志，键值对形式，便于后续接入 ELK/Loki。

### 3.3 内存池

```go
type BufferPool struct {
    pool sync.Pool
}

func (p *BufferPool) Get(size int) []byte
func (p *BufferPool) Put(b []byte)
```

- 用于 WAL 记录、压缩/解压缓冲区、网络 Packet 的复用。
- 按大小分级（如 1KB, 4KB, 16KB, 64KB），减少 GC 压力。

### 3.4 Bitmap

```go
type Bitmap struct {
    bits []uint64
    len  uint32
}

func (b *Bitmap) Set(i uint32)
func (b *Bitmap) Clear(i uint32)
func (b *Bitmap) Get(i uint32) bool
func (b *Bitmap) Count() uint32
```

- 用于 ColumnVector 的 Null 标识、BOOL 列存储、Filter 算子的 SelectionVector。
- 操作按 uint64 字对齐，批量处理时效率最高。

### 3.5 编码工具

```go
func EncodeVarint(dst []byte, v uint64) []byte
func DecodeVarint(src []byte) (uint64, int)
func EncodeUint32Slice(dst []byte, vals []uint32) []byte
```

- Varint 用于字符串偏移、行号等变长整数。
- Uint32 数组编码用于字典索引、RLE Count 等场景。

## 4. 接口定义

```go
type Pool interface {
    Get(size int) []byte
    Put([]byte)
}
```
