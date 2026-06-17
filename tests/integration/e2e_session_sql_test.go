// 端到端集成测试：持久连接 SQL 会话场景。
//
// 与 e2e_server_sql_test.go 中「每次查询新建连接」的模式互补，本文件覆盖：
//   - 单条持久 TCP 连接上顺序执行通用 SQL（DDL/DML/DQL 全流程）
//   - 多客户端各自持有持久连接并发执行通用 SQL
//   - TIMESTAMP/DATE 类型经 /write 写入后经 SQL SELECT 读回
//
// 覆盖现有集成测试未涉及的连接复用、会话内多语句交错、全部比较运算符、
// UPDATE/DELETE/DROP TABLE/SHOW TABLES/DESCRIBE 等「一般 SQL」语义。
package integration

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

// dialTCPClient 拨号并返回持久 TCP 客户端，失败时终止测试。
func dialTCPClient(t *testing.T, s *sqlServer) *tcpClient {
	t.Helper()
	c, err := dialTCP(s.tcpAddr)
	if err != nil {
		t.Fatalf("拨号失败: %v", err)
	}
	return c
}

// execSQL 在持久连接上执行 SQL，返回响应与错误。
// 不调用 t.Fatal，便于在 goroutine 中使用。
func execSQL(c *tcpClient, sql string) (*server.Response, error) {
	resp, err := c.query(sql)
	if err != nil {
		return nil, fmt.Errorf("执行失败 [%s]: %w", sql, err)
	}
	if resp.Code != 0 {
		return resp, fmt.Errorf("SQL 错误 [%s]: %s", sql, resp.Message)
	}
	return resp, nil
}

// sessExec 在持久连接上执行 SQL，失败时终止测试。
func sessExec(t *testing.T, c *tcpClient, sql string) *server.Response {
	t.Helper()
	resp, err := execSQL(c, sql)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return resp
}

// sessRows 在持久连接上执行 SELECT 并返回结果行。
func sessRows(t *testing.T, c *tcpClient, sql string) []map[string]any {
	t.Helper()
	return respRows(sessExec(t, c, sql))
}

// sessExecErr 在持久连接上执行 SQL，期望返回非零错误码。
func sessExecErr(t *testing.T, c *tcpClient, sql string) {
	t.Helper()
	resp, err := c.query(sql)
	if err != nil {
		t.Fatalf("执行失败 [%s]: %v", sql, err)
	}
	if resp.Code == 0 {
		t.Fatalf("期望 SQL 返回错误 [%s]，但返回成功", sql)
	}
}

// sessCount 在持久连接上查询表的总行数。
func sessCount(t *testing.T, c *tcpClient, table string) int64 {
	t.Helper()
	rows := sessRows(t, c, fmt.Sprintf("SELECT COUNT(*) AS cnt FROM %s", table))
	if len(rows) != 1 {
		t.Fatalf("COUNT %s: 期望 1 行，得到 %d", table, len(rows))
	}
	cnt, _ := toInt64(rows[0]["cnt"])
	return cnt
}

// sessCreateTable 在持久连接上建表并插入 5 行事件数据。
func sessCreateTable(t *testing.T, c *tcpClient, table string) {
	t.Helper()
	ddl := fmt.Sprintf("CREATE TABLE %s (id BIGINT NOT NULL, name VARCHAR NULL, "+
		"value DOUBLE NULL, active BOOLEAN NULL, PRIMARY KEY(id))", table)
	sessExec(t, c, ddl)
	ins := fmt.Sprintf("INSERT INTO %s (id, name, value, active) VALUES "+
		"(1, 'alpha', 10.5, true), (2, 'beta', 20.0, false), "+
		"(3, 'alpha', 30.0, true), (4, 'gamma', 40.5, true), "+
		"(5, 'beta', 50.0, false)", table)
	if resp := sessExec(t, c, ins); resp.Rows != 5 {
		t.Fatalf("INSERT 行数: 期望 5，得到 %d", resp.Rows)
	}
}

// sessOperatorCase 描述一个 WHERE 子句及其期望行数。
type sessOperatorCase struct {
	where string
	want  int
}

// sessVerifyOperators 验证全部比较运算符、布尔过滤与逻辑组合。
func sessVerifyOperators(t *testing.T, c *tcpClient, table string) {
	t.Helper()
	cases := []sessOperatorCase{
		{"id = 3", 1}, {"id != 1", 4}, {"id > 3", 2}, {"id >= 4", 2},
		{"id < 3", 2}, {"id <= 2", 2},
		{"active = true", 3}, {"id > 1 AND active = true", 2},
		{"id = 1 OR id = 5", 2},
	}
	for _, tc := range cases {
		sql := fmt.Sprintf("SELECT * FROM %s WHERE %s", table, tc.where)
		if got := len(sessRows(t, c, sql)); got != tc.want {
			t.Errorf("WHERE %s: 期望 %d 行，得到 %d", tc.where, tc.want, got)
		}
	}
}

// sessVerifyAggregation 验证 GROUP BY 聚合结果。
func sessVerifyAggregation(t *testing.T, c *tcpClient, table string) {
	t.Helper()
	sql := fmt.Sprintf("SELECT name, COUNT(*) AS cnt, SUM(value) AS total "+
		"FROM %s GROUP BY name", table)
	rows := sessRows(t, c, sql)
	if len(rows) != 3 {
		t.Fatalf("GROUP BY 分组数: 期望 3，得到 %d", len(rows))
	}
	wantCnt := map[string]int64{"alpha": 2, "beta": 2, "gamma": 1}
	wantSum := map[string]float64{"alpha": 40.5, "beta": 70.0, "gamma": 40.5}
	for _, r := range rows {
		name, _ := r["name"].(string)
		if cnt, _ := toInt64(r["cnt"]); cnt != wantCnt[name] {
			t.Errorf("%s cnt: 期望 %d，得到 %d", name, wantCnt[name], cnt)
		}
		if total, _ := toFloat64(r["total"]); total != wantSum[name] {
			t.Errorf("%s total: 期望 %g，得到 %g", name, wantSum[name], total)
		}
	}
}

// sessVerifyUpdateDelete 验证 UPDATE 与 DELETE 的正确性。
func sessVerifyUpdateDelete(t *testing.T, c *tcpClient, table string) {
	t.Helper()
	resp := sessExec(t, c, fmt.Sprintf(
		"UPDATE %s SET value = 99.0 WHERE id = 1", table))
	if resp.Rows != 1 {
		t.Fatalf("UPDATE 行数: 期望 1，得到 %d", resp.Rows)
	}
	rows := sessRows(t, c, fmt.Sprintf("SELECT value FROM %s WHERE id = 1", table))
	if len(rows) != 1 {
		t.Fatalf("UPDATE 后查询: 期望 1 行，得到 %d", len(rows))
	}
	if val, _ := toFloat64(rows[0]["value"]); val != 99.0 {
		t.Errorf("UPDATE 后 value: 期望 99.0，得到 %g", val)
	}
	dresp := sessExec(t, c, fmt.Sprintf("DELETE FROM %s WHERE id = 2", table))
	if dresp.Rows != 1 {
		t.Fatalf("DELETE 行数: 期望 1，得到 %d", dresp.Rows)
	}
	if r := sessRows(t, c, fmt.Sprintf("SELECT * FROM %s WHERE id = 2", table)); len(r) != 0 {
		t.Errorf("DELETE 后仍能查到 id=2: %d 行", len(r))
	}
}

// sessVerifyMeta 验证 SHOW TABLES、DESCRIBE 与 DROP TABLE。
func sessVerifyMeta(t *testing.T, c *tcpClient, table string) {
	t.Helper()
	found := false
	for _, r := range sessRows(t, c, "SHOW TABLES") {
		if name, _ := r["table"].(string); name == table {
			found = true
		}
	}
	if !found {
		t.Errorf("SHOW TABLES 未包含 %s", table)
	}
	if rows := sessRows(t, c, fmt.Sprintf("DESCRIBE %s", table)); len(rows) != 4 {
		t.Fatalf("DESCRIBE %s: 期望 4 行，得到 %d", table, len(rows))
	}
	sessExec(t, c, fmt.Sprintf("DROP TABLE %s", table))
	sessExecErr(t, c, fmt.Sprintf("DESCRIBE %s", table))
}

// TestSQLSessionPersistentConnection 验证单条持久 TCP 连接上顺序执行通用 SQL。
//
// 全程复用同一条连接，覆盖 CREATE TABLE、INSERT、SELECT（含全部比较运算符/
// 布尔过滤/逻辑组合）、GROUP BY 聚合、UPDATE、DELETE、SHOW TABLES、
// DESCRIBE、DROP TABLE，验证连接生命周期与会话内多语句交错执行。
func TestSQLSessionPersistentConnection(t *testing.T) {
	s := startSQLServer(t)
	c := dialTCPClient(t, s)
	defer c.close()

	const table = "events"
	sessCreateTable(t, c, table)
	if got := sessCount(t, c, table); got != 5 {
		t.Fatalf("初始行数: 期望 5，得到 %d", got)
	}
	sessVerifyOperators(t, c, table)
	sessVerifyAggregation(t, c, table)
	sessVerifyUpdateDelete(t, c, table)
	if got := sessCount(t, c, table); got != 4 {
		t.Fatalf("UPDATE/DELETE 后行数: 期望 4，得到 %d", got)
	}
	sessVerifyMeta(t, c, table)
}

// runPersistentSession 单个客户端在一条持久 TCP 连接上执行完整 SQL 会话。
// 每个客户端写入互不冲突的 ID 区间，验证 INSERT/SELECT/UPDATE/DELETE
// 在并发持久连接下的正确性。返回错误而非调用 t.Fatal，便于在 goroutine 中使用。
func runPersistentSession(s *sqlServer, table string, clientID, n int) error {
	c, err := dialTCP(s.tcpAddr)
	if err != nil {
		return fmt.Errorf("客户端 %d 拨号失败: %w", clientID, err)
	}
	defer c.close()

	base := clientID*100 + 10
	for i := 0; i < n; i++ {
		id := base + i
		sql := fmt.Sprintf("INSERT INTO %s (id, name, value, active) VALUES "+
			"(%d, 'c%d', %g, %v)", table, id, clientID, float64(id), i%2 == 0)
		if _, err := execSQL(c, sql); err != nil {
			return fmt.Errorf("客户端 %d: %w", clientID, err)
		}
	}
	return persistentClientVerify(c, table, base, n)
}

// persistentClientVerify 在持久连接上验证点查、范围查、UPDATE、DELETE。
func persistentClientVerify(c *tcpClient, table string, base, n int) error {
	resp, err := execSQL(c, fmt.Sprintf("SELECT * FROM %s WHERE id = %d", table, base))
	if err != nil {
		return fmt.Errorf("点查: %w", err)
	}
	if len(respRows(resp)) != 1 {
		return fmt.Errorf("点查 id=%d: 期望 1 行，得到 %d", base, len(respRows(resp)))
	}
	resp, err = execSQL(c, fmt.Sprintf(
		"SELECT * FROM %s WHERE id >= %d AND id < %d", table, base, base+n))
	if err != nil {
		return fmt.Errorf("范围查: %w", err)
	}
	if len(respRows(resp)) != n {
		return fmt.Errorf("范围查: 期望 %d 行，得到 %d", n, len(respRows(resp)))
	}
	resp, err = execSQL(c, fmt.Sprintf(
		"UPDATE %s SET name = 'updated' WHERE id = %d", table, base))
	if err != nil {
		return fmt.Errorf("UPDATE: %w", err)
	}
	if resp.Rows != 1 {
		return fmt.Errorf("UPDATE 行数: 期望 1，得到 %d", resp.Rows)
	}
	resp, err = execSQL(c, fmt.Sprintf("DELETE FROM %s WHERE id = %d", table, base+1))
	if err != nil {
		return fmt.Errorf("DELETE: %w", err)
	}
	if resp.Rows != 1 {
		return fmt.Errorf("DELETE 行数: 期望 1，得到 %d", resp.Rows)
	}
	return nil
}

// TestMultiClientPersistentSessions 验证多客户端各自持有持久 TCP 连接，
// 并发执行通用 SQL（INSERT/SELECT/UPDATE/DELETE）的正确性。
//
// 启动一个 server，预先创建共享 LSM 表，多个客户端各自建立一条持久连接，
// 在互不冲突的 ID 区间上执行完整 SQL 会话，最终校验数据完整性。
func TestMultiClientPersistentSessions(t *testing.T) {
	s := startSQLServer(t)
	setupC := dialTCPClient(t, s)
	sessExec(t, setupC, "CREATE TABLE sess_events (id BIGINT NOT NULL, "+
		"name VARCHAR NULL, value DOUBLE NULL, active BOOLEAN NULL, PRIMARY KEY(id))")
	setupC.close()

	const numClients = 8
	const rowsPerClient = 6
	const table = "sess_events"

	var wg sync.WaitGroup
	var failCount int64
	var lastErr atomic.Value
	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			if err := runPersistentSession(s, table, clientID, rowsPerClient); err != nil {
				lastErr.Store(err.Error())
				atomic.AddInt64(&failCount, 1)
			}
		}(i)
	}
	wg.Wait()

	if failCount > 0 {
		t.Fatalf("%d 个客户端失败，最后错误: %v", failCount, lastErr.Load())
	}
	// 每客户端写入 rowsPerClient 行并删除 1 行，剩余 rowsPerClient-1 行
	want := int64(numClients * (rowsPerClient - 1))
	c := dialTCPClient(t, s)
	defer c.close()
	if got := sessCount(t, c, table); got != want {
		t.Errorf("总行数: 期望 %d，得到 %d", want, got)
	}
}

// verifyTSRow 验证指定 id 的 TIMESTAMP 与 DATE 字段值。
func verifyTSRow(t *testing.T, s *sqlServer, id int, wantTS, wantDate string) {
	t.Helper()
	resp := queryVia(t, s, "tcp",
		fmt.Sprintf("SELECT ts, d FROM ts_table WHERE id = %d", id))
	if resp.Code != 0 {
		t.Fatalf("查询 id=%d 失败: %s", id, resp.Message)
	}
	rows := respRows(resp)
	if len(rows) != 1 {
		t.Fatalf("id=%d: 期望 1 行，得到 %d", id, len(rows))
	}
	if ts, _ := rows[0]["ts"].(string); ts != wantTS {
		t.Errorf("id=%d ts: 期望 %s，得到 %v", id, wantTS, rows[0]["ts"])
	}
	if d, _ := rows[0]["d"].(string); d != wantDate {
		t.Errorf("id=%d d: 期望 %s，得到 %v", id, wantDate, rows[0]["d"])
	}
}

// TestSQLTimestampDateRoundTrip 验证 TIMESTAMP 与 DATE 类型通过 /write 写入后，
// 可经 SQL SELECT 正确读回。
func TestSQLTimestampDateRoundTrip(t *testing.T) {
	s := startSQLServer(t)
	queryVia(t, s, "tcp", "CREATE TABLE ts_table (id BIGINT NOT NULL, "+
		"ts TIMESTAMP NULL, d DATE NULL, PRIMARY KEY(id))")
	writeVia(t, s, "tcp", "ts_table", []map[string]any{
		{"id": 1, "ts": "2025-06-15T10:30:00Z", "d": "2025-06-15"},
		{"id": 2, "ts": "2025-07-20T08:00:00Z", "d": "2025-07-20"},
	})
	verifyTSRow(t, s, 1, "2025-06-15T10:30:00Z", "2025-06-15")
	verifyTSRow(t, s, 2, "2025-07-20T08:00:00Z", "2025-07-20")
}
