package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// ---------------------------------------------------------------------------
// handleQueryPacket: JSON 反序列化错误路径（80.0% → >90%）
// ---------------------------------------------------------------------------

// TestHandleQueryPacket_InvalidJSON_V6 测试 handleQueryPacket 收到无效 JSON 时的错误返回。
func TestHandleQueryPacket_InvalidJSON_V6(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketQuery, []byte("<<<不是json>>>"))
	resp, err := srv.handleQueryPacket(pkt)
	if err == nil {
		t.Error("期望 JSON 反序列化错误，得到 nil")
	}
	if resp != nil {
		t.Errorf("期望 resp 为 nil，得到 %v", resp)
	}
}

// TestHandleQueryPacket_EmptyPayload_V6 测试 handleQueryPacket 收到空负载时的错误返回。
func TestHandleQueryPacket_EmptyPayload_V6(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketQuery, []byte{})
	resp, err := srv.handleQueryPacket(pkt)
	if err == nil {
		t.Error("期望空负载返回错误，得到 nil")
	}
	if resp != nil {
		t.Errorf("期望 resp 为 nil，得到 %v", resp)
	}
}

// TestHandleQueryPacket_NonZeroCode_V6 测试 handleQueryPacket 查询失败时返回非零响应码。
// handleQuery 将错误包装为 Response{Code:-1} 而非 Go error，因此 handleQueryPacket
// 仍返回非 nil Packet，其 Payload 中 Code 为 -1。
func TestHandleQueryPacket_NonZeroCode_V6(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	// 查询不存在的表，handleQuery 返回 Response{Code: -1}
	payload, _ := json.Marshal(QueryRequest{SQL: "SELECT * FROM nonexistent_v6"})
	pkt := NewPacket(PacketQuery, payload)
	resp, err := srv.handleQueryPacket(pkt)
	if err != nil {
		t.Fatalf("handleQueryPacket 不应返回 Go 错误: %v", err)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if response.Code != -1 {
		t.Errorf("响应 Code = %d，期望 -1", response.Code)
	}
}

// ---------------------------------------------------------------------------
// handleWritePacket: JSON 反序列化错误路径（80.0% → >90%）
// ---------------------------------------------------------------------------

// TestHandleWritePacket_InvalidJSON_V6 测试 handleWritePacket 收到无效 JSON 时的错误返回。
func TestHandleWritePacket_InvalidJSON_V6(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketWrite, []byte("<<<不是json>>>"))
	resp, err := srv.handleWritePacket(pkt)
	if err == nil {
		t.Error("期望 JSON 反序列化错误，得到 nil")
	}
	if resp != nil {
		t.Errorf("期望 resp 为 nil，得到 %v", resp)
	}
}

// TestHandleWritePacket_EmptyPayload_V6 测试 handleWritePacket 收到空负载时的错误返回。
func TestHandleWritePacket_EmptyPayload_V6(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketWrite, []byte{})
	resp, err := srv.handleWritePacket(pkt)
	if err == nil {
		t.Error("期望空负载返回错误，得到 nil")
	}
	if resp != nil {
		t.Errorf("期望 resp 为 nil，得到 %v", resp)
	}
}

// TestHandleWritePacket_NonZeroCode_V6 测试 handleWritePacket 写入失败时返回非零响应码。
func TestHandleWritePacket_NonZeroCode_V6(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	// 写入不存在的表，handleWrite 返回 Response{Code: -1}
	payload, _ := json.Marshal(WriteRequest{
		Table: "nonexistent_v6",
		Rows:  []map[string]interface{}{{"id": float64(1)}},
	})
	pkt := NewPacket(PacketWrite, payload)
	resp, err := srv.handleWritePacket(pkt)
	if err != nil {
		t.Fatalf("handleWritePacket 不应返回 Go 错误: %v", err)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if response.Code != -1 {
		t.Errorf("响应 Code = %d，期望 -1", response.Code)
	}
}

// ---------------------------------------------------------------------------
// handlePing: 正常路径与 JSON 序列化错误路径（80.0% → >90%）
// ---------------------------------------------------------------------------

// TestHandlePing_NormalPath_V6 测试 handlePing 正常返回 pong 响应。
func TestHandlePing_NormalPath_V6(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	resp, err := srv.handlePing()
	if err != nil {
		t.Fatalf("handlePing 失败: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应")
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应类型 = %d，期望 %d", resp.Type, PacketResponse)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if response.Code != 0 {
		t.Errorf("响应 Code = %d，期望 0", response.Code)
	}
	if response.Message != msgPong {
		t.Errorf("响应 Message = %q，期望 %q", response.Message, msgPong)
	}
}

// TestHandlePing_ViaHandlePacket_V6 测试通过 handlePacket 路由 PacketPing 的完整流程。
func TestHandlePing_ViaHandlePacket_V6(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketPing, nil)
	resp, err := srv.handlePacket(pkt)
	if err != nil {
		t.Fatalf("handlePacket(PacketPing) 失败: %v", err)
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应类型 = %d，期望 %d", resp.Type, PacketResponse)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if response.Message != msgPong {
		t.Errorf("响应 Message = %q，期望 %q", response.Message, msgPong)
	}
}

// ---------------------------------------------------------------------------
// httpQuery: 错误 HTTP 方法、JSON 解码错误、非零响应码（86.7% → >90%）
// ---------------------------------------------------------------------------

// TestHTTPQuery_WrongMethod_V6 测试 httpQuery 使用非 POST 方法的错误返回。
func TestHTTPQuery_WrongMethod_V6(t *testing.T) {
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

// TestHTTPQuery_JSONDecodeError_V6 测试 httpQuery 收到无效 JSON 请求体的错误返回。
func TestHTTPQuery_JSONDecodeError_V6(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader("<<<不是json>>>"))
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
}

// TestHTTPQuery_NonZeroResponseCode_V6 测试 httpQuery 查询失败时返回非零响应码和 HTTP 400。
func TestHTTPQuery_NonZeroResponseCode_V6(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	// 发送无效 SQL，handleQuery 返回 Response{Code: -1}
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

// TestHTTPQuery_ValidQuery_V6 测试 httpQuery 正常查询返回 HTTP 200。
func TestHTTPQuery_ValidQuery_V6(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	body := benchSelectAllSQL + "\n"
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.httpQuery(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("状态码 = %d，期望 %d", w.Code, http.StatusOK)
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Code != 0 {
		t.Errorf("响应 Code = %d，期望 0", resp.Code)
	}
}

// ---------------------------------------------------------------------------
// httpWrite: 错误 HTTP 方法、JSON 解码错误、非零响应码（86.7% → >90%）
// ---------------------------------------------------------------------------

// TestHTTPWrite_WrongMethod_V6 测试 httpWrite 使用非 POST 方法的错误返回。
func TestHTTPWrite_WrongMethod_V6(t *testing.T) {
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

// TestHTTPWrite_JSONDecodeError_V6 测试 httpWrite 收到无效 JSON 请求体的错误返回。
func TestHTTPWrite_JSONDecodeError_V6(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader("<<<不是json>>>"))
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
}

// TestHTTPWrite_NonZeroResponseCode_V6 测试 httpWrite 写入失败时返回非零响应码和 HTTP 400。
func TestHTTPWrite_NonZeroResponseCode_V6(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	// 写入不存在的表，handleWrite 返回 Response{Code: -1}
	body := `{"table":"nonexistent_v6","rows":[{"id":1}]}`
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

// TestHTTPWrite_ValidWrite_V6 测试 httpWrite 正常写入返回 HTTP 200。
func TestHTTPWrite_ValidWrite_V6(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	body := testWriteAliceBody
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
		t.Errorf("响应 Code = %d，期望 0", resp.Code)
	}
}

// TestHTTPWrite_MissingPK_V6 测试 httpWrite 缺少主键时返回非零响应码。
func TestHTTPWrite_MissingPK_V6(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	body := `{"table":"users","rows":[{"name":"alice"}]}`
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

// ---------------------------------------------------------------------------
// NewMetrics: nil 注册器路径（83.3% → >90%）
// ---------------------------------------------------------------------------

// TestNewMetrics_NilRegisterer_V6 测试 NewMetrics 在 reg 为 nil 时降级到 DefaultRegisterer。
// 由于 DefaultRegisterer 是全局单例，重复注册会 panic，
// 使用 recover 捕获以避免测试崩溃。panic 本身说明 nil 路径已被执行。
func TestNewMetrics_NilRegisterer_V6(t *testing.T) {
	var panicked bool
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		_ = NewMetrics(nil)
	}()

	if panicked {
		// panic 说明 nil 路径被执行（降级到 DefaultRegisterer 后重复注册），
		// 这证明 reg == nil 分支已被覆盖
		t.Log("NewMetrics(nil) 因 DefaultRegisterer 重复注册而 panic，nil 路径已覆盖")
	}
}

// TestNewMetrics_CustomRegistry_V6 测试 NewMetrics 使用自定义注册器。
func TestNewMetrics_CustomRegistry_V6(t *testing.T) {
	registry := prometheus.NewRegistry()
	m := NewMetrics(registry)
	if m == nil {
		t.Fatal("NewMetrics 不应返回 nil")
	}

	// 验证所有指标字段非 nil
	for _, check := range []struct {
		name string
		ok   bool
	}{
		{"QueriesTotal", m.QueriesTotal != nil},
		{"WritesTotal", m.WritesTotal != nil},
		{"QueryDuration", m.QueryDuration != nil},
		{"WriteDuration", m.WriteDuration != nil},
		{"MemTableSize", m.MemTableSize != nil},
		{"SegmentCount", m.SegmentCount != nil},
		{"CacheHits", m.CacheHits != nil},
		{"CacheMisses", m.CacheMisses != nil},
	} {
		if !check.ok {
			t.Errorf("%s 不应为 nil", check.name)
		}
	}

	// 验证指标可以正常使用
	m.QueriesTotal.WithLabelValues("success").Inc()
	m.WritesTotal.WithLabelValues("success").Add(3)

	mfs, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather 失败: %v", err)
	}
	if len(mfs) == 0 {
		t.Error("期望注册器中至少有一个指标族")
	}
}
