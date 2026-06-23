// Package integration 端到端集成测试：子进程 server + 多客户端 + 双引擎（LSM + memory）混合工作负载。
//
// 本文件补齐既有测试未覆盖的「部署维度 + 引擎混合维度」组合：把 cmd/server
// 编译为子进程拉起，同时创建 LSM 引擎表与 memory 引擎表（`ENGINE=memory`），
// 通过 TCP 长连接 + HTTP 短连接多客户端并发执行一般 SQL，验证真实部署场景
// 下双引擎混合路由的正确性、并发安全性、跨协议一致性，以及重启后两引擎的
// 持久化语义差异（LSM 持久化、memory 重启即丢）。
//
// 与既有测试的区别：
//   - e2e_subproc_general_sql_multiclient_test.go：子进程 + 多客户端 + 一般 SQL，
//     但只使用单一 LSM 引擎表，未覆盖 `ENGINE=memory` 与双引擎共存路由。
//   - e2e_mixed_engine_long_workload_test.go：双引擎混合 + 多客户端 + 跨协议，
//     但使用同进程 *server.Server，未走真实子进程 → 真实部署链路。
//   - e2e_memory_engine_restart_test.go：memory 引擎重启语义，但仅用同进程
//     server 验证「重启即丢」单一断言。
//
// 本文件是第一份「子进程 server + 双引擎混合 + 多客户端 + 跨协议 + 重启语义」
// 的组合测试，验证 routingAdapter 在多表混合路由下不出现串扰，并固化两引擎
// 的持久化语义差异（LSM 持久、memory 重启即丢）在真实子进程部署下也成立。
//
// 设计要点：
//  1. 子进程复用 e2e_subproc_smoke_test.go 的 buildSubprocBinary /
//     startSubprocessServer / stopSubprocessServer 等 helper。
//  2. TCP 客户端复用同进程 e2e_server_sql_test.go 的 dialTCP / tcpClient
//     协议（PacketQuery + JSON payload），HTTP 端点直连 /query。
//  3. 每 worker 写「自己 ID 区间」的两张表行，避免并发误判；总写入量 =
//     客户端数 × 每客户端行数，方便精确断言 COUNT。
//  4. SELECT 通过 TCP 与 HTTP 各读一次，验证跨协议结果一致；两表分别校验，
//     确保 engine 路由不串扰。
//  5. 错误路径（重复主键、未知列、错误语法）经子进程返回非零 code 验证。
//  6. 重启阶段：拉起新子进程使用同一 DataDir，校验 LSM 表数据完整保留、
//     memory 表 COUNT = 0。
//  7. 复杂度管控：每个 worker / 测试函数都拆成「编排 + 单步」两层，单步函数
//     圈复杂度 ≤ 5；编排函数只做循环与错误传播。
//
// 并发测试规范：worker goroutine 内不调用 t.Fatal/t.Errorf，统一通过 error
// channel 汇总到主 goroutine 后再断言。
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
	"syscall"
	"testing"
	"time"
)

// 子进程双引擎混合多客户端测试常量。
const (
	mxLsmTable     = "mx_lsm_kv" // LSM 引擎共享表名
	mxMemTable     = "mx_mem_kv" // memory 引擎共享表名
	mxClients      = 4           // 并发客户端数（2 TCP + 2 HTTP）
	mxPerClient    = 10          // 每客户端写入两表的行数
	mxBaseID       = 700000      // 客户端 ID 起始偏移，避免与其它测试冲突
	mxRounds       = 2           // 每客户端工作负载轮数
	mxRestartTagID = 999000      // 重启后用于验证 LSM 持久化的探针 ID
)

// mxTables 是两表名切片，用于跨表循环。所有双表场景（INSERT/COUNT/UPDATE/
// 错误路径）都通过遍历该切片实现，避免在多处硬编码 [mxLsmTable, mxMemTable]。
var mxTables = []string{mxLsmTable, mxMemTable}

// mxLSMCreateSQL 建表语句：LSM 引擎，4 列含 3 种类型 + NULLABLE。
func mxLSMCreateSQL() string {
	return "CREATE TABLE " + mxLsmTable + " (" +
		"id INT64 NOT NULL, " +
		"name STRING NULL, " +
		"score FLOAT64 NULL, " +
		"active BOOL NULL, " +
		"PRIMARY KEY(id))"
}

// mxMemCreateSQL 建表语句：memory 引擎，列定义与 LSM 表完全一致，
// 这样两表的 INSERT/UPDATE/SELECT 路径可完全对照。
func mxMemCreateSQL() string {
	return "CREATE TABLE " + mxMemTable + " (" +
		"id INT64 NOT NULL, " +
		"name STRING NULL, " +
		"score FLOAT64 NULL, " +
		"active BOOL NULL, " +
		"PRIMARY KEY(id)) ENGINE=memory"
}

// mxInsertSQL 生成 (clientID, seq) 对应的两表 INSERT SQL。
//
// score 始终为正；active 为 INT64 0/1（避免 BOOL UnaryExpr 在 INSERT
// VALUES 列表中处理的边缘情况）。两表 schema 一致，SQL 仅表名不同。
func mxInsertSQL(table string, clientID, seq int) string {
	id := mxBaseID + clientID*mxPerClient + seq
	score := float64(id) * 0.5
	active := int64(0)
	if seq%2 == 0 {
		active = 1
	}
	return fmt.Sprintf(
		"INSERT INTO %s (id, name, score, active) VALUES (%d, 'mx-%d-%d', %.2f, %d)",
		table, id, clientID, seq, score, active,
	)
}

// mxUpdateSQL 生成 (clientID, seq, round) 对应的 UPDATE SQL，
// 按 round 调整 score 末尾，避免跨轮次累加影响最终 SUM 断言。
func mxUpdateSQL(table string, clientID, seq, round int) string {
	id := mxBaseID + clientID*mxPerClient + seq
	score := float64(round+1) * float64(id) * 0.01
	return fmt.Sprintf("UPDATE %s SET score = %.4f WHERE id = %d", table, score, id)
}

// mxClientIDRange 决定本 worker 应写入/更新的 ID 区间：[lo, hi)。
func mxClientIDRange(clientID int) (lo, hi int64) {
	lo = int64(mxBaseID + clientID*mxPerClient)
	hi = lo + int64(mxPerClient)
	return
}

// mxRangeCountSQL 拼接单表的 COUNT(*) 校验 SQL。
func mxRangeCountSQL(table string, lo, hi int64) string {
	return fmt.Sprintf("SELECT COUNT(*) AS c FROM %s WHERE id >= %d AND id < %d",
		table, lo, hi)
}

// httpPostQueryNoTMX 通过 HTTP POST /query 执行单条 SQL，返回 (code, message, rows, data, err)。
// 与 httpPostQueryNoT 等价，命名加 MX 后缀避免外部测试在重构时误改两套 helper。
func httpPostQueryNoTMX(ctx context.Context, addr, sql string) (int, string, int, json.RawMessage, error) {
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

// mxExtractCountJSON 从 json.RawMessage 提取 COUNT 值（HTTP 路径）。
func mxExtractCountJSON(data json.RawMessage) int64 {
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

// mxExtractSumJSON 从 json.RawMessage 提取 SUM 值。
// SUM 在空集时返回 NULL，json 解码为 nil → 视作 0 便于断言。
func mxExtractSumJSON(data json.RawMessage) float64 {
	if len(data) == 0 || string(data) == "null" {
		return 0
	}
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil || len(rows) == 0 {
		return 0
	}
	for _, v := range rows[0] {
		switch n := v.(type) {
		case float64:
			return n
		case int64:
			return float64(n)
		case int:
			return float64(n)
		}
	}
	return 0
}

// mxParseIDColumn 从响应 Data 解析 id 列。
func mxParseIDColumn(t *testing.T, data json.RawMessage) []int64 {
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

// mxHTTPInsertRound 在第 round 轮对两表执行本 worker 的全部 INSERT。
// 第 0 轮才执行；后续轮次返回 nil 让调用方跳过。
func mxHTTPInsertRound(
	ctx context.Context, addr string, clientID, round int, errCh chan<- error,
) error {
	if round != 0 {
		return nil
	}
	for _, table := range mxTables {
		for seq := 0; seq < mxPerClient; seq++ {
			if err := mxHTTPExecNoT(ctx, addr, mxInsertSQL(table, clientID, seq),
				fmt.Sprintf("http 客户端 %d 第 %d 轮 INSERT %s", clientID, round, table),
				0, errCh); err != nil {
				return err
			}
		}
	}
	return nil
}

// mxHTTPCountRound 对两表执行 COUNT 校验，结果必须等于 mxPerClient。
// 任何错误同时通过 errCh 异步报告并同步返回，便于上层 worker 立即退出。
func mxHTTPCountRound(
	ctx context.Context, addr string, clientID, round int, lo, hi int64, errCh chan<- error,
) error {
	for _, table := range mxTables {
		code, msg, _, countData, err := httpPostQueryNoTMX(ctx, addr, mxRangeCountSQL(table, lo, hi))
		if err != nil {
			wrapped := fmt.Errorf("http 客户端 %d 第 %d 轮 COUNT %s 失败: %w", clientID, round, table, err)
			errCh <- wrapped
			return wrapped
		}
		if code != 0 {
			wrapped := fmt.Errorf("http 客户端 %d 第 %d 轮 COUNT %s 业务失败: %s", clientID, round, table, msg)
			errCh <- wrapped
			return wrapped
		}
		if got := mxExtractCountJSON(countData); got != mxPerClient {
			wrapped := fmt.Errorf("http 客户端 %d 第 %d 轮 %s COUNT = %d, 期望 %d",
				clientID, round, table, got, mxPerClient)
			errCh <- wrapped
			return wrapped
		}
	}
	return nil
}

// mxHTTPUpdateRound 对两表执行本 worker 的全部 UPDATE。
func mxHTTPUpdateRound(
	ctx context.Context, addr string, clientID, round int, errCh chan<- error,
) error {
	for _, table := range mxTables {
		for seq := 0; seq < mxPerClient; seq++ {
			if err := mxHTTPExecNoT(ctx, addr, mxUpdateSQL(table, clientID, seq, round),
				fmt.Sprintf("http 客户端 %d 第 %d 轮 UPDATE %s", clientID, round, table),
				1, errCh); err != nil {
				return err
			}
		}
	}
	return nil
}

// mxHTTPExecNoT 通用执行包装：执行单条 SQL，期望非 err 时 code==0 且 rows==expectedRows。
// 任何不符合均通过 errCh 报告并返回错误。
func mxHTTPExecNoT(
	ctx context.Context, addr, sql, label string, expectedRows int, errCh chan<- error,
) error {
	code, msg, rows, _, err := httpPostQueryNoTMX(ctx, addr, sql)
	if err != nil {
		errCh <- fmt.Errorf("%s 失败: %w", label, err)
		return fmt.Errorf("%s 失败: %w", label, err)
	}
	if code != 0 {
		errCh <- fmt.Errorf("%s 业务失败: %s", label, msg)
		return fmt.Errorf("%s 业务失败: %s", label, msg)
	}
	if expectedRows > 0 && rows != expectedRows {
		errCh <- fmt.Errorf("%s 影响行数 = %d, 期望 %d", label, rows, expectedRows)
		return fmt.Errorf("%s 影响行数 = %d, 期望 %d", label, rows, expectedRows)
	}
	return nil
}

// mxHTTPWorker 通过 HTTP 短连接完成本客户端在两表上的工作负载。
func mxHTTPWorker(
	ctx context.Context, addr string, clientID, rounds int, errCh chan<- error,
) {
	lo, hi := mxClientIDRange(clientID)
	for round := 0; round < rounds; round++ {
		if ctx.Err() != nil {
			return
		}
		if err := mxHTTPInsertRound(ctx, addr, clientID, round, errCh); err != nil {
			return
		}
		if err := mxHTTPCountRound(ctx, addr, clientID, round, lo, hi, errCh); err != nil {
			return
		}
		if err := mxHTTPUpdateRound(ctx, addr, clientID, round, errCh); err != nil {
			return
		}
	}
}

// mxTCPInsertRound 在第 round 轮对两表执行本 worker 的全部 INSERT。
func mxTCPInsertRound(
	t *testing.T, tc *tcpClient, clientID, round int, errCh chan<- error,
) error {
	t.Helper()
	if round != 0 {
		return nil
	}
	for _, table := range mxTables {
		for seq := 0; seq < mxPerClient; seq++ {
			sql := mxInsertSQL(table, clientID, seq)
			resp, err := tc.query(sql)
			if err != nil {
				errCh <- fmt.Errorf("tcp 客户端 %d 第 %d 轮 INSERT %s 失败: %w", clientID, round, table, err)
				return err
			}
			if resp.Code != 0 {
				errCh <- fmt.Errorf("tcp 客户端 %d 第 %d 轮 INSERT %s 业务失败: %s", clientID, round, table, resp.Message)
				return fmt.Errorf("%s", resp.Message)
			}
		}
	}
	return nil
}

// mxTCPCountRound 对两表执行 COUNT 校验，结果必须等于 mxPerClient。
func mxTCPCountRound(
	t *testing.T, tc *tcpClient, clientID, round int, lo, hi int64, errCh chan<- error,
) error {
	t.Helper()
	for _, table := range mxTables {
		resp, err := tc.query(mxRangeCountSQL(table, lo, hi))
		if err != nil {
			errCh <- fmt.Errorf("tcp 客户端 %d 第 %d 轮 COUNT %s 失败: %w", clientID, round, table, err)
			return err
		}
		if resp.Code != 0 {
			errCh <- fmt.Errorf("tcp 客户端 %d 第 %d 轮 COUNT %s 业务失败: %s", clientID, round, table, resp.Message)
			return fmt.Errorf("%s", resp.Message)
		}
		tcpData, err := json.Marshal(resp.Data)
		if err != nil {
			errCh <- fmt.Errorf("tcp 客户端 %d marshal %s 响应失败: %w", clientID, table, err)
			return err
		}
		if got := mxExtractCountJSON(tcpData); got != mxPerClient {
			errCh <- fmt.Errorf("tcp 客户端 %d 第 %d 轮 %s COUNT = %d, 期望 %d",
				clientID, round, table, got, mxPerClient)
			return fmt.Errorf("count mismatch")
		}
	}
	return nil
}

// mxTCPUpdateRound 对两表执行本 worker 的全部 UPDATE。
func mxTCPUpdateRound(
	t *testing.T, tc *tcpClient, clientID, round int, errCh chan<- error,
) error {
	t.Helper()
	for _, table := range mxTables {
		for seq := 0; seq < mxPerClient; seq++ {
			sql := mxUpdateSQL(table, clientID, seq, round)
			resp, err := tc.query(sql)
			if err != nil {
				errCh <- fmt.Errorf("tcp 客户端 %d 第 %d 轮 UPDATE %s 失败: %w", clientID, round, table, err)
				return err
			}
			if resp.Code != 0 {
				errCh <- fmt.Errorf("tcp 客户端 %d 第 %d 轮 UPDATE %s 业务失败: %s", clientID, round, table, resp.Message)
				return fmt.Errorf("%s", resp.Message)
			}
			if resp.Rows != 1 {
				errCh <- fmt.Errorf("tcp 客户端 %d 第 %d 轮 UPDATE %s 影响行数 = %d, 期望 1",
					clientID, round, table, resp.Rows)
				return fmt.Errorf("rows mismatch")
			}
		}
	}
	return nil
}

// mxTCPWorker 通过 TCP 长连接完成本客户端在两表上的工作负载。
func mxTCPWorker(
	t *testing.T, tc *tcpClient, clientID, rounds int, errCh chan<- error,
) {
	t.Helper()
	lo, hi := mxClientIDRange(clientID)
	for round := 0; round < rounds; round++ {
		if err := mxTCPInsertRound(t, tc, clientID, round, errCh); err != nil {
			return
		}
		if err := mxTCPCountRound(t, tc, clientID, round, lo, hi, errCh); err != nil {
			return
		}
		if err := mxTCPUpdateRound(t, tc, clientID, round, errCh); err != nil {
			return
		}
	}
}

// mxExpectedIDs 拼接全部 worker ID 区间的全集（按升序）。
func mxExpectedIDs() []int64 {
	out := make([]int64, 0, mxClients*mxPerClient)
	for c := 0; c < mxClients; c++ {
		lo, hi := mxClientIDRange(c)
		for id := lo; id < hi; id++ {
			out = append(out, id)
		}
	}
	return out
}

// mxCheckTableCrossProtocol 通过 TCP 与 HTTP 各读一次单表的 id 集合，验证一致。
// 与 mxCheckCrossProtocolConsistency 拆开，让编排函数只做循环，不嵌入复杂断言。
func mxCheckTableCrossProtocol(
	t *testing.T, s *subprocServer, table string, expected []int64,
) {
	t.Helper()
	httpCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, _, _, httpData, err := httpPostQueryNoTMX(httpCtx, s.httpAddr,
		fmt.Sprintf("SELECT id FROM %s ORDER BY id", table))
	cancel()
	if err != nil {
		t.Fatalf("HTTP SELECT id FROM %s 失败: %v", table, err)
	}
	gotHTTP := mxParseIDColumn(t, httpData)
	if !int64SliceEqual(gotHTTP, expected) {
		t.Errorf("HTTP 返回的 %s id 集合与期望不一致\n期望: %v\n实际: %v",
			table, expected, gotHTTP)
	}

	tc, err := dialTCP(s.tcpAddr)
	if err != nil {
		t.Fatalf("TCP 拨号失败: %v", err)
	}
	tcpResp, err := tc.query(
		fmt.Sprintf("SELECT id FROM %s ORDER BY id", table))
	tc.close()
	if err != nil {
		t.Fatalf("TCP SELECT id FROM %s 失败: %v", table, err)
	}
	if tcpResp.Code != 0 {
		t.Fatalf("TCP SELECT id FROM %s 业务失败: %s", table, tcpResp.Message)
	}
	tcpData, err := json.Marshal(tcpResp.Data)
	if err != nil {
		t.Fatalf("marshal TCP %s Data 失败: %v", table, err)
	}
	gotTCP := mxParseIDColumn(t, json.RawMessage(tcpData))
	if !int64SliceEqual(gotTCP, expected) {
		t.Errorf("TCP 返回的 %s id 集合与期望不一致\n期望: %v\n实际: %v",
			table, expected, gotTCP)
	}
}

// mxCheckCrossProtocolConsistency 通过 TCP 与 HTTP 各读一次两表，验证结果一致。
func mxCheckCrossProtocolConsistency(
	t *testing.T, s *subprocServer, expectedIDs []int64,
) {
	t.Helper()
	expected := make([]int64, len(expectedIDs))
	copy(expected, expectedIDs)
	sort.Slice(expected, func(i, j int) bool { return expected[i] < expected[j] })
	for _, table := range mxTables {
		mxCheckTableCrossProtocol(t, s, table, expected)
	}
}

// mxCheckTableErrorPaths 验证单表上重复主键与未知列两个错误路径返回非零 code。
// 跨表重复 + 错误语法两条公共路径由 mxCheckErrorPaths 编排调用。
func mxCheckTableErrorPaths(t *testing.T, s *subprocServer, table string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dupID := int64(mxBaseID)
	dupSQL := fmt.Sprintf("INSERT INTO %s (id, name, score, active) VALUES (%d, 'dup', 0.0, 1)",
		table, dupID)
	code, msg, _, _, err := httpPostQueryNoTMX(ctx, s.httpAddr, dupSQL)
	if err != nil {
		t.Fatalf("%s 重复主键 INSERT 请求失败: %v", table, err)
	}
	if code == 0 {
		t.Errorf("%s 重复主键 INSERT 应返回非零 code, 实际为 0 (msg=%s)", table, msg)
	}

	badColSQL := fmt.Sprintf("SELECT non_existing_col FROM %s LIMIT 1", table)
	code, msg, _, _, err = httpPostQueryNoTMX(ctx, s.httpAddr, badColSQL)
	if err != nil {
		t.Fatalf("%s 未知列 SELECT 请求失败: %v", table, err)
	}
	if code == 0 {
		t.Errorf("%s 未知列 SELECT 应返回非零 code, 实际为 0 (msg=%s)", table, msg)
	}
}

// mxCheckErrorPaths 验证子进程对错误 SQL 在两表上分别返回非零 code，
// 并校验「memory 表行为与 LSM 表一致」（错误路径不应因 engine 不同而分化）。
func mxCheckErrorPaths(t *testing.T, s *subprocServer) {
	t.Helper()
	for _, table := range mxTables {
		mxCheckTableErrorPaths(t, s, table)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code, _, _, _, err := httpPostQueryNoTMX(ctx, s.httpAddr, "THIS IS NOT SQL")
	if err != nil {
		t.Fatalf("错误语法请求失败: %v", err)
	}
	if code == 0 {
		t.Errorf("错误语法应返回非零 code, 实际为 0")
	}
}

// mxCreateBothTables 建两表；任一失败立即 Fatal。
func mxCreateBothTables(t *testing.T, addr string) {
	t.Helper()
	for _, ddl := range []string{mxLSMCreateSQL(), mxMemCreateSQL()} {
		r := httpPostQuery(t, addr, ddl)
		if r.Code != 0 {
			t.Fatalf("建表失败: %s\nSQL: %s", r.Message, ddl)
		}
	}
}

// mxRunMixedWorkerPool 启动 2 TCP + 2 HTTP worker 并等待全部完成。
// 失败时把 errCh 的错误累积到 t，全部失败后 FailNow。
func mxRunMixedWorkerPool(
	t *testing.T, s *subprocServer,
) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	errCh := make(chan error, mxClients*mxRounds*mxPerClient*4)

	// 预先建立 2 个 TCP 长连接，避免在 goroutine 内串行 dial。
	tcpClients := make([]*tcpClient, 0, mxClients/2)
	for i := 0; i < mxClients/2; i++ {
		tc, err := dialTCP(s.tcpAddr)
		if err != nil {
			t.Fatalf("TCP 拨号失败: %v", err)
		}
		tcpClients = append(tcpClients, tc)
	}
	t.Cleanup(func() {
		for _, tc := range tcpClients {
			tc.close()
		}
	})

	for i := 0; i < mxClients; i++ {
		i := i
		isTCP := i%2 == 0
		wg.Add(1)
		go func() {
			defer wg.Done()
			if isTCP {
				mxTCPWorker(t, tcpClients[i/2], i, mxRounds, errCh)
			} else {
				mxHTTPWorker(ctx, s.httpAddr, i, mxRounds, errCh)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	var workerErrs []error
	for e := range errCh {
		workerErrs = append(workerErrs, e)
	}
	if len(workerErrs) > 0 {
		for _, e := range workerErrs {
			t.Errorf("worker 错误: %v", e)
		}
		t.FailNow()
	}
}

// mxInsertProbeRow 在指定表写入一行特殊探针，便于重启后识别「这就是上次写入的数据」。
func mxInsertProbeRow(t *testing.T, addr, table string) {
	t.Helper()
	sql := fmt.Sprintf("INSERT INTO %s (id, name, score, active) VALUES (%d, 'probe', 1.0, 1)",
		table, mxRestartTagID)
	if r := httpPostQuery(t, addr, sql); r.Code != 0 {
		t.Fatalf("写入 %s 探针失败: %s", table, r.Message)
	}
}

// mxCheckLSMPersistedAfterRestart 校验新子进程内 LSM 表的探针行 + 全部数据完整保留。
// 期望行数 = mxClients*mxPerClient（worker 写入）+ 1（探针）。
func mxCheckLSMPersistedAfterRestart(t *testing.T, s *subprocServer) {
	t.Helper()
	probeReadSQL := fmt.Sprintf("SELECT id, name FROM %s WHERE id = %d", mxLsmTable, mxRestartTagID)
	if r := httpPostQuery(t, s.httpAddr, probeReadSQL); r.Code != 0 {
		t.Fatalf("重启后读取 LSM 探针失败: %s", r.Message)
	} else if r.Rows != 1 {
		t.Fatalf("重启后 LSM 探针行数 = %d, 期望 1", r.Rows)
	}
	totalSQL := fmt.Sprintf("SELECT COUNT(*) AS c FROM %s", mxLsmTable)
	r := httpPostQuery(t, s.httpAddr, totalSQL)
	if r.Code != 0 {
		t.Fatalf("重启后 LSM COUNT 失败: %s", r.Message)
	}
	want := int64(mxClients*mxPerClient + 1)
	if got := mxExtractCountJSON(r.Data); got != want {
		t.Errorf("重启后 LSM COUNT = %d, 期望 %d", got, want)
	}
}

// mxCheckMemLostAfterRestart 校验新子进程内 memory 表 COUNT = 0（重启即丢）。
func mxCheckMemLostAfterRestart(t *testing.T, s *subprocServer) {
	t.Helper()
	sql := fmt.Sprintf("SELECT COUNT(*) AS c FROM %s", mxMemTable)
	r := httpPostQuery(t, s.httpAddr, sql)
	if r.Code != 0 {
		t.Fatalf("重启后 memory COUNT 失败: %s", r.Message)
	}
	if got := mxExtractCountJSON(r.Data); got != 0 {
		t.Errorf("重启后 memory COUNT = %d, 期望 0（memory 表重启即丢）", got)
	}
}

// mxGracefulStop 向子进程发送 SIGTERM 并等待优雅退出。
func mxGracefulStop(t *testing.T, s *subprocServer) {
	t.Helper()
	sendSignalToSubprocess(t, s, syscall.SIGTERM)
	if code, err := waitForSubprocessExit(t, s, subprocStopTimeout); err != nil && code != 0 {
		t.Fatalf("子进程退出码 = %d, 期望 0; err = %v", code, err)
	}
}

// TestSubprocMixedEngineMultiClient 端到端：子进程 server + 多客户端 + 双引擎（LSM + memory）混合一般 SQL。
//
// 流程：
//  1. 拉起子进程 server（TCP + HTTP）
//  2. 创建 1 张 LSM 引擎表 + 1 张 memory 引擎表
//  3. 4 个 worker（2 TCP + 2 HTTP）在两表各自的 ID 区间内并发执行 INSERT/UPDATE/COUNT
//  4. 通过 TCP 与 HTTP 各读一次两表，校验跨协议结果一致 + 跨引擎路由无串扰
//  5. 校验错误路径在两表上行为一致
//  6. 优雅关闭子进程后，用同一 DataDir 重启新子进程
//  7. 在新子进程中：LSM 表数据完整保留，memory 表 COUNT = 0（重启即丢）
func TestSubprocMixedEngineMultiClient(t *testing.T) {
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

	mxCreateBothTables(t, s.httpAddr)
	mxRunMixedWorkerPool(t, s)
	mxCheckCrossProtocolConsistency(t, s, mxExpectedIDs())
	mxCheckErrorPaths(t, s)

	mxInsertProbeRow(t, s.httpAddr, mxLsmTable)
	mxInsertProbeRow(t, s.httpAddr, mxMemTable)
	mxGracefulStop(t, s)

	s2, log2 := startSubprocessServer(t,
		allocateEphemeralPort(t), allocateEphemeralPort(t), dir)
	t.Cleanup(func() {
		if s2 != nil {
			stopSubprocessServer(t, s2)
		}
		if t.Failed() {
			t.Logf("重启后子进程日志:\n%s", log2.String())
		}
	})

	mxCheckLSMPersistedAfterRestart(t, s2)
	mxCheckMemLostAfterRestart(t, s2)
	mxGracefulStop(t, s2)
}

// mxSUMWorker 单个 HTTP worker 完成 INSERT + UPDATE。
// UPDATE 公式：score = mxRounds * id * 0.01，使 SUM 可代数验证。
func mxSUMWorker(
	ctx context.Context, addr string, clientID int, errCh chan<- error,
) {
	for seq := 0; seq < mxPerClient; seq++ {
		for _, table := range mxTables {
			if err := mxHTTPExecNoT(ctx, addr, mxInsertSQL(table, clientID, seq),
				fmt.Sprintf("SUM client %d INSERT %s", clientID, table), 0, errCh); err != nil {
				return
			}
		}
	}
	for seq := 0; seq < mxPerClient; seq++ {
		for _, table := range mxTables {
			if err := mxHTTPExecNoT(ctx, addr, mxUpdateSQL(table, clientID, seq, mxRounds-1),
				fmt.Sprintf("SUM client %d UPDATE %s", clientID, table), 0, errCh); err != nil {
				return
			}
		}
	}
}

// mxCheckTableSUM 校验单表 SUM(score) 收敛到 expectedSum（1e-3 绝对误差）。
func mxCheckTableSUM(t *testing.T, addr, table string, expectedSum float64) {
	t.Helper()
	r := httpPostQuery(t, addr, fmt.Sprintf("SELECT SUM(score) AS s FROM %s", table))
	if r.Code != 0 {
		t.Fatalf("%s SUM 失败: %s", table, r.Message)
	}
	got := mxExtractSumJSON(r.Data)
	diff := got - expectedSum
	if diff < 0 {
		diff = -diff
	}
	if diff > 1e-3 {
		t.Errorf("%s SUM(score) = %.4f, 期望 %.4f (diff=%.6f)",
			table, got, expectedSum, diff)
	}
}

// mxExpectedSUM 计算 SUM 期望值：每行最终 score = mxRounds * id * 0.01，
// 期望 SUM = mxRounds * 0.01 * SUM(id for id in [mxBaseID, mxBaseID+mxClients*mxPerClient))
func mxExpectedSUM() float64 {
	var sumID int64
	for id := int64(mxBaseID); id < int64(mxBaseID+mxClients*mxPerClient); id++ {
		sumID += id
	}
	return float64(mxRounds) * 0.01 * float64(sumID)
}

// TestSubprocMixedEngineSUMConsistency 验证双引擎 SUM(score) 在并发 UPDATE 路径下
// 也能收敛到一致值（与 COUNT 互补的另一维度断言）。
func TestSubprocMixedEngineSUMConsistency(t *testing.T) {
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

	mxCreateBothTables(t, s.httpAddr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	errCh := make(chan error, mxClients*mxPerClient*2)
	for i := 0; i < mxClients; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			mxSUMWorker(ctx, s.httpAddr, i, errCh)
		}()
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Errorf("worker 错误: %v", e)
	}
	if t.Failed() {
		t.FailNow()
	}

	expectedSum := mxExpectedSUM()
	for _, table := range mxTables {
		mxCheckTableSUM(t, s.httpAddr, table, expectedSum)
	}

	mxGracefulStop(t, s)
}
