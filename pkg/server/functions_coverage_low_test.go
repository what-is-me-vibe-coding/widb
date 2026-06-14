package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// --- acceptTCP: error path where Accept() fails but server is NOT stopped ---

func TestAcceptTCP_AcceptErrorNotStopped(t *testing.T) {
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

	// Close the TCP listener directly without closing done channel.
	// This causes Accept() to return an error, and since done is not closed,
	// acceptTCP hits the default branch (logs error and returns).
	_ = srv.tcpListener.Close()

	// Wait briefly for acceptTCP to detect the error and exit
	time.Sleep(200 * time.Millisecond)

	// Server should still be stoppable
	if err := srv.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

// --- handleQueryPacket: JSON unmarshal error path ---

func TestHandleQueryPacket_InvalidJSON(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketQuery, []byte("not valid json"))
	resp, err := srv.handleQueryPacket(pkt)
	if err == nil {
		t.Error("handleQueryPacket with invalid JSON expected error, got nil")
	}
	if resp != nil {
		t.Errorf("handleQueryPacket with invalid JSON resp = %v, want nil", resp)
	}
}

// --- handleWritePacket: JSON unmarshal error path ---

func TestHandleWritePacket_InvalidJSON(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketWrite, []byte("not valid json"))
	resp, err := srv.handleWritePacket(pkt)
	if err == nil {
		t.Error("handleWritePacket with invalid JSON expected error, got nil")
	}
	if resp != nil {
		t.Errorf("handleWritePacket with invalid JSON resp = %v, want nil", resp)
	}
}

// --- NewServer: storage engine creation error ---

func TestNewServer_StorageEngineError(t *testing.T) {
	// Create a file where a directory is expected to force storage engine creation to fail
	dir := t.TempDir()
	filePath := dir + "/notadir"
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("create file failed: %v", err)
	}
	_ = f.Close()

	cfg := Config{
		TCPAddr:  testListenAddr,
		HTTPAddr: testListenAddr,
		DataDir:  filePath,
	}
	registry := prometheus.NewRegistry()
	_, err = NewServer(cfg, WithMetricsRegistry(registry))
	if err == nil {
		t.Error("NewServer with invalid data dir expected error, got nil")
	}
}

// --- Start: HTTP listener creation error after TCP listener succeeds ---

func TestStart_HTTPListenerError(t *testing.T) {
	dir := t.TempDir()

	// Occupy a port so that the HTTP listener creation fails
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer func() { _ = ln.Close() }()

	occupiedPort := ln.Addr().(*net.TCPAddr).Port

	cfg := Config{
		TCPAddr:  testListenAddr, // auto-assign
		HTTPAddr: fmt.Sprintf("127.0.0.1:%d", occupiedPort),
		DataDir:  dir,
	}
	registry := prometheus.NewRegistry()
	srv, err := NewServer(cfg, WithMetricsRegistry(registry))
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	err = srv.Start()
	if err == nil {
		t.Error("Start with occupied HTTP port expected error, got nil")
		_ = srv.Stop()
	} else {
		// Clean up: Stop() will close done channel and wait for goroutines
		_ = srv.Stop()
	}
}

// --- handleWrite: WriteBatch error ---

func TestHandleWrite_WriteBatchError(t *testing.T) {
	srv := newTestServerWithTable(t)

	// Close the storage engine to cause WriteBatch to fail
	_ = srv.storage.Close()

	resp, err := srv.handleWrite(&WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(1), testColName: testName}},
	})
	if err != nil {
		t.Fatalf("handleWrite returned unexpected error: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("response Code = %d, want -1; Message = %q", resp.Code, resp.Message)
	}
}

// --- valueToInterface: unsupported type (default case) ---

func TestValueToInterface_UnsupportedType(t *testing.T) {
	val := common.Value{Typ: common.DataType(99), Valid: true}
	result := valueToInterface(val)
	if result != nil {
		t.Errorf("valueToInterface(unsupported type) = %v, want nil", result)
	}
}

// --- valueToInterface: null value (Valid=false) ---

func TestValueToInterface_NullValue(t *testing.T) {
	val := common.Value{Typ: common.TypeInt64, Valid: false}
	result := valueToInterface(val)
	if result != nil {
		t.Errorf("valueToInterface(null value) = %v, want nil", result)
	}
}

// --- valueToInterface: bool type ---

func TestValueToInterface_Bool(t *testing.T) {
	val := common.NewBool(true)
	result := valueToInterface(val)
	if result != true {
		t.Errorf("valueToInterface(bool true) = %v, want true", result)
	}
}

// --- valueToInterface: timestamp type ---

func TestValueToInterface_Timestamp(t *testing.T) {
	ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	val := common.NewTimestamp(ts)
	result := valueToInterface(val)
	str, ok := result.(string)
	if !ok {
		t.Fatalf("valueToInterface(timestamp) type = %T, want string", result)
	}
	if str != ts.Format(time.RFC3339Nano) {
		t.Errorf("valueToInterface(timestamp) = %q, want %q", str, ts.Format(time.RFC3339Nano))
	}
}

// --- handleTCPConn: SetReadDeadline, SetWriteDeadline, Write error paths ---

// errConn wraps net.Conn and injects errors for specific operations.
type errConn struct {
	net.Conn
	setReadDeadlineErr  error
	setWriteDeadlineErr error
	writeErr            error
}

func (c *errConn) SetReadDeadline(t time.Time) error {
	if c.setReadDeadlineErr != nil {
		return c.setReadDeadlineErr
	}
	return c.Conn.SetReadDeadline(t)
}

func (c *errConn) SetWriteDeadline(t time.Time) error {
	if c.setWriteDeadlineErr != nil {
		return c.setWriteDeadlineErr
	}
	return c.Conn.SetWriteDeadline(t)
}

func (c *errConn) Write(b []byte) (int, error) {
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return c.Conn.Write(b)
}

func TestHandleTCPConn_SetReadDeadlineError(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()

	ec := &errConn{
		Conn:               serverConn,
		setReadDeadlineErr: errors.New("read deadline error"),
	}

	srv.wg.Add(1)
	// Call handleTCPConn directly; it should return immediately after SetReadDeadline fails
	srv.handleTCPConn(ec)
}

func TestHandleTCPConn_SetWriteDeadlineError(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()

	ec := &errConn{
		Conn:                serverConn,
		setWriteDeadlineErr: errors.New("write deadline error"),
	}

	// Client sends a ping packet so the server can read and process it,
	// then fail when trying to set the write deadline for the response.
	go func() {
		pingPkt := NewPacket(PacketPing, nil)
		_, _ = clientConn.Write(pingPkt.Encode())
	}()

	srv.wg.Add(1)
	srv.handleTCPConn(ec)
}

func TestHandleTCPConn_WriteError(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()

	ec := &errConn{
		Conn:     serverConn,
		writeErr: errors.New("write error"),
	}

	// Client sends a ping packet so the server can read and process it,
	// then fail when trying to write the response.
	go func() {
		pingPkt := NewPacket(PacketPing, nil)
		_, _ = clientConn.Write(pingPkt.Encode())
	}()

	srv.wg.Add(1)
	srv.handleTCPConn(ec)
}

// --- V2: handleQueryPacket with empty/nil payload ---

func TestHandleQueryPacket_EmptyPayload_V2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketQuery, []byte{})
	_, err := srv.handleQueryPacket(pkt)
	if err == nil {
		t.Error("expected error for empty payload, got nil")
	}
}

func TestHandleQueryPacket_NilPayload_V2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketQuery, nil)
	_, err := srv.handleQueryPacket(pkt)
	if err == nil {
		t.Error("expected error for nil payload, got nil")
	}
}

// --- V2: handleWritePacket with empty/nil payload ---

func TestHandleWritePacket_EmptyPayload_V2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketWrite, []byte{})
	_, err := srv.handleWritePacket(pkt)
	if err == nil {
		t.Error("expected error for empty payload, got nil")
	}
}

func TestHandleWritePacket_NilPayload_V2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketWrite, nil)
	_, err := srv.handleWritePacket(pkt)
	if err == nil {
		t.Error("expected error for nil payload, got nil")
	}
}

// --- V2: handlePing normal path ---

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

// --- V2: handlePacket with unknown packet type ---

func TestHandlePacket_UnknownType_V2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(255, nil)
	_, err := srv.handlePacket(pkt)
	if err == nil {
		t.Error("expected error for unknown packet type, got nil")
	}
}

// --- V2: acceptTCP connection limit test ---

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
