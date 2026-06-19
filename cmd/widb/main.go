// Package main 是 widb 一键启动入口：同进程内启动 server 与交互式 CLI。
//
// 设计要点：
//   - 主 goroutine 运行 CLI REPL，另一 goroutine 启动 server（TCP+HTTP 监听）
//   - CLI 通过 server.ExecuteQuery/ExecuteWrite 进程内调用，零网络开销
//   - 外部客户端仍可通过 TCP/HTTP 连接（server 正常监听）
//   - REPL 退出（\q 或 EOF）或收到信号时优雅关闭 server
//
// 重构说明：原 readMultiLineSQL / handleFormatCommand 已迁移到 pkg/cli，
// 命令行 flag 与配置加载委托给 pkg/cmdutil，与 cmd/server 共享实现。
// cliFlags / readMultiLineSQL / handleFormatCommand 等符号作为薄包装保留，
// 现有测试无需迁移。
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

	"github.com/what-is-me-vibe-coding/test-db/pkg/cli"
	"github.com/what-is-me-vibe-coding/test-db/pkg/cmdutil"
	"github.com/what-is-me-vibe-coding/test-db/pkg/config"
	"github.com/what-is-me-vibe-coding/test-db/pkg/render"
	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

const (
	banner = `widb - 一键启动模式（server + CLI 同进程）
输入 SQL 语句执行查询，输入 \q 退出，输入 \h 查看帮助
外部客户端仍可通过 TCP/HTTP 连接本服务`

	helpText = `可用命令:
  \q              退出客户端并关闭服务
  \h              显示帮助
  \status         显示服务状态
  \addrs          显示监听地址
  \format         显示当前输出格式
  \format <fmt>   切换输出格式: pretty/vertical/json/csv`
)

// cliFlags 封装命令行参数，server 相关 flag 委托给 cmdutil.ServerFlags，
// 仅保留 widb 独有的 execute / format 字段。
type cliFlags struct {
	fs                *flag.FlagSet
	cmd               *cmdutil.ServerFlags
	configPath        *string
	genConfigPath     *string
	tcpAddr           *string
	httpAddr          *string
	pgAddr            *string
	dataDir           *string
	maxMemTableSize   *int64
	enableScheduler   *bool
	flushInterval     *time.Duration
	compactInterval   *time.Duration
	walCleanInterval  *time.Duration
	walCleanThreshold *int64
	execute           *string
	format            *string
}

// newCLIFlags 构建命令行参数集；server 相关 flag 由 cmdutil.ServerFlags 注册，
// widb 额外注册 -e（执行单条 SQL）和 -format（输出格式）。
func newCLIFlags() *cliFlags {
	cmd := cmdutil.NewServerFlags("widb")
	return &cliFlags{
		fs:                cmd.FS,
		cmd:               cmd,
		configPath:        cmd.ConfigPath,
		genConfigPath:     cmd.GenConfigPath,
		tcpAddr:           cmd.TCPAddr,
		httpAddr:          cmd.HTTPAddr,
		pgAddr:            cmd.PGAddr,
		dataDir:           cmd.DataDir,
		maxMemTableSize:   cmd.MaxMemTableSize,
		enableScheduler:   cmd.EnableScheduler,
		flushInterval:     cmd.FlushInterval,
		compactInterval:   cmd.CompactInterval,
		walCleanInterval:  cmd.WALCleanInterval,
		walCleanThreshold: cmd.WALCleanThreshold,
		execute:           cmd.FS.String("e", "", "执行单条 SQL 语句后退出"),
		format:            cmd.FS.String("format", render.FormatPretty, "输出格式: pretty/vertical/json/csv"),
	}
}

// applyOverrides 将显式设置的命令行参数覆盖到配置上。
func (c *cliFlags) applyOverrides(cfg *config.Config) {
	c.cmd.ApplyOverrides(cfg)
}

// toServerConfig 将 YAML 配置转换为服务层配置。
func toServerConfig(cfg config.Config) server.Config {
	return cmdutil.ToServerConfig(cfg)
}

// loadConfig 按分层策略加载配置：默认值 < 配置文件。
func loadConfig(configPath string) (config.Config, error) {
	return cmdutil.LoadConfig(configPath)
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
	if !render.IsValidFormat(*c.format) {
		_, _ = fmt.Fprintf(stderr, "未知输出格式: %s（支持: %s）\n", *c.format, strings.Join(render.SupportedFormats, ", "))
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
		return runOneShot(srv, *c.format, *c.execute, stdout, stderr)
	}
	if cli.IsTerminalReader(stdin) && cli.IsTerminalWriter(stdout) {
		return runREPLTTY(srv, *c.format, stdout)
	}
	return runREPL(srv, *c.format, stdin, stdout)
}

// runOneShot 执行单条 SQL 后退出。
func runOneShot(srv *server.Server, format string, sql string, stdout, stderr io.Writer) int {
	resp, err := srv.ExecuteQuery(sql)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "错误: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, render.Response(resp, format))
	return 0
}

// runREPL 运行交互式 REPL，返回退出码。format 可通过 \format 命令在运行时切换。
func runREPL(srv *server.Server, format string, reader io.Reader, writer io.Writer) int {
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
			if shouldExit := handleCommand(srv, &format, writer, line); shouldExit {
				_, _ = fmt.Fprintln(writer, "再见!")
				return 0
			}
			continue
		}
		sql := readMultiLineSQL(scanner, writer, line)
		if sql == "" {
			continue
		}
		executeAndPrint(srv, format, writer, sql)
	}
	if err := scanner.Err(); err != nil {
		_, _ = fmt.Fprintf(writer, "读取输入失败: %v\n", err)
		return 1
	}
	return 0
}

// readMultiLineSQL 收集多行 SQL（以分号结尾），返回去除分号后的完整语句。
// 实际逻辑由 pkg/cli.ReadMultiLineSQL 提供，本函数为保持历史 API 兼容而保留。
func readMultiLineSQL(scanner *bufio.Scanner, writer io.Writer, firstLine string) string {
	return cli.ReadMultiLineSQL(scanner, writer, firstLine)
}

// runREPLTTY 是 TTY 增强版 REPL：使用 peterh/liner 提供历史/补全，
// 使用 fatih/color 高亮错误/成功/提示符。仅在 stdin/stdout 同时是 TTY 时调用。
//
// 行为与 runREPL 保持一致：反斜杠命令（\q/\h/\status/\addrs/\format）分发，
// 多行 SQL 以分号结束，执行结果通过 render.Response 格式化输出。
func runREPLTTY(srv *server.Server, format string, writer io.Writer) int {
	cli.EnableColorGlobally()
	defer cli.DisableColorGlobally()

	_, _ = fmt.Fprintln(writer, banner)
	_, _ = fmt.Fprintf(writer, "TCP: %s | HTTP: %s\n\n", srv.TCPAddr(), srv.HTTPAddr())

	session, err := cli.NewLinerSession("widb> ", cli.DefaultHistoryFile(), 1000)
	if err != nil {
		_, _ = fmt.Fprintf(writer, "%s: %v\n", cli.ColorizeError("初始化 REPL 失败"), err)
		return 1
	}
	defer session.Close(cli.DefaultHistoryFile())

	formatState := cli.NewFormatState()
	formatState.Set(format)
	prompt := cli.ColorizePrompt("widb> ")
	contPrompt := "  ...> "

	for {
		line, err := session.PromptWith(prompt)
		if err != nil {
			if err == io.EOF {
				return 0
			}
			_, _ = fmt.Fprintf(writer, "%s: %v\n", cli.ColorizeError("读取输入失败"), err)
			return 1
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		session.AppendHistory(trimmed)

		if strings.HasPrefix(trimmed, "\\") {
			if exit, handled := handleCommandTTY(srv, formatState, writer, trimmed); handled {
				if exit {
					_, _ = fmt.Fprintln(writer, cli.ColorizeSuccess("再见!"))
					return 0
				}
				continue
			}
		}

		sql, _ := cli.ReadMultiLineSQLWithLiner(session, contPrompt, trimmed)
		if sql == "" {
			continue
		}
		executeAndPrintTTY(srv, formatState, writer, sql)
	}
}

// handleCommandTTY 在 TTY 模式下处理反斜杠命令。
// 返回 (shouldExit, handled)。当 handled=false 时表示该行不是命令，由 SQL 路径处理。
func handleCommandTTY(srv *server.Server, formatState *cli.FormatState, writer io.Writer, cmd string) (bool, bool) {
	if strings.HasPrefix(cmd, "\\format") {
		before := formatState.Current()
		formatState.HandleCommand(writer, cmd)
		if formatState.Current() != before {
			formatState.Set(formatState.Current())
		}
		return false, true
	}
	switch cmd {
	case "\\q", "\\quit":
		return true, true
	case "\\h", "\\help":
		_, _ = fmt.Fprintln(writer, helpText)
		return false, true
	case "\\status":
		_, _ = fmt.Fprintf(writer, "服务状态: 正常 (%s)\n", srv.Ping())
		return false, true
	case "\\addrs":
		_, _ = fmt.Fprintf(writer, "TCP: %s\nHTTP: %s\nPG: %s\n", srv.TCPAddr(), srv.HTTPAddr(), srv.PGAddr())
		return false, true
	}
	_, _ = fmt.Fprintf(writer, "%s: %s，输入 \\h 查看帮助\n", cli.ColorizeError("未知命令"), cmd)
	return false, true
}

// executeAndPrintTTY 在 TTY 模式下执行 SQL 并打印结果，错误信息用红色高亮。
func executeAndPrintTTY(srv *server.Server, formatState *cli.FormatState, writer io.Writer, sql string) {
	resp, err := srv.ExecuteQuery(sql)
	if err != nil {
		_, _ = fmt.Fprintln(writer, cli.ColorizeError("错误: "+err.Error()))
		return
	}
	_, _ = fmt.Fprintln(writer, render.Response(resp, formatState.Current()))
}

// handleCommand 处理反斜杠命令，返回 true 表示应退出 REPL。
func handleCommand(srv *server.Server, format *string, writer io.Writer, cmd string) bool {
	if strings.HasPrefix(cmd, "\\format") {
		return handleFormatCommand(format, writer, cmd)
	}
	switch cmd {
	case "\\q", "\\quit":
		return true
	case "\\h", "\\help":
		_, _ = fmt.Fprintln(writer, helpText)
	case "\\status":
		_, _ = fmt.Fprintf(writer, "服务状态: 正常 (%s)\n", srv.Ping())
	case "\\addrs":
		_, _ = fmt.Fprintf(writer, "TCP: %s\nHTTP: %s\nPG: %s\n", srv.TCPAddr(), srv.HTTPAddr(), srv.PGAddr())
	default:
		_, _ = fmt.Fprintf(writer, "未知命令: %s，输入 \\h 查看帮助\n", cmd)
	}
	return false
}

// handleFormatCommand 处理 \format 命令：无参数显示当前格式，有参数切换格式。
// 实际逻辑由 pkg/cli.FormatState 提供，本函数为保持历史 API 兼容而保留。
func handleFormatCommand(format *string, writer io.Writer, cmd string) bool {
	state := &cli.FormatState{}
	state.Set(*format)
	before := state.Current()
	state.HandleCommand(writer, cmd)
	// 仅当 FormatState 内部真正修改了格式时才回写到 *format，避免无参/非法命令覆盖。
	if state.Current() != before {
		*format = state.Current()
	}
	return false
}

// executeAndPrint 执行 SQL 并打印结果。
func executeAndPrint(srv *server.Server, format string, writer io.Writer, sql string) {
	resp, err := srv.ExecuteQuery(sql)
	if err != nil {
		_, _ = fmt.Fprintf(writer, "错误: %v\n", err)
		return
	}
	_, _ = fmt.Fprintln(writer, render.Response(resp, format))
}

func main() {
	os.Exit(runMainWithArgs(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
