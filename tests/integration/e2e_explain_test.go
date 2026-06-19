// Package integration 端到端集成测试：EXPLAIN 语法经真实 server 的端到端正确性。
//
// 覆盖 issue #193：EXPLAIN SELECT 此前报 "unsupported statement type *sqlparser.OtherRead"，
// 本测试验证经 TCP/HTTP 真实链路执行 EXPLAIN 返回查询计划树而非实际数据，
// 并校验计划节点结构（Scan/Filter/Aggregate/Limit）与列定义。
package integration

import (
	"strings"
	"testing"
)

// TestServerExplainSelect 验证 EXPLAIN SELECT * 经 TCP 与 HTTP 返回计划树。
func TestServerExplainSelect(t *testing.T) {
	for _, via := range []string{"tcp", "http"} {
		t.Run(via, func(t *testing.T) {
			s := startSQLServer(t)
			seedSensorData(t, s, via)

			resp := queryVia(t, s, via, "explain select * from sensor")
			if resp.Code != 0 {
				t.Fatalf("EXPLAIN 失败 [%s]: %s", via, resp.Message)
			}
			if resp.Rows == 0 {
				t.Fatalf("EXPLAIN 应返回至少一行计划 [%s]", via)
			}
			assertExplainColumns(t, via, resp.Columns)
			rows := respRows(resp)
			if !rowsContainOp(rows, "Scan") {
				t.Errorf("[%s] EXPLAIN 输出应包含 Scan 操作: %v", via, rows)
			}
			assertNoDataLeak(t, via, rows)
		})
	}
}

// assertExplainColumns 校验 EXPLAIN 输出列名固定为 id/depth/operation/detail。
func assertExplainColumns(t *testing.T, via string, cols []string) {
	t.Helper()
	wantCols := []string{"id", "depth", "operation", "detail"}
	if len(cols) != len(wantCols) {
		t.Fatalf("[%s] 列数 = %d, 期望 %d", via, len(cols), len(wantCols))
	}
	for i, c := range cols {
		if c != wantCols[i] {
			t.Errorf("[%s] 列[%d] = %q, 期望 %q", via, i, c, wantCols[i])
		}
	}
}

// assertNoDataLeak 校验 EXPLAIN 输出不包含实际数据列（如 name）。
func assertNoDataLeak(t *testing.T, via string, rows []map[string]any) {
	t.Helper()
	for _, r := range rows {
		if _, ok := r["name"]; ok {
			t.Errorf("[%s] EXPLAIN 不应返回数据列 name: %v", via, r)
		}
	}
}

// TestServerExplainWhere 验证 EXPLAIN 带 WHERE 的计划包含谓词信息。
func TestServerExplainWhere(t *testing.T) {
	s := startSQLServer(t)
	seedSensorData(t, s, "tcp")

	resp := queryVia(t, s, "tcp", "explain select id from sensor where temperature > 25")
	if resp.Code != 0 {
		t.Fatalf("EXPLAIN 失败: %s", resp.Message)
	}
	rows := respRows(resp)
	if !rowsContainOp(rows, "Scan") {
		t.Errorf("EXPLAIN 输出应包含 Scan 操作: %v", rows)
	}
	// 谓词下推后 WHERE 合并到 Scan.Predicate
	if !rowsContainDetail(rows, "Predicate") {
		t.Errorf("EXPLAIN 输出应包含谓词信息: %v", rows)
	}
}

// TestServerExplainLimit 验证 EXPLAIN 带 LIMIT 的计划包含 Limit 节点。
func TestServerExplainLimit(t *testing.T) {
	s := startSQLServer(t)
	seedSensorData(t, s, "tcp")

	resp := queryVia(t, s, "tcp", "explain select * from sensor limit 3")
	if resp.Code != 0 {
		t.Fatalf("EXPLAIN 失败: %s", resp.Message)
	}
	rows := respRows(resp)
	if !rowsContainOp(rows, "Limit") {
		t.Errorf("EXPLAIN 输出应包含 Limit 操作: %v", rows)
	}
}

// TestServerExplainAggregate 验证 EXPLAIN 聚合查询包含 Aggregate 节点。
func TestServerExplainAggregate(t *testing.T) {
	s := startSQLServer(t)
	seedSensorData(t, s, "tcp")

	resp := queryVia(t, s, "tcp", "explain select name, count(*) from sensor group by name")
	if resp.Code != 0 {
		t.Fatalf("EXPLAIN 失败: %s", resp.Message)
	}
	rows := respRows(resp)
	if !rowsContainOp(rows, "Aggregate") {
		t.Errorf("EXPLAIN 输出应包含 Aggregate 操作: %v", rows)
	}
}

// TestServerExplainErrors 验证 EXPLAIN 的错误场景经网络返回非零码。
func TestServerExplainErrors(t *testing.T) {
	s := startSQLServer(t)
	cases := []struct {
		name string
		sql  string
	}{
		{"empty", "EXPLAIN"},
		{"non_select", "explain insert into sensor values(1)"},
		{"syntax_err", "explain select from sensor"},
		{"table_not_exist", "explain select * from nonexistent"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := queryVia(t, s, "tcp", c.sql)
			if resp.Code != -1 {
				t.Errorf("SQL %q 期望错误码 -1, 得到 %d", c.sql, resp.Code)
			}
		})
	}
}

// TestServerExplainDoesNotExecute 验证 EXPLAIN 不实际执行查询。
// 对空表执行 EXPLAIN 应返回计划，而非因无数据报错。
func TestServerExplainDoesNotExecute(t *testing.T) {
	s := startSQLServer(t)
	createSensorTable(t, s) // 建表但不写入数据

	resp := queryVia(t, s, "tcp", "explain select * from sensor")
	if resp.Code != 0 {
		t.Fatalf("EXPLAIN 空表应成功: %s", resp.Message)
	}
	rows := respRows(resp)
	if len(rows) == 0 {
		t.Error("EXPLAIN 空表仍应返回计划节点行")
	}
}

// rowsContainOp 检查 EXPLAIN 输出行中是否存在指定操作类型。
func rowsContainOp(rows []map[string]any, op string) bool {
	for _, r := range rows {
		if v, _ := r["operation"].(string); v == op {
			return true
		}
	}
	return false
}

// rowsContainDetail 检查 EXPLAIN 输出中是否有行的 detail 包含 substr。
func rowsContainDetail(rows []map[string]any, substr string) bool {
	for _, r := range rows {
		if v, _ := r["detail"].(string); strings.Contains(v, substr) {
			return true
		}
	}
	return false
}
