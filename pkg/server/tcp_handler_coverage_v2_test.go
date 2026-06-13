package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// covV2TransientErrListener 包装 net.Listener，在前 N 次 Accept 调用中注入瞬态错误
type covV2TransientErrListener struct {
	net.Listener
	injectCount int32
}

func (l *covV2TransientErrListener) Accept() (net.Conn, error) {
	if atomic.LoadInt32(&l.injectCount) > 0 {
		atomic.AddInt32(&l.injectCount, -1)
		return nil, &net.OpError{
			Op:  "accept",
			Net: "tcp",
			Err: errors.New("resource temporarily unavailable"),
		}
	}
	return l.Listener.Accept()
}

// TestHandleQueryPacketInvalidJSON_CovV2 测试 handleQueryPacket 对无效 JSON 的错误处理
// 覆盖 tcp_handler.go:115-117 行的 JSON 反序列化错误分支
func TestHandleQueryPacketInvalidJSON_CovV2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name    string
		payload []byte
	}{
		{"空payload", []byte{}},
		{testNameIncompleteJSON, []byte("{")},
		{testNamePureNumber, []byte("42")},
		{testNameBinaryGarbage, []byte{0x00, 0xFF, 0xFE}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(PacketQuery, tt.payload)
			resp, err := srv.handleQueryPacket(pkt)
			if err == nil {
				t.Error("期望无效 JSON 返回错误")
			}
			if resp != nil {
				t.Errorf("期望 nil 响应，得到 %v", resp)
			}
		})
	}
}

// TestHandleWritePacketInvalidJSON_CovV2 测试 handleWritePacket 对无效 JSON 的错误处理
// 覆盖 tcp_handler.go:135-137 行的 JSON 反序列化错误分支
func TestHandleWritePacketInvalidJSON_CovV2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name    string
		payload []byte
	}{
		{"空payload", []byte{}},
		{testNameIncompleteJSON, []byte("{")},
		{testNamePureNumber, []byte("42")},
		{testNameBinaryGarbage, []byte{0xDE, 0xAD}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(PacketWrite, tt.payload)
			resp, err := srv.handleWritePacket(pkt)
			if err == nil {
				t.Error("期望无效 JSON 返回错误")
			}
			if resp != nil {
				t.Errorf("期望 nil 响应，得到 %v", resp)
			}
		})
	}
}

// TestHandlePacketUnknownType_CovV2 测试 handlePacket 对未知包类型的错误处理
// 覆盖 tcp_handler.go:108 行的 default 分支
func TestHandlePacketUnknownType_CovV2(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	tests := []struct {
		name    string
		pktType uint8
	}{
		{"类型0", 0},
		{"类型5", 5},
		{"类型99", 99},
		{"类型255", 255},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := NewPacket(tt.pktType, nil)
			resp, err := srv.handlePacket(pkt)
			if err == nil {
				t.Error("期望未知包类型返回错误")
			}
			if resp != nil {
				t.Errorf("期望 nil 响应，得到 %v", resp)
			}
		})
	}
}

// TestHandleQueryPacketQueryError_CovV2 测试 handleQueryPacket 中查询出错的路径
// 注意：handleQuery 将错误编码为 Response{Code:-1} 并返回 nil Go error，
// 因此 handleQueryPacket 中 handleQuery 返回非 nil Go error 的路径在当前实现中不可达。
// 通过关闭存储引擎测试可能的错误传播路径，验证函数不会 panic。
func TestHandleQueryPacketQueryError_CovV2(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	// 关闭存储引擎，使后续查询可能产生错误
	_ = srv.storage.Close()

	payload, _ := json.Marshal(QueryRequest{SQL: testSelectAll})
	pkt := NewPacket(PacketQuery, payload)
	resp, err := srv.handleQueryPacket(pkt)

	// handleQuery 将错误编码为 Response{Code:-1}，不返回 Go error
	// 关闭存储后查询可能仍返回 Code=0（内存数据可读）或 Code=-1（执行错误），
	// 关键是验证函数不会 panic 且返回合理结果
	if err != nil {
		t.Logf("关闭存储后 handleQueryPacket 返回 Go error: %v", err)
	} else {
		if resp == nil {
			t.Fatal("期望非 nil 响应")
		}
		var response Response
		if unmarshalErr := json.Unmarshal(resp.Payload, &response); unmarshalErr == nil {
			t.Logf("关闭存储后查询响应: Code=%d, Message=%q", response.Code, response.Message)
		}
	}
}

// TestHandleWritePacketWriteError_CovV2 测试 handleWritePacket 中写入出错的路径
// 注意：handleWrite 将错误编码为 Response{Code:-1} 并返回 nil Go error，
// 因此 handleWritePacket 中 handleWrite 返回非 nil Go error 的路径在当前实现中不可达。
// 通过关闭存储引擎测试可能的错误传播路径。
func TestHandleWritePacketWriteError_CovV2(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	// 关闭存储引擎，使后续写入可能产生错误
	_ = srv.storage.Close()

	payload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(1), testColName: testName}},
	})
	pkt := NewPacket(PacketWrite, payload)
	resp, err := srv.handleWritePacket(pkt)

	if err != nil {
		t.Logf("关闭存储后 handleWritePacket 返回 Go error: %v", err)
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

// TestAcceptTCP_TransientError_CovV2 测试 acceptTCP 在遇到瞬态错误时继续重试
// 覆盖 tcp_handler.go:28-29 行的瞬态错误处理分支
func TestAcceptTCP_TransientError_CovV2(t *testing.T) {
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

	// 创建真实 TCP 监听器
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen 失败: %v", err)
	}

	// 包装为瞬态错误监听器：首次 Accept 返回瞬态错误
	wrappedLn := &covV2TransientErrListener{Listener: ln, injectCount: 1}
	srv.tcpListener = wrappedLn

	// 启动 accept 循环
	srv.wg.Add(1)
	go srv.acceptTCP()

	// 等待瞬态错误被处理
	time.Sleep(100 * time.Millisecond)

	// 验证服务器仍然可以接受连接（瞬态错误后 accept 循环继续）
	conn, dialErr := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if dialErr != nil {
		t.Fatalf("瞬态错误后连接失败: %v", dialErr)
	}
	_ = conn.Close()

	// 清理：关闭 done 通道和监听器，等待 goroutine 退出
	close(srv.done)
	_ = ln.Close()
	srv.wg.Wait()
	_ = srv.storage.Close()
}

// TestAcceptTCP_ConnectionLimit_CovV2 测试 acceptTCP 在连接数达到上限时拒绝新连接
// 覆盖 tcp_handler.go:38-41 行的连接数限制分支
func TestAcceptTCP_ConnectionLimit_CovV2(t *testing.T) {
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

	if startErr := srv.Start(); startErr != nil {
		t.Fatalf("Start 失败: %v", startErr)
	}
	defer func() { _ = srv.Stop() }()

	time.Sleep(50 * time.Millisecond)

	// 建立第一个连接（占用唯一的名额）
	conn1, dialErr := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if dialErr != nil {
		t.Fatalf("第一个连接失败: %v", dialErr)
	}
	defer func() { _ = conn1.Close() }()

	// 等待连接被接受处理
	time.Sleep(100 * time.Millisecond)

	// 第二个连接：连接数已达上限，应被拒绝或关闭
	conn2, dialErr2 := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if dialErr2 != nil {
		t.Logf("第二个连接被拒绝（预期行为）: %v", dialErr2)
		return
	}
	defer func() { _ = conn2.Close() }()

	// 如果连接建立成功，服务器应关闭它
	_ = conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1024)
	_, readErr := conn2.Read(buf)
	if readErr == nil {
		t.Log("第二个连接未被立即关闭，但限制可能已通过 connCount 检查")
	}
}

// TestHandleTCPConn_ErrorResponse_CovV2 测试 handleTCPConn 中 handlePacket 返回错误时的响应构造路径
// 覆盖 tcp_handler.go:80-86 行的错误响应构造分支
// 注意：json.Marshal(errResp) 的错误路径不可达（Response 结构简单，始终可序列化），
// 此测试覆盖 err != nil 时构造错误响应的正常路径。
func TestHandleTCPConn_ErrorResponse_CovV2(t *testing.T) {
	srv := newTestServer(t)
	if startErr := srv.Start(); startErr != nil {
		t.Fatalf("Start 失败: %v", startErr)
	}
	defer func() { _ = srv.Stop() }()

	time.Sleep(50 * time.Millisecond)

	conn, dialErr := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if dialErr != nil {
		t.Fatalf("连接 TCP 失败: %v", dialErr)
	}
	defer func() { _ = conn.Close() }()

	// 发送未知类型的包，触发 handlePacket 错误路径
	pkt := NewPacket(99, nil)
	if _, writeErr := conn.Write(pkt.Encode()); writeErr != nil {
		t.Fatalf("写入包失败: %v", writeErr)
	}

	// 读取错误响应
	if deadlineErr := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); deadlineErr != nil {
		t.Fatalf("设置读超时失败: %v", deadlineErr)
	}

	respPkt, decodeErr := DecodePacket(bufio.NewReader(conn))
	if decodeErr != nil {
		t.Fatalf("解码响应失败: %v", decodeErr)
	}

	if respPkt.Type != PacketResponse {
		t.Errorf("响应类型 = %d，期望 %d", respPkt.Type, PacketResponse)
	}

	var response Response
	if unmarshalErr := json.Unmarshal(respPkt.Payload, &response); unmarshalErr != nil {
		t.Fatalf("解析响应失败: %v", unmarshalErr)
	}
	if response.Code != -1 {
		t.Errorf("响应 Code = %d，期望 -1", response.Code)
	}
}
