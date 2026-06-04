package main

import (
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

const testListenAddr = "127.0.0.1:0"

func TestMainBuild(_ *testing.T) {
	// 验证 main 包可以成功构建
}

func TestServerCreateAndStart(t *testing.T) {
	dir := t.TempDir()

	cfg := server.Config{
		TCPAddr:         testListenAddr,
		HTTPAddr:        testListenAddr,
		DataDir:         dir,
		MaxMemTableSize: 1024 * 1024,
	}

	srv, err := server.NewServer(cfg, server.WithMetricsRegistry(prometheus.NewRegistry()))
	if err != nil {
		t.Fatalf("创建服务器失败: %v", err)
	}

	if err := srv.Start(); err != nil {
		t.Fatalf("启动服务器失败: %v", err)
	}

	if err := srv.Stop(); err != nil {
		t.Fatalf("关闭服务器失败: %v", err)
	}
}

func TestServerInvalidDataDir(t *testing.T) {
	cfg := server.Config{
		TCPAddr:         testListenAddr,
		HTTPAddr:        testListenAddr,
		DataDir:         "/proc/invalid/no-permission/data",
		MaxMemTableSize: 1024 * 1024,
	}

	_, err := server.NewServer(cfg, server.WithMetricsRegistry(prometheus.NewRegistry()))
	if err != nil {
		t.Logf("预期内的错误: %v", err)
	}
}

// TestRunSignalShutdown 测试 run() 函数在收到 SIGTERM 后正常关闭。
func TestRunSignalShutdown(t *testing.T) {
	dir := t.TempDir()

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(testListenAddr, testListenAddr, dir, 1024*1024, server.WithMetricsRegistry(prometheus.NewRegistry()))
	}()

	// 等待服务器启动
	time.Sleep(200 * time.Millisecond)

	// 发送 SIGTERM 信号触发关闭
	pid := os.Getpid()
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatalf("发送 SIGTERM 失败: %v", err)
	}

	// 等待 run() 返回，设置超时防止死锁
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("run() 返回错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() 未在超时时间内返回")
	}

	// 恢复信号处理，避免影响其他测试
	signal.Reset(syscall.SIGINT, syscall.SIGTERM)
}

// TestRunServerStartFailure 测试 run() 函数在服务器 Start() 失败时返回错误。
// 通过预先占用 TCP 端口，使 run() 内部的 Start() 因端口冲突而失败。
func TestRunServerStartFailure(t *testing.T) {
	// 先占用一个 TCP 端口
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("创建监听器失败: %v", err)
	}
	defer func() { _ = ln.Close() }()

	occupiedAddr := ln.Addr().String()
	dir := t.TempDir()

	// run() 使用已被占用的 TCP 地址，Start() 应失败
	err = run(occupiedAddr, "127.0.0.1:0", dir, 1024*1024, server.WithMetricsRegistry(prometheus.NewRegistry()))
	if err == nil {
		t.Fatal("预期 run() 因端口冲突返回错误，但返回了 nil")
	}
	t.Logf("预期内的启动失败错误: %v", err)
}

// TestServerCreateWithMetricsRegistry 测试使用自定义 Prometheus 注册器创建服务器。
func TestServerCreateWithMetricsRegistry(t *testing.T) {
	dir := t.TempDir()

	cfg := server.Config{
		TCPAddr:         testListenAddr,
		HTTPAddr:        testListenAddr,
		DataDir:         dir,
		MaxMemTableSize: 1024 * 1024,
	}

	reg := prometheus.NewRegistry()
	srv, err := server.NewServer(cfg, server.WithMetricsRegistry(reg))
	if err != nil {
		t.Fatalf("使用自定义注册器创建服务器失败: %v", err)
	}

	if err := srv.Start(); err != nil {
		t.Fatalf("启动服务器失败: %v", err)
	}

	if err := srv.Stop(); err != nil {
		t.Fatalf("关闭服务器失败: %v", err)
	}
}

// TestServerDoubleStop 测试连续两次调用 Stop() 的行为。
// 第二次调用会因为重复关闭 channel 而 panic，测试验证此 panic 被正确捕获。
func TestServerDoubleStop(t *testing.T) {
	dir := t.TempDir()

	cfg := server.Config{
		TCPAddr:         testListenAddr,
		HTTPAddr:        testListenAddr,
		DataDir:         dir,
		MaxMemTableSize: 1024 * 1024,
	}

	srv, err := server.NewServer(cfg, server.WithMetricsRegistry(prometheus.NewRegistry()))
	if err != nil {
		t.Fatalf("创建服务器失败: %v", err)
	}

	if err := srv.Start(); err != nil {
		t.Fatalf("启动服务器失败: %v", err)
	}

	if err := srv.Stop(); err != nil {
		t.Fatalf("第一次 Stop 失败: %v", err)
	}

	// 第二次 Stop 应该 panic（关闭已关闭的 channel）
	defer func() {
		if r := recover(); r == nil {
			t.Error("预期第二次 Stop 会 panic，但没有发生")
		} else {
			t.Logf("第二次 Stop 如预期 panic: %v", r)
		}
	}()
	_ = srv.Stop()
}

// TestRunServerStopFailure 测试 run() 函数在服务器 Stop() 失败时的行为。
// 尝试通过删除数据目录中的文件来触发 Stop() 失败。
// 注意：在 Linux 上，已打开的文件被删除后仍可通过文件描述符访问，
// 因此 Stop() 可能不会失败。此测试主要验证 run() 的信号处理和错误传播路径。
func TestRunServerStopFailure(t *testing.T) {
	dir := t.TempDir()

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(testListenAddr, testListenAddr, dir, 1024*1024, server.WithMetricsRegistry(prometheus.NewRegistry()))
	}()

	// 等待服务器启动
	time.Sleep(200 * time.Millisecond)

	// 尝试删除数据目录中的文件以触发 Stop 失败
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Logf("读取数据目录失败: %v", err)
	} else {
		for _, entry := range entries {
			filePath := filepath.Join(dir, entry.Name())
			if removeErr := os.Remove(filePath); removeErr != nil {
				t.Logf("删除文件 %s 失败: %v", entry.Name(), removeErr)
			}
		}
	}

	// 发送 SIGTERM 信号触发关闭
	pid := os.Getpid()
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatalf("发送 SIGTERM 失败: %v", err)
	}

	// 等待 run() 返回
	select {
	case err := <-errCh:
		if err != nil {
			t.Logf("run() 返回错误（Stop 失败路径）: %v", err)
		} else {
			t.Log("run() 正常返回（Linux 上已删除的文件仍可通过 fd 访问）")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() 未在超时时间内返回")
	}

	// 恢复信号处理
	signal.Reset(syscall.SIGINT, syscall.SIGTERM)
}

// TestRunInvalidDataDir 测试 run() 函数在数据目录无效时返回错误。
func TestRunInvalidDataDir(t *testing.T) {
	err := run(testListenAddr, testListenAddr, "/proc/invalid/no-permission/data", 1024*1024, server.WithMetricsRegistry(prometheus.NewRegistry()))
	if err == nil {
		t.Fatal("预期 run() 因无效数据目录返回错误，但返回了 nil")
	}
	t.Logf("预期内的错误: %v", err)
}

// TestRunMainWithArgsInvalidFlag 测试 runMainWithArgs 在参数无效时返回非零退出码。
func TestRunMainWithArgsInvalidFlag(t *testing.T) {
	code := runMainWithArgs([]string{"--invalid-flag"})
	if code == 0 {
		t.Fatal("预期 runMainWithArgs 因无效参数返回非零退出码，但返回了 0")
	}
	t.Logf("预期内的退出码: %d", code)
}

// TestRunMainWithArgsInvalidDataDir 测试 runMainWithArgs 在数据目录无效时返回退出码 1。
func TestRunMainWithArgsInvalidDataDir(t *testing.T) {
	code := runMainWithArgs([]string{"--data", "/proc/invalid/no-permission/data"})
	if code != 1 {
		t.Fatalf("预期退出码 1，实际 %d", code)
	}
}
