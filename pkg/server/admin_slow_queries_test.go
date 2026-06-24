package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// newSlowQueriesTestServer 构造一个慢查询阈值极小（1ns，等价于捕获所有查询）的 Server，
// 并预先创建 users 表。threshold=0 会被 NewSlowQueryLog 视为「禁用」，因此测试使用
// 极小阈值以确保每次 handleQuery 调用都被记录。
// 使用独立 prometheus.Registry 避免与 DefaultRegisterer 冲突导致 duplicate registration。
func newSlowQueriesTestServer(t *testing.T, threshold time.Duration, capacity int) *Server {
	t.Helper()
	if threshold <= 0 {
		// 阈值 <= 0 等价于禁用 NewSlowQueryLog；测试用 1ns 既能「启用」又能捕获任何实际查询。
		threshold = time.Nanosecond
	}
	dir := t.TempDir()
	registry := prometheus.NewRegistry()
	srv, err := NewServer(Config{
		TCPAddr:            "127.0.0.1:0",
		HTTPAddr:           "127.0.0.1:0",
		DataDir:            dir,
		MaxMemTableSize:    defaultMaxMemTableSize,
		SlowQueryThreshold: threshold,
		SlowQueryCapacity:  capacity,
	}, WithMetricsRegistry(registry))
	if err != nil {
		t.Fatalf("NewServer 失败: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	resp, err := srv.handleQuery(&QueryRequest{
		SQL: "create table users (id int64 not null, name string, primary key(id))",
	})
	if err != nil || resp.Code != 0 {
		t.Fatalf("创建表失败: err=%v resp=%+v", err, resp)
	}
	return srv
}

// adminSlowQueriesRequest 触发 GET /admin/slow-queries 并返回响应解码结果。
func adminSlowQueriesRequest(t *testing.T, srv *Server) (int, slowQueriesResponse) {
	t.Helper()
	mux := srv.registerHTTPHandlers()
	req := httptest.NewRequest(http.MethodGet, "/admin/slow-queries", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()
	var body slowQueriesResponse
	if rec.Body.Len() > 0 {
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("解码响应失败: %v", err)
		}
	}
	return res.StatusCode, body
}

// TestAdminSlowQueriesRegistered 验证 /admin/slow-queries 已被 registerHTTPHandlers 注册。
func TestAdminSlowQueriesRegistered(t *testing.T) {
	srv := newSlowQueriesTestServer(t, 0, 20)
	mux := srv.registerHTTPHandlers()
	req := httptest.NewRequest(http.MethodGet, "/admin/slow-queries", nil)
	_, pattern := mux.Handler(req)
	if pattern == "" {
		t.Fatalf("/admin/slow-queries 未注册")
	}
}

// TestAdminSlowQueriesRejectsNonGET 验证 /admin/slow-queries 拒绝非 GET 方法。
func TestAdminSlowQueriesRejectsNonGET(t *testing.T) {
	srv := newSlowQueriesTestServer(t, 0, 20)
	mux := srv.registerHTTPHandlers()
	req := httptest.NewRequest(http.MethodPost, "/admin/slow-queries", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("状态码 = %d, 期望 405", rec.Code)
	}
	var body adminResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if body.Code != -1 || body.Message != adminErrSlowQueriesBadMethod {
		t.Fatalf("响应 = %+v, 期望 Code=-1", body)
	}
}

// TestAdminSlowQueriesEmptyWhenNoRecordings 验证日志无记录时返回空数组（非 nil）。
func TestAdminSlowQueriesEmptyWhenNoRecordings(t *testing.T) {
	// 阈值 1 小时的服务器，正常查询（< 1h）均不会进入慢查询日志
	dir := t.TempDir()
	registry := prometheus.NewRegistry()
	srv, err := NewServer(Config{
		TCPAddr:            "127.0.0.1:0",
		HTTPAddr:           "127.0.0.1:0",
		DataDir:            dir,
		MaxMemTableSize:    defaultMaxMemTableSize,
		SlowQueryThreshold: time.Hour,
		SlowQueryCapacity:  20,
	}, WithMetricsRegistry(registry))
	if err != nil {
		t.Fatalf("NewServer 失败: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	if _, err := srv.handleQuery(&QueryRequest{SQL: "create table t (id int64, primary key(id))"}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	status, body := adminSlowQueriesRequest(t, srv)
	if status != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200", status)
	}
	if body.Code != 0 {
		t.Fatalf("Code = %d, 期望 0", body.Code)
	}
	if body.Config.ThresholdMS != int(time.Hour/time.Millisecond) {
		t.Fatalf("ThresholdMS = %d, 期望 %d", body.Config.ThresholdMS, int(time.Hour/time.Millisecond))
	}
	if !body.Config.Enabled {
		t.Fatalf("Enabled = false, 期望 true（threshold=1h）")
	}
	if body.Queries == nil {
		t.Fatalf("Queries 不应为 nil（应为空切片）")
	}
	if len(body.Queries) != 0 {
		t.Fatalf("Queries 长度 = %d, 期望 0", len(body.Queries))
	}
}

// TestAdminSlowQueriesCapturedAfterQuery 验证阈值 0 时 handleQuery 的所有调用都被记录。
func TestAdminSlowQueriesCapturedAfterQuery(t *testing.T) {
	srv := newSlowQueriesTestServer(t, 0, 20)

	for i := 0; i < 3; i++ {
		resp, err := srv.handleQuery(&QueryRequest{SQL: "select id from users"})
		if err != nil || resp.Code != 0 {
			t.Fatalf("查询失败: err=%v resp=%+v", err, resp)
		}
	}
	status, body := adminSlowQueriesRequest(t, srv)
	if status != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200", status)
	}
	// CREATE TABLE 也被记录了，再加上 3 次 SELECT，共 4 条
	if len(body.Queries) != 4 {
		t.Fatalf("Queries 长度 = %d, 期望 4", len(body.Queries))
	}
	if !body.Config.Enabled {
		t.Fatalf("threshold=0 时 Enabled 应为 false（阈值 <= 0 视为禁用）")
	}
	// 最新记录应是最后一条 SELECT
	last := body.Queries[0]
	if !strings.Contains(strings.ToLower(last.SQL), "select") {
		t.Fatalf("最新记录 SQL = %q, 期望 SELECT", last.SQL)
	}
	if last.Source != string(SlowQuerySourceHTTP) {
		t.Fatalf("Source = %q, 期望 %q", last.Source, SlowQuerySourceHTTP)
	}
	if last.DurationMS < 0 {
		t.Fatalf("DurationMS = %v, 期望 >= 0", last.DurationMS)
	}
	// Timestamp 应可被 RFC3339Nano 解析
	if _, err := time.Parse(time.RFC3339Nano, last.Timestamp); err != nil {
		t.Fatalf("Timestamp 解析失败: %v (raw=%q)", err, last.Timestamp)
	}
}

// TestAdminSlowQueriesRecordsError 验证执行错误（表不存在）也被记录且 Error 字段非空。
func TestAdminSlowQueriesRecordsError(t *testing.T) {
	srv := newSlowQueriesTestServer(t, 0, 20)
	resp, err := srv.handleQuery(&QueryRequest{SQL: "select * from nonexistent"})
	if err != nil {
		t.Fatalf("handleQuery 返回 err: %v", err)
	}
	if resp.Code == 0 {
		t.Fatalf("期望错误响应")
	}
	_, body := adminSlowQueriesRequest(t, srv)
	if len(body.Queries) == 0 {
		t.Fatalf("期望至少 1 条记录")
	}
	// 找到那条错误记录
	var found bool
	for _, q := range body.Queries {
		if strings.Contains(strings.ToLower(q.SQL), "nonexistent") {
			found = true
			if q.Error == "" {
				t.Fatalf("错误记录应填充 Error 字段: %+v", q)
			}
			break
		}
	}
	if !found {
		t.Fatalf("未找到针对 nonexistent 表的记录: %+v", body.Queries)
	}
}

// TestAdminSlowQueriesDisabledByDefault 验证默认配置（threshold=100ms）下，正常快查询不会被记录。
func TestAdminSlowQueriesDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	registry := prometheus.NewRegistry()
	srv, err := NewServer(Config{
		TCPAddr:            "127.0.0.1:0",
		HTTPAddr:           "127.0.0.1:0",
		DataDir:            dir,
		MaxMemTableSize:    defaultMaxMemTableSize,
		SlowQueryThreshold: 100 * time.Millisecond,
		SlowQueryCapacity:  10,
	}, WithMetricsRegistry(registry))
	if err != nil {
		t.Fatalf("NewServer 失败: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	if _, err := srv.handleQuery(&QueryRequest{SQL: "create table t (id int64, primary key(id))"}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if _, err := srv.handleQuery(&QueryRequest{SQL: "select id from t"}); err != nil {
		t.Fatalf("查询失败: %v", err)
	}

	_, body := adminSlowQueriesRequest(t, srv)
	if !body.Config.Enabled {
		t.Fatalf("threshold=100ms 时 Enabled 应为 true")
	}
	if body.Config.ThresholdMS != 100 {
		t.Fatalf("ThresholdMS = %d, 期望 100", body.Config.ThresholdMS)
	}
	if body.Config.Capacity != 10 {
		t.Fatalf("Capacity = %d, 期望 10", body.Config.Capacity)
	}
	// 快查询（< 100ms）不应被记录
	if len(body.Queries) != 0 {
		t.Fatalf("快查询不应被记录，实际 %d 条", len(body.Queries))
	}
}

// TestCollectSlowQueriesNilSafe 验证 slowQueries 为 nil 时也能安全返回。
// 实际 Server 总会初始化 slowQueries，但接口契约需对 nil 容忍。
func TestCollectSlowQueriesNilSafe(t *testing.T) {
	srv := &Server{}
	resp := srv.collectSlowQueries()
	if resp == nil {
		t.Fatalf("collectSlowQueries 不应返回 nil")
	}
	if resp.Code != 0 {
		t.Fatalf("Code = %d, 期望 0", resp.Code)
	}
	if resp.Config.Enabled {
		t.Fatalf("nil slowQueries 时 Enabled 应为 false")
	}
	if len(resp.Queries) != 0 {
		t.Fatalf("Queries 长度 = %d, 期望 0", len(resp.Queries))
	}
}
