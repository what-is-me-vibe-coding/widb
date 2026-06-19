// Package cli 的单元测试。
package cli

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fatih/color"

	"github.com/what-is-me-vibe-coding/test-db/pkg/render"
)

// TestReadMultiLineSQL_SingleLineWithSemicolon 验证单行带分号直接返回。
func TestReadMultiLineSQL_SingleLineWithSemicolon(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader(""))
	var out bytes.Buffer
	got := ReadMultiLineSQL(scanner, &out, "SELECT 1;")
	if got != "SELECT 1" {
		t.Errorf("结果 = %q, want %q", got, "SELECT 1")
	}
	if out.Len() != 0 {
		t.Errorf("单行 SQL 不应输出续行提示: %q", out.String())
	}
}

// TestReadMultiLineSQL_MultiLine 验证多行 SQL 被空格拼接。
func TestReadMultiLineSQL_MultiLine(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("FROM users\nWHERE id = 1;\n"))
	var out bytes.Buffer
	got := ReadMultiLineSQL(scanner, &out, "SELECT *")
	want := "SELECT * FROM users WHERE id = 1"
	if got != want {
		t.Errorf("结果 = %q, want %q", got, want)
	}
	if !strings.Contains(out.String(), continuationPrompt) {
		t.Errorf("多行输入应输出续行提示: %q", out.String())
	}
}

// TestReadMultiLineSQL_NoSemicolonEOF 验证无分号遇 EOF 立即结束。
func TestReadMultiLineSQL_NoSemicolonEOF(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("FROM users\n"))
	var out bytes.Buffer
	got := ReadMultiLineSQL(scanner, &out, "SELECT *")
	want := "SELECT * FROM users"
	if got != want {
		t.Errorf("结果 = %q, want %q", got, want)
	}
}

// TestReadMultiLineSQL_EmptySemicolon 验证只有分号的输入返回空字符串。
func TestReadMultiLineSQL_EmptySemicolon(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader(""))
	var out bytes.Buffer
	got := ReadMultiLineSQL(scanner, &out, ";")
	if got != "" {
		t.Errorf("结果 = %q, want 空字符串", got)
	}
}

// TestReadMultiLineSQL_TrimsWhitespace 验证首尾空白被去除。
func TestReadMultiLineSQL_TrimsWhitespace(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader(""))
	var out bytes.Buffer
	got := ReadMultiLineSQL(scanner, &out, "  SELECT 1;  ")
	if got != "SELECT 1" {
		t.Errorf("结果 = %q, want %q", got, "SELECT 1")
	}
}

// TestFormatState_InitialPretty 验证 NewFormatState 初始为 pretty。
func TestFormatState_InitialPretty(t *testing.T) {
	s := NewFormatState()
	if s.Current() != render.FormatPretty {
		t.Errorf("初始格式 = %q, want %q", s.Current(), render.FormatPretty)
	}
}

// TestFormatState_HandleCommand_Show 验证无参 \format 显示当前格式。
func TestFormatState_HandleCommand_Show(t *testing.T) {
	s := NewFormatState()
	var out bytes.Buffer
	s.HandleCommand(&out, "\\format")
	if !strings.Contains(out.String(), "当前格式") {
		t.Errorf("应显示当前格式: %q", out.String())
	}
	if !strings.Contains(out.String(), render.FormatPretty) {
		t.Errorf("应包含 pretty: %q", out.String())
	}
}

// TestFormatState_HandleCommand_Switch 验证合法参数切换格式。
func TestFormatState_HandleCommand_Switch(t *testing.T) {
	s := NewFormatState()
	var out bytes.Buffer
	s.HandleCommand(&out, "\\format csv")
	if s.Current() != render.FormatCSV {
		t.Errorf("切换后 = %q, want %q", s.Current(), render.FormatCSV)
	}
	if !strings.Contains(out.String(), "已切换到 csv 格式") {
		t.Errorf("应显示切换成功: %q", out.String())
	}
}

// TestFormatState_HandleCommand_Invalid 验证非法参数给出错误提示且保持原格式。
func TestFormatState_HandleCommand_Invalid(t *testing.T) {
	s := NewFormatState()
	prev := s.Current()
	var out bytes.Buffer
	s.HandleCommand(&out, "\\format xml")
	if s.Current() != prev {
		t.Errorf("非法参数不应修改格式: %q", s.Current())
	}
	if !strings.Contains(out.String(), "未知格式") {
		t.Errorf("应显示未知格式提示: %q", out.String())
	}
}

// TestFormatState_Set 验证 Set 直接更新格式。
func TestFormatState_Set(t *testing.T) {
	s := NewFormatState()
	s.Set(render.FormatJSON)
	if s.Current() != render.FormatJSON {
		t.Errorf("Set 后 = %q, want %q", s.Current(), render.FormatJSON)
	}
}

// --- 颜色辅助函数测试 ---

// TestColorizeError_NoColor 验证禁用颜色时返回原样。
func TestColorizeError_NoColor(t *testing.T) {
	prev := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = prev })

	if got := ColorizeError("boom"); got != "boom" {
		t.Errorf("禁用颜色时应原样返回: %q", got)
	}
}

// TestColorizeSuccess_NoColor 验证禁用颜色时绿色辅助返回原样。
func TestColorizeSuccess_NoColor(t *testing.T) {
	prev := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = prev })

	if got := ColorizeSuccess("OK"); got != "OK" {
		t.Errorf("禁用颜色时应原样返回: %q", got)
	}
}

// TestColorizeNull_NoColor 验证禁用颜色时黄色辅助返回原样。
func TestColorizeNull_NoColor(t *testing.T) {
	prev := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = prev })

	if got := ColorizeNull("NULL"); got != "NULL" {
		t.Errorf("禁用颜色时应原样返回: %q", got)
	}
}

// TestColorizePrompt_NoColor 验证禁用颜色时青色辅助返回原样。
func TestColorizePrompt_NoColor(t *testing.T) {
	prev := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = prev })

	if got := ColorizePrompt("widb> "); got != "widb> " {
		t.Errorf("禁用颜色时应原样返回: %q", got)
	}
}

// TestColorizeError_WithColor 验证启用颜色时输出包含 ANSI 转义码。
func TestColorizeError_WithColor(t *testing.T) {
	prev := color.NoColor
	color.NoColor = false
	t.Cleanup(func() { color.NoColor = prev })

	got := ColorizeError("boom")
	if got == "boom" {
		t.Errorf("启用颜色时输出应包含转义码: %q", got)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("启用颜色时输出应包含 ANSI 转义: %q", got)
	}
}

// TestDisableColorGlobally 验证全局关闭/开启颜色函数正确切换 color.NoColor。
func TestDisableColorGlobally(t *testing.T) {
	prev := color.NoColor
	t.Cleanup(func() { color.NoColor = prev })

	DisableColorGlobally()
	if !color.NoColor {
		t.Error("DisableColorGlobally 后 color.NoColor 应为 true")
	}
	EnableColorGlobally()
	if color.NoColor {
		t.Error("EnableColorGlobally 后 color.NoColor 应为 false")
	}
}

// TestIsTerminalReader_NonFile 验证非 os.File 类型 reader 返回 false。
func TestIsTerminalReader_NonFile(t *testing.T) {
	if IsTerminalReader(strings.NewReader("hello")) {
		t.Error("strings.Reader 不应被识别为 TTY")
	}
	if IsTerminalReader(&bytes.Buffer{}) {
		t.Error("bytes.Buffer 不应被识别为 TTY")
	}
}

// TestIsTerminalWriter_NonFile 验证非 os.File 类型 writer 返回 false。
func TestIsTerminalWriter_NonFile(t *testing.T) {
	if IsTerminalWriter(&bytes.Buffer{}) {
		t.Error("bytes.Buffer 不应被识别为 TTY")
	}
}

// TestDefaultHistoryFile 验证默认历史文件路径位于 HOME 目录下且文件名正确。
func TestDefaultHistoryFile(t *testing.T) {
	path := DefaultHistoryFile()
	if path == "" {
		// HOME 不可用时返回空（CI 容器可能），跳过断言
		t.Skip("HOME 不可用，跳过路径检查")
	}
	if filepath.Base(path) != historyFileName {
		t.Errorf("文件名 = %q, want %q", filepath.Base(path), historyFileName)
	}
}

// TestColorEnabled_DefaultWhenNoEnv 验证默认配置 + 无 NO_COLOR 时启用颜色。
func TestColorEnabled_DefaultWhenNoEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	cfg := REPLConfig{}
	if !colorEnabled(cfg) {
		t.Error("默认应启用颜色")
	}
}

// TestColorEnabled_RespectsNoColorEnv 验证 NO_COLOR 环境变量禁用颜色。
func TestColorEnabled_RespectsNoColorEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if colorEnabled(REPLConfig{}) {
		t.Error("NO_COLOR=1 应禁用颜色")
	}
}

// TestColorEnabled_RespectsDisableColor 验证 REPLConfig.DisableColor 优先级最高。
func TestColorEnabled_RespectsDisableColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	cfg := REPLConfig{DisableColor: true}
	if colorEnabled(cfg) {
		t.Error("DisableColor=true 应禁用颜色")
	}
}

// --- LinerSession 测试 ---

// TestLinerSession_AppendHistory 验证去重逻辑与容量上限。
func TestLinerSession_AppendHistory(t *testing.T) {
	dir := t.TempDir()
	session, err := NewLinerSession("widb> ", filepath.Join(dir, historyFileName), 3)
	if err != nil {
		t.Fatalf("NewLinerSession 失败: %v", err)
	}
	t.Cleanup(func() { session.Close("") })

	// 重复行不去重以外的应保留
	session.AppendHistory("SELECT 1;")
	session.AppendHistory("SELECT 1;") // 与上一行相同，应不追加
	session.AppendHistory("SELECT 2;")
	session.AppendHistory("SELECT 3;")
	session.AppendHistory("SELECT 4;") // 触发容量截断
	if got := len(session.history); got != 3 {
		t.Errorf("历史长度 = %d, want 3", got)
	}
	want := []string{"SELECT 2;", "SELECT 3;", "SELECT 4;"}
	for i, w := range want {
		if session.history[i] != w {
			t.Errorf("history[%d] = %q, want %q", i, session.history[i], w)
		}
	}
}

// TestLinerSession_AppendHistory_SkipEmpty 验证空行被忽略。
func TestLinerSession_AppendHistory_SkipEmpty(t *testing.T) {
	dir := t.TempDir()
	session, err := NewLinerSession("widb> ", filepath.Join(dir, historyFileName), 10)
	if err != nil {
		t.Fatalf("NewLinerSession 失败: %v", err)
	}
	t.Cleanup(func() { session.Close("") })

	session.AppendHistory("")
	session.AppendHistory("   ")
	if got := len(session.history); got != 0 {
		t.Errorf("空行应被忽略，history = %v", session.history)
	}
}

// TestLinerSession_Persistence 验证历史能够跨 session 持久化到文件。
func TestLinerSession_Persistence(t *testing.T) {
	dir := t.TempDir()
	histFile := filepath.Join(dir, historyFileName)

	// 第一次 session 写入历史
	s1, err := NewLinerSession("widb> ", histFile, 10)
	if err != nil {
		t.Fatalf("NewLinerSession 失败: %v", err)
	}
	s1.AppendHistory("SELECT 1;")
	s1.AppendHistory("SELECT 2;")
	s1.Close(histFile)

	// 验证文件存在且非空
	info, err := os.Stat(histFile)
	if err != nil {
		t.Fatalf("历史文件未生成: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("历史文件为空")
	}

	// 第二次 session 读取历史
	s2, err := NewLinerSession("widb> ", histFile, 10)
	if err != nil {
		t.Fatalf("第二次 NewLinerSession 失败: %v", err)
	}
	t.Cleanup(func() { s2.Close("") })

	if got := len(s2.history); got != 2 {
		t.Errorf("加载后历史长度 = %d, want 2", got)
	}
}

// TestLinerSession_Close_NoHistory 验证 historyFile 为空时 Close 不写文件。
func TestLinerSession_Close_NoHistory(t *testing.T) {
	session, err := NewLinerSession("widb> ", "", 10)
	if err != nil {
		t.Fatalf("NewLinerSession 失败: %v", err)
	}
	session.AppendHistory("SELECT 1;")
	// 不传 historyFile 时应不报错
	session.Close("")
}

// TestLinerSession_SetCompleter_Nil 验证 nil completer 不 panic。
func TestLinerSession_SetCompleter_Nil(t *testing.T) {
	dir := t.TempDir()
	session, err := NewLinerSession("widb> ", filepath.Join(dir, historyFileName), 10)
	if err != nil {
		t.Fatalf("NewLinerSession 失败: %v", err)
	}
	t.Cleanup(func() { session.Close("") })

	session.SetCompleter(nil)
	// 设置 nil 后不 panic 即可
}
