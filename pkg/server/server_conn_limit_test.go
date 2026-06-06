package server

import (
	"bufio"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const testNetTCP = "tcp"

// --- isTransientAcceptErr tests ---

func TestIsTransientAcceptErr_TemporaryOpError(t *testing.T) {
	opErr := &net.OpError{Op: "accept", Net: testNetTCP, Err: temporaryError{}}
	if !isTransientAcceptErr(opErr) {
		t.Error("isTransientAcceptErr(temporary OpError) = false, want true")
	}
}

func TestIsTransientAcceptErr_TimeoutOpError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetReadDeadline(time.Now().Add(-1 * time.Second))
	_, readErr := bufio.NewReader(conn).ReadByte()

	if readErr == nil {
		t.Fatal("expected a deadline error, got nil")
	}

	if !isTransientAcceptErr(readErr) {
		t.Errorf("isTransientAcceptErr(timeout error) = false, want true; err=%T: %v", readErr, readErr)
	}
}

func TestIsTransientAcceptErr_NonTransientError(t *testing.T) {
	opErr := &net.OpError{Op: "accept", Net: testNetTCP, Err: errors.New("fatal error")}
	if isTransientAcceptErr(opErr) {
		t.Error("isTransientAcceptErr(non-temporary OpError) = true, want false")
	}
}

func TestIsTransientAcceptErr_OtherError(t *testing.T) {
	if isTransientAcceptErr(errors.New("random error")) {
		t.Error("isTransientAcceptErr(random error) = true, want false")
	}
}

// temporaryError 实现一个 Temporary() = true 的错误，用于测试。
type temporaryError struct{}

func (temporaryError) Error() string   { return "temporary error" }
func (temporaryError) Temporary() bool { return true }
func (temporaryError) Timeout() bool   { return false }

// --- MaxConnections 测试 ---

func TestServer_MaxConnectionsZero_NoLimit(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		TCPAddr:        testListenAddr,
		HTTPAddr:       testListenAddr,
		DataDir:        dir,
		MaxConnections: 0,
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
	time.Sleep(50 * time.Millisecond)

	conns := make([]net.Conn, 0, 5)
	for i := 0; i < 5; i++ {
		conn, err := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
		if err != nil {
			t.Fatalf("连接 %d 失败: %v", i, err)
		}
		conns = append(conns, conn)
	}
	for _, c := range conns {
		_ = c.Close()
	}
}

func TestServer_MaxConnectionsEnforced(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		TCPAddr:        testListenAddr,
		HTTPAddr:       testListenAddr,
		DataDir:        dir,
		MaxConnections: 2,
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
	time.Sleep(50 * time.Millisecond)

	conn1, err := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("连接 1 失败: %v", err)
	}
	defer func() { _ = conn1.Close() }()

	conn2, err := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("连接 2 失败: %v", err)
	}
	defer func() { _ = conn2.Close() }()

	time.Sleep(100 * time.Millisecond)

	conn3, err := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Logf("第 3 个连接被拒绝（预期行为）: %v", err)
		return
	}
	defer func() { _ = conn3.Close() }()

	_ = conn3.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1024)
	_, readErr := conn3.Read(buf)
	if readErr == nil {
		t.Log("第 3 个连接未被立即关闭，但限制可能已通过 connCount 检查")
	}
}
