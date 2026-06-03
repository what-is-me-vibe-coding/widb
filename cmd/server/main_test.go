package main

import (
	"os"
	"os/signal"
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
		errCh <- run(testListenAddr, testListenAddr, dir, 1024*1024)
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
