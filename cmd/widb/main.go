// Package main 是 widb 一键启动入口：同进程内启动 server 与交互式 CLI。
//
// 设计要点：
//   - 主 goroutine 运行 CLI REPL，另一 goroutine 启动 server（TCP+HTTP 监听）
//   - CLI 通过 server.ExecuteQuery/ExecuteWrite 进程内调用，零网络开销
//   - 外部客户端仍可通过 TCP/HTTP 连接（server 正常监听）
//   - REPL 退出（\q 或 EOF）或收到信号时优雅关闭 server
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/what-is-me-vibe-coding/test-db/pkg/config"
	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

const (
	banner = `widb - 一键启动模式（server + CLI 同进程）
输入 SQL 语句执行查询，输入 \q 退出，输入 \h 查看帮助
外部客户端仍可通过 TCP/HTTP 连接本服务`

	helpText = `可用命令:
  \q          退出客户端并关闭服务
  \h          显示帮助
  \status     显示服务状态
  \addrs      显示监听地址`
)

// cliFlags 封装命令行参数，语义与 cmd/server 一致。
type cliFlags struct {
	fs                *flag.FlagSet
	configPath        *string
	genConfigPath     *string
	tcpAddr           *string
	httpAddr          *string
	dataDir           *string
	maxMemTableSize   *int64
	enableScheduler   *bool
	flushInterval     *time.Duration
	compactInterval   *time.Duration
	walCleanInterval  *time.Duration
	walCleanThreshold *int64
	execute           *string
}

// newCLIFlags 构建命令行参数集。
func newCLIFlags() *cliFlags {
	fs := flag.NewFlagSet("widb", flag.ContinueOnError)
	return &cliFlags{
		fs:                fs,
		configPath:        fs.String("config", "", "配置文件路径（YAML），未指定时依次查找 ./widb.yaml、./config.yaml"),
		genConfigPath:     fs.String("gen-config", "", "生成带注释的默认配置模板到指定路径后退出"),
		tcpAddr:           fs.String("tcp", "", "TCP 监听地址（覆盖配置文件）"),
		httpAddr:          fs.String("http", "", "HTTP 监听地址（覆盖配置文件）"),
		dataDir:           fs.String("data", "", "数据目录（覆盖配置文件）"),
		maxMemTableSize:   fs.Int64("max-memtable", 0, "MemTable 最大字节数（覆盖配置文件）"),
		enableScheduler:   fs.Bool("scheduler", false, "启用后台调度器（覆盖配置文件）"),
		flushInterval:     fs.Duration("scheduler.flush-interval", 0, "自动刷盘检查间隔（覆盖配置文件）"),
		compactInterval:   fs.Duration("scheduler.compact-interval", 0, "自动 Compaction 检查间隔（覆盖配置文件）"),
		walCleanInterval:  fs.Duration("scheduler.wal-clean-interval", 0, "WAL 清理检查间隔（覆盖配置文件）"),
		walCleanThreshold: fs.Int64("scheduler.wal-clean-threshold", 0, "WAL 文件大小阈值（覆盖配置文件）"),
		execute:           fs.String("e", "", "执行单条 SQL 语句后退出"),
	}
}

// applyOverrides 将显式设置的命令行参数覆盖到配置上。
func (c *cliFlags) applyOverrides(cfg *config.Config) {
	set := make(map[string]bool, 11)
	c.fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["tcp"] {
		cfg.Server.TCPAddr = *c.tcpAddr
	}
	if set["http"] {
		cfg.Server.HTTPAddr = *c.httpAddr
	}
	if set["data"] {
		cfg.Storage.DataDir = *c.dataDir
	}
	if set["max-memtable"] {
		cfg.Storage.MaxMemTableSize = *c.maxMemTableSize
	}
	if set["scheduler"] {
		cfg.Scheduler.Enabled = *c.enableScheduler
	}
	if set["scheduler.flush-interval"] {
		cfg.Scheduler.FlushInterval = config.Duration(*c.flushInterval)
	}
	if set["scheduler.compact-interval"] {
		cfg.Scheduler.CompactInterval = config.Duration(*c.compactInterval)
	}
	if set["scheduler.wal-clean-interval"] {
		cfg.Scheduler.WALCleanInterval = config.Duration(*c.walCleanInterval)
	}
	if set["scheduler.wal-clean-threshold"] {
		cfg.Scheduler.WALCleanThreshold = *c.walCleanThreshold
	}
}

// toServerConfig 将 YAML 配置转换为服务层配置。
func toServerConfig(cfg config.Config) server.Config {
	return server.Config{
		TCPAddr:         cfg.Server.TCPAddr,
		HTTPAddr:        cfg.Server.HTTPAddr,
		DataDir:         cfg.Storage.DataDir,
		MaxMemTableSize: cfg.Storage.MaxMemTableSize,
		EnableScheduler: cfg.Scheduler.Enabled,
		SchedulerConfig: storage.SchedulerConfig{
			FlushInterval:     time.Duration(cfg.Scheduler.FlushInterval),
			CompactInterval:   time.Duration(cfg.Scheduler.CompactInterval),
			WALCleanInterval:  time.Duration(cfg.Scheduler.WALCleanInterval),
			WALCleanThreshold: cfg.Scheduler.WALCleanThreshold,
		},
	}
}

// loadConfig 按分层策略加载配置：默认值 < 配置文件。
func loadConfig(configPath string) (config.Config, error) {
	resolved := config.ResolvePath(configPath, ".")
	if resolved == "" {
		log.Printf("未找到配置文件，使用默认配置（可用 -gen-config widb.yaml 生成模板）")
		return config.Default(), nil
	}
	return config.Load(resolved)
}

// runMainWithArgs 解析参数、启动服务并运行 REPL，返回退出码。
func runMainWithArgs(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	c := newCLIFlags()
	if err := c.fs.Parse(args); err != nil {
		return 2
	}

	if *c.genConfigPath != "" {
		if err := config.GenerateTemplate(*c.genConfigPath); err != nil {
			log.Printf("生成配置模板失败: %v", err)
			return 1
		}
		log.Printf("已生成配置模板: %s", *c.genConfigPath)
		return 0
	}

	cfg, err := loadConfig(*c.configPath)
	if err != nil {
		log.Printf("加载配置失败: %v", err)
		return 1
	}
	c.applyOverrides(&cfg)
	if err := cfg.Validate(); err != nil {
		log.Printf("配置不合法: %v", err)
		return 1
	}

	srv, err := server.NewServer(toServerConfig(cfg), server.WithMetricsRegistry(prometheus.NewRegistry()))
	if err != nil {
		log.Printf("服务器错误: %v", err)
		return 1
	}
	if err := srv.Start(); err != nil {
		log.Printf("服务器错误: %v", err)
		return 1
	}
	defer func() { _ = srv.Stop() }()

	// 信号处理：SIGINT/SIGTERM 触发优雅关闭
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("收到信号 %v，正在关闭...", sig)
		_ = srv.Stop()
		os.Exit(0)
	}()

	log.Printf("TCP 监听 %s | HTTP 监听 %s", srv.TCPAddr(), srv.HTTPAddr())

	if *c.execute != "" {
		return runOneShot(srv, *c.execute, stdout, stderr)
	}
	return runREPL(srv, stdin, stdout)
}

// runOneShot 执行单条 SQL 后退出。
func runOneShot(srv *server.Server, sql string, stdout, stderr io.Writer) int {
	resp, err := srv.ExecuteQuery(sql)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "错误: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, server.FormatResponse(resp))
	return 0
}

// runREPL 运行交互式 REPL，返回退出码。
func runREPL(srv *server.Server, reader io.Reader, writer io.Writer) int {
	_, _ = fmt.Fprintln(writer, banner)
	_, _ = fmt.Fprintf(writer, "TCP: %s | HTTP: %s\n\n", srv.TCPAddr(), srv.HTTPAddr())

	scanner := bufio.NewScanner(reader)
	for {
		_, _ = fmt.Fprint(writer, "widb> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "\\") {
			if shouldExit := handleCommand(srv, writer, line); shouldExit {
				_, _ = fmt.Fprintln(writer, "再见!")
				return 0
			}
			continue
		}
		sql := readMultiLineSQL(scanner, writer, line)
		if sql == "" {
			continue
		}
		executeAndPrint(srv, writer, sql)
	}
	if err := scanner.Err(); err != nil {
		_, _ = fmt.Fprintf(writer, "读取输入失败: %v\n", err)
		return 1
	}
	return 0
}

// readMultiLineSQL 收集多行 SQL（以分号结尾），返回去除分号后的完整语句。
func readMultiLineSQL(scanner *bufio.Scanner, writer io.Writer, firstLine string) string {
	sql := firstLine
	for !strings.HasSuffix(sql, ";") {
		_, _ = fmt.Fprint(writer, "  ...> ")
		if !scanner.Scan() {
			break
		}
		sql += " " + scanner.Text()
	}
	return strings.TrimSuffix(strings.TrimSpace(sql), ";")
}

// handleCommand 处理反斜杠命令，返回 true 表示应退出 REPL。
func handleCommand(srv *server.Server, writer io.Writer, cmd string) bool {
	switch cmd {
	case "\\q", "\\quit":
		return true
	case "\\h", "\\help":
		_, _ = fmt.Fprintln(writer, helpText)
	case "\\status":
		_, _ = fmt.Fprintf(writer, "服务状态: 正常 (%s)\n", srv.Ping())
	case "\\addrs":
		_, _ = fmt.Fprintf(writer, "TCP: %s\nHTTP: %s\n", srv.TCPAddr(), srv.HTTPAddr())
	default:
		_, _ = fmt.Fprintf(writer, "未知命令: %s，输入 \\h 查看帮助\n", cmd)
	}
	return false
}

// executeAndPrint 执行 SQL 并打印结果。
func executeAndPrint(srv *server.Server, writer io.Writer, sql string) {
	resp, err := srv.ExecuteQuery(sql)
	if err != nil {
		_, _ = fmt.Fprintf(writer, "错误: %v\n", err)
		return
	}
	_, _ = fmt.Fprintln(writer, server.FormatResponse(resp))
}

func main() {
	os.Exit(runMainWithArgs(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
