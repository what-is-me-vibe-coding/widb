package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// v8 测试用常量，避免 goconst 重复字符串警告
const (
	v8WriteNoTableBody = `{"table":"nonexistent_v7","rows":[{"id":1}]}`
	v8WriteNoKeyBody   = `{"table":"users","rows":[{"name":"alice"}]}`
)

// ---------------------------------------------------------------------------
// httpQuery: 错误 HTTP 方法、JSON 解码错误、handleQuery 错误、非零响应码
// ---------------------------------------------------------------------------

// httpQuery 错误方法测试用例
var httpQueryBadMethodTests = []struct {
	name       string
	method     string
	wantStatus int
}{
	{"错误HTTP方法_GET", http.MethodGet, http.StatusMethodNotAllowed},
	{"错误HTTP方法_PUT", http.MethodPut, http.StatusMethodNotAllowed},
	{"错误HTTP方法_DELETE", http.MethodDelete, http.StatusMethodNotAllowed},
}

// httpQuery JSON 解码错误测试用例
var httpQueryDecodeErrorTests = []struct {
	name       string
	body       string
	wantStatus int
}{
	{"JSON解码错误_无效JSON", "<<<不是json>>>", http.StatusBadRequest},
	{"JSON解码错误_空请求体", "", http.StatusBadRequest},
	{"JSON解码错误_不完整JSON", "{", http.StatusBadRequest},
}

// httpQuery 非零响应码测试用例
var httpQueryNonZeroCodeTests = []struct {
	name       string
	body       string
	wantStatus int
}{
	{"非零响应码_无效SQL", testInvalidSQLBody, http.StatusBadRequest},
	{"非零响应码_查询不存在的表", `{"sql":"SELECT * FROM nonexistent_v7"}`, http.StatusBadRequest},
}

// TestCoverageLowHandlerV7_HttpQuery_BadMethod 测试 httpQuery 错误 HTTP 方法。
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

// TestCoverageLowHandlerV7_HttpQuery_DecodeError 测试 httpQuery JSON 解码错误。
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

// TestCoverageLowHandlerV7_HttpQuery_NonZeroCode 测试 httpQuery 非零响应码。
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

// TestCoverageLowHandlerV7_HttpQuery_Success 测试 httpQuery 正常查询。
func TestCoverageLowHandlerV7_HttpQuery_Success(t *testing.T) {
	srv := newTestServerV7WithTable(t)
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(benchSelectAllSQL))
	w := httptest.NewRecorder()
	srv.httpQuery(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("状态码 = %d，期望 %d", w.Code, http.StatusOK)
	}
}

// TestCoverageLowHandlerV7_HttpQuery_HandleQueryError 测试 httpQuery 中 handleQuery 返回错误时的行为。
// 注意：当前 handleQuery 实现始终返回 nil error（错误通过 Response.Code 传递），
// 因此 httpQuery 中的 `if err != nil` 分支（返回 HTTP 500）在当前实现中不可达。
// 此测试验证：handleQuery 返回非零 Code 的 Response 时，httpQuery 正确返回 HTTP 400。
func TestCoverageLowHandlerV7_HttpQuery_HandleQueryError(t *testing.T) {
	srv := newTestServerV7(t)

	// 发送无效 SQL，handleQuery 返回 Response{Code: -1}, nil（而非 Go error）
	// httpQuery 应将非零 Code 的 Response 映射为 HTTP 400
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

// ---------------------------------------------------------------------------
// httpWrite: 错误 HTTP 方法、JSON 解码错误、handleWrite 错误、非零响应码
// ---------------------------------------------------------------------------

// httpWrite 错误方法测试用例
var httpWriteBadMethodTests = []struct {
	name       string
	method     string
	wantStatus int
}{
	{"错误HTTP方法_GET", http.MethodGet, http.StatusMethodNotAllowed},
	{"错误HTTP方法_PUT", http.MethodPut, http.StatusMethodNotAllowed},
	{"错误HTTP方法_PATCH", http.MethodPatch, http.StatusMethodNotAllowed},
}

// httpWrite JSON 解码错误测试用例
var httpWriteDecodeErrorTests = []struct {
	name       string
	body       string
	wantStatus int
}{
	{"JSON解码错误_无效JSON", "<<<不是json>>>", http.StatusBadRequest},
	{"JSON解码错误_空请求体", "", http.StatusBadRequest},
	{"JSON解码错误_不完整JSON", "{", http.StatusBadRequest},
}

// httpWrite 非零响应码测试用例
var httpWriteNonZeroCodeTests = []struct {
	name       string
	body       string
	wantStatus int
}{
	{"非零响应码_表不存在_v8", v8WriteNoTableBody, http.StatusBadRequest},
	{"非零响应码_缺少主键_v8", v8WriteNoKeyBody, http.StatusBadRequest},
	{"非零响应码_类型不匹配", `{"table":"users","rows":[{"id":1,"name":true}]}`, http.StatusBadRequest},
}

// TestCoverageLowHandlerV7_HttpWrite_BadMethod 测试 httpWrite 错误 HTTP 方法。
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

// TestCoverageLowHandlerV7_HttpWrite_DecodeError 测试 httpWrite JSON 解码错误。
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

// TestCoverageLowHandlerV7_HttpWrite_NonZeroCode 测试 httpWrite 非零响应码。
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

// TestCoverageLowHandlerV7_HttpWrite_Success 测试 httpWrite 正常写入。
func TestCoverageLowHandlerV7_HttpWrite_Success(t *testing.T) {
	srv := newTestServerV7WithTable(t)
	req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(testWriteAliceBody))
	w := httptest.NewRecorder()
	srv.httpWrite(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("状态码 = %d，期望 %d", w.Code, http.StatusOK)
	}
}

// TestCoverageLowHandlerV7_HttpWrite_HandleWriteError 测试 httpWrite 中 handleWrite 返回错误时的行为。
// 注意：当前 handleWrite 实现始终返回 nil error（错误通过 Response.Code 传递），
// 因此 httpWrite 中的 `if err != nil` 分支（返回 HTTP 500）在当前实现中不可达。
// 此测试验证：handleWrite 返回非零 Code 的 Response 时，httpWrite 正确返回 HTTP 400。
func TestCoverageLowHandlerV7_HttpWrite_HandleWriteError(t *testing.T) {
	srv := newTestServerV7(t)

	// 写入不存在的表，handleWrite 返回 Response{Code: -1}, nil（而非 Go error）
	// httpWrite 应将非零 Code 的 Response 映射为 HTTP 400
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

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

// newTestServerV7WithTable 创建用于 V7 覆盖率测试的服务器，并注册 users 表。
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
