package server

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
	"time"
)

// --- handleTCPConn: handlePacket error response path ---

func TestHandleTCPConn_HandlePacketError(t *testing.T) {
	srv := newTestServer(t) // no table created
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

	// Send a PacketWrite with invalid payload (not valid WriteRequest JSON).
	// This will cause handlePacket -> handleWritePacket to return an error
	// (json unmarshal failure), which triggers the error response path in
	// handleTCPConn (Code: -1).
	invalidWritePkt := NewPacket(PacketWrite, []byte("not json"))
	if _, err := conn.Write(invalidWritePkt.Encode()); err != nil {
		t.Fatalf("write invalid write packet failed: %v", err)
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
	if response.Code != -1 {
		t.Errorf("response Code = %d, want -1; Message = %q", response.Code, response.Message)
	}
}

// --- handleTCPConn: write then query round-trip ---

func TestHandleTCPConn_WriteThenQuery(t *testing.T) {
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

	// Write a row
	writePayload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows: []map[string]interface{}{
			{"id": float64(1), testColName: testName, "score": 88.0},
		},
	})
	writePkt := NewPacket(PacketWrite, writePayload)
	if _, err := conn.Write(writePkt.Encode()); err != nil {
		t.Fatalf("write packet failed: %v", err)
	}

	writeResp, err := DecodePacket(bufio.NewReader(conn))
	if err != nil {
		t.Fatalf("decode write response failed: %v", err)
	}
	var wr Response
	if err := json.Unmarshal(writeResp.Payload, &wr); err != nil {
		t.Fatalf("unmarshal write response failed: %v", err)
	}
	if wr.Code != 0 {
		t.Fatalf("write Code = %d, want 0; Message = %q", wr.Code, wr.Message)
	}

	// Query the data back
	queryPayload, _ := json.Marshal(QueryRequest{SQL: testSelectAll})
	queryPkt := NewPacket(PacketQuery, queryPayload)
	if _, err := conn.Write(queryPkt.Encode()); err != nil {
		t.Fatalf("write query packet failed: %v", err)
	}

	queryResp, err := DecodePacket(bufio.NewReader(conn))
	if err != nil {
		t.Fatalf("decode query response failed: %v", err)
	}
	var qr Response
	if err := json.Unmarshal(queryResp.Payload, &qr); err != nil {
		t.Fatalf("unmarshal query response failed: %v", err)
	}
	if qr.Code != 0 {
		t.Errorf("query Code = %d, want 0; Message = %q", qr.Code, qr.Message)
	}
}

// --- handleTCPConn: ping then query on same connection ---

func TestHandleTCPConn_PingThenQuery(t *testing.T) {
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

	// Send ping first
	pingPkt := NewPacket(PacketPing, nil)
	if _, err := conn.Write(pingPkt.Encode()); err != nil {
		t.Fatalf("write ping failed: %v", err)
	}
	pingResp, err := DecodePacket(bufio.NewReader(conn))
	if err != nil {
		t.Fatalf("decode ping response failed: %v", err)
	}
	var pr Response
	if err := json.Unmarshal(pingResp.Payload, &pr); err != nil {
		t.Fatalf("unmarshal ping response failed: %v", err)
	}
	if pr.Code != 0 || pr.Message != msgPong {
		t.Errorf("ping response Code=%d Message=%q, want Code=0 Message='pong'", pr.Code, pr.Message)
	}

	// Then send a query on the same connection
	queryPayload, _ := json.Marshal(QueryRequest{SQL: testSelectAll})
	queryPkt := NewPacket(PacketQuery, queryPayload)
	if _, err := conn.Write(queryPkt.Encode()); err != nil {
		t.Fatalf("write query packet failed: %v", err)
	}
	queryResp, err := DecodePacket(bufio.NewReader(conn))
	if err != nil {
		t.Fatalf("decode query response failed: %v", err)
	}
	var qr Response
	if err := json.Unmarshal(queryResp.Payload, &qr); err != nil {
		t.Fatalf("unmarshal query response failed: %v", err)
	}
	if qr.Code != 0 {
		t.Errorf("query Code = %d, want 0; Message = %q", qr.Code, qr.Message)
	}
}

// --- handleTCPConn: write to non-existent table returns error response ---

func TestHandleTCPConn_WriteToNonExistentTable(t *testing.T) {
	srv := newTestServer(t) // no table
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

	// Send a valid WriteRequest JSON but for a table that doesn't exist.
	// handleWrite will return an error (table not found), which triggers
	// the error response path in handleTCPConn.
	writePayload, _ := json.Marshal(WriteRequest{
		Table: "nonexistent", //nolint:goconst
		Rows: []map[string]interface{}{
			{"id": float64(1), "name": testTableName},
		},
	})
	writePkt := NewPacket(PacketWrite, writePayload)
	if _, err := conn.Write(writePkt.Encode()); err != nil {
		t.Fatalf("write packet failed: %v", err)
	}

	resp, err := DecodePacket(bufio.NewReader(conn))
	if err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if response.Code != -1 {
		t.Errorf("response Code = %d, want -1; Message = %q", response.Code, response.Message)
	}
}
