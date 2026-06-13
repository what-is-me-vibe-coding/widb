package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

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
