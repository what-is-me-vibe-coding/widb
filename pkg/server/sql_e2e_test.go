package server

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// TestSQLCreateInsertSelect 验证通过 SQL 完成 CREATE TABLE → INSERT → SELECT 全流程，
// 覆盖用户报告的"建表成功但查询报 table does not exist"问题。
func TestSQLCreateInsertSelect(t *testing.T) {
	srv := newTestServer(t)

	// 1. CREATE TABLE
	resp, err := srv.handleQuery(&QueryRequest{
		SQL: "CREATE TABLE sensor (id INT64, name STRING, temperature FLOAT64, active BOOL, PRIMARY KEY (id))",
	})
	if err != nil {
		t.Fatalf("CREATE TABLE 失败: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("CREATE TABLE 失败: %s", resp.Message)
	}

	// 2. INSERT
	resp, err = srv.handleQuery(&QueryRequest{
		SQL: "INSERT INTO sensor (id, name, temperature, active) VALUES (1, 'sensor-1', 23.5, true)",
	})
	if err != nil {
		t.Fatalf("INSERT 失败: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("INSERT 失败: %s", resp.Message)
	}
	if resp.Rows != 1 {
		t.Errorf("INSERT 影响行数 = %d, 期望 1", resp.Rows)
	}

	// 3. SELECT —— 此前会报 "table does not exist"
	resp, err = srv.handleQuery(&QueryRequest{
		SQL: "SELECT id, name, temperature FROM sensor WHERE id = 1",
	})
	if err != nil {
		t.Fatalf("SELECT 失败: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("SELECT 失败: %s", resp.Message)
	}
	if resp.Rows != 1 {
		t.Fatalf("SELECT 行数 = %d, 期望 1", resp.Rows)
	}

	// 验证返回数据
	data, ok := resp.Data.([]map[string]any)
	if !ok {
		t.Fatalf("SELECT 返回数据类型错误: %T", resp.Data)
	}
	if len(data) != 1 {
		t.Fatalf("SELECT 返回 %d 行, 期望 1", len(data))
	}
	row := data[0]
	if v, ok := row["id"].(int64); !ok || v != 1 {
		t.Errorf("id: 期望 int64(1), got %T(%v)", row["id"], row["id"])
	}
	if v, ok := row["name"].(string); !ok || v != "sensor-1" {
		t.Errorf("name: 期望 'sensor-1', got %T(%v)", row["name"], row["name"])
	}
	if v, ok := row["temperature"].(float64); !ok || v != 23.5 {
		t.Errorf("temperature: 期望 23.5, got %T(%v)", row["temperature"], row["temperature"])
	}
}

// TestSQLInsertMultipleRows 验证 INSERT 多行 + SELECT 全表扫描。
func TestSQLInsertMultipleRows(t *testing.T) {
	srv := newTestServer(t)

	resp, _ := srv.handleQuery(&QueryRequest{
		SQL: "CREATE TABLE t (id INT64, val STRING, PRIMARY KEY (id))",
	})
	if resp.Code != 0 {
		t.Fatalf("CREATE TABLE 失败: %s", resp.Message)
	}

	resp, _ = srv.handleQuery(&QueryRequest{
		SQL: "INSERT INTO t (id, val) VALUES (1, 'a'), (2, 'b'), (3, 'c')",
	})
	if resp.Code != 0 {
		t.Fatalf("INSERT 失败: %s", resp.Message)
	}
	if resp.Rows != 3 {
		t.Errorf("INSERT 影响行数 = %d, 期望 3", resp.Rows)
	}

	resp, _ = srv.handleQuery(&QueryRequest{
		SQL: "SELECT id, val FROM t",
	})
	if resp.Code != 0 {
		t.Fatalf("SELECT 失败: %s", resp.Message)
	}
	if resp.Rows != 3 {
		t.Errorf("SELECT 行数 = %d, 期望 3", resp.Rows)
	}
}

// TestSQLCreateTableIfNotExists 验证 IF NOT EXISTS 语义。
func TestSQLCreateTableIfNotExists(t *testing.T) {
	srv := newTestServer(t)

	resp, _ := srv.handleQuery(&QueryRequest{
		SQL: "CREATE TABLE t (id INT64 PRIMARY KEY)",
	})
	if resp.Code != 0 {
		t.Fatalf("第一次 CREATE TABLE 失败: %s", resp.Message)
	}

	// 不带 IF NOT EXISTS 重复建表应失败
	resp, _ = srv.handleQuery(&QueryRequest{
		SQL: "CREATE TABLE t (id INT64 PRIMARY KEY)",
	})
	if resp.Code == 0 {
		t.Error("重复建表应失败，但返回成功")
	}

	// 带 IF NOT EXISTS 重复建表应成功
	resp, _ = srv.handleQuery(&QueryRequest{
		SQL: "CREATE TABLE IF NOT EXISTS t (id INT64 PRIMARY KEY)",
	})
	if resp.Code != 0 {
		t.Errorf("CREATE TABLE IF NOT EXISTS 应成功: %s", resp.Message)
	}
}

// TestSQLInsertTableNotExist 验证向不存在的表 INSERT 报错。
func TestSQLInsertTableNotExist(t *testing.T) {
	srv := newTestServer(t)

	resp, _ := srv.handleQuery(&QueryRequest{
		SQL: "INSERT INTO nonexistent (id) VALUES (1)",
	})
	if resp.Code == 0 {
		t.Error("向不存在的表 INSERT 应失败")
	}
}

// TestSQLInsertMissingPrimaryKey 验证缺少主键列时报错。
func TestSQLInsertMissingPrimaryKey(t *testing.T) {
	srv := newTestServer(t)

	resp, _ := srv.handleQuery(&QueryRequest{
		SQL: "CREATE TABLE t (id INT64 PRIMARY KEY, val STRING)",
	})
	if resp.Code != 0 {
		t.Fatalf("CREATE TABLE 失败: %s", resp.Message)
	}

	resp, _ = srv.handleQuery(&QueryRequest{
		SQL: "INSERT INTO t (val) VALUES ('x')",
	})
	if resp.Code == 0 {
		t.Error("缺少主键列应失败")
	}
}

// TestSQLCatalogPersistence 验证重启后表定义可恢复。
func TestSQLCatalogPersistence(t *testing.T) {
	srv := newTestServer(t)
	dataDir := srv.cfg.DataDir

	resp, _ := srv.handleQuery(&QueryRequest{
		SQL: "CREATE TABLE persist_t (id INT64 PRIMARY KEY, name STRING)",
	})
	if resp.Code != 0 {
		t.Fatalf("CREATE TABLE 失败: %s", resp.Message)
	}

	resp, _ = srv.handleQuery(&QueryRequest{
		SQL: "INSERT INTO persist_t (id, name) VALUES (1, 'alice')",
	})
	if resp.Code != 0 {
		t.Fatalf("INSERT 失败: %s", resp.Message)
	}

	// 关闭旧服务器
	if err := srv.Stop(); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}

	// 用同一数据目录重建服务器
	srv2, err := NewServer(Config{
		TCPAddr:  testListenAddr,
		HTTPAddr: testListenAddr,
		DataDir:  dataDir,
	}, WithMetricsRegistry(prometheus.NewRegistry()))
	if err != nil {
		t.Fatalf("重启 NewServer 失败: %v", err)
	}
	defer srv2.Stop()

	// 重启后表应存在，且数据可查
	resp, _ = srv2.handleQuery(&QueryRequest{
		SQL: "SELECT id, name FROM persist_t WHERE id = 1",
	})
	if resp.Code != 0 {
		t.Fatalf("重启后 SELECT 失败: %s", resp.Message)
	}
	if resp.Rows != 1 {
		t.Errorf("重启后 SELECT 行数 = %d, 期望 1", resp.Rows)
	}
}
