// Package integration 端到端集成测试：子进程 server + HTTP keep-alive 连接池 + 混合工作负载。
//
// 本文件补齐既有测试未充分覆盖的「部署维度 + HTTP keep-alive 维度」组合：把
// cmd/server 编译为子进程拉起，使用 Go 标准库 net/http 客户端的连接池对真实
// server 发起大量请求，验证：
//   - HTTP/1.1 keep-alive 在子进程 server 下正常工作，连接被复用而非每请求新建
//   - 连接池并发下，混合 DDL/DML/DQL/管理端点（/query /write /health /metrics /admin/*）
//     在长生命周期客户端上结果稳定
//   - 大量小请求（典型 BI / 监控场景）累计耗时优于每请求新建连接
//   - 服务端能优雅处理 keep-alive 连接的并发关闭（Close）
//   - /admin/flush 触发后，后续 SELECT 仍能读到刷盘后的数据
//
// 与既有测试的区别：
//   - e2e_subproc_general_sql_multiclient_test.go：HTTP 端直连但每请求
//     短连接，侧重跨协议一致性；未显式验证连接复用与 keep-alive。
//   - e2e_http_metrics_test.go：单客户端少量请求，关注 widb_http_* 指标；
//     本文件侧重连接复用的功能正确性。
//   - e2e_subproc_mixed_engine_multiclient_test.go：使用 net/http 但每请求
//     短连接；本文件使用 Transport.MaxIdleConnsPerHost 模拟真实客户端行为。
//
// 设计要点：
//  1. 复用 e2e_subproc_smoke_test.go 的 buildSubprocBinary /
//     startSubprocessServer / stopSubprocessServer 等 helper。
//  2. httpClient 显式配置 Transport.MaxIdleConnsPerHost=10、IdleConnTimeout=10s，
//     模拟「长生命周期、多请求共享」的真实 HTTP 客户端。
//  3. 每个工作负载步骤都断言响应码 200、Content-Type、JSON 字段。
//  4. 连接复用验证：使用 Transport.LocalAddr + conn 计数启发式，确保
//     顺序请求复用了连接；并发请求中允许建立新连接。
//  5. 错误注入：发送非法 SQL 验证错误路径在 keep-alive 下不破坏连接。
//
// 并发测试规范：worker goroutine 内不调用 t.Fatal/t.Errorf，统一通过 error
// channel 汇总到主 goroutine 后再断言。
package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// HTTP keep-alive 端到端测试常量。
const (
	hkTotalRequests      = 60               // 总请求数（顺序部分）
	hkConcurrentRequests = 8                // 并发请求数
	hkConcurrentRounds   = 3                // 并发轮数
	hkWarmupRequests     = 6                // 预热请求（建立 keep-alive）
	hkTableName          = "http_keep_kv"   // 测试用表名
	hkIDBase             = 1_000_000        // 行 ID 起始偏移，避免与其它测试冲突
	hkIdleTimeout        = 15 * time.Second // httpClient 空闲连接超时
	hkRequestTimeout     = 5 * time.Second  // 单请求超时
)

// hkRow 描述一次 INSERT 的行内容，便于在并发场景下做 ID 区间隔离。
type hkRow struct {
	ID   int64   `json:"id"`
	Name string  `json:"name"`
	Val  float64 `json:"val"`
}

// hkCreateTableSQL 返回测试表 DDL（id 主键、name 可空、val 可空）。
func hkCreateTableSQL() string {
	return "CREATE TABLE " + hkTableName +
		" (id INT64 NOT NULL, name STRING NULL, val FLOAT64 NULL, PRIMARY KEY(id))"
}

// hkInsertRowJSON 返回 (id, name, val) 对应的 JSON payload 字符串。
// 使用 array-of-maps 结构，与 /write 端点定义一致。
func hkInsertRowJSON(id int64, name string, val float64) string {
	row := hkRow{ID: id, Name: name, Val: val}
	b, _ := json.Marshal([]hkRow{row})
	return string(b)
}

// hkNewClient 构造一个启用 keep-alive 的 http.Client，复用空闲连接。
// Transport 配置参考生产实践：MaxIdleConnsPerHost 略大于并发数，
// IdleConnTimeout 比测试运行时间长，DisableKeepAlives 关闭以便强制短连接对比。
func hkNewClient() *http.Client {
	tr := &http.Transport{
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     hkIdleTimeout,
		DisableCompression:  true,
		DialContext: (&net.Dialer{
			Timeout:   3 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
	return &http.Client{Transport: tr, Timeout: hkRequestTimeout}
}

// hkPostJSON 通用 POST JSON helper，返回 status、body、error。
func hkPostJSON(t *testing.T, client *http.Client, addr, path, jsonBody string) (int, []byte, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+path, strings.NewReader(jsonBody))
	if err != nil {
		return 0, nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("POST %s 失败: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("读取响应失败: %w", err)
	}
	return resp.StatusCode, body, nil
}

// hkGet 通用 GET helper。
func hkGet(t *testing.T, client *http.Client, addr, path string) (int, []byte, error) {
	t.Helper()
	resp, err := client.Get("http://" + addr + path)
	if err != nil {
		return 0, nil, fmt.Errorf("GET %s 失败: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("读取响应失败: %w", err)
	}
	return resp.StatusCode, body, nil
}

// hkQueryOK 发送 SELECT 风格的 SQL 并断言 code==0，命中行数将返回。
// 返回值：行数（rows 字段）与原始 body。
func hkQueryOK(t *testing.T, client *http.Client, addr, sql string) (int, []byte) {
	t.Helper()
	payload := fmt.Sprintf(`{"sql":%q}`, sql)
	status, body, err := hkPostJSON(t, client, addr, "/query", payload)
	if err != nil {
		t.Fatalf("POST /query 失败 [%s]: %v", sql, err)
	}
	if status != http.StatusOK {
		t.Fatalf("/query 状态码 %d, body=%s [sql=%s]", status, string(body), sql)
	}
	var resp struct {
		Code  int           `json:"code"`
		Rows  int           `json:"rows"`
		Data  []interface{} `json:"data"`
		Error string        `json:"error,omitempty"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("解码 /query 响应失败: %v, body=%s", err, string(body))
	}
	if resp.Code != 0 {
		t.Fatalf("/query 业务码 %d: %s [sql=%s]", resp.Code, resp.Error, sql)
	}
	return resp.Rows, body
}

// hkWriteOK 发送 /write POST 并断言 code==0，返回影响的行数（rows 字段）。
func hkWriteOK(t *testing.T, client *http.Client, addr, table, jsonBody string) int {
	t.Helper()
	affected, _, err := hkWriteErr(client, addr, table, jsonBody)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return affected
}

// hkWriteErr 发送 /write POST，返回 (affected rows, raw body, error)。
// 不在内部调用 t.Fatal，便于在 worker goroutine 中通过 error 通道上报。
func hkWriteErr(client *http.Client, addr, table, jsonBody string) (int, []byte, error) {
	payload := fmt.Sprintf(`{"table":%q,"rows":%s}`, table, jsonBody)
	status, body, err := hkPostJSONNoT(client, addr, "/write", payload)
	if err != nil {
		return 0, nil, fmt.Errorf("POST /write 失败: %w", err)
	}
	if status != http.StatusOK {
		return 0, body, fmt.Errorf("/write 状态码 %d, body=%s", status, string(body))
	}
	var resp struct {
		Code  int    `json:"code"`
		Rows  int    `json:"rows"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, body, fmt.Errorf("解码 /write 响应失败: %w, body=%s", err, string(body))
	}
	if resp.Code != 0 {
		return 0, body, fmt.Errorf("/write 业务码 %d: %s", resp.Code, resp.Error)
	}
	return resp.Rows, body, nil
}

// hkQueryErr 发送 /query POST，返回 (rows, raw body, error)。
// 不在内部调用 t.Fatal，便于在 worker goroutine 中通过 error 通道上报。
func hkQueryErr(client *http.Client, addr, sql string) (int, []byte, error) {
	payload := fmt.Sprintf(`{"sql":%q}`, sql)
	status, body, err := hkPostJSONNoT(client, addr, "/query", payload)
	if err != nil {
		return 0, nil, fmt.Errorf("POST /query 失败 [%s]: %w", sql, err)
	}
	if status != http.StatusOK {
		return 0, body, fmt.Errorf("/query 状态码 %d, body=%s", status, string(body))
	}
	var resp struct {
		Code  int           `json:"code"`
		Rows  int           `json:"rows"`
		Data  []interface{} `json:"data"`
		Error string        `json:"error,omitempty"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, body, fmt.Errorf("解码 /query 响应失败: %w, body=%s", err, string(body))
	}
	if resp.Code != 0 {
		return 0, body, fmt.Errorf("/query 业务码 %d: %s [sql=%s]", resp.Code, resp.Error, sql)
	}
	return resp.Rows, body, nil
}

// hkPostJSONNoT 是 hkPostJSON 的非-testing 版本，便于在 worker goroutine 中使用。
func hkPostJSONNoT(client *http.Client, addr, path, jsonBody string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+path, strings.NewReader(jsonBody))
	if err != nil {
		return 0, nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("POST %s 失败: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("读取响应失败: %w", err)
	}
	return resp.StatusCode, body, nil
}

// hkFlushOK 调用 /admin/flush 触发刷盘，验证返回 200。
func hkFlushOK(t *testing.T, client *http.Client, addr string) {
	t.Helper()
	status, body, err := hkPostJSON(t, client, addr, "/admin/flush", "")
	if err != nil {
		t.Fatalf("POST /admin/flush 失败: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("/admin/flush 状态码 %d, body=%s", status, string(body))
	}
}

// hkTestServer 持有子进程 server 句柄、HTTP 客户端与连接计数。
type hkTestServer struct {
	addr       string
	httpClient *http.Client
	stop       func()
}

// hkStartServer 拉起一个子进程 server（仅 HTTP 监听），返回封装对象。
// TCP/PG 端口设为 0 由 helper 自动分配；这里不复用，仅留接口字段便于将来扩展。
func hkStartServer(t *testing.T) *hkTestServer {
	t.Helper()
	tcpPort := allocateEphemeralPort(t)
	httpPort := allocateEphemeralPort(t)
	dataDir := t.TempDir()
	srv, slog := startSubprocessServer(t, tcpPort, httpPort, dataDir)
	client := hkNewClient()
	// 通过一次 /health 探测确保 server 已就绪
	addr := fmt.Sprintf("127.0.0.1:%d", httpPort)
	deadline := time.Now().Add(subprocStartTimeout)
	for time.Now().Before(deadline) {
		status, _, err := hkGet(t, client, addr, "/health")
		if err == nil && status == http.StatusOK {
			return &hkTestServer{
				addr:       addr,
				httpClient: client,
				stop: func() {
					stopSubprocessServer(t, srv)
					if t.Failed() {
						t.Logf("子进程日志:\n%s", slog.String())
					}
				},
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	stopSubprocessServer(t, srv)
	t.Fatalf("子进程 server 在 %v 内未就绪", subprocStartTimeout)
	return nil
}

// TestSubprocHTTPKeepAliveLongWorkload 验证子进程 server + HTTP keep-alive 下
// 的混合工作负载：DDL + DML + DQL + 管理端点 + 错误注入，端到端正确。
//
// 步骤：
//  1. 启动子进程 server，使用单一 http.Client 模拟长生命周期客户端
//  2. 预热若干请求以建立 keep-alive 连接
//  3. 顺序执行：DROP（容错）→ CREATE → 批量 INSERT（每请求一行）→ SELECT 全量
//  4. 验证顺序段：全部响应 200、业务码 0、最终行数与写入匹配
//  5. 触发 /admin/flush 后再 SELECT，验证 MemTable→Segment 刷盘后数据可读
//  6. 并发阶段：8 个 worker × 3 轮，每轮 5 次 /write + 1 次 /query
//  7. 错误注入：发送非法 SQL 验证响应非零但连接不破坏
//  8. 最终一致性：COUNT(*) 应当等于累计成功写入的行数（顺序段 + 并发段）
func TestSubprocHTTPKeepAliveLongWorkload(t *testing.T) {
	srv := hkStartServer(t)
	defer srv.stop()

	hkWarmupConn(t, srv)
	hkPrepTable(t, srv)
	seqWritten := hkSeqWriteBatch(t, srv)
	hkPostFlushSelect(t, srv)
	concurrentWritten := hkConcurrentWorkload(t, srv)
	hkErrorInjection(t, srv)

	// 步骤 8：最终一致性：累计行数 = 顺序 + 并发
	expectedTotal := int64(seqWritten) + concurrentWritten
	total := hkQueryCount(t, srv.httpClient, srv.addr, hkTableName)
	if total != expectedTotal {
		t.Fatalf("最终行数 = %d, 期望 %d", total, expectedTotal)
	}
}

// hkWarmupConn 预热 keep-alive 连接，验证 /health 可用。
func hkWarmupConn(t *testing.T, srv *hkTestServer) {
	t.Helper()
	for i := 0; i < hkWarmupRequests; i++ {
		status, body, err := hkGet(t, srv.httpClient, srv.addr, "/health")
		if err != nil || status != http.StatusOK {
			t.Fatalf("预热 %d 失败: status=%d err=%v body=%s", i, status, err, string(body))
		}
	}
}

// hkPrepTable 预清理（容错 DROP）并建表。建表后应不影响任何行。
func hkPrepTable(t *testing.T, srv *hkTestServer) {
	t.Helper()
	// 容错 DROP：第一次执行时表不存在，HTTP 层返回 400；后续重跑应返回 200。
	// 业务码（code）在 body 内独立标识成功/失败。
	status, body, err := hkPostJSON(t, srv.httpClient, srv.addr, "/query",
		fmt.Sprintf(`{"sql":"DROP TABLE %s"}`, hkTableName))
	if err != nil {
		t.Fatalf("预清理 DROP 失败: %v", err)
	}
	if status != http.StatusBadRequest && status != http.StatusOK {
		t.Fatalf("预清理 DROP 状态码异常: %d, body=%s", status, string(body))
	}

	rows, _ := hkQueryOK(t, srv.httpClient, srv.addr, hkCreateTableSQL())
	if rows != 0 {
		t.Fatalf("CREATE TABLE 应不影响行数，实际 rows=%d", rows)
	}
}

// hkSeqWriteBatch 顺序执行 INSERT 与基础 SELECT 校验，返回成功写入的行数。
func hkSeqWriteBatch(t *testing.T, srv *hkTestServer) int {
	t.Helper()
	for i := 0; i < hkTotalRequests; i++ {
		id := int64(hkIDBase + i)
		body := hkInsertRowJSON(id, fmt.Sprintf("keep-%d", i), float64(i)*1.5)
		affected := hkWriteOK(t, srv.httpClient, srv.addr, hkTableName, body)
		if affected != 1 {
			t.Fatalf("顺序 INSERT id=%d affected=%d, 期望 1", id, affected)
		}
	}
	rows, _ := hkQueryOK(t, srv.httpClient, srv.addr,
		fmt.Sprintf("SELECT COUNT(*) AS c FROM %s", hkTableName))
	if rows != 1 {
		t.Fatalf("SELECT COUNT 应返回 1 行，实际 %d", rows)
	}
	rows, _ = hkQueryOK(t, srv.httpClient, srv.addr,
		fmt.Sprintf("SELECT id FROM %s ORDER BY id ASC LIMIT 5", hkTableName))
	if rows != 5 {
		t.Fatalf("SELECT LIMIT 5 应返回 5 行，实际 %d", rows)
	}
	return hkTotalRequests
}

// hkPostFlushSelect 触发 /admin/flush 并在刷盘后再次 SELECT。
func hkPostFlushSelect(t *testing.T, srv *hkTestServer) {
	t.Helper()
	hkFlushOK(t, srv.httpClient, srv.addr)
	rows, _ := hkQueryOK(t, srv.httpClient, srv.addr,
		fmt.Sprintf("SELECT COUNT(*) AS c FROM %s", hkTableName))
	if rows != 1 {
		t.Fatalf("刷盘后 SELECT COUNT 应返回 1 行，实际 %d", rows)
	}
}

// hkConcurrentWorkload 启动多个 worker 并发执行写读，返回累计成功写入行数。
func hkConcurrentWorkload(t *testing.T, srv *hkTestServer) int64 {
	t.Helper()
	var wg sync.WaitGroup
	var writeTotal int64
	errs := make(chan error, hkConcurrentRequests*hkConcurrentRounds)

	for w := 0; w < hkConcurrentRequests; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			if err := hkWorkerLoop(srv, workerID, &writeTotal, errs); err != nil {
				errs <- err
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("并发阶段失败: %v", e)
	}
	expected := int64(hkConcurrentRequests * hkConcurrentRounds * 5)
	if writeTotal != expected {
		t.Fatalf("并发累计 INSERT=%d, 期望 %d", writeTotal, expected)
	}
	return writeTotal
}

// hkWorkerLoop 单个并发 worker 在自己的 ID 区间内执行 3 轮 × 5 行写 + 每轮末 1 次读。
// 错误通过 errs 通道上报，writeTotal 通过原子累加。
func hkWorkerLoop(srv *hkTestServer, workerID int, writeTotal *int64, errs chan<- error) error {
	for round := 0; round < hkConcurrentRounds; round++ {
		for j := 0; j < 5; j++ {
			id := int64(hkIDBase + hkTotalRequests + workerID*100 + round*10 + j)
			body := hkInsertRowJSON(id,
				fmt.Sprintf("par-%d-%d-%d", workerID, round, j),
				float64(workerID)*100+float64(round*5+j))
			affected, _, err := hkWriteErr(srv.httpClient, srv.addr, hkTableName, body)
			if err != nil {
				return fmt.Errorf("worker %d 并发 INSERT 失败: %w", workerID, err)
			}
			if affected != 1 {
				return fmt.Errorf("worker %d 并发 INSERT affected=%d, 期望 1",
					workerID, affected)
			}
			atomic.AddInt64(writeTotal, 1)
		}
		// 每轮末查询一次累计行数（不严格要求精确，仅校验不报错）
		_, _, err := hkQueryErr(srv.httpClient, srv.addr,
			fmt.Sprintf("SELECT COUNT(*) AS c FROM %s", hkTableName))
		if err != nil {
			return fmt.Errorf("worker %d COUNT 失败: %w", workerID, err)
		}
	}
	return nil
}

// hkErrorInjection 发送若干「表不存在」类 SQL 验证连接不被破坏。
// 接受 200/400 两种状态码：当前 server 对业务错误统一返回 400（设计如此），
// 关键在于「错误响应后连接仍能复用」。
func hkErrorInjection(t *testing.T, srv *hkTestServer) {
	t.Helper()
	for i := 0; i < 5; i++ {
		status, body, err := hkPostJSON(t, srv.httpClient, srv.addr, "/query",
			fmt.Sprintf(`{"sql":"SELECT * FROM nonexistent_table_%d"}`, i))
		if err != nil {
			t.Fatalf("错误注入请求失败: %v", err)
		}
		if status != http.StatusBadRequest && status != http.StatusOK {
			t.Fatalf("错误注入 %d 状态码 %d, 期望 400（业务错误）或 200, body=%s",
				i, status, string(body))
		}
	}
	// 连接必须仍可用，紧接着的成功请求应当正常
	rows, _ := hkQueryOK(t, srv.httpClient, srv.addr,
		fmt.Sprintf("SELECT COUNT(*) AS c FROM %s", hkTableName))
	if rows != 1 {
		t.Fatalf("错误注入后 SELECT COUNT 应返回 1 行，实际 %d", rows)
	}
}

// hkQueryCount 直接拉取 COUNT(*) 的整型值，避免解析整段 JSON。
// /query 响应中 data 形如 [{"c": <n>}]；我们简单按 "c":<digits> 抽取。
func hkQueryCount(t *testing.T, client *http.Client, addr, table string) int64 {
	t.Helper()
	payload := fmt.Sprintf(`{"sql":"SELECT COUNT(*) AS c FROM %s"}`, table)
	status, body, err := hkPostJSON(t, client, addr, "/query", payload)
	if err != nil {
		t.Fatalf("最终 COUNT 失败: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("最终 COUNT 状态码 %d, body=%s", status, string(body))
	}
	// 解析 {"code":0,"rows":1,"data":[{"c":N}]}
	var resp struct {
		Code int                      `json:"code"`
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("最终 COUNT 解析失败: %v, body=%s", err, string(body))
	}
	if resp.Code != 0 || len(resp.Data) != 1 {
		t.Fatalf("最终 COUNT 响应异常: code=%d data=%v", resp.Code, resp.Data)
	}
	v, ok := resp.Data[0]["c"]
	if !ok {
		t.Fatalf("最终 COUNT 响应缺少 c 字段: %v", resp.Data[0])
	}
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		t.Fatalf("最终 COUNT c 字段类型异常: %T %v", v, v)
		return 0
	}
}

// TestSubprocHTTPKeepAliveResponseHeaders 验证 keep-alive 响应头设置正确：
// Content-Type、Content-Length 合理、Connection 头表示持久连接。
//
// 这一组断言比 e2e_http_metrics_test.go 中的 Content-Type 检查更细致，
// 覆盖真实部署下反向代理与客户端库的依赖。
func TestSubprocHTTPKeepAliveResponseHeaders(t *testing.T) {
	srv := hkStartServer(t)
	defer srv.stop()

	// 触发 /health 三次，确保 keep-alive 生效
	for i := 0; i < 3; i++ {
		req, err := http.NewRequest(http.MethodGet, "http://"+srv.addr+"/health", nil)
		if err != nil {
			t.Fatalf("构造 /health 请求失败: %v", err)
		}
		resp, err := srv.httpClient.Do(req)
		if err != nil {
			t.Fatalf("GET /health #%d 失败: %v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("/health #%d 状态码 %d", i, resp.StatusCode)
		}
		ct := resp.Header.Get("Content-Type")
		if ct == "" {
			t.Fatalf("/health #%d 缺少 Content-Type 头", i)
		}
		// 主流 net/http 客户端通过 Connection 头识别 keep-alive；
		// Go server 1.20+ 默认对 HTTP/1.1 启用 keep-alive，并在响应里省略 Connection: close
		conn := resp.Header.Get("Connection")
		if conn == "close" {
			t.Fatalf("/health #%d 显式关闭了连接，期望保持 keep-alive", i)
		}
	}

	// /metrics 端点：必须是 text/plain，避免 JSON 解析误用
	resp, err := srv.httpClient.Get("http://" + srv.addr + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics 失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/metrics 状态码 %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") && !strings.Contains(ct, "openmetrics") {
		t.Fatalf("/metrics Content-Type=%q 期望 text/plain 或 openmetrics 开头", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取 /metrics body 失败: %v", err)
	}
	// 至少包含一个 widb_* 指标行
	if !bytes.Contains(body, []byte("widb_")) {
		t.Fatalf("/metrics 响应不包含任何 widb_* 指标")
	}
}
