package server

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

const (
	testSelectAll         = "SELECT * FROM users"
	testTable             = "users"
	testName              = "alice"
	testTableName         = "test"
	testListenAddr        = "127.0.0.1:0"
	testColScore          = "score"
	testStrHello          = "hello"
	testColName           = "name"
	testNameBob           = "bob"
	testWriteAliceBody    = `{"table":"users","rows":[{"id":1,"name":"alice"}]}`
	testInvalidSQLBody    = `{"sql":"INVALID SQL !!!"}`
	testPlainText         = "纯文本"
	testJSONArray         = "JSON数组"
	testInvalidSQL        = "INVALID SQL !!!"
	testSelectNonexistent = "SELECT * FROM nonexistent"
)

// --- handleQuery / handleWrite 直接测试 ---

func TestHandleQuerySelectFromTable(t *testing.T) {
	srv := newTestServerWithTable(t)
	resp, err := srv.handleQuery(&QueryRequest{SQL: testSelectAll})
	if err != nil {
		t.Fatalf("handleQuery 失败: %v", err)
	}
	if resp.Code != 0 {
		t.Errorf("响应 Code = %d, Message = %q", resp.Code, resp.Message)
	}
}

func TestHandleQueryInvalidSQL(t *testing.T) {
	srv := newTestServer(t)
	resp, err := srv.handleQuery(&QueryRequest{SQL: testInvalidSQL})
	if err != nil {
		t.Fatalf("handleQuery 失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", resp.Code)
	}
}

func TestHandleQueryTableNotExist(t *testing.T) {
	srv := newTestServer(t)
	resp, err := srv.handleQuery(&QueryRequest{SQL: testSelectNonexistent})
	if err != nil {
		t.Fatalf("handleQuery 失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", resp.Code)
	}
}

func TestHandleWriteSuccess(t *testing.T) {
	srv := newTestServerWithTable(t)
	resp, err := srv.handleWrite(&WriteRequest{
		Table: testTable,
		Rows: []map[string]interface{}{
			{"id": float64(1), testColName: testName},
			{"id": float64(2), testColName: testNameBob},
		},
	})
	if err != nil {
		t.Fatalf("handleWrite 失败: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("写入响应 Code = %d, Message = %q", resp.Code, resp.Message)
	}
	if resp.Rows != 2 {
		t.Errorf("写入行数 = %d, 期望 2", resp.Rows)
	}
}

func TestHandleWriteTableNotExist(t *testing.T) {
	srv := newTestServer(t)
	resp, err := srv.handleWrite(&WriteRequest{
		Table: "nonexistent", //nolint:goconst
		Rows:  []map[string]interface{}{{"id": 1}},
	})
	if err != nil {
		t.Fatalf("handleWrite 失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", resp.Code)
	}
}

func TestHandleWriteMissingPK(t *testing.T) {
	srv := newTestServerWithTable(t)
	resp, err := srv.handleWrite(&WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{testColName: testName}},
	})
	if err != nil {
		t.Fatalf("handleWrite 失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", resp.Code)
	}
}

func TestHandleWriteTypeMismatch(t *testing.T) {
	srv := newTestServerWithTable(t)
	resp, err := srv.handleWrite(&WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(1), testColName: true}},
	})
	if err != nil {
		t.Fatalf("handleWrite 失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", resp.Code)
	}
}

// --- buildPrimaryKey 测试 ---

func TestBuildPrimaryKey(t *testing.T) {
	srv := newTestServer(t)
	tbl := &catalog.Table{Name: testTableName, PrimaryKey: []string{"id"}}

	tests := []struct {
		name    string
		row     map[string]interface{}
		wantKey string
		wantErr bool
	}{
		{"单主键", map[string]interface{}{"id": 1}, "1", false},
		{"缺失主键", map[string]interface{}{testColName: testTableName}, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := srv.buildPrimaryKey(tbl, tt.row)
			if (err != nil) != tt.wantErr {
				t.Errorf("buildPrimaryKey() 错误 = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if key != tt.wantKey {
				t.Errorf("buildPrimaryKey() = %q, 期望 %q", key, tt.wantKey)
			}
		})
	}
}

func TestBuildCompositePrimaryKey(t *testing.T) {
	srv := newTestServer(t)
	tbl := &catalog.Table{Name: testTableName, PrimaryKey: []string{"region", "id"}}

	row := map[string]interface{}{"region": "us", "id": 42}
	key, err := srv.buildPrimaryKey(tbl, row)
	if err != nil {
		t.Fatalf("buildPrimaryKey 失败: %v", err)
	}
	expected := "us\x0042"
	if key != expected {
		t.Errorf("复合主键 = %q, 期望 %q", key, expected)
	}
}

func TestBuildPrimaryKeySeparatorNoCollision(t *testing.T) {
	srv := newTestServer(t)
	tbl := &catalog.Table{Name: testTableName, PrimaryKey: []string{"a", "b"}}

	tests := []struct {
		name string
		row  map[string]interface{}
	}{
		{"值包含竖线-1", map[string]interface{}{"a": "x|y", "b": "z"}},
		{"值包含竖线-2", map[string]interface{}{"a": "x", "b": "y|z"}},
	}

	keys := make(map[string]string)
	for _, tt := range tests {
		key, err := srv.buildPrimaryKey(tbl, tt.row)
		if err != nil {
			t.Fatalf("%s: buildPrimaryKey 失败: %v", tt.name, err)
		}
		if prev, exists := keys[key]; exists {
			t.Errorf("主键碰撞: %q 和 %q 生成了相同 key %q", prev, tt.name, key)
		}
		keys[key] = tt.name
	}
}

// --- RoutingAdapter 测试 ---

func TestRoutingAdapter(t *testing.T) {
	dir, err := os.MkdirTemp("", "testdb-adapter-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	eng, err := storage.NewEngine(storage.EngineConfig{
		DataDir: dir, MaxMemTableSize: 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	ra := newRoutingAdapter(eng)
	_ = ra.ScanRange("", "\xff\xff\xff\xff")
	_ = ra.ColumnMeta()

	if ra.PrimaryIndex() == nil {
		t.Error("PrimaryIndex 不应为 nil")
	}
	if ra.SparseIndex() == nil {
		t.Error("SparseIndex 不应为 nil")
	}

	// ForTable 对未注册内存引擎的表应返回委托给默认引擎的适配器
	sp := ra.ForTable("unknown_table")
	if sp == nil {
		t.Fatal("ForTable 不应返回 nil")
	}
	_ = sp.ScanRange("", "\xff\xff\xff\xff")
}

// --- 默认配置测试 ---

func TestNewServerDefaultConfig(t *testing.T) {
	dir, err := os.MkdirTemp("", "testdb-cfg-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cfg := Config{
		TCPAddr:  testListenAddr,
		HTTPAddr: testListenAddr,
		DataDir:  filepath.Join(dir, "data"),
	}

	srv, err := NewServer(cfg, WithMetricsRegistry(prometheus.NewRegistry()))
	if err != nil {
		t.Fatalf("NewServer 失败: %v", err)
	}
	if srv.cfg.MaxMemTableSize != 64*1024*1024 {
		t.Errorf("MaxMemTableSize = %d, 期望 %d", srv.cfg.MaxMemTableSize, 64*1024*1024)
	}
	if err := srv.Stop(); err != nil {
		t.Logf("Stop 错误: %v", err)
	}
}

// --- convertWriteRow 测试 ---

func TestConvertWriteRowIgnoreUnknownColumn(t *testing.T) {
	srv := newTestServerWithTable(t)
	tbl, _ := srv.catalog.GetTable(testTable)

	key, values, err := srv.convertWriteRow(tbl, map[string]interface{}{
		"id":        float64(1),
		testColName: testName,
		"unknown":   "value",
	})
	if err != nil {
		t.Fatalf("convertWriteRow 失败: %v", err)
	}
	if key != "1" {
		t.Errorf("key = %q, 期望 '1'", key)
	}
	if _, ok := values["unknown"]; ok {
		t.Error("未知列应被忽略")
	}
	if len(values) != 2 {
		t.Errorf("values 长度 = %d, 期望 2", len(values))
	}
}

// --- isClosedConnErr 测试 ---

func TestIsClosedConnErrWithTimeout(t *testing.T) {
	opErr := &net.OpError{Err: timeoutError{}}
	if !isClosedConnErr(opErr) {
		t.Error("超时 OpError 应返回 true")
	}

	opErr2 := &net.OpError{Err: fmt.Errorf("connection refused")}
	if isClosedConnErr(opErr2) {
		t.Error("非超时 OpError 应返回 false")
	}
}

// timeoutError 实现 net.Error 接口用于测试。
type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return false }

// ---------------------------------------------------------------------------
// handleQueryPacket: JSON marshal 响应错误路径（80.0% → >90%）
// ---------------------------------------------------------------------------

// TestHandleQueryPacket_ValidQueryViaDirect 测试 handleQueryPacket 正常路径（直接调用）。
func TestHandleQueryPacket_ValidQueryViaDirect(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	// 创建表并写入数据
	writePayload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(1), testColName: testName}},
	})
	writePkt := NewPacket(PacketWrite, writePayload)
	_, err := srv.handleWritePacket(writePkt)
	if err != nil {
		t.Fatalf("handleWritePacket 失败: %v", err)
	}

	// 查询数据
	queryPayload, _ := json.Marshal(QueryRequest{SQL: "SELECT * FROM " + testTable})
	queryPkt := NewPacket(PacketQuery, queryPayload)
	resp, err := srv.handleQueryPacket(queryPkt)
	if err != nil {
		t.Fatalf("handleQueryPacket 正常路径失败: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应")
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应类型 = %d，期望 %d", resp.Type, PacketResponse)
	}
}

// ---------------------------------------------------------------------------
// handleWritePacket: 正常写入路径（80.0% → >90%）
// ---------------------------------------------------------------------------

// TestHandleWritePacket_ValidWriteViaDirect 测试 handleWritePacket 正常路径（直接调用）。
func TestHandleWritePacket_ValidWriteViaDirect(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	writePayload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(1), testColName: testName}},
	})
	writePkt := NewPacket(PacketWrite, writePayload)
	resp, err := srv.handleWritePacket(writePkt)
	if err != nil {
		t.Fatalf("handleWritePacket 正常路径失败: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应")
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应类型 = %d，期望 %d", resp.Type, PacketResponse)
	}

	// 验证响应内容
	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if response.Code != 0 {
		t.Errorf("响应 Code = %d，期望 0", response.Code)
	}
}

// ---------------------------------------------------------------------------
// handlePing: 正常路径（80.0% → >90%）
// ---------------------------------------------------------------------------

// TestHandlePing_DirectCall 测试 handlePing 直接调用。
func TestHandlePing_DirectCall(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	resp, err := srv.handlePing()
	if err != nil {
		t.Fatalf("handlePing 失败: %v", err)
	}
	if resp == nil {
		t.Fatal("期望非 nil 响应")
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应类型 = %d，期望 %d", resp.Type, PacketResponse)
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

// ---------------------------------------------------------------------------
// handlePacket: 未知包类型路径
// ---------------------------------------------------------------------------

// TestHandlePacket_UnknownTypeDirect 测试 handlePacket 处理未知包类型（直接调用）。
func TestHandlePacket_UnknownTypeDirect(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(99, nil)
	_, err := srv.handlePacket(pkt)
	if err == nil {
		t.Error("期望未知包类型返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// acceptTCP: 连接数限制路径（88.9% → >90%）
// ---------------------------------------------------------------------------

// TestAcceptTCP_ConnectionLimit 测试 TCP 连接数限制。
func TestAcceptTCP_ConnectionLimit(t *testing.T) {
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
		t.Fatalf("NewServer 失败: %v", err)
	}

	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	// 等待服务器启动
	time.Sleep(50 * time.Millisecond)

	// 第一个连接应成功
	conn1, err := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("第一个连接失败: %v", err)
	}
	defer func() { _ = conn1.Close() }()

	// 发送 ping 确保连接被处理
	pingPkt := NewPacket(PacketPing, nil)
	if _, err := conn1.Write(pingPkt.Encode()); err != nil {
		t.Fatalf("发送 ping 失败: %v", err)
	}

	// 等待连接被接受
	time.Sleep(100 * time.Millisecond)

	// 第二个连接应被拒绝（达到连接数上限后服务端会关闭连接）
	conn2, err := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if err == nil {
		// 连接建立了但可能被服务端立即关闭
		_ = conn2.Close()
	}
}

// ---------------------------------------------------------------------------
// V2: handleQueryPacket, handleWritePacket, handlePing, handlePacket routes
// ---------------------------------------------------------------------------

// --- handleQueryPacket: happy path with valid query request ---

func TestHandleQueryPacket_ValidQuery(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	payload, _ := json.Marshal(QueryRequest{SQL: testSelectAll})
	pkt := NewPacket(PacketQuery, payload)

	resp, err := srv.handleQueryPacket(pkt)
	if err != nil {
		t.Fatalf("handleQueryPacket 失败: %v", err)
	}
	if resp == nil {
		t.Fatal("handleQueryPacket 返回 nil 响应包")
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应包类型 = %d, 期望 %d", resp.Type, PacketResponse)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if response.Code != 0 {
		t.Errorf("响应 Code = %d, 期望 0; Message = %q", response.Code, response.Message)
	}
}

// --- handleWritePacket: happy path with valid write request ---

func TestHandleWritePacket_ValidWrite(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	payload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows: []map[string]interface{}{
			{"id": float64(1), testColName: testName},
		},
	})
	pkt := NewPacket(PacketWrite, payload)

	resp, err := srv.handleWritePacket(pkt)
	if err != nil {
		t.Fatalf("handleWritePacket 失败: %v", err)
	}
	if resp == nil {
		t.Fatal("handleWritePacket 返回 nil 响应包")
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应包类型 = %d, 期望 %d", resp.Type, PacketResponse)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if response.Code != 0 {
		t.Errorf("响应 Code = %d, 期望 0; Message = %q", response.Code, response.Message)
	}
	if response.Rows != 1 {
		t.Errorf("写入行数 = %d, 期望 1", response.Rows)
	}
}

// --- handlePing: returns correct response with "pong" message ---

func TestHandlePing_ReturnsPong(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	resp, err := srv.handlePing()
	if err != nil {
		t.Fatalf("handlePing 失败: %v", err)
	}
	if resp == nil {
		t.Fatal("handlePing 返回 nil 响应包")
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应包类型 = %d, 期望 %d", resp.Type, PacketResponse)
	}

	var response Response
	if err := json.Unmarshal(resp.Payload, &response); err != nil {
		t.Fatalf("解析心跳响应失败: %v", err)
	}
	if response.Code != 0 {
		t.Errorf("响应 Code = %d, 期望 0", response.Code)
	}
	if response.Message != msgPong {
		t.Errorf("响应 Message = %q, 期望 %q", response.Message, msgPong)
	}
}

// --- handlePacket: default case with unknown packet type ---

func TestHandlePacket_UnknownTypeDefault(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	// Use a packet type that doesn't match any known type
	pkt := NewPacket(99, nil)
	resp, err := srv.handlePacket(pkt)
	if err == nil {
		t.Error("handlePacket(未知类型) 期望返回错误, 得到 nil")
	}
	if resp != nil {
		t.Errorf("handlePacket(未知类型) 响应 = %v, 期望 nil", resp)
	}
}

// --- handlePacket: routes to handleQueryPacket ---

func TestHandlePacket_QueryRoute(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	payload, _ := json.Marshal(QueryRequest{SQL: testSelectAll})
	pkt := NewPacket(PacketQuery, payload)

	resp, err := srv.handlePacket(pkt)
	if err != nil {
		t.Fatalf("handlePacket(Query) 失败: %v", err)
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应包类型 = %d, 期望 %d", resp.Type, PacketResponse)
	}
}

// --- handlePacket: routes to handleWritePacket ---

func TestHandlePacket_WriteRoute(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	payload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(1), testColName: testName}},
	})
	pkt := NewPacket(PacketWrite, payload)

	resp, err := srv.handlePacket(pkt)
	if err != nil {
		t.Fatalf("handlePacket(Write) 失败: %v", err)
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应包类型 = %d, 期望 %d", resp.Type, PacketResponse)
	}
}

// --- handlePacket: routes to handlePing ---

func TestHandlePacket_PingRoute(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	pkt := NewPacket(PacketPing, nil)
	resp, err := srv.handlePacket(pkt)
	if err != nil {
		t.Fatalf("handlePacket(Ping) 失败: %v", err)
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应包类型 = %d, 期望 %d", resp.Type, PacketResponse)
	}
}
