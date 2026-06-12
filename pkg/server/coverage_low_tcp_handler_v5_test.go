package server

import (
	"encoding/json"
	"errors"
	"testing"
)

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
