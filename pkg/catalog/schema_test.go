package catalog

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestNewDatabase(t *testing.T) {
	db := NewDatabase()
	if db == nil {
		t.Fatal("NewDatabase() returned nil")
	}
	if db.Version != 1 {
		t.Errorf("Version = %d, want 1", db.Version)
	}
	if db.Tables == nil {
		t.Error("Tables map is nil")
	}
	if len(db.Tables) != 0 {
		t.Errorf("len(Tables) = %d, want 0", len(db.Tables))
	}
}

func TestDatabaseGetTable(t *testing.T) {
	db := NewDatabase()
	db.Tables["users"] = &Table{Name: "users"}

	_, err := db.GetTable("users")
	if err != nil {
		t.Errorf("GetTable(users) error = %v", err)
	}

	_, err = db.GetTable("notexist")
	if err != common.ErrTableNotExist {
		t.Errorf("GetTable(notexist) error = %v, want ErrTableNotExist", err)
	}
}

func TestTableColumnIndex(t *testing.T) {
	tbl := &Table{
		Name: "users",
		Columns: []ColumnDef{
			{Name: "id", Type: common.TypeInt64},
			{Name: "name", Type: common.TypeString},
			{Name: "age", Type: common.TypeInt64},
		},
	}

	idx, err := tbl.ColumnIndex("name")
	if err != nil || idx != 1 {
		t.Errorf("ColumnIndex(name) = %d, %v, want 1, nil", idx, err)
	}

	_, err = tbl.ColumnIndex("notexist")
	if err != common.ErrColumnNotExist {
		t.Errorf("ColumnIndex(notexist) error = %v, want ErrColumnNotExist", err)
	}
}

func TestTableGetColumn(t *testing.T) {
	tbl := &Table{
		Name: "users",
		Columns: []ColumnDef{
			{Name: "id", Type: common.TypeInt64},
			{Name: "name", Type: common.TypeString},
		},
	}

	col, err := tbl.GetColumn("name")
	if err != nil || col.Name != "name" || col.Type != common.TypeString {
		t.Errorf("GetColumn(name) = %+v, %v", col, err)
	}

	_, err = tbl.GetColumn("notexist")
	if err != common.ErrColumnNotExist {
		t.Errorf("GetColumn(notexist) error = %v, want ErrColumnNotExist", err)
	}
}

func TestTableHasColumn(t *testing.T) {
	tbl := &Table{
		Name: "users",
		Columns: []ColumnDef{
			{Name: "id", Type: common.TypeInt64},
		},
	}

	if !tbl.HasColumn("id") {
		t.Error("HasColumn(id) = false, want true")
	}
	if tbl.HasColumn("notexist") {
		t.Error("HasColumn(notexist) = true, want false")
	}
}
