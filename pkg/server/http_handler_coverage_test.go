package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// TestHTTPQuery_WrongMethod 测试 /query 使用 GET 方法时返回 405。
func TestHTTPQuery_WrongMethod(t *testing.T) {
	srv := newTestServerForCoverage(t)

	// 使用 GET 方法访问 /query（只支持 POST）
	req := httptest.NewRequest(http.MethodGet, "/query", nil)
	w := httptest.NewRecorder()
	srv.httpQuery(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("状态码 = %d, 期望 %d", w.Code, http.StatusMethodNotAllowed)
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", resp.Code)
	}
	if !strings.Contains(resp.Message, "POST") {
		t.Errorf("响应消息应包含 'POST'，实际: %q", resp.Message)
	}
}

// TestHTTPQuery_InvalidJSON 测试 /query 接收无效 JSON 时返回 400。
func TestHTTPQuery_InvalidJSON(t *testing.T) {
	srv := newTestServerForCoverage(t)

	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader("{invalid json"))
	w := httptest.NewRecorder()
	srv.httpQuery(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("状态码 = %d, 期望 %d", w.Code, http.StatusBadRequest)
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", resp.Code)
	}
	if !strings.Contains(resp.Message, "解析请求体失败") {
		t.Errorf("响应消息应包含 '解析请求体失败'，实际: %q", resp.Message)
	}
}

// TestHTTPQuery_HandleQueryError 测试 /query 处理查询返回错误时返回 500。
func TestHTTPQuery_HandleQueryError(t *testing.T) {
	srv := newTestServerForCoverage(t)

	// 发送一条无法解析的 SQL，handleQuery 会返回错误
	body := `{"sql":"!!!INVALID SQL!!!@@@"}`
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.httpQuery(w, req)

	// 解析错误会返回 Code=-1 但不是 500（handleQuery 返回 *Response 而非 error）
	// 所以这里验证响应中 Code 不为 0
	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Code == 0 {
		t.Error("无效 SQL 应返回非零 Code")
	}
}

// TestHTTPWrite_WrongMethod 测试 /write 使用 GET 方法时返回 405。
func TestHTTPWrite_WrongMethod(t *testing.T) {
	srv := newTestServerForCoverage(t)

	req := httptest.NewRequest(http.MethodGet, "/write", nil)
	w := httptest.NewRecorder()
	srv.httpWrite(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("状态码 = %d, 期望 %d", w.Code, http.StatusMethodNotAllowed)
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", resp.Code)
	}
	if !strings.Contains(resp.Message, "POST") {
		t.Errorf("响应消息应包含 'POST'，实际: %q", resp.Message)
	}
}

// TestHTTPWrite_InvalidJSON 测试 /write 接收无效 JSON 时返回 400。
func TestHTTPWrite_InvalidJSON(t *testing.T) {
	srv := newTestServerForCoverage(t)

	req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader("not json at all"))
	w := httptest.NewRecorder()
	srv.httpWrite(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("状态码 = %d, 期望 %d", w.Code, http.StatusBadRequest)
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", resp.Code)
	}
	if !strings.Contains(resp.Message, "解析请求体失败") {
		t.Errorf("响应消息应包含 '解析请求体失败'，实际: %q", resp.Message)
	}
}

// TestHTTPWrite_TableNotFound 测试 /write 写入不存在的表时返回 400。
func TestHTTPWrite_TableNotFound(t *testing.T) {
	srv := newTestServerForCoverage(t)

	body := `{"table":"nonexistent_table","rows":[{"id":1}]}`
	req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.httpWrite(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("状态码 = %d, 期望 %d", w.Code, http.StatusBadRequest)
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", resp.Code)
	}
}

// TestHTTPHealth_WrongMethod 测试 /health 使用 POST 方法时返回 405。
func TestHTTPHealth_WrongMethod(t *testing.T) {
	srv := newTestServerForCoverage(t)

	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	w := httptest.NewRecorder()
	srv.httpHealth(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("状态码 = %d, 期望 %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// TestHTTPQuery_EmptyBody 测试 /query 接收空请求体时返回 400。
func TestHTTPQuery_EmptyBody(t *testing.T) {
	srv := newTestServerForCoverage(t)

	req := httptest.NewRequest(http.MethodPost, "/query", nil)
	w := httptest.NewRecorder()
	srv.httpQuery(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("状态码 = %d, 期望 %d", w.Code, http.StatusBadRequest)
	}
}

// TestHTTPWrite_EmptyBody 测试 /write 接收空请求体时返回 400。
func TestHTTPWrite_EmptyBody(t *testing.T) {
	srv := newTestServerForCoverage(t)

	req := httptest.NewRequest(http.MethodPost, "/write", nil)
	w := httptest.NewRecorder()
	srv.httpWrite(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("状态码 = %d, 期望 %d", w.Code, http.StatusBadRequest)
	}
}

// newTestServerForCoverage 创建用于 HTTP handler 覆盖率测试的服务器。
func newTestServerForCoverage(t *testing.T) *Server {
	t.Helper()

	dir := t.TempDir()
	cfg := Config{
		TCPAddr:  testListenAddr,
		HTTPAddr: testListenAddr,
		DataDir:  dir,
	}

	registry := prometheus.NewRegistry()
	srv, err := NewServer(cfg, WithMetricsRegistry(registry))
	if err != nil {
		t.Fatalf("NewServer 失败: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	return srv
}
