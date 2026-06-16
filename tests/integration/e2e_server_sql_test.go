package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

const (
	sqlDialTimeout = 2 * time.Second
	sqlHTTPTimeout = 10 * time.Second
	sensorTable    = "sensor"
)

var sqlHTTPClient = &http.Client{Timeout: sqlHTTPTimeout}

// sqlServer 持有已启动的服务器及其监听地址。
type sqlServer struct {
	srv      *server.Server
	tcpAddr  string
	httpAddr string
}

// startSQLServer 在临时端口上启动服务器并注册清理。
func startSQLServer(t *testing.T) *sqlServer {
	t.Helper()
	dir, err := os.MkdirTemp("", "e2e-sql-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	cfg := server.Config{
		TCPAddr:  "127.0.0.1:0",
		HTTPAddr: "127.0.0.1:0",
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

// createSensorTable 通过 catalog 创建 sensor 表。
func createSensorTable(t *testing.T, s *sqlServer) {
	t.Helper()
	err := s.srv.Catalog().CreateTable(sensorTable, []catalog.ColumnDef{
		{Name: "id", Type: common.TypeInt64, Nullable: false},
		{Name: "name", Type: common.TypeString, Nullable: true},
		{Name: "temperature", Type: common.TypeFloat64, Nullable: true},
		{Name: "active", Type: common.TypeBool, Nullable: true},
	}, []string{"id"}, catalog.TableOptions{})
	if err != nil {
		t.Fatalf("建表失败: %v", err)
	}
}

// sensorRows 返回测试用的传感器数据。
func sensorRows() []map[string]any {
	return []map[string]any{
		{"id": 1, "name": "sensor-A", "temperature": 20.0, "active": true},
		{"id": 2, "name": "sensor-A", "temperature": 30.0, "active": false},
		{"id": 3, "name": "sensor-B", "temperature": 25.0, "active": true},
		{"id": 4, "name": "sensor-B", "temperature": 25.0, "active": true},
		{"id": 5, "name": "sensor-C", "temperature": 40.0, "active": false},
	}
}

// seedSensorData 建表并通过指定协议写入传感器数据。
func seedSensorData(t *testing.T, s *sqlServer, via string) {
	t.Helper()
	createSensorTable(t, s)
	writeVia(t, s, via, sensorTable, sensorRows())
}

// tcpClient 是保持长连接的 TCP 客户端。
type tcpClient struct {
	conn net.Conn
}

// dialTCP 建立到 addr 的 TCP 连接。
func dialTCP(addr string) (*tcpClient, error) {
	conn, err := net.DialTimeout("tcp", addr, sqlDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("tcp 拨号 %s 失败: %w", addr, err)
	}
	return &tcpClient{conn: conn}, nil
}

func (c *tcpClient) close() { _ = c.conn.Close() }

// send 发送一个数据包并读取响应。
func (c *tcpClient) send(typ uint8, payload []byte) (*server.Response, error) {
	pkt := server.NewPacket(typ, payload)
	if _, err := c.conn.Write(pkt.Encode()); err != nil {
		return nil, fmt.Errorf("发送请求失败: %w", err)
	}
	respPkt, err := server.DecodePacket(c.conn)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	var resp server.Response
	if err := json.Unmarshal(respPkt.Payload, &resp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}
	return &resp, nil
}

func (c *tcpClient) query(sql string) (*server.Response, error) {
	payload, _ := json.Marshal(server.QueryRequest{SQL: sql})
	return c.send(server.PacketQuery, payload)
}

func (c *tcpClient) write(table string, rows []map[string]any) (*server.Response, error) {
	payload, _ := json.Marshal(server.WriteRequest{Table: table, Rows: rows})
	return c.send(server.PacketWrite, payload)
}

func (c *tcpClient) ping() (string, error) {
	resp, err := c.send(server.PacketPing, nil)
	if err != nil {
		return "", err
	}
	return resp.Message, nil
}

// httpDo 发送 HTTP POST JSON 请求并解析响应。
func httpDo(addr, path string, body any) (*server.Response, error) {
	reqBody, _ := json.Marshal(body)
	url := fmt.Sprintf("http://%s%s", addr, path)
	resp, err := sqlHTTPClient.Post(url, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	var result server.Response
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}
	return &result, nil
}

func httpQuery(addr, sql string) (*server.Response, error) {
	return httpDo(addr, "/query", server.QueryRequest{SQL: sql})
}

func httpWrite(addr, table string, rows []map[string]any) (*server.Response, error) {
	return httpDo(addr, "/write", server.WriteRequest{Table: table, Rows: rows})
}

// rawQuery 按协议执行查询，返回响应与错误（不调用 t.Fatal，便于在 goroutine 中使用）。
func rawQuery(s *sqlServer, via, sql string) (*server.Response, error) {
	if via == "http" {
		return httpQuery(s.httpAddr, sql)
	}
	tc, err := dialTCP(s.tcpAddr)
	if err != nil {
		return nil, err
	}
	defer tc.close()
	return tc.query(sql)
}

// rawWrite 按协议写入数据，返回响应与错误（不调用 t.Fatal，便于在 goroutine 中使用）。
func rawWrite(s *sqlServer, via, table string, rows []map[string]any) (*server.Response, error) {
	if via == "http" {
		return httpWrite(s.httpAddr, table, rows)
	}
	tc, err := dialTCP(s.tcpAddr)
	if err != nil {
		return nil, err
	}
	defer tc.close()
	return tc.write(table, rows)
}

// queryVia 按协议执行查询，失败时终止测试。
func queryVia(t *testing.T, s *sqlServer, via, sql string) *server.Response {
	t.Helper()
	resp, err := rawQuery(s, via, sql)
	if err != nil {
		t.Fatalf("%s 查询请求失败: %v", via, err)
	}
	return resp
}

// writeVia 按协议写入数据，失败时终止测试。
func writeVia(t *testing.T, s *sqlServer, via, table string, rows []map[string]any) {
	t.Helper()
	resp, err := rawWrite(s, via, table, rows)
	if err != nil {
		t.Fatalf("%s 写入请求失败: %v", via, err)
	}
	if resp.Code != 0 {
		t.Fatalf("%s 写入失败: %s", via, resp.Message)
	}
}

// respRows 从响应中提取行数据。
func respRows(resp *server.Response) []map[string]any {
	rows, ok := resp.Data.([]any)
	if !ok {
		return nil
	}
	result := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		if m, ok := r.(map[string]any); ok {
			result = append(result, m)
		}
	}
	return result
}

// toInt64 将 any 转换为 int64（JSON 数字经反序列化后通常为 float64）。
func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	}
	return 0, false
}

// toFloat64 将 any 转换为 float64。
func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	}
	return 0, false
}

// rowsByID 将响应行按 id 建立索引。
func rowsByID(rows []map[string]any) map[int64]map[string]any {
	result := make(map[int64]map[string]any, len(rows))
	for _, r := range rows {
		if id, ok := toInt64(r["id"]); ok {
			result[id] = r
		}
	}
	return result
}

// verifyAllSensorRows 验证 5 行传感器数据的正确性。
func verifyAllSensorRows(t *testing.T, rows []map[string]any) {
	t.Helper()
	if len(rows) != 5 {
		t.Fatalf("期望 5 行，得到 %d", len(rows))
	}
	byID := rowsByID(rows)
	for _, exp := range sensorRows() {
		id, _ := toInt64(exp["id"])
		row, ok := byID[id]
		if !ok {
			t.Errorf("缺少 id=%d 的行", id)
			continue
		}
		if row["name"] != exp["name"] {
			t.Errorf("id=%d name: 期望 %v，得到 %v", id, exp["name"], row["name"])
		}
		expTemp, _ := toFloat64(exp["temperature"])
		if temp, ok := toFloat64(row["temperature"]); !ok || temp != expTemp {
			t.Errorf("id=%d temperature: 期望 %v，得到 %v", id, expTemp, row["temperature"])
		}
		if row["active"] != exp["active"] {
			t.Errorf("id=%d active: 期望 %v，得到 %v", id, exp["active"], row["active"])
		}
	}
}

// assertFloat 断言聚合数值字段相等。
func assertFloat(t *testing.T, name, field string, got, want any) {
	t.Helper()
	g, ok := toFloat64(got)
	if !ok {
		t.Errorf("%s %s: 值类型异常 %v", name, field, got)
		return
	}
	w, _ := toFloat64(want)
	if g != w {
		t.Errorf("%s %s: 期望 %v，得到 %v", name, field, w, g)
	}
}

// TestServerSQLWriteAndSelect 验证写入与全表查询的完整流程（TCP 与 HTTP）。
func TestServerSQLWriteAndSelect(t *testing.T) {
	for _, via := range []string{"tcp", "http"} {
		t.Run(via, func(t *testing.T) {
			s := startSQLServer(t)
			seedSensorData(t, s, via)
			resp := queryVia(t, s, via, "SELECT * FROM sensor")
			if resp.Code != 0 {
				t.Fatalf("查询失败: %s", resp.Message)
			}
			verifyAllSensorRows(t, respRows(resp))
		})
	}
}

// TestServerSQLPointQuery 验证 WHERE 等值点查。
func TestServerSQLPointQuery(t *testing.T) {
	for _, via := range []string{"tcp", "http"} {
		t.Run(via, func(t *testing.T) {
			s := startSQLServer(t)
			seedSensorData(t, s, via)
			resp := queryVia(t, s, via, "SELECT * FROM sensor WHERE id = 3")
			if resp.Code != 0 {
				t.Fatalf("查询失败: %s", resp.Message)
			}
			rows := respRows(resp)
			if len(rows) != 1 {
				t.Fatalf("期望 1 行，得到 %d", len(rows))
			}
			if name := rows[0]["name"]; name != "sensor-B" {
				t.Errorf("期望 name=sensor-B，得到 %v", name)
			}
		})
	}
}

// TestServerSQLFilterRange 验证 WHERE 范围过滤。
func TestServerSQLFilterRange(t *testing.T) {
	s := startSQLServer(t)
	seedSensorData(t, s, "tcp")
	resp := queryVia(t, s, "tcp", "SELECT * FROM sensor WHERE id > 2")
	if resp.Code != 0 {
		t.Fatalf("查询失败: %s", resp.Message)
	}
	rows := respRows(resp)
	if len(rows) != 3 {
		t.Fatalf("期望 3 行，得到 %d", len(rows))
	}
	byID := rowsByID(rows)
	for _, id := range []int64{3, 4, 5} {
		if _, ok := byID[id]; !ok {
			t.Errorf("期望包含 id=%d", id)
		}
	}
}

// TestServerSQLAggregation 验证 GROUP BY 与聚合函数。
func TestServerSQLAggregation(t *testing.T) {
	s := startSQLServer(t)
	seedSensorData(t, s, "tcp")
	sql := "SELECT name, COUNT(*) AS cnt, AVG(temperature) AS avg_temp, " +
		"SUM(temperature) AS sum_temp, MIN(temperature) AS min_temp, " +
		"MAX(temperature) AS max_temp FROM sensor GROUP BY name"
	resp := queryVia(t, s, "tcp", sql)
	if resp.Code != 0 {
		t.Fatalf("聚合查询失败: %s", resp.Message)
	}
	rows := respRows(resp)
	if len(rows) != 3 {
		t.Fatalf("期望 3 个分组，得到 %d", len(rows))
	}
	// 每组期望值顺序: cnt, avg, sum, min, max
	expected := map[string][]float64{
		"sensor-A": {2, 25.0, 50.0, 20.0, 30.0},
		"sensor-B": {2, 25.0, 50.0, 25.0, 25.0},
		"sensor-C": {1, 40.0, 40.0, 40.0, 40.0},
	}
	for _, row := range rows {
		name, _ := row["name"].(string)
		exp, ok := expected[name]
		if !ok {
			t.Errorf("未知分组: %v", name)
			continue
		}
		assertFloat(t, name, "cnt", row["cnt"], exp[0])
		assertFloat(t, name, "avg_temp", row["avg_temp"], exp[1])
		assertFloat(t, name, "sum_temp", row["sum_temp"], exp[2])
		assertFloat(t, name, "min_temp", row["min_temp"], exp[3])
		assertFloat(t, name, "max_temp", row["max_temp"], exp[4])
	}
}

// TestServerSQLLimit 验证 LIMIT 截断。
func TestServerSQLLimit(t *testing.T) {
	s := startSQLServer(t)
	seedSensorData(t, s, "tcp")
	resp := queryVia(t, s, "tcp", "SELECT * FROM sensor LIMIT 2")
	if resp.Code != 0 {
		t.Fatalf("查询失败: %s", resp.Message)
	}
	if got := len(respRows(resp)); got != 2 {
		t.Errorf("期望 2 行，得到 %d", got)
	}
}

// TestServerSQLProjection 验证列投影只返回指定列。
func TestServerSQLProjection(t *testing.T) {
	s := startSQLServer(t)
	seedSensorData(t, s, "tcp")
	resp := queryVia(t, s, "tcp", "SELECT id, name FROM sensor")
	if resp.Code != 0 {
		t.Fatalf("查询失败: %s", resp.Message)
	}
	rows := respRows(resp)
	if len(rows) != 5 {
		t.Fatalf("期望 5 行，得到 %d", len(rows))
	}
	for i, row := range rows {
		if len(row) != 2 {
			t.Errorf("第 %d 行期望 2 列，得到 %d", i, len(row))
		}
		if _, ok := row["id"]; !ok {
			t.Errorf("第 %d 行缺少 id 列", i)
		}
		if _, ok := row["name"]; !ok {
			t.Errorf("第 %d 行缺少 name 列", i)
		}
	}
}

// TestServerProtocolConsistency 验证同一查询在 TCP 与 HTTP 下结果一致。
func TestServerProtocolConsistency(t *testing.T) {
	s := startSQLServer(t)
	createSensorTable(t, s)
	// 通过 TCP 写入，通过两种协议查询并比对。
	writeVia(t, s, "tcp", sensorTable, sensorRows())

	tcpResp := queryVia(t, s, "tcp", "SELECT * FROM sensor")
	httpResp := queryVia(t, s, "http", "SELECT * FROM sensor")
	if tcpResp.Code != 0 || httpResp.Code != 0 {
		t.Fatalf("查询失败: tcp=%s http=%s", tcpResp.Message, httpResp.Message)
	}
	tcpRows := rowsByID(respRows(tcpResp))
	httpRows := rowsByID(respRows(httpResp))
	if len(tcpRows) != len(httpRows) {
		t.Fatalf("行数不一致: tcp=%d http=%d", len(tcpRows), len(httpRows))
	}
	for id, tcpRow := range tcpRows {
		httpRow, ok := httpRows[id]
		if !ok {
			t.Errorf("HTTP 缺少 id=%d", id)
			continue
		}
		if tcpRow["name"] != httpRow["name"] {
			t.Errorf("id=%d name 不一致: tcp=%v http=%v", id, tcpRow["name"], httpRow["name"])
		}
	}
}

// TestServerMultiClientConcurrent 验证多客户端并发写入与查询。
func TestServerMultiClientConcurrent(t *testing.T) {
	s := startSQLServer(t)
	createSensorTable(t, s)

	const numClients = 8
	const rowsPerClient = 10
	var wg sync.WaitGroup
	var failCount int64

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			via := "tcp"
			if clientID%2 == 0 {
				via = "http"
			}
			if err := runClientWork(s, via, clientID, rowsPerClient); err != nil {
				t.Logf("client %d (%s) 失败: %v", clientID, via, err)
				atomic.AddInt64(&failCount, 1)
			}
		}(i)
	}
	wg.Wait()

	if failCount > 0 {
		t.Fatalf("%d 个客户端失败", failCount)
	}
	resp := queryVia(t, s, "tcp", "SELECT * FROM sensor")
	if resp.Code != 0 {
		t.Fatalf("最终查询失败: %s", resp.Message)
	}
	want := numClients * rowsPerClient
	if got := len(respRows(resp)); got != want {
		t.Errorf("期望 %d 行，得到 %d", want, got)
	}
}

// runClientWork 模拟单个客户端：写入一批数据并执行查询。
func runClientWork(s *sqlServer, via string, clientID, n int) error {
	rows := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		id := clientID*n + i + 1
		rows[i] = map[string]any{
			"id":          id,
			"name":        fmt.Sprintf("c%d", clientID),
			"temperature": float64(id),
			"active":      true,
		}
	}
	resp, err := rawWrite(s, via, sensorTable, rows)
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return fmt.Errorf("写入失败: %s", resp.Message)
	}
	qresp, err := rawQuery(s, via, "SELECT * FROM sensor LIMIT 5")
	if err != nil {
		return err
	}
	if qresp.Code != 0 {
		return fmt.Errorf("查询失败: %s", qresp.Message)
	}
	return nil
}

// TestServerSQLErrors 验证错误场景返回非零码。
func TestServerSQLErrors(t *testing.T) {
	s := startSQLServer(t)
	createSensorTable(t, s)

	// 查询不存在的表
	resp := queryVia(t, s, "tcp", "SELECT * FROM nonexistent")
	if resp.Code == 0 {
		t.Error("查询不存在的表应返回错误")
	}

	// 写入不存在的表
	wresp, err := rawWrite(s, "http", "nonexistent", []map[string]any{{"id": 1}})
	if err != nil {
		t.Fatalf("写入请求失败: %v", err)
	}
	if wresp.Code == 0 {
		t.Error("写入不存在的表应返回错误")
	}

	// 无效 SQL
	iresp := queryVia(t, s, "tcp", "INVALID SQL !!!")
	if iresp.Code == 0 {
		t.Error("无效 SQL 应返回错误")
	}
}

// TestServerHealthAndPing 验证 HTTP 健康检查与 TCP 心跳。
func TestServerHealthAndPing(t *testing.T) {
	s := startSQLServer(t)

	// HTTP 健康检查
	resp, err := sqlHTTPClient.Get(fmt.Sprintf("http://%s/health", s.httpAddr))
	if err != nil {
		t.Fatalf("健康检查失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("健康检查状态码: 期望 %d，得到 %d", http.StatusOK, resp.StatusCode)
	}

	// TCP 心跳
	tc, err := dialTCP(s.tcpAddr)
	if err != nil {
		t.Fatalf("拨号失败: %v", err)
	}
	defer tc.close()
	msg, err := tc.ping()
	if err != nil {
		t.Fatalf("心跳失败: %v", err)
	}
	if msg != "pong" {
		t.Errorf("心跳响应: 期望 pong，得到 %q", msg)
	}
}
