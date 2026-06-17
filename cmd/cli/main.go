// Package main 是 widb-cli 命令行客户端的入口点。
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
		format:   formatPretty,
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

	return renderResponse(&resp, c.format), nil
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

	return renderResponse(&resp, c.format), nil
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
func (c *cli) readMultiLineSQL(scanner *bufio.Scanner, writer io.Writer, firstLine string) string {
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

// handleCommand 处理反斜杠命令。
func (c *cli) handleCommand(writer io.Writer, cmd string) error {
	if strings.HasPrefix(cmd, "\\format") {
		return c.handleFormatCommand(writer, cmd)
	}
	switch cmd {
	case "\\q", "\\quit":
		_, _ = fmt.Fprintln(writer, "再见!")
		return io.EOF
	case "\\h", "\\help":
		_, _ = fmt.Fprintln(writer, helpText)
	case "\\status":
		result, err := c.pingTCP()
		if err != nil {
			_, _ = fmt.Fprintf(writer, "服务器状态: 不可达 (%v)\n", err)
		} else {
			_, _ = fmt.Fprintf(writer, "服务器状态: 正常 (%s)\n", result)
		}
	case "\\use TCP":
		c.mode = modeTCP
		c.conn = nil
		_, _ = fmt.Fprintln(writer, "已切换到 TCP 模式")
	case "\\use HTTP":
		c.mode = modeHTTP
		_, _ = fmt.Fprintln(writer, "已切换到 HTTP 模式")
	default:
		_, _ = fmt.Fprintf(writer, "未知命令: %s，输入 \\h 查看帮助\n", cmd)
	}
	return nil
}

// handleFormatCommand 处理 \format 命令：无参数显示当前格式，有参数切换格式。
func (c *cli) handleFormatCommand(writer io.Writer, cmd string) error {
	arg := strings.TrimSpace(strings.TrimPrefix(cmd, "\\format"))
	if arg == "" {
		_, _ = fmt.Fprintf(writer, "当前格式: %s（支持: %s）\n", c.format, strings.Join(supportedFormats, ", "))
		return nil
	}
	if !isValidFormat(arg) {
		_, _ = fmt.Fprintf(writer, "未知格式: %s，支持: %s\n", arg, strings.Join(supportedFormats, ", "))
		return nil
	}
	c.format = arg
	_, _ = fmt.Fprintf(writer, "已切换到 %s 格式\n", arg)
	return nil
}

// runCLI 是主逻辑，提取出来便于测试。
func runCLI(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("widb-cli", flag.ContinueOnError)
	tcpAddr := fs.String("tcp", "127.0.0.1:9000", "服务器 TCP 地址")
	httpAddr := fs.String("http", "127.0.0.1:8080", "服务器 HTTP 地址")
	mode := fs.String("mode", "tcp", "连接模式: tcp 或 http")
	execute := fs.String("e", "", "执行单条 SQL 语句后退出")
	format := fs.String("format", formatPretty, "输出格式: pretty/vertical/json/csv")

	if err := fs.Parse(args); err != nil {
		_, _ = fmt.Fprintf(stderr, "参数解析错误: %v\n", err)
		return 1
	}

	c := newCLI(*tcpAddr, *httpAddr, strings.ToLower(*mode))
	defer c.close()
	if !isValidFormat(*format) {
		_, _ = fmt.Fprintf(stderr, "未知输出格式: %s（支持: %s）\n", *format, strings.Join(supportedFormats, ", "))
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

	if err := c.runInteractive(stdin, stdout); err != nil && err != io.EOF {
		_, _ = fmt.Fprintf(stderr, "错误: %v\n", err)
		return 1
	}
	return 0
}

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
