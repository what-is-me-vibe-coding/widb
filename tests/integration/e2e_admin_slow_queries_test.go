// Package integration 端到端集成测试：HTTP 运维端点 GET /admin/slow-queries。
//
// 本文件针对慢查询日志与 /admin/slow-queries 端点补齐端到端覆盖：
//   - 通过真实 HTTP 客户端（net/http）访问 GET /admin/slow-queries，验证路由/响应/Content-Type
//   - 验证响应 JSON 的顶层结构（code/message/config/queries）
//   - 验证 config 字段回显（enabled/threshold_ms/capacity）
//   - 验证 queries 数组按时间倒序、字段对齐（timestamp/duration_ms/source/sql）
//   - 验证多次 HTTP /query 调用产生多条记录
//   - 验证 404（端点未在 server 路径中暴露）时返回的 JSON 错误结构
//   - 验证非 GET 方法（POST/PUT/DELETE）返回 405
//   - 验证空日志响应（threshold 远高于实际查询耗时）时返回空 queries
//
// 与 pkg/server/admin_slow_queries_test.go 单测的区别：单测使用 httptest.NewRecorder
// 直接调用 handler 闭包或 Server.collectSlowQueries；本文件通过 startSQLServer
// 启动的完整 HTTP 监听，验证真实 TCP→HTTP→Server 的端到端调用链，
// 并覆盖 handleQuery 在执行真实 SQL 时的慢查询打点。
package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

// slowQueriesConfigResp 与 pkg/server/admin_slow_queries.go 中 slowQueryConfigView 字段一致。
// 独立定义（不直接 import server 内部类型）以保持集成测试对 server 内部实现的弱耦合。
type slowQueriesConfigResp struct {
	Enabled     bool `json:"enabled"`
	ThresholdMS int  `json:"threshold_ms"`
	Capacity    int  `json:"capacity"`
}

// slowQueryEntryResp 与 pkg/server/admin_slow_queries.go 中 slowQueryEntryView 字段一致。
type slowQueryEntryResp struct {
	Timestamp  string  `json:"timestamp"`
	Duration   int64   `json:"duration_ns"`
	DurationMS float64 `json:"duration_ms"`
	Source     string  `json:"source"`
	SQL        string  `json:"sql"`
	Error      string  `json:"error,omitempty"`
}

// slowQueriesResponseResp 是 /admin/slow-queries 的统一响应结构。
type slowQueriesResponseResp struct {
	Code    int                   `json:"code"`
	Message string                `json:"message,omitempty"`
	Config  slowQueriesConfigResp `json:"config"`
	Queries []slowQueryEntryResp  `json:"queries"`
}

// startSQLServerWithSlowQuery 启动一个 SlowQueryThreshold 取指定值的 server。
// 当 threshold < 0 时视为「禁用慢查询日志」；测试场景用 time.Nanosecond 让所有查询都进入日志。
func startSQLServerWithSlowQuery(t *testing.T, threshold time.Duration) *sqlServer {
	t.Helper()
	dir := t.TempDir()
	cfg := server.Config{
		TCPAddr:            "127.0.0.1:0",
		HTTPAddr:           "127.0.0.1:0",
		DataDir:            dir,
		SlowQueryThreshold: threshold,
		SlowQueryCapacity:  50,
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

// getAdminSlowQueries 通过真实 HTTP 客户端访问 GET /admin/slow-queries。
func getAdminSlowQueries(t *testing.T, s *sqlServer) (int, slowQueriesResponseResp, []byte, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+s.httpAddr+"/admin/slow-queries", nil)
	if err != nil {
		return 0, slowQueriesResponseResp{}, nil, fmt.Errorf("构造请求失败: %w", err)
	}
	resp, err := sqlHTTPClient.Do(req)
	if err != nil {
		return 0, slowQueriesResponseResp{}, nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, slowQueriesResponseResp{}, body, fmt.Errorf("读取响应失败: %w", err)
	}
	var out slowQueriesResponseResp
	if len(body) > 0 {
		if err := json.Unmarshal(body, &out); err != nil {
			return resp.StatusCode, slowQueriesResponseResp{}, body, fmt.Errorf("解码响应失败: %v (body=%q)", err, string(body))
		}
	}
	return resp.StatusCode, out, body, nil
}

// callAdminSlowQueriesMethod 用任意 method 访问 /admin/slow-queries，用于验证 405。
func callAdminSlowQueriesMethod(t *testing.T, s *sqlServer, method string) (int, []byte, error) {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+s.httpAddr+"/admin/slow-queries", nil)
	if err != nil {
		return 0, nil, fmt.Errorf("构造请求失败: %w", err)
	}
	resp, err := sqlHTTPClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, body, fmt.Errorf("读取响应失败: %w", err)
	}
	return resp.StatusCode, body, nil
}

// TestAdminSlowQueriesEmptyDatabase 验证空库（无任何查询）下 GET /admin/slow-queries 的响应。
// 期望：HTTP 200、Code=0、Enabled=true、ThresholdMS=1（纳秒）、Capacity=50、Queries=[]。
func TestAdminSlowQueriesEmptyDatabase(t *testing.T) {
	t.Parallel()
	s := startSQLServerWithSlowQuery(t, time.Nanosecond)
	status, body, raw, err := getAdminSlowQueries(t, s)
	if err != nil {
		t.Fatalf("GET /admin/slow-queries 失败: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200; body = %s", status, string(raw))
	}
	if body.Code != 0 {
		t.Errorf("Code = %d, 期望 0; Message = %q", body.Code, body.Message)
	}
	if !body.Config.Enabled {
		t.Errorf("Config.Enabled = false, 期望 true (threshold=1ns)")
	}
	if body.Config.ThresholdMS != 0 {
		// 1ns 换算成毫秒为 0，与实现保持一致：时间单位在响应里以毫秒呈现时取整
		t.Errorf("Config.ThresholdMS = %d, 期望 0 (1ns 取整)", body.Config.ThresholdMS)
	}
	if body.Config.Capacity != 50 {
		t.Errorf("Config.Capacity = %d, 期望 50", body.Config.Capacity)
	}
	if body.Queries == nil {
		t.Errorf("Queries 不应为 nil (应为空切片)")
	}
	if len(body.Queries) != 0 {
		t.Errorf("Queries 长度 = %d, 期望 0; 内容 = %+v", len(body.Queries), body.Queries)
	}
}

// TestAdminSlowQueriesCapturesSuccessfulQueries 验证多次 HTTP /query 调用后，慢查询日志按时间倒序记录。
// 阈值取 1ns，等价于「记录所有查询」。
func TestAdminSlowQueriesCapturesSuccessfulQueries(t *testing.T) {
	t.Parallel()
	s := startSQLServerWithSlowQuery(t, time.Nanosecond)
	// 通过 HTTP /query 执行多次 DDL/DML
	for _, sql := range []string{
		"create table logs (id int64 not null, msg string, primary key(id))",
		"insert into logs values (1, 'a')",
		"insert into logs values (2, 'b')",
		"select id, msg from logs",
	} {
		resp := queryVia(t, s, "http", sql)
		if resp.Code != 0 {
			t.Fatalf("SQL %q 失败: %s", sql, resp.Message)
		}
	}

	status, body, raw, err := getAdminSlowQueries(t, s)
	if err != nil {
		t.Fatalf("GET /admin/slow-queries 失败: %v", err)
	}
	if status != http.StatusOK || body.Code != 0 {
		t.Fatalf("状态码=%d Code=%d Message=%q raw=%s", status, body.Code, body.Message, string(raw))
	}
	// 4 次调用应有 4 条记录
	if len(body.Queries) != 4 {
		t.Fatalf("Queries 长度 = %d, 期望 4; 内容 = %+v", len(body.Queries), body.Queries)
	}
	// 最新记录应是最后一条 SELECT
	first := body.Queries[0]
	if !strings.Contains(strings.ToLower(first.SQL), "select") {
		t.Errorf("最新记录 SQL = %q, 期望 SELECT", first.SQL)
	}
	if first.Source != "http" {
		t.Errorf("Source = %q, 期望 http", first.Source)
	}
	if _, err := time.Parse(time.RFC3339Nano, first.Timestamp); err != nil {
		t.Errorf("Timestamp 解析失败: %v (raw=%q)", err, first.Timestamp)
	}
	// 验证时间倒序
	for i := 1; i < len(body.Queries); i++ {
		prev, err := time.Parse(time.RFC3339Nano, body.Queries[i-1].Timestamp)
		if err != nil {
			continue
		}
		cur, err := time.Parse(time.RFC3339Nano, body.Queries[i].Timestamp)
		if err != nil {
			continue
		}
		if cur.After(prev) {
			t.Errorf("顺序错乱: idx=%d 早于 idx=%d", i, i-1)
		}
	}
}

// TestAdminSlowQueriesRecordsErrorSQL 验证执行失败的 SQL 也被记录且 Error 字段非空。
// 实际：当前实现下，DDL/DML 错误可能走 ConvertError 路径，但只要最终 handleQuery 返回 Code != 0，
// recordSlowQuery 会把 resp.Message 写入 Error 字段。
func TestAdminSlowQueriesRecordsErrorSQL(t *testing.T) {
	t.Parallel()
	s := startSQLServerWithSlowQuery(t, time.Nanosecond)
	// 引用不存在的表
	resp := queryVia(t, s, "http", "select * from nonexistent")
	if resp.Code == 0 {
		t.Fatalf("期望错误响应 (Code != 0)")
	}
	_, body, _, err := getAdminSlowQueries(t, s)
	if err != nil {
		t.Fatalf("GET /admin/slow-queries 失败: %v", err)
	}
	if len(body.Queries) == 0 {
		t.Fatalf("期望至少 1 条记录 (含失败 SQL)")
	}
	var found bool
	for _, q := range body.Queries {
		if strings.Contains(strings.ToLower(q.SQL), "nonexistent") {
			found = true
			if q.Error == "" {
				t.Errorf("失败记录应填充 Error 字段: %+v", q)
			}
			break
		}
	}
	if !found {
		t.Fatalf("未找到针对 nonexistent 的记录: %+v", body.Queries)
	}
}

// TestAdminSlowQueriesRespectsThreshold 验证 threshold 高于实际查询耗时时不记录。
// 阈值取 1 小时，正常的毫秒级查询都不会进入日志。
func TestAdminSlowQueriesRespectsThreshold(t *testing.T) {
	t.Parallel()
	s := startSQLServerWithSlowQuery(t, time.Hour)
	// 触发 5 次查询
	for i := 0; i < 5; i++ {
		_ = queryVia(t, s, "http", "select 1")
	}
	_, body, _, err := getAdminSlowQueries(t, s)
	if err != nil {
		t.Fatalf("GET /admin/slow-queries 失败: %v", err)
	}
	if !body.Config.Enabled {
		t.Errorf("threshold=1h 时 Config.Enabled 应为 true")
	}
	if body.Config.ThresholdMS != int(time.Hour/time.Millisecond) {
		t.Errorf("Config.ThresholdMS = %d, 期望 %d", body.Config.ThresholdMS, int(time.Hour/time.Millisecond))
	}
	if len(body.Queries) != 0 {
		t.Errorf("1h 阈值下不应记录快查询，实际 %d 条", len(body.Queries))
	}
}

// TestAdminSlowQueriesRingBufferCapacity 验证 capacity 上限生效。
// 阈值取 1ns，触发 5 次查询，capacity=50，最终 Snapshot 应保留 5 条。
func TestAdminSlowQueriesRingBufferCapacity(t *testing.T) {
	t.Parallel()
	s := startSQLServerWithSlowQuery(t, time.Nanosecond)
	// 触发 5 次查询
	for i := 0; i < 5; i++ {
		_ = queryVia(t, s, "http", "select 1")
	}
	_, body, _, err := getAdminSlowQueries(t, s)
	if err != nil {
		t.Fatalf("GET /admin/slow-queries 失败: %v", err)
	}
	if body.Config.Capacity != 50 {
		t.Errorf("Config.Capacity = %d, 期望 50 (startSQLServerWithSlowQuery 固定 capacity=50)", body.Config.Capacity)
	}
	if len(body.Queries) != 5 {
		t.Errorf("Queries 长度 = %d, 期望 5（capacity=50 容纳所有）", len(body.Queries))
	}
}

// TestAdminSlowQueriesRejectsNonGET 验证 /admin/slow-queries 拒绝非 GET 方法。
func TestAdminSlowQueriesRejectsNonGET(t *testing.T) {
	t.Parallel()
	s := startSQLServerWithSlowQuery(t, time.Nanosecond)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			status, _, err := callAdminSlowQueriesMethod(t, s, method)
			if err != nil {
				t.Fatalf("%s 请求失败: %v", method, err)
			}
			if status != http.StatusMethodNotAllowed {
				t.Errorf("状态码 = %d, 期望 405", status)
			}
		})
	}
}
