// Package main 是 widb-cli 命令行客户端的入口点。
//
// 重构说明：readMultiLineSQL 与 handleFormatCommand 已迁移到 pkg/cli，
// 本文件保留同名方法作为薄包装，便于现有测试直接调用。
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	clishared "github.com/what-is-me-vibe-coding/test-db/pkg/cli"
	"github.com/what-is-me-vibe-coding/test-db/pkg/render"
	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

const (
	modeTCP  = "tcp"
	modeHTTP = "http"

	banner = `widb-cli - WiDB 命令行客户端
输入 SQL 语句执行查询，输入 \q 退出，输入 \h 查看帮助`
	helpText = `可用命令:
  \q              退出客户端
  \h              显示帮助
  \status         显示服务器状态
  \use HTTP       切换到 HTTP 模式
  \use TCP        切换到 TCP 模式
  \format         显示当前输出格式
  \format <fmt>   切换输出格式: pretty/vertical/json/csv`
)

// cli 是命令行客户端。
type cli struct {
	mode     string // "tcp" 或 "http"
	tcpAddr  string
	httpAddr string
	conn     net.Conn
	httpCli  *http.Client
	format   string // 输出格式：pretty/vertical/json/csv
}

func newCLI(tcpAddr, httpAddr string, mode string) *cli {
	return &cli{
		mode:     mode,
		tcpAddr:  tcpAddr,
		httpAddr: httpAddr,
		httpCli:  &http.Client{Timeout: 30 * time.Second},
		format:   render.FormatPretty,
	}
}

// connect 建立 TCP 连接。
func (c *cli) connect() error {
	conn, err := net.DialTimeout("tcp", c.tcpAddr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("连接 %s 失败: %w", c.tcpAddr, err)
	}
	c.conn = conn
	return nil
}

// close 关闭连接。
func (c *cli) close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// execute 执行 SQL 语句并返回格式化结果。
func (c *cli) execute(sql string) (string, error) {
	switch c.mode {
	case modeTCP:
		return c.executeTCP(sql)
	case modeHTTP:
		return c.executeHTTP(sql)
	default:
		return "", fmt.Errorf("未知模式: %s", c.mode)
	}
}

// executeTCP 通过 TCP 协议执行查询。
func (c *cli) executeTCP(sql string) (string, error) {
	if c.conn == nil {
		if err := c.connect(); err != nil {
			return "", err
		}
	}

	req := server.QueryRequest{SQL: sql}
	payload, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("序列化请求失败: %w", err)
	}

	pkt := server.NewPacket(server.PacketQuery, payload)
	if _, err := c.conn.Write(pkt.Encode()); err != nil {
		c.conn = nil
		return "", fmt.Errorf("发送请求失败: %w", err)
	}

	respPkt, err := server.DecodePacket(c.conn)
	if err != nil {
		c.conn = nil
		return "", fmt.Errorf("读取响应失败: %w", err)
	}

	var resp server.Response
	if err := json.Unmarshal(respPkt.Payload, &resp); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}

	return render.Response(&resp, c.format), nil
}

// executeHTTP 通过 HTTP REST API 执行查询。
func (c *cli) executeHTTP(sql string) (string, error) {
	reqBody, _ := json.Marshal(server.QueryRequest{SQL: sql})
	url := fmt.Sprintf("http://%s/query", c.httpAddr)

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpCli.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}

	var resp server.Response
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}

	return render.Response(&resp, c.format), nil
}

// pingTCP 通过 TCP 发送心跳检测。
func (c *cli) pingTCP() (string, error) {
	if c.conn == nil {
		if err := c.connect(); err != nil {
			return "", err
		}
	}

	pkt := server.NewPacket(server.PacketPing, nil)
	if _, err := c.conn.Write(pkt.Encode()); err != nil {
		c.conn = nil
		return "", fmt.Errorf("发送心跳失败: %w", err)
	}

	respPkt, err := server.DecodePacket(c.conn)
	if err != nil {
		c.conn = nil
		return "", fmt.Errorf("读取心跳响应失败: %w", err)
	}

	var resp server.Response
	if err := json.Unmarshal(respPkt.Payload, &resp); err != nil {
		return "", fmt.Errorf("解析心跳响应失败: %w", err)
	}

	return resp.Message, nil
}

// runInteractive 运行交互式 REPL，从 reader 读取输入，输出到 writer。
func (c *cli) runInteractive(reader io.Reader, writer io.Writer) error {
	_, _ = fmt.Fprintln(writer, banner)
	_, _ = fmt.Fprintf(writer, "模式: %s | TCP: %s | HTTP: %s\n", c.mode, c.tcpAddr, c.httpAddr)
	_, _ = fmt.Fprintln(writer)

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
			if err := c.handleCommand(writer, line); err != nil {
				return err
			}
			continue
		}

		sql := c.readMultiLineSQL(scanner, writer, line)
		if sql == "" {
			continue
		}

		result, err := c.execute(sql)
		if err != nil {
			_, _ = fmt.Fprintf(writer, "错误: %v\n", err)
			continue
		}
		_, _ = fmt.Fprintln(writer, result)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取输入失败: %w", err)
	}
	return nil
}

// readMultiLineSQL 收集多行 SQL（以分号结尾），返回去除分号后的完整语句。
// 实际逻辑由 pkg/cli.ReadMultiLineSQL 提供，本方法为保持历史 API 兼容而保留。
func (c *cli) readMultiLineSQL(scanner *bufio.Scanner, writer io.Writer, firstLine string) string {
	return clishared.ReadMultiLineSQL(scanner, writer, firstLine)
}

// runInteractiveTTY 是 TTY 增强版 REPL：使用 peterh/liner 提供历史/补全，
// 使用 fatih/color 高亮错误/成功/提示符。仅在 stdin/stdout 同时是 TTY 时调用。
//
// 行为与 runInteractive 保持一致：反斜杠命令（\q/\h/\status/\use/\format）分发，
// 多行 SQL 以分号结束，执行结果通过 render.Response 格式化输出。
func (c *cli) runInteractiveTTY(writer io.Writer) error {
	clishared.EnableColorGlobally()
	defer clishared.DisableColorGlobally()

	_, _ = fmt.Fprintln(writer, banner)
	_, _ = fmt.Fprintf(writer, "模式: %s | TCP: %s | HTTP: %s\n", c.mode, c.tcpAddr, c.httpAddr)
	_, _ = fmt.Fprintln(writer)

	session, err := clishared.NewLinerSession("widb> ", clishared.DefaultHistoryFile(), 1000)
	if err != nil {
		return fmt.Errorf("初始化 REPL 失败: %w", err)
	}
	defer session.Close(clishared.DefaultHistoryFile())

	formatState := clishared.NewFormatState()
	formatState.Set(c.format)

	for {
		trimmed, err := readTTYLine(session, writer, clishared.ColorizePrompt("widb> "))
		if err != nil {
			return err
		}
		if trimmed == "" {
			continue
		}
		session.AppendHistory(trimmed)

		if strings.HasPrefix(trimmed, "\\") {
			done, shouldExit := c.handleCommandTTY(writer, trimmed, formatState)
			if done && shouldExit {
				_, _ = fmt.Fprintln(writer, clishared.ColorizeSuccess("再见!"))
				return nil
			}
			if done {
				continue
			}
		}

		sql, _ := clishared.ReadMultiLineSQLWithLiner(session, "  ...> ", trimmed)
		if sql == "" {
			continue
		}
		c.executeTTY(writer, sql)
	}
}

// readTTYLine 从 liner 读取一行并 TrimSpace。EOF 转换为 io.EOF 返回。
//
// prompt 允许包含 ANSI 颜色转义码，调用 PromptWithWriter 把它写到 writer
// 后再用空 prompt 调 liner.Prompt——这是 peterh/liner 在 prompt 含
// 控制字符时返回 "invalid prompt" 的标准规避方式。
func readTTYLine(session *clishared.LinerSession, writer io.Writer, prompt string) (string, error) {
	line, err := session.PromptWithWriter(writer, prompt)
	if err != nil {
		if err == io.EOF {
			return "", io.EOF
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// executeTTY 在 TTY 模式下执行 SQL 并打印结果，错误信息用红色高亮。
func (c *cli) executeTTY(writer io.Writer, sql string) {
	result, err := c.execute(sql)
	if err != nil {
		_, _ = fmt.Fprintln(writer, clishared.ColorizeError("错误: "+err.Error()))
		return
	}
	_, _ = fmt.Fprintln(writer, result)
}

// handleCommandTTY 在 TTY 模式下处理反斜杠命令。
// 返回 (handled, shouldExit)。当 handled=false 时表示该行不是命令，由 SQL 路径处理。
func (c *cli) handleCommandTTY(writer io.Writer, cmd string, formatState *clishared.FormatState) (bool, bool) {
	switch clishared.ParseMetaCmd(cmd) {
	case clishared.MetaCmdFormat:
		// \format 委托给 FormatState
		before := formatState.Current()
		formatState.HandleCommand(writer, cmd)
		if formatState.Current() != before {
			c.format = formatState.Current()
		}
		return true, false
	case clishared.MetaCmdQuit:
		return true, true
	case clishared.MetaCmdHelp:
		_, _ = fmt.Fprintln(writer, helpText)
		return true, false
	case clishared.MetaCmdStatus:
		result, err := c.pingTCP()
		if err != nil {
			_, _ = fmt.Fprintf(writer, "%s: 不可达 (%v)\n", clishared.ColorizeError("服务器状态"), err)
		} else {
			_, _ = fmt.Fprintf(writer, "服务器状态: 正常 (%s)\n", result)
		}
		return true, false
	case clishared.MetaCmdUseTCP:
		c.mode = modeTCP
		c.conn = nil
		_, _ = fmt.Fprintln(writer, clishared.ColorizeSuccess("已切换到 TCP 模式"))
		return true, false
	case clishared.MetaCmdUseHTTP:
		c.mode = modeHTTP
		c.conn = nil
		_, _ = fmt.Fprintln(writer, clishared.ColorizeSuccess("已切换到 HTTP 模式"))
		return true, false
	default:
		// MetaCmdNotCommand: 调用方已过滤。
		// MetaCmdUnknown / MetaCmdAddrs: 独立 CLI 不支持 \addrs。
		_, _ = fmt.Fprintf(writer, "%s: %s，输入 \\h 查看帮助\n", clishared.ColorizeError("未知命令"), cmd)
		return true, false
	}
}

// handleCommand 处理反斜杠命令。
func (c *cli) handleCommand(writer io.Writer, cmd string) error {
	switch clishared.ParseMetaCmd(cmd) {
	case clishared.MetaCmdQuit:
		_, _ = fmt.Fprintln(writer, "再见!")
		return io.EOF
	case clishared.MetaCmdHelp:
		_, _ = fmt.Fprintln(writer, helpText)
	case clishared.MetaCmdStatus:
		result, err := c.pingTCP()
		if err != nil {
			_, _ = fmt.Fprintf(writer, "服务器状态: 不可达 (%v)\n", err)
		} else {
			_, _ = fmt.Fprintf(writer, "服务器状态: 正常 (%s)\n", result)
		}
	case clishared.MetaCmdUseTCP:
		c.mode = modeTCP
		c.conn = nil
		_, _ = fmt.Fprintln(writer, "已切换到 TCP 模式")
	case clishared.MetaCmdUseHTTP:
		c.mode = modeHTTP
		_, _ = fmt.Fprintln(writer, "已切换到 HTTP 模式")
	case clishared.MetaCmdFormat:
		return c.handleFormatCommand(writer, cmd)
	default:
		// MetaCmdNotCommand: 调用方已过滤。
		// MetaCmdUnknown / MetaCmdAddrs: 独立 CLI 不支持 \addrs。
		_, _ = fmt.Fprintf(writer, "未知命令: %s，输入 \\h 查看帮助\n", cmd)
	}
	return nil
}

// handleFormatCommand 处理 \format 命令：无参数显示当前格式，有参数切换格式。
// 实际逻辑由 pkg/cli.FormatState 提供，本方法为保持历史 API 兼容而保留。
func (c *cli) handleFormatCommand(writer io.Writer, cmd string) error {
	state := &clishared.FormatState{}
	state.Set(c.format)
	before := state.Current()
	state.HandleCommand(writer, cmd)
	if state.Current() != before {
		c.format = state.Current()
	}
	return nil
}

// runCLI 是主逻辑，提取出来便于测试。
func runCLI(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("widb-cli", flag.ContinueOnError)
	tcpAddr := fs.String("tcp", "127.0.0.1:9000", "服务器 TCP 地址")
	httpAddr := fs.String("http", "127.0.0.1:8080", "服务器 HTTP 地址")
	mode := fs.String("mode", "tcp", "连接模式: tcp 或 http")
	execute := fs.String("e", "", "执行单条 SQL 语句后退出")
	format := fs.String("format", render.FormatPretty, "输出格式: pretty/vertical/json/csv")

	if err := fs.Parse(args); err != nil {
		_, _ = fmt.Fprintf(stderr, "参数解析错误: %v\n", err)
		return 1
	}

	c := newCLI(*tcpAddr, *httpAddr, strings.ToLower(*mode))
	defer c.close()
	if !render.IsValidFormat(*format) {
		_, _ = fmt.Fprintf(stderr, "未知输出格式: %s（支持: %s）\n", *format, strings.Join(render.SupportedFormats, ", "))
		return 1
	}
	c.format = *format

	if *execute != "" {
		result, err := c.execute(*execute)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "错误: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, result)
		return 0
	}

	if clishared.IsTerminalReader(stdin) && clishared.IsTerminalWriter(stdout) {
		if err := c.runInteractiveTTY(stdout); err != nil && err != io.EOF {
			_, _ = fmt.Fprintf(stderr, "错误: %v\n", err)
			return 1
		}
		return 0
	}
	if err := c.runInteractive(stdin, stdout); err != nil && err != io.EOF {
		_, _ = fmt.Fprintf(stderr, "错误: %v\n", err)
		return 1
	}
	return 0
}

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
