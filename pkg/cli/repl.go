// Package cli 提供命令行客户端与 REPL 共享的原语，被 cmd/widb 与 cmd/cli 共用。
//
// 设计要点：
//   - ReadMultiLineSQL 收集多行 SQL（以分号结尾），与 cmd/widb、cmd/cli 历史行为一致。
//   - FormatState 封装当前输出格式与 \format 命令处理逻辑，避免在两处 REPL 中重复。
//   - RunWithLiner 提供 TTY 增强的 REPL：历史记录、Ctrl-R 搜索、Tab 补全、颜色。
//   - 本包仅依赖 pkg/render 与经过审批的第三方库（peterh/liner、fatih/color），
//     符合 AGENTS.md 的依赖约束。
package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/mattn/go-isatty"
	"github.com/peterh/liner"

	"github.com/what-is-me-vibe-coding/test-db/pkg/render"
)

// continuationPrompt 是多行 SQL 输入时展示的续行提示符。
const continuationPrompt = "  ...> "

// formatCommandPrefix 是 \format 命令的前缀，HandleFormatCommand 据此分流。
const formatCommandPrefix = "\\format"

// historyFileName 是默认历史记录文件名，位于用户 HOME 目录下。
const historyFileName = ".widb_history"

// defaultHistoryMaxEntries 限制历史记录最大条数，避免文件无限增长。
const defaultHistoryMaxEntries = 1000

// ReadMultiLineSQL 从 scanner 读取多行 SQL 直到遇到分号或 EOF。
// firstLine 是已经读到的第一行内容，函数会将其与后续行用空格连接。
// 末尾分号会被去除，结果去除首尾空白；输入为空白时返回空字符串。
//
// 行为与原 cmd/widb.readMultiLineSQL、cmd/cli.readMultiLineSQL 完全一致，
// 用于在重构中替换两份重复实现，保证现有测试不失效。
func ReadMultiLineSQL(scanner *bufio.Scanner, writer io.Writer, firstLine string) string {
	sql := firstLine
	for !strings.HasSuffix(sql, ";") {
		_, _ = fmt.Fprint(writer, continuationPrompt)
		if !scanner.Scan() {
			break
		}
		sql += " " + scanner.Text()
	}
	return strings.TrimSuffix(strings.TrimSpace(sql), ";")
}

// ReadMultiLineSQLWithLiner 与 ReadMultiLineSQL 类似，但从 liner 会话读取续行。
// 若 firstLine 已以分号结尾则直接返回；否则循环读取直到遇到分号、io.EOF 或其他错误。
// 返回最终拼接后的 SQL（去分号、TrimSpace）与读取过程中遇到的错误。
// 续行提示通过 contPrompt 参数传入，调用方负责颜色。
func ReadMultiLineSQLWithLiner(session *LinerSession, contPrompt, firstLine string) (string, error) {
	if strings.HasSuffix(firstLine, ";") {
		return strings.TrimSuffix(strings.TrimSpace(firstLine), ";"), nil
	}
	sql := firstLine
	for {
		next, err := session.PromptWith(contPrompt)
		if err != nil {
			if err == io.EOF {
				return strings.TrimSuffix(strings.TrimSpace(sql), ";"), io.EOF
			}
			return "", err
		}
		sql += " " + strings.TrimSpace(next)
		if strings.HasSuffix(sql, ";") {
			return strings.TrimSuffix(strings.TrimSpace(sql), ";"), nil
		}
	}
}

// FormatState 持有 REPL 当前输出格式，对外暴露 Current/Set 与 HandleCommand。
// 原 cmd/widb 与 cmd/cli 各自使用一个 string 与 handleFormatCommand 函数，
// 重构后将状态与命令处理统一收敛到本类型。
type FormatState struct {
	current string
}

// NewFormatState 构造 FormatState，初始格式为 render.FormatPretty。
func NewFormatState() *FormatState {
	return &FormatState{current: render.FormatPretty}
}

// Current 返回当前输出格式。
func (s *FormatState) Current() string {
	return s.current
}

// Set 直接设置当前输出格式（不进行合法性校验，调用方需自行保证）。
// 适用于初始化或测试场景；REPL 中应使用 HandleCommand 以获得友好提示。
func (s *FormatState) Set(format string) {
	s.current = format
}

// HandleCommand 处理 \format 命令的输出与格式切换。
// 参数 cmd 形如 "\format" 或 "\format csv"。
//   - 无参数：打印当前格式与支持列表。
//   - 合法参数：切换格式并打印确认信息。
//   - 非法参数：打印错误信息，格式保持不变。
//
// 返回值无意义，仅为兼容未来扩展（例如 \format <fmt> 之外的子命令）。
func (s *FormatState) HandleCommand(writer io.Writer, cmd string) {
	arg := strings.TrimSpace(strings.TrimPrefix(cmd, formatCommandPrefix))
	if arg == "" {
		_, _ = fmt.Fprintf(writer, "当前格式: %s（支持: %s）\n", s.current, strings.Join(render.SupportedFormats, ", "))
		return
	}
	if !render.IsValidFormat(arg) {
		_, _ = fmt.Fprintf(writer, "未知格式: %s，支持: %s\n", arg, strings.Join(render.SupportedFormats, ", "))
		return
	}
	s.current = arg
	_, _ = fmt.Fprintf(writer, "已切换到 %s 格式\n", arg)
}

// REPLConfig 描述 TTY 增强 REPL 的可选行为。
//
// 所有字段均为可选，零值表示「采用默认/禁用」。默认 Prompt 为 "widb> "，
// 默认 ContinuationPrompt 为 "  ...> "，默认 HistoryFile 为 "$HOME/.widb_history"。
// WordCompleter 与 OnSubmit 允许调用方注入业务定制逻辑。
type REPLConfig struct {
	// Prompt 是首行提示符；为空时使用 "widb> "。
	Prompt string
	// ContinuationPrompt 是多行 SQL 续行提示符；为空时使用 "  ...> "。
	ContinuationPrompt string
	// HistoryFile 是历史记录文件路径；为空时使用 "$HOME/.widb_history"。
	HistoryFile string
	// MaxHistoryEntries 限制历史记录最大条数；<=0 时使用 defaultHistoryMaxEntries。
	MaxHistoryEntries int
	// DisableColor 为 true 时关闭终端颜色输出（等价于设置 NO_COLOR=1）。
	DisableColor bool
	// WordCompleter 是 Tab 补全函数，输入当前行，返回候选词列表。nil 表示无补全。
	WordCompleter func(line string) []string
	// OnSubmit 接收用户输入的非空行；返回 (response, true) 时由 REPL 直接打印并 continue，
	// 返回 (response, false) 时表示该 SQL 应被上层执行。返回 ("", false) 表示忽略该行。
	// OnSubmit 为 nil 时所有行由调用方执行。
	OnSubmit func(line string) (response string, handled bool)
}

// DefaultHistoryFile 返回默认历史记录文件路径（$HOME/.widb_history）。
// 当 HOME 不可用时返回空字符串，调用方应回退到不持久化历史的模式。
func DefaultHistoryFile() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, historyFileName)
}

// colorEnabled 综合 NO_COLOR 环境变量与 REPLConfig.DisableColor 判定是否启用颜色。
// 遵循 https://no-color.org 规范：NO_COLOR 存在且非空即禁用。
func colorEnabled(cfg REPLConfig) bool {
	if cfg.DisableColor {
		return false
	}
	if v, ok := os.LookupEnv("NO_COLOR"); ok && v != "" {
		return false
	}
	return true
}

// IsTerminalReader 判断 reader 是否来自 TTY。
// 非 os.File 类型的 reader 一律返回 false（用于测试场景）。
func IsTerminalReader(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd())
}

// IsTerminalWriter 判断 writer 是否是 TTY。
func IsTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd())
}

// IsTerminalStdin 便捷函数：判断 os.Stdin 是否是 TTY。
func IsTerminalStdin() bool {
	return IsTerminalReader(os.Stdin)
}

// ColorizeError 在启用颜色时把字符串染成红色，否则原样返回。
// 用于 REPL 错误消息高亮。
func ColorizeError(s string) string {
	if !color.NoColor {
		return color.RedString(s)
	}
	return s
}

// ColorizeSuccess 在启用颜色时把字符串染成绿色，否则原样返回。
// 用于 REPL 成功消息高亮（如 "再见!"）。
func ColorizeSuccess(s string) string {
	if !color.NoColor {
		return color.GreenString(s)
	}
	return s
}

// ColorizeNull 在启用颜色时把字符串染成黄色，否则原样返回。
// 用于 NULL 单元格高亮。
func ColorizeNull(s string) string {
	if !color.NoColor {
		return color.YellowString(s)
	}
	return s
}

// ColorizePrompt 在启用颜色时把字符串染成青色加粗，否则原样返回。
// 用于首行提示符高亮。
func ColorizePrompt(s string) string {
	if !color.NoColor {
		c := color.New(color.FgCyan, color.Bold)
		return c.Sprint(s)
	}
	return s
}

// DisableColorGlobally 强制关闭 fatih/color 的全局颜色输出。
// 由 main 在 NO_COLOR 存在或检测到非 TTY 时调用一次。
func DisableColorGlobally() {
	color.NoColor = true
}

// EnableColorGlobally 启用 fatih/color 的全局颜色输出。
func EnableColorGlobally() {
	color.NoColor = false
}

// LinerSession 封装 liner.State 的生命周期，向外暴露 Prompt/PasswordPrompt/AppendHistory/Close。
// 主进程退出前必须调用 Close，否则终端状态可能未还原。
type LinerSession struct {
	state    *liner.State
	history  []string
	capacity int
}

// NewLinerSession 创建并初始化一个 Liner 会话。
//
//   - historyFile：历史记录文件路径；空字符串表示不持久化。
//   - maxEntries：历史记录最大条数；<=0 时使用 defaultHistoryMaxEntries。
//
// 返回的 session 必须在使用完毕后调用 Close，否则终端可能处于异常状态。
// prompt 参数当前未直接使用（保留供未来扩展），调用方可通过 PromptWith 指定每次提示符。
func NewLinerSession(_ string, historyFile string, maxEntries int) (*LinerSession, error) {
	state := liner.NewLiner()
	state.SetCtrlCAborts(true)
	if maxEntries <= 0 {
		maxEntries = defaultHistoryMaxEntries
	}
	session := &LinerSession{state: state, capacity: maxEntries}
	if historyFile != "" {
		if f, err := os.Open(historyFile); err == nil {
			_, _ = state.ReadHistory(f)
			_ = f.Close()
			// 同步加载文件到 session.history，便于历史查询与回放。
			if data, err := os.ReadFile(historyFile); err == nil {
				for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
					if line == "" {
						continue
					}
					session.history = append(session.history, line)
				}
				if len(session.history) > maxEntries {
					session.history = session.history[len(session.history)-maxEntries:]
				}
			}
		}
	}
	return session, nil
}

// Prompt 输出一行提示符并返回用户输入。
// 多行 SQL 时由调用方控制循环；本方法只读取单行。
func (s *LinerSession) Prompt() (string, error) {
	line, err := s.state.Prompt("")
	if err != nil {
		return "", err
	}
	return line, nil
}

// PromptWith 输出指定提示符并返回用户输入。
// prompt 通常为 ANSI 高亮后的字符串；空字符串表示不输出提示。
func (s *LinerSession) PromptWith(prompt string) (string, error) {
	return s.state.Prompt(prompt)
}

// ContinuationPrompt 输出续行提示符并返回用户输入。
func (s *LinerSession) ContinuationPrompt(cont string) (string, error) {
	s.state.SetCtrlCAborts(true)
	return s.state.Prompt(cont)
}

// AppendHistory 将输入行加入历史。重复行不重复添加；超过容量时丢弃最早条目。
func (s *LinerSession) AppendHistory(line string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}
	// 简单去重：与最后一条相同则不追加
	if n := len(s.history); n > 0 && s.history[n-1] == trimmed {
		return
	}
	s.history = append(s.history, trimmed)
	if len(s.history) > s.capacity {
		s.history = s.history[len(s.history)-s.capacity:]
	}
	s.state.AppendHistory(trimmed)
}

// SetCompleter 注册 Tab 补全函数。
func (s *LinerSession) SetCompleter(f func(line string) []string) {
	if f == nil {
		s.state.SetCompleter(func(_ string) []string { return nil })
		return
	}
	s.state.SetCompleter(f)
}

// Close 关闭 liner 会话并尝试将历史写回磁盘。
// historyFile 为空时仅还原终端状态，不写文件。
func (s *LinerSession) Close(historyFile string) {
	if historyFile == "" {
		_ = s.state.Close()
		return
	}
	if f, err := os.Create(historyFile); err == nil {
		_, _ = s.state.WriteHistory(f)
		_ = f.Close()
	}
	_ = s.state.Close()
}

// State 返回底层 liner.State，谨慎使用以免破坏封装。
// 当前仅用于需要直接访问 liner 行为的场景。
func (s *LinerSession) State() *liner.State {
	return s.state
}
