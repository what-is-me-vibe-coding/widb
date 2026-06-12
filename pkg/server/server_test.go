package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// --- 服务器创建与启停测试 ---

func newTestServer(t *testing.T) *Server {
	t.Helper()

	dir, err := os.MkdirTemp("", "testdb-server-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

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

	return srv
}

func newTestServerWithTable(t *testing.T) *Server {
	t.Helper()

	srv := newTestServer(t)

	err := srv.catalog.CreateTable(testTable, []catalog.ColumnDef{
		{Name: "id", Type: common.TypeInt64, Nullable: false},
		{Name: testColName, Type: common.TypeString, Nullable: true},
		{Name: testColScore, Type: common.TypeFloat64, Nullable: true},
	}, []string{"id"}, catalog.TableOptions{})
	if err != nil {
		t.Fatalf("CreateTable 失败: %v", err)
	}

	return srv
}

func TestNewServer(t *testing.T) {
	srv := newTestServer(t)
	if srv == nil {
		t.Fatal("NewServer 返回 nil")
	}
	if srv.storage == nil {
		t.Error("storage 不应为 nil")
	}
	if srv.catalog == nil {
		t.Error("catalog 不应为 nil")
	}
	if srv.parser == nil {
		t.Error("parser 不应为 nil")
	}
	if srv.executor == nil {
		t.Error("executor 不应为 nil")
	}

	if err := srv.Stop(); err != nil {
		t.Logf("Stop 错误: %v", err)
	}
}

func TestServerStartStop(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if err := srv.Stop(); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
}

func TestServerGracefulShutdown(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	done := make(chan error, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		done <- srv.Stop()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop 失败: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("优雅关闭超时")
	}
}

// --- TCP 连接测试 ---

func TestTCPPing(t *testing.T) {
	srv := newTestServer(t)
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

	pingPkt := NewPacket(PacketPing, nil)
	if _, err := conn.Write(pingPkt.Encode()); err != nil {
		t.Fatalf("写入 Ping 包失败: %v", err)
	}

	resp, err := DecodePacket(bufio.NewReader(conn))
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	if resp.Type != PacketResponse {
		t.Errorf("响应类型 = %d, 期望 %d", resp.Type, PacketResponse)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if response.Code != 0 {
		t.Errorf("响应 Code = %d, 期望 0", response.Code)
	}
	if response.Message != msgPong {
		t.Errorf("响应 Message = %q, 期望 'pong'", response.Message)
	}
}

func TestTCPUnknownPacketType(t *testing.T) {
	srv := newTestServer(t)
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

	pkt := NewPacket(99, nil)
	if _, err := conn.Write(pkt.Encode()); err != nil {
		t.Fatalf("写入包失败: %v", err)
	}

	resp, err := DecodePacket(bufio.NewReader(conn))
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if response.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", response.Code)
	}
}

func TestTCPQueryPacket(t *testing.T) {
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

	queryPayload, _ := json.Marshal(QueryRequest{SQL: testSelectAll})
	queryPkt := NewPacket(PacketQuery, queryPayload)
	if _, err := conn.Write(queryPkt.Encode()); err != nil {
		t.Fatalf("写入查询包失败: %v", err)
	}

	resp, err := DecodePacket(bufio.NewReader(conn))
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应类型 = %d, 期望 %d", resp.Type, PacketResponse)
	}
}

func TestTCPInvalidPayloads(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	time.Sleep(50 * time.Millisecond)

	tests := []struct {
		name    string
		pktType uint8
	}{
		{"无效查询JSON", PacketQuery},
		{"无效写入JSON", PacketWrite},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
			if err != nil {
				t.Fatalf("连接 TCP 失败: %v", err)
			}
			defer func() { _ = conn.Close() }()

			invalidPkt := NewPacket(tt.pktType, []byte("not json"))
			if _, err := conn.Write(invalidPkt.Encode()); err != nil {
				t.Fatalf("写入包失败: %v", err)
			}

			resp, err := DecodePacket(bufio.NewReader(conn))
			if err != nil {
				t.Fatalf("读取响应失败: %v", err)
			}

			var response Response
			if err := json.Unmarshal(resp.Payload, &response); err != nil {
				t.Fatalf("解析响应失败: %v", err)
			}
			if response.Code != -1 {
				t.Errorf("响应 Code = %d, 期望 -1", response.Code)
			}
		})
	}
}

func TestTCPWriteAndQuery(t *testing.T) {
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

	writePayload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(1), testColName: testTableName}},
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
}

// --- HTTP 集成测试 ---

func TestHTTPIntegration(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	time.Sleep(50 * time.Millisecond)
	baseURL := fmt.Sprintf("http://%s", srv.httpListener.Addr())

	resp, err := http.Get(baseURL + "/health")
	if err != nil {
		t.Fatalf("请求 /health 失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/health 状态码 = %d, 期望 %d", resp.StatusCode, http.StatusOK)
	}

	resp2, err := http.Get(baseURL + "/metrics")
	if err != nil {
		t.Fatalf("请求 /metrics 失败: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("/metrics 状态码 = %d, 期望 %d", resp2.StatusCode, http.StatusOK)
	}
}

// TestHandlePacketUnknownType 测试未知包类型的错误处理。
func TestHandlePacketUnknownType(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(99, nil)
	_, err := srv.handlePacket(pkt)
	if err == nil {
		t.Error("expected error for unknown packet type")
	}
}

// TestHandleQueryPacketInvalidJSON 测试查询包无效 JSON 的错误处理。
func TestHandleQueryPacketInvalidJSON(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketQuery, []byte("not json"))
	_, err := srv.handleQueryPacket(pkt)
	if err == nil {
		t.Error("expected error for invalid JSON in query packet")
	}
}

// TestHandleWritePacketInvalidJSON 测试写入包无效 JSON 的错误处理。
func TestHandleWritePacketInvalidJSON(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketWrite, []byte("not json"))
	_, err := srv.handleWritePacket(pkt)
	if err == nil {
		t.Error("expected error for invalid JSON in write packet")
	}
}

// TestHandlePingPacket 测试 Ping 包的正常处理。
func TestHandlePingPacket(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	resp, err := srv.handlePing()
	if err != nil {
		t.Fatalf("handlePing failed: %v", err)
	}
	if resp.Type != PacketResponse {
		t.Errorf("response type = %d, want %d", resp.Type, PacketResponse)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("unmarshal ping response: %v", err)
	}
	if response.Code != 0 {
		t.Errorf("response code = %d, want 0", response.Code)
	}
	if response.Message != msgPong {
		t.Errorf("response message = %q, want %q", response.Message, msgPong)
	}
}

// TestHandleQueryPacketValid 测试查询包的正常处理。
func TestHandleQueryPacketValid(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	payload, _ := json.Marshal(QueryRequest{SQL: testSelectAll})
	pkt := NewPacket(PacketQuery, payload)
	resp, err := srv.handleQueryPacket(pkt)
	if err != nil {
		t.Fatalf("handleQueryPacket failed: %v", err)
	}
	if resp.Type != PacketResponse {
		t.Errorf("response type = %d, want %d", resp.Type, PacketResponse)
	}
}

// TestHandleWritePacketValid 测试写入包的正常处理。
func TestHandleWritePacketValid(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	writePayload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(1), testColName: testTableName}},
	})
	pkt := NewPacket(PacketWrite, writePayload)
	resp, err := srv.handleWritePacket(pkt)
	if err != nil {
		t.Fatalf("handleWritePacket failed: %v", err)
	}
	if resp.Type != PacketResponse {
		t.Errorf("response type = %d, want %d", resp.Type, PacketResponse)
	}
}

// TestServerStopDoubleCall 验证多次调用 Stop() 不会 panic。
// 修复前双重调用可能因重复关闭 channel 而崩溃。
func TestServerStopDoubleCall(t *testing.T) {
	srv := newTestServer(t)

	// 第一次 Stop
	if err := srv.Stop(); err != nil {
		t.Fatalf("第一次 Stop 失败: %v", err)
	}

	// 第二次 Stop 不应 panic
	if err := srv.Stop(); err != nil {
		t.Fatalf("第二次 Stop 不应返回错误: %v", err)
	}

	// 第三次 Stop 也不应 panic
	if err := srv.Stop(); err != nil {
		t.Fatalf("第三次 Stop 不应返回错误: %v", err)
	}
}

// TestServerStopDoubleCallAfterStart 验证启动后多次调用 Stop() 不会 panic。
func TestServerStopDoubleCallAfterStart(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// 第一次 Stop
	if err := srv.Stop(); err != nil {
		t.Fatalf("第一次 Stop 失败: %v", err)
	}

	// 第二次 Stop 不应 panic
	if err := srv.Stop(); err != nil {
		t.Fatalf("第二次 Stop 不应返回错误: %v", err)
	}
}
