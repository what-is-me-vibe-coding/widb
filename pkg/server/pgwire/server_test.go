package pgwire

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockExecutor 是测试用的 SQL 执行器。
type mockExecutor struct {
	mu      sync.Mutex
	result  *SQLResult
	err     error
	queries []string
}

func (m *mockExecutor) ExecuteSQL(sql string) (*SQLResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queries = append(m.queries, sql)
	if m.err != nil {
		return nil, m.err
	}
	if m.result != nil {
		return m.result, nil
	}
	return &SQLResult{}, nil
}

func (m *mockExecutor) lastQuery() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.queries) == 0 {
		return ""
	}
	return m.queries[len(m.queries)-1]
}

// TestNewServer 验证 NewServer 创建实例。
func TestNewServer(t *testing.T) {
	exec := &mockExecutor{}
	srv := NewServer("127.0.0.1:0", exec)
	if srv == nil {
		t.Fatal("NewServer 返回 nil")
	}
	if srv.addr != "127.0.0.1:0" {
		t.Errorf("addr 期望 127.0.0.1:0, got %s", srv.addr)
	}
	if srv.executor == nil {
		t.Error("executor 不应为 nil")
	}
	if srv.done == nil {
		t.Error("done channel 不应为 nil")
	}
	if srv.listener != nil {
		t.Error("未启动时 listener 应为 nil")
	}
}

// TestServerAddrBeforeStart 验证未启动时 Addr 返回空字符串。
func TestServerAddrBeforeStart(t *testing.T) {
	srv := NewServer("127.0.0.1:0", &mockExecutor{})
	if srv.Addr() != "" {
		t.Errorf("未启动时 Addr 应返回空, got %q", srv.Addr())
	}
}

// TestServerStartStop 验证启动和停止。
func TestServerStartStop(t *testing.T) {
	srv := NewServer("127.0.0.1:0", &mockExecutor{})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	if srv.Addr() == "" {
		t.Error("启动后 Addr 不应为空")
	}
	time.Sleep(20 * time.Millisecond)
	srv.Stop()
}

// TestServerStartInvalidAddr 验证无效地址启动失败。
func TestServerStartInvalidAddr(t *testing.T) {
	// 使用一个无法监听的地址（无效端口）
	srv := NewServer("127.0.0.1:-1", &mockExecutor{})
	err := srv.Start()
	if err == nil {
		srv.Stop()
		t.Fatal("期望 Start 失败, 但成功了")
	}
	if !strings.Contains(err.Error(), "pgwire: listen") {
		t.Errorf("错误信息应包含 'pgwire: listen', got %v", err)
	}
}

// TestServerStopMultipleCalls 验证多次调用 Stop 是安全的。
func TestServerStopMultipleCalls(t *testing.T) {
	srv := NewServer("127.0.0.1:0", &mockExecutor{})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	// 多次调用 Stop 不应 panic
	srv.Stop()
	srv.Stop()
	srv.Stop()
}

// TestServerStopWithoutStart 验证未启动时 Stop 不 panic。
func TestServerStopWithoutStart(t *testing.T) {
	t.Helper()
	srv := NewServer("127.0.0.1:0", &mockExecutor{})
	// 未启动直接 Stop 不应 panic
	srv.Stop()
}

// TestServerAcceptAndHandleConn 验证服务端能接受连接并处理。
func TestServerAcceptAndHandleConn(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{Columns: []string{"v"}, Rows: []map[string]any{{"v": int64(1)}}}}
	srv := NewServer("127.0.0.1:0", exec)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer srv.Stop()
	time.Sleep(20 * time.Millisecond)

	// 建立一个 TCP 连接（仅验证 accept 不报错）
	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("Dial 失败: %v", err)
	}
	defer func() { _ = conn.Close() }()
	// 立即关闭连接，触发 handleConn 退出
	time.Sleep(20 * time.Millisecond)
}

// TestServerConcurrentStop 验证并发 Stop 安全。
func TestServerConcurrentStop(t *testing.T) {
	srv := NewServer("127.0.0.1:0", &mockExecutor{})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			srv.Stop()
		}()
	}
	wg.Wait()
}
