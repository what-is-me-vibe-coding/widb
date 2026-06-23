package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
)

// statsTestRequest 触发 /admin/stats 并返回解码后的响应体。
// 与 adminRequest 区别在于返回值类型是 statsResponse，便于断言表格内容。
func statsTestRequest(t *testing.T, srv *Server, method, path string) (int, *statsResponse) {
	t.Helper()
	mux := srv.registerHTTPHandlers()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()
	var body statsResponse
	if rec.Body.Len() > 0 {
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatalf("解码响应失败: %v", err)
		}
	}
	return res.StatusCode, &body
}

// findTableItem 在 stats 响应中按表名查找项，缺则返回 nil。
func findTableItem(items []tableStatsItem, name string) *tableStatsItem {
	for i := range items {
		if items[i].Name == name {
			return &items[i]
		}
	}
	return nil
}

// newAdminStatsTestServer 构造同时含 LSM 表与 memory 表的 server。
// 覆盖：单个 LSM 表 + 写入若干行；单个 memory 表 + 写入若干行。
func newAdminStatsTestServer(t *testing.T) *Server {
	t.Helper()
	srv := newTestServer(t)
	// 建一张 LSM 表并写入 2 行
	if resp, err := srv.handleQuery(&QueryRequest{
		SQL: "create table lsm_t (id int64 not null, v string, primary key(id))",
	}); err != nil || resp.Code != 0 {
		t.Fatalf("创建 lsm_t 失败: err=%v resp=%+v", err, resp)
	}
	for i := int64(1); i <= 2; i++ {
		if _, err := srv.handleWrite(&WriteRequest{
			Table: "lsm_t",
			Rows:  []map[string]any{{"id": i, "v": "x"}},
		}); err != nil {
			t.Fatalf("写 lsm_t 失败: %v", err)
		}
	}
	// 建一张 memory 表并写入 3 行
	if resp, err := srv.handleQuery(&QueryRequest{
		SQL: "create table mem_t (id int64 not null, v string, primary key(id)) engine=memory",
	}); err != nil || resp.Code != 0 {
		t.Fatalf("创建 mem_t 失败: err=%v resp=%+v", err, resp)
	}
	for i := int64(1); i <= 3; i++ {
		if _, err := srv.handleWrite(&WriteRequest{
			Table: "mem_t",
			Rows:  []map[string]any{{"id": i, "v": "y"}},
		}); err != nil {
			t.Fatalf("写 mem_t 失败: %v", err)
		}
	}
	return srv
}

// TestAdminStatsSuccess 验证 GET /admin/stats 正常返回两表的统计。
// 为规避 gocyclo/gocognit 阈值，拆分为「基础响应校验」+「LSM 表字段」
// +「memory 表字段」三个用例。
func TestAdminStatsSuccess(t *testing.T) {
	srv := newAdminStatsTestServer(t)
	status, body := statsTestRequest(t, srv, http.MethodGet, "/admin/stats")
	if status != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200; body = %+v", status, body)
	}
	if body.Code != 0 {
		t.Errorf("响应 Code = %d, 期望 0; Message = %q", body.Code, body.Message)
	}
	if body.Message != adminMsgStatsOK {
		t.Errorf("响应 Message = %q, 期望 %q", body.Message, adminMsgStatsOK)
	}
	if body.Summary.TotalTables != 2 {
		t.Errorf("TotalTables = %d, 期望 2", body.Summary.TotalTables)
	}
	if body.Summary.LSMTables != 1 {
		t.Errorf("LSMTables = %d, 期望 1", body.Summary.LSMTables)
	}
	if body.Summary.MemoryTables != 1 {
		t.Errorf("MemoryTables = %d, 期望 1", body.Summary.MemoryTables)
	}
	if body.Summary.TotalRows != 5 {
		t.Errorf("TotalRows = %d, 期望 5 (2 lsm + 3 mem)", body.Summary.TotalRows)
	}
	if findTableItem(body.Tables, "lsm_t") == nil {
		t.Errorf("响应 tables 缺少 lsm_t: %+v", body.Tables)
	}
	if findTableItem(body.Tables, "mem_t") == nil {
		t.Errorf("响应 tables 缺少 mem_t: %+v", body.Tables)
	}
}

// TestAdminStatsLSMTableFields 验证 lsm_t 的元数据 + 运行时统计。
func TestAdminStatsLSMTableFields(t *testing.T) {
	srv := newAdminStatsTestServer(t)
	_, body := statsTestRequest(t, srv, http.MethodGet, "/admin/stats")
	lsm := findTableItem(body.Tables, "lsm_t")
	if lsm == nil {
		t.Fatalf("响应 tables 缺少 lsm_t: %+v", body.Tables)
	}
	if lsm.Engine != "lsm" {
		t.Errorf("lsm_t.Engine = %q, 期望 \"lsm\"", lsm.Engine)
	}
	if lsm.Columns != 2 {
		t.Errorf("lsm_t.Columns = %d, 期望 2", lsm.Columns)
	}
	if len(lsm.PrimaryKey) != 1 || lsm.PrimaryKey[0] != "id" {
		t.Errorf("lsm_t.PrimaryKey = %v, 期望 [id]", lsm.PrimaryKey)
	}
	if lsm.RowCount != 2 {
		t.Errorf("lsm_t.RowCount = %d, 期望 2", lsm.RowCount)
	}
	// 数据还在 MemTable 中，未触发 flush，因此 SegmentCount/L0SegmentCount 为 0。
	if lsm.SegmentCount != 0 {
		t.Errorf("未 flush 时 lsm_t.SegmentCount = %d, 期望 0", lsm.SegmentCount)
	}
	if lsm.L0SegmentCount != 0 {
		t.Errorf("未 flush 时 lsm_t.L0SegmentCount = %d, 期望 0", lsm.L0SegmentCount)
	}
	if lsm.MemTableSize <= 0 {
		t.Errorf("lsm_t.MemTableSize = %d, 期望 > 0（数据仍在 MemTable）", lsm.MemTableSize)
	}
	if lsm.ActiveRowCount != 2 {
		t.Errorf("lsm_t.ActiveRowCount = %d, 期望 2", lsm.ActiveRowCount)
	}
}

// TestAdminStatsMemoryTableFields 验证 mem_t 的元数据 + 运行时统计。
func TestAdminStatsMemoryTableFields(t *testing.T) {
	srv := newAdminStatsTestServer(t)
	_, body := statsTestRequest(t, srv, http.MethodGet, "/admin/stats")
	mem := findTableItem(body.Tables, "mem_t")
	if mem == nil {
		t.Fatalf("响应 tables 缺少 mem_t: %+v", body.Tables)
	}
	if mem.Engine != "memory" {
		t.Errorf("mem_t.Engine = %q, 期望 \"memory\"", mem.Engine)
	}
	if mem.Columns != 2 {
		t.Errorf("mem_t.Columns = %d, 期望 2", mem.Columns)
	}
	if mem.RowCount != 3 {
		t.Errorf("mem_t.RowCount = %d, 期望 3", mem.RowCount)
	}
	// 内存引擎不输出 LSM 专属字段
	if mem.SegmentCount != 0 || mem.L0SegmentCount != 0 || mem.MemTableSize != 0 {
		t.Errorf("mem_t 不应输出 LSM 字段: %+v", mem)
	}
}

// TestAdminStatsEmptyServer 验证空 server（无表）也能成功响应。
func TestAdminStatsEmptyServer(t *testing.T) {
	srv := newTestServer(t)
	status, body := statsTestRequest(t, srv, http.MethodGet, "/admin/stats")
	if status != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200; body = %+v", status, body)
	}
	if body.Summary.TotalTables != 0 {
		t.Errorf("TotalTables = %d, 期望 0", body.Summary.TotalTables)
	}
	if body.Summary.LSMTables != 0 || body.Summary.MemoryTables != 0 {
		t.Errorf("子计数应全为 0: %+v", body.Summary)
	}
	if body.Tables == nil {
		t.Errorf("Tables 应为非 nil 数组（即便为空）")
	}
	if len(body.Tables) != 0 {
		t.Errorf("Tables 长度 = %d, 期望 0", len(body.Tables))
	}
}

// TestAdminStatsRejectsNonGET 验证非 GET 方法被拒。
func TestAdminStatsRejectsNonGET(t *testing.T) {
	srv := newTestServer(t)
	status, body := statsTestRequest(t, srv, http.MethodPost, "/admin/stats")
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("状态码 = %d, 期望 405; body = %+v", status, body)
	}
	if body.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", body.Code)
	}
}

// TestAdminStatsAfterFlush 验证强制 flush 后 RowCount / SegmentCount 同步。
func TestAdminStatsAfterFlush(t *testing.T) {
	srv := newAdminStatsTestServer(t)
	// 触发 flush 把 lsm_t 的 MemTable 落盘
	if status, body := adminRequest(t, srv, http.MethodPost, "/admin/flush"); status != http.StatusOK {
		t.Fatalf("flush 失败: %v", body)
	}
	// 再 stats：lsm_t 的 SegmentCount 应 >= 1 且 RowCount 仍为 2
	status, body := statsTestRequest(t, srv, http.MethodGet, "/admin/stats")
	if status != http.StatusOK {
		t.Fatalf("stats 失败: %d %+v", status, body)
	}
	lsm := findTableItem(body.Tables, "lsm_t")
	if lsm == nil {
		t.Fatalf("缺少 lsm_t: %+v", body.Tables)
	}
	if lsm.RowCount != 2 {
		t.Errorf("flush 后 RowCount = %d, 期望 2", lsm.RowCount)
	}
	if lsm.SegmentCount < 1 {
		t.Errorf("flush 后 SegmentCount = %d, 期望 >= 1", lsm.SegmentCount)
	}
	if body.Summary.TotalSegments < 1 {
		t.Errorf("TotalSegments = %d, 期望 >= 1", body.Summary.TotalSegments)
	}
}

// TestNormalizeEngineName 验证大小写归一化与空值兜底。
func TestNormalizeEngineName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "lsm"},
		{"lsm", "lsm"},
		{"LSM", "lsm"},
		{"Lsm", "lsm"},
		{"memory", "memory"},
		{"Memory", "memory"},
		{"MEMORY", "memory"},
		{"custom", "custom"}, // 未知值原样保留
	}
	for _, c := range cases {
		got := normalizeEngineName(&catalog.Table{Engine: c.in})
		if got != c.want {
			t.Errorf("normalizeEngineName(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}
