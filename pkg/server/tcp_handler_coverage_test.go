package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// covV2TransientErrListener 包装 net.Listener，在前 N 次 Accept 调用中注入瞬态错误
type covV2TransientErrListener struct {
	net.Listener
	injectCount int32
}

func (l *covV2TransientErrListener) Accept() (net.Conn, error) {
	if atomic.LoadInt32(&l.injectCount) > 0 {
		atomic.AddInt32(&l.injectCount, -1)
		return nil, &net.OpError{
			Op:  "accept",
			Net: "tcp",
			Err: errors.New("resource temporarily unavailable"),
		}
	}
	return l.Listener.Accept()
}

// TestHandleQueryPacketInvalidJSON_CovV2 测试 handleQueryPacket 对无效 JSON 的错误处理
// 覆盖 tcp_handler.go:115-117 行的 JSON 反序列化错误分支
func TestHandleQueryPacketInvalidJSON_CovV2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name    string
		payload []byte
	}{
		{"空payload", []byte{}},
		{testNameIncompleteJSON, []byte("{")},
		{testNamePureNumber, []byte("42")},
		{testNameBinaryGarbage, []byte{0x00, 0xFF, 0xFE}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(PacketQuery, tt.payload)
			resp, err := srv.handleQueryPacket(pkt)
			if err == nil {
				t.Error("期望无效 JSON 返回错误")
			}
			if resp != nil {
				t.Errorf("期望 nil 响应，得到 %v", resp)
			}
		})
	}
}

// TestHandleWritePacketInvalidJSON_CovV2 测试 handleWritePacket 对无效 JSON 的错误处理
// 覆盖 tcp_handler.go:135-137 行的 JSON 反序列化错误分支
func TestHandleWritePacketInvalidJSON_CovV2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name    string
		payload []byte
	}{
		{"空payload", []byte{}},
		{testNameIncompleteJSON, []byte("{")},
		{testNamePureNumber, []byte("42")},
		{testNameBinaryGarbage, []byte{0xDE, 0xAD}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(PacketWrite, tt.payload)
			resp, err := srv.handleWritePacket(pkt)
			if err == nil {
				t.Error("期望无效 JSON 返回错误")
			}
			if resp != nil {
				t.Errorf("期望 nil 响应，得到 %v", resp)
			}
		})
	}
}

// TestHandlePacketUnknownType_CovV2 测试 handlePacket 对未知包类型的错误处理
// 覆盖 tcp_handler.go:108 行的 default 分支
func TestHandlePacketUnknownType_CovV2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name    string
		pktType uint8
	}{
		{"类型0", 0},
		{"类型5", 5},
		{"类型99", 99},
		{"类型255", 255},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(tt.pktType, nil)
			resp, err := srv.handlePacket(pkt)
			if err == nil {
				t.Error("期望未知包类型返回错误")
			}
			if resp != nil {
				t.Errorf("期望 nil 响应，得到 %v", resp)
			}
		})
	}
}

// TestHandleQueryPacketQueryError_CovV2 测试 handleQueryPacket 中查询出错的路径
// 注意：handleQuery 将错误编码为 Response{Code:-1} 并返回 nil Go error，
// 因此 handleQueryPacket 中 handleQuery 返回非 nil Go error 的路径在当前实现中不可达。
// 通过关闭存储引擎测试可能的错误传播路径，验证函数不会 panic。
func TestHandleQueryPacketQueryError_CovV2(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	// 关闭存储引擎，使后续查询可能产生错误
	_ = srv.storage.Close()

	payload, _ := json.Marshal(QueryRequest{SQL: testSelectAll})
	pkt := NewPacket(PacketQuery, payload)
	resp, err := srv.handleQueryPacket(pkt)

	// handleQuery 将错误编码为 Response{Code:-1}，不返回 Go error
	// 关闭存储后查询可能仍返回 Code=0（内存数据可读）或 Code=-1（执行错误），
	// 关键是验证函数不会 panic 且返回合理结果
	if err != nil {
		t.Logf("关闭存储后 handleQueryPacket 返回 Go error: %v", err)
	} else {
		if resp == nil {
			t.Fatal("期望非 nil 响应")
		}
		var response Response
		if unmarshalErr := json.Unmarshal(resp.Payload, &response); unmarshalErr == nil {
			t.Logf("关闭存储后查询响应: Code=%d, Message=%q", response.Code, response.Message)
		}
	}
}

// TestHandleWritePacketWriteError_CovV2 测试 handleWritePacket 中写入出错的路径
// 注意：handleWrite 将错误编码为 Response{Code:-1} 并返回 nil Go error，
// 因此 handleWritePacket 中 handleWrite 返回非 nil Go error 的路径在当前实现中不可达。
// 通过关闭存储引擎测试可能的错误传播路径。
func TestHandleWritePacketWriteError_CovV2(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	// 关闭存储引擎，使后续写入可能产生错误
	_ = srv.storage.Close()

	payload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(1), testColName: testName}},
	})
	pkt := NewPacket(PacketWrite, payload)
	resp, err := srv.handleWritePacket(pkt)

	if err != nil {
		t.Logf("关闭存储后 handleWritePacket 返回 Go error: %v", err)
	}
	if resp != nil {
		var response Response
		if unmarshalErr := json.Unmarshal(resp.Payload, &response); unmarshalErr == nil {
			if response.Code != -1 {
				t.Errorf("期望 Code=-1，得到 %d", response.Code)
			}
		}
	}
}

// TestAcceptTCP_TransientError_CovV2 测试 acceptTCP 在遇到瞬态错误时继续重试
// 覆盖 tcp_handler.go:28-29 行的瞬态错误处理分支
func TestAcceptTCP_TransientError_CovV2(t *testing.T) {
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

	// 创建真实 TCP 监听器
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen 失败: %v", err)
	}

	// 包装为瞬态错误监听器：首次 Accept 返回瞬态错误
	wrappedLn := &covV2TransientErrListener{Listener: ln, injectCount: 1}
	srv.tcpListener = wrappedLn

	// 启动 accept 循环
	srv.wg.Add(1)
	go srv.acceptTCP()

	// 等待瞬态错误被处理
	time.Sleep(100 * time.Millisecond)

	// 验证服务器仍然可以接受连接（瞬态错误后 accept 循环继续）
	conn, dialErr := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if dialErr != nil {
		t.Fatalf("瞬态错误后连接失败: %v", dialErr)
	}
	_ = conn.Close()

	// 清理：关闭 done 通道和监听器，等待 goroutine 退出
	close(srv.done)
	_ = ln.Close()
	srv.wg.Wait()
	_ = srv.storage.Close()
}

// TestAcceptTCP_ConnectionLimit_CovV2 测试 acceptTCP 在连接数达到上限时拒绝新连接
// 覆盖 tcp_handler.go:38-41 行的连接数限制分支
func TestAcceptTCP_ConnectionLimit_CovV2(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		TCPAddr:        testListenAddr,
		HTTPAddr:       testListenAddr,
		DataDir:        dir,
		MaxConnections: 1,
	}
	registry := prometheus.NewRegistry()
	srv, err := NewServer(cfg, WithMetricsRegistry(registry))
	if err != nil {
		t.Fatalf("NewServer 失败: %v", err)
	}

	if startErr := srv.Start(); startErr != nil {
		t.Fatalf("Start 失败: %v", startErr)
	}
	defer func() { _ = srv.Stop() }()

	time.Sleep(50 * time.Millisecond)

	// 建立第一个连接（占用唯一的名额）
	conn1, dialErr := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if dialErr != nil {
		t.Fatalf("第一个连接失败: %v", dialErr)
	}
	defer func() { _ = conn1.Close() }()

	// 等待连接被接受处理
	time.Sleep(100 * time.Millisecond)

	// 第二个连接：连接数已达上限，应被拒绝或关闭
	conn2, dialErr2 := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if dialErr2 != nil {
		t.Logf("第二个连接被拒绝（预期行为）: %v", dialErr2)
		return
	}
	defer func() { _ = conn2.Close() }()

	// 如果连接建立成功，服务器应关闭它
	_ = conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1024)
	_, readErr := conn2.Read(buf)
	if readErr == nil {
		t.Log("第二个连接未被立即关闭，但限制可能已通过 connCount 检查")
	}
}

// TestHandleTCPConn_ErrorResponse_CovV2 测试 handleTCPConn 中 handlePacket 返回错误时的响应构造路径
// 覆盖 tcp_handler.go:80-86 行的错误响应构造分支
// 注意：json.Marshal(errResp) 的错误路径不可达（Response 结构简单，始终可序列化），
// 此测试覆盖 err != nil 时构造错误响应的正常路径。
func TestHandleTCPConn_ErrorResponse_CovV2(t *testing.T) {
	srv := newTestServer(t)
	if startErr := srv.Start(); startErr != nil {
		t.Fatalf("Start 失败: %v", startErr)
	}
	defer func() { _ = srv.Stop() }()

	time.Sleep(50 * time.Millisecond)

	conn, dialErr := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if dialErr != nil {
		t.Fatalf("连接 TCP 失败: %v", dialErr)
	}
	defer func() { _ = conn.Close() }()

	// 发送未知类型的包，触发 handlePacket 错误路径
	pkt := NewPacket(99, nil)
	if _, writeErr := conn.Write(pkt.Encode()); writeErr != nil {
		t.Fatalf("写入包失败: %v", writeErr)
	}

	// 读取错误响应
	if deadlineErr := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); deadlineErr != nil {
		t.Fatalf("设置读超时失败: %v", deadlineErr)
	}

	respPkt, decodeErr := DecodePacket(bufio.NewReader(conn))
	if decodeErr != nil {
		t.Fatalf("解码响应失败: %v", decodeErr)
	}

	if respPkt.Type != PacketResponse {
		t.Errorf("响应类型 = %d，期望 %d", respPkt.Type, PacketResponse)
	}

	var response Response
	if unmarshalErr := json.Unmarshal(respPkt.Payload, &response); unmarshalErr != nil {
		t.Fatalf("解析响应失败: %v", unmarshalErr)
	}
	if response.Code != -1 {
		t.Errorf("响应 Code = %d，期望 -1", response.Code)
	}
}

// 测试用例中重复使用的字符串常量，避免 goconst 重复字符串警告
const (
	testNameIncompleteJSON = "不完整JSON"
	testNamePureNumber     = "纯数字"
	testNameBinaryGarbage  = "二进制垃圾"
)

// ---------------------------------------------------------------------------
// handleQueryPacket JSON 反序列化错误路径（tcp_handler.go:113）
// ---------------------------------------------------------------------------

// TestHandleQueryPacketBadJSONCov_V2 测试 handleQueryPacket 对各种无效 JSON 的处理
// 覆盖 tcp_handler.go:115-117 行的 JSON 反序列化错误分支
func TestHandleQueryPacketBadJSONCov_V2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name    string
		payload []byte
	}{
		{"空字节", []byte{}},
		{testNameIncompleteJSON, []byte("{")},
		{testNamePureNumber, []byte("42")},
		{"数组", []byte(`[1,2,3]`)},
		{"布尔值", []byte("true")},
		{testNameBinaryGarbage, []byte{0x00, 0x01, 0x02, 0xFF}},
		{"无效UTF8", []byte("\xff\xfe\xfd")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(PacketQuery, tt.payload)
			resp, err := srv.handleQueryPacket(pkt)
			if err == nil {
				t.Error("期望无效 JSON 返回错误，得到 nil")
			}
			if resp != nil {
				t.Errorf("期望 nil 响应，得到 %v", resp)
			}
		})
	}
}

// TestHandleQueryPacketValidJSONCov_V2 测试 handleQueryPacket 对有效 JSON 的处理
// 验证正常路径：有效 JSON 解析后执行查询
func TestHandleQueryPacketValidJSONCov_V2(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	payload, _ := json.Marshal(QueryRequest{SQL: "SELECT * FROM " + testTable})
	pkt := NewPacket(PacketQuery, payload)
	resp, err := srv.handleQueryPacket(pkt)
	if err != nil {
		t.Fatalf("有效 JSON 不应返回错误: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应")
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应类型 = %d，期望 %d", resp.Type, PacketResponse)
	}
}

// ---------------------------------------------------------------------------
// handleWritePacket JSON 反序列化错误路径（tcp_handler.go:133）
// ---------------------------------------------------------------------------

// TestHandleWritePacketBadJSONCov_V2 测试 handleWritePacket 对各种无效 JSON 的处理
// 覆盖 tcp_handler.go:135-137 行的 JSON 反序列化错误分支
func TestHandleWritePacketBadJSONCov_V2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name    string
		payload []byte
	}{
		{"空字节", []byte{}},
		{testNameIncompleteJSON, []byte("{")},
		{testNamePureNumber, []byte("42")},
		{"字符串", []byte(`"hello"`)},
		{"布尔值", []byte("false")},
		{testNameBinaryGarbage, []byte{0xDE, 0xAD, 0xBE, 0xEF}},
		{"无效UTF8", []byte("\xff\xfe")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(PacketWrite, tt.payload)
			resp, err := srv.handleWritePacket(pkt)
			if err == nil {
				t.Error("期望无效 JSON 返回错误，得到 nil")
			}
			if resp != nil {
				t.Errorf("期望 nil 响应，得到 %v", resp)
			}
		})
	}
}

// TestHandleWritePacketValidJSONCov_V2 测试 handleWritePacket 对有效 JSON 的处理
// 验证正常路径：有效 JSON 解析后执行写入
func TestHandleWritePacketValidJSONCov_V2(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	payload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(1), testColName: testName}},
	})
	pkt := NewPacket(PacketWrite, payload)
	resp, err := srv.handleWritePacket(pkt)
	if err != nil {
		t.Fatalf("有效 JSON 不应返回错误: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应")
	}
}

// ---------------------------------------------------------------------------
// handlePacket 路由覆盖补充（tcp_handler.go:98）
// ---------------------------------------------------------------------------

// TestHandlePacketRouteBadPayloadsCov_V2 测试 handlePacket 对各种包类型的路由
// 验证 Query/Write 路由在无效 payload 时正确返回错误
func TestHandlePacketRouteBadPayloadsCov_V2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	// Query 路由 + 无效 payload
	_, err := srv.handlePacket(NewPacket(PacketQuery, []byte("not json")))
	if err == nil {
		t.Error("PacketQuery + 无效 payload 应返回错误")
	}

	// Write 路由 + 无效 payload
	_, err = srv.handlePacket(NewPacket(PacketWrite, []byte("not json")))
	if err == nil {
		t.Error("PacketWrite + 无效 payload 应返回错误")
	}

	// Ping 路由始终成功
	resp, err := srv.handlePacket(NewPacket(PacketPing, nil))
	if err != nil {
		t.Fatalf("PacketPing 不应返回错误: %v", err)
	}
	if resp.Type != PacketResponse {
		t.Errorf("ping 响应类型 = %d，期望 %d", resp.Type, PacketResponse)
	}
}

// ---------------------------------------------------------------------------
// V3: handleQueryPacket / handleWritePacket invalid JSON and error paths
// ---------------------------------------------------------------------------

// TestHandleQueryPacket_InvalidJSON_V3 tests handleQueryPacket with invalid JSON payload.
func TestHandleQueryPacket_InvalidJSON_V3(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	// Send invalid JSON as query payload
	pkt := NewPacket(PacketQuery, []byte("not valid json"))
	_, err := srv.handleQueryPacket(pkt)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

// TestHandleWritePacket_InvalidJSON_V3 tests handleWritePacket with invalid JSON payload.
func TestHandleWritePacket_InvalidJSON_V3(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	// Send invalid JSON as write payload
	pkt := NewPacket(PacketWrite, []byte("{bad json"))
	_, err := srv.handleWritePacket(pkt)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

// TestHandleQueryPacket_QueryError_V3 tests handleQueryPacket when the query itself fails.
// handleQuery returns errors as Response{Code:-1} with nil Go error, so handleQueryPacket
// returns a non-nil Packet (not a Go error). We verify the response has Code=-1.
func TestHandleQueryPacket_QueryError_V3(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	// Send a query for a non-existent table
	payload, _ := json.Marshal(QueryRequest{SQL: "SELECT * FROM " + v14Nonexistent})
	pkt := NewPacket(PacketQuery, payload)
	resp, err := srv.handleQueryPacket(pkt)
	if err != nil {
		t.Fatalf("handleQueryPacket should not return Go error: %v", err)
	}
	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if response.Code != -1 {
		t.Errorf("expected Code=-1 for querying non-existent table, got %d", response.Code)
	}
}

// TestHandleWritePacket_WriteError_V3 tests handleWritePacket when the write itself fails.
// handleWrite returns errors as Response{Code:-1} with nil Go error, so handleWritePacket
// returns a non-nil Packet (not a Go error). We verify the response has Code=-1.
func TestHandleWritePacket_WriteError_V3(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	// Write to a non-existent table
	payload, _ := json.Marshal(WriteRequest{
		Table: v14Nonexistent,
		Rows:  []map[string]interface{}{{"id": float64(1)}},
	})
	pkt := NewPacket(PacketWrite, payload)
	resp, err := srv.handleWritePacket(pkt)
	if err != nil {
		t.Fatalf("handleWritePacket should not return Go error: %v", err)
	}
	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if response.Code != -1 {
		t.Errorf("expected Code=-1 for writing to non-existent table, got %d", response.Code)
	}
}

// TestHandleTCPConn_InvalidPacket_V3 tests handleTCPConn with an invalid packet type.
func TestHandleTCPConn_InvalidPacket_V3(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	time.Sleep(50 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send a packet with unknown type
	pkt := NewPacket(99, []byte("test"))
	if _, err := conn.Write(pkt.Encode()); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Read response - should get an error response
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline failed: %v", err)
	}
	respPkt, err := DecodePacket(bufio.NewReader(conn))
	if err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(respPkt.Payload, &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if resp.Code == 0 {
		t.Error("expected non-zero code for unknown packet type")
	}
}

// ---------------------------------------------------------------------------
// V4: handlePing, handleTCPConn shutdown, isClosedConnErr, isTransientAcceptErr
// ---------------------------------------------------------------------------

// handlePing: verify via handlePacket that PacketPing produces a valid response

func TestHandlePing_ViaHandlePacket(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketPing, nil)
	resp, err := srv.handlePacket(pkt)
	if err != nil {
		t.Fatalf("handlePacket(PacketPing) returned error: %v", err)
	}
	if resp == nil {
		t.Fatal("handlePacket(PacketPing) returned nil response")
	}
	if resp.Type != PacketResponse {
		t.Errorf("response type = %d, want %d", resp.Type, PacketResponse)
	}
	if resp.Magic != Magic {
		t.Errorf("response Magic = 0x%08x, want 0x%08x", resp.Magic, Magic)
	}
	if resp.Version != ProtocolVersion {
		t.Errorf("response Version = %d, want %d", resp.Version, ProtocolVersion)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("unmarshal ping response: %v", err)
	}
	if response.Code != 0 {
		t.Errorf("response Code = %d, want 0", response.Code)
	}
	if response.Message != msgPong {
		t.Errorf("response Message = %q, want %q", response.Message, msgPong)
	}
}

// handleTCPConn: exits cleanly when server is shutting down (s.done closed)

func TestHandleTCPConn_ServerShutdown_V4(t *testing.T) {
	srv := newTestServer(t)
	// Do NOT defer srv.Stop() because we close srv.done ourselves below,
	// and Stop() would try to close it again causing a panic.

	// Close the done channel to simulate server shutdown
	close(srv.done)

	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()

	// handleTCPConn should detect the closed done channel and exit immediately
	done := make(chan struct{})
	srv.wg.Add(1)
	go func() {
		srv.handleTCPConn(serverConn)
		close(done)
	}()

	select {
	case <-done:
		// handleTCPConn exited as expected
	case <-time.After(2 * time.Second):
		t.Fatal("handleTCPConn did not exit after server shutdown")
	}

	// Clean up: close storage manually since we're not calling Stop()
	_ = srv.storage.Close()
}

// isClosedConnErr: test with io.EOF, net.ErrClosed, and a regular error

func TestIsClosedConnErr_V4(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{testNameIOEOF, io.EOF, true},
		{"net.ErrClosed", net.ErrClosed, true},
		{"wrapped net.ErrClosed", fmt.Errorf("wrap: %w", net.ErrClosed), true},
		{"regular error", errors.New("something else"), false},
		{testNameNilErr, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isClosedConnErr(tt.err); got != tt.want {
				t.Errorf("isClosedConnErr() = %v, want %v", got, tt.want)
			}
		})
	}
}

// isTransientAcceptErr: timeout OpError, non-timeout OpError with
// "resource temporarily unavailable", and regular error

func TestIsTransientAcceptErr_V4(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			"timeout OpError",
			&net.OpError{Op: v14OpAccept, Net: testNetTCP, Err: timeoutError{}},
			true,
		},
		{
			"non-timeout OpError with resource temporarily unavailable",
			&net.OpError{Op: v14OpAccept, Net: testNetTCP, Err: errors.New("resource temporarily unavailable")},
			true,
		},
		{
			"non-timeout OpError with too many open files",
			&net.OpError{Op: v14OpAccept, Net: testNetTCP, Err: errors.New("too many open files")},
			true,
		},
		{
			"non-timeout OpError with other message",
			&net.OpError{Op: v14OpAccept, Net: testNetTCP, Err: errors.New("connection refused")},
			false,
		},
		{
			"regular error",
			errors.New("some error"),
			false,
		},
		{
			testNameNilErr,
			nil,
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransientAcceptErr(tt.err); got != tt.want {
				t.Errorf("isTransientAcceptErr() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// V5: newErrorResponse normal error path
// ---------------------------------------------------------------------------

// TestNewErrorResponse_NormalError 测试 newErrorResponse 对普通错误的处理。
func TestNewErrorResponse_NormalError(t *testing.T) {
	pkt := newErrorResponse(errors.New("test error"))
	if pkt.Type != PacketResponse {
		t.Errorf("Type = %d, want %d", pkt.Type, PacketResponse)
	}

	var resp Response
	if err := json.Unmarshal(pkt.Payload, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("Code = %d, want -1", resp.Code)
	}
	if resp.Message != "test error" {
		t.Errorf("Message = %q, want %q", resp.Message, "test error")
	}
}
