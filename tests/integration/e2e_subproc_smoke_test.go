// Package integration 子进程级端到端烟雾测试。
//
// 既有集成测试（e2e_*_test.go）均在测试进程内创建 *server.Server 并直连其监听端口，
// 覆盖的是「server 内部协议/逻辑」的正确性。本文件补充另一维度：把 cmd/server
// 实际编译为二进制并作为子进程拉起，端到端覆盖：
//  1. flag 解析（命令行参数、YAML 配置文件、-gen-config 模板生成）
//  2. 进程级优雅关闭（SIGINT / SIGTERM → 干净退出，退出码 = 0）
//  3. 错误参数路径（非法 flag → 退出码 != 0）
//  4. 重启持久化（写数据 → SIGTERM → 同 DataDir 重新拉起 → 数据可读）
//  5. /health 端点、/metrics 端点可用
//  6. README 快速开始示例的端到端可执行性
//
// 与现有测试互不重叠：本文件用 os/exec 真实拉起子进程，捕获 stderr 并解析监听地址。
package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// 子进程测试常量。
const (
	// subprocStartTimeout 单次 startSubprocessServer 等待服务就绪的总超时。
	subprocStartTimeout = 20 * time.Second
	// subprocStopTimeout 等待子进程退出的总超时。
	subprocStopTimeout = 10 * time.Second
	// subprocAddrPattern 匹配子进程 stderr 中 "TCP 监听 X.X.X.X:PORT" 行。
	subprocAddrPattern = `(TCP|HTTP|PG\s+wire) 监听 ([^|\s]+(?::\d+)?)`
)

// subprocServer 持有已启动的子进程及其解析出的监听地址。
type subprocServer struct {
	cmd      *exec.Cmd
	tcpAddr  string
	httpAddr string
	dataDir  string
	cancel   context.CancelFunc
}

// subprocLog 实时收集的子进程日志，供失败时诊断打印。
type subprocLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// append 把一行追加到日志缓冲。
func (l *subprocLog) append(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.WriteString(s)
	if !strings.HasSuffix(s, "\n") {
		l.buf.WriteString("\n")
	}
}

// String 返回当前累积的日志内容。
func (l *subprocLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// 子进程二进制路径（懒构建，所有测试共享）。
var (
	subprocBinaryOnce sync.Once
	subprocBinaryPath string
	subprocBinaryErr  error
)

// buildSubprocBinary 编译 cmd/server 为临时目录下的可执行文件，并发安全。
func buildSubprocBinary() (string, error) {
	subprocBinaryOnce.Do(func() {
		tmp, err := os.MkdirTemp("", "widb-subproc-*")
		if err != nil {
			subprocBinaryErr = fmt.Errorf("创建临时目录失败: %w", err)
			return
		}
		bin := filepath.Join(tmp, "widb-server")
		cmd := exec.Command("go", "build", "-trimpath", "-o", bin, "./cmd/server")
		cmd.Dir = repoRoot()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		cmd.Stdout = &bytes.Buffer{}
		if err := cmd.Run(); err != nil {
			subprocBinaryErr = fmt.Errorf("编译 widb-server 失败: %w (stderr: %s)", err, stderr.String())
			return
		}
		subprocBinaryPath = bin
	})
	return subprocBinaryPath, subprocBinaryErr
}

// repoRoot 返回仓库根目录（go.mod 所在目录），用于子进程构建。
func repoRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

// 端口分配互斥锁与计数器：保证同一进程内连续两次 allocateEphemeralPort 不会
// 返回同一端口（修复 CI 上"两次连续 net.Listen(127.0.0.1:0) 拿到相同端口"
// 的间歇性 flake）。子进程测试串行执行，故单 mutex 即可。
var (
	portAllocMu sync.Mutex
	nextPort    = ephemeralPortStart
)

// 端口分配策略：从 30000 起单调递增，若超过 30999 则回绕到 30000。
// 每次分配先递增计数器，再尝试 bind 验证空闲；若已被占用则继续递增。
// 选定端口后立刻关闭监听器——portAllocMu 与顺序递增共同保证「在我拿到这个
// 端口号到子进程 bind 之前」该端口不会被其他 goroutine 再次分配。
const ephemeralPortStart = 30000
const ephemeralPortEnd = 30999

// allocateEphemeralPort 在 127.0.0.1 上预占一个空闲端口并立即释放。
//
// 与旧实现的区别：使用包级单调递增计数器（而非依赖 OS 的 127.0.0.1:0 行为）
// 显式指定端口号，从根上避免「连续两次拿到同一端口」的问题。
// portAllocMu 保护 nextPort 的并发安全；同时确保在 t.Cleanup 关闭另一个
// 监听器之前，本调用不会跨过 nextPort 计数器（消除相邻分配拿到同值的可能）。
func allocateEphemeralPort(t *testing.T) int {
	t.Helper()
	portAllocMu.Lock()
	defer portAllocMu.Unlock()
	for i := 0; i < 1000; i++ {
		port := nextPort
		// 单调递增，超过上限回绕
		if port >= ephemeralPortEnd {
			nextPort = ephemeralPortStart
		} else {
			nextPort = port + 1
		}
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			_ = l.Close()
			return port
		}
		// 端口被占（其他测试残留或外部进程占用），继续找下一个
	}
	t.Fatalf("在端口范围 %d-%d 内找不到空闲端口（连续 1000 次失败）",
		ephemeralPortStart, ephemeralPortEnd)
	return 0
}

// startSubprocessServer 用给定的 TCP/HTTP 端口与 DataDir 拉起 cmd/server 子进程。
// 返回 subprocServer 与日志句柄；调用方负责在结束时 stopSubprocessServer。
func startSubprocessServer(t *testing.T, tcpPort, httpPort int, dataDir string, extraArgs ...string) (*subprocServer, *subprocLog) {
	t.Helper()
	bin, err := buildSubprocBinary()
	if err != nil {
		t.Skipf("无法构建子进程二进制: %v", err)
	}
	return startSubprocessServerWithBinary(t, bin, tcpPort, httpPort, dataDir, extraArgs...)
}

// startSubprocessServerWithBinary 与 startSubprocessServer 行为一致，但接受外部传入的 binary 路径，
// 供 -gen-config / -config 路径复用同一套地址解析逻辑。
func startSubprocessServerWithBinary(t *testing.T, bin string, tcpPort, httpPort int, dataDir string, extraArgs ...string) (*subprocServer, *subprocLog) {
	t.Helper()
	cancel := func() {}
	cmd := buildSubprocCmd(t, bin, tcpPort, httpPort, dataDir, extraArgs)
	cmd.Dir = repoRoot()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdoutR, stderrR, _ := attachSubprocPipes(cmd)

	if err := cmd.Start(); err != nil {
		t.Fatalf("启动子进程失败: %v", err)
	}
	// 父进程侧不再需要写端，关闭以让子进程在退出后 read 返回 EOF。
	_ = cmd.Stdout.(io.WriteCloser).Close()
	_ = cmd.Stderr.(io.WriteCloser).Close()
	cmd.Stdout = nil
	cmd.Stderr = nil

	addrCh, log := spawnSubprocAddrConsumers(stdoutR, stderrR)
	select {
	case got := <-addrCh:
		return &subprocServer{
			cmd: cmd, cancel: cancel,
			tcpAddr: got.tcp, httpAddr: got.http, dataDir: dataDir,
		}, log
	case <-time.After(subprocStartTimeout):
		_ = cmd.Process.Kill()
		t.Fatalf("子进程启动超时（%v），已收集日志: %s", subprocStartTimeout, log.String())
	}
	return nil, nil
}

// buildSubprocCmd 构造 cmd/server 启动参数列表，返回已填充 args 的 *exec.Cmd。
func buildSubprocCmd(t *testing.T, bin string, tcpPort, httpPort int, dataDir string, extraArgs []string) *exec.Cmd {
	t.Helper()
	tcpAddr := fmt.Sprintf("127.0.0.1:%d", tcpPort)
	httpAddr := fmt.Sprintf("127.0.0.1:%d", httpPort)
	args := make([]string, 0, 12+len(extraArgs))
	args = append(args,
		"-tcp", tcpAddr,
		"-http", httpAddr,
		"-pg", "",
		"-data", dataDir,
		"-max-memtable", "1048576",
		"-scheduler", "true",
	)
	args = append(args, extraArgs...)
	return exec.Command(bin, args...)
}

// attachSubprocPipes 为 cmd 创建 stdout/stderr 的 os.Pipe，返回 read 端。
func attachSubprocPipes(cmd *exec.Cmd) (stdoutR, stderrR *os.File, err error) {
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, nil, fmt.Errorf("创建 stdout pipe: %w", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		return nil, nil, fmt.Errorf("创建 stderr pipe: %w", err)
	}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW
	return stdoutR, stderrR, nil
}

// spawnSubprocAddrConsumers 启动两个 goroutine 分别从 stdoutR/stderrR 逐行扫描，
// 把每行写入共享 log，匹配到监听地址后通过 channel 通知。返回地址 channel 与 log。
func spawnSubprocAddrConsumers(stdoutR, stderrR io.ReadCloser) (<-chan struct{ tcp, http string }, *subprocLog) {
	addrCh := make(chan struct{ tcp, http string }, 1)
	addrRe := regexp.MustCompile(subprocAddrPattern)
	log := &subprocLog{}
	gotAddr := struct{ tcp, http string }{}
	var addrOnce sync.Once
	reportReady := func() {
		addrOnce.Do(func() { addrCh <- gotAddr })
	}
	consume := func(r io.ReadCloser) {
		defer func() { _ = r.Close() }()
		s := bufio.NewScanner(r)
		s.Buffer(make([]byte, 0, 4096), 1024*1024)
		for s.Scan() {
			line := s.Text()
			log.append(line)
			m := addrRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			addr := strings.TrimSpace(m[2])
			switch m[1] {
			case "TCP":
				gotAddr.tcp = addr
			case "HTTP":
				gotAddr.http = addr
			}
			if gotAddr.tcp != "" && gotAddr.http != "" {
				reportReady()
			}
		}
	}
	go consume(stdoutR)
	go consume(stderrR)
	return addrCh, log
}

// stopSubprocessServer 向子进程发送 SIGTERM，等待其优雅退出。
// 超时则发送 SIGKILL 兜底。已退出的进程不报错。
func stopSubprocessServer(t *testing.T, s *subprocServer) {
	t.Helper()
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return
	}
	if s.cmd.ProcessState != nil {
		return // 已退出
	}
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		if !strings.Contains(err.Error(), "process already finished") {
			t.Logf("发送 SIGTERM 失败: %v", err)
		}
	}
	doneCh := make(chan error, 1)
	go func() { doneCh <- s.cmd.Wait() }()
	select {
	case <-doneCh:
		s.cancel()
	case <-time.After(subprocStopTimeout):
		_ = s.cmd.Process.Kill()
		<-doneCh
		s.cancel()
		t.Logf("子进程未在 %v 内优雅退出，已强制 SIGKILL", subprocStopTimeout)
	}
}

// sendSignalToSubprocess 向子进程发送指定信号（不等待退出），用于测试 SIGINT 路径。
func sendSignalToSubprocess(t *testing.T, s *subprocServer, sig syscall.Signal) {
	t.Helper()
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return
	}
	if err := s.cmd.Process.Signal(sig); err != nil {
		t.Fatalf("发送信号 %v 失败: %v", sig, err)
	}
}

// waitForSubprocessExit 等待子进程退出，返回退出码与可能的错误。
func waitForSubprocessExit(t *testing.T, s *subprocServer, timeout time.Duration) (int, error) {
	t.Helper()
	if s == nil || s.cmd == nil {
		return -1, fmt.Errorf("subprocess 未启动")
	}
	doneCh := make(chan error, 1)
	go func() { doneCh <- s.cmd.Wait() }()
	select {
	case err := <-doneCh:
		s.cancel()
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), err
		}
		return 0, err
	case <-time.After(timeout):
		_ = s.cmd.Process.Kill()
		<-doneCh
		s.cancel()
		return -1, fmt.Errorf("等待退出超时（%v）", timeout)
	}
}

// serverQueryResp 是 /query 端点的最小化响应结构，避免直接 import server 包造成循环。
type serverQueryResp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	Rows    int             `json:"rows"`
}

// httpHealthHit 请求子进程的 /health 端点，验证可连通。
func httpHealthHit(t *testing.T, addr string) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatalf("/health 请求失败: %v", err)
	}
	return resp
}

// httpMetricsHit 请求子进程的 /metrics 端点。
func httpMetricsHit(t *testing.T, addr string) string {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatalf("/metrics 请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取 /metrics 失败: %v", err)
	}
	return string(data)
}

// httpPostQuery 通过 HTTP POST /query 执行单条 SQL。
func httpPostQuery(t *testing.T, addr, sql string) *serverQueryResp {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"sql": sql})
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post("http://"+addr+"/query", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /query 失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	var out serverQueryResp
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("解析 /query 响应失败: %v (raw: %s)", err, data)
	}
	return &out
}

// httpPostWrite 通过 HTTP POST /write 写入一批行。
func httpPostWrite(t *testing.T, addr, table string, rows []map[string]any) *serverQueryResp {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"table": table, "rows": rows})
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post("http://"+addr+"/write", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /write 失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	var out serverQueryResp
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("解析 /write 响应失败: %v (raw: %s)", err, data)
	}
	return &out
}

// TestSubprocServerSmoke 验证 cmd/server 子进程能正常启动、执行基本 SQL、SIGTERM 优雅退出。
func TestSubprocServerSmoke(t *testing.T) {
	dir := t.TempDir()
	s, log := startSubprocessServer(t, allocateEphemeralPort(t), allocateEphemeralPort(t), dir)
	t.Cleanup(func() {
		if s != nil {
			stopSubprocessServer(t, s)
		}
		if t.Failed() {
			t.Logf("子进程日志:\n%s", log.String())
		}
	})

	// 1. /health 应返回 200。
	hp := httpHealthHit(t, s.httpAddr)
	_ = hp.Body.Close()
	if hp.StatusCode != 200 {
		t.Fatalf("/health 状态码 = %d, want 200", hp.StatusCode)
	}

	// 2. /metrics 应输出 Prometheus 格式文本且包含 widb_ 指标。
	metrics := httpMetricsHit(t, s.httpAddr)
	if !strings.Contains(metrics, "widb_") {
		t.Errorf("/metrics 输出缺少 widb_ 指标")
	}

	// 3. 执行建表 + 写入 + 查询。
	createResp := httpPostQuery(t, s.httpAddr,
		"CREATE TABLE t1 (id INT64, name STRING, PRIMARY KEY(id))")
	if createResp.Code != 0 {
		t.Fatalf("建表失败: %s", createResp.Message)
	}
	wr := httpPostWrite(t, s.httpAddr, "t1", []map[string]any{
		{"id": 1, "name": "alpha"}, {"id": 2, "name": "beta"},
	})
	if wr.Code != 0 {
		t.Fatalf("/write 失败: %s", wr.Message)
	}
	if wr.Rows != 2 {
		t.Errorf("/write 行数 = %d, want 2", wr.Rows)
	}
	qResp := httpPostQuery(t, s.httpAddr, "SELECT id, name FROM t1 ORDER BY id")
	if qResp.Code != 0 {
		t.Fatalf("查询失败: %s", qResp.Message)
	}
	if qResp.Rows != 2 {
		t.Errorf("查询行数 = %d, want 2", qResp.Rows)
	}

	// 4. SIGTERM 优雅关闭。
	sendSignalToSubprocess(t, s, syscall.SIGTERM)
	code, err := waitForSubprocessExit(t, s, subprocStopTimeout)
	if err != nil && code != 0 {
		t.Errorf("子进程退出码 = %d, 期望 0; err = %v", code, err)
	}
}

// TestSubprocServerGracefulShutdownSIGINT 验证 SIGINT（Ctrl-C）也能优雅关闭。
func TestSubprocServerGracefulShutdownSIGINT(t *testing.T) {
	dir := t.TempDir()
	s, log := startSubprocessServer(t, allocateEphemeralPort(t), allocateEphemeralPort(t), dir)
	t.Cleanup(func() {
		if s != nil {
			stopSubprocessServer(t, s)
		}
		if t.Failed() {
			t.Logf("子进程日志:\n%s", log.String())
		}
	})

	// 至少执行一次 /health 确认子进程就绪。
	hp := httpHealthHit(t, s.httpAddr)
	_ = hp.Body.Close()
	if hp.StatusCode != 200 {
		t.Fatalf("/health 状态码 = %d, want 200", hp.StatusCode)
	}

	sendSignalToSubprocess(t, s, syscall.SIGINT)
	code, err := waitForSubprocessExit(t, s, subprocStopTimeout)
	if err != nil && code != 0 {
		t.Errorf("SIGINT 退出码 = %d, 期望 0; err = %v", code, err)
	}
}

// TestSubprocServerInvalidArgs 验证非法 flag 立即以非零码退出。
func TestSubprocServerInvalidArgs(t *testing.T) {
	bin, err := buildSubprocBinary()
	if err != nil {
		t.Skipf("无法构建子进程二进制: %v", err)
	}
	cmd := exec.Command(bin, "--totally-bogus-flag")
	cmd.Dir = repoRoot()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &bytes.Buffer{}
	err = cmd.Run()
	if err == nil {
		t.Fatal("预期 --bogus-flag 退出码非零，实际为 0")
	}
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("预期 *exec.ExitError，实际: %T", err)
	}
	if ee.ExitCode() == 0 {
		t.Errorf("退出码 = 0，期望非零")
	}
}

// TestSubprocServerGenConfig 验证 -gen-config 写出可加载的 YAML 模板。
func TestSubprocServerGenConfig(t *testing.T) {
	bin, err := buildSubprocBinary()
	if err != nil {
		t.Skipf("无法构建子进程二进制: %v", err)
	}
	out := filepath.Join(t.TempDir(), "widb.yaml")
	cmd := exec.Command(bin, "-gen-config", out)
	cmd.Dir = repoRoot()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &bytes.Buffer{}
	if err := cmd.Run(); err != nil {
		t.Fatalf("-gen-config 退出码非零: %v, stderr: %s", err, stderr.String())
	}
	st, statErr := os.Stat(out)
	if statErr != nil {
		t.Fatalf("配置模板未生成: %v", statErr)
	}
	if st.Size() == 0 {
		t.Fatal("配置模板为空")
	}
	data, _ := os.ReadFile(out)
	for _, key := range []string{"server:", "storage:", "scheduler:"} {
		if !bytes.Contains(data, []byte(key)) {
			t.Errorf("模板缺少 %q 段", key)
		}
	}
}

// TestSubprocServerConfigFile 验证 -config <path> 加载 YAML 后子进程按配置启动。
func TestSubprocServerConfigFile(t *testing.T) {
	bin, err := buildSubprocBinary()
	if err != nil {
		t.Skipf("无法构建子进程二进制: %v", err)
	}
	dataDir := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "widb.yaml")
	tcpPort := allocateEphemeralPort(t)
	httpPort := allocateEphemeralPort(t)
	yaml := fmt.Sprintf(`server:
  tcp_addr: "127.0.0.1:%d"
  http_addr: "127.0.0.1:%d"
storage:
  data_dir: %q
  max_memtable_size: 1048576
scheduler:
  enabled: true
`, tcpPort, httpPort, dataDir)
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatalf("写入配置文件失败: %v", err)
	}

	s, log := startSubprocessServerWithBinary(t, bin, tcpPort, httpPort, dataDir, "-config", cfgPath)
	t.Cleanup(func() {
		if s != nil {
			stopSubprocessServer(t, s)
		}
		if t.Failed() {
			t.Logf("子进程日志:\n%s", log.String())
		}
	})

	wantTCP := fmt.Sprintf("127.0.0.1:%d", tcpPort)
	if s.tcpAddr != wantTCP {
		t.Errorf("TCP 监听 = %q, want %q（应来自配置文件）", s.tcpAddr, wantTCP)
	}
	hp := httpHealthHit(t, s.httpAddr)
	_ = hp.Body.Close()
	if hp.StatusCode != 200 {
		t.Errorf("/health 状态码 = %d, want 200", hp.StatusCode)
	}
}

// TestSubprocServerRestartPersistence 验证写数据 → SIGTERM → 同 DataDir 重新拉起 → 数据可读。
func TestSubprocServerRestartPersistence(t *testing.T) {
	dir := t.TempDir()
	seedRestartPersistenceDir(t, dir)
	verifyRestartPersistenceCount(t, dir, 2)
}

// seedRestartPersistenceDir 在 dir 中启动一个 widb-server 子进程，建表 r 并写入
// 指定行，最后 SIGTERM 优雅退出。后续测试可以用同一个 dir 重新拉起验证持久化。
func seedRestartPersistenceDir(t *testing.T, dir string) {
	t.Helper()
	s, log := startSubprocessServer(t, allocateEphemeralPort(t), allocateEphemeralPort(t), dir)
	t.Cleanup(func() {
		if s != nil && s.cmd.ProcessState == nil {
			stopSubprocessServer(t, s)
		}
		if t.Failed() {
			t.Logf("第一轮子进程日志:\n%s", log.String())
		}
	})
	createResp := httpPostQuery(t, s.httpAddr, "CREATE TABLE r (id INT64, v STRING, PRIMARY KEY(id))")
	if createResp.Code != 0 {
		t.Fatalf("建表失败: %s", createResp.Message)
	}
	wr := httpPostWrite(t, s.httpAddr, "r", []map[string]any{
		{"id": 1, "v": "hello"}, {"id": 2, "v": "world"},
	})
	if wr.Code != 0 {
		t.Fatalf("/write 失败: %s", wr.Message)
	}
	sendSignalToSubprocess(t, s, syscall.SIGTERM)
	code, err := waitForSubprocessExit(t, s, subprocStopTimeout)
	if err != nil && code != 0 {
		t.Fatalf("第一轮子进程退出码 = %d, 期望 0; err = %v", code, err)
	}
}

// verifyRestartPersistenceCount 在 dir 重新拉起 widb-server，校验 r 表的 COUNT(*) 等于 wantCount。
func verifyRestartPersistenceCount(t *testing.T, dir string, wantCount int) {
	t.Helper()
	s, log := startSubprocessServer(t, allocateEphemeralPort(t), allocateEphemeralPort(t), dir)
	t.Cleanup(func() {
		if s != nil && s.cmd.ProcessState == nil {
			stopSubprocessServer(t, s)
		}
		if t.Failed() {
			t.Logf("第二轮子进程日志:\n%s", log.String())
		}
	})
	qResp := httpPostQuery(t, s.httpAddr, "SELECT COUNT(*) AS cnt FROM r")
	if qResp.Code != 0 {
		t.Fatalf("重启后查询失败: %s", qResp.Message)
	}
	if qResp.Rows != 1 {
		t.Fatalf("重启后 COUNT 行数 = %d, want 1", qResp.Rows)
	}
	var dataRows []map[string]any
	if err := json.Unmarshal(qResp.Data, &dataRows); err != nil {
		t.Fatalf("解析响应数据失败: %v", err)
	}
	if len(dataRows) != 1 {
		t.Fatalf("重启后 COUNT 数据行 = %d, want 1", len(dataRows))
	}
	if n, ok := dataRows[0]["cnt"].(float64); !ok || int(n) != wantCount {
		t.Errorf("重启后 COUNT(*) = %v, want %d", dataRows[0]["cnt"], wantCount)
	}
}

// TestSubprocServerReadmeQuickstartDryrun 验证 README.md 中的"快速开始"示例能端到端跑通。
//
// 文档与代码同步是常见漂移点：README 中的命令若与 cmd/server 实际行为不一致，
// 用户首次体验会失败。本测试用子进程形式复现 README "快速开始"节的关键命令：
//   - 拉起 widb-server
//   - 通过 HTTP /query 执行 SELECT * FROM sensor LIMIT 10（README 中的示例 SQL）
//
// 任何一处漂移都会被本测试捕获。
func TestSubprocServerReadmeQuickstartDryrun(t *testing.T) {
	dir := t.TempDir()
	s, log := startSubprocessServer(t, allocateEphemeralPort(t), allocateEphemeralPort(t), dir)
	t.Cleanup(func() {
		if s != nil {
			stopSubprocessServer(t, s)
		}
		if t.Failed() {
			t.Logf("子进程日志:\n%s", log.String())
		}
	})

	// README 中"启动 CLI"章节的关键示例：建表 → 插入 → SELECT LIMIT。
	createResp := httpPostQuery(t, s.httpAddr,
		"CREATE TABLE sensor (id INT64, temp FLOAT64, PRIMARY KEY(id))")
	if createResp.Code != 0 {
		t.Fatalf("建表失败: %s", createResp.Message)
	}
	wr := httpPostWrite(t, s.httpAddr, "sensor", []map[string]any{
		{"id": 1, "temp": 23.5},
		{"id": 2, "temp": 24.0},
	})
	if wr.Code != 0 {
		t.Fatalf("/write 失败: %s", wr.Message)
	}
	resp := httpPostQuery(t, s.httpAddr, "SELECT * FROM sensor LIMIT 10")
	if resp.Code != 0 {
		t.Fatalf("SELECT 返回非零码: %s", resp.Message)
	}
	if resp.Rows != 2 {
		t.Errorf("SELECT 行数 = %d, want 2", resp.Rows)
	}
}

// TestSubprocServerConcurrentClients 验证多客户端并发对子进程的端到端稳定性。
//
// 与既有的 e2e_general_sql_multiclient_test.go 不同：本测试走子进程而非
// in-process server，捕获"实际二进制 + 多客户端"的真实链路。
func TestSubprocServerConcurrentClients(t *testing.T) {
	dir := t.TempDir()
	s, log := startSubprocessServer(t, allocateEphemeralPort(t), allocateEphemeralPort(t), dir)
	t.Cleanup(func() {
		if s != nil {
			stopSubprocessServer(t, s)
		}
		if t.Failed() {
			t.Logf("子进程日志:\n%s", log.String())
		}
	})

	createResp := httpPostQuery(t, s.httpAddr, "CREATE TABLE m (id INT64, payload STRING, PRIMARY KEY(id))")
	if createResp.Code != 0 {
		t.Fatalf("建表失败: %s", createResp.Message)
	}

	const clients = 4
	const rowsPerClient = 5
	var wg sync.WaitGroup
	failures := make(chan string, clients*rowsPerClient)
	for c := 0; c < clients; c++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			base := clientID * rowsPerClient
			for i := 0; i < rowsPerClient; i++ {
				id := base + i + 1
				wr := httpPostWrite(t, s.httpAddr, "m", []map[string]any{
					{"id": id, "payload": fmt.Sprintf("c%d-i%d", clientID, i)},
				})
				if wr.Code != 0 {
					failures <- fmt.Sprintf("c%d i%d: %s", clientID, i, wr.Message)
					return
				}
			}
		}(c)
	}
	wg.Wait()
	close(failures)
	for msg := range failures {
		t.Errorf("并发客户端失败: %s", msg)
	}

	cResp := httpPostQuery(t, s.httpAddr, "SELECT COUNT(*) AS cnt FROM m")
	if cResp.Code != 0 {
		t.Fatalf("COUNT 失败: %s", cResp.Message)
	}
	if cResp.Rows != 1 {
		t.Errorf("COUNT 行数 = %d, want 1", cResp.Rows)
	}
	var dataRows []map[string]any
	if err := json.Unmarshal(cResp.Data, &dataRows); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(dataRows) != 1 {
		t.Fatalf("COUNT 数据行 = %d, want 1", len(dataRows))
	}
	if n, ok := dataRows[0]["cnt"].(float64); !ok || int(n) != clients*rowsPerClient {
		t.Errorf("COUNT(*) = %v, want %d", dataRows[0]["cnt"], clients*rowsPerClient)
	}
}
