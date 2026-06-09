package server

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
	"time"
)

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
