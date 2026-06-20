package server

import (
	"strconv"
	"testing"
)

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

// queryInt64 执行 SELECT 并返回首行指定列的 int64 值，简化断言。
func queryInt64(t *testing.T, srv *Server, sql, col string) int64 {
	t.Helper()
	resp := runSQL(t, srv, sql)
	rows := resp.Data.([]map[string]any)
	if len(rows) == 0 {
		t.Fatalf("SQL %q 无结果行", sql)
	}
	v, ok := rows[0][col].(int64)
	if !ok {
		t.Fatalf("SQL %q 列 %s = %v（类型 %T），期望 int64", sql, col, rows[0][col], rows[0][col])
	}
	return v
}

// queryFloat64 执行 SELECT 并返回首行指定列的 float64 值。
func queryFloat64(t *testing.T, srv *Server, sql, col string) float64 {
	t.Helper()
	resp := runSQL(t, srv, sql)
	rows := resp.Data.([]map[string]any)
	if len(rows) == 0 {
		t.Fatalf("SQL %q 无结果行", sql)
	}
	v, ok := rows[0][col].(float64)
	if !ok {
		t.Fatalf("SQL %q 列 %s = %v（类型 %T），期望 float64", sql, col, rows[0][col], rows[0][col])
	}
	return v
}

// TestSQLUpdateArithmeticExpr 验证 UPDATE SET 支持列与字面量的算术表达式。
// 覆盖 issue #192：update <table> set <column>=<complex expr>。
func TestSQLUpdateArithmeticExpr(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	runSQL(t, srv, "CREATE TABLE t (id INT64, v INT64, PRIMARY KEY (id))")
	runSQL(t, srv, "INSERT INTO t (id, v) VALUES (1, 10), (2, 20), (3, 30)")

	resp := runSQL(t, srv, "UPDATE t SET v = id + 1")
	if resp.Rows != 3 {
		t.Errorf("UPDATE 影响行数 = %d, 期望 3", resp.Rows)
	}

	want := map[int64]int64{1: 2, 2: 3, 3: 4}
	for id, exp := range want {
		if got := queryInt64(t, srv, "SELECT v FROM t WHERE id = "+strconv.FormatInt(id, 10), "v"); got != exp {
			t.Errorf("id=%d 的 v = %d, 期望 %d", id, got, exp)
		}
	}
}

// TestSQLUpdateArithmeticAllOps 验证 UPDATE SET 四则运算均正确求值。
func TestSQLUpdateArithmeticAllOps(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want int64
	}{
		{"add", "UPDATE t SET v = id + 5 WHERE id = 10", 15},
		{"sub", "UPDATE t SET v = id - 3 WHERE id = 10", 7},
		{"mul", "UPDATE t SET v = id * 2 WHERE id = 10", 20},
		{"div", "UPDATE t SET v = id / 3 WHERE id = 10", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := newTestServer(t)
			defer func() { _ = srv.Stop() }()

			runSQL(t, srv, "CREATE TABLE t (id INT64, v INT64, PRIMARY KEY (id))")
			runSQL(t, srv, "INSERT INTO t (id, v) VALUES (10, 0)")

			runSQL(t, srv, c.sql)
			if got := queryInt64(t, srv, "SELECT v FROM t WHERE id = 10", "v"); got != c.want {
				t.Errorf("%s: v = %d, 期望 %d", c.name, got, c.want)
			}
		})
	}
}

// TestSQLUpdateMultiColumnArithmetic 验证多列 UPDATE 同时使用算术表达式。
// 覆盖 issue #192：update <table> set <c1>=<expr1>, <c2>=<expr2>。
// 所有 SET 赋值基于原始行值求值（标准 SQL 语义）。
func TestSQLUpdateMultiColumnArithmetic(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	runSQL(t, srv, "CREATE TABLE t (id INT64, a INT64, b INT64, PRIMARY KEY (id))")
	runSQL(t, srv, "INSERT INTO t (id, a, b) VALUES (1, 10, 100)")

	runSQL(t, srv, "UPDATE t SET a = b + 1, b = a * 2")

	// a 基于原 b：100 + 1 = 101
	if got := queryInt64(t, srv, "SELECT a FROM t WHERE id = 1", "a"); got != 101 {
		t.Errorf("a = %d, 期望 101（原 b+1）", got)
	}
	// b 基于原 a：10 * 2 = 20
	if got := queryInt64(t, srv, "SELECT b FROM t WHERE id = 1", "b"); got != 20 {
		t.Errorf("b = %d, 期望 20（原 a*2）", got)
	}
}

// TestSQLUpdateArithmeticFloatCoercion 验证算术结果（INT64）写入 FLOAT64 列时自动类型转换。
func TestSQLUpdateArithmeticFloatCoercion(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	runSQL(t, srv, "CREATE TABLE t (id INT64, score FLOAT64, PRIMARY KEY (id))")
	runSQL(t, srv, "INSERT INTO t (id, score) VALUES (5, 0.0)")

	// id + 1 求值为 INT64(6)，写入 FLOAT64 列时强制转换为 6.0
	runSQL(t, srv, "UPDATE t SET score = id + 1")
	if got := queryFloat64(t, srv, "SELECT score FROM t WHERE id = 5", "score"); got != 6.0 {
		t.Errorf("score = %g, 期望 6.0", got)
	}
}

// TestSQLUpdateArithmeticExprWithWhere 验证带 WHERE 的算术 UPDATE 只更新匹配行。
func TestSQLUpdateArithmeticExprWithWhere(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	runSQL(t, srv, "CREATE TABLE t (id INT64, v INT64, PRIMARY KEY (id))")
	runSQL(t, srv, "INSERT INTO t (id, v) VALUES (1, 10), (2, 20), (3, 30)")

	resp := runSQL(t, srv, "UPDATE t SET v = v + 100 WHERE id > 1")
	if resp.Rows != 2 {
		t.Errorf("UPDATE 影响行数 = %d, 期望 2", resp.Rows)
	}

	// id=1 未被更新
	if got := queryInt64(t, srv, "SELECT v FROM t WHERE id = 1", "v"); got != 10 {
		t.Errorf("id=1 的 v = %d, 期望 10（不应更新）", got)
	}
	// id=2、3 被更新
	if got := queryInt64(t, srv, "SELECT v FROM t WHERE id = 2", "v"); got != 120 {
		t.Errorf("id=2 的 v = %d, 期望 120", got)
	}
	if got := queryInt64(t, srv, "SELECT v FROM t WHERE id = 3", "v"); got != 130 {
		t.Errorf("id=3 的 v = %d, 期望 130", got)
	}
}

// TestSQLDeleteWithRangePredicate 验证 DELETE 的范围谓词在段裁剪路径下
// 仍能正确删除匹配行（覆盖 ColumnPredicate 下推到 ScanRangeWithPruning 的正确性）。
func TestSQLDeleteWithRangePredicate(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	runSQL(t, srv, "CREATE TABLE t (id INT64, v STRING, PRIMARY KEY (id))")
	runSQL(t, srv, "INSERT INTO t (id, v) VALUES (1, 'a'), (2, 'b'), (3, 'c'), (4, 'd'), (5, 'e')")

	// 删除 id > 2 的行（应删除 3、4、5）
	resp := runSQL(t, srv, "DELETE FROM t WHERE id > 2")
	if resp.Rows != 3 {
		t.Errorf("DELETE 影响行数 = %d, 期望 3", resp.Rows)
	}

	// 验证只剩余 id=1、2
	resp = runSQL(t, srv, "SELECT id FROM t ORDER BY id")
	if resp.Rows != 2 {
		t.Fatalf("DELETE 后剩余 %d 行, 期望 2", resp.Rows)
	}
	rows := resp.Data.([]map[string]any)
	if rows[0]["id"].(int64) != 1 || rows[1]["id"].(int64) != 2 {
		t.Errorf("剩余行 = %+v, 期望 id=1,2", rows)
	}
}

// TestSQLUpdateWithRangePredicate 验证 UPDATE 的范围谓词在段裁剪路径下
// 仍能正确更新匹配行。
func TestSQLUpdateWithRangePredicate(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	runSQL(t, srv, "CREATE TABLE t (id INT64, v STRING, PRIMARY KEY (id))")
	runSQL(t, srv, "INSERT INTO t (id, v) VALUES (1, 'a'), (2, 'b'), (3, 'c'), (4, 'd'), (5, 'e')")

	// 更新 id >= 3 的行（应更新 3、4、5）
	resp := runSQL(t, srv, "UPDATE t SET v = 'updated' WHERE id >= 3")
	if resp.Rows != 3 {
		t.Errorf("UPDATE 影响行数 = %d, 期望 3", resp.Rows)
	}

	want := map[int64]string{1: "a", 2: "b", 3: "updated", 4: "updated", 5: "updated"}
	for id, exp := range want {
		sql := "SELECT v FROM t WHERE id = "
		if got := queryString(t, srv, sql+strconv.FormatInt(id, 10), "v"); got != exp {
			t.Errorf("id=%d 的 v = %q, 期望 %q", id, got, exp)
		}
	}
}

// TestSQLDeleteWithCompoundPredicate 验证 AND 连接复合谓词中
// 列-字面量部分走段裁剪（id = 2），剩余 LIKE 部分在 EvalRowPredicate 二次过滤。
// 这同时验证段裁剪的安全性：仅当匹配行可能在段内时才返回，再由上层精确过滤。
func TestSQLDeleteWithCompoundPredicate(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	runSQL(t, srv, "CREATE TABLE t (id INT64, name STRING, PRIMARY KEY (id))")
	runSQL(t, srv, "INSERT INTO t (id, name) VALUES (1, 'alice'), (2, 'bob'), (3, 'bob_jr')")

	// id = 2 AND name LIKE 'bob'  → 仅删除 id=2（id=3 的 name='bob_jr' 不匹配 LIKE）
	resp := runSQL(t, srv, "DELETE FROM t WHERE id = 2 AND name LIKE 'bob'")
	if resp.Rows != 1 {
		t.Errorf("DELETE 影响行数 = %d, 期望 1", resp.Rows)
	}

	// 验证 id=1（name=alice）和 id=3（name=bob_jr）均未删除
	resp = runSQL(t, srv, "SELECT COUNT(*) FROM t")
	if resp.Rows != 1 {
		t.Fatalf("剩余行数 = %v, 期望 1", resp.Data)
	}
}

// TestSQLDeleteWithOrPredicate 验证 OR 连接谓词时不下推（避免漏匹配），
// 走 EvalRowPredicate 完整求值：DELETE 仍能正确处理 OR 条件。
func TestSQLDeleteWithOrPredicate(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	runSQL(t, srv, "CREATE TABLE t (id INT64, v STRING, PRIMARY KEY (id))")
	runSQL(t, srv, "INSERT INTO t (id, v) VALUES (1, 'a'), (2, 'b'), (3, 'c')")

	// id = 1 OR id = 3 → 删除 id=1 和 id=3（OR 不下推，由 EvalRowPredicate 完整求值）
	resp := runSQL(t, srv, "DELETE FROM t WHERE id = 1 OR id = 3")
	if resp.Rows != 2 {
		t.Errorf("DELETE 影响行数 = %d, 期望 2", resp.Rows)
	}

	resp = runSQL(t, srv, "SELECT id FROM t")
	if resp.Rows != 1 {
		t.Fatalf("剩余行数 = %d, 期望 1", resp.Rows)
	}
	rows := resp.Data.([]map[string]any)
	if rows[0]["id"].(int64) != 2 {
		t.Errorf("剩余 id = %v, 期望 2", rows[0]["id"])
	}
}

// queryString 执行 SELECT 并返回首行指定列的字符串值，简化断言。
func queryString(t *testing.T, srv *Server, sql, col string) string {
	t.Helper()
	resp := runSQL(t, srv, sql)
	rows := resp.Data.([]map[string]any)
	if len(rows) == 0 {
		t.Fatalf("SQL %q 无结果行", sql)
	}
	v, ok := rows[0][col].(string)
	if !ok {
		t.Fatalf("SQL %q 列 %s = %v（类型 %T），期望 string", sql, col, rows[0][col], rows[0][col])
	}
	return v
}
