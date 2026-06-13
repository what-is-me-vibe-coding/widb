package server

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// --- handleQueryPacket with invalid JSON payload (additional coverage) ---

// TestHandleQueryPacket_EmptyPayload_V2 tests handleQueryPacket with empty payload.
func TestHandleQueryPacket_EmptyPayload_V2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketQuery, []byte{})
	_, err := srv.handleQueryPacket(pkt)
	if err == nil {
		t.Error("expected error for empty payload, got nil")
	}
}

// TestHandleQueryPacket_NilPayload_V2 tests handleQueryPacket with nil payload.
func TestHandleQueryPacket_NilPayload_V2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketQuery, nil)
	_, err := srv.handleQueryPacket(pkt)
	if err == nil {
		t.Error("expected error for nil payload, got nil")
	}
}

// --- handleWritePacket with invalid JSON payload (additional coverage) ---

// TestHandleWritePacket_EmptyPayload_V2 tests handleWritePacket with empty payload.
func TestHandleWritePacket_EmptyPayload_V2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketWrite, []byte{})
	_, err := srv.handleWritePacket(pkt)
	if err == nil {
		t.Error("expected error for empty payload, got nil")
	}
}

// TestHandleWritePacket_NilPayload_V2 tests handleWritePacket with nil payload.
func TestHandleWritePacket_NilPayload_V2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketWrite, nil)
	_, err := srv.handleWritePacket(pkt)
	if err == nil {
		t.Error("expected error for nil payload, got nil")
	}
}

// --- handlePing normal path ---

// TestHandlePing_NormalPath_V2 tests handlePing returns correct response.
func TestHandlePing_NormalPath_V2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	resp, err := srv.handlePing()
	if err != nil {
		t.Fatalf("handlePing: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Type != PacketResponse {
		t.Errorf("response type = %d, want %d", resp.Type, PacketResponse)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if response.Code != 0 {
		t.Errorf("Code = %d, want 0", response.Code)
	}
	if response.Message != msgPong {
		t.Errorf("Message = %q, want %q", response.Message, msgPong)
	}
}

// --- handlePacket with unknown packet type ---

// TestHandlePacket_UnknownType_V2 tests handlePacket with an unknown packet type.
func TestHandlePacket_UnknownType_V2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(255, nil)
	_, err := srv.handlePacket(pkt)
	if err == nil {
		t.Error("expected error for unknown packet type, got nil")
	}
}

// --- acceptTCP connection limit test ---

// TestAcceptTCP_ConnectionLimit_V2 tests that connections are rejected when
// the connection limit is reached.
func TestAcceptTCP_ConnectionLimit_V2(t *testing.T) {
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
		t.Fatalf("NewServer: %v", err)
	}

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	time.Sleep(50 * time.Millisecond)

	// First connection should succeed
	conn1, err := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("first connection failed: %v", err)
	}
	defer func() { _ = conn1.Close() }()

	// Send a ping to make sure the first connection is handled
	pingPkt := NewPacket(PacketPing, nil)
	if _, err := conn1.Write(pingPkt.Encode()); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	// Read the response
	if err := conn1.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 1024)
	_, _ = conn1.Read(buf)

	// Second connection should be rejected (server closes it immediately)
	conn2, err := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("second connection dial failed: %v", err)
	}
	defer func() { _ = conn2.Close() }()

	// The second connection should be closed by the server, so reads should fail
	if err := conn2.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	_, _ = conn2.Read(buf)
}
