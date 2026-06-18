package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/config"
	"github.com/what-is-me-vibe-coding/test-db/pkg/render"
	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

const testListenAddr = "127.0.0.1:0"

// newTestServer 创建并启动一个使用临时目录与随机端口的服务器。
func newTestServer(t *testing.T) *server.Server {
	t.Helper()
	cfg := server.Config{
		TCPAddr:         testListenAddr,
		HTTPAddr:        testListenAddr,
		DataDir:         t.TempDir(),
		MaxMemTableSize: 1024 * 1024,
	}
	srv, err := server.NewServer(cfg, server.WithMetricsRegistry(prometheus.NewRegistry()))
	if err != nil {
		t.Fatalf("创建服务器失败: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("启动服务器失败: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	return srv
}

// seedTable 通过 Catalog API 建表并通过 ExecuteWrite 写入数据。
// SQL 仅支持 SELECT，DDL/DML 需走 Catalog/Write API。
func seedTable(t *testing.T, srv *server.Server, name string, rows []map[string]any) {
	t.Helper()
	if err := srv.Catalog().CreateTable(name, []catalog.ColumnDef{
		{Name: "id", Type: common.TypeInt64, Nullable: false},
		{Name: "name", Type: common.TypeString, Nullable: true},
	}, []string{"id"}, catalog.TableOptions{}); err != nil {
		t.Fatalf("建表 %s 失败: %v", name, err)
	}
	if len(rows) == 0 {
		return
	}
	resp, err := srv.ExecuteWrite(name, rows)
	if err != nil {
		t.Fatalf("写入 %s 失败: %v", name, err)
	}
	if resp.Code != 0 {
		t.Fatalf("写入 %s 失败: %s", name, resp.Message)
	}
}

func TestMainBuild(_ *testing.T) {
	// 验证 main 包可以成功构建
}

// TestRunREPLCreateInsertSelect 验证 REPL 进程内查询流程。
// 建表与写入走 Catalog/ExecuteWrite API（SQL 仅支持 SELECT），
// REPL 中执行 SELECT 验证进程内查询路径正确。
func TestRunREPLCreateInsertSelect(t *testing.T) {
	srv := newTestServer(t)
	seedTable(t, srv, "t", []map[string]any{
		{"id": int64(1), "name": "alice"},
		{"id": int64(2), "name": "bob"},
	})
	input := strings.NewReader("SELECT * FROM t;\n\\q\n")
	var out bytes.Buffer
	code := runREPL(srv, render.FormatPretty, input, &out)
	if code != 0 {
		t.Fatalf("runREPL 退出码 = %d, want 0", code)
	}
	output := out.String()
	if !strings.Contains(output, "再见") {
		t.Errorf("输出缺少退出提示，输出: %s", output)
	}
	if !strings.Contains(output, "alice") || !strings.Contains(output, "bob") {
		t.Errorf("输出缺少查询结果 alice/bob，输出: %s", output)
	}
}

// TestRunREPLEOF 验证 REPL 在 EOF 时正常退出。
func TestRunREPLEOF(t *testing.T) {
	srv := newTestServer(t)
	input := strings.NewReader("SELECT 1;\n")
	var out bytes.Buffer
	code := runREPL(srv, render.FormatPretty, input, &out)
	if code != 0 {
		t.Fatalf("runREPL 退出码 = %d, want 0", code)
	}
}

// TestRunREPLHelpCommand 验证 \h 命令输出帮助文本。
func TestRunREPLHelpCommand(t *testing.T) {
	srv := newTestServer(t)
	input := strings.NewReader("\\h\n\\q\n")
	var out bytes.Buffer
	_ = runREPL(srv, render.FormatPretty, input, &out)
	if !strings.Contains(out.String(), "可用命令") {
		t.Errorf("输出缺少帮助文本，输出: %s", out.String())
	}
}

// TestRunREPLStatusCommand 验证 \status 命令显示服务状态。
func TestRunREPLStatusCommand(t *testing.T) {
	srv := newTestServer(t)
	input := strings.NewReader("\\status\n\\q\n")
	var out bytes.Buffer
	_ = runREPL(srv, render.FormatPretty, input, &out)
	if !strings.Contains(out.String(), "正常") {
		t.Errorf("输出缺少服务状态，输出: %s", out.String())
	}
}

// TestRunREPLAddrsCommand 验证 \addrs 命令显示监听地址。
func TestRunREPLAddrsCommand(t *testing.T) {
	srv := newTestServer(t)
	input := strings.NewReader("\\addrs\n\\q\n")
	var out bytes.Buffer
	_ = runREPL(srv, render.FormatPretty, input, &out)
	output := out.String()
	if !strings.Contains(output, "TCP:") {
		t.Errorf("输出缺少 TCP 地址，输出: %s", output)
	}
	if !strings.Contains(output, "HTTP:") {
		t.Errorf("输出缺少 HTTP 地址，输出: %s", output)
	}
}

// TestRunREPLUnknownCommand 验证未知命令给出提示。
func TestRunREPLUnknownCommand(t *testing.T) {
	srv := newTestServer(t)
	input := strings.NewReader("\\unknown\n\\q\n")
	var out bytes.Buffer
	_ = runREPL(srv, render.FormatPretty, input, &out)
	if !strings.Contains(out.String(), "未知命令") {
		t.Errorf("输出缺少未知命令提示，输出: %s", out.String())
	}
}

// TestRunREPLMultiLineSQL 验证多行 SQL（以分号结尾）被正确收集。
func TestRunREPLMultiLineSQL(t *testing.T) {
	srv := newTestServer(t)
	input := strings.NewReader(
		"CREATE TABLE\n  multi (id INT64, PRIMARY KEY(id));\n\\q\n",
	)
	var out bytes.Buffer
	code := runREPL(srv, render.FormatPretty, input, &out)
	if code != 0 {
		t.Fatalf("runREPL 退出码 = %d, want 0", code)
	}
}

// TestRunREPLEmptyLineIgnored 验证空行被忽略。
func TestRunREPLEmptyLineIgnored(t *testing.T) {
	srv := newTestServer(t)
	input := strings.NewReader("\n  \n\\q\n")
	var out bytes.Buffer
	code := runREPL(srv, render.FormatPretty, input, &out)
	if code != 0 {
		t.Fatalf("runREPL 退出码 = %d, want 0", code)
	}
}

// TestRunREPLInvalidSQL 验证无效 SQL 返回错误信息但不退出。
func TestRunREPLInvalidSQL(t *testing.T) {
	srv := newTestServer(t)
	input := strings.NewReader("INVALID SQL !!!\n\\q\n")
	var out bytes.Buffer
	code := runREPL(srv, render.FormatPretty, input, &out)
	if code != 0 {
		t.Fatalf("runREPL 退出码 = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "错误") {
		t.Errorf("输出缺少错误信息，输出: %s", out.String())
	}
}

// TestRunOneShotSuccess 验证 -e 模式执行 SELECT 查询后退出。
func TestRunOneShotSuccess(t *testing.T) {
	srv := newTestServer(t)
	seedTable(t, srv, "one", []map[string]any{{"id": int64(1), "name": "ok"}})
	var out, errOut bytes.Buffer
	code := runOneShot(srv, render.FormatPretty, "SELECT * FROM one", &out, &errOut)
	if code != 0 {
		t.Fatalf("runOneShot 退出码 = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "ok") {
		t.Errorf("输出缺少查询结果，输出: %s", out.String())
	}
}

// TestRunOneShotQuery 验证 -e 模式执行查询并输出结果。
func TestRunOneShotQuery(t *testing.T) {
	srv := newTestServer(t)
	seedTable(t, srv, "q", []map[string]any{{"id": int64(1), "name": "hello"}})
	var out, errOut bytes.Buffer
	code := runOneShot(srv, render.FormatPretty, "SELECT * FROM q", &out, &errOut)
	if code != 0 {
		t.Fatalf("runOneShot 退出码 = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Errorf("输出缺少查询结果，输出: %s", out.String())
	}
}

// TestHandleCommandQuit 验证 \q 命令返回 true（应退出）。
func TestHandleCommandQuit(t *testing.T) {
	srv := newTestServer(t)
	format := render.FormatPretty
	var out bytes.Buffer
	if shouldExit := handleCommand(srv, &format, &out, "\\q"); !shouldExit {
		t.Error("\\q 应返回 true")
	}
	if shouldExit := handleCommand(srv, &format, &out, "\\quit"); !shouldExit {
		t.Error("\\quit 应返回 true")
	}
}

// TestHandleCommandNonExit 验证非退出命令返回 false。
func TestHandleCommandNonExit(t *testing.T) {
	srv := newTestServer(t)
	format := render.FormatPretty
	var out bytes.Buffer
	for _, cmd := range []string{"\\h", "\\status", "\\addrs", "\\unknown"} {
		if shouldExit := handleCommand(srv, &format, &out, cmd); shouldExit {
			t.Errorf("%s 不应返回 true", cmd)
		}
	}
}

// TestReadMultiLineSQLSingleLine 验证单行 SQL（带分号）直接返回。
func TestReadMultiLineSQLSingleLine(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader(""))
	var out bytes.Buffer
	sql := readMultiLineSQL(scanner, &out, "SELECT 1;")
	if sql != "SELECT 1" {
		t.Errorf("sql = %q, want %q", sql, "SELECT 1")
	}
}

// TestReadMultiLineSQLMultiLine 验证多行 SQL 被正确拼接。
func TestReadMultiLineSQLMultiLine(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("id INT64, PRIMARY KEY(id));\n"))
	var out bytes.Buffer
	sql := readMultiLineSQL(scanner, &out, "CREATE TABLE t (")
	want := "CREATE TABLE t ( id INT64, PRIMARY KEY(id))"
	if sql != want {
		t.Errorf("sql = %q, want %q", sql, want)
	}
}

// TestRunMainWithArgsInvalidFlag 验证无效参数返回非零退出码。
func TestRunMainWithArgsInvalidFlag(t *testing.T) {
	code := runMainWithArgs([]string{"--invalid-flag"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if code == 0 {
		t.Fatal("预期非零退出码")
	}
}

// TestRunMainWithArgsGenConfig 验证 -gen-config 生成模板后返回 0。
func TestRunMainWithArgsGenConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "widb.yaml")
	code := runMainWithArgs([]string{"-gen-config", path}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("预期退出码 0，实际 %d", code)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("配置模板未生成: %v", err)
	}
	if _, err := config.Load(path); err != nil {
		t.Errorf("生成的模板无法加载: %v", err)
	}
}

// TestRunMainWithArgsGenConfigOverwrite 验证 -gen-config 拒绝覆盖。
func TestRunMainWithArgsGenConfigOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "widb.yaml")
	if err := os.WriteFile(path, []byte("existing"), 0o644); err != nil {
		t.Fatalf("写入占位文件失败: %v", err)
	}
	code := runMainWithArgs([]string{"-gen-config", path}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if code != 1 {
		t.Fatalf("预期退出码 1，实际 %d", code)
	}
}

// TestRunMainWithArgsConfigNotFound 验证 -config 指向不存在文件返回 1。
func TestRunMainWithArgsConfigNotFound(t *testing.T) {
	code := runMainWithArgs(
		[]string{"-config", filepath.Join(t.TempDir(), "nonexistent.yaml")},
		strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{},
	)
	if code != 1 {
		t.Fatalf("预期退出码 1，实际 %d", code)
	}
}

// TestRunMainWithArgsConfigInvalidValue 验证配置值不合法返回 1。
func TestRunMainWithArgsConfigInvalidValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "widb.yaml")
	content := "storage:\n  max_memtable_size: 0\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入配置文件失败: %v", err)
	}
	code := runMainWithArgs([]string{"-config", path}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if code != 1 {
		t.Fatalf("预期退出码 1，实际 %d", code)
	}
}

// TestRunMainWithArgsInvalidDataDir 验证无效数据目录返回 1。
func TestRunMainWithArgsInvalidDataDir(t *testing.T) {
	code := runMainWithArgs(
		[]string{"-data", "/proc/invalid/no-permission/data"},
		strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{},
	)
	if code != 1 {
		t.Fatalf("预期退出码 1，实际 %d", code)
	}
}

// TestRunMainWithArgsOneShot 验证 -e 模式端到端流程。
func TestRunMainWithArgsOneShot(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	code := runMainWithArgs(
		[]string{"-data", dir, "-tcp", "127.0.0.1:0", "-http", "127.0.0.1:0",
			"-e", "CREATE TABLE e2e (id INT64, PRIMARY KEY(id))"},
		strings.NewReader(""), &out, &errOut,
	)
	if code != 0 {
		t.Fatalf("预期退出码 0，实际 %d，stderr: %s", code, errOut.String())
	}
}

// TestToServerConfig 验证 YAML 配置到服务层配置的转换。
func TestToServerConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Server.TCPAddr = "127.0.0.1:7000"
	cfg.Server.HTTPAddr = "127.0.0.1:7001"
	cfg.Storage.DataDir = "/tmp/data"
	cfg.Storage.MaxMemTableSize = 1024
	cfg.Scheduler.Enabled = false
	cfg.Scheduler.FlushInterval = config.Duration(3 * time.Second)
	cfg.Scheduler.WALCleanThreshold = 2048

	got := toServerConfig(cfg)
	if got.TCPAddr != "127.0.0.1:7000" {
		t.Errorf("TCPAddr = %q, want 127.0.0.1:7000", got.TCPAddr)
	}
	if got.HTTPAddr != "127.0.0.1:7001" {
		t.Errorf("HTTPAddr = %q, want 127.0.0.1:7001", got.HTTPAddr)
	}
	if got.DataDir != "/tmp/data" {
		t.Errorf("DataDir = %q, want /tmp/data", got.DataDir)
	}
	if got.MaxMemTableSize != 1024 {
		t.Errorf("MaxMemTableSize = %d, want 1024", got.MaxMemTableSize)
	}
	if got.EnableScheduler {
		t.Error("EnableScheduler = true, want false")
	}
	if got.SchedulerConfig.FlushInterval != 3*time.Second {
		t.Errorf("FlushInterval = %v, want 3s", got.SchedulerConfig.FlushInterval)
	}
	if got.SchedulerConfig.WALCleanThreshold != 2048 {
		t.Errorf("WALCleanThreshold = %d, want 2048", got.SchedulerConfig.WALCleanThreshold)
	}
}

// TestCLIFlagsApplyOverrides 验证命令行参数覆盖配置文件值。
func TestCLIFlagsApplyOverrides(t *testing.T) {
	c := newCLIFlags()
	if err := c.fs.Parse([]string{
		"-tcp", "127.0.0.1:9999",
		"-data", "/custom/data",
		"-max-memtable", "12345",
		"-scheduler.flush-interval", "7s",
	}); err != nil {
		t.Fatalf("解析参数失败: %v", err)
	}

	cfg := config.Default()
	c.applyOverrides(&cfg)

	if cfg.Server.TCPAddr != "127.0.0.1:9999" {
		t.Errorf("TCPAddr = %q, want 127.0.0.1:9999", cfg.Server.TCPAddr)
	}
	if cfg.Server.HTTPAddr != "0.0.0.0:8080" {
		t.Errorf("HTTPAddr = %q, want 默认 0.0.0.0:8080", cfg.Server.HTTPAddr)
	}
	if cfg.Storage.DataDir != "/custom/data" {
		t.Errorf("DataDir = %q, want /custom/data", cfg.Storage.DataDir)
	}
	if cfg.Storage.MaxMemTableSize != 12345 {
		t.Errorf("MaxMemTableSize = %d, want 12345", cfg.Storage.MaxMemTableSize)
	}
	if time.Duration(cfg.Scheduler.FlushInterval) != 7*time.Second {
		t.Errorf("FlushInterval = %v, want 7s", cfg.Scheduler.FlushInterval)
	}
}

// TestCLIFlagsNoOverridesKeepsDefaults 验证不传覆盖参数时保留默认值。
func TestCLIFlagsNoOverridesKeepsDefaults(t *testing.T) {
	c := newCLIFlags()
	if err := c.fs.Parse([]string{}); err != nil {
		t.Fatalf("解析参数失败: %v", err)
	}

	cfg := config.Default()
	c.applyOverrides(&cfg)

	if cfg.Server.TCPAddr != "0.0.0.0:9000" {
		t.Errorf("TCPAddr = %q, want 默认 0.0.0.0:9000", cfg.Server.TCPAddr)
	}
	if cfg.Storage.DataDir != "./data" {
		t.Errorf("DataDir = %q, want 默认 ./data", cfg.Storage.DataDir)
	}
}

// TestLoadConfigDefault 验证未找到配置文件时返回默认配置。
func TestLoadConfigDefault(t *testing.T) {
	cfg, err := loadConfig("")
	if err != nil {
		t.Fatalf("loadConfig 失败: %v", err)
	}
	if cfg.Server.TCPAddr != "0.0.0.0:9000" {
		t.Errorf("默认 TCPAddr = %q, want 0.0.0.0:9000", cfg.Server.TCPAddr)
	}
}

// TestLoadConfigFromYAML 验证从 YAML 文件加载配置。
func TestLoadConfigFromYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "widb.yaml")
	if err := config.GenerateTemplate(path); err != nil {
		t.Fatalf("生成配置模板失败: %v", err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig 失败: %v", err)
	}
	if cfg.Storage.DataDir != "./data" {
		t.Errorf("DataDir = %q, want ./data", cfg.Storage.DataDir)
	}
}

// --- 美化输出（issue #180）测试 ---

// TestRunREPLPrettyDefault 验证 REPL 默认使用 pretty 格式渲染为表格。
func TestRunREPLPrettyDefault(t *testing.T) {
	srv := newTestServer(t)
	seedTable(t, srv, "pt", []map[string]any{
		{"id": int64(1), "name": "alice"},
	})
	input := strings.NewReader("SELECT * FROM pt;\n\\q\n")
	var out bytes.Buffer
	code := runREPL(srv, render.FormatPretty, input, &out)
	if code != 0 {
		t.Fatalf("runREPL 退出码 = %d, want 0", code)
	}
	output := out.String()
	// pretty 表格应包含圆角边框字符
	if !strings.Contains(output, "│") {
		t.Errorf("pretty 输出应包含表格边框 '│': %q", output)
	}
	if !strings.Contains(output, "alice") {
		t.Errorf("pretty 输出应包含数据 alice: %q", output)
	}
}

// TestRunREPLFormatShow 验证 \format 命令显示当前格式。
func TestRunREPLFormatShow(t *testing.T) {
	srv := newTestServer(t)
	input := strings.NewReader("\\format\n\\q\n")
	var out bytes.Buffer
	_ = runREPL(srv, render.FormatPretty, input, &out)
	if !strings.Contains(out.String(), "当前格式") {
		t.Errorf("输出应包含当前格式: %q", out.String())
	}
}

// TestRunREPLFormatSwitch 验证 \format csv 切换格式后输出为 CSV。
func TestRunREPLFormatSwitch(t *testing.T) {
	srv := newTestServer(t)
	seedTable(t, srv, "sw", []map[string]any{
		{"id": int64(1), "name": "alice"},
	})
	input := strings.NewReader("\\format csv\nSELECT * FROM sw;\n\\q\n")
	var out bytes.Buffer
	code := runREPL(srv, render.FormatPretty, input, &out)
	if code != 0 {
		t.Fatalf("runREPL 退出码 = %d, want 0", code)
	}
	output := out.String()
	if !strings.Contains(output, "已切换到 csv 格式") {
		t.Errorf("应显示切换成功: %q", output)
	}
	// CSV 首行应为列名
	if !strings.Contains(output, "id,name") {
		t.Errorf("csv 输出应包含列名行: %q", output)
	}
	// CSV 格式不应包含表格边框
	if strings.Contains(output, "│") {
		t.Errorf("csv 输出不应包含表格边框: %q", output)
	}
}

// TestRunREPLFormatUnknown 验证未知格式给出提示。
func TestRunREPLFormatUnknown(t *testing.T) {
	srv := newTestServer(t)
	input := strings.NewReader("\\format xml\n\\q\n")
	var out bytes.Buffer
	_ = runREPL(srv, render.FormatPretty, input, &out)
	if !strings.Contains(out.String(), "未知格式") {
		t.Errorf("应显示未知格式提示: %q", out.String())
	}
}

// TestRunOneShotCSVFormat 验证 -e 模式使用 csv 格式输出。
func TestRunOneShotCSVFormat(t *testing.T) {
	srv := newTestServer(t)
	seedTable(t, srv, "csv_t", []map[string]any{
		{"id": int64(1), "name": "alice"},
	})
	var out, errOut bytes.Buffer
	code := runOneShot(srv, render.FormatCSV, "SELECT * FROM csv_t", &out, &errOut)
	if code != 0 {
		t.Fatalf("runOneShot 退出码 = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "id,name") {
		t.Errorf("csv 输出应包含列名行: %q", out.String())
	}
}

// TestRunMainWithArgsFormatFlag 验证 -format 标志端到端流程。
func TestRunMainWithArgsFormatFlag(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	code := runMainWithArgs(
		[]string{"-data", dir, "-tcp", "127.0.0.1:0", "-http", "127.0.0.1:0",
			"-format", "csv", "-e", "CREATE TABLE fmt_e2e (id INT64, PRIMARY KEY(id))"},
		strings.NewReader(""), &out, &errOut,
	)
	if code != 0 {
		t.Fatalf("预期退出码 0，实际 %d，stderr: %s", code, errOut.String())
	}
}

// TestRunMainWithArgsFormatInvalid 验证 -format 无效值返回 1。
func TestRunMainWithArgsFormatInvalid(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	code := runMainWithArgs(
		[]string{"-data", dir, "-tcp", "127.0.0.1:0", "-http", "127.0.0.1:0",
			"-format", "xml", "-e", "SELECT 1"},
		strings.NewReader(""), &out, &errOut,
	)
	if code != 1 {
		t.Fatalf("预期退出码 1，实际 %d", code)
	}
	if !strings.Contains(errOut.String(), "未知输出格式") {
		t.Errorf("stderr 应包含错误: %q", errOut.String())
	}
}
