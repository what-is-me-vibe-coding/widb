package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- HTTP 处理器测试 ---

func TestHTTPHealth(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	srv.httpHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("状态码 = %d, 期望 %d", w.Code, http.StatusOK)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("status = %v, 期望 'ok'", result["status"])
	}
}

func TestHTTPHealthWrongMethod(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	w := httptest.NewRecorder()
	srv.httpHealth(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("状态码 = %d, 期望 %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHTTPMetrics(t *testing.T) {
	srv := newTestServer(t)
	mux := srv.registerHTTPHandlers()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("状态码 = %d, 期望 %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "widb_queries_total") {
		t.Error("metrics 应包含 widb_queries_total")
	}
	if !strings.Contains(body, "widb_writes_total") {
		t.Error("metrics 应包含 widb_writes_total")
	}
	if !strings.Contains(body, "widb_memtable_size_bytes") {
		t.Error("metrics 应包含 widb_memtable_size_bytes")
	}
}

func TestHTTPQueryWrongMethod(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/query", nil)
	w := httptest.NewRecorder()
	srv.httpQuery(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("状态码 = %d, 期望 %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHTTPQueryInvalidBody(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader("invalid json"))
	w := httptest.NewRecorder()
	srv.httpQuery(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("状态码 = %d, 期望 %d", w.Code, http.StatusBadRequest)
	}
}

func TestHTTPQueryValidSQL(t *testing.T) {
	srv := newTestServerWithTable(t)
	body := `{"sql":"SELECT * FROM users"}`
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.httpQuery(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("状态码 = %d, 期望 %d", w.Code, http.StatusOK)
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Code != 0 {
		t.Errorf("响应 Code = %d, 期望 0, Message = %q", resp.Code, resp.Message)
	}
}

func TestHTTPQueryInvalidSQL(t *testing.T) {
	srv := newTestServer(t)
	body := `{"sql":"INVALID SQL !!!"}`
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.httpQuery(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("状态码 = %d, 期望 %d", w.Code, http.StatusBadRequest)
	}
}

func TestHTTPWriteWrongMethod(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/write", nil)
	w := httptest.NewRecorder()
	srv.httpWrite(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("状态码 = %d, 期望 %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHTTPWriteInvalidBody(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader("invalid json"))
	w := httptest.NewRecorder()
	srv.httpWrite(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("状态码 = %d, 期望 %d", w.Code, http.StatusBadRequest)
	}
}

func TestHTTPWriteTableNotExist(t *testing.T) {
	srv := newTestServer(t)
	body := `{"table":"nonexistent","rows":[{"id":1}]}`
	req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.httpWrite(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("状态码 = %d, 期望 %d", w.Code, http.StatusBadRequest)
	}
}

func TestHTTPWriteValid(t *testing.T) {
	srv := newTestServerWithTable(t)
	body := `{"table":"users","rows":[{"id":1,"name":"alice"},{"id":2,"name":"bob"}]}`
	req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.httpWrite(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("状态码 = %d, 期望 %d, Body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Code != 0 {
		t.Errorf("响应 Code = %d, 期望 0, Message = %q", resp.Code, resp.Message)
	}
	if resp.Rows != 2 {
		t.Errorf("写入行数 = %d, 期望 2", resp.Rows)
	}
}

func TestHTTPWriteMissingPK(t *testing.T) {
	srv := newTestServerWithTable(t)
	body := `{"table":"users","rows":[{"name":"alice"}]}`
	req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.httpWrite(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("状态码 = %d, 期望 %d", w.Code, http.StatusBadRequest)
	}
}

// --- 路由注册测试 ---

func TestRegisterHTTPHandlers(t *testing.T) {
	srv := newTestServer(t)
	mux := srv.registerHTTPHandlers()

	routes := []struct {
		path   string
		method string
	}{
		{"/query", http.MethodPost},
		{"/write", http.MethodPost},
		{"/health", http.MethodGet},
		{"/metrics", http.MethodGet},
	}

	for _, tt := range routes {
		t.Run(tt.path, func(t *testing.T) {
			var req *http.Request
			if tt.method == http.MethodGet {
				req = httptest.NewRequest(tt.method, tt.path, nil)
			} else {
				req = httptest.NewRequest(tt.method, tt.path, strings.NewReader("{}"))
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code == http.StatusNotFound {
				t.Errorf("路由 %s 未注册", tt.path)
			}
		})
	}
}

// --- Metrics 集成测试 ---

func TestMetricsRecordedOnQuery(t *testing.T) {
	srv := newTestServerWithTable(t)
	mux := srv.registerHTTPHandlers()

	// 执行一次查询
	body := `{"sql":"SELECT * FROM users"}`
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// 检查 metrics 端点是否记录了查询指标
	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsW := httptest.NewRecorder()
	mux.ServeHTTP(metricsW, metricsReq)

	metricsBody := metricsW.Body.String()
	if !strings.Contains(metricsBody, "widb_queries_total") {
		t.Error("metrics 应记录 widb_queries_total")
	}
}

func TestMetricsRecordedOnWrite(t *testing.T) {
	srv := newTestServerWithTable(t)
	mux := srv.registerHTTPHandlers()

	// 执行一次写入
	body := `{"table":"users","rows":[{"id":1,"name":"alice"}]}`
	req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// 检查 metrics 端点是否记录了写入指标
	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsW := httptest.NewRecorder()
	mux.ServeHTTP(metricsW, metricsReq)

	metricsBody := metricsW.Body.String()
	if !strings.Contains(metricsBody, "widb_writes_total") {
		t.Error("metrics 应记录 widb_writes_total")
	}
}
