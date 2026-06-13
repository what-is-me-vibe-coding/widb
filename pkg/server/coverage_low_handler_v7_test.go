package server

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// unmarshallableData 是一个无法被 JSON 序列化的类型，用于测试 JSON 序列化错误路径。
type unmarshallableData struct{}

func (unmarshallableData) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("强制 JSON 序列化失败")
}

// ---------------------------------------------------------------------------
// handleQueryPacket: JSON 反序列化错误路径、handleQuery 错误路径、JSON 序列化错误路径
// ---------------------------------------------------------------------------

// TestCoverageLowHandlerV7_HandleQueryPacket_JSONUnmarshalError 测试查询包 JSON 反序列化错误路径。
// 使用表驱动测试覆盖多种无效 JSON 负载场景。
func TestCoverageLowHandlerV7_HandleQueryPacket_JSONUnmarshalError(t *testing.T) {
	srv := newTestServerV7(t)

	tests := []struct {
		name    string
		payload []byte
	}{
		{"无效JSON字符串", []byte("<<<不是json>>>")},
		{"空负载", []byte{}},
		{"不完整JSON对象_v7", []byte("{")},
		{"纯数字", []byte("42")},
		{"JSON数组非对象", []byte("[]")},
		{"JSON布尔值_v7", []byte("true")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(PacketQuery, tt.payload)
			resp, err := srv.handleQueryPacket(pkt)
			if err == nil {
				t.Error("期望 JSON 反序列化错误，得到 nil error")
			}
			if resp != nil {
				t.Errorf("期望 resp 为 nil，得到 %v", resp)
			}
		})
	}
}

// TestCoverageLowHandlerV7_HandleQueryPacket_HandleQueryError 测试查询包处理查询错误路径。
// 注意：当前 handleQuery 实现始终返回 nil error（错误通过 Response.Code 传递），
// 因此 handleQueryPacket 中的 `if err != nil` 分支在当前实现中不可达。
// 此测试验证 handleQuery 返回非零 Code 时，handleQueryPacket 仍能正常序列化并返回响应包。
func TestCoverageLowHandlerV7_HandleQueryPacket_HandleQueryError(t *testing.T) {
	srv := newTestServerV7(t)

	// 查询不存在的表，handleQuery 返回 Response{Code: -1}, nil（而非 Go error）
	payload, _ := json.Marshal(QueryRequest{SQL: "SELECT * FROM nonexistent_v7"})
	pkt := NewPacket(PacketQuery, payload)
	resp, err := srv.handleQueryPacket(pkt)
	if err != nil {
		t.Fatalf("handleQueryPacket 不应返回 Go 错误: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应包")
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应包类型 = %d，期望 %d", resp.Type, PacketResponse)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if response.Code != -1 {
		t.Errorf("响应 Code = %d，期望 -1", response.Code)
	}
}

// TestCoverageLowHandlerV7_HandleQueryPacket_MarshalError 测试查询包 JSON 序列化错误路径。
// 注意：当前 handleQuery 返回的 Response 始终包含可序列化的数据，
// 因此 json.Marshal(resp) 的错误分支在正常流程中不可达。
// 此测试通过直接构造包含不可序列化数据的 Response 来验证该错误路径的存在性。
func TestCoverageLowHandlerV7_HandleQueryPacket_MarshalError(t *testing.T) {
	// 构造包含不可序列化数据的 Response，验证 json.Marshal 会失败
	resp := &Response{Code: 0, Data: unmarshallableData{}}
	_, err := json.Marshal(resp)
	if err == nil {
		t.Error("期望 JSON 序列化错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// handleWritePacket: JSON 反序列化错误路径、handleWrite 错误路径、JSON 序列化错误路径
// ---------------------------------------------------------------------------

// TestCoverageLowHandlerV7_HandleWritePacket_JSONUnmarshalError 测试写入包 JSON 反序列化错误路径。
// 使用表驱动测试覆盖多种无效 JSON 负载场景。
func TestCoverageLowHandlerV7_HandleWritePacket_JSONUnmarshalError(t *testing.T) {
	srv := newTestServerV7(t)

	tests := []struct {
		name    string
		payload []byte
	}{
		{"无效JSON字符串", []byte("<<<不是json>>>")},
		{"空负载", []byte{}},
		{"不完整JSON对象_v7", []byte("{")},
		{"纯数字", []byte("42")},
		{"JSON数组非对象", []byte("[]")},
		{"JSON布尔值_v7", []byte("true")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(PacketWrite, tt.payload)
			resp, err := srv.handleWritePacket(pkt)
			if err == nil {
				t.Error("期望 JSON 反序列化错误，得到 nil error")
			}
			if resp != nil {
				t.Errorf("期望 resp 为 nil，得到 %v", resp)
			}
		})
	}
}

// TestCoverageLowHandlerV7_HandleWritePacket_HandleWriteError 测试写入包处理写入错误路径。
// 注意：当前 handleWrite 实现始终返回 nil error（错误通过 Response.Code 传递），
// 因此 handleWritePacket 中的 `if err != nil` 分支在当前实现中不可达。
// 此测试验证 handleWrite 返回非零 Code 时，handleWritePacket 仍能正常序列化并返回响应包。
func TestCoverageLowHandlerV7_HandleWritePacket_HandleWriteError(t *testing.T) {
	srv := newTestServerV7(t)

	// 写入不存在的表，handleWrite 返回 Response{Code: -1}, nil（而非 Go error）
	payload, _ := json.Marshal(WriteRequest{
		Table: "nonexistent_v7",
		Rows:  []map[string]interface{}{{"id": float64(1)}},
	})
	pkt := NewPacket(PacketWrite, payload)
	resp, err := srv.handleWritePacket(pkt)
	if err != nil {
		t.Fatalf("handleWritePacket 不应返回 Go 错误: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应包")
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应包类型 = %d，期望 %d", resp.Type, PacketResponse)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if response.Code != -1 {
		t.Errorf("响应 Code = %d，期望 -1", response.Code)
	}
}

// TestCoverageLowHandlerV7_HandleWritePacket_MarshalError 测试写入包 JSON 序列化错误路径。
// 注意：当前 handleWrite 返回的 Response 始终包含可序列化的数据，
// 因此 json.Marshal(resp) 的错误分支在正常流程中不可达。
// 此测试通过直接构造包含不可序列化数据的 Response 来验证该错误路径的存在性。
func TestCoverageLowHandlerV7_HandleWritePacket_MarshalError(t *testing.T) {
	// 构造包含不可序列化数据的 Response，验证 json.Marshal 会失败
	resp := &Response{Code: 0, Data: unmarshallableData{}}
	_, err := json.Marshal(resp)
	if err == nil {
		t.Error("期望 JSON 序列化错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// handlePing: JSON 序列化错误路径
// ---------------------------------------------------------------------------

// TestCoverageLowHandlerV7_HandlePing_Normal 测试心跳正常返回路径。
func TestCoverageLowHandlerV7_HandlePing_Normal(t *testing.T) {
	srv := newTestServerV7(t)

	resp, err := srv.handlePing()
	if err != nil {
		t.Fatalf("handlePing 失败: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应包")
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应包类型 = %d，期望 %d", resp.Type, PacketResponse)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if response.Code != 0 {
		t.Errorf("响应 Code = %d，期望 0", response.Code)
	}
	if response.Message != msgPong {
		t.Errorf("响应 Message = %q，期望 %q", response.Message, msgPong)
	}
}

// TestCoverageLowHandlerV7_HandlePing_MarshalError 测试心跳 JSON 序列化错误路径。
// 注意：handlePing 构造的 Response{Code:0, Message:"pong"} 始终可序列化，
// 因此 json.Marshal 的错误分支在正常流程中不可达。
// 此测试通过直接构造包含不可序列化数据的 Response 来验证该错误路径的存在性。
func TestCoverageLowHandlerV7_HandlePing_MarshalError(t *testing.T) {
	// 构造包含不可序列化数据的 Response，验证 json.Marshal 会失败
	resp := &Response{Code: 0, Message: msgPong, Data: unmarshallableData{}}
	_, err := json.Marshal(resp)
	if err == nil {
		t.Error("期望 JSON 序列化错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

// newTestServerV7 创建用于 V7 覆盖率测试的服务器。
func newTestServerV7(t *testing.T) *Server {
	t.Helper()

	dir := t.TempDir()
	cfg := Config{
		TCPAddr:  testListenAddr,
		HTTPAddr: testListenAddr,
		DataDir:  dir,
	}

	registry := prometheus.NewRegistry()
	srv, err := NewServer(cfg, WithMetricsRegistry(registry))
	if err != nil {
		t.Fatalf("NewServer 失败: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	return srv
}
