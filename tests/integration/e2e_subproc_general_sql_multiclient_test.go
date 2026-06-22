// Package integration 端到端集成测试：真实 cmd/server 子进程 + 多客户端一般 SQL 工作负载。
//
// 本文件补齐既有测试未覆盖的"部署维度"：把 cmd/server 编译为子进程拉起，
// 通过 TCP 长连接 + HTTP 短连接多客户端并发执行一般 SQL（DDL + DML + DQL
// 混合），验证真实部署场景下 server 的正确性、并发安全性与跨协议一致性。
//
// 与既有测试的区别：
//   - e2e_subproc_smoke_test.go：聚焦 flag 解析、退出码、/health、/metrics，
//     客户端层面只跑单条 SQL，未做多客户端并发一般工作负载。
//   - e2e_general_sql_multiclient_test.go：使用同进程 *server.Server，
//     验证内部协议/逻辑正确性，但未走真实子进程 → 真实部署链路。
//   - e2e_mrpcm_multiprotocol_test.go：使用同进程 *server.Server，侧重三协议
//     交错 + 重启持久化，未做大规模一般 SQL 覆盖。
//
// 本文件是第一份"子进程 server + 多客户端 + 一般 SQL 模板"的组合测试。
//
// 设计要点：
//  1. 子进程复用 e2e_subproc_smoke_test.go 的 buildSubprocBinary /
//     startSubprocessServer / stopSubprocessServer 等 helper。
//  2. TCP 客户端复用同进程 e2e_server_sql_test.go 的 dialTCP / tcpClient
//     协议（PacketQuery + JSON payload），HTTP 端点直连。
//  3. 每个 worker 写"自己 ID 区间"的行，避免并发误判；总写入量 = 客户端数 ×
//     每客户端行数，方便精确断言 COUNT。
//  4. SELECT 通过 TCP 与 HTTP 各读一次，验证跨协议结果一致。
//  5. 错误路径（重复主键、未知列）经子进程返回非零 code 验证。
//
// 并发测试规范：worker goroutine 内不调用 t.Fatal/t.Errorf，统一通过 error
// channel 汇总到主 goroutine 后再断言（与 e2e_realistic_business_sql_test.go
// 一致）。HTTP 错误信息透传时也用 error 而非 t.Fatal。
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// subprocGen 测试常量。
const (
	subprocGenTable     = "subproc_gen_kv" // 共享表名
	subprocGenClients   = 4                // 并发客户端数（2 TCP + 2 HTTP）
	subprocGenPerClient = 12               // 每客户端写入行数
	subprocGenBaseID    = 100000           // 客户端 ID 起始偏移，避免与其它测试冲突
	subprocGenRounds    = 3                // 每客户端工作负载轮数
)

// subprocGenCreateSQL 建表语句：4 列含 4 种类型 + NULLABLE 列。
func subprocGenCreateSQL() string {
	return "CREATE TABLE " + subprocGenTable + " (" +
		"id INT64 NOT NULL, " +
		"name STRING NULL, " +
		"score FLOAT64 NULL, " +
		"active BOOL NULL, " +
		"PRIMARY KEY(id))"
}

// subprocGenInsertSQL 生成 (clientID, seq) 对应的 INSERT SQL。
//
// score 始终为正，active 为 INT64 0/1（避免负号 UnaryExpr 与 BOOL true/false
// UnaryExpr 在当前 parser 下无法在 INSERT VALUES 列表中处理）。
func subprocGenInsertSQL(clientID, seq int) string {
	id := subprocGenBaseID + clientID*subprocGenPerClient + seq
	score := float64(id) * 0.1
	active := int64(0)
	if seq%2 == 0 {
		active = 1
	}
	return fmt.Sprintf(
		"INSERT INTO %s (id, name, score, active) VALUES (%d, 'gen-%d-%d', %.2f, %d)",
		subprocGenTable, id, clientID, seq, score, active,
	)
}

// subprocGenUpdateSQL 生成 (clientID, seq, round) 对应的 UPDATE SQL。
//
// 用 (round, score) 作为固定值，避免跨轮次累加影响最终 SUM 断言。
func subprocGenUpdateSQL(clientID, seq, round int) string {
	id := subprocGenBaseID + clientID*subprocGenPerClient + seq
	score := float64(round+1) * float64(id) * 0.01
	return fmt.Sprintf(
		"UPDATE %s SET score = %.4f WHERE id = %d",
		subprocGenTable, score, id,
	)
}

// subprocGenClientIDRange 决定本 worker 应写入/更新的 ID 区间：[lo, hi)。
func subprocGenClientIDRange(clientID int) (lo, hi int64) {
	lo = int64(subprocGenBaseID + clientID*subprocGenPerClient)
	hi = lo + int64(subprocGenPerClient)
	return
}

// subprocGenRangeCountSQL 拼接 COUNT(*) 校验 SQL。
func subprocGenRangeCountSQL(lo, hi int64) string {
	return fmt.Sprintf("SELECT COUNT(*) AS c FROM %s WHERE id >= %d AND id < %d",
		subprocGenTable, lo, hi)
}

// subprocGenRangeAvgSQL 拼接 AVG(score) 校验 SQL。
func subprocGenRangeAvgSQL(lo, hi int64) string {
	return fmt.Sprintf("SELECT AVG(score) AS a FROM %s WHERE id >= %d AND id < %d",
		subprocGenTable, lo, hi)
}

// httpPostQueryNoT 通过 HTTP POST /query 执行单条 SQL，返回 (code, message, rows, data, err)。
//
// 不依赖 *testing.T，以便在 goroutine 内调用。所有错误通过返回值传递。
func httpPostQueryNoT(ctx context.Context, addr, sql string) (int, string, int, json.RawMessage, error) {
	body, _ := json.Marshal(map[string]string{"sql": sql})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+addr+"/query", bytes.NewReader(body))
	if err != nil {
		return -1, "", 0, nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return -1, "", 0, nil, fmt.Errorf("POST /query 失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return -1, "", 0, nil, fmt.Errorf("读取 /query 响应失败: %w", err)
	}
	var out serverQueryResp
	if err := json.Unmarshal(data, &out); err != nil {
		return -1, "", 0, nil, fmt.Errorf("解析 /query 响应失败: %w (raw: %s)", err, data)
	}
	return out.Code, out.Message, out.Rows, out.Data, nil
}

// subprocGenHTTPInsertRange 通过 HTTP 短连接在自身 ID 区间内批量 INSERT。
func subprocGenHTTPInsertRange(ctx context.Context, addr string, clientID int, errCh chan<- error) error {
	for seq := 0; seq < subprocGenPerClient; seq++ {
		sql := subprocGenInsertSQL(clientID, seq)
		code, msg, _, _, err := httpPostQueryNoT(ctx, addr, sql)
		if err != nil {
			errCh <- fmt.Errorf("http 客户端 %d INSERT 失败: %w", clientID, err)
			return err
		}
		if code != 0 {
			errCh <- fmt.Errorf("http 客户端 %d INSERT 业务失败: %s", clientID, msg)
			return fmt.Errorf("%s", msg)
		}
	}
	return nil
}

// subprocGenHTTPCheckCount 通过 HTTP 校验 COUNT(*) 等于 subprocGenPerClient。
func subprocGenHTTPCheckCount(ctx context.Context, addr string, clientID, round int, lo, hi int64, errCh chan<- error) error {
	code, msg, _, countData, err := httpPostQueryNoT(ctx, addr, subprocGenRangeCountSQL(lo, hi))
	if err != nil {
		errCh <- fmt.Errorf("http 客户端 %d 第 %d 轮 COUNT 失败: %w", clientID, round, err)
		return err
	}
	if code != 0 {
		errCh <- fmt.Errorf("http 客户端 %d 第 %d 轮 COUNT 业务失败: %s", clientID, round, msg)
		return fmt.Errorf("%s", msg)
	}
	if got := subprocGenExtractCountJSON(countData); got != subprocGenPerClient {
		errCh <- fmt.Errorf("http 客户端 %d 第 %d 轮 COUNT = %d, 期望 %d", clientID, round, got, subprocGenPerClient)
		return fmt.Errorf("count mismatch")
	}
	return nil
}

// subprocGenHTTPUpdateRange 通过 HTTP 短连接在自身 ID 区间内批量 UPDATE。
func subprocGenHTTPUpdateRange(ctx context.Context, addr string, clientID, round int, errCh chan<- error) error {
	for seq := 0; seq < subprocGenPerClient; seq++ {
		sql := subprocGenUpdateSQL(clientID, seq, round)
		code, msg, rows, _, err := httpPostQueryNoT(ctx, addr, sql)
		if err != nil {
			errCh <- fmt.Errorf("http 客户端 %d 第 %d 轮 UPDATE 失败: %w", clientID, round, err)
			return err
		}
		if code != 0 {
			errCh <- fmt.Errorf("http 客户端 %d 第 %d 轮 UPDATE 业务失败: %s", clientID, round, msg)
			return fmt.Errorf("%s", msg)
		}
		if rows != 1 {
			errCh <- fmt.Errorf("http 客户端 %d 第 %d 轮 UPDATE 影响行数 = %d, 期望 1", clientID, round, rows)
			return fmt.Errorf("rows mismatch")
		}
	}
	return nil
}

// subprocGenRunHTTPWorker 通过 HTTP 短连接完成本客户端的工作负载。
//
// 不复用 TCP 长连接是为了验证 HTTP 模式（无连接池）下的并发正确性。
// INSERT 仅在第 0 轮做（同一主键多次 INSERT 会冲突），后续轮次只 UPDATE。
func subprocGenRunHTTPWorker(
	ctx context.Context, addr string, clientID, rounds int, errCh chan<- error,
) {
	lo, hi := subprocGenClientIDRange(clientID)
	for round := 0; round < rounds; round++ {
		if ctx.Err() != nil {
			return
		}
		if round == 0 {
			if e := subprocGenHTTPInsertRange(ctx, addr, clientID, errCh); e != nil {
				return
			}
		}
		if e := subprocGenHTTPCheckCount(ctx, addr, clientID, round, lo, hi, errCh); e != nil {
			return
		}
		if e := subprocGenHTTPUpdateRange(ctx, addr, clientID, round, errCh); e != nil {
			return
		}
	}
}

// subprocGenTCPInsertRange 通过 TCP 在自身 ID 区间内批量 INSERT。
func subprocGenTCPInsertRange(t *testing.T, tc *tcpClient, clientID int, errCh chan<- error) error {
	t.Helper()
	for seq := 0; seq < subprocGenPerClient; seq++ {
		sql := subprocGenInsertSQL(clientID, seq)
		resp, err := tc.query(sql)
		if err != nil {
			errCh <- fmt.Errorf("tcp 客户端 %d INSERT 失败: %w", clientID, err)
			return err
		}
		if resp.Code != 0 {
			errCh <- fmt.Errorf("tcp 客户端 %d INSERT 业务失败: %s", clientID, resp.Message)
			return fmt.Errorf("%s", resp.Message)
		}
	}
	return nil
}

// subprocGenTCPCheckCount 通过 TCP 校验 COUNT(*) 等于 subprocGenPerClient。
func subprocGenTCPCheckCount(t *testing.T, tc *tcpClient, clientID, round int, lo, hi int64, errCh chan<- error) error {
	t.Helper()
	resp, err := tc.query(subprocGenRangeCountSQL(lo, hi))
	if err != nil {
		errCh <- fmt.Errorf("tcp 客户端 %d 第 %d 轮 COUNT 查询失败: %w", clientID, round, err)
		return err
	}
	if resp.Code != 0 {
		errCh <- fmt.Errorf("tcp 客户端 %d 第 %d 轮 COUNT 业务失败: %s", clientID, round, resp.Message)
		return fmt.Errorf("%s", resp.Message)
	}
	if got := subprocGenExtractCount(t, resp.Data); got != subprocGenPerClient {
		errCh <- fmt.Errorf("tcp 客户端 %d 第 %d 轮 COUNT = %d, 期望 %d", clientID, round, got, subprocGenPerClient)
		return fmt.Errorf("count mismatch")
	}
	return nil
}

// subprocGenTCPUpdateRange 通过 TCP 在自身 ID 区间内批量 UPDATE。
func subprocGenTCPUpdateRange(t *testing.T, tc *tcpClient, clientID, round int, errCh chan<- error) error {
	t.Helper()
	for seq := 0; seq < subprocGenPerClient; seq++ {
		sql := subprocGenUpdateSQL(clientID, seq, round)
		resp, err := tc.query(sql)
		if err != nil {
			errCh <- fmt.Errorf("tcp 客户端 %d 第 %d 轮 UPDATE 失败: %w", clientID, round, err)
			return err
		}
		if resp.Code != 0 {
			errCh <- fmt.Errorf("tcp 客户端 %d 第 %d 轮 UPDATE 业务失败: %s", clientID, round, resp.Message)
			return fmt.Errorf("%s", resp.Message)
		}
		if resp.Rows != 1 {
			errCh <- fmt.Errorf("tcp 客户端 %d 第 %d 轮 UPDATE 影响行数 = %d, 期望 1", clientID, round, resp.Rows)
			return fmt.Errorf("rows mismatch")
		}
	}
	return nil
}

// subprocGenTCPCheckAvg 通过 TCP 校验 AVG 查询返回 1 行。
func subprocGenTCPCheckAvg(t *testing.T, tc *tcpClient, clientID, round int, lo, hi int64, errCh chan<- error) error {
	t.Helper()
	resp, err := tc.query(subprocGenRangeAvgSQL(lo, hi))
	if err != nil {
		errCh <- fmt.Errorf("tcp 客户端 %d 第 %d 轮 AVG 查询失败: %w", clientID, round, err)
		return err
	}
	if resp.Code != 0 {
		errCh <- fmt.Errorf("tcp 客户端 %d 第 %d 轮 AVG 业务失败: %s", clientID, round, resp.Message)
		return fmt.Errorf("%s", resp.Message)
	}
	if resp.Rows != 1 {
		errCh <- fmt.Errorf("tcp 客户端 %d 第 %d 轮 AVG 行数 = %d, 期望 1", clientID, round, resp.Rows)
		return fmt.Errorf("rows mismatch")
	}
	return nil
}

// subprocGenRunTCPWorker 持长连接完成本客户端的工作负载。
//
// 第 0 轮：INSERT；每轮：COUNT → UPDATE → AVG。INSERT/UPDATE/AVG 各自
// 由 helper 函数实现，便于将单函数长度与圈复杂度都控制在 CI 阈值内。
func subprocGenRunTCPWorker(
	ctx context.Context, t *testing.T, addr string, clientID, rounds int, errCh chan<- error,
) {
	tc, err := dialTCP(addr)
	if err != nil {
		errCh <- fmt.Errorf("tcp 拨号失败: %w", err)
		return
	}
	defer tc.close()
	lo, hi := subprocGenClientIDRange(clientID)

	for round := 0; round < rounds; round++ {
		if ctx.Err() != nil {
			return
		}
		if round == 0 {
			if e := subprocGenTCPInsertRange(t, tc, clientID, errCh); e != nil {
				return
			}
		}
		if e := subprocGenTCPCheckCount(t, tc, clientID, round, lo, hi, errCh); e != nil {
			return
		}
		if e := subprocGenTCPUpdateRange(t, tc, clientID, round, errCh); e != nil {
			return
		}
		if e := subprocGenTCPCheckAvg(t, tc, clientID, round, lo, hi, errCh); e != nil {
			return
		}
	}
}

// subprocGenExtractCount 从 SELECT COUNT(*) 响应中提取整数结果（TCP 路径）。
//
// server.Response.Data 已经是 []any（解析后），取第一行第一列转 int64。
func subprocGenExtractCount(t *testing.T, data any) int64 {
	t.Helper()
	if data == nil {
		return -1
	}
	rows, ok := data.([]any)
	if !ok || len(rows) == 0 {
		return -1
	}
	row, ok := rows[0].(map[string]any)
	if !ok {
		return -1
	}
	for _, v := range row {
		if n, ok := toInt64(v); ok {
			return n
		}
	}
	return -1
}

// subprocGenExtractCountJSON 从 json.RawMessage 提取 COUNT 值（HTTP 路径）。
func subprocGenExtractCountJSON(data json.RawMessage) int64 {
	if len(data) == 0 || string(data) == "null" {
		return -1
	}
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil || len(rows) == 0 {
		return -1
	}
	for _, v := range rows[0] {
		if n, ok := toInt64(v); ok {
			return n
		}
	}
	return -1
}

// subprocGenCheckCrossProtocolConsistency 通过 TCP 与 HTTP 各读一次，验证结果一致。
//
// id 列经排序后应完全相同，否则视为协议间结果不一致。
func subprocGenCheckCrossProtocolConsistency(
	t *testing.T, s *subprocServer, expectedIDs []int64,
) {
	t.Helper()
	expected := make([]int64, len(expectedIDs))
	copy(expected, expectedIDs)
	sort.Slice(expected, func(i, j int) bool { return expected[i] < expected[j] })

	httpCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _, _, httpData, err := httpPostQueryNoT(httpCtx, s.httpAddr,
		fmt.Sprintf("SELECT id FROM %s ORDER BY id", subprocGenTable))
	if err != nil {
		t.Fatalf("HTTP SELECT id 失败: %v", err)
	}
	gotHTTP := subprocGenParseIDColumn(t, httpData)
	if !int64SliceEqual(gotHTTP, expected) {
		t.Errorf("HTTP 返回的 id 集合与期望不一致\n期望: %v\n实际: %v", expected, gotHTTP)
	}

	tc, err := dialTCP(s.tcpAddr)
	if err != nil {
		t.Fatalf("TCP 拨号失败: %v", err)
	}
	defer tc.close()
	tcpResp, err := tc.query(
		fmt.Sprintf("SELECT id FROM %s ORDER BY id", subprocGenTable))
	if err != nil {
		t.Fatalf("TCP SELECT id 失败: %v", err)
	}
	if tcpResp.Code != 0 {
		t.Fatalf("TCP SELECT id 业务失败: %s", tcpResp.Message)
	}
	tcpData, err := json.Marshal(tcpResp.Data)
	if err != nil {
		t.Fatalf("marshal TCP Data 失败: %v", err)
	}
	gotTCP := subprocGenParseIDColumn(t, json.RawMessage(tcpData))
	if !int64SliceEqual(gotTCP, expected) {
		t.Errorf("TCP 返回的 id 集合与期望不一致\n期望: %v\n实际: %v", expected, gotTCP)
	}
}

// subprocGenParseIDColumn 从响应 Data 解析 id 列（支持 []any 与 []map[string]any）。
func subprocGenParseIDColumn(t *testing.T, data json.RawMessage) []int64 {
	t.Helper()
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatalf("解析响应 data 失败: %v (raw: %s)", err, data)
	}
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		v, ok := r["id"]
		if !ok {
			t.Fatalf("响应行缺少 id 字段: %v", r)
		}
		id, ok := toInt64(v)
		if !ok {
			t.Fatalf("id 字段不是整数: %v", v)
		}
		out = append(out, id)
	}
	return out
}

// int64SliceEqual 比较两个 int64 slice 是否完全相等。
func int64SliceEqual(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// subprocGenCheckErrorPaths 验证子进程对错误 SQL 返回非零 code。
func subprocGenCheckErrorPaths(t *testing.T, s *subprocServer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. 重复主键 INSERT
	dupID := int64(subprocGenBaseID)
	dupSQL := fmt.Sprintf("INSERT INTO %s (id, name, score, active) VALUES (%d, 'dup', 0.0, 1)",
		subprocGenTable, dupID)
	code, _, _, _, err := httpPostQueryNoT(ctx, s.httpAddr, dupSQL)
	if err != nil {
		t.Fatalf("重复主键 INSERT 请求失败: %v", err)
	}
	if code == 0 {
		t.Errorf("重复主键 INSERT 应返回非零 code, 实际为 0")
	}

	// 2. 未知列 SELECT
	badColSQL := fmt.Sprintf("SELECT non_existing_col FROM %s LIMIT 1", subprocGenTable)
	code, _, _, _, err = httpPostQueryNoT(ctx, s.httpAddr, badColSQL)
	if err != nil {
		t.Fatalf("未知列 SELECT 请求失败: %v", err)
	}
	if code == 0 {
		t.Errorf("未知列 SELECT 应返回非零 code, 实际为 0")
	}

	// 3. 错误 SQL 语法
	bareSQL := "THIS IS NOT SQL"
	code, _, _, _, err = httpPostQueryNoT(ctx, s.httpAddr, bareSQL)
	if err != nil {
		t.Fatalf("错误语法请求失败: %v", err)
	}
	if code == 0 {
		t.Errorf("错误语法应返回非零 code, 实际为 0")
	}

	// 4. 正常 LIMIT 工作
	goodLimitSQL := fmt.Sprintf("SELECT id FROM %s ORDER BY id LIMIT %d", subprocGenTable, subprocGenPerClient)
	code, msg, _, _, err := httpPostQueryNoT(ctx, s.httpAddr, goodLimitSQL)
	if err != nil {
		t.Fatalf("正常 LIMIT 查询请求失败: %v", err)
	}
	if code != 0 {
		t.Errorf("正常 LIMIT 查询被错误路径影响: %s", msg)
	}
}

// subprocGenExpectedIDs 拼接全部 worker ID 区间的全集（按升序）。
func subprocGenExpectedIDs() []int64 {
	out := make([]int64, 0, subprocGenClients*subprocGenPerClient)
	for c := 0; c < subprocGenClients; c++ {
		lo, hi := subprocGenClientIDRange(c)
		for id := lo; id < hi; id++ {
			out = append(out, id)
		}
	}
	return out
}

// subprocGenRunWorkers 启动 2 TCP + 2 HTTP worker 并等待完成。
//
// 返回值：errs 切片（空表示全部成功）、TCP/HTTP 各自完成数。
func subprocGenRunWorkers(t *testing.T, s *subprocServer) (errs []error, tcpOK, httpOK int64) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	errCh := make(chan error, subprocGenClients*subprocGenRounds*subprocGenPerClient*2)
	for i := 0; i < subprocGenClients; i++ {
		i := i
		isTCP := i%2 == 0
		wg.Add(1)
		go func() {
			defer wg.Done()
			if isTCP {
				subprocGenRunTCPWorker(ctx, t, s.tcpAddr, i, subprocGenRounds, errCh)
				atomic.AddInt64(&tcpOK, 1)
			} else {
				subprocGenRunHTTPWorker(ctx, s.httpAddr, i, subprocGenRounds, errCh)
				atomic.AddInt64(&httpOK, 1)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		errs = append(errs, e)
	}
	return
}

// TestSubprocGeneralSQLMultiClient 主测试：子进程 + 多客户端一般 SQL 端到端。
//
// 流程：
//  1. 拉起 cmd/server 子进程（独立临时 DataDir）
//  2. HTTP 建表
//  3. 启动 2 TCP + 2 HTTP worker 并发执行 INSERT/UPDATE/COUNT/AVG（subprocGenRounds 轮）
//  4. 跨协议一致性校验：TCP 与 HTTP 各读一次全集，对比 id 集合
//  5. 错误路径校验：重复主键、未知列、错误语法应返回非零 code
//  6. SIGTERM 子进程，验证优雅退出
func TestSubprocGeneralSQLMultiClient(t *testing.T) {
	dir := t.TempDir()
	s, log := startSubprocessServer(t,
		allocateEphemeralPort(t), allocateEphemeralPort(t), dir)
	t.Cleanup(func() {
		if s != nil {
			stopSubprocessServer(t, s)
		}
		if t.Failed() {
			t.Logf("子进程日志:\n%s", log.String())
		}
	})

	hp := httpHealthHit(t, s.httpAddr)
	_ = hp.Body.Close()
	if hp.StatusCode != 200 {
		t.Fatalf("/health 状态码 = %d, want 200", hp.StatusCode)
	}

	createResp := httpPostQuery(t, s.httpAddr, subprocGenCreateSQL())
	if createResp.Code != 0 {
		t.Fatalf("建表失败: %s", createResp.Message)
	}

	errs, tcpOK, httpOK := subprocGenRunWorkers(t, s)
	if len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("worker 错误: %v", e)
		}
		t.FailNow()
	}
	if got := atomic.LoadInt64(&tcpOK); got != int64(subprocGenClients/2) {
		t.Errorf("TCP worker 完成数 = %d, 期望 %d", got, subprocGenClients/2)
	}
	if got := atomic.LoadInt64(&httpOK); got != int64(subprocGenClients/2) {
		t.Errorf("HTTP worker 完成数 = %d, 期望 %d", got, subprocGenClients/2)
	}

	subprocGenCheckCrossProtocolConsistency(t, s, subprocGenExpectedIDs())
	subprocGenCheckErrorPaths(t, s)

	sendSignalToSubprocess(t, s, syscall.SIGTERM)
	code, err := waitForSubprocessExit(t, s, subprocStopTimeout)
	if err != nil && code != 0 {
		t.Errorf("子进程退出码 = %d, 期望 0; err = %v", code, err)
	}
}

// TestSubprocGeneralSQLHealthMetricsOnly 最小子进程烟测：/health + /metrics + 1 条 SELECT。
func TestSubprocGeneralSQLHealthMetricsOnly(t *testing.T) {
	dir := t.TempDir()
	s, log := startSubprocessServer(t,
		allocateEphemeralPort(t), allocateEphemeralPort(t), dir)
	t.Cleanup(func() {
		if s != nil {
			stopSubprocessServer(t, s)
		}
		if t.Failed() {
			t.Logf("子进程日志:\n%s", log.String())
		}
	})

	hp := httpHealthHit(t, s.httpAddr)
	_ = hp.Body.Close()
	if hp.StatusCode != 200 {
		t.Fatalf("/health 状态码 = %d, want 200", hp.StatusCode)
	}
	metrics := httpMetricsHit(t, s.httpAddr)
	if len(metrics) == 0 {
		t.Fatal("/metrics 返回空")
	}

	createResp := httpPostQuery(t, s.httpAddr, subprocGenCreateSQL())
	if createResp.Code != 0 {
		t.Fatalf("建表失败: %s", createResp.Message)
	}
	resp := httpPostQuery(t, s.httpAddr, "SELECT COUNT(*) AS c FROM "+subprocGenTable)
	if resp.Code != 0 {
		t.Errorf("COUNT 空表失败: %s", resp.Message)
	}
	if resp.Rows != 1 {
		t.Errorf("COUNT 空表返回 %d 行, 期望 1", resp.Rows)
	}

	sendSignalToSubprocess(t, s, syscall.SIGTERM)
	code, err := waitForSubprocessExit(t, s, subprocStopTimeout)
	if err != nil && code != 0 {
		t.Errorf("子进程退出码 = %d, 期望 0; err = %v", code, err)
	}
}
