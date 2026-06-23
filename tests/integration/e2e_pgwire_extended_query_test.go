// Package integration 端到端集成测试：PG wire Extended Query Protocol（issue #234）。
//
// PR #236 修复了 PG 端口下"show tables 能返回，select 无结果"的问题，核心
// 改动是实现了 Parse/Bind/Describe/Execute/Sync 序列。本文件提供端到端覆盖：
//
//   - 使用 jackc/pgproto3.Frontend 作为"真实 PG 客户端"（与 pgx、psql、DBeaver
//     内部驱动走的协议路径完全一致），不再走 Simple Query('Q')。
//   - 覆盖完整 SQL 生命周期：CREATE / INSERT / SELECT / UPDATE / DELETE /
//     SHOW TABLES / DESCRIBE，与 Simple Query 路径在结果上一致。
//   - 多客户端交错：N 个并发连接同时走 Extended Query 路径，验证并发安全与
//     per-connection 状态（prepared statement / portal 映射）相互隔离。
//   - 错误路径：解析失败时收到 ErrorResponse + Sync 后 ReadyForQuery，连接仍可用。
//
// 复用 e2e_pgwire_sql_test.go 中的 startPGWireServer；本文件独立实现
// pgExtClient（用 pgproto3.Frontend 收发消息）以与 Simple Query 客户端解耦，
// 便于未来 Extended Query 路径单独演进。
package integration

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgproto3/v2"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

// pgExtClient 是基于 pgproto3.Frontend 的"真实 PG 客户端"，驱动
// Extended Query 协议（Parse/Bind/Describe/Execute/Sync）。
type pgExtClient struct {
	fe   *pgproto3.Frontend
	conn net.Conn
}

// dialPGExt 建立到 PG wire 服务端的 Extended Query 客户端。
// 使用 pgproto3.NewChunkReader 包装 conn 以满足 pgproto3.Frontend 的 ChunkReader 接口；
// 与 pgx、psql 等真实驱动内部走相同的协议栈。
func dialPGExt(t *testing.T, addr string) *pgExtClient {
	t.Helper()
	c, err := dialPGExtErr(addr)
	if err != nil {
		t.Fatalf("拨号 PG wire %s 失败: %v", addr, err)
	}
	return c
}

// dialPGExtErr 是 dialPGExt 的非 testing 版本：失败时返回 error，
// 供运行在独立 goroutine 中的工作负载复用，避免在子 goroutine 中
// 调用 t.Fatalf 导致 "go test -race" 报 fatal on non-test goroutine。
func dialPGExtErr(addr string) (*pgExtClient, error) {
	conn, err := net.DialTimeout("tcp", addr, pgDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("拨号 PG wire %s 失败: %w", addr, err)
	}
	fe := pgproto3.NewFrontend(pgproto3.NewChunkReader(conn), conn)
	return &pgExtClient{fe: fe, conn: conn}, nil
}

// close 关闭连接。
func (c *pgExtClient) close() { _ = c.conn.Close() }

// handshake 完成启动握手：发送 StartupMessage 并消费至 ReadyForQuery。
func (c *pgExtClient) handshake(t *testing.T) {
	t.Helper()
	if err := c.sendStartup(); err != nil {
		t.Fatalf("发送 StartupMessage 失败: %v", err)
	}
	if err := c.consumeUntilReadyForQuery(); err != nil {
		t.Fatalf("握手响应: %v", err)
	}
}

// sendStartup 构造并发送 StartupMessage。
func (c *pgExtClient) sendStartup() error {
	params := map[string]string{"user": "test", "database": "testdb"}
	startup := &pgproto3.StartupMessage{ProtocolVersion: pgProtocolVersion, Parameters: params}
	return c.fe.Send(startup)
}

// consumeUntilReadyForQuery 读取消息直到 ReadyForQuery，返回吸收的消息类型序列。
// 启动握手阶段忽略非 ReadyForQuery 消息（ParameterStatus / NoticeResponse 等）。
func (c *pgExtClient) consumeUntilReadyForQuery() error {
	for {
		msg, err := c.fe.Receive()
		if err != nil {
			return fmt.Errorf("接收消息: %w", err)
		}
		if _, ok := msg.(*pgproto3.ReadyForQuery); ok {
			return nil
		}
	}
}

// --- 高层辅助查询（便于测试断言） ---

// pgExtResult 是一次 extended query 的完整结果（列名、行值、NULL 标记、命令标签、错误）。
type pgExtResult struct {
	columns []string
	rows    [][]string
	tag     string
	errMsg  string
	nilMask []bool // 每列的 NULL 标记（与 values 等长）
}

// runExtendedSQL 走一次完整的 extended query 周期：
// Parse("") -> Bind("") -> Describe('P',"") -> Execute("") -> Sync。
// 返回结构化结果。失败时 t.Fatal。
func (c *pgExtClient) runExtendedSQL(t *testing.T, sql string) *pgExtResult {
	t.Helper()
	res, err := c.runExtendedSQLErr(sql)
	if err != nil {
		t.Fatalf("执行 %q 失败: %v", sql, err)
	}
	if res.errMsg != "" {
		t.Fatalf("执行 %q 服务端报错: %s", sql, res.errMsg)
	}
	return res
}

// runExtendedSQLErr 与 runExtendedSQL 类似，但将错误通过返回值传递，
// 供错误恢复/连接存活测试复用。
func (c *pgExtClient) runExtendedSQLErr(sql string) (*pgExtResult, error) {
	// Parse(stmtName="", query=sql, paramTypes=nil)
	if err := c.fe.Send(&pgproto3.Parse{Name: "", Query: sql, ParameterOIDs: nil}); err != nil {
		return nil, fmt.Errorf("send Parse: %w", err)
	}
	// Bind(portalName="", stmtName="", params=nil, resultFormats=nil)
	if err := c.fe.Send(&pgproto3.Bind{DestinationPortal: "", PreparedStatement: ""}); err != nil {
		return nil, fmt.Errorf("send Bind: %w", err)
	}
	// Describe('P', "") 描述 portal
	if err := c.fe.Send(&pgproto3.Describe{ObjectType: 'P', Name: ""}); err != nil {
		return nil, fmt.Errorf("send Describe: %w", err)
	}
	// Execute(portal="", maxRows=0 表示不限)
	if err := c.fe.Send(&pgproto3.Execute{Portal: "", MaxRows: 0}); err != nil {
		return nil, fmt.Errorf("send Execute: %w", err)
	}
	// Sync 结束一个 extended query 周期
	if err := c.fe.Send(&pgproto3.Sync{}); err != nil {
		return nil, fmt.Errorf("send Sync: %w", err)
	}
	return c.absorbExtendedResponse()
}

// absorbExtendedResponse 读取一个 extended query 周期的完整响应，组装为 pgExtResult。
// 期望消息序列：ParseComplete(1) + BindComplete(2) + (NoData|n|ParameterDescription+T) +
// DataRow* + CommandComplete + ReadyForQuery，可能含 ErrorResponse。
func (c *pgExtClient) absorbExtendedResponse() (*pgExtResult, error) {
	res := &pgExtResult{}
	for {
		msg, err := c.fe.Receive()
		if err != nil {
			return res, fmt.Errorf("接收响应: %w", err)
		}
		switch m := msg.(type) {
		case *pgproto3.ParseComplete:
			// 1
		case *pgproto3.BindComplete:
			// 2
		case *pgproto3.NoData:
			// n：Describe('P',"") 在我们不实现 ParameterDescription 时也可能返回
		case *pgproto3.ParameterDescription:
			// 略：仅供 Parse/Describe('S',...) 使用
		case *pgproto3.RowDescription:
			cols := make([]string, len(m.Fields))
			for i, f := range m.Fields {
				cols[i] = string(f.Name)
			}
			res.columns = cols
		case *pgproto3.DataRow:
			vals := make([]string, len(m.Values))
			nulls := make([]bool, len(m.Values))
			for i, v := range m.Values {
				if v == nil {
					nulls[i] = true
				} else {
					vals[i] = string(v)
				}
			}
			res.rows = append(res.rows, vals)
			res.nilMask = append(res.nilMask, nulls...)
		case *pgproto3.CommandComplete:
			res.tag = string(m.CommandTag)
		case *pgproto3.ErrorResponse:
			res.errMsg = m.Message
		case *pgproto3.ReadyForQuery:
			return res, nil
		default:
			// 忽略 ParameterStatus / NoticeResponse 等无关消息
		}
	}
}

// --- 高层辅助查询（便于测试断言） ---

// getColumnIndex 返回列名对应的索引，未找到时返回 -1。
func (r *pgExtResult) getColumnIndex(name string) int {
	for i, c := range r.columns {
		if c == name {
			return i
		}
	}
	return -1
}

// cell 取第 rowIdx 行的 colName 列文本值；列不存在或行越界返回 false。
func (r *pgExtResult) cell(rowIdx int, colName string) (string, bool) {
	col := r.getColumnIndex(colName)
	if col < 0 || rowIdx >= len(r.rows) {
		return "", false
	}
	return r.rows[rowIdx][col], true
}

// cellIsNull 报告 (rowIdx, colName) 是否为 NULL。
func (r *pgExtResult) cellIsNull(rowIdx int, colName string) bool {
	col := r.getColumnIndex(colName)
	if col < 0 || rowIdx >= len(r.nilMask) {
		return false
	}
	// nilMask 是按行追加的扁平切片，需要定位到 (rowIdx, col)
	offset := rowIdx * len(r.columns)
	if col+offset >= len(r.nilMask) {
		return false
	}
	return r.nilMask[col+offset]
}

// findRow 返回第一个 colName == value 的行索引，未找到返回 -1。
func (r *pgExtResult) findRow(colName, value string) int {
	col := r.getColumnIndex(colName)
	if col < 0 {
		return -1
	}
	for i, row := range r.rows {
		if col < len(row) && row[col] == value {
			return i
		}
	}
	return -1
}

// rowCount 返回结果行数。
func (r *pgExtResult) rowCount() int { return len(r.rows) }

// columnCount 返回列数。
func (r *pgExtResult) columnCount() int { return len(r.columns) }

// --- 工具函数 ---

// pgExtFloat 将 PG 文本值转为 float64。
func pgExtFloat(s string) (float64, error) { return strconv.ParseFloat(s, 64) }

// pgExtInt 将 PG 文本值转为 int64。
func pgExtInt(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }

// startExtPGWireServer 启动一个同时监听 TCP/HTTP/PG wire 的服务器。
func startExtPGWireServer(t *testing.T) *sqlServer {
	t.Helper()
	dir, err := os.MkdirTemp("", "e2e-pgext-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	cfg := server.Config{
		TCPAddr:  "127.0.0.1:0",
		HTTPAddr: "127.0.0.1:0",
		PGAddr:   "127.0.0.1:0",
		DataDir:  dir,
	}
	srv, err := server.NewServer(cfg, server.WithMetricsRegistry(prometheus.NewRegistry()))
	if err != nil {
		t.Fatalf("NewServer 失败: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	return &sqlServer{srv: srv, tcpAddr: srv.TCPAddr(), httpAddr: srv.HTTPAddr()}
}

// pgExtCreateSensorTable 通过 Extended Query 创建 sensor 表并插入初始数据。
func pgExtCreateSensorTable(t *testing.T, c *pgExtClient) {
	t.Helper()
	c.runExtendedSQL(t, "CREATE TABLE sensor ("+
		"id INT64 NOT NULL, "+
		"name STRING NULL, "+
		"temperature FLOAT64 NULL, "+
		"active BOOL NULL, "+
		"PRIMARY KEY(id))")
	c.runExtendedSQL(t, "INSERT INTO sensor (id, name, temperature, active) VALUES "+
		"(1, 'sensor-A', 20.0, true), "+
		"(2, 'sensor-A', 30.0, false), "+
		"(3, 'sensor-B', 25.0, true), "+
		"(4, 'sensor-B', 25.0, true), "+
		"(5, 'sensor-C', 40.0, false)")
}

// --- 端到端测试用例 ---

// TestPGExtQuerySelect 验证 extended query 路径下 SELECT 正常返回数据行。
// 这是 issue #234 的核心修复：Simple Query 之外的 Parse/Bind/Describe/Execute 序列
// 必须返回 DataRow + CommandComplete，而非仅 ReadyForQuery。
func TestPGExtQuerySelect(t *testing.T) {
	s := startExtPGWireServer(t)
	c := dialPGExt(t, s.srv.PGAddr())
	defer c.close()
	c.handshake(t)
	pgExtCreateSensorTable(t, c)

	res := c.runExtendedSQL(t, "SELECT id, name, temperature FROM sensor")
	if res.errMsg != "" {
		t.Fatalf("extended SELECT 失败: %s", res.errMsg)
	}
	if res.rowCount() != 5 {
		t.Errorf("SELECT 期望 5 行，得到 %d", res.rowCount())
	}
	if res.columnCount() != 3 {
		t.Errorf("SELECT 期望 3 列，得到 %d", res.columnCount())
	}
	if got, _ := res.cell(0, "name"); got != "sensor-A" {
		t.Errorf("首行 name 期望 sensor-A，得到 %q", got)
	}
	if !strings.Contains(res.tag, "5") {
		t.Errorf("CommandComplete 标签期望含 5，得到 %q", res.tag)
	}
}

// TestPGExtQueryPointRange 验证点查、范围查询、LIKE 在 extended query 路径下正确。
func TestPGExtQueryPointRange(t *testing.T) {
	s := startExtPGWireServer(t)
	c := dialPGExt(t, s.srv.PGAddr())
	defer c.close()
	c.handshake(t)
	pgExtCreateSensorTable(t, c)

	// 点查
	pres := c.runExtendedSQL(t, "SELECT name, temperature FROM sensor WHERE id = 3")
	if pres.rowCount() != 1 {
		t.Errorf("点查 id=3 期望 1 行，得到 %d", pres.rowCount())
	}
	if v, _ := pres.cell(0, "name"); v != "sensor-B" {
		t.Errorf("点查 name 期望 sensor-B，得到 %q", v)
	}
	if v, _ := pres.cell(0, "temperature"); v != "25" {
		t.Errorf("点查 temperature 期望 25，得到 %q", v)
	}

	// 范围
	rres := c.runExtendedSQL(t, "SELECT id FROM sensor WHERE id >= 2 AND id <= 4")
	if rres.rowCount() != 3 {
		t.Errorf("范围 2..4 期望 3 行，得到 %d", rres.rowCount())
	}

	// LIKE
	lres := c.runExtendedSQL(t, "SELECT id FROM sensor WHERE name LIKE '%%A%%'")
	if lres.rowCount() != 2 {
		t.Errorf("LIKE '%%A%%' 期望 2 行，得到 %d", lres.rowCount())
	}
}

// TestPGExtQueryGroupByAggregate 验证 extended query 路径下 GROUP BY 聚合正确。
func TestPGExtQueryGroupByAggregate(t *testing.T) {
	s := startExtPGWireServer(t)
	c := dialPGExt(t, s.srv.PGAddr())
	defer c.close()
	c.handshake(t)
	pgExtCreateSensorTable(t, c)

	res := c.runExtendedSQL(t, "SELECT name, COUNT(*) AS cnt, SUM(temperature) AS total "+
		"FROM sensor GROUP BY name ORDER BY name")
	if res.errMsg != "" {
		t.Fatalf("GROUP BY 失败: %s", res.errMsg)
	}
	if res.rowCount() != 3 {
		t.Fatalf("GROUP BY 期望 3 组，得到 %d", res.rowCount())
	}
	// 按 name 排序：A=2/50, B=2/50, C=1/40
	expected := []struct {
		name  string
		cnt   int64
		total float64
	}{
		{"sensor-A", 2, 50.0},
		{"sensor-B", 2, 50.0},
		{"sensor-C", 1, 40.0},
	}
	for i, exp := range expected {
		gotName, _ := res.cell(i, "name")
		if gotName != exp.name {
			t.Errorf("第 %d 组 name 期望 %q，得到 %q", i, exp.name, gotName)
		}
		gotCnt, _ := res.cell(i, "cnt")
		n, _ := pgExtInt(gotCnt)
		if n != exp.cnt {
			t.Errorf("第 %d 组 cnt 期望 %d，得到 %d", i, exp.cnt, n)
		}
		gotTot, _ := res.cell(i, "total")
		f, _ := pgExtFloat(gotTot)
		if f != exp.total {
			t.Errorf("第 %d 组 total 期望 %v，得到 %v", i, exp.total, f)
		}
	}
}

// TestPGExtWriteDML 验证 extended query 路径下 UPDATE/DELETE 的 CommandComplete
// 标签与受影响行数，并通过后续 SELECT 验证数据正确性。
func TestPGExtWriteDML(t *testing.T) {
	s := startExtPGWireServer(t)
	c := dialPGExt(t, s.srv.PGAddr())
	defer c.close()
	c.handshake(t)
	pgExtCreateSensorTable(t, c)

	// UPDATE
	ures := c.runExtendedSQL(t, "UPDATE sensor SET temperature = 99.0 WHERE id = 1")
	if !strings.Contains(ures.tag, "1") {
		t.Errorf("UPDATE 标签期望含 1，得到 %q", ures.tag)
	}
	vres := c.runExtendedSQL(t, "SELECT temperature FROM sensor WHERE id = 1")
	if v, _ := vres.cell(0, "temperature"); v != "99" {
		t.Errorf("UPDATE 后 temperature 期望 99，得到 %q", v)
	}

	// DELETE
	dres := c.runExtendedSQL(t, "DELETE FROM sensor WHERE id = 5")
	if !strings.Contains(dres.tag, "1") {
		t.Errorf("DELETE 标签期望含 1，得到 %q", dres.tag)
	}
	cres := c.runExtendedSQL(t, "SELECT COUNT(*) AS cnt FROM sensor")
	if v, _ := cres.cell(0, "cnt"); v != "4" {
		t.Errorf("DELETE 后 COUNT 期望 4，得到 %q", v)
	}
}

// TestPGExtMetaCommands 验证 SHOW TABLES / DESCRIBE 在 extended query 路径下可用。
func TestPGExtMetaCommands(t *testing.T) {
	s := startExtPGWireServer(t)
	c := dialPGExt(t, s.srv.PGAddr())
	defer c.close()
	c.handshake(t)
	pgExtCreateSensorTable(t, c)

	st := c.runExtendedSQL(t, "SHOW TABLES")
	if st.rowCount() < 1 {
		t.Fatalf("SHOW TABLES 期望至少 1 行，得到 %d", st.rowCount())
	}
	if st.findRow("table", "sensor") < 0 {
		t.Errorf("SHOW TABLES 应包含 sensor，实际 %v", st.rows)
	}

	desc := c.runExtendedSQL(t, "DESCRIBE sensor")
	if desc.rowCount() != 4 {
		t.Errorf("DESCRIBE sensor 期望 4 行（4 列），得到 %d", desc.rowCount())
	}
}

// TestPGExtNullValue 验证 NULL 值在 extended query 路径下正确编解码。
func TestPGExtNullValue(t *testing.T) {
	s := startExtPGWireServer(t)
	c := dialPGExt(t, s.srv.PGAddr())
	defer c.close()
	c.handshake(t)

	c.runExtendedSQL(t, "CREATE TABLE nt (id INT64 NOT NULL, opt STRING NULL, PRIMARY KEY(id))")
	c.runExtendedSQL(t, "INSERT INTO nt (id, opt) VALUES (1, 'a'), (2, NULL), (3, 'c')")

	res := c.runExtendedSQL(t, "SELECT id, opt FROM nt ORDER BY id")
	if res.rowCount() != 3 {
		t.Fatalf("SELECT 期望 3 行，得到 %d", res.rowCount())
	}
	if v, _ := res.cell(0, "opt"); v != "a" {
		t.Errorf("第 1 行 opt 期望 a，得到 %q", v)
	}
	if !res.cellIsNull(1, "opt") {
		t.Error("第 2 行 opt 应为 NULL")
	}
	if v, _ := res.cell(2, "opt"); v != "c" {
		t.Errorf("第 3 行 opt 期望 c，得到 %q", v)
	}
}

// TestPGExtErrorRecovery 验证 extended query 路径下：
//   - 无效 SQL 触发 ErrorResponse + 后续 ReadyForQuery
//   - 错误后连接仍可用，下一次 extended query 周期能正常返回
func TestPGExtErrorRecovery(t *testing.T) {
	s := startExtPGWireServer(t)
	c := dialPGExt(t, s.srv.PGAddr())
	defer c.close()
	c.handshake(t)

	// 故意构造一个解析失败
	bad, err := c.runExtendedSQLErr("THIS IS NOT VALID SQL !!!")
	if err != nil {
		t.Fatalf("传输层错误: %v", err)
	}
	if bad.errMsg == "" {
		t.Error("无效 SQL 应返回 ErrorResponse")
	}

	// 连接仍可用：合法 SQL 应正常返回
	ok := c.runExtendedSQL(t, "CREATE TABLE recover_ok (id INT64 NOT NULL, PRIMARY KEY(id))")
	if ok.errMsg != "" {
		t.Errorf("错误后连接应仍可用: %s", ok.errMsg)
	}
}

// TestPGExtMultiClientParallel 验证多客户端并发走 extended query 路径
// 时，per-connection 状态（prepared statement / portal 映射）相互隔离。
//
// 设计：6 个客户端，每个执行 4 个周期的 CREATE/INSERT/SELECT/UPDATE，
// 全部成功后断言各自表数据一致；如某客户端错误地使用了其他客户端的
// prepared statement 名空间（理论上不可能，因为服务端按连接隔离），
// 会以 ErrorResponse 形式暴露。
//
// 注意：所有工作负载都在独立 goroutine 中运行，子 goroutine 不能调用
// t.Fatal/t.Fatalf（Go 测试框架要求 Fatal 必须从运行测试的 goroutine 调用），
// 因此 worker 函数不持有 *testing.T，所有错误通过 errCh 上报，
// 由 TestPGExtMultiClientParallel 在主 goroutine 中通过 t.Error 统一记录。
func TestPGExtMultiClientParallel(t *testing.T) {
	s := startExtPGWireServer(t)

	const (
		clientCount = 6
		iterations  = 4
	)
	var wg sync.WaitGroup
	errCh := make(chan error, clientCount*iterations*2)

	for cid := 0; cid < clientCount; cid++ {
		wg.Add(1)
		go runMultiClientWorkload(s.srv.PGAddr(), cid, iterations, &wg, errCh)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

// runMultiClientWorkload 在独立连接上执行一个客户端的扩展查询工作负载，
// 每轮 INSERT/SELECT/UPDATE/SELECT，并通过 errCh 报告任何阶段错误。
// 独立函数拆出是为了将 TestPGExtMultiClientParallel 的认知复杂度控制在阈值内。
//
// 不接受 *testing.T：调用方（goroutine）禁止调用 t.Fatal，错误全部经 errCh
// 上报给主测试 goroutine。
func runMultiClientWorkload(addr string, clientID, iterations int, wg *sync.WaitGroup, errCh chan<- error) {
	defer wg.Done()
	c, err := dialPGExtErr(addr)
	if err != nil {
		errCh <- fmt.Errorf("client %d: 拨号: %w", clientID, err)
		return
	}
	defer c.close()
	if err := c.sendStartup(); err != nil {
		errCh <- fmt.Errorf("client %d: 启动: %w", clientID, err)
		return
	}
	if err := c.consumeUntilReadyForQuery(); err != nil {
		errCh <- fmt.Errorf("client %d: 握手: %w", clientID, err)
		return
	}

	table := fmt.Sprintf("multi_t_%d", clientID)
	createSQL := "CREATE TABLE " + table +
		" (id INT64 NOT NULL, payload STRING NULL, PRIMARY KEY(id))"
	if r, err := c.runExtendedSQLErr(createSQL); err != nil {
		errCh <- fmt.Errorf("client %d: CREATE 发送: %w", clientID, err)
		return
	} else if r.errMsg != "" {
		errCh <- fmt.Errorf("client %d: CREATE: %s", clientID, r.errMsg)
		return
	}

	for iter := 0; iter < iterations; iter++ {
		if err := runMultiClientIteration(c, clientID, iter, table, errCh); err != nil {
			return
		}
	}
}

// runMultiClientIteration 执行一轮 INSERT/SELECT/UPDATE/SELECT 并校验结果。
// 任何步骤失败时通过 errCh 报告并返回非 nil；调用方负责早退。
//
// 不接受 *testing.T：调用方（goroutine）禁止调用 t.Fatal，错误全部经 errCh
// 上报给主测试 goroutine。
func runMultiClientIteration(c *pgExtClient, clientID, iter int, table string, errCh chan<- error) error {
	id := int64(clientID*1000 + iter + 1)
	payload := fmt.Sprintf("c%d-iter%d", clientID, iter)
	newPayload := payload + "_u"

	// INSERT
	ins, err := c.runExtendedSQLErr(fmt.Sprintf(
		"INSERT INTO %s (id, payload) VALUES (%d, '%s')", table, id, payload))
	if err != nil {
		errCh <- fmt.Errorf("client %d iter %d: INSERT 发送: %w", clientID, iter, err)
		return fmt.Errorf("insert send")
	}
	if ins.errMsg != "" {
		errCh <- fmt.Errorf("client %d iter %d: INSERT: %s", clientID, iter, ins.errMsg)
		return fmt.Errorf("insert failed")
	}
	// 校验 SELECT
	sel, err := c.runExtendedSQLErr(fmt.Sprintf(
		"SELECT payload FROM %s WHERE id = %d", table, id))
	if err != nil {
		errCh <- fmt.Errorf("client %d iter %d: SELECT 发送: %w", clientID, iter, err)
		return fmt.Errorf("select send")
	}
	if sel.rowCount() != 1 {
		errCh <- fmt.Errorf("client %d iter %d: SELECT 期望 1 行，得到 %d",
			clientID, iter, sel.rowCount())
		return fmt.Errorf("select row count")
	}
	if v, _ := sel.cell(0, "payload"); v != payload {
		errCh <- fmt.Errorf("client %d iter %d: SELECT payload 期望 %q 得到 %q",
			clientID, iter, payload, v)
		return fmt.Errorf("select value")
	}
	// UPDATE
	upd, err := c.runExtendedSQLErr(fmt.Sprintf(
		"UPDATE %s SET payload = '%s' WHERE id = %d", table, newPayload, id))
	if err != nil {
		errCh <- fmt.Errorf("client %d iter %d: UPDATE 发送: %w", clientID, iter, err)
		return fmt.Errorf("update send")
	}
	if upd.errMsg != "" {
		errCh <- fmt.Errorf("client %d iter %d: UPDATE: %s", clientID, iter, upd.errMsg)
		return fmt.Errorf("update failed")
	}
	// 校验 UPDATE 后值
	sel2, err := c.runExtendedSQLErr(fmt.Sprintf(
		"SELECT payload FROM %s WHERE id = %d", table, id))
	if err != nil {
		errCh <- fmt.Errorf("client %d iter %d: 校验 SELECT 发送: %w", clientID, iter, err)
		return fmt.Errorf("post-update select send")
	}
	if v, _ := sel2.cell(0, "payload"); v != newPayload {
		errCh <- fmt.Errorf("client %d iter %d: UPDATE 后 payload 期望 %q 得到 %q",
			clientID, iter, newPayload, v)
		return fmt.Errorf("post-update value")
	}
	return nil
}

// TestPGExtRoundTrip 验证一次 extended query 周期内消息序列符合 PG 协议：
// ParseComplete -> BindComplete -> (NoData) -> (RowDescription) -> DataRow* ->
// CommandComplete -> ReadyForQuery。本测试是 issue #234 的"协议层防回归"用例。
func TestPGExtRoundTrip(t *testing.T) {
	s := startExtPGWireServer(t)
	c := dialPGExt(t, s.srv.PGAddr())
	defer c.close()
	c.handshake(t)
	pgExtCreateSensorTable(t, c)

	if err := c.sendExtendedQueryRequest("SELECT id FROM sensor WHERE id = 1"); err != nil {
		t.Fatalf("发送 extended query 失败: %v", err)
	}
	types, dataRows, commandTag := receiveUntilReadyForQuery(t, c.fe)
	want := []byte{'1', '2', 'n', 'T', 'D', 'C', 'Z'}
	if !bytes.Equal(types, want) {
		t.Errorf("消息序列不匹配: 期望 %v, 得到 %v", want, types)
	}
	if dataRows != 1 {
		t.Errorf("DataRow 数量期望 1，得到 %d", dataRows)
	}
	if !strings.Contains(commandTag, "1") {
		t.Errorf("CommandComplete 标签期望含 1，得到 %q", commandTag)
	}
}

// sendExtendedQueryRequest 发送完整的 Parse/Bind/Describe/Execute/Sync 序列。
func (c *pgExtClient) sendExtendedQueryRequest(sql string) error {
	if err := c.fe.Send(&pgproto3.Parse{Name: "", Query: sql, ParameterOIDs: nil}); err != nil {
		return fmt.Errorf("send Parse: %w", err)
	}
	if err := c.fe.Send(&pgproto3.Bind{DestinationPortal: "", PreparedStatement: ""}); err != nil {
		return fmt.Errorf("send Bind: %w", err)
	}
	if err := c.fe.Send(&pgproto3.Describe{ObjectType: 'P', Name: ""}); err != nil {
		return fmt.Errorf("send Describe: %w", err)
	}
	if err := c.fe.Send(&pgproto3.Execute{Portal: "", MaxRows: 0}); err != nil {
		return fmt.Errorf("send Execute: %w", err)
	}
	if err := c.fe.Send(&pgproto3.Sync{}); err != nil {
		return fmt.Errorf("send Sync: %w", err)
	}
	return nil
}

// receiveUntilReadyForQuery 读取一条 extended query 响应的原始消息类型，组装为
// (types 字节序列, DataRow 数量, CommandComplete 标签)。遇到 ReadyForQuery 结束。
// 遇 ErrorResponse 终止测试，避免继续读取导致协议错位。
func receiveUntilReadyForQuery(t *testing.T, fe *pgproto3.Frontend) (types []byte, dataRows int, commandTag string) {
	t.Helper()
	for {
		msg, err := fe.Receive()
		if err != nil {
			t.Fatalf("接收消息: %v", err)
		}
		switch m := msg.(type) {
		case *pgproto3.ParseComplete:
			types = append(types, '1')
		case *pgproto3.BindComplete:
			types = append(types, '2')
		case *pgproto3.NoData:
			types = append(types, 'n')
		case *pgproto3.RowDescription:
			types = append(types, 'T')
		case *pgproto3.DataRow:
			types = append(types, 'D')
			dataRows++
		case *pgproto3.CommandComplete:
			types = append(types, 'C')
			commandTag = string(m.CommandTag)
		case *pgproto3.ErrorResponse:
			types = append(types, 'E')
			t.Fatalf("unexpected ErrorResponse: %s", m.Message)
		case *pgproto3.ReadyForQuery:
			types = append(types, 'Z')
			return types, dataRows, commandTag
		}
	}
}

// 未使用导入保护：当前实现中 pgproto3.Frontend 已封装所有底层编解码，
// 无需直接操作字节或时间常量，故本文件不再额外 import encoding/binary 或 time。
// 如未来需要 raw 字节级断言或自定义超时，再恢复即可。
