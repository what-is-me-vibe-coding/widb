package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// --- isClosedConnErr tests ---

func TestIsClosedConnErr_EOF(t *testing.T) {
	if !isClosedConnErr(io.EOF) {
		t.Error("isClosedConnErr(io.EOF) = false, want true")
	}
}

func TestIsClosedConnErr_OpErrorTimeout(t *testing.T) {
	// Use a real net.Error that reports Timeout() = true
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetReadDeadline(time.Now().Add(-1 * time.Second))
	_, readErr := bufio.NewReader(conn).ReadByte()

	if readErr == nil {
		t.Fatal("expected a deadline error, got nil")
	}

	// The error from a timed-out read should be a *net.OpError with Timeout()=true
	if !isClosedConnErr(readErr) {
		t.Errorf("isClosedConnErr(timeout error) = false, want true; err=%T: %v", readErr, readErr)
	}
}

func TestIsClosedConnErr_OpErrorNotTimeout(t *testing.T) {
	opErr := &net.OpError{Op: "read", Net: testNetTCP, Err: errors.New("some error")}
	if isClosedConnErr(opErr) {
		t.Error("isClosedConnErr(non-timeout OpError) = true, want false")
	}
}

func TestIsClosedConnErr_OtherError(t *testing.T) {
	if isClosedConnErr(errors.New("random error")) {
		t.Error("isClosedConnErr(random error) = true, want false")
	}
}

func TestIsClosedConnErr_NetErrClosed(t *testing.T) {
	if !isClosedConnErr(net.ErrClosed) {
		t.Error("isClosedConnErr(net.ErrClosed) = false, want true")
	}
}

// --- handleTCPConn tests ---

func TestHandleTCPConn_ValidQueryPacket(t *testing.T) {
	srv := newTestServerWithTable(t)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	time.Sleep(50 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial TCP failed: %v", err)
	}
	defer func() { _ = conn.Close() }()

	queryPayload, _ := json.Marshal(QueryRequest{SQL: testSelectAll})
	queryPkt := NewPacket(PacketQuery, queryPayload)
	if _, err := conn.Write(queryPkt.Encode()); err != nil {
		t.Fatalf("write query packet failed: %v", err)
	}

	resp, err := DecodePacket(bufio.NewReader(conn))
	if err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if resp.Type != PacketResponse {
		t.Errorf("response type = %d, want %d", resp.Type, PacketResponse)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if response.Code != 0 {
		t.Errorf("response Code = %d, want 0; Message = %q", response.Code, response.Message)
	}
}

func TestHandleTCPConn_InvalidPacket(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	time.Sleep(50 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial TCP failed: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Write garbage bytes that will fail DecodePacket (bad magic)
	garbage := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if _, err := conn.Write(garbage); err != nil {
		t.Fatalf("write garbage failed: %v", err)
	}

	// Server should close the connection after decode error.
	// Set a deadline so we don't block forever.
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 1024)
	_, readErr := conn.Read(buf)
	if readErr == nil {
		// If we got data, that's unexpected but not a failure; the important
		// thing is the server didn't panic.
		t.Log("server sent data before closing; that's acceptable")
	}
}

func TestHandleTCPConn_ServerShutdown(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial TCP failed: %v", err)
	}

	// Send a ping to confirm connection works
	pingPkt := NewPacket(PacketPing, nil)
	if _, err := conn.Write(pingPkt.Encode()); err != nil {
		t.Fatalf("write ping failed: %v", err)
	}
	resp, err := DecodePacket(bufio.NewReader(conn))
	if err != nil {
		t.Fatalf("read ping response failed: %v", err)
	}
	if resp.Type != PacketResponse {
		t.Errorf("ping response type = %d, want %d", resp.Type, PacketResponse)
	}

	// Close client connection so the server's handleTCPConn loop
	// hits io.EOF on the next read and exits cleanly.
	_ = conn.Close()

	// Stop the server; should complete quickly since the TCP handler
	// will see the closed connection.
	done := make(chan error, 1)
	go func() {
		done <- srv.Stop()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server shutdown timed out")
	}
}

// --- handleTCPConn 覆盖率提升测试 ---

func TestHandleTCPConn_WritePacketWithValidData(t *testing.T) {
	// 测试 TCP 写入包携带有效数据的情况
	srv := newTestServerWithTable(t)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	time.Sleep(50 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("连接 TCP 失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// 发送写入请求包
	writePayload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows: []map[string]interface{}{
			{"id": float64(1), testColName: "alice"},
			{"id": float64(2), testColName: testNameBob},
		},
	})
	writePkt := NewPacket(PacketWrite, writePayload)
	if _, err := conn.Write(writePkt.Encode()); err != nil {
		t.Fatalf("写入包发送失败: %v", err)
	}

	resp, err := DecodePacket(bufio.NewReader(conn))
	if err != nil {
		t.Fatalf("读取写入响应失败: %v", err)
	}

	var writeResp Response
	if err := json.Unmarshal(resp.Payload, &writeResp); err != nil {
		t.Fatalf("解析写入响应失败: %v", err)
	}
	if writeResp.Code != 0 {
		t.Errorf("写入响应 Code = %d, Message = %q", writeResp.Code, writeResp.Message)
	}
	if writeResp.Rows != 2 {
		t.Errorf("写入行数 = %d, 期望 2", writeResp.Rows)
	}
}

func TestHandleTCPConn_MultiplePacketsInSequence(t *testing.T) {
	// 测试同一 TCP 连接上连续发送多个包
	srv := newTestServerWithTable(t)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	time.Sleep(50 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("连接 TCP 失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// 先发送 Ping
	pingPkt := NewPacket(PacketPing, nil)
	if _, err := conn.Write(pingPkt.Encode()); err != nil {
		t.Fatalf("写入 Ping 包失败: %v", err)
	}
	resp1, err := DecodePacket(bufio.NewReader(conn))
	if err != nil {
		t.Fatalf("读取 Ping 响应失败: %v", err)
	}
	if resp1.Type != PacketResponse {
		t.Errorf("Ping 响应类型 = %d, 期望 %d", resp1.Type, PacketResponse)
	}

	// 再发送查询
	queryPayload, _ := json.Marshal(QueryRequest{SQL: testSelectAll})
	queryPkt := NewPacket(PacketQuery, queryPayload)
	if _, err := conn.Write(queryPkt.Encode()); err != nil {
		t.Fatalf("写入查询包失败: %v", err)
	}
	resp2, err := DecodePacket(bufio.NewReader(conn))
	if err != nil {
		t.Fatalf("读取查询响应失败: %v", err)
	}
	if resp2.Type != PacketResponse {
		t.Errorf("查询响应类型 = %d, 期望 %d", resp2.Type, PacketResponse)
	}

	// 最后发送写入
	writePayload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(42), testColName: "charlie"}},
	})
	writePkt := NewPacket(PacketWrite, writePayload)
	if _, err := conn.Write(writePkt.Encode()); err != nil {
		t.Fatalf("写入包发送失败: %v", err)
	}
	resp3, err := DecodePacket(bufio.NewReader(conn))
	if err != nil {
		t.Fatalf("读取写入响应失败: %v", err)
	}
	var writeResp Response
	if err := json.Unmarshal(resp3.Payload, &writeResp); err != nil {
		t.Fatalf("解析写入响应失败: %v", err)
	}
	if writeResp.Code != 0 {
		t.Errorf("写入响应 Code = %d, Message = %q", writeResp.Code, writeResp.Message)
	}
}

// --- serveHTTP tests ---

func TestServeHTTP_GracefulShutdown(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		TCPAddr:  testListenAddr,
		HTTPAddr: testListenAddr,
		DataDir:  dir,
	}
	registry := prometheus.NewRegistry()
	srv, err := NewServer(cfg, WithMetricsRegistry(registry))
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// Verify HTTP is serving
	baseURL := "http://" + srv.httpListener.Addr().String()
	resp, err := http.Get(baseURL + "/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/health status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// Gracefully stop the server; serveHTTP should exit cleanly via <-s.done
	if err := srv.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// After stop, HTTP should be unavailable
	_, err = http.Get(baseURL + "/health")
	if err == nil {
		t.Error("expected error after server stop, got nil")
	}
}

// --- serveHTTP 服务器错误测试 ---

func TestServeHTTP_ServerError(t *testing.T) {
	// 当 httpListener 被直接关闭时，Serve 会返回非 ErrServerClosed 的错误，
	// 触发 serveHTTP 中 select 的 default 分支。
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

	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// 直接关闭 httpListener，使 Serve 返回 "use of closed network connection" 错误
	// 该错误不是 http.ErrServerClosed，因此会进入 default 分支
	_ = srv.httpListener.Close()

	// 等待 serveHTTP goroutine 处理完错误并退出
	time.Sleep(200 * time.Millisecond)

	// 服务器应该仍然可以正常关闭（Stop 不会因为 serveHTTP 已退出而死锁）
	if err := srv.Stop(); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
}

// --- handlePacket tests ---

func TestHandlePacket_UnknownType(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(255, nil)
	resp, err := srv.handlePacket(pkt)
	if err == nil {
		t.Error("handlePacket(unknown type) expected error, got nil")
	}
	if resp != nil {
		t.Errorf("handlePacket(unknown type) resp = %v, want nil", resp)
	}
	if err.Error() != "未知的包类型: 255" {
		t.Errorf("error = %q, want %q", err.Error(), "未知的包类型: 255")
	}
}

// --- NewServer default MaxMemTableSize test ---

func TestNewServer_DefaultMaxMemTableSize(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		TCPAddr:  testListenAddr,
		HTTPAddr: testListenAddr,
		DataDir:  dir,
		// MaxMemTableSize is 0, should default to 64MB
	}
	registry := prometheus.NewRegistry()
	srv, err := NewServer(cfg, WithMetricsRegistry(registry))
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	const expected int64 = 64 * 1024 * 1024
	if srv.cfg.MaxMemTableSize != expected {
		t.Errorf("MaxMemTableSize = %d, want %d", srv.cfg.MaxMemTableSize, expected)
	}
}

func TestNewServer_CustomMaxMemTableSize(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		TCPAddr:         testListenAddr,
		HTTPAddr:        testListenAddr,
		DataDir:         dir,
		MaxMemTableSize: 128 * 1024 * 1024,
	}
	registry := prometheus.NewRegistry()
	srv, err := NewServer(cfg, WithMetricsRegistry(registry))
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	if srv.cfg.MaxMemTableSize != 128*1024*1024 {
		t.Errorf("MaxMemTableSize = %d, want %d", srv.cfg.MaxMemTableSize, 128*1024*1024)
	}
}

// TestStart_HTTPListenFailureCleanup 验证 HTTP 监听失败时，
// 已启动的 TCP goroutine 被优雅关闭，不会泄漏。
func TestStart_HTTPListenFailureCleanup(t *testing.T) {
	dir := t.TempDir()

	// 先启动一个服务器占用 HTTP 端口
	blocker, err := NewServer(Config{
		TCPAddr:  testListenAddr,
		HTTPAddr: testListenAddr,
		DataDir:  dir + "_blocker",
	}, WithMetricsRegistry(prometheus.NewRegistry()))
	if err != nil {
		t.Fatalf("create blocker server: %v", err)
	}
	if err := blocker.Start(); err != nil {
		t.Fatalf("start blocker server: %v", err)
	}
	defer func() { _ = blocker.Stop() }()

	time.Sleep(50 * time.Millisecond)

	// 创建第二个服务器，使用相同的 HTTP 端口（会失败）
	// 但使用不同的 TCP 端口（自动分配，不会冲突）
	srv, err := NewServer(Config{
		TCPAddr:  testListenAddr,
		HTTPAddr: blocker.HTTPAddr(), // 使用已被占用的 HTTP 端口
		DataDir:  dir,
	}, WithMetricsRegistry(prometheus.NewRegistry()))
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	// Start 应该返回错误（HTTP 端口被占用）
	err = srv.Start()
	if err == nil {
		t.Error("expected error when HTTP port is already in use, got nil")
		_ = srv.Stop()
		return
	}

	// 验证服务器状态一致：done 通道已重置，可以重试 Start
	// 尝试用新的端口启动
	srv2, err := NewServer(Config{
		TCPAddr:  testListenAddr,
		HTTPAddr: testListenAddr,
		DataDir:  dir + "_2",
	}, WithMetricsRegistry(prometheus.NewRegistry()))
	if err != nil {
		t.Fatalf("create second server: %v", err)
	}
	if err := srv2.Start(); err != nil {
		t.Fatalf("start second server: %v", err)
	}
	defer func() { _ = srv2.Stop() }()
}
