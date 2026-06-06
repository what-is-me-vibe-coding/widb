package catalog

import (
	"fmt"
	"sync"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// Catalog 管理数据库元数据，提供原子性的 Schema 变更与持久化。
type Catalog struct {
	mu   sync.RWMutex
	db   *Database
	path string // 持久化文件路径，空则不持久化
}

// NewCatalog 创建一个新的 Catalog 实例。
func NewCatalog(path string) *Catalog {
	return &Catalog{
		db:   NewDatabase(),
		path: path,
	}
}

// LoadCatalog 从文件加载 Catalog，若文件不存在则创建新的。
func LoadCatalog(path string) (*Catalog, error) {
	c := NewCatalog(path)
	if path == "" {
		return c, nil
	}
	db, err := loadFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("load catalog: %w", err)
	}
	c.db = db
	return c, nil
}

// CreateTable 创建新表。
func (c *Catalog) CreateTable(name string, columns []ColumnDef, primaryKey []string, opts TableOptions) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.db.Tables[name]; ok {
		return fmt.Errorf("table %q already exists", name)
	}
	if len(columns) == 0 {
		return fmt.Errorf("table must have at least one column: %w", common.ErrInvalidSchema)
	}
	if len(primaryKey) == 0 {
		return fmt.Errorf("table must have a primary key: %w", common.ErrInvalidSchema)
	}
	// 验证主键列存在
	for _, pk := range primaryKey {
		found := false
		for _, col := range columns {
			if col.Name == pk {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("primary key column %q not found in table columns: %w", pk, common.ErrInvalidSchema)
		}
	}

	tbl := &Table{
		Name:        name,
		Columns:     columns,
		PrimaryKey:  primaryKey,
		SegmentList: make([]SegmentRef, 0),
		Options:     opts,
		Version:     1,
		CreatedAt:   time.Now(),
	}
	c.db.Tables[name] = tbl
	c.db.Version++
	return c.persist()
}

// DropTable 删除表。
func (c *Catalog) DropTable(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.db.Tables[name]; !ok {
		return common.ErrTableNotExist
	}
	delete(c.db.Tables, name)
	c.db.Version++
	return c.persist()
}

// AddColumn 向表中添加列。新列默认 Nullable=true。
func (c *Catalog) AddColumn(table, column string, def ColumnDef) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	tbl, ok := c.db.Tables[table]
	if !ok {
		return common.ErrTableNotExist
	}
	if tbl.HasColumn(column) {
		return fmt.Errorf("column %q already exists in table %q", column, table)
	}
	// 新列默认 Nullable
	def.Nullable = true
	tbl.Columns = append(tbl.Columns, def)
	tbl.Version++
	c.db.Version++
	return c.persist()
}

// DropColumn 从表中删除列。
func (c *Catalog) DropColumn(table, column string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	tbl, ok := c.db.Tables[table]
	if !ok {
		return common.ErrTableNotExist
	}
	idx, err := tbl.ColumnIndex(column)
	if err != nil {
		return err
	}
	// 不允许删除主键列
	for _, pk := range tbl.PrimaryKey {
		if pk == column {
			return fmt.Errorf("cannot drop primary key column %q", column)
		}
	}
	tbl.Columns = append(tbl.Columns[:idx], tbl.Columns[idx+1:]...)
	tbl.Version++
	c.db.Version++
	return c.persist()
}

// RegisterSegment 向表中注册一个 Segment。
func (c *Catalog) RegisterSegment(table string, seg SegmentRef) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	tbl, ok := c.db.Tables[table]
	if !ok {
		return common.ErrTableNotExist
	}
	// 检查是否已存在同 ID 的 Segment
	for _, s := range tbl.SegmentList {
		if s.ID == seg.ID {
			return fmt.Errorf("segment %d already registered in table %q", seg.ID, table)
		}
	}
	tbl.SegmentList = append(tbl.SegmentList, seg)
	c.db.Version++
	return c.persist()
}

// UnregisterSegment 从表中移除一个 Segment。
func (c *Catalog) UnregisterSegment(table string, segID uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	tbl, ok := c.db.Tables[table]
	if !ok {
		return common.ErrTableNotExist
	}
	for i, s := range tbl.SegmentList {
		if s.ID == segID {
			tbl.SegmentList = append(tbl.SegmentList[:i], tbl.SegmentList[i+1:]...)
			c.db.Version++
			return c.persist()
		}
	}
	return fmt.Errorf("segment %d not found in table %q", segID, table)
}

// GetTable 按名称获取表定义（返回深拷贝，避免外部修改内部状态）。
func (c *Catalog) GetTable(name string) (*Table, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	tbl, ok := c.db.Tables[name]
	if !ok {
		return nil, common.ErrTableNotExist
	}
	// 深拷贝以防止外部修改内部状态
	tblCopy := *tbl
	tblCopy.Columns = make([]ColumnDef, len(tbl.Columns))
	copy(tblCopy.Columns, tbl.Columns)
	tblCopy.PrimaryKey = make([]string, len(tbl.PrimaryKey))
	copy(tblCopy.PrimaryKey, tbl.PrimaryKey)
	tblCopy.SegmentList = make([]SegmentRef, len(tbl.SegmentList))
	copy(tblCopy.SegmentList, tbl.SegmentList)
	tblCopy.colTypeMap = nil // 延迟重建
	return &tblCopy, nil
}

// Snapshot 返回当前 Database 的一致快照。
func (c *Catalog) Snapshot() *Database {
	c.mu.RLock()
	defer c.mu.RUnlock()

	snap := &Database{
		Version:   c.db.Version,
		Tables:    make(map[string]*Table, len(c.db.Tables)),
		CreatedAt: c.db.CreatedAt,
	}
	for name, tbl := range c.db.Tables {
		tblCopy := *tbl
		tblCopy.Columns = make([]ColumnDef, len(tbl.Columns))
		copy(tblCopy.Columns, tbl.Columns)
		tblCopy.PrimaryKey = make([]string, len(tbl.PrimaryKey))
		copy(tblCopy.PrimaryKey, tbl.PrimaryKey)
		tblCopy.SegmentList = make([]SegmentRef, len(tbl.SegmentList))
		copy(tblCopy.SegmentList, tbl.SegmentList)
		snap.Tables[name] = &tblCopy
	}
	return snap
}

// Version 返回当前 Catalog 版本号。
func (c *Catalog) Version() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.db.Version
}

// persist 持久化 Catalog 到文件。调用方需持有写锁。
func (c *Catalog) persist() error {
	if c.path == "" {
		return nil
	}
	return saveToFile(c.path, c.db)
}
