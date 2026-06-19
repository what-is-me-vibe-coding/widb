package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestBuildCommandTag 验证根据 SQL 语句和响应推断 PG 协议命令标签。
// 覆盖 buildCommandTag 的所有分支。
func TestBuildCommandTag(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		rows int
		want string
	}{
		{"select", "SELECT * FROM t", 5, "SELECT 5"},
		{"select lowercase", "select 1", 1, "SELECT 1"},
		{"select with leading space", "  SELECT 1", 0, "SELECT 0"},
		{"insert", "INSERT INTO t VALUES (1)", 3, "INSERT 0 3"},
		{"create table", "CREATE TABLE t (id INT64)", 0, "CREATE TABLE"},
		{"create index", "CREATE INDEX idx ON t(id)", 0, "CREATE"},
		{"drop", "DROP TABLE t", 0, "DROP"},
		{"delete", "DELETE FROM t", 7, "DELETE 7"},
		{"update", "UPDATE t SET a=1", 2, "UPDATE 2"},
		{"explain", "EXPLAIN SELECT 1", 2, "EXPLAIN 2"},
		{"explain lowercase", "explain select * from t", 1, "EXPLAIN 1"},
		{"empty", "", 0, "OK"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &Response{Rows: tt.rows}
			got := buildCommandTag(tt.sql, resp)
			if got != tt.want {
				t.Errorf("buildCommandTag(%q) = %q, want %q", tt.sql, got, tt.want)
			}
		})
	}
}

// TestPgwireAdapterExecuteSQLQuery 验证 pgwireAdapter.ExecuteSQL 正确透传列类型。
// 创建含 DATE/TIMESTAMP/INT 等类型的表，插入数据后查询，验证 SQLResult.ColumnTypes
// 与 Schema 类型一致（修复前 ColumnTypes 始终为空，导致 pgwire 类型推断错误）。
func TestPgwireAdapterExecuteSQLQuery(t *testing.T) {
	srv := newTestServer(t)

	// 建表：覆盖多种类型（INT16 用 SMALLINT，INT32 用 MEDIUMINT）
	createResp, err := srv.ExecuteQuery(
		"CREATE TABLE typed (id INT64, name STRING, score FLOAT64, active BOOL, ts TIMESTAMP, d DATE, small SMALLINT, PRIMARY KEY(id))")
	if err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if createResp.Code != 0 {
		t.Fatalf("建表失败: %s", createResp.Message)
	}

	// 插入数据
	insertResp, err := srv.ExecuteQuery(
		"INSERT INTO typed (id, name, score, active, ts, d, small) VALUES (1, 'alice', 9.5, true, '2024-01-02T03:04:05Z', '2024-01-02', 7)")
	if err != nil {
		t.Fatalf("插入失败: %v", err)
	}
	if insertResp.Code != 0 {
		t.Fatalf("插入失败: %s", insertResp.Message)
	}

	adapter := &pgwireAdapter{server: srv}
	result, err := adapter.ExecuteSQL("SELECT id, name, score, active, ts, d, small FROM typed")
	if err != nil {
		t.Fatalf("ExecuteSQL 失败: %v", err)
	}
	if !result.IsQuery {
		t.Fatal("期望 IsQuery=true")
	}
	if len(result.Rows) != 1 {
		t.Fatalf("期望 1 行, got %d", len(result.Rows))
	}

	// 验证 ColumnTypes 已透传
	wantTypes := []int{
		int(common.TypeInt64), int(common.TypeString), int(common.TypeFloat64),
		int(common.TypeBool), int(common.TypeTimestamp), int(common.TypeDate),
		int(common.TypeInt16),
	}
	if len(result.ColumnTypes) != len(wantTypes) {
		t.Fatalf("期望 %d 个列类型, got %d (%v)", len(wantTypes), len(result.ColumnTypes), result.ColumnTypes)
	}
	for i, want := range wantTypes {
		if result.ColumnTypes[i] != want {
			t.Errorf("列 %d 期望类型 %d, got %d", i, want, result.ColumnTypes[i])
		}
	}
}

// TestPgwireAdapterExecuteSQLInsert 验证非查询语句（INSERT）的适配。
func TestPgwireAdapterExecuteSQLInsert(t *testing.T) {
	srv := newTestServer(t)

	if _, err := srv.ExecuteQuery(
		"CREATE TABLE ins (id INT64, PRIMARY KEY(id))"); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	adapter := &pgwireAdapter{server: srv}
	result, err := adapter.ExecuteSQL("INSERT INTO ins (id) VALUES (1), (2), (3)")
	if err != nil {
		t.Fatalf("ExecuteSQL 失败: %v", err)
	}
	if result.IsQuery {
		t.Error("INSERT 不应为查询")
	}
	if result.RowsAffected != 3 {
		t.Errorf("期望 RowsAffected=3, got %d", result.RowsAffected)
	}
	if result.CommandTag != "INSERT 0 3" {
		t.Errorf("期望 CommandTag='INSERT 0 3', got %q", result.CommandTag)
	}
}

// TestPgwireAdapterExecuteSQLError 验证 SQL 执行错误时返回 error。
func TestPgwireAdapterExecuteSQLError(t *testing.T) {
	srv := newTestServer(t)

	adapter := &pgwireAdapter{server: srv}
	_, err := adapter.ExecuteSQL("SELECT * FROM nonexistent")
	if err == nil {
		t.Error("期望返回错误, got nil")
	}
}

// TestPgwireAdapterExecuteSQLColumnTypesPassthrough 验证 ColumnTypes 与 Response.ColumnTypes 一致。
func TestPgwireAdapterExecuteSQLColumnTypesPassthrough(t *testing.T) {
	srv := newTestServer(t)

	createResp, err := srv.ExecuteQuery(
		"CREATE TABLE ct (a MEDIUMINT, b DATE, c TIMESTAMP, PRIMARY KEY(a))")
	if err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if createResp.Code != 0 {
		t.Fatalf("建表失败: %s", createResp.Message)
	}
	insertResp, err := srv.ExecuteQuery(
		"INSERT INTO ct (a, b, c) VALUES (1, '2024-05-06', '2024-05-06T07:08:09Z')")
	if err != nil {
		t.Fatalf("插入失败: %v", err)
	}
	if insertResp.Code != 0 {
		t.Fatalf("插入失败: %s", insertResp.Message)
	}

	// 先通过 ExecuteQuery 验证 Response.ColumnTypes 已填充
	resp, err := srv.ExecuteQuery("SELECT a, b, c FROM ct")
	if err != nil {
		t.Fatalf("ExecuteQuery 失败: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("查询失败: %s", resp.Message)
	}
	if len(resp.ColumnTypes) != 3 {
		t.Fatalf("期望 3 个列类型, got %d", len(resp.ColumnTypes))
	}

	// 再通过 adapter 验证透传
	adapter := &pgwireAdapter{server: srv}
	result, err := adapter.ExecuteSQL("SELECT a, b, c FROM ct")
	if err != nil {
		t.Fatalf("ExecuteSQL 失败: %v", err)
	}
	for i, ct := range resp.ColumnTypes {
		if result.ColumnTypes[i] != int(ct) {
			t.Errorf("列 %d 期望 %d, got %d", i, ct, result.ColumnTypes[i])
		}
	}
}

// TestResponseColumnTypesNotSerialized 验证 ColumnTypes 字段不参与 JSON 序列化，
// 确保 TCP/HTTP 线协议不受影响。
func TestResponseColumnTypesNotSerialized(t *testing.T) {
	resp := &Response{
		Code:        0,
		Columns:     []string{"a"},
		ColumnTypes: []common.DataType{common.TypeDate},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	str := string(data)
	if strings.Contains(str, "ColumnTypes") {
		t.Errorf("ColumnTypes 不应出现在 JSON 中, got %s", str)
	}
	if strings.Contains(str, "column_types") {
		t.Errorf("column_types 不应出现在 JSON 中, got %s", str)
	}
}

// TestHandleQueryPopulatesColumnTypes 验证 handleQuery 正确填充 ColumnTypes。
func TestHandleQueryPopulatesColumnTypes(t *testing.T) {
	srv := newTestServer(t)

	if _, err := srv.ExecuteQuery(
		"CREATE TABLE qct (id INT64, d DATE, PRIMARY KEY(id))"); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if _, err := srv.ExecuteQuery(
		"INSERT INTO qct (id, d) VALUES (1, '2024-01-01')"); err != nil {
		t.Fatalf("插入失败: %v", err)
	}

	resp, err := srv.handleQuery(&QueryRequest{SQL: "SELECT id, d FROM qct"})
	if err != nil {
		t.Fatalf("handleQuery 失败: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("响应错误: %s", resp.Message)
	}
	if len(resp.ColumnTypes) != 2 {
		t.Fatalf("期望 2 个列类型, got %d", len(resp.ColumnTypes))
	}
	if resp.ColumnTypes[0] != common.TypeInt64 {
		t.Errorf("列 0 期望 TypeInt64, got %s", resp.ColumnTypes[0])
	}
	if resp.ColumnTypes[1] != common.TypeDate {
		t.Errorf("列 1 期望 TypeDate, got %s", resp.ColumnTypes[1])
	}
}

// TestHandleQueryNoSchemaColumnTypes 验证无 Schema 的查询（如 DDL）ColumnTypes 为空。
func TestHandleQueryNoSchemaColumnTypes(t *testing.T) {
	srv := newTestServer(t)
	resp, err := srv.handleQuery(&QueryRequest{SQL: "CREATE TABLE ns (id INT64, PRIMARY KEY(id))"})
	if err != nil {
		t.Fatalf("handleQuery 失败: %v", err)
	}
	if resp.ColumnTypes != nil {
		t.Errorf("DDL 查询不应有 ColumnTypes, got %v", resp.ColumnTypes)
	}
}
