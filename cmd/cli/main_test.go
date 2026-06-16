package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

const (
	testModeTCP  = "tcp"
	testModeHTTP = "http"
	testPong     = "pong"
	testFlagHTTP = "-http"
	testFlagTCP  = "-tcp"
	testFlagMode = "-mode"
	testSQL      = "SELECT * FROM users"
)

func startServer(t *testing.T) (string, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "testdb-cli-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	cfg := server.Config{TCPAddr: "127.0.0.1:0", HTTPAddr: "127.0.0.1:0", DataDir: dir}
	srv, err := server.NewServer(cfg, server.WithMetricsRegistry(prometheus.NewRegistry()))
	if err != nil {
		t.Fatalf("NewServer 失败: %v", err)
	}
	_ = srv.Catalog().CreateTable("users", []catalog.ColumnDef{
		{Name: "id", Type: common.TypeInt64, Nullable: false},
		{Name: "name", Type: common.TypeString, Nullable: true},
	}, []string{"id"}, catalog.TableOptions{})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	time.Sleep(50 * time.Millisecond)
	return srv.TCPAddr(), srv.HTTPAddr()
}

// --- TCP/HTTP 查询测试 ---

func TestCLIExecuteTCPQuery(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	c := newCLI(tcpAddr, httpAddr, testModeTCP)
	defer c.close()
	result, err := c.execute("SELECT * FROM users")
	if err != nil {
		t.Fatalf("execute TCP 失败: %v", err)
	}
	if result == "" {
		t.Error("查询结果为空")
	}
}

func TestCLIExecuteHTTPQuery(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	c := newCLI(tcpAddr, httpAddr, testModeHTTP)
	defer c.close()
	result, err := c.execute("SELECT * FROM users")
	if err != nil {
		t.Fatalf("execute HTTP 失败: %v", err)
	}
	if result == "" {
		t.Error("查询结果为空")
	}
}

func TestCLIPingTCP(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	c := newCLI(tcpAddr, httpAddr, testModeTCP)
	defer c.close()
	if err := c.connect(); err != nil {
		t.Fatalf("connect 失败: %v", err)
	}
	result, err := c.pingTCP()
	if err != nil {
		t.Fatalf("pingTCP 失败: %v", err)
	}
	if result != testPong {
		t.Errorf("ping 结果 = %q, 期望 pong", result)
	}
}

func TestCLIExecuteTCPWithExistingConn(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	c := newCLI(tcpAddr, httpAddr, testModeTCP)
	defer c.close()
	_, _ = c.pingTCP() // 建立连接
	result, err := c.execute("SELECT * FROM users")
	if err != nil {
		t.Fatalf("execute TCP 失败: %v", err)
	}
	if result == "" {
		t.Error("查询结果为空")
	}
}

func TestCLIExecuteTCPWriteError(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	c := newCLI(tcpAddr, httpAddr, testModeTCP)
	defer c.close()
	if err := c.connect(); err != nil {
		t.Fatalf("connect 失败: %v", err)
	}
	_ = c.conn.Close()
	c.conn = nil // 让它重连
	result, err := c.execute("SELECT * FROM users")
	if err != nil {
		t.Fatalf("重连后应成功: %v", err)
	}
	if result == "" {
		t.Error("查询结果为空")
	}
}

func TestCLIPingTCPWriteError(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	c := newCLI(tcpAddr, httpAddr, testModeTCP)
	defer c.close()
	if err := c.connect(); err != nil {
		t.Fatalf("connect 失败: %v", err)
	}
	_ = c.conn.Close()
	_, err := c.pingTCP()
	if err == nil {
		t.Error("期望 ping 失败")
	}
}

// --- 连接与模式测试 ---

func TestCLICloseWithConnection(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	c := newCLI(tcpAddr, httpAddr, testModeTCP)
	if err := c.connect(); err != nil {
		t.Fatalf("connect 失败: %v", err)
	}
	c.close()
}

func TestCLICloseWithoutConnection(_ *testing.T) {
	c := newCLI("127.0.0.1:1", "127.0.0.1:1", testModeTCP)
	c.close()
}

func TestCLIConnectFailure(t *testing.T) {
	c := newCLI("127.0.0.1:1", "127.0.0.1:1", testModeTCP)
	defer c.close()
	_, err := c.execute("SELECT 1")
	if err == nil {
		t.Error("期望连接失败错误")
	}
}

func TestCLIExecuteHTTPFailure(t *testing.T) {
	c := newCLI("127.0.0.1:1", "127.0.0.1:1", testModeHTTP)
	defer c.close()
	_, err := c.execute("SELECT 1")
	if err == nil {
		t.Error("期望 HTTP 请求失败错误")
	}
}

func TestCLIExecuteUnknownMode(t *testing.T) {
	c := newCLI("127.0.0.1:1", "127.0.0.1:1", "grpc")
	defer c.close()
	_, err := c.execute("SELECT 1")
	if err == nil {
		t.Error("期望未知模式错误")
	}
}

// --- runInteractive 测试 ---

func runInt(c *cli, input string) (string, error) {
	var buf bytes.Buffer
	err := c.runInteractive(strings.NewReader(input), &buf)
	return buf.String(), err
}

func TestCLIRunInteractiveQuit(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	c := newCLI(tcpAddr, httpAddr, testModeTCP)
	defer c.close()
	out, err := runInt(c, "\\q\n")
	if err != nil && err.Error() != "EOF" {
		t.Errorf("不应返回非 EOF 错误: %v", err)
	}
	if !strings.Contains(out, "再见!") {
		t.Errorf("输出应包含 '再见!': %q", out)
	}
}

func TestCLIRunInteractiveHelp(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	c := newCLI(tcpAddr, httpAddr, testModeTCP)
	defer c.close()
	out, _ := runInt(c, "\\h\n\\q\n")
	if !strings.Contains(out, "可用命令") {
		t.Errorf("输出应包含帮助文本: %q", out)
	}
}

func TestCLIRunInteractiveSQL(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	c := newCLI(tcpAddr, httpAddr, testModeTCP)
	defer c.close()
	out, _ := runInt(c, "SELECT * FROM users;\n\\q\n")
	if !strings.Contains(out, "widb-cli") {
		t.Errorf("输出应包含 banner: %q", out)
	}
}

func TestCLIRunInteractiveEmptyLine(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	c := newCLI(tcpAddr, httpAddr, testModeTCP)
	defer c.close()
	_, _ = runInt(c, "\n\n\\q\n")
}

func TestCLIRunInteractiveSwitchMode(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	c := newCLI(tcpAddr, httpAddr, testModeTCP)
	defer c.close()
	out, _ := runInt(c, "\\use HTTP\n\\q\n")
	if !strings.Contains(out, "已切换到 HTTP 模式") {
		t.Errorf("输出应包含模式切换信息: %q", out)
	}
}

func TestCLIRunInteractiveUnknownCommand(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	c := newCLI(tcpAddr, httpAddr, testModeTCP)
	defer c.close()
	out, _ := runInt(c, "\\foo\n\\q\n")
	if !strings.Contains(out, "未知命令") {
		t.Errorf("输出应包含未知命令提示: %q", out)
	}
}

func TestCLIRunInteractiveStatus(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	c := newCLI(tcpAddr, httpAddr, testModeTCP)
	defer c.close()
	out, _ := runInt(c, "\\status\n\\q\n")
	if !strings.Contains(out, "正常") {
		t.Errorf("输出应包含服务器状态: %q", out)
	}
}

func TestCLIRunInteractiveStatusUnreachable(t *testing.T) {
	c := newCLI("127.0.0.1:1", "127.0.0.1:1", testModeTCP)
	defer c.close()
	out, _ := runInt(c, "\\status\n\\q\n")
	if !strings.Contains(out, "不可达") {
		t.Errorf("输出应包含不可达信息: %q", out)
	}
}

func TestCLIRunInteractiveSQLError(t *testing.T) {
	c := newCLI("127.0.0.1:1", "127.0.0.1:1", testModeTCP)
	defer c.close()
	out, _ := runInt(c, "SELECT 1;\n\\q\n")
	if !strings.Contains(out, "错误") {
		t.Errorf("输出应包含错误信息: %q", out)
	}
}

func TestCLIRunInteractiveMultilineSQL(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	c := newCLI(tcpAddr, httpAddr, testModeTCP)
	defer c.close()
	_, _ = runInt(c, "SELECT *\nFROM users;\n\\q\n")
}

func TestCLIRunInteractiveHTTPMode(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	c := newCLI(tcpAddr, httpAddr, testModeHTTP)
	defer c.close()
	out, _ := runInt(c, "SELECT * FROM users;\n\\q\n")
	if !strings.Contains(out, "widb-cli") {
		t.Errorf("输出应包含 banner: %q", out)
	}
}

func TestCLIRunInteractiveEOF(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	c := newCLI(tcpAddr, httpAddr, testModeTCP)
	defer c.close()
	_, err := runInt(c, "")
	if err != nil {
		t.Errorf("不应返回错误: %v", err)
	}
}

// --- FormatResponse 测试 ---

func TestFormatResponse(t *testing.T) {
	tests := []struct {
		name string
		resp *server.Response
		want string
	}{
		{"错误", &server.Response{Code: -1, Message: "表不存在"}, "错误: 表不存在"},
		{"行数", &server.Response{Code: 0, Rows: 5}, "成功，影响 5 行"},
		{"消息", &server.Response{Code: 0, Message: "pong"}, "pong"},
		{"空", &server.Response{Code: 0}, "成功"},
		{"nil数据", &server.Response{Code: 0, Data: nil, Rows: 0}, "成功"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := server.FormatResponse(tt.resp); got != tt.want {
				t.Errorf("FormatResponse() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatResponseWithDataRows(t *testing.T) {
	resp := &server.Response{Code: 0, Data: []interface{}{map[string]interface{}{"col_0": int64(1)}}, Rows: 1}
	if got := server.FormatResponse(resp); !strings.Contains(got, "1 行") {
		t.Errorf("应包含行数: %q", got)
	}
}

func TestFormatResponseMapData(t *testing.T) {
	resp := &server.Response{Code: 0, Data: map[string]interface{}{"key": "value"}, Rows: 0}
	if got := server.FormatResponse(resp); !strings.Contains(got, "key") {
		t.Errorf("应包含 key: %q", got)
	}
}

func TestFormatResponseJSONRoundTrip(t *testing.T) {
	orig := &server.Response{Code: 0, Data: map[string]interface{}{"key": "value"}, Rows: 1}
	b, _ := json.Marshal(orig)
	var decoded server.Response
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if got := server.FormatResponse(&decoded); len(got) == 0 {
		t.Error("返回空字符串")
	}
}

// --- newCLI 测试 ---

func TestNewCLI(t *testing.T) {
	c := newCLI("localhost:9000", "localhost:8080", testModeTCP)
	if c.mode != testModeTCP || c.tcpAddr != "localhost:9000" || c.httpAddr != "localhost:8080" || c.httpCli == nil {
		t.Errorf("newCLI 初始化不正确: %+v", c)
	}
}

// --- runCLI 测试 ---

func TestRunCLIExecuteFlag(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	var stdout, stderr bytes.Buffer
	code := runCLI([]string{testFlagTCP, tcpAddr, testFlagHTTP, httpAddr, "-e", testSQL}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, stderr: %s", code, stderr.String())
	}
	if stdout.String() == "" {
		t.Error("stdout 不应为空")
	}
}

func TestRunCLIExecuteFlagError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCLI([]string{testFlagTCP, "127.0.0.1:1", "-e", "SELECT 1"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

func TestRunCLIInteractiveQuit(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	var stdout, stderr bytes.Buffer
	code := runCLI([]string{testFlagTCP, tcpAddr, testFlagHTTP, httpAddr}, strings.NewReader("\\q\n"), &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, stderr: %s", code, stderr.String())
	}
}

func TestRunCLIInvalidArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCLI([]string{"-unknown"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

func TestRunCLIHTTPMode(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	var stdout, stderr bytes.Buffer
	code := runCLI([]string{testFlagTCP, tcpAddr, testFlagHTTP, httpAddr, testFlagMode, testModeHTTP, "-e", testSQL}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, stderr: %s", code, stderr.String())
	}
}
