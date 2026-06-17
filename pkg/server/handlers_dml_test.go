package server

import "testing"

// runSQL 是 handleQuery 的测试辅助函数，失败时 fatal 并返回响应。
func runSQL(t *testing.T, srv *Server, sql string) *Response {
	t.Helper()
	resp, err := srv.handleQuery(&QueryRequest{SQL: sql})
	if err != nil {
		t.Fatalf("执行 SQL %q 出错: %v", sql, err)
	}
	if resp.Code != 0 {
		t.Fatalf("执行 SQL %q 失败: %s", sql, resp.Message)
	}
	return resp
}

// failSQL 断言 SQL 执行应失败（返回非 0 错误码）。
func failSQL(t *testing.T, srv *Server, sql string) {
	t.Helper()
	resp, err := srv.handleQuery(&QueryRequest{SQL: sql})
	if err != nil {
		t.Fatalf("执行 SQL %q 出错: %v", sql, err)
	}
	if resp.Code == 0 {
		t.Errorf("SQL %q 应失败，但返回成功", sql)
	}
}

// TestSQLInsertPKConflict 验证 INSERT 主键冲突时拒绝写入。
func TestSQLInsertPKConflict(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	runSQL(t, srv, "CREATE TABLE t (id INT64, v STRING, PRIMARY KEY (id))")
	runSQL(t, srv, "INSERT INTO t (id, v) VALUES (1, 'a')")
	// 重复主键应失败
	failSQL(t, srv, "INSERT INTO t (id, v) VALUES (1, 'b')")

	// 原数据未被覆盖
	resp := runSQL(t, srv, "SELECT id, v FROM t WHERE id = 1")
	rows := resp.Data.([]map[string]any)
	if len(rows) != 1 {
		t.Fatalf("SELECT 行数 = %d, 期望 1", len(rows))
	}
	if rows[0]["v"] != "a" {
		t.Errorf("v = %v, 期望 'a'（不应被覆盖）", rows[0]["v"])
	}
}

// TestSQLUpdateWithWhere 验证 UPDATE 带 WHERE 子句更新匹配行。
func TestSQLUpdateWithWhere(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	runSQL(t, srv, "CREATE TABLE t (id INT64, v STRING, PRIMARY KEY (id))")
	runSQL(t, srv, "INSERT INTO t (id, v) VALUES (1, 'a'), (2, 'b'), (3, 'c')")

	resp := runSQL(t, srv, "UPDATE t SET v = 'updated' WHERE id = 2")
	if resp.Rows != 1 {
		t.Errorf("UPDATE 影响行数 = %d, 期望 1", resp.Rows)
	}

	// 验证只有 id=2 被更新
	resp = runSQL(t, srv, "SELECT id, v FROM t WHERE id = 2")
	rows := resp.Data.([]map[string]any)
	if rows[0]["v"] != "updated" {
		t.Errorf("id=2 的 v = %v, 期望 'updated'", rows[0]["v"])
	}

	resp = runSQL(t, srv, "SELECT id, v FROM t WHERE id = 1")
	rows = resp.Data.([]map[string]any)
	if rows[0]["v"] != "a" {
		t.Errorf("id=1 的 v = %v, 期望 'a'（不应被更新）", rows[0]["v"])
	}
}

// TestSQLUpdateAllRows 验证无 WHERE 的 UPDATE 更新全表。
func TestSQLUpdateAllRows(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	runSQL(t, srv, "CREATE TABLE t (id INT64, v INT64, PRIMARY KEY (id))")
	runSQL(t, srv, "INSERT INTO t (id, v) VALUES (1, 10), (2, 20), (3, 30)")

	resp := runSQL(t, srv, "UPDATE t SET v = 0")
	if resp.Rows != 3 {
		t.Errorf("UPDATE 影响行数 = %d, 期望 3", resp.Rows)
	}

	resp = runSQL(t, srv, "SELECT id, v FROM t")
	if resp.Rows != 3 {
		t.Fatalf("SELECT 行数 = %d, 期望 3", resp.Rows)
	}
	rows := resp.Data.([]map[string]any)
	for _, r := range rows {
		if r["v"].(int64) != 0 {
			t.Errorf("id=%v 的 v = %v, 期望 0", r["id"], r["v"])
		}
	}
}

// TestSQLUpdatePKConflict 验证 UPDATE 导致主键冲突时报错。
func TestSQLUpdatePKConflict(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	runSQL(t, srv, "CREATE TABLE t (id INT64, v STRING, PRIMARY KEY (id))")
	runSQL(t, srv, "INSERT INTO t (id, v) VALUES (1, 'a'), (2, 'b')")

	// 将 id=1 更新为 id=2，与已有行冲突
	failSQL(t, srv, "UPDATE t SET id = 2 WHERE id = 1")
}

// TestSQLDeleteWithWhere 验证 DELETE 带 WHERE 删除匹配行。
func TestSQLDeleteWithWhere(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	runSQL(t, srv, "CREATE TABLE t (id INT64, v STRING, PRIMARY KEY (id))")
	runSQL(t, srv, "INSERT INTO t (id, v) VALUES (1, 'a'), (2, 'b'), (3, 'c')")

	resp := runSQL(t, srv, "DELETE FROM t WHERE id = 2")
	if resp.Rows != 1 {
		t.Errorf("DELETE 影响行数 = %d, 期望 1", resp.Rows)
	}

	resp = runSQL(t, srv, "SELECT id, v FROM t")
	if resp.Rows != 2 {
		t.Fatalf("DELETE 后 SELECT 行数 = %d, 期望 2", resp.Rows)
	}
}

// TestSQLDeleteAll 验证无 WHERE 的 DELETE 清空全表。
func TestSQLDeleteAll(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	runSQL(t, srv, "CREATE TABLE t (id INT64, v STRING, PRIMARY KEY (id))")
	runSQL(t, srv, "INSERT INTO t (id, v) VALUES (1, 'a'), (2, 'b')")

	resp := runSQL(t, srv, "DELETE FROM t")
	if resp.Rows != 2 {
		t.Errorf("DELETE 影响行数 = %d, 期望 2", resp.Rows)
	}

	resp = runSQL(t, srv, "SELECT id, v FROM t")
	if resp.Rows != 0 {
		t.Errorf("DELETE 后 SELECT 行数 = %d, 期望 0", resp.Rows)
	}
}

// TestSQLDeleteTableNotExist 验证从不存在的表 DELETE 报错。
func TestSQLDeleteTableNotExist(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	failSQL(t, srv, "DELETE FROM nonexistent WHERE id = 1")
}

// TestSQLDropTable 验证 DROP TABLE 删除表后查询应失败。
func TestSQLDropTable(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	runSQL(t, srv, "CREATE TABLE t (id INT64 PRIMARY KEY)")
	runSQL(t, srv, "INSERT INTO t (id) VALUES (1)")
	runSQL(t, srv, "DROP TABLE t")

	// 删除后查询应失败
	failSQL(t, srv, "SELECT id FROM t")
}

// TestSQLDropTableIfExists 验证 DROP TABLE IF EXISTS 语义。
func TestSQLDropTableIfExists(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	// 不存在的表带 IF EXISTS 应成功
	runSQL(t, srv, "DROP TABLE IF EXISTS nonexistent")

	// 不存在的表不带 IF EXISTS 应失败
	failSQL(t, srv, "DROP TABLE nonexistent")
}

// TestSQLShowTables 验证 SHOW TABLES 返回所有表名。
func TestSQLShowTables(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	runSQL(t, srv, "CREATE TABLE t1 (id INT64 PRIMARY KEY)")
	runSQL(t, srv, "CREATE TABLE t2 (id INT64 PRIMARY KEY)")

	resp := runSQL(t, srv, "SHOW TABLES")
	rows := resp.Data.([]map[string]any)
	if len(rows) != 2 {
		t.Fatalf("SHOW TABLES 返回 %d 行, 期望 2", len(rows))
	}
	// 结果按名称排序
	if rows[0]["table"] != "t1" {
		t.Errorf("第一行 = %v, 期望 t1", rows[0]["table"])
	}
	if rows[1]["table"] != "t2" {
		t.Errorf("第二行 = %v, 期望 t2", rows[1]["table"])
	}
}

// TestSQLDescribe 验证 DESCRIBE 返回表结构信息。
func TestSQLDescribe(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	runSQL(t, srv, "CREATE TABLE t (id INT64, name STRING, PRIMARY KEY (id))")

	resp := runSQL(t, srv, "DESCRIBE t")
	rows := resp.Data.([]map[string]any)
	if len(rows) != 2 {
		t.Fatalf("DESCRIBE 返回 %d 行, 期望 2", len(rows))
	}

	// 验证第一列 id 为主键
	idRow := rows[0]
	if idRow["field"] != "id" {
		t.Errorf("第一行 field = %v, 期望 id", idRow["field"])
	}
	if idRow["key"] != true {
		t.Errorf("id 的 key = %v, 期望 true", idRow["key"])
	}

	// 验证第二列 name 非主键
	nameRow := rows[1]
	if nameRow["field"] != "name" {
		t.Errorf("第二行 field = %v, 期望 name", nameRow["field"])
	}
	if nameRow["key"] != false {
		t.Errorf("name 的 key = %v, 期望 false", nameRow["key"])
	}
}

// TestSQLDescribeTableNotExist 验证 DESCRIBE 不存在的表报错。
func TestSQLDescribeTableNotExist(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	failSQL(t, srv, "DESCRIBE nonexistent")
}

// TestSQLCRUDFlow 验证完整 CRUD 流程：建表→插入→查询→更新→删除。
func TestSQLCRUDFlow(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	runSQL(t, srv, "CREATE TABLE users (id INT64, name STRING, age INT64, PRIMARY KEY (id))")

	// Create
	runSQL(t, srv, "INSERT INTO users (id, name, age) VALUES (1, 'alice', 30)")
	runSQL(t, srv, "INSERT INTO users (id, name, age) VALUES (2, 'bob', 25)")

	// Read
	resp := runSQL(t, srv, "SELECT name, age FROM users WHERE id = 1")
	rows := resp.Data.([]map[string]any)
	if rows[0]["name"] != "alice" {
		t.Errorf("name = %v, 期望 alice", rows[0]["name"])
	}

	// Update
	runSQL(t, srv, "UPDATE users SET age = 31 WHERE id = 1")
	resp = runSQL(t, srv, "SELECT age FROM users WHERE id = 1")
	rows = resp.Data.([]map[string]any)
	if rows[0]["age"].(int64) != 31 {
		t.Errorf("age = %v, 期望 31", rows[0]["age"])
	}

	// Delete
	runSQL(t, srv, "DELETE FROM users WHERE id = 2")
	resp = runSQL(t, srv, "SELECT id FROM users")
	if resp.Rows != 1 {
		t.Errorf("DELETE 后剩余 %d 行, 期望 1", resp.Rows)
	}
}
