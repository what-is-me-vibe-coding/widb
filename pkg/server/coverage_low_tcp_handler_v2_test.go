package server

import (
	"encoding/json"
	"testing"
)

// 测试用例中重复使用的字符串常量，避免 goconst 重复字符串警告
const (
	testNameIncompleteJSON = "不完整JSON"
	testNamePureNumber     = "纯数字"
	testNameBinaryGarbage  = "二进制垃圾"
)

// ---------------------------------------------------------------------------
// handleQueryPacket JSON 反序列化错误路径（tcp_handler.go:113）
// ---------------------------------------------------------------------------

// TestHandleQueryPacketBadJSONCov_V2 测试 handleQueryPacket 对各种无效 JSON 的处理
// 覆盖 tcp_handler.go:115-117 行的 JSON 反序列化错误分支
func TestHandleQueryPacketBadJSONCov_V2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name    string
		payload []byte
	}{
		{"空字节", []byte{}},
		{testNameIncompleteJSON, []byte("{")},
		{testNamePureNumber, []byte("42")},
		{"数组", []byte(`[1,2,3]`)},
		{"布尔值", []byte("true")},
		{testNameBinaryGarbage, []byte{0x00, 0x01, 0x02, 0xFF}},
		{"无效UTF8", []byte("\xff\xfe\xfd")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(PacketQuery, tt.payload)
			resp, err := srv.handleQueryPacket(pkt)
			if err == nil {
				t.Error("期望无效 JSON 返回错误，得到 nil")
			}
			if resp != nil {
				t.Errorf("期望 nil 响应，得到 %v", resp)
			}
		})
	}
}

// TestHandleQueryPacketValidJSONCov_V2 测试 handleQueryPacket 对有效 JSON 的处理
// 验证正常路径：有效 JSON 解析后执行查询
func TestHandleQueryPacketValidJSONCov_V2(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	payload, _ := json.Marshal(QueryRequest{SQL: "SELECT * FROM " + testTable})
	pkt := NewPacket(PacketQuery, payload)
	resp, err := srv.handleQueryPacket(pkt)
	if err != nil {
		t.Fatalf("有效 JSON 不应返回错误: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应")
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应类型 = %d，期望 %d", resp.Type, PacketResponse)
	}
}

// ---------------------------------------------------------------------------
// handleWritePacket JSON 反序列化错误路径（tcp_handler.go:133）
// ---------------------------------------------------------------------------

// TestHandleWritePacketBadJSONCov_V2 测试 handleWritePacket 对各种无效 JSON 的处理
// 覆盖 tcp_handler.go:135-137 行的 JSON 反序列化错误分支
func TestHandleWritePacketBadJSONCov_V2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name    string
		payload []byte
	}{
		{"空字节", []byte{}},
		{testNameIncompleteJSON, []byte("{")},
		{testNamePureNumber, []byte("42")},
		{"字符串", []byte(`"hello"`)},
		{"布尔值", []byte("false")},
		{testNameBinaryGarbage, []byte{0xDE, 0xAD, 0xBE, 0xEF}},
		{"无效UTF8", []byte("\xff\xfe")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(PacketWrite, tt.payload)
			resp, err := srv.handleWritePacket(pkt)
			if err == nil {
				t.Error("期望无效 JSON 返回错误，得到 nil")
			}
			if resp != nil {
				t.Errorf("期望 nil 响应，得到 %v", resp)
			}
		})
	}
}

// TestHandleWritePacketValidJSONCov_V2 测试 handleWritePacket 对有效 JSON 的处理
// 验证正常路径：有效 JSON 解析后执行写入
func TestHandleWritePacketValidJSONCov_V2(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	payload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(1), testColName: testName}},
	})
	pkt := NewPacket(PacketWrite, payload)
	resp, err := srv.handleWritePacket(pkt)
	if err != nil {
		t.Fatalf("有效 JSON 不应返回错误: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应")
	}
}

// ---------------------------------------------------------------------------
// handlePacket 路由覆盖补充（tcp_handler.go:98）
// ---------------------------------------------------------------------------

// TestHandlePacketRouteBadPayloadsCov_V2 测试 handlePacket 对各种包类型的路由
// 验证 Query/Write 路由在无效 payload 时正确返回错误
func TestHandlePacketRouteBadPayloadsCov_V2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	// Query 路由 + 无效 payload
	_, err := srv.handlePacket(NewPacket(PacketQuery, []byte("not json")))
	if err == nil {
		t.Error("PacketQuery + 无效 payload 应返回错误")
	}

	// Write 路由 + 无效 payload
	_, err = srv.handlePacket(NewPacket(PacketWrite, []byte("not json")))
	if err == nil {
		t.Error("PacketWrite + 无效 payload 应返回错误")
	}

	// Ping 路由始终成功
	resp, err := srv.handlePacket(NewPacket(PacketPing, nil))
	if err != nil {
		t.Fatalf("PacketPing 不应返回错误: %v", err)
	}
	if resp.Type != PacketResponse {
		t.Errorf("ping 响应类型 = %d，期望 %d", resp.Type, PacketResponse)
	}
}
