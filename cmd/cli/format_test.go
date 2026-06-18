package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/render"
)

// --- \format 命令测试 ---

func TestRunInteractiveFormatShow(t *testing.T) {
	c := newCLI("127.0.0.1:1", "127.0.0.1:1", testModeTCP)
	defer c.close()
	out, _ := runInt(c, "\\format\n\\q\n")
	if !strings.Contains(out, "当前格式") {
		t.Errorf("应显示当前格式: %q", out)
	}
}

func TestRunInteractiveFormatSwitch(t *testing.T) {
	c := newCLI("127.0.0.1:1", "127.0.0.1:1", testModeTCP)
	defer c.close()
	out, _ := runInt(c, "\\format csv\n\\q\n")
	if !strings.Contains(out, "已切换到 csv 格式") {
		t.Errorf("应显示切换成功: %q", out)
	}
	if c.format != render.FormatCSV {
		t.Errorf("c.format = %q, want csv", c.format)
	}
}

func TestRunInteractiveFormatUnknown(t *testing.T) {
	c := newCLI("127.0.0.1:1", "127.0.0.1:1", testModeTCP)
	defer c.close()
	out, _ := runInt(c, "\\format xml\n\\q\n")
	if !strings.Contains(out, "未知格式") {
		t.Errorf("应显示未知格式提示: %q", out)
	}
}

// --- -format 标志测试 ---

func TestRunCLIFormatFlag(t *testing.T) {
	tcpAddr, httpAddr := startServer(t)
	var stdout, stderr bytes.Buffer
	// -format csv 应被接受并成功执行（空结果集返回 "成功"）
	code := runCLI([]string{testFlagTCP, tcpAddr, testFlagHTTP, httpAddr, "-format", "csv", "-e", testSQL},
		strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr.String())
	}
	if stdout.String() == "" {
		t.Error("stdout 不应为空")
	}
}

func TestRunCLIFormatFlagInvalid(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCLI([]string{"-format", "xml", "-e", "SELECT 1"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "未知输出格式") {
		t.Errorf("stderr 应包含错误: %q", stderr.String())
	}
}
