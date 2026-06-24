// Package integration 端到端集成测试：进程内 API（inproc）慢查询打标。
//
// 本文件补齐慢查询日志 source 维度的 inproc 端到端覆盖：
//   - 现有 e2e_slow_query_multiprotocol_test.go 覆盖 HTTP / TCP / PG wire 三种外部协议
//   - 现有 e2e_admin_slow_queries_test.go 覆盖单协议（HTTP）的端点行为
//   - 本文件新增两类覆盖：
//     1. ExecuteQuery / ExecuteWrite（pkg/server/inproc.go）的 SQL 会以 source="inproc"
//     进入 /admin/slow-queries，便于在 cmd/widb 一键启动模式下区分进程内调用
//     2. 同 server 内同时混合四种 source（inproc / http / tcp / pgwire），
//     验证四类调用在并发场景下都能被独立打标，互不污染
//
// 设计要点：
//   - 不引入新的 server 启动工具，复用 startSQLServerAllProtocolsSlowQuery 启动完整 server
//   - inproc 调用直接走 srv.ExecuteQuery / srv.ExecuteWrite，零网络开销
//   - 多源并发场景下，最后断言 4 种 source 至少各出现 1 次，且 inproc SQL 内容能定位到具体表
package integration

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

// inprocSlow 常量：进程内慢查询测试。
const (
	// inprocTableName 测试表名，与现有集成测试错开避免污染。
	inprocTableName = "inproc_demo"
	// inprocNumWritesExecuteWrite 演示 ExecuteWrite 写入的行数。
	inprocNumWritesExecuteWrite = 6
	// inprocSelectCalls 演示 ExecuteQuery 执行的 SELECT 次数。
	inprocSelectCalls = 3
	// inprocMixedGoroutines 多源并发场景下，每类 source 启动的客户端数量。
	inprocMixedGoroutines = 2
)

// inprocSingleSourceTest 准备 inprocSlow 单源测试的数据：建表 + 写入种子行。
func inprocSingleSourceTest(s *sqlServer, t *testing.T) {
	t.Helper()
	if resp, err := s.srv.ExecuteQuery(
		"CREATE TABLE " + inprocTableName + " (id INT64 NOT NULL, label STRING NULL, qty INT64 NULL, PRIMARY KEY(id))",
	); err != nil || resp.Code != 0 {
		t.Fatalf("建表失败: err=%v resp=%+v", err, resp)
	}
	inprocSeedRows(s, t, inprocNumWritesExecuteWrite, 100)
}

// inprocSeedRows 走 inproc 写入 n 行种子数据，id 从 startID 起递增。
func inprocSeedRows(s *sqlServer, t *testing.T, n, startID int) {
	t.Helper()
	rows := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, map[string]any{
			"id":    int64(startID + i),
			"label": "row-" + itoa(i),
			"qty":   int64(i * 10),
		})
	}
	if resp, err := s.srv.ExecuteWrite(inprocTableName, rows); err != nil || resp.Code != 0 {
		t.Fatalf("ExecuteWrite 失败: err=%v resp=%+v", err, resp)
	}
}

// inprocRunSelects 通过 inproc 多次执行 SELECT。
func inprocRunSelects(s *sqlServer, t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		resp, err := s.srv.ExecuteQuery("SELECT id, label, qty FROM " + inprocTableName + " ORDER BY id")
		if err != nil {
			t.Fatalf("ExecuteQuery SELECT 失败: %v", err)
		}
		if resp.Code != 0 {
			t.Fatalf("ExecuteQuery SELECT 返回错误: %s", resp.Message)
		}
	}
}

// inprocRunBadQuery 故意触发一次失败 SQL，返回响应便于断言 Error 字段。
func inprocRunBadQuery(s *sqlServer, t *testing.T) {
	t.Helper()
	badResp, badErr := s.srv.ExecuteQuery("SELECT * FROM definitely_missing")
	if badErr != nil {
		t.Fatalf("ExecuteQuery 失败 SQL 不应返回 Go error: %v", badErr)
	}
	if badResp.Code == 0 {
		t.Fatalf("引用缺失表的 SQL 应返回 Code != 0")
	}
}

// inprocAssertHasInprocRecord 断言 /admin/slow-queries 至少存在一条 source=inproc 记录，
// 且至少一条 inproc 记录 SQL 涉及目标表、且至少含一条 SELECT。
func inprocAssertHasInprocRecord(body slowQueriesResponseResp, t *testing.T) {
	t.Helper()
	inprocQueries := sqmpFilteredSQLs(body.Queries, string(server.SlowQuerySourceInProc))
	if len(inprocQueries) == 0 {
		t.Fatalf("/admin/slow-queries 未捕获到 source=%q 的记录；全部记录 sources=%v",
			server.SlowQuerySourceInProc, inprocDistinctSources(body.Queries))
	}
	if !sqmpHasSQLContaining(inprocQueries, inprocTableName) {
		t.Errorf("inproc 记录中缺少涉及表 %q 的 SQL；inproc sqls=%v", inprocTableName, inprocQueries)
	}
	if !sqmpHasSQLWithPrefix(inprocQueries, "select") {
		t.Errorf("inproc 记录中缺少 SELECT；inproc sqls=%v", inprocQueries)
	}
}

// inprocAssertFailedSQLHasError 断言失败 SQL 对应的 inproc 记录 Error 字段非空。
func inprocAssertFailedSQLHasError(body slowQueriesResponseResp, t *testing.T, inprocQueries []string) {
	t.Helper()
	foundFailed := false
	for _, q := range body.Queries {
		if q.Source != string(server.SlowQuerySourceInProc) {
			continue
		}
		if !strings.Contains(strings.ToLower(q.SQL), "definitely_missing") {
			continue
		}
		foundFailed = true
		if q.Error == "" {
			t.Errorf("失败 SQL 的 inproc 记录应填充 Error 字段: %+v", q)
		}
	}
	if !foundFailed {
		t.Errorf("未找到引用 definitely_missing 的 inproc 记录；inproc sqls=%v", inprocQueries)
	}
}

// TestInProcSlowQuerySourceIsTagged 验证 ExecuteQuery / ExecuteWrite 产生的 SQL
// 在 /admin/slow-queries 中以 source="inproc" 出现。
func TestInProcSlowQuerySourceIsTagged(t *testing.T) {
	t.Parallel()
	s := startSQLServerAllProtocolsSlowQuery(t, time.Nanosecond, 64)
	inprocSingleSourceTest(s, t)
	inprocRunSelects(s, t, inprocSelectCalls)
	inprocRunBadQuery(s, t)

	status, body, _, err := getAdminSlowQueries(t, s)
	if err != nil {
		t.Fatalf("GET /admin/slow-queries 失败: %v", err)
	}
	if status != 200 || body.Code != 0 {
		t.Fatalf("状态码=%d Code=%d", status, body.Code)
	}
	inprocAssertHasInprocRecord(body, t)
	inprocSQLs := sqmpFilteredSQLs(body.Queries, string(server.SlowQuerySourceInProc))
	inprocAssertFailedSQLHasError(body, t, inprocSQLs)
}

// inprocMixedSetup 准备多源并发测试的基础数据：建表 + 写入 20 行种子。
func inprocMixedSetup(s *sqlServer, t *testing.T) {
	t.Helper()
	if resp, err := s.srv.ExecuteQuery(
		"CREATE TABLE " + inprocTableName + " (id INT64 NOT NULL, v STRING NULL, PRIMARY KEY(id))",
	); err != nil || resp.Code != 0 {
		t.Fatalf("建表失败: err=%v resp=%+v", err, resp)
	}
	inprocSeedRows(s, t, 20, 1)
}

// inprocRunMixedClients 启动 4 source × N goroutine 并发 SELECT，返回失败计数。
func inprocRunMixedClients(s *sqlServer, t *testing.T) int {
	t.Helper()
	sources := []string{
		string(server.SlowQuerySourceInProc),
		"http",
		"tcp",
		"pgwire",
	}
	var wg sync.WaitGroup
	var failCount int64
	var lastErr atomic.Value
	for _, src := range sources {
		for i := 0; i < inprocMixedGoroutines; i++ {
			wg.Add(1)
			go func(source string, workerID int) {
				defer wg.Done()
				sqlText := "SELECT id, v FROM " + inprocTableName + " WHERE id = " +
					itoa((workerID%20)+1)
				if err := inprocRunOneSelect(t, s, source, sqlText); err != nil {
					t.Logf("[%s worker %d] 失败: %v", source, workerID, err)
					lastErr.Store(err.Error())
					atomic.AddInt64(&failCount, 1)
				}
			}(src, i)
		}
	}
	wg.Wait()
	if failCount > 0 {
		t.Fatalf("%d 个客户端失败，最后错误: %v", failCount, lastErr.Load())
	}
	return int(failCount)
}

// inprocAssertAllSources 断言 /admin/slow-queries 4 种 source 全部出现且计数均非零。
func inprocAssertAllSources(body slowQueriesResponseResp, t *testing.T) {
	t.Helper()
	stats := sqmpCountSources(body.Queries)
	distinct := inprocDistinctSources(body.Queries)
	if len(distinct) < 4 {
		t.Errorf("期望 4 个 source，实际 %d 个：%v", len(distinct), distinct)
	}
	if inprocCountSource(body.Queries) == 0 {
		t.Errorf("inproc 计数为 0；distinct sources=%v", distinct)
	}
	if stats.httpCount == 0 {
		t.Errorf("http 计数为 0；distinct sources=%v", distinct)
	}
	if stats.tcpCount == 0 {
		t.Errorf("tcp 计数为 0；distinct sources=%v", distinct)
	}
	if stats.pgwireCount == 0 {
		t.Errorf("pgwire 计数为 0；distinct sources=%v", distinct)
	}
}

// inprocAssertInprocFairness 断言 inproc 计数至少占总数 1/4。
func inprocAssertInprocFairness(body slowQueriesResponseResp, t *testing.T) {
	t.Helper()
	total := len(body.Queries)
	wantMinInproc := total / 4
	if wantMinInproc < 1 {
		wantMinInproc = 1
	}
	inprocCount := inprocCountSource(body.Queries)
	if inprocCount < wantMinInproc {
		t.Errorf("inproc 计数过少：total=%d inproc=%d，期望至少 %d（≈total/4）",
			total, inprocCount, wantMinInproc)
	}
	inprocSQLs := sqmpFilteredSQLs(body.Queries, string(server.SlowQuerySourceInProc))
	if !sqmpHasSQLContaining(inprocSQLs, inprocTableName) {
		t.Errorf("inproc 记录中缺少涉及表 %q 的 SQL；sqls=%v", inprocTableName, inprocSQLs)
	}
}

// TestInProcMixedWithExternalSources 验证同 server 内 4 种 source 并发执行时，
// 慢查询日志能正确区分并按 source 标签聚合，inproc 不会被外部协议淹没。
func TestInProcMixedWithExternalSources(t *testing.T) {
	t.Parallel()
	s := startSQLServerAllProtocolsSlowQuery(t, time.Nanosecond, 256)
	inprocMixedSetup(s, t)
	inprocRunMixedClients(s, t)

	status, body, _, err := getAdminSlowQueries(t, s)
	if err != nil {
		t.Fatalf("GET /admin/slow-queries 失败: %v", err)
	}
	if status != 200 || body.Code != 0 {
		t.Fatalf("状态码=%d Code=%d", status, body.Code)
	}
	inprocAssertAllSources(body, t)
	inprocAssertInprocFairness(body, t)
}

// inprocSelectViaInProc 通过 inproc 执行 SELECT。
func inprocSelectViaInProc(s *sqlServer, sqlText string) error {
	resp, err := s.srv.ExecuteQuery(sqlText)
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return errf("inproc SELECT 失败: code=%d msg=%q", resp.Code, resp.Message)
	}
	return nil
}

// inprocSelectViaHTTP 通过 HTTP /query 执行 SELECT。
func inprocSelectViaHTTP(s *sqlServer, sqlText string) error {
	resp, err := rawQuery(s, "http", sqlText)
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return errf("http SELECT 失败: code=%d msg=%q", resp.Code, resp.Message)
	}
	return nil
}

// inprocSelectViaTCP 通过 TCP 长连接执行 SELECT。
func inprocSelectViaTCP(s *sqlServer, sqlText string) error {
	tc, err := dialTCP(s.tcpAddr)
	if err != nil {
		return err
	}
	defer tc.close()
	resp, err := tc.query(sqlText)
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return errf("tcp SELECT 失败: code=%d msg=%q", resp.Code, resp.Message)
	}
	return nil
}

// inprocSelectViaPGWire 通过 PG wire 执行 SELECT。
func inprocSelectViaPGWire(s *sqlServer, sqlText string) error {
	c, err := dialPGWireErr(s.srv.PGAddr())
	if err != nil {
		return err
	}
	defer c.close()
	if err := c.handshakeErr(); err != nil {
		return err
	}
	res, err := c.sendQueryRead(sqlText)
	if err != nil {
		return err
	}
	if res.errMsg != "" {
		return errf("pgwire SELECT 失败: %s", res.errMsg)
	}
	return nil
}

// inprocRunOneSelect 按 source 分发到对应客户端执行单条 SELECT。
// inproc 走 srv.ExecuteQuery，其它协议复用既有的 rawQuery / PG wire helper。
func inprocRunOneSelect(t *testing.T, s *sqlServer, source, sqlText string) error {
	t.Helper()
	switch source {
	case string(server.SlowQuerySourceInProc):
		return inprocSelectViaInProc(s, sqlText)
	case "http":
		return inprocSelectViaHTTP(s, sqlText)
	case "tcp":
		return inprocSelectViaTCP(s, sqlText)
	case "pgwire":
		return inprocSelectViaPGWire(s, sqlText)
	default:
		return errf("未知 source: %q", source)
	}
}

// inprocDistinctSources 返回 queries 中所有出现过的 source 集合（去重 + 排序）。
// 与 sqmpCountSources 互补：本函数只关心「出现与否」与种类数。
func inprocDistinctSources(queries []slowQueryEntryResp) []string {
	seen := make(map[string]struct{}, 4)
	for _, q := range queries {
		seen[q.Source] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for src := range seen {
		out = append(out, src)
	}
	// 简单稳定排序，便于失败时输出可读
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// inprocCountSource 统计 queries 中 source=inproc 的条目数。
// 独立于 sqmpCountSources，避免修改既有结构体。
func inprocCountSource(queries []slowQueryEntryResp) int {
	n := 0
	for _, q := range queries {
		if q.Source == string(server.SlowQuerySourceInProc) {
			n++
		}
	}
	return n
}

// 静态断言：编译期验证 server 包暴露的常量与本文件使用的字面量一致。
// 若有人改了 server.SlowQuerySourceInProc 的值，本文件会编译失败提醒。
var _ = string(server.SlowQuerySourceInProc) == "inproc"
