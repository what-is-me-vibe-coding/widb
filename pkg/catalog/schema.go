package catalog

import (
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ColumnDef 定义表的列结构。
type ColumnDef struct {
	Name     string
	Type     common.DataType
	Nullable bool
	Default  common.Value
}

// TableOptions 存储表的配置选项。
type TableOptions struct {
	MaxSegmentSize  int64  // 单个 Segment 的最大字节数
	MaxMemTableSize int64  // MemTable 刷盘阈值
	Engine          string // 存储引擎类型："lsm"（默认）或 "memory"
}

// SegmentRef 引用一个已持久化的 Segment。
type SegmentRef struct {
	ID       uint64
	Level    uint8
	MinKey   string
	MaxKey   string
	Size     int64
	RowCount uint32
}

// Table 定义一张宽表的结构。
type Table struct {
	Name        string
	Columns     []ColumnDef
	PrimaryKey  []string     // 支持复合主键
	SegmentList []SegmentRef // 当前表的所有 Segment
	Options     TableOptions
	Version     uint64 // 表结构版本号
	CreatedAt   time.Time
	Engine      string // 存储引擎类型："lsm"（默认）或 "memory"，由 TableOptions 传播

	// colTypeMap 是列名到数据类型的缓存映射，延迟初始化。
	// 避免每次写入请求都重建 map，减少热点路径上的内存分配。
	colTypeMap map[string]common.DataType
}

// ColTypeMap 返回列名到数据类型的映射，延迟初始化并缓存。
func (t *Table) ColTypeMap() map[string]common.DataType {
	if t.colTypeMap == nil {
		m := make(map[string]common.DataType, len(t.Columns))
		for _, col := range t.Columns {
			m[col.Name] = col.Type
		}
		t.colTypeMap = m
	}
	return t.colTypeMap
}

// Database 是 Catalog 的顶层结构，包含所有表定义。
type Database struct {
	Version   uint64
	Tables    map[string]*Table
	CreatedAt time.Time
}

// NewDatabase 创建一个新的 Database 实例。
func NewDatabase() *Database {
	return &Database{
		Version:   1,
		Tables:    make(map[string]*Table),
		CreatedAt: time.Now(),
	}
}

// GetTable 按名称获取表定义。
func (db *Database) GetTable(name string) (*Table, error) {
	t, ok := db.Tables[name]
	if !ok {
		return nil, common.ErrTableNotExist
	}
	return t, nil
}

// ColumnIndex 返回指定列在表定义中的索引位置。
func (t *Table) ColumnIndex(name string) (int, error) {
	for i, col := range t.Columns {
		if col.Name == name {
			return i, nil
		}
	}
	return -1, common.ErrColumnNotExist
}

// GetColumn 按名称获取列定义。
func (t *Table) GetColumn(name string) (*ColumnDef, error) {
	idx, err := t.ColumnIndex(name)
	if err != nil {
		return nil, err
	}
	return &t.Columns[idx], nil
}

// HasColumn 判断表是否包含指定列。
func (t *Table) HasColumn(name string) bool {
	_, err := t.ColumnIndex(name)
	return err == nil
}
