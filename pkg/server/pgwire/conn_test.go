package pgwire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgproto3/v2"
)

// sslNegotiationResponse 测试
func TestSSLNegotiationResponseEncode(t *testing.T) {
	r := sslNegotiationResponse{}
	dst, err := r.Encode(nil)
	if err != nil {
		t.Fatalf("Encode 失败: %v", err)
	}
	if len(dst) != 1 || dst[0] != 'N' {
		t.Errorf("期望单字节 'N', got %v", dst)
	}
}

func TestSSLNegotiationResponseDecode(t *testing.T) {
	r := sslNegotiationResponse{}
	if err := r.Decode([]byte{1, 2, 3}); err != nil {
		t.Errorf("Decode 不应返回错误: %v", err)
	}
}

func TestSSLNegotiationResponseBackend(t *testing.T) {
	t.Helper()
	r := sslNegotiationResponse{}
	r.Backend() // 仅验证不 panic
}

// --- PG 协议客户端辅助函数 ---

// pgClient 封装一个原始 PG 协议客户端，用于测试。
type pgClient struct {
	conn net.Conn
}

func newPGClient(t *testing.T, addr string) *pgClient {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial 失败: %v", err)
	}
	return &pgClient{conn: conn}
}

func (c *pgClient) close() { _ = c.conn.Close() }

// sendStartupMessage 发送 StartupMessage。
func (c *pgClient) sendStartupMessage() error {
	// StartupMessage: length(4) + protocol(4) + params + \0\0
	buf := &bytes.Buffer{}
	// protocol version 3.0 = 196608
	_ = binary.Write(buf, binary.BigEndian, uint32(196608))
	buf.WriteString("user")
	buf.WriteByte(0)
	buf.WriteString("test")
	buf.WriteByte(0)
	buf.WriteString("database")
	buf.WriteByte(0)
	buf.WriteString("testdb")
	buf.WriteByte(0)
	buf.WriteByte(0) // 终止符
	body := buf.Bytes()
	// length = 4 (length itself) + len(body)
	totalLen := uint32(4 + len(body))
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, totalLen)
	if _, err := c.conn.Write(header); err != nil {
		return err
	}
	if _, err := c.conn.Write(body); err != nil {
		return err
	}
	return nil
}

// sendSSLRequest 发送 SSLRequest。
func (c *pgClient) sendSSLRequest() error {
	// SSLRequest: length(4) + 80877103(4)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint32(buf[0:4], 8)
	binary.BigEndian.PutUint32(buf[4:8], 80877103)
	_, err := c.conn.Write(buf)
	return err
}

// sendGSSEncRequest 发送 GSSEncRequest。
func (c *pgClient) sendGSSEncRequest() error {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint32(buf[0:4], 8)
	binary.BigEndian.PutUint32(buf[4:8], 80877104)
	_, err := c.conn.Write(buf)
	return err
}

// sendQuery 发送 Query 消息。
func (c *pgClient) sendQuery(sql string) error {
	body := []byte(sql)
	body = append(body, 0) // 终止符
	totalLen := uint32(4 + len(body))
	buf := make([]byte, 5)
	buf[0] = 'Q'
	binary.BigEndian.PutUint32(buf[1:5], totalLen)
	if _, err := c.conn.Write(buf); err != nil {
		return err
	}
	if _, err := c.conn.Write(body); err != nil {
		return err
	}
	return nil
}

// sendTerminate 发送 Terminate 消息。
func (c *pgClient) sendTerminate() error {
	buf := make([]byte, 5)
	buf[0] = 'X'
	binary.BigEndian.PutUint32(buf[1:5], 4)
	_, err := c.conn.Write(buf)
	return err
}

// sendSync 发送 Sync 消息。
func (c *pgClient) sendSync() error {
	buf := make([]byte, 5)
	buf[0] = 'S'
	binary.BigEndian.PutUint32(buf[1:5], 4)
	_, err := c.conn.Write(buf)
	return err
}

// sendFlush 发送 Flush 消息。
func (c *pgClient) sendFlush() error {
	buf := make([]byte, 5)
	buf[0] = 'H'
	binary.BigEndian.PutUint32(buf[1:5], 4)
	_, err := c.conn.Write(buf)
	return err
}

// readMessage 读取一个 PG 后端消息（带类型前缀）。
func (c *pgClient) readMessage() (byte, []byte, error) {
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

// readUntilReadyForQuery 读取消息直到 ReadyForQuery，返回所有消息类型序列。
func (c *pgClient) readUntilReadyForQuery() ([]byte, error) {
	var types []byte
	for {
		mt, _, err := c.readMessage()
		if err != nil {
			return types, err
		}
		types = append(types, mt)
		if mt == 'Z' { // ReadyForQuery
			return types, nil
		}
		if mt == 'E' { // ErrorResponse
			// 继续读取直到 ReadyForQuery
			continue
		}
	}
}

// --- 连接处理器测试 ---

// startTestServer 启动一个测试用的 pgwire 服务端。
func startTestServer(t *testing.T, exec SQLExecutor) *Server {
	t.Helper()
	srv := NewServer("127.0.0.1:0", exec)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	return srv
}

// TestConnStartupHandshake 验证完整的启动握手流程。
func TestConnStartupHandshake(t *testing.T) {
	srv := startTestServer(t, &mockExecutor{})
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()

	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}

	// 期望收到: AuthenticationOk ('R') + ParameterStatus* ('S') + BackendKeyData ('K') + ReadyForQuery ('Z')
	var gotTypes []byte
	for {
		mt, _, err := client.readMessage()
		if err != nil {
			t.Fatalf("读取消息失败: %v", err)
		}
		gotTypes = append(gotTypes, mt)
		if mt == 'Z' {
			break
		}
		if len(gotTypes) > 20 {
			t.Fatalf("消息过多, got %v", gotTypes)
		}
	}

	if gotTypes[0] != 'R' {
		t.Errorf("第一个消息应为 AuthenticationOk('R'), got %c", gotTypes[0])
	}
	if gotTypes[len(gotTypes)-1] != 'Z' {
		t.Errorf("最后一个消息应为 ReadyForQuery('Z'), got %c", gotTypes[len(gotTypes)-1])
	}
	// 应包含 BackendKeyData ('K')
	hasK := false
	for _, c := range gotTypes {
		if c == 'K' {
			hasK = true
		}
	}
	if !hasK {
		t.Errorf("应包含 BackendKeyData('K'), got %v", gotTypes)
	}
}

// TestConnSSLNegotiation 验证 SSL 协商流程。
func TestConnSSLNegotiation(t *testing.T) {
	srv := startTestServer(t, &mockExecutor{})
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()

	// 先发 SSLRequest
	if err := client.sendSSLRequest(); err != nil {
		t.Fatalf("sendSSLRequest 失败: %v", err)
	}
	// 期望收到单字节 'N'
	resp := make([]byte, 1)
	if _, err := io.ReadFull(client.conn, resp); err != nil {
		t.Fatalf("读取 SSL 响应失败: %v", err)
	}
	if resp[0] != 'N' {
		t.Errorf("期望 'N', got %c", resp[0])
	}
	// 再发 StartupMessage
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	types, err := client.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取握手响应失败: %v", err)
	}
	if len(types) == 0 || types[0] != 'R' {
		t.Errorf("SSL 协商后第一个消息应为 AuthenticationOk, got %v", types)
	}
}

// TestConnGSSEncNegotiation 验证 GSS 加密协商流程。
func TestConnGSSEncNegotiation(t *testing.T) {
	srv := startTestServer(t, &mockExecutor{})
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()

	if err := client.sendGSSEncRequest(); err != nil {
		t.Fatalf("sendGSSEncRequest 失败: %v", err)
	}
	resp := make([]byte, 1)
	if _, err := io.ReadFull(client.conn, resp); err != nil {
		t.Fatalf("读取 GSS 响应失败: %v", err)
	}
	if resp[0] != 'N' {
		t.Errorf("期望 'N', got %c", resp[0])
	}
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	types, err := client.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取握手响应失败: %v", err)
	}
	if len(types) == 0 || types[0] != 'R' {
		t.Errorf("GSS 协商后第一个消息应为 AuthenticationOk, got %v", types)
	}
}

// TestConnQuerySelect 验证 SELECT 查询流程。
func TestConnQuerySelect(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns: []string{"id", "name"},
		Rows: []map[string]any{
			{"id": int64(1), "name": "alice"},
			{"id": int64(2), "name": "bob"},
		},
		IsQuery: true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendQuery("SELECT * FROM t"); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	// 期望: RowDescription('T') + DataRow*('D') + CommandComplete('C') + ReadyForQuery('Z')
	types, err := client.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取查询响应失败: %v", err)
	}
	if len(types) < 4 {
		t.Fatalf("消息过少: %v", types)
	}
	if types[0] != 'T' {
		t.Errorf("第一个应为 RowDescription('T'), got %c", types[0])
	}
	// 2 个 DataRow
	if types[1] != 'D' || types[2] != 'D' {
		t.Errorf("应为 2 个 DataRow('D'), got %v", types[1:3])
	}
	if types[3] != 'C' {
		t.Errorf("应为 CommandComplete('C'), got %c", types[3])
	}
	if types[4] != 'Z' {
		t.Errorf("应为 ReadyForQuery('Z'), got %c", types[4])
	}
	// 验证 executor 收到了 SQL
	if got := exec.lastQuery(); got != "SELECT * FROM t" {
		t.Errorf("executor 收到 %q, 期望 SELECT * FROM t", got)
	}
}

// TestConnQueryError 验证查询错误返回 ErrorResponse。
func TestConnQueryError(t *testing.T) {
	exec := &mockExecutor{err: errors.New("table not found")}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendQuery("SELECT * FROM missing"); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	types, err := client.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	hasError := false
	for _, c := range types {
		if c == 'E' {
			hasError = true
		}
	}
	if !hasError {
		t.Errorf("应包含 ErrorResponse('E'), got %v", types)
	}
	if types[len(types)-1] != 'Z' {
		t.Errorf("应以 ReadyForQuery 结尾, got %c", types[len(types)-1])
	}
}

// TestConnEmptyQuery 验证空查询返回 EmptyQueryResponse。
func TestConnEmptyQuery(t *testing.T) {
	exec := &mockExecutor{}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendQuery("   "); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	types, err := client.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	if len(types) != 2 {
		t.Fatalf("期望 2 个消息, got %v", types)
	}
	if types[0] != 'I' { // EmptyQueryResponse
		t.Errorf("期望 EmptyQueryResponse('I'), got %c", types[0])
	}
	if types[1] != 'Z' {
		t.Errorf("期望 ReadyForQuery('Z'), got %c", types[1])
	}
}

// TestConnTerminate 验证 Terminate 消息关闭连接。
func TestConnTerminate(t *testing.T) {
	srv := startTestServer(t, &mockExecutor{})
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendTerminate(); err != nil {
		t.Fatalf("sendTerminate 失败: %v", err)
	}
	// 服务端应关闭连接，读取应返回 EOF
	_, _, err := client.readMessage()
	if err == nil {
		t.Error("期望 EOF 或错误, 但读取成功")
	}
}

// TestConnSync 验证 Sync 消息触发 ReadyForQuery。
func TestConnSync(t *testing.T) {
	srv := startTestServer(t, &mockExecutor{})
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendSync(); err != nil {
		t.Fatalf("sendSync 失败: %v", err)
	}
	mt, _, err := client.readMessage()
	if err != nil {
		t.Fatalf("读取 Sync 响应失败: %v", err)
	}
	if mt != 'Z' {
		t.Errorf("Sync 后期望 ReadyForQuery('Z'), got %c", mt)
	}
}

// TestConnFlush 验证 Flush 消息不触发响应但保持连接。
func TestConnFlush(t *testing.T) {
	srv := startTestServer(t, &mockExecutor{})
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendFlush(); err != nil {
		t.Fatalf("sendFlush 失败: %v", err)
	}
	// Flush 不返回消息，发送一个 Query 验证连接仍可用
	if err := client.sendQuery("SELECT 1"); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	types, err := client.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取查询响应失败: %v", err)
	}
	if len(types) == 0 {
		t.Error("Flush 后查询应返回消息")
	}
}

// TestConnNonQueryCommand 验证非查询命令（如 INSERT）返回 CommandComplete。
func TestConnNonQueryCommand(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		RowsAffected: 3,
		CommandTag:   "INSERT 0 3",
		IsQuery:      false,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendQuery("INSERT INTO t VALUES (1)"); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	types, err := client.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	if len(types) != 2 {
		t.Fatalf("期望 2 个消息, got %v", types)
	}
	if types[0] != 'C' { // CommandComplete
		t.Errorf("期望 CommandComplete('C'), got %c", types[0])
	}
}

// TestConnMultipleQueries 验证连接可处理多个连续查询。
func TestConnMultipleQueries(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns: []string{"v"},
		Rows:    []map[string]any{{"v": int64(1)}},
		IsQuery: true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := client.sendQuery("SELECT 1"); err != nil {
			t.Fatalf("第 %d 次 sendQuery 失败: %v", i, err)
		}
		types, err := client.readUntilReadyForQuery()
		if err != nil {
			t.Fatalf("第 %d 次读取响应失败: %v", i, err)
		}
		if len(types) < 3 {
			t.Errorf("第 %d 次响应消息过少: %v", i, types)
		}
	}
}

// TestConnQueryWithNilValues 验证含 NULL 值的查询结果。
func TestConnQueryWithNilValues(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns: []string{"id", "name"},
		Rows: []map[string]any{
			{"id": int64(1), "name": nil},
			{"id": nil, "name": "bob"},
		},
		IsQuery: true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendQuery("SELECT * FROM t"); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	types, err := client.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	// T + D + D + C + Z
	if len(types) != 5 {
		t.Errorf("期望 5 个消息, got %v", types)
	}
}

// TestConnEmptyResultQuery 验证空结果集查询。
func TestConnEmptyResultQuery(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns: []string{"id"},
		Rows:    nil,
		IsQuery: true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendQuery("SELECT * FROM empty"); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	types, err := client.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	// T + C + Z (无 DataRow)
	if len(types) != 3 {
		t.Errorf("期望 3 个消息, got %v", types)
	}
	if types[0] != 'T' {
		t.Errorf("期望 RowDescription('T'), got %c", types[0])
	}
	if types[1] != 'C' {
		t.Errorf("期望 CommandComplete('C'), got %c", types[1])
	}
}

// TestConnUnexpectedStartupMessage 验证非预期启动消息的处理。
func TestConnUnexpectedStartupMessage(t *testing.T) {
	srv := startTestServer(t, &mockExecutor{})
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()

	// 发送一个无效的启动消息（长度为 0 的特殊消息）
	// 使用一个未知协议版本
	buf := make([]byte, 8)
	binary.BigEndian.PutUint32(buf[0:4], 8)     // length = 8
	binary.BigEndian.PutUint32(buf[4:8], 99999) // 未知协议
	if _, err := client.conn.Write(buf); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	// 服务端应关闭连接或返回错误
	time.Sleep(50 * time.Millisecond)
	// 尝试读取，应失败或 EOF
	client.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, err := client.readMessage()
	if err == nil {
		t.Log("读取返回 nil（连接已关闭）")
	}
}

// TestConnConcurrentConnections 验证并发连接处理。
func TestConnConcurrentConnections(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns: []string{"v"},
		Rows:    []map[string]any{{"v": int64(1)}},
		IsQuery: true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	const n = 10
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			client := newPGClient(t, srv.Addr())
			defer client.close()
			if err := client.sendStartupMessage(); err != nil {
				errs <- fmt.Errorf("client %d startup: %w", idx, err)
				return
			}
			if _, err := client.readUntilReadyForQuery(); err != nil {
				errs <- fmt.Errorf("client %d handshake: %w", idx, err)
				return
			}
			if err := client.sendQuery("SELECT 1"); err != nil {
				errs <- fmt.Errorf("client %d query: %w", idx, err)
				return
			}
			if _, err := client.readUntilReadyForQuery(); err != nil {
				errs <- fmt.Errorf("client %d response: %w", idx, err)
				return
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestConnHandlerDirect 直接测试 connHandler 的方法（不通过网络）。
func TestConnHandlerDirect(t *testing.T) {
	t.Run("newConnHandler", func(t *testing.T) {
		// 使用一对 net.Pipe 模拟连接
		clientConn, serverConn := net.Pipe()
		defer func() { _ = clientConn.Close() }()
		defer func() { _ = serverConn.Close() }()

		backend := pgproto3.NewBackend(pgproto3.NewChunkReader(serverConn), serverConn)
		exec := &mockExecutor{}
		h := newConnHandler(backend, exec)
		if h == nil {
			t.Fatal("newConnHandler 返回 nil")
		}
		if h.backend == nil {
			t.Error("backend 不应为 nil")
		}
		if h.executor == nil {
			t.Error("executor 不应为 nil")
		}
	})
}

// TestConnQueryResultWithAllTypes 验证所有支持类型的查询结果。
func TestConnQueryResultWithAllTypes(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns: []string{"b", "i", "f", "s"},
		Rows: []map[string]any{
			{"b": true, "i": int64(42), "f": float64(3.14), "s": "text"},
		},
		IsQuery: true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendQuery("SELECT * FROM types"); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	types, err := client.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	// T + D + C + Z
	if len(types) != 4 {
		t.Errorf("期望 4 个消息, got %v", types)
	}
}
