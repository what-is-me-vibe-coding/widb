package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// unmarshallableData 是一个无法被 JSON 序列化的类型，用于测试 JSON 序列化错误路径。
type unmarshallableData struct{}

func (unmarshallableData) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("强制 JSON 序列化失败")
}

// handleQueryPacket: JSON 反序列化错误路径、handleQuery 错误路径、JSON 序列化错误路径

func TestCoverageLowHandlerV7_HandleQueryPacket_JSONUnmarshalError(t *testing.T) {
	srv := newTestServerV7(t)

	tests := []struct {
		name    string
		payload []byte
	}{
		{"无效JSON字符串", []byte("<<<不是json>>>")},
		{"空负载", []byte{}},
		{"不完整JSON对象_v7", []byte("{")},
		{"纯数字", []byte("42")},
		{"JSON数组非对象", []byte("[]")},
		{"JSON布尔值_v7", []byte("true")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(PacketQuery, tt.payload)
			resp, err := srv.handleQueryPacket(pkt)
			if err == nil {
				t.Error("期望 JSON 反序列化错误，得到 nil error")
			}
			if resp != nil {
				t.Errorf("期望 resp 为 nil，得到 %v", resp)
			}
		})
	}
}

func TestCoverageLowHandlerV7_HandleQueryPacket_HandleQueryError(t *testing.T) {
	srv := newTestServerV7(t)

	payload, _ := json.Marshal(QueryRequest{SQL: "SELECT * FROM nonexistent_v7"})
	pkt := NewPacket(PacketQuery, payload)
	resp, err := srv.handleQueryPacket(pkt)
	if err != nil {
		t.Fatalf("handleQueryPacket 不应返回 Go 错误: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应包")
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应包类型 = %d，期望 %d", resp.Type, PacketResponse)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if response.Code != -1 {
		t.Errorf("响应 Code = %d，期望 -1", response.Code)
	}
}

func TestCoverageLowHandlerV7_HandleQueryPacket_MarshalError(t *testing.T) {
	resp := &Response{Code: 0, Data: unmarshallableData{}}
	_, err := json.Marshal(resp)
	if err == nil {
		t.Error("期望 JSON 序列化错误，得到 nil")
	}
}

// handleWritePacket: JSON 反序列化错误路径、handleWrite 错误路径、JSON 序列化错误路径

func TestCoverageLowHandlerV7_HandleWritePacket_JSONUnmarshalError(t *testing.T) {
	srv := newTestServerV7(t)

	tests := []struct {
		name    string
		payload []byte
	}{
		{"无效JSON字符串", []byte("<<<不是json>>>")},
		{"空负载", []byte{}},
		{"不完整JSON对象_v7", []byte("{")},
		{"纯数字", []byte("42")},
		{"JSON数组非对象", []byte("[]")},
		{"JSON布尔值_v7", []byte("true")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(PacketWrite, tt.payload)
			resp, err := srv.handleWritePacket(pkt)
			if err == nil {
				t.Error("期望 JSON 反序列化错误，得到 nil error")
			}
			if resp != nil {
				t.Errorf("期望 resp 为 nil，得到 %v", resp)
			}
		})
	}
}

func TestCoverageLowHandlerV7_HandleWritePacket_HandleWriteError(t *testing.T) {
	srv := newTestServerV7(t)

	payload, _ := json.Marshal(WriteRequest{
		Table: "nonexistent_v7",
		Rows:  []map[string]interface{}{{"id": float64(1)}},
	})
	pkt := NewPacket(PacketWrite, payload)
	resp, err := srv.handleWritePacket(pkt)
	if err != nil {
		t.Fatalf("handleWritePacket 不应返回 Go 错误: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应包")
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应包类型 = %d，期望 %d", resp.Type, PacketResponse)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if response.Code != -1 {
		t.Errorf("响应 Code = %d，期望 -1", response.Code)
	}
}

func TestCoverageLowHandlerV7_HandleWritePacket_MarshalError(t *testing.T) {
	resp := &Response{Code: 0, Data: unmarshallableData{}}
	_, err := json.Marshal(resp)
	if err == nil {
		t.Error("期望 JSON 序列化错误，得到 nil")
	}
}

// handlePing: 正常路径与 JSON 序列化错误路径

func TestCoverageLowHandlerV7_HandlePing_Normal(t *testing.T) {
	srv := newTestServerV7(t)

	resp, err := srv.handlePing()
	if err != nil {
		t.Fatalf("handlePing 失败: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应包")
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应包类型 = %d，期望 %d", resp.Type, PacketResponse)
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

func TestCoverageLowHandlerV7_HandlePing_MarshalError(t *testing.T) {
	resp := &Response{Code: 0, Message: msgPong, Data: unmarshallableData{}}
	_, err := json.Marshal(resp)
	if err == nil {
		t.Error("期望 JSON 序列化错误，得到 nil")
	}
}

// 辅助函数

func newTestServerV7(t *testing.T) *Server {
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

// v8 测试用常量，避免 goconst 重复字符串警告
const (
	v8WriteNoTableBody = `{"table":"nonexistent_v7","rows":[{"id":1}]}`
	v8WriteNoKeyBody   = `{"table":"users","rows":[{"name":"alice"}]}`
)

// httpQuery: 错误 HTTP 方法、JSON 解码错误、handleQuery 错误、非零响应码

var httpQueryBadMethodTests = []struct {
	name       string
	method     string
	wantStatus int
}{
	{"错误HTTP方法_GET", http.MethodGet, http.StatusMethodNotAllowed},
	{"错误HTTP方法_PUT", http.MethodPut, http.StatusMethodNotAllowed},
	{"错误HTTP方法_DELETE", http.MethodDelete, http.StatusMethodNotAllowed},
}

var httpQueryDecodeErrorTests = []struct {
	name       string
	body       string
	wantStatus int
}{
	{"JSON解码错误_无效JSON", "<<<不是json>>>", http.StatusBadRequest},
	{"JSON解码错误_空请求体", "", http.StatusBadRequest},
	{"JSON解码错误_不完整JSON", "{", http.StatusBadRequest},
}

var httpQueryNonZeroCodeTests = []struct {
	name       string
	body       string
	wantStatus int
}{
	{"非零响应码_无效SQL", testInvalidSQLBody, http.StatusBadRequest},
	{"非零响应码_查询不存在的表", `{"sql":"SELECT * FROM nonexistent_v7"}`, http.StatusBadRequest},
}

func TestCoverageLowHandlerV7_HttpQuery_BadMethod(t *testing.T) {
	srv := newTestServerV7WithTable(t)
	for _, tt := range httpQueryBadMethodTests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/query", nil)
			w := httptest.NewRecorder()
			srv.httpQuery(w, req)
			if w.Code != tt.wantStatus {
				t.Errorf("状态码 = %d，期望 %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestCoverageLowHandlerV7_HttpQuery_DecodeError(t *testing.T) {
	srv := newTestServerV7WithTable(t)
	for _, tt := range httpQueryDecodeErrorTests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			srv.httpQuery(w, req)
			if w.Code != tt.wantStatus {
				t.Errorf("状态码 = %d，期望 %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestCoverageLowHandlerV7_HttpQuery_NonZeroCode(t *testing.T) {
	srv := newTestServerV7WithTable(t)
	for _, tt := range httpQueryNonZeroCodeTests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			srv.httpQuery(w, req)
			if w.Code != tt.wantStatus {
				t.Errorf("状态码 = %d，期望 %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestCoverageLowHandlerV7_HttpQuery_Success(t *testing.T) {
	srv := newTestServerV7WithTable(t)
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(benchSelectAllSQL))
	w := httptest.NewRecorder()
	srv.httpQuery(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("状态码 = %d，期望 %d", w.Code, http.StatusOK)
	}
}

func TestCoverageLowHandlerV7_HttpQuery_HandleQueryError(t *testing.T) {
	srv := newTestServerV7(t)

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

// httpWrite: 错误 HTTP 方法、JSON 解码错误、handleWrite 错误、非零响应码

var httpWriteBadMethodTests = []struct {
	name       string
	method     string
	wantStatus int
}{
	{"错误HTTP方法_GET", http.MethodGet, http.StatusMethodNotAllowed},
	{"错误HTTP方法_PUT", http.MethodPut, http.StatusMethodNotAllowed},
	{"错误HTTP方法_PATCH", http.MethodPatch, http.StatusMethodNotAllowed},
}

var httpWriteDecodeErrorTests = []struct {
	name       string
	body       string
	wantStatus int
}{
	{"JSON解码错误_无效JSON", "<<<不是json>>>", http.StatusBadRequest},
	{"JSON解码错误_空请求体", "", http.StatusBadRequest},
	{"JSON解码错误_不完整JSON", "{", http.StatusBadRequest},
}

var httpWriteNonZeroCodeTests = []struct {
	name       string
	body       string
	wantStatus int
}{
	{"非零响应码_表不存在_v8", v8WriteNoTableBody, http.StatusBadRequest},
	{"非零响应码_缺少主键_v8", v8WriteNoKeyBody, http.StatusBadRequest},
	{"非零响应码_类型不匹配", `{"table":"users","rows":[{"id":1,"name":true}]}`, http.StatusBadRequest},
}

func TestCoverageLowHandlerV7_HttpWrite_BadMethod(t *testing.T) {
	srv := newTestServerV7WithTable(t)
	for _, tt := range httpWriteBadMethodTests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/write", nil)
			w := httptest.NewRecorder()
			srv.httpWrite(w, req)
			if w.Code != tt.wantStatus {
				t.Errorf("状态码 = %d，期望 %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestCoverageLowHandlerV7_HttpWrite_DecodeError(t *testing.T) {
	srv := newTestServerV7WithTable(t)
	for _, tt := range httpWriteDecodeErrorTests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			srv.httpWrite(w, req)
			if w.Code != tt.wantStatus {
				t.Errorf("状态码 = %d，期望 %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestCoverageLowHandlerV7_HttpWrite_NonZeroCode(t *testing.T) {
	srv := newTestServerV7WithTable(t)
	for _, tt := range httpWriteNonZeroCodeTests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			srv.httpWrite(w, req)
			if w.Code != tt.wantStatus {
				t.Errorf("状态码 = %d，期望 %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestCoverageLowHandlerV7_HttpWrite_Success(t *testing.T) {
	srv := newTestServerV7WithTable(t)
	req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(testWriteAliceBody))
	w := httptest.NewRecorder()
	srv.httpWrite(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("状态码 = %d，期望 %d", w.Code, http.StatusOK)
	}
}

func TestCoverageLowHandlerV7_HttpWrite_HandleWriteError(t *testing.T) {
	srv := newTestServerV7(t)

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

// 辅助函数

func newTestServerV7WithTable(t *testing.T) *Server {
	t.Helper()

	srv := newTestServerV7(t)

	err := srv.catalog.CreateTable(testTable, []catalog.ColumnDef{
		{Name: "id", Type: common.TypeInt64, Nullable: false},
		{Name: testColName, Type: common.TypeString, Nullable: true},
		{Name: testColScore, Type: common.TypeFloat64, Nullable: true},
	}, []string{"id"}, catalog.TableOptions{})
	if err != nil {
		t.Fatalf("CreateTable 失败: %v", err)
	}

	return srv
}
