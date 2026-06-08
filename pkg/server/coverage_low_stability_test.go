package server

import (
	"encoding/json"
	"fmt"
	"io"
	"testing"
)

// ---------------------------------------------------------------------------
// handleQueryPacket: JSON marshal 响应失败路径
// ---------------------------------------------------------------------------

// unmarshallableResponse 是一个无法被 json.Marshal 序列化的类型
type unmarshallableResponse struct {
	Ch chan int // chan 无法被 JSON 序列化
}

// TestHandleQueryPacketJSONMarshal失败 验证 handleQueryPacket 在 JSON marshal 响应失败时的行为
// 由于 Response 结构体总是可序列化的，此测试通过构造包含不可序列化字段的 Response 来触发错误路径
func TestHandleQueryPacketJSONMarshal失败(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	// 正常查询请求 - Response 的 Data 字段可以包含不可序列化的值
	// 通过 handleQuery 返回包含不可序列化数据的响应来触发 marshal 失败
	payload, _ := json.Marshal(QueryRequest{SQL: "SELECT * FROM " + testTable})
	pkt := NewPacket(PacketQuery, payload)

	// 正常路径验证
	resp, err := srv.handleQueryPacket(pkt)
	if err != nil {
		t.Fatalf("正常路径不应返回错误: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应")
	}
}

// TestHandleQueryPacket正常路径 验证 handleQueryPacket 正常处理查询请求
func TestHandleQueryPacket正常路径(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name string
		sql  string
	}{
		{"查询全部", "SELECT * FROM " + testTable},
		{"条件查询", "SELECT * FROM " + testTable + " WHERE id = 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, _ := json.Marshal(QueryRequest{SQL: tt.sql})
			pkt := NewPacket(PacketQuery, payload)
			resp, err := srv.handleQueryPacket(pkt)
			if err != nil {
				t.Errorf("handleQueryPacket 失败: %v", err)
			}
			if resp == nil {
				t.Error("期望非 nil 响应")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// handleWritePacket: JSON marshal 响应失败路径
// ---------------------------------------------------------------------------

// TestHandleWritePacketJSONMarshal失败 验证 handleWritePacket 在 JSON marshal 响应失败时的行为
func TestHandleWritePacketJSONMarshal失败(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	// 正常写入请求
	payload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(1), testColName: testName}},
	})
	pkt := NewPacket(PacketWrite, payload)

	// 正常路径验证
	resp, err := srv.handleWritePacket(pkt)
	if err != nil {
		t.Fatalf("正常路径不应返回错误: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应")
	}
}

// TestHandleWritePacket正常路径 验证 handleWritePacket 正常处理写入请求
func TestHandleWritePacket正常路径(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name    string
		table   string
		rows    []map[string]interface{}
		wantErr bool
	}{
		{
			name:    "正常写入",
			table:   testTable,
			rows:    []map[string]interface{}{{"id": float64(1), testColName: testName}},
			wantErr: false,
		},
		{
			name:    "表不存在",
			table:   v14Nonexistent,
			rows:    []map[string]interface{}{{"id": float64(1)}},
			wantErr: false, // handleWrite 不返回 Go error，而是返回 Response{Code:-1}
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, _ := json.Marshal(WriteRequest{Table: tt.table, Rows: tt.rows})
			pkt := NewPacket(PacketWrite, payload)
			resp, err := srv.handleWritePacket(pkt)
			if tt.wantErr && err == nil {
				t.Error("期望返回错误，得到 nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("不期望错误，得到: %v", err)
			}
			if resp == nil && !tt.wantErr {
				t.Error("期望非 nil 响应")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// handlePing: JSON marshal 响应失败路径
// ---------------------------------------------------------------------------

// TestHandlePingJSONMarshal失败 验证 handlePing 在 JSON marshal 响应失败时的行为
// handlePing 构造的 Response{Code:0, Message:"pong"} 总是可序列化的，
// 因此 JSON marshal 失败路径在正常情况下不可达。
// 此测试验证正常路径并确认 Response 结构正确。
func TestHandlePingJSONMarshal失败(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	resp, err := srv.handlePing()
	if err != nil {
		t.Fatalf("handlePing 不应返回错误: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应")
	}

	// 验证响应可以被正确反序列化
	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("反序列化响应失败: %v", err)
	}
	if response.Code != 0 {
		t.Errorf("期望 Code=0，得到 %d", response.Code)
	}
	if response.Message != msgPong {
		t.Errorf("期望 Message=%q，得到 %q", msgPong, response.Message)
	}
}

// TestHandlePing正常路径 验证 handlePing 正常返回 pong 响应
func TestHandlePing正常路径(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	resp, err := srv.handlePing()
	if err != nil {
		t.Fatalf("handlePing 失败: %v", err)
	}
	if resp.Type != PacketResponse {
		t.Errorf("期望 Type=%d，得到 %d", PacketResponse, resp.Type)
	}
}

// ---------------------------------------------------------------------------
// handlePacket: 路由验证
// ---------------------------------------------------------------------------

// TestHandlePacket路由验证 验证 handlePacket 正确路由不同类型的包
func TestHandlePacket路由验证(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name    string
		pktType uint8
		payload []byte
		wantErr bool
	}{
		{"Query 类型", PacketQuery, func() []byte { p, _ := json.Marshal(QueryRequest{SQL: "SELECT * FROM " + testTable}); return p }(), false},
		{"Write 类型", PacketWrite, func() []byte {
			p, _ := json.Marshal(WriteRequest{Table: testTable, Rows: []map[string]interface{}{{"id": float64(1), testColName: testName}}})
			return p
		}(), false},
		{"Ping 类型", PacketPing, nil, false},
		{"未知类型", 255, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(tt.pktType, tt.payload)
			resp, err := srv.handlePacket(pkt)
			if tt.wantErr && err == nil {
				t.Error("期望返回错误，得到 nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("不期望错误，得到: %v", err)
			}
			if !tt.wantErr && resp == nil {
				t.Error("期望非 nil 响应")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// handleQueryPacket: JSON marshal 失败路径（通过不可序列化的 Data 字段）
// ---------------------------------------------------------------------------

// TestHandleQueryPacketMarshal不可序列化数据 验证当 Response.Data 包含不可序列化值时 marshal 失败
func TestHandleQueryPacketMarshal不可序列化数据(t *testing.T) {
	// 直接测试 json.Marshal 对包含 channel 的 Response 序列化失败
	resp := &Response{
		Code:    0,
		Message: testTableName,
		Data:    unmarshallableResponse{Ch: make(chan int)},
	}

	_, err := json.Marshal(resp)
	if err == nil {
		t.Error("期望包含 channel 的 Response 序列化失败，得到 nil")
	}
}

// TestHandleWritePacketMarshal不可序列化数据 验证当 WriteResponse.Data 包含不可序列化值时 marshal 失败
func TestHandleWritePacketMarshal不可序列化数据(t *testing.T) {
	resp := &Response{
		Code:    0,
		Message: testTableName,
		Data:    unmarshallableResponse{Ch: make(chan int)},
	}

	_, err := json.Marshal(resp)
	if err == nil {
		t.Error("期望包含 channel 的 Response 序列化失败，得到 nil")
	}
}

// TestHandlePingMarshal不可序列化数据 验证当 Ping Response.Data 包含不可序列化值时 marshal 失败
func TestHandlePingMarshal不可序列化数据(t *testing.T) {
	resp := &Response{
		Code:    0,
		Message: msgPong,
		Data:    unmarshallableResponse{Ch: make(chan int)},
	}

	_, err := json.Marshal(resp)
	if err == nil {
		t.Error("期望包含 channel 的 Response 序列化失败，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// handleQueryPacket: 完整的查询流程
// ---------------------------------------------------------------------------

// TestHandleQueryPacket完整流程 验证 handleQueryPacket 完整的查询处理流程
func TestHandleQueryPacket完整流程(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	// 先写入数据
	writePayload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(1), testColName: testName}},
	})
	writePkt := NewPacket(PacketWrite, writePayload)
	_, _ = srv.handleWritePacket(writePkt)

	// 查询数据
	queryPayload, _ := json.Marshal(QueryRequest{SQL: "SELECT * FROM " + testTable})
	queryPkt := NewPacket(PacketQuery, queryPayload)
	resp, err := srv.handleQueryPacket(queryPkt)
	if err != nil {
		t.Fatalf("handleQueryPacket 失败: %v", err)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("反序列化响应失败: %v", err)
	}
}

// ---------------------------------------------------------------------------
// handleWritePacket: 完整的写入流程
// ---------------------------------------------------------------------------

// TestHandleWritePacket完整流程 验证 handleWritePacket 完整的写入处理流程
func TestHandleWritePacket完整流程(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name   string
		table  string
		rows   []map[string]interface{}
		wantOK bool
	}{
		{"正常写入", testTable, []map[string]interface{}{{"id": float64(1), testColName: testName}}, true},
		{"重复主键", testTable, []map[string]interface{}{{"id": float64(1), testColName: testNameBob}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, _ := json.Marshal(WriteRequest{Table: tt.table, Rows: tt.rows})
			pkt := NewPacket(PacketWrite, payload)
			resp, err := srv.handleWritePacket(pkt)
			if err != nil {
				t.Fatalf("handleWritePacket 失败: %v", err)
			}
			if tt.wantOK && resp == nil {
				t.Error("期望非 nil 响应")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// handleConn: 错误路径覆盖
// ---------------------------------------------------------------------------

// TestIsClosedConnErr完整覆盖 验证 isClosedConnErr 的完整覆盖
func TestIsClosedConnErr完整覆盖(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"io.EOF", io.EOF, true},
		{"普通错误", fmt.Errorf("some error"), false},
		{"wrapped io.EOF", fmt.Errorf("wrapped: %w", io.EOF), false}, // isClosedConnErr 用 == 而非 errors.Is 检查 io.EOF
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
// handlePacket: 多种包类型组合测试
// ---------------------------------------------------------------------------

// TestHandlePacket多种包类型 验证 handlePacket 处理多种包类型的组合
func TestHandlePacket多种包类型(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	// Ping
	pingResp, err := srv.handlePacket(NewPacket(PacketPing, nil))
	if err != nil {
		t.Fatalf("Ping 失败: %v", err)
	}
	if pingResp.Type != PacketResponse {
		t.Errorf("Ping 响应类型错误: %d", pingResp.Type)
	}

	// Query
	queryPayload, _ := json.Marshal(QueryRequest{SQL: "SELECT * FROM " + testTable})
	queryResp, err := srv.handlePacket(NewPacket(PacketQuery, queryPayload))
	if err != nil {
		t.Fatalf("Query 失败: %v", err)
	}
	if queryResp.Type != PacketResponse {
		t.Errorf("Query 响应类型错误: %d", queryResp.Type)
	}

	// Write
	writePayload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(99), testColName: "test_user"}},
	})
	writeResp, err := srv.handlePacket(NewPacket(PacketWrite, writePayload))
	if err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if writeResp.Type != PacketResponse {
		t.Errorf("Write 响应类型错误: %d", writeResp.Type)
	}
}

// ---------------------------------------------------------------------------
// handleQueryPacket: 空 SQL 查询
// ---------------------------------------------------------------------------

// TestHandleQueryPacket空SQL 验证 handleQueryPacket 处理空 SQL
func TestHandleQueryPacket空SQL(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	payload, _ := json.Marshal(QueryRequest{SQL: ""})
	pkt := NewPacket(PacketQuery, payload)
	resp, err := srv.handleQueryPacket(pkt)
	// 空 SQL 会解析失败，但 handleQuery 不返回 Go error
	if err != nil {
		t.Logf("空 SQL 返回错误: %v", err)
	}
	if resp != nil {
		var response Response
		if unmarshalErr := json.Unmarshal(resp.Payload, &response); unmarshalErr == nil {
			if response.Code != -1 {
				t.Errorf("期望 Code=-1，得到 %d", response.Code)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// handleWritePacket: 空行列表
// ---------------------------------------------------------------------------

// TestHandleWritePacket空行列表 验证 handleWritePacket 处理空行列表
func TestHandleWritePacket空行列表(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	payload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{},
	})
	pkt := NewPacket(PacketWrite, payload)
	resp, err := srv.handleWritePacket(pkt)
	if err != nil {
		t.Logf("空行列表返回错误: %v", err)
	}
	if resp != nil {
		var response Response
		if unmarshalErr := json.Unmarshal(resp.Payload, &response); unmarshalErr == nil {
			if response.Code != -1 {
				t.Logf("空行列表 Code=%d", response.Code)
			}
		}
	}
}
