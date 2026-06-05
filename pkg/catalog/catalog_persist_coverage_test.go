package catalog

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestSaveToFile_MkdirAllError(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("permission-based test not reliable on Windows")
	}
	dir := t.TempDir()
	// Create a regular file where a directory would need to be created.
	// MkdirAll will fail because the path component is a file, not a directory.
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	path := filepath.Join(blocker, "sub", testCatalogFile)
	db := NewDatabase()
	err := saveToFile(path, db)
	if err == nil {
		t.Error("saveToFile should return error when directory creation fails")
	}
}

func TestSaveToFile_RenameError(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("rename-based test not reliable on Windows")
	}
	dir := t.TempDir()
	// Create the target file as a directory to make Rename fail.
	path := filepath.Join(dir, testCatalogFile)
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	db := NewDatabase()
	err := saveToFile(path, db)
	if err == nil {
		t.Error("saveToFile should return error when rename fails")
	}

	// Clean up the .tmp file left behind.
	tmpPath := path + ".tmp"
	_ = os.Remove(tmpPath)
}

func TestLoadFromFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, testCatalogFile)
	if err := os.WriteFile(path, []byte{}, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	db, err := loadFromFile(path)
	if err != nil {
		t.Fatalf("loadFromFile() error = %v", err)
	}
	if db.Version != 1 {
		t.Errorf("version = %d, want 1", db.Version)
	}
	if len(db.Tables) != 0 {
		t.Errorf("tables count = %d, want 0", len(db.Tables))
	}
}

func TestLoadFromFile_CorruptedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, testCatalogFile)
	if err := os.WriteFile(path, []byte("{invalid json!!!"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := loadFromFile(path)
	if err == nil {
		t.Error("loadFromFile should return error for corrupted JSON")
	}
}

func TestLoadFromFile_NilTablesMap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, testCatalogFile)
	// Write JSON with a null Tables map — after unmarshal, db.Tables will be nil
	// and loadFromFile should initialize it.
	content := `{"Version":5,"Tables":null,"CreatedAt":"2025-01-01T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	db, err := loadFromFile(path)
	if err != nil {
		t.Fatalf("loadFromFile() error = %v", err)
	}
	if db.Tables == nil {
		t.Error("Tables should be initialized, got nil")
	}
	if len(db.Tables) != 0 {
		t.Errorf("tables count = %d, want 0", len(db.Tables))
	}
	if db.Version != 5 {
		t.Errorf("version = %d, want 5", db.Version)
	}
}

func buildMultiTableDB() *Database {
	db := NewDatabase()
	db.Tables[tableUsers] = &Table{
		Name:       tableUsers,
		Columns:    []ColumnDef{{Name: "id", Type: common.TypeInt64}, {Name: colName, Type: common.TypeString}},
		PrimaryKey: []string{"id"},
		SegmentList: []SegmentRef{
			{ID: 1, Level: 0, MinKey: "a", MaxKey: "z", Size: 1024, RowCount: 50},
			{ID: 2, Level: 1, MinKey: "b", MaxKey: "y", Size: 2048, RowCount: 100},
		},
		Options: TableOptions{MaxSegmentSize: 4096},
		Version: 1,
	}
	db.Tables[testTableOrders] = &Table{
		Name:       testTableOrders,
		Columns:    []ColumnDef{{Name: testColOrderID, Type: common.TypeInt64}, {Name: "user_id", Type: common.TypeInt64}},
		PrimaryKey: []string{testColOrderID},
		SegmentList: []SegmentRef{
			{ID: 10, Level: 0, MinKey: "1", MaxKey: "999", Size: 512, RowCount: 20},
		},
		Options: TableOptions{},
		Version: 1,
	}
	db.Version = 3
	return db
}

func TestSaveAndLoad_RoundTripMultipleTables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, testCatalogFile)
	db := buildMultiTableDB()

	if err := saveToFile(path, db); err != nil {
		t.Fatalf("saveToFile() error = %v", err)
	}
	loaded, err := loadFromFile(path)
	if err != nil {
		t.Fatalf("loadFromFile() error = %v", err)
	}

	if loaded.Version != 3 {
		t.Errorf("version = %d, want 3", loaded.Version)
	}
	if len(loaded.Tables) != 2 {
		t.Fatalf("tables count = %d, want 2", len(loaded.Tables))
	}
}

func TestSaveAndLoad_UsersTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, testCatalogFile)
	db := buildMultiTableDB()

	if err := saveToFile(path, db); err != nil {
		t.Fatalf("saveToFile: %v", err)
	}
	loaded, err := loadFromFile(path)
	if err != nil {
		t.Fatalf("loadFromFile: %v", err)
	}

	users := loaded.Tables[tableUsers]
	if users == nil {
		t.Fatal("users table not found")
	}
	if users.Name != tableUsers {
		t.Errorf("Name = %q, want %q", users.Name, tableUsers)
	}
	if len(users.Columns) != 2 {
		t.Errorf("columns = %d, want 2", len(users.Columns))
	}
	if len(users.PrimaryKey) != 1 || users.PrimaryKey[0] != "id" {
		t.Errorf("primary key = %v, want [id]", users.PrimaryKey)
	}
	if len(users.SegmentList) != 2 {
		t.Fatalf("segments = %d, want 2", len(users.SegmentList))
	}
	if users.SegmentList[0].ID != 1 || users.SegmentList[1].ID != 2 {
		t.Errorf("segment IDs = %d,%d, want 1,2", users.SegmentList[0].ID, users.SegmentList[1].ID)
	}
	if users.Options.MaxSegmentSize != 4096 {
		t.Errorf("MaxSegmentSize = %d, want 4096", users.Options.MaxSegmentSize)
	}
}

func TestSaveAndLoad_OrdersTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, testCatalogFile)
	db := buildMultiTableDB()

	if err := saveToFile(path, db); err != nil {
		t.Fatalf("saveToFile: %v", err)
	}
	loaded, err := loadFromFile(path)
	if err != nil {
		t.Fatalf("loadFromFile: %v", err)
	}

	orders := loaded.Tables[testTableOrders]
	if orders == nil {
		t.Fatal("orders table not found")
	}
	if len(orders.Columns) != 2 {
		t.Errorf("columns = %d, want 2", len(orders.Columns))
	}
	if len(orders.SegmentList) != 1 {
		t.Errorf("segments = %d, want 1", len(orders.SegmentList))
	}
	if orders.SegmentList[0].ID != 10 {
		t.Errorf("segment ID = %d, want 10", orders.SegmentList[0].ID)
	}
}
