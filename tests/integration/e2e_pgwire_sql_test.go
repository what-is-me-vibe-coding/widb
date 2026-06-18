package integration

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

// PG wire 协议常量。
const (
	pgProtocolVersion = 196608 // PostgreSQL 协议 3.0
	pgDialTimeout     = 2 * time.Second
	pgFieldOverhead   = 18 // RowDescription 每列除列名外的固定字节数
)

// pgResult 是一次 PG wire 查询的解析结果。
type pgResult struct {
	columns []string // RowDescription 中的列名顺序
	rows    [][]any  // 每行的值（string 或 nil）
	tag     string   // CommandComplete 命令标签
	errMsg  string   // ErrorResponse 的 message 字段，非空表示执行出错
}

// pgWireClient 是一个极简的 PostgreSQL wire 协议客户端，仅使用 Simple Query 协议。
type pgWireClient struct {
	conn net.Conn
}

// dialPGWire 建立到 PG wire 服务端的连接。
func dialPGWire(t *testing.T, addr string) *pgWireClient {
	t.Helper()
	c, err := dialPGWireErr(addr)
	if err != nil {
		t.Fatalf("拨号 PG wire %s 失败: %v", addr, err)
	}
	return c
}

// dialPGWireErr 建立到 PG wire 服务端的连接，返回错误而非终止测试，
// 便于在并发 goroutine 中使用。
func dialPGWireErr(addr string) (*pgWireClient, error) {
	conn, err := net.DialTimeout("tcp", addr, pgDialTimeout)
	if err != nil {
		return nil, err
	}
	return &pgWireClient{conn: conn}, nil
}

// handshake 发送 StartupMessage 并读取至 ReadyForQuery，完成启动握手。
func (c *pgWireClient) handshake(t *testing.T) {
	t.Helper()
	if err := c.handshakeErr(); err != nil {
		t.Fatalf("PG wire 握手失败: %v", err)
	}
}

// handshakeErr 完成启动握手，返回错误而非终止测试。
func (c *pgWireClient) handshakeErr() error {
	if err := c.sendStartup(); err != nil {
		return fmt.Errorf("发送 startup: %w", err)
	}
	if _, err := c.readUntilReadyForQuery(); err != nil {
		return fmt.Errorf("握手响应: %w", err)
	}
	return nil
}

// sendQueryRead 发送一条 Query 并读取完整响应，返回结果与错误（不终止测试）。
func (c *pgWireClient) sendQueryRead(sql string) (*pgResult, error) {
	if err := c.sendQuery(sql); err != nil {
		return nil, err
	}
	return c.readQueryResult()
}

// close 发送 Terminate 并关闭连接。
func (c *pgWireClient) close() {
	_ = c.sendTerminate()
	_ = c.conn.Close()
}

// exec 发送一条 Query 并读取完整响应，返回解析结果。
// 传输层错误终止测试；SQL 执行错误通过返回结果的 errMsg 字段体现。
func (c *pgWireClient) exec(t *testing.T, sql string) *pgResult {
	t.Helper()
	if err := c.sendQuery(sql); err != nil {
		t.Fatalf("发送查询 %q 失败: %v", sql, err)
	}
	res, err := c.readQueryResult()
	if err != nil {
		t.Fatalf("读取查询 %q 响应失败: %v", sql, err)
	}
	return res
}

// execOK 执行 SQL 并断言无错误，返回结果。
func (c *pgWireClient) execOK(t *testing.T, sql string) *pgResult {
	t.Helper()
	res := c.exec(t, sql)
	if res.errMsg != "" {
		t.Fatalf("执行 %q 失败: %s", sql, res.errMsg)
	}
	return res
}

// sendStartup 发送 StartupMessage（无类型前缀，长度自包含）。
func (c *pgWireClient) sendStartup() error {
	buf := &bytes.Buffer{}
	_ = binary.Write(buf, binary.BigEndian, uint32(pgProtocolVersion))
	for _, kv := range [][2]string{{"user", "test"}, {"database", "testdb"}} {
		buf.WriteString(kv[0])
		buf.WriteByte(0)
		buf.WriteString(kv[1])
		buf.WriteByte(0)
	}
	buf.WriteByte(0) // 参数终止符
	body := buf.Bytes()
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(4+len(body)))
	_, err := c.conn.Write(append(header, body...))
	return err
}

// sendQuery 发送 Query 消息（'Q' 前缀）。
func (c *pgWireClient) sendQuery(sql string) error {
	body := append([]byte(sql), 0)
	header := make([]byte, 5)
	header[0] = 'Q'
	binary.BigEndian.PutUint32(header[1:5], uint32(4+len(body)))
	if _, err := c.conn.Write(header); err != nil {
		return err
	}
	_, err := c.conn.Write(body)
	return err
}

// sendTerminate 发送 Terminate 消息（'X' 前缀）。
func (c *pgWireClient) sendTerminate() error {
	header := []byte{'X', 0, 0, 0, 4}
	_, err := c.conn.Write(header)
	return err
}

// readMessage 读取一个带类型前缀的后端消息，返回类型字节与消息体。
func (c *pgWireClient) readMessage() (byte, []byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(c.conn, header); err != nil {
		return 0, nil, err
	}
	msgType := header[0]
	bodyLen := binary.BigEndian.Uint32(header[1:5])
	if bodyLen < 4 {
		return 0, nil, fmt.Errorf("invalid body length: %d", bodyLen)
	}
	body := make([]byte, bodyLen-4)
	if _, err := io.ReadFull(c.conn, body); err != nil {
		return 0, nil, err
	}
	return msgType, body, nil
}

// readUntilReadyForQuery 读取消息直到 ReadyForQuery('Z')，返回消息类型序列。
func (c *pgWireClient) readUntilReadyForQuery() ([]byte, error) {
	var types []byte
	for {
		mt, _, err := c.readMessage()
		if err != nil {
			return types, err
		}
		types = append(types, mt)
		if mt == 'Z' {
			return types, nil
		}
	}
}

// readQueryResult 读取一次查询的完整响应：RowDescription? + DataRow* +
// CommandComplete/EmptyQueryResponse + ReadyForQuery，可能含 ErrorResponse。
func (c *pgWireClient) readQueryResult() (*pgResult, error) {
	res := &pgResult{}
	for {
		mt, body, err := c.readMessage()
		if err != nil {
			return res, err
		}
		if !c.absorbQueryMessage(res, mt, body) {
			return res, nil
		}
	}
}

// absorbQueryMessage 将一条消息并入结果，返回 false 表示响应结束（收到 ReadyForQuery）。
func (c *pgWireClient) absorbQueryMessage(res *pgResult, mt byte, body []byte) bool {
	switch mt {
	case 'T': // RowDescription
		res.columns = parsePGColumns(body)
	case 'D': // DataRow
		res.rows = append(res.rows, parsePGDataRow(body))
	case 'C': // CommandComplete
		res.tag = parsePGCString(body)
	case 'I': // EmptyQueryResponse
		res.tag = ""
	case 'E': // ErrorResponse
		res.errMsg = parsePGError(body)
	case 'Z': // ReadyForQuery
		return false
	default:
		// 忽略其它后端消息（如 ParameterStatus/NoticeResponse）
	}
	return true
}

// parsePGColumns 从 RowDescription 消息体解析列名顺序。
func parsePGColumns(body []byte) []string {
	if len(body) < 2 {
		return nil
	}
	fieldCount := int(binary.BigEndian.Uint16(body[0:2]))
	cols := make([]string, 0, fieldCount)
	pos := 2
	for i := 0; i < fieldCount; i++ {
		end := bytes.IndexByte(body[pos:], 0)
		if end < 0 {
			return cols
		}
		cols = append(cols, string(body[pos:pos+end]))
		pos += end + 1 + pgFieldOverhead
	}
	return cols
}

// parsePGDataRow 从 DataRow 消息体解析各列值（string 或 nil）。
func parsePGDataRow(body []byte) []any {
	if len(body) < 2 {
		return nil
	}
	colCount := int(binary.BigEndian.Uint16(body[0:2]))
	vals := make([]any, 0, colCount)
	pos := 2
	for i := 0; i < colCount; i++ {
		if pos+4 > len(body) {
			return vals
		}
		ln := int32(binary.BigEndian.Uint32(body[pos : pos+4]))
		pos += 4
		if ln < 0 {
			vals = append(vals, nil)
			continue
		}
		vals = append(vals, string(body[pos:pos+int(ln)]))
		pos += int(ln)
	}
	return vals
}

// parsePGCString 解析以 \0 结尾的 C 字符串。
func parsePGCString(body []byte) string {
	return strings.TrimRight(string(body), "\x00")
}

// parsePGError 从 ErrorResponse 消息体提取 'M'（message）字段。
func parsePGError(body []byte) string {
	pos := 0
	for pos < len(body) {
		field := body[pos]
		pos++
		if field == 0 {
			break
		}
		end := bytes.IndexByte(body[pos:], 0)
		if end < 0 {
			return ""
		}
		val := string(body[pos : pos+end])
		pos += end + 1
		if field == 'M' {
			return val
		}
	}
	return ""
}

// pgRowToMap 将一行值按列名打包为 map，便于按列名取值。
func pgRowToMap(cols []string, row []any) map[string]any {
	m := make(map[string]any, len(cols))
	for i, col := range cols {
		if i < len(row) {
			m[col] = row[i]
		}
	}
	return m
}

// pgFloat 将 PG 文本值转为 float64，用于数值断言。
func pgFloat(v any) (float64, bool) {
	s, ok := v.(string)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// pgInt 将 PG 文本值转为 int64，用于整数断言。
func pgInt(v any) (int64, bool) {
	s, ok := v.(string)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// startPGWireServer 启动一个同时监听 TCP/HTTP/PG wire 的服务器。
func startPGWireServer(t *testing.T) *sqlServer {
	t.Helper()
	dir, err := os.MkdirTemp("", "e2e-pg-*")
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

// pgCreateSensorTable 通过 PG wire 创建 sensor 表并写入初始数据。
func pgCreateSensorTable(t *testing.T, c *pgWireClient) {
	t.Helper()
	c.execOK(t, "CREATE TABLE sensor (id INT64 NOT NULL, name STRING NULL, "+
		"temperature FLOAT64 NULL, active BOOL NULL, PRIMARY KEY(id))")
	c.execOK(t, "INSERT INTO sensor (id, name, temperature, active) VALUES "+
		"(1, 'sensor-A', 20.0, true), (2, 'sensor-A', 30.0, false), "+
		"(3, 'sensor-B', 25.0, true), (4, 'sensor-B', 25.0, true), "+
		"(5, 'sensor-C', 40.0, false)")
}

// TestPGWireEndToEndSQL 验证通过 PG wire 协议执行完整 SQL 生命周期的正确性：
// CREATE/INSERT/SELECT/点查/范围/聚合/LIMIT/投影/UPDATE/DELETE/SHOW/DESCRIBE。
func TestPGWireEndToEndSQL(t *testing.T) {
	s := startPGWireServer(t)
	c := dialPGWire(t, s.srv.PGAddr())
	defer c.close()
	c.handshake(t)
	pgCreateSensorTable(t, c)

	pgVerifyReadQueries(t, c)
	pgVerifyWriteAndMeta(t, c)
}

// pgVerifyReadQueries 验证 SELECT/点查/范围/LIMIT/投影/GROUP BY 聚合。
func pgVerifyReadQueries(t *testing.T, c *pgWireClient) {
	t.Helper()
	// SELECT * 全表查询
	res := c.execOK(t, "SELECT * FROM sensor")
	if len(res.rows) != 5 {
		t.Fatalf("SELECT * 期望 5 行，得到 %d", len(res.rows))
	}
	byID := pgRowsByID(res)
	pgVerifySensorRow(t, byID, 1, "sensor-A", 20.0, "t")
	pgVerifySensorRow(t, byID, 3, "sensor-B", 25.0, "t")
	pgVerifySensorRow(t, byID, 5, "sensor-C", 40.0, "f")

	// 点查
	pres := c.execOK(t, "SELECT * FROM sensor WHERE id = 2")
	if len(pres.rows) != 1 {
		t.Errorf("点查 id=2 期望 1 行，得到 %d", len(pres.rows))
	}
	// 范围过滤
	rres := c.execOK(t, "SELECT * FROM sensor WHERE id > 2")
	if len(rres.rows) != 3 {
		t.Errorf("范围查询 id>2 期望 3 行，得到 %d", len(rres.rows))
	}
	// LIMIT
	lres := c.execOK(t, "SELECT * FROM sensor LIMIT 2")
	if len(lres.rows) != 2 {
		t.Errorf("LIMIT 2 期望 2 行，得到 %d", len(lres.rows))
	}
	// 投影
	pgVerifyProjection(t, c)
	// GROUP BY 聚合
	pgVerifyGroupBy(t, c)
}

// pgVerifyProjection 验证列投影只返回指定列。
func pgVerifyProjection(t *testing.T, c *pgWireClient) {
	t.Helper()
	proj := c.execOK(t, "SELECT id, name FROM sensor")
	for _, row := range proj.rows {
		if len(row) != 2 {
			t.Errorf("投影期望 2 列，得到 %d", len(row))
		}
	}
}

// pgVerifyGroupBy 验证 GROUP BY 聚合结果。
func pgVerifyGroupBy(t *testing.T, c *pgWireClient) {
	t.Helper()
	ares := c.execOK(t, "SELECT name, COUNT(*) AS cnt, SUM(temperature) AS total "+
		"FROM sensor GROUP BY name")
	if len(ares.rows) != 3 {
		t.Fatalf("GROUP BY 期望 3 组，得到 %d", len(ares.rows))
	}
	pgVerifyAggregate(t, ares, "sensor-A", 2, 50.0)
	pgVerifyAggregate(t, ares, "sensor-B", 2, 50.0)
	pgVerifyAggregate(t, ares, "sensor-C", 1, 40.0)
}

// pgVerifyWriteAndMeta 验证 UPDATE/DELETE/SHOW TABLES/DESCRIBE。
func pgVerifyWriteAndMeta(t *testing.T, c *pgWireClient) {
	t.Helper()
	// UPDATE
	ures := c.execOK(t, "UPDATE sensor SET temperature = 99.0 WHERE id = 1")
	if !strings.Contains(ures.tag, "1") {
		t.Errorf("UPDATE 命令标签 %q 应含受影响行数 1", ures.tag)
	}
	vres := c.execOK(t, "SELECT temperature FROM sensor WHERE id = 1")
	if temp, _ := pgFloat(pgRowToMap(vres.columns, vres.rows[0])["temperature"]); temp != 99.0 {
		t.Errorf("UPDATE 后 temperature 期望 99，得到 %v", vres.rows[0])
	}

	// DELETE
	dres := c.execOK(t, "DELETE FROM sensor WHERE id = 5")
	if !strings.Contains(dres.tag, "1") {
		t.Errorf("DELETE 命令标签 %q 应含受影响行数 1", dres.tag)
	}
	cres := c.execOK(t, "SELECT COUNT(*) AS cnt FROM sensor")
	if cnt, _ := pgInt(pgRowToMap(cres.columns, cres.rows[0])["cnt"]); cnt != 4 {
		t.Errorf("DELETE 后 COUNT 期望 4，得到 %d", cnt)
	}

	// SHOW TABLES / DESCRIBE
	st := c.execOK(t, "SHOW TABLES")
	if !pgRowsContain(st.rows, "sensor") {
		t.Errorf("SHOW TABLES 未包含 sensor: %v", st.rows)
	}
	desc := c.execOK(t, "DESCRIBE sensor")
	if len(desc.rows) != 4 {
		t.Errorf("DESCRIBE 期望 4 行，得到 %d", len(desc.rows))
	}
}

// TestPGWireErrorHandling 验证 PG wire 对错误 SQL 返回 ErrorResponse。
func TestPGWireErrorHandling(t *testing.T) {
	s := startPGWireServer(t)
	c := dialPGWire(t, s.srv.PGAddr())
	defer c.close()
	c.handshake(t)

	// 无效 SQL
	if res := c.exec(t, "INVALID SQL !!!"); res.errMsg == "" {
		t.Error("无效 SQL 应返回 ErrorResponse")
	}

	// 查询不存在的表
	if res := c.exec(t, "SELECT * FROM nonexistent"); res.errMsg == "" {
		t.Error("查询不存在的表应返回 ErrorResponse")
	}

	// 错误后连接仍可用（ReadyForQuery 已发送）
	ok := c.execOK(t, "CREATE TABLE ok (id INT64 NOT NULL, PRIMARY KEY(id))")
	if ok.errMsg != "" {
		t.Errorf("错误后连接应仍可用: %s", ok.errMsg)
	}
}

// pgRowsByID 将结果行按 id 列建立索引。
func pgRowsByID(res *pgResult) map[int64]map[string]any {
	result := make(map[int64]map[string]any, len(res.rows))
	for _, row := range res.rows {
		m := pgRowToMap(res.columns, row)
		if id, ok := pgInt(m["id"]); ok {
			result[id] = m
		}
	}
	return result
}

// pgVerifySensorRow 断言单行传感器数据。
func pgVerifySensorRow(t *testing.T, byID map[int64]map[string]any, id int64,
	name string, temp float64, active string) {
	t.Helper()
	row, ok := byID[id]
	if !ok {
		t.Errorf("缺少 id=%d 的行", id)
		return
	}
	if row["name"] != name {
		t.Errorf("id=%d name 期望 %q，得到 %v", id, name, row["name"])
	}
	if got, _ := pgFloat(row["temperature"]); got != temp {
		t.Errorf("id=%d temperature 期望 %v，得到 %v", id, temp, row["temperature"])
	}
	if row["active"] != active {
		t.Errorf("id=%d active 期望 %q，得到 %v", id, active, row["active"])
	}
}

// pgVerifyAggregate 断言 GROUP BY 聚合结果中的某一组。
func pgVerifyAggregate(t *testing.T, res *pgResult, name string, cnt int64, total float64) {
	t.Helper()
	for _, row := range res.rows {
		m := pgRowToMap(res.columns, row)
		if m["name"] != name {
			continue
		}
		if got, _ := pgInt(m["cnt"]); got != cnt {
			t.Errorf("%s cnt 期望 %d，得到 %v", name, cnt, m["cnt"])
		}
		if got, _ := pgFloat(m["total"]); got != total {
			t.Errorf("%s total 期望 %v，得到 %v", name, total, m["total"])
		}
		return
	}
	t.Errorf("聚合结果缺少分组 %q", name)
}

// pgRowsContain 判断结果行中是否存在某列等于 want 的行。
func pgRowsContain(rows [][]any, want string) bool {
	for _, row := range rows {
		for _, v := range row {
			if s, ok := v.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}
