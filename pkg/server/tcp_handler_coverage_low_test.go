package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
)

const (
	v14OpAccept    = "accept"
	v14OpRead      = "read"
	v14Nonexistent = "nonexistent"
	testNameIOEOF  = "io.EOF"
	testNameNilErr = "nil error"
)

// ---------------------------------------------------------------------------
// isTransientAcceptErr: comprehensive tests
// ---------------------------------------------------------------------------

func TestIsTransientAcceptErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"non-OpError", errors.New("some error"), false},
		{testNameNilErr, nil, false},
		{"OpError with timeout", &net.OpError{Op: v14OpAccept, Net: testNetTCP, Err: timeoutError{}}, true},
		{"OpError resource temporarily unavailable", &net.OpError{Op: v14OpAccept, Net: testNetTCP, Err: errors.New("resource temporarily unavailable")}, true},
		{"OpError too many open files", &net.OpError{Op: v14OpAccept, Net: testNetTCP, Err: errors.New("too many open files")}, true},
		{"OpError other message", &net.OpError{Op: v14OpAccept, Net: testNetTCP, Err: errors.New("connection refused")}, false},
		{"OpError partial match resource", &net.OpError{Op: v14OpAccept, Net: testNetTCP, Err: errors.New("accept: resource temporarily unavailable - retry")}, true},
		{"OpError partial match too many", &net.OpError{Op: v14OpAccept, Net: testNetTCP, Err: errors.New("socket: too many open files in system")}, true},
		{"OpError empty message", &net.OpError{Op: v14OpAccept, Net: testNetTCP, Err: errors.New("")}, false},
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
// isClosedConnErr: comprehensive tests
// ---------------------------------------------------------------------------

func TestIsClosedConnErr_Table(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{testNameIOEOF, io.EOF, true},
		{"net.ErrClosed", net.ErrClosed, true},
		{"wrapped net.ErrClosed", fmt.Errorf("wrapped: %w", net.ErrClosed), true},
		{"double-wrapped net.ErrClosed", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", net.ErrClosed)), true},
		{"timeout OpError", &net.OpError{Op: v14OpRead, Net: testNetTCP, Err: timeoutError{}}, true},
		{"non-timeout OpError", &net.OpError{Op: v14OpRead, Net: testNetTCP, Err: errors.New("connection reset")}, false},
		{"arbitrary error", errors.New("some error"), false},
		{testNameNilErr, nil, false},
		{"OpError empty Err", &net.OpError{Op: v14OpRead, Net: testNetTCP, Err: errors.New("")}, false},
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
// handlePacket: unknown packet type paths
// ---------------------------------------------------------------------------

func TestHandlePacket_UnknownTypes(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name    string
		pktType uint8
		payload []byte
	}{
		{"type 0", 0, nil},
		{"type 4", 4, nil},
		{"type 255", 255, nil},
		{"type 99 with payload", 99, []byte(`{"test": true}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(tt.pktType, tt.payload)
			resp, err := srv.handlePacket(pkt)
			if err == nil {
				t.Error("unknown packet type should return error")
			}
			if resp != nil {
				t.Errorf("resp = %v, want nil", resp)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// handleQueryPacket: invalid JSON payload paths
// ---------------------------------------------------------------------------

func TestHandleQueryPacket_BadJSON(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name    string
		payload []byte
	}{
		{"empty", []byte{}},
		{"partial JSON", []byte("{")},
		{"number", []byte("42")},
		{"array", []byte(`[1,2,3]`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(PacketQuery, tt.payload)
			resp, err := srv.handleQueryPacket(pkt)
			if err == nil {
				t.Error("invalid JSON should return error")
			}
			if resp != nil {
				t.Errorf("resp = %v, want nil", resp)
			}
		})
	}
}

// "null" is valid JSON and unmarshals into QueryRequest as an empty struct,
// producing QueryRequest{SQL: ""} which fails at SQL parse (Code=-1, no Go error).
func TestHandleQueryPacket_NullPayload(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketQuery, []byte("null"))
	resp, err := srv.handleQueryPacket(pkt)
	if err != nil {
		t.Fatalf("null payload should unmarshal: %v", err)
	}
	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if response.Code != -1 {
		t.Errorf("Code = %d, want -1", response.Code)
	}
}

// ---------------------------------------------------------------------------
// handleQueryPacket: valid query returning error response from handleQuery
// ---------------------------------------------------------------------------
// Note: handleQuery never returns a non-nil Go error; errors are encoded as
// Response{Code: -1}. The error path in handleQueryPacket where handleQuery
// returns a non-nil error is currently unreachable without code changes.

func TestHandleQueryPacket_ErrorResponses(t *testing.T) {
	tests := []struct {
		name string
		sql  string
	}{
		{"parse error", testInvalidSQL},
		{"table not exist", testSelectNonexistent},
		{"empty SQL", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			defer func() { _ = srv.Stop() }()

			payload, _ := json.Marshal(QueryRequest{SQL: tt.sql})
			pkt := NewPacket(PacketQuery, payload)
			resp, err := srv.handleQueryPacket(pkt)
			if err != nil {
				t.Fatalf("handleQueryPacket should not return Go error: %v", err)
			}
			var response Response
			if err := json.Unmarshal(resp.Payload, &response); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if response.Code != -1 {
				t.Errorf("Code = %d, want -1", response.Code)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// handleWritePacket: invalid JSON payload paths
// ---------------------------------------------------------------------------

func TestHandleWritePacket_BadJSON(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name    string
		payload []byte
	}{
		{"empty", []byte{}},
		{"partial JSON", []byte("{")},
		{"number", []byte("42")},
		{"string", []byte(`"hello"`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(PacketWrite, tt.payload)
			resp, err := srv.handleWritePacket(pkt)
			if err == nil {
				t.Error("invalid JSON should return error")
			}
			if resp != nil {
				t.Errorf("resp = %v, want nil", resp)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// handleWritePacket: valid write returning error response from handleWrite
// ---------------------------------------------------------------------------
// Note: handleWrite never returns a non-nil Go error; errors are encoded as
// Response{Code: -1}. The error path in handleWritePacket where handleWrite
// returns a non-nil error is currently unreachable.

func TestHandleWritePacket_ErrorResponses(t *testing.T) {
	tests := []struct {
		name  string
		table string
		rows  []map[string]interface{}
	}{
		{"table not exist", v14Nonexistent, []map[string]interface{}{{"id": float64(1)}}},
		{"missing primary key", testTable, []map[string]interface{}{{testColName: testName}}},
		{"type mismatch", testTable, []map[string]interface{}{{"id": float64(1), testColName: true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServerWithTable(t)
			defer func() { _ = srv.Stop() }()

			payload, _ := json.Marshal(WriteRequest{Table: tt.table, Rows: tt.rows})
			pkt := NewPacket(PacketWrite, payload)
			resp, err := srv.handleWritePacket(pkt)
			if err != nil {
				t.Fatalf("handleWritePacket should not return Go error: %v", err)
			}
			var response Response
			if err := json.Unmarshal(resp.Payload, &response); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if response.Code != -1 {
				t.Errorf("Code = %d, want -1", response.Code)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// handlePing: response content verification
// ---------------------------------------------------------------------------
// Note: handlePing always constructs Response{Code:0, Message:"pong"}, which
// is always marshallable. The json.Marshal error path is unreachable.

func TestHandlePing_ResponseContent(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	resp, err := srv.handlePing()
	if err != nil {
		t.Fatalf("handlePing failed: %v", err)
	}
	if resp.Type != PacketResponse {
		t.Errorf("type = %d, want %d", resp.Type, PacketResponse)
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

// ---------------------------------------------------------------------------
// handlePacket: routing verification
// ---------------------------------------------------------------------------

func TestHandlePacket_RouteInvalidPayloads(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	// Query route with invalid payload
	if _, err := srv.handlePacket(NewPacket(PacketQuery, []byte("bad"))); err == nil {
		t.Error("PacketQuery with bad payload should return error")
	}
	// Write route with invalid payload
	if _, err := srv.handlePacket(NewPacket(PacketWrite, []byte("bad"))); err == nil {
		t.Error("PacketWrite with bad payload should return error")
	}
	// Ping route succeeds
	resp, err := srv.handlePacket(NewPacket(PacketPing, nil))
	if err != nil {
		t.Fatalf("PacketPing should not error: %v", err)
	}
	if resp.Type != PacketResponse {
		t.Errorf("ping resp type = %d, want %d", resp.Type, PacketResponse)
	}
}

// ---------------------------------------------------------------------------
// handleQueryPacket / handleWritePacket: closed storage
// ---------------------------------------------------------------------------

func TestHandleQueryPacket_ClosedStorage(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	_ = srv.storage.Close()
	payload, _ := json.Marshal(QueryRequest{SQL: testSelectAll})
	pkt := NewPacket(PacketQuery, payload)
	resp, err := srv.handleQueryPacket(pkt)
	if err != nil {
		t.Logf("closed storage returned Go error: %v", err)
	}
	if resp != nil {
		var response Response
		if unmarshalErr := json.Unmarshal(resp.Payload, &response); unmarshalErr == nil {
			t.Logf("closed storage: Code=%d, Message=%q", response.Code, response.Message)
		}
	}
}

func TestHandleWritePacket_ClosedStorage(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	_ = srv.storage.Close()
	payload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(1), testColName: testName}},
	})
	pkt := NewPacket(PacketWrite, payload)
	resp, err := srv.handleWritePacket(pkt)
	if err != nil {
		t.Logf("closed storage returned Go error: %v", err)
	}
	if resp != nil {
		var response Response
		if unmarshalErr := json.Unmarshal(resp.Payload, &response); unmarshalErr == nil {
			if response.Code != -1 {
				t.Errorf("Code = %d, want -1 (closed storage)", response.Code)
			}
		}
	}
}
