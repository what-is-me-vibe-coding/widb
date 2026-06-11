package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

// ---------------------------------------------------------------------------
// newErrorResponse: 正常路径，验证 JSON payload
// ---------------------------------------------------------------------------

// verifyErrorResponsePacket 验证错误响应 Packet 的公共字段和 JSON payload。
func verifyErrorResponsePacket(t *testing.T, pkt *Packet, wantMsg string) {
	t.Helper()
	if pkt == nil {
		t.Fatal("newErrorResponse 不应返回 nil")
	}
	if pkt.Type != PacketResponse {
		t.Errorf("Type = %d, want %d", pkt.Type, PacketResponse)
	}
	if pkt.Magic != Magic {
		t.Errorf("Magic = 0x%08x, want 0x%08x", pkt.Magic, Magic)
	}
	if pkt.Version != ProtocolVersion {
		t.Errorf("Version = %d, want %d", pkt.Version, ProtocolVersion)
	}

	var resp Response
	if err := json.Unmarshal(pkt.Payload, &resp); err != nil {
		t.Fatalf("JSON 反序列化失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("Code = %d, want -1", resp.Code)
	}
	if resp.Message != wantMsg {
		t.Errorf("Message = %q, want %q", resp.Message, wantMsg)
	}
}

func TestCoverageStabilityNewErrorResponse(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{"简单错误", errors.New("something failed"), "something failed"},
		{"格式化错误", fmt.Errorf("query error: %s", "bad syntax"), "query error: bad syntax"},
		{"空消息错误", errors.New(""), ""},
		{"wrapped 错误", fmt.Errorf("outer: %w", errors.New("inner")), "outer: inner"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := newErrorResponse(tt.err)
			verifyErrorResponsePacket(t, pkt, tt.wantMsg)
		})
	}
}
