// Package integration 端到端集成测试：三协议（TCP / HTTP / PG wire）+ 多客户端
// 触发慢查询日志，验证 /admin/slow-queries 能正确按 source 区分各协议来源。
//
// 场景设计：
//   - 启动一个 server，同时开启 TCP、HTTP、PG wire 三个监听端口，慢查询阈值取 1ns
//     （等价于「记录所有查询」），环形缓冲容量 50
//   - 9 个并发客户端（3 个 TCP 长连接 + 3 个 HTTP 短连接 + 3 个 PG wire 长连接）
//     各自在互不冲突的 ID 区间内执行 INSERT + SELECT + UPDATE + DELETE
//   - 所有客户端完成后：
//     1. 由 HTTP 客户端查 /admin/slow-queries，断言所有 3 个 source 标签均出现
//     2. 由 PG wire 客户端查 COUNT(*) 验证数据完整性
//     3. 由 TCP 客户端查 SELECT 验证更新/删除生效
//   - 二次查询 /admin/slow-queries 应仍能看到新追加的查询（环形缓冲未刷新）
//
// 与现有 e2e_admin_slow_queries_test.go 单协议慢查询测试的区别：
//   - 现有测试只覆盖 HTTP 协议（queryVia/t, s, "http", sql）
//   - 本测试覆盖三协议并发，确保 source 字段在并发场景下也能正确打标
//   - 同时验证慢查询日志不会因为协议间交叉执行而出错或丢记录
package integration

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

// sqmp 常量：三协议多客户端慢查询测试。
const (
	// sqmpClientsPerProtocol 单协议下的并发客户端数（每协议 3 个，共 9 客户端）
	sqmpClientsPerProtocol = 3
	// sqmpRowsPerClient 每个客户端写入的行数
	sqmpRowsPerClient = 8
	// sqmpIDBase 客户端写入 ID 起始偏移（与现有测试错开，避免表名/ID 冲突）
	sqmpIDBase = 80000
	// sqmpTableName 测试表名
	sqmpTableName = "sqmp_orders"
	// sqmpSlowCapacity 慢查询环形缓冲容量，覆盖 9 客户端 × 多种 SQL 后的总记录数
	sqmpSlowCapacity = 200
)

// sqmpSourceStats 汇总各协议 source 的出现次数。
type sqmpSourceStats struct {
	httpCount   int
	tcpCount    int
	pgwireCount int
	// otherCount 用于发现未预期的 source 标签（应始终为 0）
	otherCount int
	// sampleSQL 用于调试时快速定位具体 SQL
	sampleSQL map[string]string
}

// startSQLServerAllProtocolsSlowQuery 启动同时开启 TCP/HTTP/PG wire + 慢查询日志的服务器。
//
// 与 startSQLServerWithSlowQuery 的区别：本函数额外启用 PGAddr 监听，
// 便于测试 PG wire 协议下的慢查询打标路径。
func startSQLServerAllProtocolsSlowQuery(t *testing.T, threshold time.Duration, capacity int) *sqlServer {
	t.Helper()
	dir := t.TempDir()
	cfg := server.Config{
		TCPAddr:            "127.0.0.1:0",
		HTTPAddr:           "127.0.0.1:0",
		PGAddr:             "127.0.0.1:0",
		DataDir:            dir,
		SlowQueryThreshold: threshold,
		SlowQueryCapacity:  capacity,
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

// sqmpCountSources 统计慢查询响应中各 source 的出现次数。
// 同时收集每个 source 的首条 SQL 样本，便于断言失败时输出可读上下文。
func sqmpCountSources(queries []slowQueryEntryResp) sqmpSourceStats {
	s := sqmpSourceStats{sampleSQL: make(map[string]string, 3)}
	for _, q := range queries {
		switch q.Source {
		case "http":
			s.httpCount++
			if _, ok := s.sampleSQL["http"]; !ok {
				s.sampleSQL["http"] = q.SQL
			}
		case "tcp":
			s.tcpCount++
			if _, ok := s.sampleSQL["tcp"]; !ok {
				s.sampleSQL["tcp"] = q.SQL
			}
		case "pgwire":
			s.pgwireCount++
			if _, ok := s.sampleSQL["pgwire"]; !ok {
				s.sampleSQL["pgwire"] = q.SQL
			}
		default:
			s.otherCount++
		}
	}
	return s
}

// sqmpHasSource 断言 queries 中至少有一条记录的 source 与 target 一致。
func sqmpHasSource(queries []slowQueryEntryResp, target string) bool {
	for _, q := range queries {
		if q.Source == target {
			return true
		}
	}
	return false
}

// sqmpFilteredSQLs 返回指定 source 的所有 SQL 文本（去前后空格、小写）。
// 便于后续断言 "其中至少一条是 INSERT/SELECT/UPDATE/DELETE"。
func sqmpFilteredSQLs(queries []slowQueryEntryResp, source string) []string {
	out := make([]string, 0, len(queries))
	for _, q := range queries {
		if q.Source == source {
			out = append(out, strings.ToLower(strings.TrimSpace(q.SQL)))
		}
	}
	sort.Strings(out)
	return out
}

// sqmpHasSQLWithPrefix 判断 sqls 中是否存在以 prefix 开头（大小写不敏感）的语句。
func sqmpHasSQLWithPrefix(sqls []string, prefix string) bool {
	p := strings.ToLower(prefix)
	for _, sql := range sqls {
		if strings.HasPrefix(sql, p) {
			return true
		}
	}
	return false
}

// sqmpHasSQLContaining 判断 sqls 中是否存在包含 substr（大小写不敏感）的语句。
func sqmpHasSQLContaining(sqls []string, substr string) bool {
	p := strings.ToLower(substr)
	for _, sql := range sqls {
		if strings.Contains(sql, p) {
			return true
		}
	}
	return false
}

// TestSlowQueryMultiProtocolMultiClient 验证多协议多客户端并发执行时，
// /admin/slow-queries 能正确捕获并按 source 区分各协议来源，同时数据一致性保持正确。
func TestSlowQueryMultiProtocolMultiClient(t *testing.T) {
	t.Parallel()
	s := startSQLServerAllProtocolsSlowQuery(t, time.Nanosecond, sqmpSlowCapacity)
	sqmpBootstrapTable(t, s)
	sqmpRunMultiClientWorkload(t, s)

	first := sqmpFetchAndAssertSlowQueries(t, s)
	sqmpAssertSourceCoverage(t, first)
	sqmpAssertPerSourceSQLOps(t, first)
	sqmpAssertDataConsistency(t, s)

	second := sqmpVerifyAppendOnly(t, s)
	sqmpAssertSecondAtLeastAsLong(t, first.Queries, second.Queries)
}

// sqmpBootstrapTable 通过 HTTP 客户端建表，是后续多客户端写数据的前置条件。
func sqmpBootstrapTable(t *testing.T, s *sqlServer) {
	t.Helper()
	resp := queryVia(t, s, "http",
		"CREATE TABLE "+sqmpTableName+" ("+
			"id INT64 NOT NULL, "+
			"vendor STRING NULL, "+
			"qty INT64 NULL, "+
			"PRIMARY KEY(id))")
	if resp.Code != 0 {
		t.Fatalf("建表失败: code=%d message=%q", resp.Code, resp.Message)
	}
}

// sqmpRunMultiClientWorkload 启动 totalClients 个并发客户端，每个客户端在互不冲突的
// ID 区间内执行 INSERT/SELECT/UPDATE/DELETE 序列。任一客户端失败立即终止测试。
func sqmpRunMultiClientWorkload(t *testing.T, s *sqlServer) {
	t.Helper()
	totalClients := sqmpClientsPerProtocol * 3
	var wg sync.WaitGroup
	var failCount int64
	var lastErr atomic.Value

	for i := 0; i < totalClients; i++ {
		protocol := sqmpProtocolForIndex(i)
		wg.Add(1)
		go func(clientID int, proto string) {
			defer wg.Done()
			if err := sqmpClientWork(s, proto, clientID); err != nil {
				t.Logf("[%s client %d] 失败: %v", proto, clientID, err)
				lastErr.Store(err.Error())
				atomic.AddInt64(&failCount, 1)
			}
		}(i, protocol)
	}
	wg.Wait()

	if failCount > 0 {
		t.Fatalf("%d 个客户端失败，最后错误: %v", failCount, lastErr.Load())
	}
}

// sqmpFetchAndAssertSlowQueries 调用 /admin/slow-queries 并做基础字段断言。
// 返回解码后的响应，供后续断言复用。
func sqmpFetchAndAssertSlowQueries(t *testing.T, s *sqlServer) slowQueriesResponseResp {
	t.Helper()
	status, body, raw, err := getAdminSlowQueries(t, s)
	if err != nil {
		t.Fatalf("GET /admin/slow-queries 失败: %v", err)
	}
	if status != 200 || body.Code != 0 {
		t.Fatalf("状态码=%d Code=%d Message=%q raw=%s", status, body.Code, body.Message, string(raw))
	}
	if !body.Config.Enabled {
		t.Errorf("Config.Enabled = false, 期望 true（threshold=1ns）")
	}
	if body.Config.Capacity != sqmpSlowCapacity {
		t.Errorf("Config.Capacity = %d, 期望 %d", body.Config.Capacity, sqmpSlowCapacity)
	}
	return body
}

// sqmpAssertSourceCoverage 断言三协议 source 都被记录，且没有未预期的 source。
func sqmpAssertSourceCoverage(t *testing.T, body slowQueriesResponseResp) {
	t.Helper()
	for _, src := range []string{"http", "tcp", "pgwire"} {
		if !sqmpHasSource(body.Queries, src) {
			t.Errorf("未捕获到 %s source 的慢查询", src)
		}
	}
	stats := sqmpCountSources(body.Queries)
	if stats.otherCount > 0 {
		t.Errorf("出现非预期的 source 标签 %d 次", stats.otherCount)
	}
	if stats.httpCount == 0 || stats.tcpCount == 0 || stats.pgwireCount == 0 {
		t.Errorf("某协议 source 计数为 0: http=%d tcp=%d pgwire=%d (样本: %v)",
			stats.httpCount, stats.tcpCount, stats.pgwireCount, stats.sampleSQL)
	}
}

// sqmpAssertPerSourceSQLOps 断言每个 source 都覆盖了 INSERT/SELECT/UPDATE/DELETE 四类语句。
// 这些操作与慢查询打点路径正交，因此可以同时验证 source 标记与 SQL 内容。
func sqmpAssertPerSourceSQLOps(t *testing.T, body slowQueriesResponseResp) {
	t.Helper()
	for _, src := range []string{"http", "tcp", "pgwire"} {
		sqls := sqmpFilteredSQLs(body.Queries, src)
		if !sqmpHasSQLWithPrefix(sqls, "insert") {
			t.Errorf("[%s] 缺少 INSERT 语句，sqls=%v", src, sqls)
		}
		if !sqmpHasSQLWithPrefix(sqls, "select") {
			t.Errorf("[%s] 缺少 SELECT 语句，sqls=%v", src, sqls)
		}
		if !sqmpHasSQLWithPrefix(sqls, "update") {
			t.Errorf("[%s] 缺少 UPDATE 语句，sqls=%v", src, sqls)
		}
		if !sqmpHasSQLWithPrefix(sqls, "delete") {
			t.Errorf("[%s] 缺少 DELETE 语句，sqls=%v", src, sqls)
		}
	}
}

// sqmpAssertDataConsistency 验证多客户端并发执行后数据的总量、UPDATE、DELETE 一致性。
// 通过 PG wire 协议查 COUNT 与单行 qty/id，与预期值比对。
func sqmpAssertDataConsistency(t *testing.T, s *sqlServer) {
	t.Helper()
	totalClients := sqmpClientsPerProtocol * 3
	wantRows := int64(totalClients * (sqmpRowsPerClient - 1))

	pg := dialPGWire(t, s.srv.PGAddr())
	defer pg.close()
	pg.handshake(t)

	cntRes := pg.execOK(t, "SELECT COUNT(*) AS cnt FROM "+sqmpTableName)
	if cnt, ok := pgInt(pgRowToMap(cntRes.columns, cntRes.rows[0])["cnt"]); !ok || cnt != wantRows {
		t.Errorf("PG wire 校验 COUNT: 期望 %d, 实际 %d (ok=%v)", wantRows, cnt, ok)
	}

	// 验证 UPDATE 生效：每个客户端首行 qty 应为 9999
	for i := 0; i < totalClients; i++ {
		id := sqmpIDBase + i*sqmpRowsPerClient
		r := pg.execOK(t, "SELECT qty FROM "+sqmpTableName+" WHERE id = "+itoa(id))
		if len(r.rows) != 1 {
			t.Errorf("id=%d 期望 1 行，得到 %d", id, len(r.rows))
			continue
		}
		if q, _ := pgInt(pgRowToMap(r.columns, r.rows[0])["qty"]); q != 9999 {
			t.Errorf("id=%d qty 期望 9999，得到 %d", id, q)
		}
	}

	// 验证 DELETE 生效：每个客户端第二行应已被删除
	for i := 0; i < totalClients; i++ {
		id := sqmpIDBase + i*sqmpRowsPerClient + 1
		r := pg.execOK(t, "SELECT id FROM "+sqmpTableName+" WHERE id = "+itoa(id))
		if len(r.rows) != 0 {
			t.Errorf("id=%d 应已被删除，但查到 %d 行", id, len(r.rows))
		}
	}
}

// sqmpVerifyAppendOnly 触发一次额外的 HTTP SELECT，再查询 /admin/slow-queries 验证
// 慢查询环形缓冲按预期追加。
func sqmpVerifyAppendOnly(t *testing.T, s *sqlServer) slowQueriesResponseResp {
	t.Helper()
	_ = queryVia(t, s, "http", "SELECT id FROM "+sqmpTableName+" ORDER BY id LIMIT 1")
	_, body, _, err := getAdminSlowQueries(t, s)
	if err != nil {
		t.Fatalf("二次 GET /admin/slow-queries 失败: %v", err)
	}
	return body
}

// sqmpAssertSecondAtLeastAsLong 断言二次查询的记录数不小于首次，并确认新的 SELECT 已落盘。
func sqmpAssertSecondAtLeastAsLong(t *testing.T, first, second []slowQueryEntryResp) {
	t.Helper()
	if len(second) < len(first) {
		t.Errorf("二次查询返回的记录数应不小于首次（环形缓冲 LRU）: 首次=%d 二次=%d",
			len(first), len(second))
	}
	if !sqmpHasSQLContaining(sqmpFilteredSQLs(second, "http"), "order by id") {
		t.Errorf("二次查询未捕获到新追加的 HTTP SELECT，sqls=%v",
			sqmpFilteredSQLs(second, "http"))
	}
}

// sqmpProtocolForIndex 将客户端全局索引映射到协议标签。
// 索引 0..2 → http, 3..5 → tcp, 6..8 → pgwire（与 sqmpClientsPerProtocol=3 对齐）。
func sqmpProtocolForIndex(i int) string {
	switch {
	case i < sqmpClientsPerProtocol:
		return "http"
	case i < sqmpClientsPerProtocol*2:
		return "tcp"
	default:
		return "pgwire"
	}
}

// sqmpClientWork 单个客户端的工作负载：INSERT 多行 → SELECT 全部 → UPDATE 首行 → DELETE 第二行。
// 根据协议选择对应的客户端类型：HTTP 走 httpQuery，TCP 走 tcp 长连接，PG wire 走 pgWireClient。
func sqmpClientWork(s *sqlServer, proto string, clientID int) error {
	switch proto {
	case "http":
		return sqmpHTTPClientWork(s, clientID)
	case "tcp":
		return sqmpTCPClientWork(s, clientID)
	case "pgwire":
		return sqmpPGWireClientWork(s, clientID)
	default:
		return nil
	}
}

// sqmpHTTPClientWork HTTP 客户端工作负载：每次新建短连接，模拟真实 Web 客户端。
func sqmpHTTPClientWork(s *sqlServer, clientID int) error {
	// INSERT
	insertSQL := sqmpBuildInsertSQL(clientID)
	if resp, err := httpQuery(s.httpAddr, insertSQL); err != nil {
		return err
	} else if resp.Code != 0 {
		return errf("HTTP INSERT 失败: code=%d msg=%q", resp.Code, resp.Message)
	}

	// SELECT 全部（确认自己写入的行存在）
	selectAll := "SELECT id, vendor, qty FROM " + sqmpTableName + " WHERE id >= " +
		itoa(sqmpIDBase+clientID*sqmpRowsPerClient) + " AND id < " +
		itoa(sqmpIDBase+(clientID+1)*sqmpRowsPerClient)
	if resp, err := httpQuery(s.httpAddr, selectAll); err != nil {
		return err
	} else if resp.Code != 0 {
		return errf("HTTP SELECT 失败: code=%d msg=%q", resp.Code, resp.Message)
	}

	// UPDATE 首行
	firstID := sqmpIDBase + clientID*sqmpRowsPerClient
	updSQL := "UPDATE " + sqmpTableName + " SET qty = 9999 WHERE id = " + itoa(firstID)
	if resp, err := httpQuery(s.httpAddr, updSQL); err != nil {
		return err
	} else if resp.Code != 0 {
		return errf("HTTP UPDATE 失败: code=%d msg=%q", resp.Code, resp.Message)
	}

	// DELETE 第二行
	secondID := firstID + 1
	delSQL := "DELETE FROM " + sqmpTableName + " WHERE id = " + itoa(secondID)
	if resp, err := httpQuery(s.httpAddr, delSQL); err != nil {
		return err
	} else if resp.Code != 0 {
		return errf("HTTP DELETE 失败: code=%d msg=%q", resp.Code, resp.Message)
	}
	return nil
}

// sqmpTCPClientWork TCP 客户端工作负载：复用单条长连接，模拟真实客户端（连接池/JDBC）。
func sqmpTCPClientWork(s *sqlServer, clientID int) error {
	tc, err := dialTCP(s.tcpAddr)
	if err != nil {
		return err
	}
	defer tc.close()

	// INSERT
	insertSQL := sqmpBuildInsertSQL(clientID)
	if resp, err := tc.query(insertSQL); err != nil {
		return err
	} else if resp.Code != 0 {
		return errf("TCP INSERT 失败: code=%d msg=%q", resp.Code, resp.Message)
	}

	// SELECT 全部
	selectAll := "SELECT id, vendor, qty FROM " + sqmpTableName + " WHERE id >= " +
		itoa(sqmpIDBase+clientID*sqmpRowsPerClient) + " AND id < " +
		itoa(sqmpIDBase+(clientID+1)*sqmpRowsPerClient)
	if resp, err := tc.query(selectAll); err != nil {
		return err
	} else if resp.Code != 0 {
		return errf("TCP SELECT 失败: code=%d msg=%q", resp.Code, resp.Message)
	}

	// UPDATE 首行
	firstID := sqmpIDBase + clientID*sqmpRowsPerClient
	updSQL := "UPDATE " + sqmpTableName + " SET qty = 9999 WHERE id = " + itoa(firstID)
	if resp, err := tc.query(updSQL); err != nil {
		return err
	} else if resp.Code != 0 {
		return errf("TCP UPDATE 失败: code=%d msg=%q", resp.Code, resp.Message)
	}

	// DELETE 第二行
	secondID := firstID + 1
	delSQL := "DELETE FROM " + sqmpTableName + " WHERE id = " + itoa(secondID)
	if resp, err := tc.query(delSQL); err != nil {
		return err
	} else if resp.Code != 0 {
		return errf("TCP DELETE 失败: code=%d msg=%q", resp.Code, resp.Message)
	}
	return nil
}

// sqmpPGWireClientWork PG wire 客户端工作负载：复用单条长连接，发送 Simple Query 协议。
func sqmpPGWireClientWork(s *sqlServer, clientID int) error {
	c, err := dialPGWireErr(s.srv.PGAddr())
	if err != nil {
		return err
	}
	defer c.close()
	if err := c.handshakeErr(); err != nil {
		return err
	}

	// INSERT
	insertSQL := sqmpBuildInsertSQL(clientID)
	if res, err := c.sendQueryRead(insertSQL); err != nil {
		return err
	} else if res.errMsg != "" {
		return errf("PG wire INSERT 失败: %s", res.errMsg)
	}

	// SELECT 全部
	selectAll := "SELECT id, vendor, qty FROM " + sqmpTableName + " WHERE id >= " +
		itoa(sqmpIDBase+clientID*sqmpRowsPerClient) + " AND id < " +
		itoa(sqmpIDBase+(clientID+1)*sqmpRowsPerClient)
	if res, err := c.sendQueryRead(selectAll); err != nil {
		return err
	} else if res.errMsg != "" {
		return errf("PG wire SELECT 失败: %s", res.errMsg)
	}

	// UPDATE 首行
	firstID := sqmpIDBase + clientID*sqmpRowsPerClient
	updSQL := "UPDATE " + sqmpTableName + " SET qty = 9999 WHERE id = " + itoa(firstID)
	if res, err := c.sendQueryRead(updSQL); err != nil {
		return err
	} else if res.errMsg != "" {
		return errf("PG wire UPDATE 失败: %s", res.errMsg)
	}

	// DELETE 第二行
	secondID := firstID + 1
	delSQL := "DELETE FROM " + sqmpTableName + " WHERE id = " + itoa(secondID)
	if res, err := c.sendQueryRead(delSQL); err != nil {
		return err
	} else if res.errMsg != "" {
		return errf("PG wire DELETE 失败: %s", res.errMsg)
	}
	return nil
}

// sqmpBuildInsertSQL 构造指定 clientID 区间的多行 INSERT。
// 区间：[sqmpIDBase + clientID*sqmpRowsPerClient, sqmpIDBase + (clientID+1)*sqmpRowsPerClient)
func sqmpBuildInsertSQL(clientID int) string {
	base := sqmpIDBase + clientID*sqmpRowsPerClient
	prefix := "INSERT INTO " + sqmpTableName + " (id, vendor, qty) VALUES "
	parts := make([]string, 0, sqmpRowsPerClient)
	for i := 0; i < sqmpRowsPerClient; i++ {
		id := base + i
		parts = append(parts, "("+strconv.Itoa(id)+", 'v"+strconv.Itoa(clientID)+"', "+strconv.Itoa(i*10)+")")
	}
	return prefix + strings.Join(parts, ", ")
}

// itoa 是 strconv.Itoa 的本地缩写，避免在测试文件里到处写 strconv.Itoa。
func itoa(i int) string { return strconv.Itoa(i) }

// errf 是 fmt.Errorf 的本地缩写，便于在 goroutine 中返回带格式的错误。
func errf(format string, a ...any) error { return fmt.Errorf(format, a...) }
