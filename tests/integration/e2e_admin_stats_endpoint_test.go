// Package integration 端到端集成测试：HTTP 运维端点 GET /admin/stats。
//
// 本文件针对 PR #250 新增的 /admin/stats 端点补齐端到端覆盖：
//   - 通过真实 HTTP 客户端（net/http）访问 GET /admin/stats，验证路由/响应/Content-Type
//   - 验证响应 JSON 的顶层结构（code/message/summary/tables）
//   - 验证 summary 字段（total_tables/lsm_tables/memory_tables/total_rows）
//   - 验证 tables 数组按表名字典序排序
//   - 验证 LSM 表的运行时字段（segment_count/l0_segment_count/memtable_size 等）
//     与 memory 引擎表的字段裁剪（omitempty 不输出 LSM 专属字段）
//   - 验证多次写入 + flush 后 SegmentCount、RowCount 等统计量正确变化
//   - 验证非 GET 方法（POST/PUT/DELETE）返回 405
//   - 验证空库响应（total_tables=0、tables=[]）
//
// 与 pkg/server/admin_stats_test.go 单测的区别：单测使用 httptest.NewRecorder
// 直接调用 handler 闭包或 Server.collectStats；本文件通过 startSQLServer 启动的
// 真实 HTTP 监听，验证真实 TCP→HTTP→Server→routingAdapter→storage.Engine
// 的完整调用链，并检查与 catalog / SQL 写入路径的协同。
package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
)

// tableStatsItemResp 与 pkg/server/admin_stats.go 中 tableStatsItem 字段一致。
// 独立定义（不直接 import server 内部类型）以保持集成测试对 server 内部实现的弱耦合。
type tableStatsItemResp struct {
	Name           string   `json:"name"`
	Engine         string   `json:"engine"`
	Columns        int      `json:"columns"`
	PrimaryKey     []string `json:"primary_key,omitempty"`
	RowCount       int64    `json:"row_count"`
	SegmentCount   int      `json:"segment_count,omitempty"`
	L0SegmentCount int      `json:"l0_segment_count,omitempty"`
	ImmutableCount int      `json:"immutable_count,omitempty"`
	MemTableSize   int64    `json:"memtable_size,omitempty"`
	ActiveRowCount int64    `json:"active_row_count,omitempty"`
	ImmRowCount    int64    `json:"immutable_row_count,omitempty"`
}

// statsSummaryResp 与 pkg/server/admin_stats.go 中 statsSummary 字段一致。
type statsSummaryResp struct {
	TotalTables   int   `json:"total_tables"`
	LSMTables     int   `json:"lsm_tables"`
	MemoryTables  int   `json:"memory_tables"`
	TotalSegments int   `json:"total_segments"`
	TotalRows     int64 `json:"total_rows"`
}

// statsResponseResp 是 /admin/stats 的统一响应结构。
type statsResponseResp struct {
	Code    int                  `json:"code"`
	Message string               `json:"message,omitempty"`
	Summary statsSummaryResp     `json:"summary"`
	Tables  []tableStatsItemResp `json:"tables"`
}

// getAdminStats 通过真实 HTTP 客户端访问 GET /admin/stats。
func getAdminStats(t *testing.T, s *sqlServer) (int, statsResponseResp, []byte, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+s.httpAddr+"/admin/stats", nil)
	if err != nil {
		return 0, statsResponseResp{}, nil, fmt.Errorf("构造请求失败: %w", err)
	}
	resp, err := sqlHTTPClient.Do(req)
	if err != nil {
		return 0, statsResponseResp{}, nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, statsResponseResp{}, body, fmt.Errorf("读取响应失败: %w", err)
	}
	var out statsResponseResp
	if len(body) > 0 {
		if err := json.Unmarshal(body, &out); err != nil {
			return resp.StatusCode, statsResponseResp{}, body, fmt.Errorf("解码响应失败: %w (body=%q)", err, string(body))
		}
	}
	return resp.StatusCode, out, body, nil
}

// callAdminStatsMethod 用任意 method 访问 /admin/stats，用于验证 405。
func callAdminStatsMethod(t *testing.T, s *sqlServer, method string) (int, []byte, error) {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+s.httpAddr+"/admin/stats", nil)
	if err != nil {
		return 0, nil, fmt.Errorf("构造请求失败: %w", err)
	}
	resp, err := sqlHTTPClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, body, fmt.Errorf("读取响应失败: %w", err)
	}
	return resp.StatusCode, body, nil
}

// findTable 在响应中按表名查找统计项，缺失时 t.Fatalf。
func findTable(t *testing.T, resp statsResponseResp, name string) tableStatsItemResp {
	t.Helper()
	for _, item := range resp.Tables {
		if item.Name == name {
			return item
		}
	}
	names := make([]string, 0, len(resp.Tables))
	for _, item := range resp.Tables {
		names = append(names, item.Name)
	}
	sort.Strings(names)
	t.Fatalf("未找到表 %q 的统计项; 响应包含: %v", name, names)
	return tableStatsItemResp{}
}

// findRawTable 返回 raw JSON 数组中指定表名的原始 map（用于校验 omitempty）。
func findRawTable(t *testing.T, raw []byte, name string) map[string]any {
	t.Helper()
	var doc struct {
		Tables []map[string]any `json:"tables"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("解析 raw tables 失败: %v", err)
	}
	for _, item := range doc.Tables {
		if n, _ := item["name"].(string); n == name {
			return item
		}
	}
	t.Fatalf("raw 中未找到表 %q; 现有: %+v", name, doc.Tables)
	return nil
}

// TestAdminStatsEmptyDatabase 验证空库（无任何表）下 GET /admin/stats 的响应。
// 期望：HTTP 200、Code=0、TotalTables=0、Tables=[]、TotalRows=0、LSM/MemoryTables=0。
func TestAdminStatsEmptyDatabase(t *testing.T) {
	t.Parallel()
	s := startSQLServer(t)

	status, body, raw, err := getAdminStats(t, s)
	if err != nil {
		t.Fatalf("GET /admin/stats 失败: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200; body = %s", status, string(raw))
	}
	if body.Code != 0 {
		t.Errorf("Code = %d, 期望 0; Message = %q", body.Code, body.Message)
	}
	if body.Summary.TotalTables != 0 {
		t.Errorf("Summary.TotalTables = %d, 期望 0", body.Summary.TotalTables)
	}
	if body.Summary.LSMTables != 0 || body.Summary.MemoryTables != 0 {
		t.Errorf("Summary.LSMTables/MemoryTables = %d/%d, 期望 0/0", body.Summary.LSMTables, body.Summary.MemoryTables)
	}
	if body.Summary.TotalRows != 0 {
		t.Errorf("Summary.TotalRows = %d, 期望 0", body.Summary.TotalRows)
	}
	if len(body.Tables) != 0 {
		t.Errorf("Tables 长度 = %d, 期望 0; 内容 = %+v", len(body.Tables), body.Tables)
	}
}

// TestAdminStatsSingleLSMTable 验证单张 LSM 表的统计：
//   - summary.total_tables=1, lsm_tables=1, memory_tables=0
//   - 表项字段：engine=lsm, columns=4, primary_key=[id], row_count=5
//   - 运行时段段字段在写入后存在（memtable_size>0 或 active_row_count>0）
func TestAdminStatsSingleLSMTable(t *testing.T) {
	t.Parallel()
	s := startSQLServer(t)
	adminCreateSensorTable(t, s)
	writeVia(t, s, "tcp", sensorTable, sensorRows())

	status, body, raw, err := getAdminStats(t, s)
	if err != nil {
		t.Fatalf("GET /admin/stats 失败: %v", err)
	}
	if status != http.StatusOK || body.Code != 0 {
		t.Fatalf("状态码=%d Code=%d Message=%q raw=%s", status, body.Code, body.Message, string(raw))
	}

	if body.Summary.TotalTables != 1 {
		t.Errorf("Summary.TotalTables = %d, 期望 1", body.Summary.TotalTables)
	}
	if body.Summary.LSMTables != 1 {
		t.Errorf("Summary.LSMTables = %d, 期望 1", body.Summary.LSMTables)
	}
	if body.Summary.MemoryTables != 0 {
		t.Errorf("Summary.MemoryTables = %d, 期望 0", body.Summary.MemoryTables)
	}
	if body.Summary.TotalRows != 5 {
		t.Errorf("Summary.TotalRows = %d, 期望 5", body.Summary.TotalRows)
	}

	item := findTable(t, body, sensorTable)
	if item.Engine != "lsm" {
		t.Errorf("表 %s engine = %q, 期望 lsm", sensorTable, item.Engine)
	}
	if item.Columns != 4 {
		t.Errorf("表 %s columns = %d, 期望 4", sensorTable, item.Columns)
	}
	if len(item.PrimaryKey) != 1 || item.PrimaryKey[0] != "id" {
		t.Errorf("表 %s primary_key = %v, 期望 [id]", sensorTable, item.PrimaryKey)
	}
	if item.RowCount != 5 {
		t.Errorf("表 %s row_count = %d, 期望 5", sensorTable, item.RowCount)
	}
	// 活跃 MemTable 中应包含 5 行
	if item.ActiveRowCount != 5 {
		t.Errorf("表 %s active_row_count = %d, 期望 5 (刚写入尚未 flush)", sensorTable, item.ActiveRowCount)
	}
	// MemTableSize > 0 是软断言（不同编码路径字节数不同）
	if item.MemTableSize <= 0 {
		t.Errorf("表 %s memtable_size = %d, 期望 > 0", sensorTable, item.MemTableSize)
	}
}

// TestAdminStatsMixedEngines 验证 LSM 表与 memory 引擎表共存时的统计：
//   - summary 正确汇总：lsm_tables=1, memory_tables=1, total_rows=6
//   - tables 数组按表名字典序排序（mem_cache < sensor）
//   - LSM 表包含 segment_count/l0_segment_count/memtable_size 等字段
//   - memory 表不包含 LSM 专属字段（omitempty 生效）
func TestAdminStatsMixedEngines(t *testing.T) {
	t.Parallel()
	s := startSQLServer(t)
	adminCreateSensorTable(t, s)
	adminCreateMemoryTable(t, s, "mem_cache")
	writeVia(t, s, "tcp", sensorTable, sensorRows()[:3])
	resp := queryVia(t, s, "tcp", "INSERT INTO mem_cache VALUES (1, 'a'), (2, 'b'), (3, 'c')")
	if resp.Code != 0 {
		t.Fatalf("写入内存表失败: %s", resp.Message)
	}

	status, body, raw, err := getAdminStats(t, s)
	if err != nil {
		t.Fatalf("GET /admin/stats 失败: %v", err)
	}
	if status != http.StatusOK || body.Code != 0 {
		t.Fatalf("状态码=%d Code=%d Message=%q raw=%s", status, body.Code, body.Message, string(raw))
	}

	assertMixedEnginesSummary(t, body)
	if !assertMixedEnginesSorted(t, body) {
		return
	}
	assertMemoryTableFields(t, body.Tables[0], raw)
	assertLSMTableFields(t, body.Tables[1])
}

// assertMixedEnginesSummary 校验混合引擎场景下 summary 字段。
func assertMixedEnginesSummary(t *testing.T, body statsResponseResp) {
	t.Helper()
	if body.Summary.TotalTables != 2 {
		t.Errorf("Summary.TotalTables = %d, 期望 2", body.Summary.TotalTables)
	}
	if body.Summary.LSMTables != 1 {
		t.Errorf("Summary.LSMTables = %d, 期望 1", body.Summary.LSMTables)
	}
	if body.Summary.MemoryTables != 1 {
		t.Errorf("Summary.MemoryTables = %d, 期望 1", body.Summary.MemoryTables)
	}
	if body.Summary.TotalRows != 6 {
		t.Errorf("Summary.TotalRows = %d, 期望 6 (3 LSM + 3 memory)", body.Summary.TotalRows)
	}
}

// assertMixedEnginesSorted 校验 tables 数组按表名字典序排序。失败时返回 false。
func assertMixedEnginesSorted(t *testing.T, body statsResponseResp) bool {
	t.Helper()
	if len(body.Tables) != 2 {
		t.Fatalf("Tables 长度 = %d, 期望 2; 内容 = %+v", len(body.Tables), body.Tables)
		return false
	}
	if body.Tables[0].Name != "mem_cache" {
		t.Errorf("Tables[0] = %q, 期望 mem_cache（按字典序）", body.Tables[0].Name)
	}
	if body.Tables[1].Name != sensorTable {
		t.Errorf("Tables[1] = %q, 期望 %s（按字典序）", body.Tables[1].Name, sensorTable)
	}
	return true
}

// assertMemoryTableFields 校验 memory 引擎表的元数据 + omitempty LSM 字段不输出。
func assertMemoryTableFields(t *testing.T, item tableStatsItemResp, raw []byte) {
	t.Helper()
	if item.Engine != "memory" {
		t.Errorf("mem_cache engine = %q, 期望 memory", item.Engine)
	}
	if item.RowCount != 3 {
		t.Errorf("mem_cache row_count = %d, 期望 3", item.RowCount)
	}
	// LSM 专属字段为 omitempty，检查 raw JSON 中确实没有这些字段
	rawMem := findRawTable(t, raw, "mem_cache")
	for _, lsmOnly := range []string{
		"segment_count", "l0_segment_count", "immutable_count",
		"memtable_size", "active_row_count", "immutable_row_count",
	} {
		if _, present := rawMem[lsmOnly]; present {
			t.Errorf("memory 表不应输出字段 %q (omitempty 失效); raw=%v", lsmOnly, rawMem)
		}
	}
}

// assertLSMTableFields 校验 LSM 表的元数据 + 运行时字段。
func assertLSMTableFields(t *testing.T, item tableStatsItemResp) {
	t.Helper()
	if item.Engine != "lsm" {
		t.Errorf("%s engine = %q, 期望 lsm", sensorTable, item.Engine)
	}
	if item.RowCount != 3 {
		t.Errorf("%s row_count = %d, 期望 3", sensorTable, item.RowCount)
	}
	if item.ActiveRowCount != 3 {
		t.Errorf("%s active_row_count = %d, 期望 3", sensorTable, item.ActiveRowCount)
	}
}

// TestAdminStatsFlushAffectsSegmentCount 验证强制 flush 后 segment_count 上升。
// 写 5 行 → flush → segment_count ≥ 1。
func TestAdminStatsFlushAffectsSegmentCount(t *testing.T) {
	t.Parallel()
	s := startSQLServer(t)
	adminCreateSensorTable(t, s)
	writeVia(t, s, "tcp", sensorTable, sensorRows())

	// 1) flush 前：segment_count 应为 0
	_, before, _, err := getAdminStats(t, s)
	if err != nil {
		t.Fatalf("flush 前 GET /admin/stats 失败: %v", err)
	}
	beforeItem := findTable(t, before, sensorTable)
	if beforeItem.SegmentCount != 0 {
		t.Errorf("flush 前 segment_count = %d, 期望 0", beforeItem.SegmentCount)
	}

	// 2) 强制 flush
	if status, body, err := postAdmin(t, s, "/admin/flush"); err != nil || status != http.StatusOK || body.Code != 0 {
		t.Fatalf("/admin/flush 失败: status=%d body=%+v err=%v", status, body, err)
	}

	// 3) flush 后：segment_count ≥ 1
	_, after, _, err := getAdminStats(t, s)
	if err != nil {
		t.Fatalf("flush 后 GET /admin/stats 失败: %v", err)
	}
	afterItem := findTable(t, after, sensorTable)
	if afterItem.SegmentCount < 1 {
		t.Errorf("flush 后 segment_count = %d, 期望 >= 1", afterItem.SegmentCount)
	}
	if afterItem.SegmentCount != beforeItem.SegmentCount+1 {
		t.Errorf("flush 后 segment_count = %d, 期望 = 之前(%d)+1", afterItem.SegmentCount, beforeItem.SegmentCount)
	}
	// row_count 不受 flush 影响
	if afterItem.RowCount != beforeItem.RowCount {
		t.Errorf("flush 后 row_count 变化: 之前=%d 之后=%d, 期望不变", beforeItem.RowCount, afterItem.RowCount)
	}
}

// TestAdminStatsRejectsNonGET 验证 /admin/stats 对非 GET 方法返回 405。
func TestAdminStatsRejectsNonGET(t *testing.T) {
	t.Parallel()
	s := startSQLServer(t)
	adminCreateSensorTable(t, s)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		status, body, err := callAdminStatsMethod(t, s, method)
		if err != nil {
			t.Fatalf("%s /admin/stats 请求失败: %v", method, err)
		}
		if status != http.StatusMethodNotAllowed {
			t.Errorf("%s /admin/stats 状态码 = %d, 期望 405; body = %s", method, status, string(body))
		}
	}
}

// TestAdminStatsContentType 验证 /admin/stats 响应 Content-Type 为 application/json。
// 运维脚本（curl、监控面板）通常依据 Content-Type 判断响应格式。
func TestAdminStatsContentType(t *testing.T) {
	t.Parallel()
	s := startSQLServer(t)
	adminCreateSensorTable(t, s)

	req, err := http.NewRequest(http.MethodGet, "http://"+s.httpAddr+"/admin/stats", nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	resp, err := sqlHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/stats 失败: %v", err)
	}
	_ = resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, 期望 application/json 前缀", ct)
	}
}

// TestAdminStatsResponseStableShape 验证响应 JSON 顶层字段名稳定：
// 监控/巡检脚本可能依赖字段名解析，字段重命名会破坏这些脚本。
func TestAdminStatsResponseStableShape(t *testing.T) {
	t.Parallel()
	s := startSQLServer(t)
	adminCreateSensorTable(t, s)
	writeVia(t, s, "tcp", sensorTable, sensorRows())

	_, _, raw, err := getAdminStats(t, s)
	if err != nil {
		t.Fatalf("GET /admin/stats 失败: %v", err)
	}

	// 顶层字段集合（仅校验存在性，不校验顺序）
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("解析顶层 JSON 失败: %v", err)
	}
	for _, key := range []string{"code", "message", "summary", "tables"} {
		if _, ok := top[key]; !ok {
			t.Errorf("响应缺少顶层字段 %q; raw=%s", key, string(raw))
		}
	}

	// summary 子字段
	var summary map[string]json.RawMessage
	if err := json.Unmarshal(top["summary"], &summary); err != nil {
		t.Fatalf("解析 summary 失败: %v", err)
	}
	for _, key := range []string{"total_tables", "lsm_tables", "memory_tables", "total_segments", "total_rows"} {
		if _, ok := summary[key]; !ok {
			t.Errorf("响应 summary 缺少字段 %q", key)
		}
	}

	// tables[0] 子字段
	var tables []map[string]json.RawMessage
	if err := json.Unmarshal(top["tables"], &tables); err != nil {
		t.Fatalf("解析 tables 失败: %v", err)
	}
	if len(tables) == 0 {
		t.Fatalf("tables 为空")
	}
	for _, key := range []string{"name", "engine", "columns", "primary_key", "row_count"} {
		if _, ok := tables[0][key]; !ok {
			t.Errorf("响应 tables[*] 缺少字段 %q", key)
		}
	}
}
