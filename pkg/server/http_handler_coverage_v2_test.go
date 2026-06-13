package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// V7: httpQuery / httpWrite comprehensive coverage
// ---------------------------------------------------------------------------

// TestHTTPQuery_PostValidSQLV7 测试 httpQuery 使用 POST 方法发送有效 SQL 查询。
func TestHTTPQuery_PostValidSQLV7(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	// 先写入数据
	writeBody := testWriteAliceBody
	writeReq := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(writeBody))
	writeW := httptest.NewRecorder()
	srv.httpWrite(writeW, writeReq)
	if writeW.Code != http.StatusOK {
		t.Fatalf("写入预置数据失败: 状态码=%d", writeW.Code)
	}

	// 发送有效查询
	queryBody := `{"sql":"SELECT * FROM users"}`
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(queryBody))
	w := httptest.NewRecorder()
	srv.httpQuery(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("状态码 = %d，期望 %d，Body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Code != 0 {
		t.Errorf("响应 Code = %d，期望 0，Message = %q", resp.Code, resp.Message)
	}
}

// TestHTTPQuery_PostInvalidJSONV7 测试 httpQuery 使用 POST 方法发送无效 JSON 请求体。
func TestHTTPQuery_PostInvalidJSONV7(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name string
		body string
	}{
		{testPlainText, "hello world"},
		{"不完整JSON", `{"sql":`},
		{testJSONArray, `[1,2,3]`},
		{"空字符串", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			srv.httpQuery(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("状态码 = %d，期望 %d", w.Code, http.StatusBadRequest)
			}

			var resp Response
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("解析响应失败: %v", err)
			}
			if resp.Code != -1 {
				t.Errorf("响应 Code = %d，期望 -1", resp.Code)
			}
			if !strings.Contains(resp.Message, "解析请求体失败") {
				t.Errorf("响应 Message = %q，期望包含 '解析请求体失败'", resp.Message)
			}
		})
	}
}

// TestHTTPQuery_GetMethodRejectedV7 测试 httpQuery 使用 GET 方法被拒绝。
func TestHTTPQuery_GetMethodRejectedV7(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	req := httptest.NewRequest(http.MethodGet, "/query", nil)
	w := httptest.NewRecorder()
	srv.httpQuery(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("状态码 = %d，期望 %d", w.Code, http.StatusMethodNotAllowed)
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d，期望 -1", resp.Code)
	}
	if !strings.Contains(resp.Message, "POST") {
		t.Errorf("响应 Message = %q，期望包含 'POST'", resp.Message)
	}
}

// TestHTTPQuery_QueryErrorV7 测试 httpQuery 查询执行错误时返回 HTTP 400。
func TestHTTPQuery_QueryErrorV7(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	// 发送无效 SQL
	body := testInvalidSQLBody
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.httpQuery(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("状态码 = %d，期望 %d", w.Code, http.StatusBadRequest)
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d，期望 -1", resp.Code)
	}
}

// TestHTTPQuery_PutMethodRejectedV7 测试 httpQuery 使用 PUT 方法被拒绝。
func TestHTTPQuery_PutMethodRejectedV7(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	req := httptest.NewRequest(http.MethodPut, "/query", strings.NewReader(`{"sql":"SELECT 1"}`))
	w := httptest.NewRecorder()
	srv.httpQuery(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("状态码 = %d，期望 %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// TestHTTPWrite_PostValidDataV7 测试 httpWrite 使用 POST 方法发送有效写入数据。
func TestHTTPWrite_PostValidDataV7(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	body := `{"table":"users","rows":[{"id":1,"name":"alice"},{"id":2,"name":"bob"}]}`
	req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.httpWrite(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("状态码 = %d，期望 %d，Body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Code != 0 {
		t.Errorf("响应 Code = %d，期望 0，Message = %q", resp.Code, resp.Message)
	}
	if resp.Rows != 2 {
		t.Errorf("写入行数 = %d，期望 2", resp.Rows)
	}
}

// TestHTTPWrite_PostInvalidJSONV7 测试 httpWrite 使用 POST 方法发送无效 JSON 请求体。
func TestHTTPWrite_PostInvalidJSONV7(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name string
		body string
	}{
		{testPlainText, "hello world"},
		{"不完整JSON", `{"table":`},
		{testJSONArray, `[1,2,3]`},
		{"空字符串", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			srv.httpWrite(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("状态码 = %d，期望 %d", w.Code, http.StatusBadRequest)
			}

			var resp Response
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("解析响应失败: %v", err)
			}
			if resp.Code != -1 {
				t.Errorf("响应 Code = %d，期望 -1", resp.Code)
			}
			if !strings.Contains(resp.Message, "解析请求体失败") {
				t.Errorf("响应 Message = %q，期望包含 '解析请求体失败'", resp.Message)
			}
		})
	}
}

// TestHTTPWrite_GetMethodRejectedV7 测试 httpWrite 使用 GET 方法被拒绝。
func TestHTTPWrite_GetMethodRejectedV7(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	req := httptest.NewRequest(http.MethodGet, "/write", nil)
	w := httptest.NewRecorder()
	srv.httpWrite(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("状态码 = %d，期望 %d", w.Code, http.StatusMethodNotAllowed)
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d，期望 -1", resp.Code)
	}
	if !strings.Contains(resp.Message, "POST") {
		t.Errorf("响应 Message = %q，期望包含 'POST'", resp.Message)
	}
}

// TestHTTPWrite_WriteErrorV7 测试 httpWrite 写入执行错误时返回 HTTP 400。
func TestHTTPWrite_WriteErrorV7(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	// 写入不存在的表
	body := `{"table":"nonexistent_v7","rows":[{"id":1}]}`
	req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.httpWrite(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("状态码 = %d，期望 %d", w.Code, http.StatusBadRequest)
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d，期望 -1", resp.Code)
	}
}

// TestHTTPWrite_DeleteMethodRejectedV7 测试 httpWrite 使用 DELETE 方法被拒绝。
func TestHTTPWrite_DeleteMethodRejectedV7(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	req := httptest.NewRequest(http.MethodDelete, "/write", nil)
	w := httptest.NewRecorder()
	srv.httpWrite(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("状态码 = %d，期望 %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// TestHTTPQuery_ResponseContentTypeV7 测试 httpQuery 响应的 Content-Type 为 JSON。
func TestHTTPQuery_ResponseContentTypeV7(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	req := httptest.NewRequest(http.MethodGet, "/query", nil)
	w := httptest.NewRecorder()
	srv.httpQuery(w, req)

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("Content-Type = %q，期望包含 'application/json'", contentType)
	}
}

// TestHTTPWrite_ResponseContentTypeV7 测试 httpWrite 响应的 Content-Type 为 JSON。
func TestHTTPWrite_ResponseContentTypeV7(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	req := httptest.NewRequest(http.MethodGet, "/write", nil)
	w := httptest.NewRecorder()
	srv.httpWrite(w, req)

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("Content-Type = %q，期望包含 'application/json'", contentType)
	}
}
