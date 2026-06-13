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
