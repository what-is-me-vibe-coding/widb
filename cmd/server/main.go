// Package main 是 test-db 服务器的入口点。
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

// run 启动服务器并等待终止信号，用于支持测试。
func run(tcpAddr, httpAddr, dataDir string, maxMemTableSize int64) error {
	cfg := server.Config{
		TCPAddr:         tcpAddr,
		HTTPAddr:        httpAddr,
		DataDir:         dataDir,
		MaxMemTableSize: maxMemTableSize,
	}

	srv, err := server.NewServer(cfg)
	if err != nil {
		return err
	}

	if err := srv.Start(); err != nil {
		return err
	}

	// 等待终止信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("收到信号 %v，正在关闭...", sig)

	return srv.Stop()
}

func main() {
	tcpAddr := flag.String("tcp", "0.0.0.0:9000", "TCP 监听地址")
	httpAddr := flag.String("http", "0.0.0.0:8080", "HTTP 监听地址")
	dataDir := flag.String("data", "./data", "数据目录")
	maxMemTableSize := flag.Int64("max-memtable", 64*1024*1024, "MemTable 最大字节数")
	flag.Parse()

	if err := run(*tcpAddr, *httpAddr, *dataDir, *maxMemTableSize); err != nil {
		log.Fatalf("服务器错误: %v", err)
	}
}
