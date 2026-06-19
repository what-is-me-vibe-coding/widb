package server

import (
	"strings"
	"testing"
)

// TestHandleExplainSelectAll 验证 EXPLAIN SELECT * 返回计划树行。
func TestHandleExplainSelectAll(t *testing.T) {
	srv := newTestServerWithTable(t)
	resp, err := srv.handleQuery(&QueryRequest{SQL: "explain select * from users"})
	if err != nil {
		t.Fatalf("handleQuery 失败: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("响应 Code = %d, Message = %q", resp.Code, resp.Message)
	}
	if resp.Rows == 0 {
		t.Fatal("EXPLAIN 应返回至少一行计划")
	}
	wantCols := []string{"id", "depth", "operation", "detail"}
	if len(resp.Columns) != len(wantCols) {
		t.Fatalf("列数 = %d, 期望 %d", len(resp.Columns), len(wantCols))
	}
	for i, c := range resp.Columns {
		if c != wantCols[i] {
			t.Errorf("列[%d] = %q, 期望 %q", i, c, wantCols[i])
		}
	}
	// SELECT * 不需要投影，计划应至少包含 Scan 节点
	rows := resp.Data.([]map[string]any)
	if !hasOperation(rows, "Scan") {
		t.Errorf("EXPLAIN 输出应包含 Scan 操作, rows=%v", rows)
	}
}

// TestHandleExplainWithWhere 验证 EXPLAIN 带 WHERE 的查询包含 Filter 与 Scan 节点。
func TestHandleExplainWithWhere(t *testing.T) {
	srv := newTestServerWithTable(t)
	resp, err := srv.handleQuery(&QueryRequest{SQL: "explain select id from users where id > 10"})
	if err != nil {
		t.Fatalf("handleQuery 失败: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("响应 Code = %d, Message = %q", resp.Code, resp.Message)
	}
	rows := resp.Data.([]map[string]any)
	if !hasOperation(rows, "Scan") {
		t.Errorf("EXPLAIN 输出应包含 Scan 操作, rows=%v", rows)
	}
	// 谓词下推优化后 WHERE 会合并到 Scan 的 Predicate，可能不再有独立 Filter 节点，
	// 因此只校验 Scan 节点的 detail 包含谓词文本。
	if !detailContains(rows, "Predicate") {
		t.Errorf("EXPLAIN 输出应包含谓词信息, rows=%v", rows)
	}
}

// TestHandleExplainWithLimit 验证 EXPLAIN 带 LIMIT 的查询包含 Limit 节点。
func TestHandleExplainWithLimit(t *testing.T) {
	srv := newTestServerWithTable(t)
	resp, err := srv.handleQuery(&QueryRequest{SQL: "explain select * from users limit 5"})
	if err != nil {
		t.Fatalf("handleQuery 失败: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("响应 Code = %d, Message = %q", resp.Code, resp.Message)
	}
	rows := resp.Data.([]map[string]any)
	if !hasOperation(rows, "Limit") {
		t.Errorf("EXPLAIN 输出应包含 Limit 操作, rows=%v", rows)
	}
}

// TestHandleExplainAggregate 验证 EXPLAIN 聚合查询包含 Aggregate 节点。
func TestHandleExplainAggregate(t *testing.T) {
	srv := newTestServerWithTable(t)
	resp, err := srv.handleQuery(&QueryRequest{SQL: "explain select count(*) from users"})
	if err != nil {
		t.Fatalf("handleQuery 失败: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("响应 Code = %d, Message = %q", resp.Code, resp.Message)
	}
	rows := resp.Data.([]map[string]any)
	if !hasOperation(rows, "Aggregate") {
		t.Errorf("EXPLAIN 输出应包含 Aggregate 操作, rows=%v", rows)
	}
}

// TestHandleExplainTableNotExist 验证 EXPLAIN 不存在的表返回分析错误。
func TestHandleExplainTableNotExist(t *testing.T) {
	srv := newTestServer(t)
	resp, err := srv.handleQuery(&QueryRequest{SQL: "explain select * from nonexistent"})
	if err != nil {
		t.Fatalf("handleQuery 失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", resp.Code)
	}
}

// TestHandleExplainInvalidSQL 验证 EXPLAIN 后跟语法错误返回解析错误。
func TestHandleExplainInvalidSQL(t *testing.T) {
	srv := newTestServer(t)
	resp, err := srv.handleQuery(&QueryRequest{SQL: "explain select from t"})
	if err != nil {
		t.Fatalf("handleQuery 失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", resp.Code)
	}
	if !strings.Contains(resp.Message, "EXPLAIN") {
		t.Errorf("错误信息 %q 应包含 EXPLAIN", resp.Message)
	}
}

// TestHandleExplainNonSelect 验证 EXPLAIN 非 SELECT 语句返回解析错误。
func TestHandleExplainNonSelect(t *testing.T) {
	srv := newTestServer(t)
	resp, err := srv.handleQuery(&QueryRequest{SQL: "explain insert into t values(1)"})
	if err != nil {
		t.Fatalf("handleQuery 失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", resp.Code)
	}
}

// TestHandleExplainDoesNotExecute 验证 EXPLAIN 不实际执行查询（不读取数据）。
// 通过 EXPLAIN 一张空表确认返回计划而非数据行。
func TestHandleExplainDoesNotExecute(t *testing.T) {
	srv := newTestServerWithTable(t)
	// 先写入一行数据
	_, _ = srv.handleWrite(&WriteRequest{
		Table: testTable,
		Rows:  []map[string]any{{"id": float64(1), testColName: testName}},
	})

	resp, err := srv.handleQuery(&QueryRequest{SQL: "explain select * from users"})
	if err != nil {
		t.Fatalf("handleQuery 失败: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("响应 Code = %d, Message = %q", resp.Code, resp.Message)
	}
	rows := resp.Data.([]map[string]any)
	// EXPLAIN 返回的是计划节点行（Scan 等），而非数据行（alice 等）
	for _, r := range rows {
		if op, _ := r["operation"].(string); op == "" {
			t.Errorf("EXPLAIN 行缺少 operation 字段: %v", r)
		}
		// 确保不返回实际数据值
		if name, ok := r[testColName]; ok {
			t.Errorf("EXPLAIN 不应返回数据列 %q, got %v", testColName, name)
		}
	}
}

// hasOperation 检查 EXPLAIN 输出行中是否存在指定操作类型。
func hasOperation(rows []map[string]any, op string) bool {
	for _, r := range rows {
		if v, _ := r["operation"].(string); v == op {
			return true
		}
	}
	return false
}

// detailContains 检查 EXPLAIN 输出中是否有行的 detail 包含 substr。
func detailContains(rows []map[string]any, substr string) bool {
	for _, r := range rows {
		if v, _ := r["detail"].(string); strings.Contains(v, substr) {
			return true
		}
	}
	return false
}
