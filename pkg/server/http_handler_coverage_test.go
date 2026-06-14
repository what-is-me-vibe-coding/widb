package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

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

// ---------------------------------------------------------------------------
// httpQuery / httpWrite: wrong method, invalid JSON, error paths
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// StorageClosed, InternalError, ResponseCode paths
// ---------------------------------------------------------------------------

// TestHTTPQuery_StorageClosed 测试 httpQuery 在存储引擎关闭后的行为。
// 关闭存储引擎后发送查询请求，由于数据仍在内存中，查询仍可正常返回。
// 注意：httpQuery 的内部错误路径（handleQuery 返回非 nil error）在当前实现中不可达，
// 因为 handleQuery 将所有错误封装为 Response 返回，不会返回非 nil error。
func TestHTTPQuery_StorageClosed(t *testing.T) {
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

	// 创建表并写入数据
	err = srv.catalog.CreateTable(testTable, []catalog.ColumnDef{
		{Name: "id", Type: common.TypeInt64, Nullable: false},
		{Name: testColName, Type: common.TypeString, Nullable: true},
	}, []string{"id"}, catalog.TableOptions{})
	if err != nil {
		t.Fatalf("CreateTable 失败: %v", err)
	}

	// 先写入一条数据
	writeBody := testWriteAliceBody
	writeReq := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(writeBody))
	writeW := httptest.NewRecorder()
	srv.httpWrite(writeW, writeReq)
	if writeW.Code != http.StatusOK {
		t.Fatalf("写入失败: 状态码=%d, Body=%s", writeW.Code, writeW.Body.String())
	}

	// 关闭存储引擎
	_ = srv.storage.Close()

	// 发送查询请求，数据仍在内存中，查询应正常返回
	queryBody := benchSelectAllSQL
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(queryBody))
	w := httptest.NewRecorder()
	srv.httpQuery(w, req)

	// 验证查询仍能返回结果（数据在内存中）
	if w.Code != http.StatusOK {
		t.Errorf("状态码 = %d, 期望 %d（数据仍在内存中）", w.Code, http.StatusOK)
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Code != 0 {
		t.Errorf("响应 Code = %d, 期望 0（数据仍在内存中）, Message = %q", resp.Code, resp.Message)
	}
}

// TestHTTPWrite_InternalError 测试 httpWrite 在存储引擎关闭后返回内部错误。
// 关闭存储引擎后发送有效写入请求，WriteBatch 会因 WAL 关闭而失败。
func TestHTTPWrite_InternalError(t *testing.T) {
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

	// 创建表
	err = srv.catalog.CreateTable(testTable, []catalog.ColumnDef{
		{Name: "id", Type: common.TypeInt64, Nullable: false},
		{Name: testColName, Type: common.TypeString, Nullable: true},
	}, []string{"id"}, catalog.TableOptions{})
	if err != nil {
		t.Fatalf("CreateTable 失败: %v", err)
	}

	// 关闭存储引擎，使后续写入操作触发内部错误
	_ = srv.storage.Close()

	// 发送有效的写入请求，期望触发 handleWrite 的内部错误路径
	body := testWriteAliceBody
	req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.httpWrite(w, req)

	// 验证响应包含错误信息（存储关闭后写入应返回错误）
	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	// 存储引擎关闭后，写入应返回非零 Code
	if resp.Code == 0 {
		t.Error("期望写入返回非零 Code，因为存储引擎已关闭")
	}
}

// TestHTTPQuery_ValidQueryWithResponseCode 测试 httpQuery 在查询返回非零 Code 时返回 400 状态码。
func TestHTTPQuery_ValidQueryWithResponseCode(t *testing.T) {
	srv := newTestServerForCoverage(t)

	// 发送一条无效 SQL，handleQuery 会返回 Code=-1 的 Response（非 error）
	body := testInvalidSQLBody
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.httpQuery(w, req)

	// handleQuery 返回 Code=-1 的 Response，httpQuery 应返回 400
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

// TestHTTPWrite_ValidWriteWithResponseCode 测试 httpWrite 在写入返回非零 Code 时返回 400 状态码。
func TestHTTPWrite_ValidWriteWithResponseCode(t *testing.T) {
	srv := newTestServerForCoverage(t)

	// 写入不存在的表，handleWrite 会返回 Code=-1 的 Response（非 error）
	body := `{"table":"nonexistent","rows":[{"id":1}]}`
	req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.httpWrite(w, req)

	// handleWrite 返回 Code=-1 的 Response，httpWrite 应返回 400
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

// ---------------------------------------------------------------------------
// httpQuery / httpWrite comprehensive coverage (extra tests)
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
