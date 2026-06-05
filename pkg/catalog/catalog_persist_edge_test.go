package catalog

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const (
	osWindows       = "windows"
	testTableOrders = "orders"
	testTableT1     = "t1"
	testTableT2     = "t2"
	testColOrderID  = "order_id"
	testColAmount   = "amount"
	testCatalogFile = "catalog.json"
)

// TestSaveToFileReadOnlyDir 测试保存到只读目录时的错误路径
func TestSaveToFileReadOnlyDir(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("权限测试在 Windows 上不可靠")
	}
	if os.Getuid() == 0 {
		t.Skip("root 用户绕过文件权限检查")
	}

	dir := t.TempDir()
	readonlyDir := filepath.Join(dir, "readonly")
	if err := os.Mkdir(readonlyDir, 0555); err != nil {
		t.Fatalf("Mkdir 失败: %v", err)
	}
	defer func() { _ = os.Chmod(readonlyDir, 0755) }() // 恢复权限以便清理

	path := filepath.Join(readonlyDir, "sub", testCatalogFile)
	db := NewDatabase()
	err := saveToFile(path, db)
	if err == nil {
		t.Error("saveToFile 应在只读目录下返回错误")
	}
}

// TestSaveToFileWriteToReadOnlyDir 测试写入只读目录时的错误路径
// saveToFile 使用原子 rename 模式（先写临时文件再 rename），只读文件权限不会阻止 rename 替换文件，
// 因此改为测试只读目录场景，确保 MkdirAll 或临时文件写入失败。
func TestSaveToFileWriteToReadOnlyDir(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("权限测试在 Windows 上不可靠")
	}
	if os.Getuid() == 0 {
		t.Skip("root 用户绕过文件权限检查")
	}

	dir := t.TempDir()
	readonlyDir := filepath.Join(dir, "readonly")
	if err := os.Mkdir(readonlyDir, 0555); err != nil {
		t.Fatalf("Mkdir 失败: %v", err)
	}
	defer func() { _ = os.Chmod(readonlyDir, 0755) }()

	path := filepath.Join(readonlyDir, "sub", testCatalogFile)
	db := NewDatabase()
	db.Tables[testTableT1] = &Table{
		Name:       testTableT1,
		Columns:    []ColumnDef{{Name: "id", Type: common.TypeInt64}},
		PrimaryKey: []string{"id"},
		Version:    1,
	}

	err := saveToFile(path, db)
	if err == nil {
		t.Error("saveToFile 应在只读目录下返回错误")
	}
}

// TestLoadFromFilePermissionDenied 测试从无权限文件加载时的错误路径
func TestLoadFromFilePermissionDenied(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("权限测试在 Windows 上不可靠")
	}
	if os.Getuid() == 0 {
		t.Skip("root 用户绕过文件权限检查")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, testCatalogFile)

	// 创建文件并设为无读权限
	if err := os.WriteFile(path, []byte(`{"Version":1,"Tables":{}}`), 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatalf("Chmod 失败: %v", err)
	}
	defer func() { _ = os.Chmod(path, 0644) }() // 恢复权限以便清理

	_, err := loadFromFile(path)
	if err == nil {
		t.Error("loadFromFile 应在无权限文件下返回错误")
	}
}

// TestSaveToFileAndLoadRoundTrip 测试完整的保存和加载往返
func TestSaveToFileAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, testCatalogFile)

	db := NewDatabase()
	db.Version = 5
	db.Tables[tableUsers] = &Table{
		Name:       tableUsers,
		Columns:    []ColumnDef{{Name: "id", Type: common.TypeInt64}, {Name: colName, Type: common.TypeString}},
		PrimaryKey: []string{"id"},
		SegmentList: []SegmentRef{
			{ID: 1, Level: 0, MinKey: "a", MaxKey: "z", Size: 1024, RowCount: 50},
		},
		Options: TableOptions{MaxSegmentSize: 4096},
		Version: 2,
	}

	if err := saveToFile(path, db); err != nil {
		t.Fatalf("saveToFile 失败: %v", err)
	}

	loaded, err := loadFromFile(path)
	if err != nil {
		t.Fatalf("loadFromFile 失败: %v", err)
	}

	if loaded.Version != 5 {
		t.Errorf("版本: 期望 5，得到 %d", loaded.Version)
	}
	if len(loaded.Tables) != 1 {
		t.Fatalf("表数量: 期望 1，得到 %d", len(loaded.Tables))
	}
	users := loaded.Tables[tableUsers]
	if users == nil {
		t.Fatal("users 表未找到")
	}
	if users.Version != 2 {
		t.Errorf("users 版本: 期望 2，得到 %d", users.Version)
	}
	if len(users.Columns) != 2 {
		t.Errorf("列数量: 期望 2，得到 %d", len(users.Columns))
	}
	if len(users.SegmentList) != 1 {
		t.Errorf("Segment 数量: 期望 1，得到 %d", len(users.SegmentList))
	}
}

// TestLoadFromFileWithValidData 测试加载有效的 JSON 数据
func TestLoadFromFileWithValidData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, testCatalogFile)

	content := `{
  "Version": 3,
  "Tables": {
    "orders": {
      "Name": "orders",
      "Columns": [
        {"Name": "order_id", "Type": 1, "Nullable": false},
        {"Name": "amount", "Type": 3, "Nullable": true}
      ],
      "PrimaryKey": ["order_id"],
      "SegmentList": [],
      "Options": {"MaxSegmentSize": 8192},
      "Version": 1
    }
  }
}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	db, err := loadFromFile(path)
	if err != nil {
		t.Fatalf("loadFromFile 失败: %v", err)
	}

	if db.Version != 3 {
		t.Errorf("版本: 期望 3，得到 %d", db.Version)
	}
	orders, ok := db.Tables[testTableOrders]
	if !ok {
		t.Fatal("orders 表未找到")
	}
	if len(orders.Columns) != 2 {
		t.Errorf("列数量: 期望 2，得到 %d", len(orders.Columns))
	}
}

// TestSaveToFileWithEmptyDatabase 测试保存空数据库
func TestSaveToFileWithEmptyDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, testCatalogFile)

	db := NewDatabase()
	if err := saveToFile(path, db); err != nil {
		t.Fatalf("saveToFile 空数据库失败: %v", err)
	}

	loaded, err := loadFromFile(path)
	if err != nil {
		t.Fatalf("loadFromFile 失败: %v", err)
	}

	if loaded.Version != 1 {
		t.Errorf("版本: 期望 1，得到 %d", loaded.Version)
	}
	if len(loaded.Tables) != 0 {
		t.Errorf("表数量: 期望 0，得到 %d", len(loaded.Tables))
	}
}

// TestSaveToFileOverwrite 测试覆盖已有文件
func TestSaveToFileOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, testCatalogFile)

	// 第一次保存
	db1 := NewDatabase()
	db1.Tables[testTableT1] = &Table{
		Name:       testTableT1,
		Columns:    []ColumnDef{{Name: "id", Type: common.TypeInt64}},
		PrimaryKey: []string{"id"},
		Version:    1,
	}
	db1.Version = 2
	if err := saveToFile(path, db1); err != nil {
		t.Fatalf("第一次 saveToFile 失败: %v", err)
	}

	// 第二次保存（覆盖）
	db2 := NewDatabase()
	db2.Tables[testTableT2] = &Table{
		Name:       testTableT2,
		Columns:    []ColumnDef{{Name: "id", Type: common.TypeInt64}},
		PrimaryKey: []string{"id"},
		Version:    1,
	}
	db2.Version = 3
	if err := saveToFile(path, db2); err != nil {
		t.Fatalf("第二次 saveToFile 失败: %v", err)
	}

	// 验证覆盖后的数据
	loaded, err := loadFromFile(path)
	if err != nil {
		t.Fatalf("loadFromFile 失败: %v", err)
	}
	if _, ok := loaded.Tables[testTableT1]; ok {
		t.Error("t1 表不应存在（已被覆盖）")
	}
	if _, ok := loaded.Tables[testTableT2]; !ok {
		t.Error("t2 表应存在")
	}
}

// TestLoadFromFileTruncatedJSON 测试加载截断的 JSON 文件
func TestLoadFromFileTruncatedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, testCatalogFile)

	// 写入截断的 JSON
	if err := os.WriteFile(path, []byte(`{"Version": 1, "Tables":`), 0644); err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}

	_, err := loadFromFile(path)
	if err == nil {
		t.Error("loadFromFile 应对截断的 JSON 返回错误")
	}
}
