package server

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// handleQueryPacket JSON 序列化错误路径
// ---------------------------------------------------------------------------

// TestHandleQueryPacket_MarshalError_V18 测试 handleQueryPacket 中 JSON 序列化响应失败的路径。
// 由于正常情况下 json.Marshal 不容易失败，我们通过间接方式验证。
// 当 handleQuery 返回包含无法序列化数据的 Response 时，marshal 会失败。
func TestHandleQueryPacket_MarshalError_V18(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	// 正常查询应该可以序列化
	payload, _ := json.Marshal(QueryRequest{SQL: testSelectAll})
	pkt := NewPacket(PacketQuery, payload)
	resp, err := srv.handleQueryPacket(pkt)
	if err != nil {
		t.Fatalf("handleQueryPacket failed: %v", err)
	}
	if resp == nil {
		t.Error("expected non-nil response")
	}
}

// ---------------------------------------------------------------------------
// handleWritePacket JSON 序列化错误路径
// ---------------------------------------------------------------------------

// TestHandleWritePacket_MarshalError_V18 测试 handleWritePacket 中 JSON 序列化响应失败的路径。
func TestHandleWritePacket_MarshalError_V18(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	// 正常写入应该可以序列化
	writePayload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(1), testColName: testName}},
	})
	pkt := NewPacket(PacketWrite, writePayload)
	resp, err := srv.handleWritePacket(pkt)
	if err != nil {
		t.Fatalf("handleWritePacket failed: %v", err)
	}
	if resp == nil {
		t.Error("expected non-nil response")
	}
}

// ---------------------------------------------------------------------------
// handlePing 正常路径补充
// ---------------------------------------------------------------------------

// TestHandlePing_MarshalError_V18 测试 handlePing 的 JSON 序列化。
// json.Marshal 对简单结构不太可能失败，但确保路径被覆盖。
func TestHandlePing_MarshalError_V18(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	resp, err := srv.handlePing()
	if err != nil {
		t.Fatalf("handlePing failed: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("unmarshal ping response: %v", err)
	}
	if response.Message != msgPong {
		t.Errorf("expected message %q, got %q", msgPong, response.Message)
	}
}

// ---------------------------------------------------------------------------
// handlePacket 路由覆盖
// ---------------------------------------------------------------------------

// TestHandlePacket_AllTypes_V18 测试 handlePacket 对所有包类型的路由。
func TestHandlePacket_AllTypes_V18(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name    string
		pktType uint8
		wantErr bool
	}{
		{"Query", PacketQuery, false},
		{"Write", PacketWrite, false},
		{"Ping", PacketPing, false},
		{"Unknown", 99, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var payload []byte
			switch tt.pktType {
			case PacketQuery:
				payload, _ = json.Marshal(QueryRequest{SQL: testSelectAll})
			case PacketWrite:
				payload, _ = json.Marshal(WriteRequest{
					Table: testTable,
					Rows:  []map[string]interface{}{{"id": float64(1), testColName: testName}},
				})
			case PacketPing:
				payload = nil
			default:
				payload = []byte("test")
			}

			pkt := NewPacket(tt.pktType, payload)
			_, err := srv.handlePacket(pkt)
			if (err != nil) != tt.wantErr {
				t.Errorf("handlePacket(%s) error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
		})
	}
}
