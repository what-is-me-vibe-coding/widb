package catalog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// 测试中使用的常量名
const (
	colEmail    = "email"
	colCol1     = "col1"
	tableT1     = "t1"
	tableNotExt = "notexist"
)

// createTestTable 是辅助函数，创建测试用的 users 表。
func createTestTable(c *Catalog, t *testing.T) {
	t.Helper()
	err := c.CreateTable(tableUsers, []ColumnDef{
		{Name: "id", Type: common.TypeInt64},
	}, []string{"id"}, TableOptions{})
	if err != nil {
		t.Fatalf("CreateTable() error = %v", err)
	}
}

// createTestTableWithColumns 是辅助函数，创建带多列的测试表。
func createTestTableWithColumns(c *Catalog, t *testing.T) {
	t.Helper()
	err := c.CreateTable(tableUsers, []ColumnDef{
		{Name: "id", Type: common.TypeInt64},
		{Name: colName, Type: common.TypeString},
	}, []string{"id"}, TableOptions{})
	if err != nil {
		t.Fatalf("CreateTable() error = %v", err)
	}
}

// ---- Catalog CRUD 测试 ----

func TestCatalogCreateTable(t *testing.T) {
	c := NewCatalog("")
	err := c.CreateTable(tableUsers, []ColumnDef{
		{Name: "id", Type: common.TypeInt64},
		{Name: colName, Type: common.TypeString},
	}, []string{"id"}, TableOptions{})
	if err != nil {
		t.Fatalf("CreateTable() error = %v", err)
	}
	tbl, err := c.GetTable(tableUsers)
	if err != nil {
		t.Fatalf("GetTable() error = %v", err)
	}
	if tbl.Name != tableUsers {
		t.Errorf("table name = %q, want %q", tbl.Name, tableUsers)
	}
	if len(tbl.Columns) != 2 {
		t.Errorf("columns count = %d, want 2", len(tbl.Columns))
	}
	if len(tbl.PrimaryKey) != 1 || tbl.PrimaryKey[0] != "id" {
		t.Errorf("primary key = %v, want [id]", tbl.PrimaryKey)
	}
}

func TestCatalogCreateTableDuplicate(t *testing.T) {
	c := NewCatalog("")
	err := c.CreateTable(tableUsers, []ColumnDef{
		{Name: "id", Type: common.TypeInt64},
	}, []string{"id"}, TableOptions{})
	if err != nil {
		t.Fatalf("first CreateTable() error = %v", err)
	}
	err = c.CreateTable(tableUsers, []ColumnDef{
		{Name: "id", Type: common.TypeInt64},
	}, []string{"id"}, TableOptions{})
	if err == nil {
		t.Error("duplicate CreateTable() should return error")
	}
}

func TestCatalogCreateTableNoColumns(t *testing.T) {
	c := NewCatalog("")
	err := c.CreateTable("t", []ColumnDef{}, []string{"id"}, TableOptions{})
	if err == nil {
		t.Error("CreateTable with no columns should return error")
	}
}

func TestCatalogCreateTableNoPrimaryKey(t *testing.T) {
	c := NewCatalog("")
	err := c.CreateTable("t", []ColumnDef{
		{Name: "id", Type: common.TypeInt64},
	}, nil, TableOptions{})
	if err == nil {
		t.Error("CreateTable with no primary key should return error")
	}
}

func TestCatalogCreateTableInvalidPrimaryKey(t *testing.T) {
	c := NewCatalog("")
	err := c.CreateTable("t", []ColumnDef{
		{Name: "id", Type: common.TypeInt64},
	}, []string{"missing_col"}, TableOptions{})
	if err == nil {
		t.Error("CreateTable with invalid primary key column should return error")
	}
}

func TestCatalogDropTable(t *testing.T) {
	c := NewCatalog("")
	createTestTable(c, t)

	err := c.DropTable(tableUsers)
	if err != nil {
		t.Fatalf("DropTable() error = %v", err)
	}
	_, err = c.GetTable(tableUsers)
	if err != common.ErrTableNotExist {
		t.Errorf("GetTable after drop = %v, want ErrTableNotExist", err)
	}
}

func TestCatalogDropTableNotExist(t *testing.T) {
	c := NewCatalog("")
	err := c.DropTable(tableNotExt)
	if err != common.ErrTableNotExist {
		t.Errorf("DropTable(notexist) = %v, want ErrTableNotExist", err)
	}
}

func TestCatalogAddColumn(t *testing.T) {
	c := NewCatalog("")
	createTestTable(c, t)

	err := c.AddColumn(tableUsers, colEmail, ColumnDef{
		Name: colEmail, Type: common.TypeString,
	})
	if err != nil {
		t.Fatalf("AddColumn() error = %v", err)
	}
	tbl, err := c.GetTable(tableUsers)
	if err != nil {
		t.Fatalf("GetTable() error = %v", err)
	}
	if !tbl.HasColumn(colEmail) {
		t.Error("table should have email column after AddColumn")
	}
	col, err := tbl.GetColumn(colEmail)
	if err != nil {
		t.Fatalf("GetColumn() error = %v", err)
	}
	if !col.Nullable {
		t.Error("new column should be nullable by default")
	}
}

func TestCatalogAddColumnTableNotExist(t *testing.T) {
	c := NewCatalog("")
	err := c.AddColumn(tableNotExt, "col", ColumnDef{Name: "col", Type: common.TypeInt64})
	if err != common.ErrTableNotExist {
		t.Errorf("AddColumn on non-existent table = %v, want ErrTableNotExist", err)
	}
}

func TestCatalogAddColumnDuplicate(t *testing.T) {
	c := NewCatalog("")
	createTestTable(c, t)

	err := c.AddColumn(tableUsers, "id", ColumnDef{Name: "id", Type: common.TypeInt64})
	if err == nil {
		t.Error("AddColumn duplicate should return error")
	}
}

func TestCatalogDropColumn(t *testing.T) {
	c := NewCatalog("")
	createTestTableWithColumns(c, t)

	err := c.DropColumn(tableUsers, colName)
	if err != nil {
		t.Fatalf("DropColumn() error = %v", err)
	}
	tbl, err := c.GetTable(tableUsers)
	if err != nil {
		t.Fatalf("GetTable() error = %v", err)
	}
	if tbl.HasColumn(colName) {
		t.Error("table should not have name column after DropColumn")
	}
}

func TestCatalogDropColumnPrimaryKey(t *testing.T) {
	c := NewCatalog("")
	createTestTable(c, t)

	err := c.DropColumn(tableUsers, "id")
	if err == nil {
		t.Error("DropColumn on primary key should return error")
	}
}

func TestCatalogDropColumnNotExist(t *testing.T) {
	c := NewCatalog("")
	createTestTable(c, t)

	err := c.DropColumn(tableUsers, tableNotExt)
	if err != common.ErrColumnNotExist {
		t.Errorf("DropColumn(notexist) = %v, want ErrColumnNotExist", err)
	}
}

// ---- Segment 管理测试 ----

func TestCatalogRegisterSegment(t *testing.T) {
	c := NewCatalog("")
	createTestTable(c, t)

	seg := SegmentRef{ID: 1, Level: 0, MinKey: "a", MaxKey: "z", Size: 1024, RowCount: 100}
	err := c.RegisterSegment(tableUsers, seg)
	if err != nil {
		t.Fatalf("RegisterSegment() error = %v", err)
	}
	tbl, err := c.GetTable(tableUsers)
	if err != nil {
		t.Fatalf("GetTable() error = %v", err)
	}
	if len(tbl.SegmentList) != 1 {
		t.Errorf("segment count = %d, want 1", len(tbl.SegmentList))
	}
	if tbl.SegmentList[0].ID != 1 {
		t.Errorf("segment ID = %d, want 1", tbl.SegmentList[0].ID)
	}
}

func TestCatalogRegisterSegmentDuplicate(t *testing.T) {
	c := NewCatalog("")
	createTestTable(c, t)

	seg := SegmentRef{ID: 1, Level: 0, MinKey: "a", MaxKey: "z"}
	err := c.RegisterSegment(tableUsers, seg)
	if err != nil {
		t.Fatalf("first RegisterSegment() error = %v", err)
	}
	err = c.RegisterSegment(tableUsers, seg)
	if err == nil {
		t.Error("RegisterSegment duplicate should return error")
	}
}

func TestCatalogUnregisterSegment(t *testing.T) {
	c := NewCatalog("")
	createTestTable(c, t)

	err := c.RegisterSegment(tableUsers, SegmentRef{ID: 1, Level: 0, MinKey: "a", MaxKey: "z"})
	if err != nil {
		t.Fatalf("RegisterSegment(1) error = %v", err)
	}
	err = c.RegisterSegment(tableUsers, SegmentRef{ID: 2, Level: 0, MinKey: "b", MaxKey: "y"})
	if err != nil {
		t.Fatalf("RegisterSegment(2) error = %v", err)
	}

	err = c.UnregisterSegment(tableUsers, 1)
	if err != nil {
		t.Fatalf("UnregisterSegment() error = %v", err)
	}
	tbl, err := c.GetTable(tableUsers)
	if err != nil {
		t.Fatalf("GetTable() error = %v", err)
	}
	if len(tbl.SegmentList) != 1 {
		t.Errorf("segment count = %d, want 1", len(tbl.SegmentList))
	}
	if tbl.SegmentList[0].ID != 2 {
		t.Errorf("remaining segment ID = %d, want 2", tbl.SegmentList[0].ID)
	}
}

func TestCatalogUnregisterSegmentNotFound(t *testing.T) {
	c := NewCatalog("")
	createTestTable(c, t)

	err := c.UnregisterSegment(tableUsers, 999)
	if err == nil {
		t.Error("UnregisterSegment with non-existent ID should return error")
	}
}

// ---- Snapshot 测试 ----

func TestCatalogSnapshot(t *testing.T) {
	c := NewCatalog("")
	createTestTableWithColumns(c, t)

	snap := c.Snapshot()
	if snap.Version != c.Version() {
		t.Errorf("snapshot version = %d, want %d", snap.Version, c.Version())
	}
	if len(snap.Tables) != 1 {
		t.Errorf("snapshot tables count = %d, want 1", len(snap.Tables))
	}
	// 修改快照不应影响原始 Catalog
	delete(snap.Tables, tableUsers)
	_, err := c.GetTable(tableUsers)
	if err != nil {
		t.Error("modifying snapshot should not affect original catalog")
	}
}

// ---- LoadCatalog 测试 ----

func TestLoadCatalog_EmptyPath(t *testing.T) {
	// 空路径应返回新的空 Catalog
	c, err := LoadCatalog("")
	if err != nil {
		t.Fatalf("LoadCatalog(空路径) 不应返回错误: %v", err)
	}
	if c == nil {
		t.Fatal("LoadCatalog(空路径) 返回 nil")
	}
	if c.Version() != 1 {
		t.Errorf("version = %d, 期望 1", c.Version())
	}
	if len(c.Snapshot().Tables) != 0 {
		t.Errorf("tables 数量 = %d, 期望 0", len(c.Snapshot().Tables))
	}
}

func TestLoadCatalog_ValidFilePath(t *testing.T) {
	// 先创建并持久化一个 Catalog，然后通过 LoadCatalog 加载
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")

	c1 := NewCatalog(path)
	err := c1.CreateTable(tableUsers, []ColumnDef{
		{Name: "id", Type: common.TypeInt64},
		{Name: colName, Type: common.TypeString},
	}, []string{"id"}, TableOptions{})
	if err != nil {
		t.Fatalf("CreateTable 失败: %v", err)
	}

	// 从文件加载
	c2, err := LoadCatalog(path)
	if err != nil {
		t.Fatalf("LoadCatalog(有效路径) 不应返回错误: %v", err)
	}
	tbl, err := c2.GetTable(tableUsers)
	if err != nil {
		t.Fatalf("GetTable 失败: %v", err)
	}
	if tbl.Name != tableUsers {
		t.Errorf("表名 = %q, 期望 %q", tbl.Name, tableUsers)
	}
	if len(tbl.Columns) != 2 {
		t.Errorf("列数 = %d, 期望 2", len(tbl.Columns))
	}
}

func TestLoadCatalog_CorruptedFile(t *testing.T) {
	// 损坏的 JSON 文件应导致 LoadCatalog 返回错误
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(path, []byte("{invalid json!!!"), 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	_, err := LoadCatalog(path)
	if err == nil {
		t.Error("LoadCatalog(损坏文件) 应返回错误")
	}
}

// ---- 版本号递增测试 ----

func TestCatalogVersionIncrement(t *testing.T) {
	c := NewCatalog("")
	initial := c.Version()

	err := c.CreateTable(tableT1, []ColumnDef{{Name: "id", Type: common.TypeInt64}}, []string{"id"}, TableOptions{})
	if err != nil {
		t.Fatalf("CreateTable() error = %v", err)
	}
	if c.Version() <= initial {
		t.Error("version should increment after CreateTable")
	}

	v := c.Version()
	err = c.AddColumn(tableT1, colCol1, ColumnDef{Name: colCol1, Type: common.TypeString})
	if err != nil {
		t.Fatalf("AddColumn() error = %v", err)
	}
	if c.Version() <= v {
		t.Error("version should increment after AddColumn")
	}

	v = c.Version()
	err = c.DropColumn(tableT1, colCol1)
	if err != nil {
		t.Fatalf("DropColumn() error = %v", err)
	}
	if c.Version() <= v {
		t.Error("version should increment after DropColumn")
	}

	v = c.Version()
	err = c.RegisterSegment(tableT1, SegmentRef{ID: 1, Level: 0, MinKey: "a", MaxKey: "z"})
	if err != nil {
		t.Fatalf("RegisterSegment() error = %v", err)
	}
	if c.Version() <= v {
		t.Error("version should increment after RegisterSegment")
	}

	v = c.Version()
	err = c.UnregisterSegment(tableT1, 1)
	if err != nil {
		t.Fatalf("UnregisterSegment() error = %v", err)
	}
	if c.Version() <= v {
		t.Error("version should increment after UnregisterSegment")
	}

	v = c.Version()
	err = c.DropTable(tableT1)
	if err != nil {
		t.Fatalf("DropTable() error = %v", err)
	}
	if c.Version() <= v {
		t.Error("version should increment after DropTable")
	}
}
