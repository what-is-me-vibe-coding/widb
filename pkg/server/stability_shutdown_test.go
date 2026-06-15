package server

import (
	"bufio"
	"net"
	"os"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// newShutdownTestServer 创建用于关闭测试的服务器实例。
func newShutdownTestServer(t *testing.T) *Server {
	t.Helper()

	dir, err := os.MkdirTemp("", "testdb-shutdown-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

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

	return srv
}

// TestShutdownClosesActiveConnections 验证 Stop() 会主动关闭所有已建立的 TCP 连接，
// 而不是等待 30 秒的读超时。
// 修复前：Stop() 只关闭监听器并等待 wg.Wait()，如果客户端连接空闲，
// handleTCPConn 会阻塞在 30 秒的读超时上，导致 Stop() 长时间不返回。
// 修复后：Stop() 调用 closeAllConns() 主动关闭所有已跟踪的连接，
// 使 handleTCPConn 立即退出，Stop() 快速完成。
func TestShutdownClosesActiveConnections(t *testing.T) {
	srv := newShutdownTestServer(t)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	// 等待服务器启动
	time.Sleep(50 * time.Millisecond)

	// 建立 TCP 连接
	conn, err := net.DialTimeout("tcp", srv.TCPAddr(), 2*time.Second)
	if err != nil {
		t.Fatalf("连接 TCP 失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// 发送 Ping 确认连接已建立并被服务器跟踪
	pingPkt := NewPacket(PacketPing, nil)
	if _, err := conn.Write(pingPkt.Encode()); err != nil {
		t.Fatalf("写入 Ping 包失败: %v", err)
	}

	resp, err := DecodePacket(bufio.NewReader(conn))
	if err != nil {
		t.Fatalf("读取 Ping 响应失败: %v", err)
	}
	if resp.Type != PacketResponse {
		t.Errorf("响应类型 = %d, 期望 %d", resp.Type, PacketResponse)
	}

	// 调用 Stop() 并验证它在短时间内完成
	// 如果没有连接跟踪，Stop() 需要等待 30 秒读超时才能返回
	done := make(chan error, 1)
	go func() {
		done <- srv.Stop()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop 失败: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop 在 5 秒内未完成 - 连接跟踪可能未生效，仍在等待读超时")
	}

	// 验证连接已被关闭
	conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 1)
	_, err = conn.Read(buf)
	if err == nil {
		t.Error("期望连接在 Stop() 后已关闭，但仍然可以读取数据")
	}
}

// TestShutdownMultipleConnections 验证 Stop() 能同时关闭多个活跃的 TCP 连接。
func TestShutdownMultipleConnections(t *testing.T) {
	srv := newShutdownTestServer(t)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// 建立多个 TCP 连接
	const numConns = 3
	conns := make([]net.Conn, numConns)
	for i := 0; i < numConns; i++ {
		conn, err := net.DialTimeout("tcp", srv.TCPAddr(), 2*time.Second)
		if err != nil {
			t.Fatalf("连接 %d TCP 失败: %v", i, err)
		}
		conns[i] = conn
	}
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()

	// 对每个连接发送 Ping 确认已建立
	for i, conn := range conns {
		pingPkt := NewPacket(PacketPing, nil)
		if _, err := conn.Write(pingPkt.Encode()); err != nil {
			t.Fatalf("连接 %d 写入 Ping 包失败: %v", i, err)
		}
		reader := bufio.NewReader(conn)
		resp, err := DecodePacket(reader)
		if err != nil {
			t.Fatalf("连接 %d 读取 Ping 响应失败: %v", i, err)
		}
		if resp.Type != PacketResponse {
			t.Errorf("连接 %d 响应类型 = %d, 期望 %d", i, resp.Type, PacketResponse)
		}
	}

	// 调用 Stop() 并验证快速完成
	done := make(chan error, 1)
	go func() {
		done <- srv.Stop()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop 失败: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop 在 5 秒内未完成 - 多连接关闭可能未生效")
	}

	// 验证所有连接都已关闭
	for i, conn := range conns {
		conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		buf := make([]byte, 1)
		_, err := conn.Read(buf)
		if err == nil {
			t.Errorf("连接 %d 期望已关闭，但仍然可以读取数据", i)
		}
	}
}

// TestShutdownIdleConnection 验证即使连接空闲（未发送任何数据），
// Stop() 也能快速关闭连接，不会等待 30 秒读超时。
func TestShutdownIdleConnection(t *testing.T) {
	srv := newShutdownTestServer(t)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// 建立连接但不发送任何数据，模拟空闲连接
	conn, err := net.DialTimeout("tcp", srv.TCPAddr(), 2*time.Second)
	if err != nil {
		t.Fatalf("连接 TCP 失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// 等待服务器接受连接并开始跟踪
	time.Sleep(100 * time.Millisecond)

	// 调用 Stop() 并验证快速完成
	done := make(chan error, 1)
	go func() {
		done <- srv.Stop()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop 失败: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop 在 5 秒内未完成 - 空闲连接可能未被主动关闭")
	}

	// 验证连接已被关闭
	conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 1)
	_, err = conn.Read(buf)
	if err == nil {
		t.Error("期望空闲连接在 Stop() 后已关闭")
	}
}
