package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// handlePing: verify via handlePacket that PacketPing produces a valid response
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// handleTCPConn: exits cleanly when server is shutting down (s.done closed)
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// isClosedConnErr: test with io.EOF, net.ErrClosed, and a regular error
// ---------------------------------------------------------------------------

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
		{testNameNilError, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isClosedConnErr(tt.err); got != tt.want {
				t.Errorf("isClosedConnErr() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isTransientAcceptErr: timeout OpError, non-timeout OpError with
// "resource temporarily unavailable", and regular error
// ---------------------------------------------------------------------------

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
			testNameNilError,
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
