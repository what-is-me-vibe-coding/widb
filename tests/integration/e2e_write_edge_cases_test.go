// Package integration 端到端集成测试：HTTP/TCP /write API 边界场景。
//
// 补充既有 e2e_server_sql_test.go 未覆盖的写入侧边界：
//   - 空 rows 数组：协议不报错，Rows=0
//   - 不存在的表：返回 -1 且 message 包含「表不存在」
//   - 类型不匹配（INT64 列收到 string）：返回 -1 且整批回滚
//   - NULL 值：nil 字段写入后 SELECT 读回仍为 nil
//   - 大批量写入：单批 500 行经 HTTP/TCP 写入，COUNT(*) 正确
//   - JSON 数字类型：float64 与 int64 经反序列化后正确归类
//   - GET 写到 /write：HTTP 返回 405
//   - 非法 JSON：HTTP 返回 400
//   - 跨协议一致性：HTTP 写入 → TCP/PG 读回完全一致
//
// 设计原则：所有测试 t.Parallel，并使用独立 writeEdgeLikeTable 表，互不干扰。
package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

// writeEdgeLikeTable 是 write-edge 测试专用表名（与 sensorTable 解耦）。
const writeEdgeLikeTable = "write_edge"

// writeEdgeCols 定义 write-edge 表的列结构（id 主键，其余可空）。
var writeEdgeCols = []catalog.ColumnDef{
	{Name: "id", Type: common.TypeInt64, Nullable: false},
	{Name: "name", Type: common.TypeString, Nullable: true},
	{Name: "score", Type: common.TypeFloat64, Nullable: true},
	{Name: "active", Type: common.TypeBool, Nullable: true},
}

// createWriteEdgeTable 通过 catalog 建表（id 主键）。
func createWriteEdgeTable(t *testing.T, s *sqlServer) {
	t.Helper()
	err := s.srv.Catalog().CreateTable(writeEdgeLikeTable, writeEdgeCols,
		[]string{"id"}, catalog.TableOptions{})
	if err != nil {
		t.Fatalf("建表 %s 失败: %v", writeEdgeLikeTable, err)
	}
}

// startWriteEdgeTriProtoServer 启动同时监听 TCP/HTTP/PG wire 的服务器。
//
// 与 startSQLServer 相比，额外启用 PG wire 监听，用于 TestWriteEdgeCrossProtocolConsistency
// 的三协议一致性校验。
func startWriteEdgeTriProtoServer(t *testing.T) *sqlServer {
	t.Helper()
	dir, err := os.MkdirTemp("", "e2e-write-edge-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	cfg := server.Config{
		TCPAddr:  "127.0.0.1:0",
		HTTPAddr: "127.0.0.1:0",
		PGAddr:   "127.0.0.1:0",
		DataDir:  dir,
	}
	srv, err := server.NewServer(cfg, server.WithMetricsRegistry(prometheus.NewRegistry()))
	if err != nil {
		t.Fatalf("NewServer 失败: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	return &sqlServer{srv: srv, tcpAddr: srv.TCPAddr(), httpAddr: srv.HTTPAddr()}
}

// TestWriteEdgeEmptyRows 验证空 rows 数组写入返回成功且 Rows=0。
//
// 场景：rows 字段为空 JSON 数组 []。服务端应正常处理，不当作错误。
func TestWriteEdgeEmptyRows(t *testing.T) {
	t.Parallel()
	for _, via := range []string{"tcp", "http"} {
		t.Run(via, func(t *testing.T) {
			t.Parallel()
			s := startSQLServer(t)
			createWriteEdgeTable(t, s)

			resp, err := rawWrite(s, via, writeEdgeLikeTable, []map[string]any{})
			if err != nil {
				t.Fatalf("%s 空行写入失败: %v", via, err)
			}
			if resp.Code != 0 {
				t.Errorf("%s 空行写入期望 code=0，得到 %d (%s)", via, resp.Code, resp.Message)
			}
			if resp.Rows != 0 {
				t.Errorf("%s 空行写入期望 rows=0，得到 %d", via, resp.Rows)
			}

			// 校验表中确实无数据
			qresp := queryVia(t, s, via, fmt.Sprintf("SELECT COUNT(*) AS cnt FROM %s", writeEdgeLikeTable))
			if got := countFromResp(qresp); got != 0 {
				t.Errorf("%s 空行写入后表行数期望 0，得到 %d", via, got)
			}
		})
	}
}

// TestWriteEdgeUnknownTable 验证写入不存在的表返回 -1 + 「表不存在」错误。
func TestWriteEdgeUnknownTable(t *testing.T) {
	t.Parallel()
	for _, via := range []string{"tcp", "http"} {
		t.Run(via, func(t *testing.T) {
			t.Parallel()
			s := startSQLServer(t)

			resp, err := rawWrite(s, via, "no_such_table", []map[string]any{
				{"id": 1},
			})
			if err != nil {
				t.Fatalf("%s 写入错误: %v", via, err)
			}
			if resp.Code == 0 {
				t.Errorf("%s 期望 code=-1，得到 code=0", via)
			}
			if !strings.Contains(resp.Message, "表不存在") {
				t.Errorf("%s 错误消息应包含「表不存在」，实际: %s", via, resp.Message)
			}
		})
	}
}

// TestWriteEdgeTypeMismatchBatchRollback 验证一批中含类型不匹配行时整批回滚。
//
// 场景：3 行中第 2 行 score 字段传 string「abc」而非 float64。
// 期望：整批失败，code=-1；再次 SELECT 验证表行数仍为 0（已写入行被回滚）。
func TestWriteEdgeTypeMismatchBatchRollback(t *testing.T) {
	t.Parallel()
	for _, via := range []string{"tcp", "http"} {
		t.Run(via, func(t *testing.T) {
			t.Parallel()
			s := startSQLServer(t)
			createWriteEdgeTable(t, s)

			bad := []map[string]any{
				{"id": 1, "name": "ok-1", "score": 1.0, "active": true},
				{"id": 2, "name": "bad", "score": "abc", "active": true}, // 类型错误
				{"id": 3, "name": "ok-3", "score": 3.0, "active": true},
			}
			resp, err := rawWrite(s, via, writeEdgeLikeTable, bad)
			if err != nil {
				t.Fatalf("%s 写入错误: %v", via, err)
			}
			if resp.Code == 0 {
				t.Errorf("%s 类型错误批处理应被拒绝，得到 code=0", via)
			}
			if !strings.Contains(resp.Message, "类型") && !strings.Contains(resp.Message, "expected") {
				t.Errorf("%s 错误消息应提示类型错误，实际: %s", via, resp.Message)
			}

			// 校验：整批回滚，表行数仍为 0
			qresp := queryVia(t, s, via, fmt.Sprintf("SELECT COUNT(*) AS cnt FROM %s", writeEdgeLikeTable))
			if got := countFromResp(qresp); got != 0 {
				t.Errorf("%s 类型错误批处理后表行数期望 0（整批回滚），得到 %d", via, got)
			}
		})
	}
}

// TestWriteEdgeNullValues 验证 NULL 字段（JSON null）写入后 SELECT 读回仍为 nil。
//
// 场景：3 行中第 1 行 name/score/active 全为 null。
// 期望：写入成功，SELECT 返回行中对应字段为 nil。
func TestWriteEdgeNullValues(t *testing.T) {
	t.Parallel()
	for _, via := range []string{"tcp", "http"} {
		t.Run(via, func(t *testing.T) {
			t.Parallel()
			s := startSQLServer(t)
			createWriteEdgeTable(t, s)

			rows := []map[string]any{
				{"id": 1, "name": nil, "score": nil, "active": nil},
				{"id": 2, "name": "two", "score": 2.5, "active": false},
			}
			writeVia(t, s, via, writeEdgeLikeTable, rows)

			qresp := queryVia(t, s, via, fmt.Sprintf("SELECT id, name, score, active FROM %s ORDER BY id", writeEdgeLikeTable))
			if qresp.Code != 0 {
				t.Fatalf("%s SELECT 失败: %s", via, qresp.Message)
			}
			got := respRows(qresp)
			if len(got) != 2 {
				t.Fatalf("%s 期望 2 行，得到 %d", via, len(got))
			}
			// id=1 的 name/score/active 应均为 nil
			for _, col := range []string{"name", "score", "active"} {
				if got[0][col] != nil {
					t.Errorf("%s id=1.%s 期望 nil，得到 %v", via, col, got[0][col])
				}
			}
			if got[1]["name"] != "two" {
				t.Errorf("%s id=2.name 期望 two，得到 %v", via, got[1]["name"])
			}
		})
	}
}

// TestWriteEdgeLargeBatch 验证大批量（500 行）单次写入正确性。
//
// 场景：500 行单次 HTTP 写入，id ∈ [1, 500]。
// 期望：写入成功，COUNT(*) = 500。
func TestWriteEdgeLargeBatch(t *testing.T) {
	t.Parallel()
	const n = 500
	for _, via := range []string{"tcp", "http"} {
		t.Run(via, func(t *testing.T) {
			t.Parallel()
			s := startSQLServer(t)
			createWriteEdgeTable(t, s)

			rows := make([]map[string]any, n)
			for i := 0; i < n; i++ {
				rows[i] = map[string]any{
					"id":     int64(i + 1),
					"name":   fmt.Sprintf("row-%d", i+1),
					"score":  float64(i),
					"active": i%2 == 0,
				}
			}
			resp, err := rawWrite(s, via, writeEdgeLikeTable, rows)
			if err != nil {
				t.Fatalf("%s 大批量写入错误: %v", via, err)
			}
			if resp.Code != 0 {
				t.Fatalf("%s 大批量写入失败: %s", via, resp.Message)
			}
			if resp.Rows != n {
				t.Errorf("%s 大批量写入 rows 期望 %d，得到 %d", via, n, resp.Rows)
			}

			qresp := queryVia(t, s, via, fmt.Sprintf("SELECT COUNT(*) AS cnt FROM %s", writeEdgeLikeTable))
			if got := countFromResp(qresp); got != n {
				t.Errorf("%s 大批量写入后行数期望 %d，得到 %d", via, n, got)
			}
		})
	}
}

// TestWriteEdgeJSONNumberTypes 验证 JSON 数字类型在写入时被正确归类。
//
// 场景：score 列为 FLOAT64，传入 float64 与 int64 两种数字。
// 期望：两者都成功写入（int64 经 float64 路径强转），读回值正确。
func TestWriteEdgeJSONNumberTypes(t *testing.T) {
	t.Parallel()
	s := startSQLServer(t)
	createWriteEdgeTable(t, s)

	rows := []map[string]any{
		// 显式 float64
		{"id": 1, "name": "f64", "score": 3.14, "active": true},
		// 显式 int64，期望被强转为 float64 = 2.0
		{"id": 2, "name": "i64", "score": int64(2), "active": false},
	}
	writeVia(t, s, "http", writeEdgeLikeTable, rows)

	qresp := queryVia(t, s, "http", fmt.Sprintf("SELECT id, score FROM %s ORDER BY id", writeEdgeLikeTable))
	if qresp.Code != 0 {
		t.Fatalf("SELECT 失败: %s", qresp.Message)
	}
	got := respRows(qresp)
	if len(got) != 2 {
		t.Fatalf("期望 2 行，得到 %d", len(got))
	}
	if v, _ := toFloat64(got[0]["score"]); v != 3.14 {
		t.Errorf("id=1 score 期望 3.14，得到 %v", got[0]["score"])
	}
	if v, _ := toFloat64(got[1]["score"]); v != 2.0 {
		t.Errorf("id=2 score 期望 2.0（int64 强转），得到 %v", got[1]["score"])
	}
}

// TestWriteEdgeHTTPGetRejected 验证 GET /write 被服务端拒绝（HTTP 405）。
//
// 场景：对 /write 发起 GET 请求。
// 期望：HTTP 状态码 405，响应体 code=-1。
func TestWriteEdgeHTTPGetRejected(t *testing.T) {
	t.Parallel()
	s := startSQLServer(t)
	createWriteEdgeTable(t, s)

	url := "http://" + s.httpAddr + "/write"
	resp, err := sqlHTTPClient.Get(url)
	if err != nil {
		t.Fatalf("GET /write 失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /write 期望 405，得到 %d", resp.StatusCode)
	}
	var body server.Response
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body.Code != -1 {
		t.Errorf("GET /write 响应 code 期望 -1，得到 %d", body.Code)
	}
}

// TestWriteEdgeHTTPMalformedJSON 验证 /write 收到非法 JSON 时返回 HTTP 400。
//
// 场景：POST /write，body 为非 JSON 字符串「{not valid json」。
// 期望：HTTP 状态码 400，响应体 code=-1。
func TestWriteEdgeHTTPMalformedJSON(t *testing.T) {
	t.Parallel()
	s := startSQLServer(t)
	createWriteEdgeTable(t, s)

	url := "http://" + s.httpAddr + "/write"
	resp, err := sqlHTTPClient.Post(url, "application/json", bytes.NewBufferString("{not valid json"))
	if err != nil {
		t.Fatalf("POST /write 失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("非法 JSON 期望 400，得到 %d", resp.StatusCode)
	}
	var body server.Response
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body.Code != -1 {
		t.Errorf("非法 JSON 响应 code 期望 -1，得到 %d", body.Code)
	}
}

// TestWriteEdgeCrossProtocolConsistency 验证 HTTP 写入可被 TCP 与 PG 读回一致。
//
// 场景：HTTP 写入 3 行；TCP 与 PG 各自全表 SELECT 读回。
// 期望：TCP/PG 返回的 (id, name) 集合与 HTTP 写入完全一致。
func TestWriteEdgeCrossProtocolConsistency(t *testing.T) {
	t.Parallel()
	s := startWriteEdgeTriProtoServer(t)
	createWriteEdgeTable(t, s)

	rows := []map[string]any{
		{"id": 1, "name": "alpha", "score": 1.0, "active": true},
		{"id": 2, "name": "beta", "score": 2.0, "active": false},
		{"id": 3, "name": "gamma", "score": 3.0, "active": true},
	}
	writeVia(t, s, "http", writeEdgeLikeTable, rows)

	// TCP 读回
	tcpResp := queryVia(t, s, "tcp", fmt.Sprintf("SELECT id, name FROM %s ORDER BY id", writeEdgeLikeTable))
	if tcpResp.Code != 0 {
		t.Fatalf("TCP SELECT 失败: %s", tcpResp.Message)
	}
	tcpGot := respRows(tcpResp)
	if len(tcpGot) != 3 {
		t.Fatalf("TCP 读回期望 3 行，得到 %d", len(tcpGot))
	}
	for i, want := range rows {
		if id, _ := toInt64(tcpGot[i]["id"]); id != int64(i+1) {
			t.Errorf("TCP row[%d].id 期望 %d，得到 %d", i, i+1, id)
		}
		if tcpGot[i]["name"] != want["name"] {
			t.Errorf("TCP row[%d].name 期望 %s，得到 %v", i, want["name"], tcpGot[i]["name"])
		}
	}

	// PG 读回
	pgAddr := s.srv.PGAddr()
	if pgAddr == "" {
		t.Fatal("PG wire 未启用，但 cross-protocol 测试需要 PG")
	}
	c := dialPGWire(t, pgAddr)
	c.handshake(t)
	sql := fmt.Sprintf("SELECT id, name FROM %s ORDER BY id", writeEdgeLikeTable)
	pgRes, err := c.sendQueryRead(sql)
	if err != nil {
		t.Fatalf("PG 查询失败: %v", err)
	}
	_ = c.conn.Close()
	if pgRes.errMsg != "" {
		t.Fatalf("PG SELECT 失败: %s", pgRes.errMsg)
	}
	if len(pgRes.rows) != 3 {
		t.Fatalf("PG 读回期望 3 行，得到 %d", len(pgRes.rows))
	}
	for i, want := range rows {
		if got := pgRes.rows[i][0]; got != fmt.Sprintf("%d", i+1) {
			t.Errorf("PG row[%d].id 期望 %d，得到 %v", i, i+1, got)
		}
		if got := pgRes.rows[i][1]; got != want["name"] {
			t.Errorf("PG row[%d].name 期望 %s，得到 %v", i, want["name"], got)
		}
	}
}

// countFromResp 从 COUNT(*) AS cnt 响应中提取行数。
func countFromResp(resp *server.Response) int64 {
	rows := respRows(resp)
	if len(rows) == 0 {
		return 0
	}
	v, ok := rows[0]["cnt"]
	if !ok {
		return 0
	}
	n, _ := toInt64(v)
	return n
}
