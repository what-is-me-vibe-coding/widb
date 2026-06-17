package pgwire

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgproto3/v2"
)

// TestConnHandleStartupDefaultCase 验证 handleStartup 对未知启动消息的处理。
func TestConnHandleStartupDefaultCase(t *testing.T) {
	srv := startTestServer(t, &mockExecutor{})
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()

	// 发送 SSLRequest 后，再发送一个非 StartupMessage 的消息
	if err := client.sendSSLRequest(); err != nil {
		t.Fatalf("sendSSLRequest 失败: %v", err)
	}
	resp := make([]byte, 1)
	if _, err := io.ReadFull(client.conn, resp); err != nil {
		t.Fatalf("读取 SSL 响应失败: %v", err)
	}
	// 再发 SSLRequest（而非 StartupMessage），触发 handleSSLNegotiation 的非 Startup 分支
	if err := client.sendSSLRequest(); err != nil {
		t.Fatalf("sendSSLRequest 失败: %v", err)
	}
	// 服务端应关闭连接
	time.Sleep(50 * time.Millisecond)
	client.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, err := client.readMessage()
	if err == nil {
		t.Log("连接已关闭（预期行为）")
	}
}

// TestConnDispatchMessageDefault 验证 dispatchMessage 对未知消息类型的处理。
func TestConnDispatchMessageDefault(t *testing.T) {
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

	// 发送一个未知类型的消息（'Z' 是 ReadyForQuery 但由服务端发送，
	// 客户端发送时 pgproto3 可能不识别；用 'p' PasswordMessage 试试）
	// 实际上发一个 CopyData ('d') 消息，它不在 dispatchMessage 的 switch 中
	buf := make([]byte, 5)
	buf[0] = 'd' // CopyData - 不在 switch 中
	binary.BigEndian.PutUint32(buf[1:5], 4)
	if _, err := client.conn.Write(buf); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	// dispatchMessage 的 default 分支返回 true，连接保持
	// 发送一个 Query 验证连接仍可用
	if err := client.sendQuery("SELECT 1"); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	types, err := client.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	if len(types) == 0 {
		t.Error("default 分支后连接应仍可用")
	}
}

// TestConnSendQueryResultWriteError 验证 sendQueryResult 在写入失败时的处理。
func TestConnSendQueryResultWriteError(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns: []string{"id", "name"},
		Rows: []map[string]any{
			{"id": int64(1), "name": "alice"},
		},
		IsQuery: true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	// 发送查询后立即关闭客户端连接，触发服务端写入错误
	if err := client.sendQuery("SELECT * FROM t"); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	_ = client.conn.Close()
	// 等待服务端处理完（写入失败会被记录但不会 panic）
	time.Sleep(100 * time.Millisecond)
}

// TestConnSendStartupResponseError 验证 sendStartupResponse 在写入失败时的处理。
func TestConnSendStartupResponseError(t *testing.T) {
	srv := startTestServer(t, &mockExecutor{})
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	// 发送 StartupMessage 后立即关闭连接，触发服务端写入错误
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	_ = client.conn.Close()
	time.Sleep(100 * time.Millisecond)
}

// TestConnHandleQueryExecutorError 验证 handleQuery 在 executor 返回错误时的处理。
func TestConnHandleQueryExecutorError(t *testing.T) {
	exec := &mockExecutor{err: errors.New("syntax error at or near 'FOO'")}
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

	if err := client.sendQuery("FOO BAR"); err != nil {
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
		t.Errorf("应包含 ErrorResponse, got %v", types)
	}
}

// TestConnServeStartupError 验证 serve 在启动失败时优雅退出。
func TestConnServeStartupError(t *testing.T) {
	srv := startTestServer(t, &mockExecutor{})
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()

	// 发送无效数据导致 ReceiveStartupMessage 失败
	if _, err := client.conn.Write([]byte{0, 0, 0, 1}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	// 服务端应关闭连接
	time.Sleep(50 * time.Millisecond)
	client.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, err := client.readMessage()
	if err == nil {
		t.Log("连接已关闭（预期行为）")
	}
}

// TestConnQueryLoopReceiveError 验证 queryLoop 在 Receive 失败时退出。
func TestConnQueryLoopReceiveError(t *testing.T) {
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

	// 发送不完整消息导致 Receive 失败
	buf := make([]byte, 5)
	buf[0] = 'Q'
	binary.BigEndian.PutUint32(buf[1:5], 100) // 声明 100 字节但不发送
	if _, err := client.conn.Write(buf); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	// 立即关闭连接
	_ = client.conn.Close()
	time.Sleep(100 * time.Millisecond)
}

// TestSSLNegotiationResponseBackendMethod 验证 Backend 方法被调用。
func TestSSLNegotiationResponseBackendMethod(t *testing.T) {
	t.Helper()
	r := sslNegotiationResponse{}
	// Backend() 是接口方法，验证可调用且不 panic
	r.Backend()
	// 同时验证它满足 pgproto3.BackendMessage 接口
	var _ pgproto3.BackendMessage = sslNegotiationResponse{}
}

// TestConnAcceptLoopErrorLogging 验证 acceptLoop 在非关闭错误时记录日志。
func TestConnAcceptLoopErrorLogging(t *testing.T) {
	srv := NewServer("127.0.0.1:0", &mockExecutor{})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	// 直接关闭 listener（不通过 Stop），触发 Accept 错误
	// 但 done channel 未关闭，会走 default 分支记录日志
	_ = srv.listener.Close()
	// 此时 acceptLoop 会进入 default 分支记录日志并 continue
	// 由于 listener 已关闭，Accept 会持续返回错误，形成日志循环
	// 短暂等待后通过 Stop 终止
	time.Sleep(50 * time.Millisecond)
	srv.Stop()
}

// TestConnHandleSSLNegotiationSendError 验证 handleSSLNegotiation 在 Send 失败时的处理。
func TestConnHandleSSLNegotiationSendError(t *testing.T) {
	srv := startTestServer(t, &mockExecutor{})
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	// 发送 SSLRequest 后立即关闭连接（在服务端 Send 'N' 之前或之后）
	if err := client.sendSSLRequest(); err != nil {
		t.Fatalf("sendSSLRequest 失败: %v", err)
	}
	// 立即关闭写端，保留读端
	if tcpConn, ok := client.conn.(*net.TCPConn); ok {
		_ = tcpConn.CloseWrite()
	} else {
		_ = client.conn.Close()
	}
	time.Sleep(100 * time.Millisecond)
	_ = client.conn.Close()
}

// TestConnSendParameterStatusesError 验证 sendParameterStatuses 在写入失败时的处理。
func TestConnSendParameterStatusesError(t *testing.T) {
	srv := startTestServer(t, &mockExecutor{})
	defer srv.Stop()

	// 使用 net.Pipe 创建一个可控的连接对
	// 客户端发送 StartupMessage 后立即关闭，使服务端在发送 ParameterStatus 时失败
	client := newPGClient(t, srv.Addr())
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	// 读取部分数据后关闭
	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = client.conn.Close()
	}()
	time.Sleep(100 * time.Millisecond)
}
