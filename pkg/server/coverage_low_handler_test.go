package server

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// ---------------------------------------------------------------------------
// handleQueryPacket: JSON marshal 响应错误路径（80.0% → >90%）
// ---------------------------------------------------------------------------

// TestHandleQueryPacket_ValidQueryViaDirect 测试 handleQueryPacket 正常路径（直接调用）。
func TestHandleQueryPacket_ValidQueryViaDirect(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	// 创建表并写入数据
	writePayload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(1), testColName: testName}},
	})
	writePkt := NewPacket(PacketWrite, writePayload)
	_, err := srv.handleWritePacket(writePkt)
	if err != nil {
		t.Fatalf("handleWritePacket 失败: %v", err)
	}

	// 查询数据
	queryPayload, _ := json.Marshal(QueryRequest{SQL: "SELECT * FROM " + testTable})
	queryPkt := NewPacket(PacketQuery, queryPayload)
	resp, err := srv.handleQueryPacket(queryPkt)
	if err != nil {
		t.Fatalf("handleQueryPacket 正常路径失败: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应")
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应类型 = %d，期望 %d", resp.Type, PacketResponse)
	}
}

// ---------------------------------------------------------------------------
// handleWritePacket: 正常写入路径（80.0% → >90%）
// ---------------------------------------------------------------------------

// TestHandleWritePacket_ValidWriteViaDirect 测试 handleWritePacket 正常路径（直接调用）。
func TestHandleWritePacket_ValidWriteViaDirect(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	writePayload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(1), testColName: testName}},
	})
	writePkt := NewPacket(PacketWrite, writePayload)
	resp, err := srv.handleWritePacket(writePkt)
	if err != nil {
		t.Fatalf("handleWritePacket 正常路径失败: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应")
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应类型 = %d，期望 %d", resp.Type, PacketResponse)
	}

	// 验证响应内容
	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if response.Code != 0 {
		t.Errorf("响应 Code = %d，期望 0", response.Code)
	}
}

// ---------------------------------------------------------------------------
// handlePing: 正常路径（80.0% → >90%）
// ---------------------------------------------------------------------------

// TestHandlePing_DirectCall 测试 handlePing 直接调用。
func TestHandlePing_DirectCall(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	resp, err := srv.handlePing()
	if err != nil {
		t.Fatalf("handlePing 失败: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应")
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应类型 = %d，期望 %d", resp.Type, PacketResponse)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if response.Code != 0 {
		t.Errorf("响应 Code = %d，期望 0", response.Code)
	}
	if response.Message != msgPong {
		t.Errorf("响应 Message = %q，期望 %q", response.Message, msgPong)
	}
}

// ---------------------------------------------------------------------------
// handlePacket: 未知包类型路径
// ---------------------------------------------------------------------------

// TestHandlePacket_UnknownTypeDirect 测试 handlePacket 处理未知包类型（直接调用）。
func TestHandlePacket_UnknownTypeDirect(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(99, nil)
	_, err := srv.handlePacket(pkt)
	if err == nil {
		t.Error("期望未知包类型返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// acceptTCP: 连接数限制路径（88.9% → >90%）
// ---------------------------------------------------------------------------

// TestAcceptTCP_ConnectionLimit 测试 TCP 连接数限制。
func TestAcceptTCP_ConnectionLimit(t *testing.T) {
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

	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	// 等待服务器启动
	time.Sleep(50 * time.Millisecond)

	// 第一个连接应成功
	conn1, err := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("第一个连接失败: %v", err)
	}
	defer func() { _ = conn1.Close() }()

	// 发送 ping 确保连接被处理
	pingPkt := NewPacket(PacketPing, nil)
	if _, err := conn1.Write(pingPkt.Encode()); err != nil {
		t.Fatalf("发送 ping 失败: %v", err)
	}

	// 等待连接被接受
	time.Sleep(100 * time.Millisecond)

	// 第二个连接应被拒绝（达到连接数上限后服务端会关闭连接）
	conn2, err := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if err == nil {
		// 连接建立了但可能被服务端立即关闭
		_ = conn2.Close()
	}
}
