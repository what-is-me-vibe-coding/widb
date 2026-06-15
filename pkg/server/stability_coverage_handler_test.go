package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// ---------------------------------------------------------------------------
// TCP handler 边界情况
// ---------------------------------------------------------------------------

// TestStabilityHandleQueryPacketInvalidJSON 测试 handleQueryPacket 接收无效 JSON 的错误路径。
func TestStabilityHandleQueryPacketInvalidJSON(t *testing.T) {
	srv := newStabilityServer(t)

	tests := []struct {
		name    string
		payload []byte
	}{
		{"空字节", []byte{}},
		{"不完整JSON", []byte("{")},
		{"纯数字", []byte("42")},
		{"纯文本", []byte("not json at all")},
		{"二进制垃圾", []byte{0xDE, 0xAD, 0xBE}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(PacketQuery, tt.payload)
			resp, err := srv.handleQueryPacket(pkt)
			if err == nil {
				t.Error("期望无效 JSON 返回错误")
			}
			if resp != nil {
				t.Errorf("期望 resp 为 nil，得到 %v", resp)
			}
		})
	}
}

// TestStabilityHandleWritePacketInvalidJSON 测试 handleWritePacket 接收无效 JSON 的错误路径。
func TestStabilityHandleWritePacketInvalidJSON(t *testing.T) {
	srv := newStabilityServer(t)

	tests := []struct {
		name    string
		payload []byte
	}{
		{"空字节", []byte{}},
		{"不完整JSON", []byte("{")},
		{"纯数字", []byte("42")},
		{"纯文本", []byte("not json at all")},
		{"二进制垃圾", []byte{0xCA, 0xFE}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(PacketWrite, tt.payload)
			resp, err := srv.handleWritePacket(pkt)
			if err == nil {
				t.Error("期望无效 JSON 返回错误")
			}
			if resp != nil {
				t.Errorf("期望 resp 为 nil，得到 %v", resp)
			}
		})
	}
}

// TestStabilityHandlePingNormal 测试 handlePing 正常路径。
func TestStabilityHandlePingNormal(t *testing.T) {
	srv := newStabilityServer(t)

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
	if resp.Magic != Magic {
		t.Errorf("Magic = 0x%08x，期望 0x%08x", resp.Magic, Magic)
	}
	if resp.Version != ProtocolVersion {
		t.Errorf("Version = %d，期望 %d", resp.Version, ProtocolVersion)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("反序列化响应失败: %v", err)
	}
	if response.Code != 0 {
		t.Errorf("Code = %d，期望 0", response.Code)
	}
	if response.Message != msgPong {
		t.Errorf("Message = %q，期望 %q", response.Message, msgPong)
	}
}

// TestStabilityHandlePacketUnknownType 测试 handlePacket 未知包类型的错误路径。
func TestStabilityHandlePacketUnknownType(t *testing.T) {
	srv := newStabilityServer(t)

	tests := []struct {
		name    string
		pktType uint8
	}{
		{"类型0", 0},
		{"类型4", 4},
		{"类型99", 99},
		{"类型255", 255},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(tt.pktType, nil)
			resp, err := srv.handlePacket(pkt)
			if err == nil {
				t.Error("期望未知包类型返回错误")
			}
			if resp != nil {
				t.Errorf("期望 resp 为 nil，得到 %v", resp)
			}
		})
	}
}

func checkErrorResponsePacket(t *testing.T, pkt *Packet, wantMsg string) {
	t.Helper()
	if pkt == nil {
		t.Fatal("newErrorResponse 不应返回 nil")
	}
	if pkt.Type != PacketResponse {
		t.Errorf("Type = %d，期望 %d", pkt.Type, PacketResponse)
	}
	if pkt.Magic != Magic {
		t.Errorf("Magic = 0x%08x，期望 0x%08x", pkt.Magic, Magic)
	}
	if pkt.Version != ProtocolVersion {
		t.Errorf("Version = %d，期望 %d", pkt.Version, ProtocolVersion)
	}
	var resp Response
	if err := json.Unmarshal(pkt.Payload, &resp); err != nil {
		t.Fatalf("JSON 反序列化失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("Code = %d，期望 -1", resp.Code)
	}
	if resp.Message != wantMsg {
		t.Errorf("Message = %q，期望 %q", resp.Message, wantMsg)
	}
}

// TestStabilityNewErrorResponse 测试 newErrorResponse 函数。
func TestStabilityNewErrorResponse(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{"简单错误", errors.New("something failed"), "something failed"},
		{"格式化错误", fmt.Errorf("query error: %s", "bad syntax"), "query error: bad syntax"},
		{"空消息错误", errors.New(""), ""},
		{"wrapped 错误", fmt.Errorf("outer: %w", errors.New("inner")), "outer: inner"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkErrorResponsePacket(t, newErrorResponse(tt.err), tt.wantMsg)
		})
	}
}

// TestStabilityIsClosedConnErr 测试 isClosedConnErr 对各种错误类型的判断。
func TestStabilityIsClosedConnErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"io.EOF", io.EOF, true},
		{"net.ErrClosed", net.ErrClosed, true},
		{"wrapped net.ErrClosed", fmt.Errorf("wrap: %w", net.ErrClosed), true},
		{"timeout OpError", &net.OpError{Op: "read", Net: "tcp", Err: timeoutError{}}, true},
		{"非timeout OpError", &net.OpError{Op: "read", Net: "tcp", Err: errors.New("reset")}, false},
		{"普通错误", errors.New("some error"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isClosedConnErr(tt.err); got != tt.want {
				t.Errorf("isClosedConnErr() = %v，期望 %v", got, tt.want)
			}
		})
	}
}

// TestStabilityIsTransientAcceptErr 测试 isTransientAcceptErr 对各种错误类型的判断。
func TestStabilityIsTransientAcceptErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			"timeout OpError",
			&net.OpError{Op: "accept", Net: "tcp", Err: timeoutError{}},
			true,
		},
		{
			"resource temporarily unavailable",
			&net.OpError{Op: "accept", Net: "tcp", Err: errors.New("resource temporarily unavailable")},
			true,
		},
		{
			"too many open files",
			&net.OpError{Op: "accept", Net: "tcp", Err: errors.New("too many open files")},
			true,
		},
		{
			"其他 OpError 消息",
			&net.OpError{Op: "accept", Net: "tcp", Err: errors.New("connection refused")},
			false,
		},
		{
			"普通错误",
			errors.New("some error"),
			false,
		},
		{
			"nil",
			nil,
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransientAcceptErr(tt.err); got != tt.want {
				t.Errorf("isTransientAcceptErr() = %v，期望 %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// HTTP handler 边界情况
// ---------------------------------------------------------------------------

// TestStabilityHTTPQueryGetMethod 测试 httpQuery 使用 GET 方法时返回 405。
func TestStabilityHTTPQueryGetMethod(t *testing.T) {
	srv := newStabilityServer(t)

	req := httptest.NewRequest(http.MethodGet, "/query", http.NoBody)
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
		t.Errorf("响应消息应包含 'POST'，实际: %q", resp.Message)
	}
}

// TestStabilityHTTPWriteGetMethod 测试 httpWrite 使用 GET 方法时返回 405。
func TestStabilityHTTPWriteGetMethod(t *testing.T) {
	srv := newStabilityServer(t)

	req := httptest.NewRequest(http.MethodGet, "/write", http.NoBody)
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
		t.Errorf("响应消息应包含 'POST'，实际: %q", resp.Message)
	}
}

// TestStabilityHTTPHealthPostMethod 测试 httpHealth 使用 POST 方法时返回 405。
func TestStabilityHTTPHealthPostMethod(t *testing.T) {
	srv := newStabilityServer(t)

	req := httptest.NewRequest(http.MethodPost, "/health", http.NoBody)
	w := httptest.NewRecorder()
	srv.httpHealth(w, req)

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
	if !strings.Contains(resp.Message, "GET") {
		t.Errorf("响应消息应包含 'GET'，实际: %q", resp.Message)
	}
}

// TestStabilityHTTPQueryInvalidJSON 测试 httpQuery 使用无效 JSON 请求体。
func TestStabilityHTTPQueryInvalidJSON(t *testing.T) {
	srv := newStabilityServer(t)

	tests := []struct {
		name string
		body string
	}{
		{"纯文本", "hello world"},
		{"不完整JSON", `{"sql":`},
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
				t.Errorf("响应消息应包含 '解析请求体失败'，实际: %q", resp.Message)
			}
		})
	}
}

// TestStabilityHTTPWriteInvalidJSON 测试 httpWrite 使用无效 JSON 请求体。
func TestStabilityHTTPWriteInvalidJSON(t *testing.T) {
	srv := newStabilityServer(t)

	tests := []struct {
		name string
		body string
	}{
		{"纯文本", "hello world"},
		{"不完整JSON", `{"table":`},
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
				t.Errorf("响应消息应包含 '解析请求体失败'，实际: %q", resp.Message)
			}
		})
	}
}

// TestStabilityWriteJSON 测试 writeJSON 函数正常路径。
func TestStabilityWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()

	data := &Response{Code: 0, Message: "ok"}
	writeJSON(w, http.StatusOK, data)

	if w.Code != http.StatusOK {
		t.Errorf("状态码 = %d，期望 %d", w.Code, http.StatusOK)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("Content-Type = %q，期望包含 'application/json'", contentType)
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Code != 0 {
		t.Errorf("响应 Code = %d，期望 0", resp.Code)
	}
	if resp.Message != "ok" {
		t.Errorf("响应 Message = %q，期望 'ok'", resp.Message)
	}
}

// TestStabilityWriteJSON_NonOKStatus 测试 writeJSON 使用非 200 状态码。
func TestStabilityWriteJSON_NonOKStatus(t *testing.T) {
	w := httptest.NewRecorder()

	data := &Response{Code: -1, Message: "仅支持 POST 方法"}
	writeJSON(w, http.StatusMethodNotAllowed, data)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("状态码 = %d，期望 %d", w.Code, http.StatusMethodNotAllowed)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("Content-Type = %q，期望包含 'application/json'", contentType)
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d，期望 -1", resp.Code)
	}
}

// TestStabilityWriteJSON_MapData 测试 writeJSON 写入 map 类型数据。
func TestStabilityWriteJSON_MapData(t *testing.T) {
	w := httptest.NewRecorder()

	data := map[string]any{
		"status":    "ok",
		"timestamp": "2024-01-01T00:00:00Z",
	}
	writeJSON(w, http.StatusOK, data)

	if w.Code != http.StatusOK {
		t.Errorf("状态码 = %d，期望 %d", w.Code, http.StatusOK)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("status = %v，期望 'ok'", result["status"])
	}
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

// newStabilityServer 创建用于稳定性覆盖率测试的服务器实例。
func newStabilityServer(t *testing.T) *Server {
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
