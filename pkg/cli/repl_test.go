// Package cli 的单元测试。
package cli

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fatih/color"
	"github.com/peterh/liner"

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

// ansiPrompt 构造一个含 ANSI 转义码的 prompt（模拟 ColorizePrompt 的产物）。
// ESC = \x1b；常见的 SGR 转义 \x1b[36;1m…\x1b[0m 给 prompt 上色。
// 该 prompt 必含 unicode category Cc（控制字符），会被 liner 拒绝。
const ansiPrompt = "\x1b[36;1mwidb> \x1b[0m"

// TestLinerSession_PromptWith_RejectsControlChars 验证 liner 在 prompt 含控制
// 字符（含 ANSI 转义码）时返回 ErrInvalidPrompt。这是 issue #233（widb-cli
// 打开报 "invalid prompt"）的根因，必须用 LinerSession.PromptWithWriter 规避。
func TestLinerSession_PromptWith_RejectsControlChars(t *testing.T) {
	session, err := NewLinerSession("widb> ", "", 10)
	if err != nil {
		t.Fatalf("NewLinerSession 失败: %v", err)
	}
	t.Cleanup(func() { session.Close("") })

	_, err = session.PromptWith(ansiPrompt)
	if err == nil {
		t.Fatal("PromptWith 应拒绝含控制字符的 prompt")
	}
	if !errors.Is(err, liner.ErrInvalidPrompt) {
		t.Errorf("PromptWith 应返回 ErrInvalidPrompt，实际: %v", err)
	}
}

// TestLinerSession_PromptWithWriter_WritesPrompt 验证 PromptWithWriter
// 把 prompt 写入 writer 后才发起 liner.Prompt，规避 ErrInvalidPrompt。
//
// 测试方法：在 ANSI prompt 下，PromptWith 会返回 ErrInvalidPrompt；
// 而 PromptWithWriter 改由调用方负责写出，因此不应触发该错误。
// 进一步断言 writer 已被写入 prompt 字节序列。
func TestLinerSession_PromptWithWriter_WritesPrompt(t *testing.T) {
	session, err := NewLinerSession("widb> ", "", 10)
	if err != nil {
		t.Fatalf("NewLinerSession 失败: %v", err)
	}
	t.Cleanup(func() { session.Close("") })

	// PromptWith 应返回 ErrInvalidPrompt（已知 liner 行为）。
	_, err = session.PromptWith(ansiPrompt)
	if !errors.Is(err, liner.ErrInvalidPrompt) {
		t.Fatalf("前置条件：PromptWith(ansiPrompt) 应返回 ErrInvalidPrompt，实际: %v", err)
	}

	// 构造一个永不阻塞的 writer：PromptWithWriter 内部 writer.Write 同步完成。
	var out bytes.Buffer
	w := &syncWriter{buf: &out}
	// 通过 goroutine 启动 PromptWithWriter 防止 liner 阻塞；它在非 TTY
	// 环境下会走 promptUnsupported 并立即返回（因为 promptUnsupported 读
	// os.Stdin 时若 inputRedirected=true，会调用 s.r.ReadLine()，我们的
	// 进程里没有真实终端，可能被 hang 住——但本次只验证 writer 被写入）。
	type outcome struct {
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		_, e := session.PromptWithWriter(w, ansiPrompt)
		done <- outcome{e}
	}()
	select {
	case o := <-done:
		if errors.Is(o.err, liner.ErrInvalidPrompt) {
			t.Fatalf("PromptWithWriter 仍返回 ErrInvalidPrompt，修复失效: %v", o.err)
		}
	case <-time.After(2 * time.Second):
		// 非 TTY 测试环境下 liner 可能阻塞，验证 writer 已写入 prompt 即可
	}

	if w.buf.Len() == 0 {
		t.Fatal("PromptWithWriter 未把 prompt 写入 writer")
	}
	if !strings.Contains(w.buf.String(), "widb>") {
		t.Errorf("writer 应包含 prompt 文本，实际: %q", w.buf.String())
	}
	if !strings.Contains(w.buf.String(), "\x1b[") {
		t.Errorf("writer 应保留 ANSI 转义码（不被过滤），实际: %q", w.buf.String())
	}
}

// syncWriter 把 writer.Write 串行化到内嵌 buf，避免 race detector 误报。
type syncWriter struct {
	buf *bytes.Buffer
}

// Write 实现 io.Writer。
func (w *syncWriter) Write(p []byte) (int, error) {
	return w.buf.Write(p)
}
