package server

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
	"time"
)

// --- handleQueryPacket: JSON unmarshal error via TCP with empty payload ---

func TestHandleQueryPacket_EmptyPayloadViaTCP(t *testing.T) {
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

	// 发送空 payload 的 Query 包，触发 json.Unmarshal 错误
	pkt := NewPacket(PacketQuery, []byte{})
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

// --- handleWritePacket: JSON unmarshal error via TCP with empty payload ---

func TestHandleWritePacket_EmptyPayloadViaTCP(t *testing.T) {
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

	// 发送空 payload 的 Write 包，触发 json.Unmarshal 错误
	pkt := NewPacket(PacketWrite, []byte{})
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

// --- handleQueryPacket: valid JSON but empty SQL via TCP ---

func TestHandleQueryPacket_EmptySQLViaTCP(t *testing.T) {
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

	// 发送合法 JSON 但 SQL 为空的 Query 包
	// json.Unmarshal 成功，但 handleQuery 在解析 SQL 时失败
	queryPayload, _ := json.Marshal(QueryRequest{SQL: ""})
	queryPkt := NewPacket(PacketQuery, queryPayload)
	if _, err := conn.Write(queryPkt.Encode()); err != nil {
		t.Fatalf("写入查询包失败: %v", err)
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
		t.Errorf("响应 Code = %d, 期望 -1 (空 SQL 应解析失败)", response.Code)
	}
}

// --- handleWritePacket: valid JSON but nonexistent table via TCP ---

func TestHandleWritePacket_NonexistentTableViaTCP(t *testing.T) {
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

	// 发送合法 JSON 但表不存在的 Write 包
	// json.Unmarshal 成功，但 handleWrite 在查找表时失败
	writePayload, _ := json.Marshal(WriteRequest{
		Table: "nonexistent", //nolint:goconst
		Rows:  []map[string]interface{}{{"id": float64(1)}},
	})
	writePkt := NewPacket(PacketWrite, writePayload)
	if _, err := conn.Write(writePkt.Encode()); err != nil {
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
		t.Errorf("响应 Code = %d, 期望 -1 (不存在的表应返回错误)", response.Code)
	}
}

// --- handlePing: thorough normal path test via TCP ---

func TestHandlePing_ThoroughViaTCP(t *testing.T) {
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

	// 验证响应包类型
	if resp.Type != PacketResponse {
		t.Errorf("响应类型 = %d, 期望 %d", resp.Type, PacketResponse)
	}

	// 验证响应包的 Magic 和 Version
	if resp.Magic != Magic {
		t.Errorf("响应 Magic = 0x%08x, 期望 0x%08x", resp.Magic, Magic)
	}
	if resp.Version != ProtocolVersion {
		t.Errorf("响应 Version = %d, 期望 %d", resp.Version, ProtocolVersion)
	}

	// 验证响应 payload 内容
	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if response.Code != 0 {
		t.Errorf("响应 Code = %d, 期望 0", response.Code)
	}
	if response.Message != msgPong {
		t.Errorf("响应 Message = %q, 期望 %q", response.Message, msgPong)
	}
	if response.Rows != 0 {
		t.Errorf("响应 Rows = %d, 期望 0", response.Rows)
	}
}

// --- handlePacket: unknown packet type 0 via TCP ---

func TestHandlePacket_UnknownTypeZeroViaTCP(t *testing.T) {
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

	// 发送类型为 0 的未知包
	pkt := NewPacket(0, nil)
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

// --- handlePacket: unknown packet type 255 via TCP ---

func TestHandlePacket_UnknownTypeMaxViaTCP(t *testing.T) {
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

	// 发送类型为 255 的未知包
	pkt := NewPacket(255, nil)
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

// --- handleQueryPacket: incomplete JSON via TCP ---

func TestHandleQueryPacket_IncompleteJSONViaTCP(t *testing.T) {
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

	// 发送不完整 JSON 的 Query 包
	pkt := NewPacket(PacketQuery, []byte("{"))
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

// --- handleWritePacket: incomplete JSON via TCP ---

func TestHandleWritePacket_IncompleteJSONViaTCP(t *testing.T) {
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

	// 发送不完整 JSON 的 Write 包
	pkt := NewPacket(PacketWrite, []byte("["))
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
