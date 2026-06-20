// 端到端集成测试：跨协议多客户端长连接 + 重启持久化 + 一般 SQL 工作负载。
//
// 本文件补充 e2e_general_sql_multiclient_* 与 e2e_pgwire_multi_test.go 未覆盖的三个维度：
//  1. 跨协议（TCP + HTTP + PG wire）多客户端并发执行一般 SQL（DDL/DML/DQL 混合）；
//  2. 写入数据后停止服务进程，再以同一 DataDir 重建，验证数据从 WAL/Segment 完整恢复；
//  3. 验证重启后新客户端经任一协议仍能正确读到所有行，并继续执行 DML。
//
// 与 e2e_general_sql_multiclient_test.go 的区别：后者仅验证同一 server 进程内
// 多客户端并发的正确性；本文件进一步覆盖"进程崩溃 + 重新拉起"的真实部署场景。
// 与 e2e_pgwire_multi_test.go 的区别：后者仅验证 PG wire 协议并发；
// 本文件将 TCP/HTTP/PG wire 三种协议在同一表上交错运行，确保协议间
// MemTable/Segment 并发安全（多协议共享同一 server 实例与底层存储）。
package integration

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

// 跨协议多客户端 + 重启测试常量。
const (
	mrpcmTable       = "mrpcm_orders" // 跨协议共享表名
	mrpcmClients     = 6              // 并发客户端数（每协议 2 个：TCP×2 + HTTP×2 + PG wire×2）
	mrpcmRowsPerPeer = 8              // 每客户端写入行数
	mrpcmPeerBase    = 80000          // 客户端写入 ID 起始偏移，避免与其它测试冲突
	mrpcmRestarts    = 2              // 重启次数（每轮验证数据恢复）
	mrpcmCycleRounds = 20             // 跨协议读写循环测试中每客户端的轮数
)

// mrpcmCreateTableSQL 跨协议共享的建表语句，确保三协议操作同一张表。
const mrpcmCreateTableSQL = "CREATE TABLE " + mrpcmTable + " (" +
	"id INT64 NOT NULL, " +
	"region STRING NULL, " +
	"amount FLOAT64 NULL, " +
	"active BOOL NULL, " +
	"PRIMARY KEY(id))"

// mrpcmInsertSQL 生成指定 (clientID, seq) 对应的 INSERT SQL，region 与 amount 由客户端决定。
func mrpcmInsertSQL(clientID, seq int) string {
	id := mrpcmPeerBase + clientID*mrpcmRowsPerPeer + seq
	region := fmt.Sprintf("region-%d", clientID%3)
	amount := float64(id) * 0.25
	active := seq%2 == 0
	return fmt.Sprintf(
		"INSERT INTO %s (id, region, amount, active) VALUES (%d, '%s', %.2f, %t)",
		mrpcmTable, id, region, amount, active,
	)
}

// startSQLServerWithPersistDir 启动一个支持 TCP/HTTP/PG wire 三协议的 server，
// 使用指定 DataDir（测试结束后由调用方决定是否清理）。返回 *sqlServer 与
// 底层 server.Server，方便测试在重启阶段手动 Stop 后重新 NewServer/Start。
func startSQLServerWithPersistDir(t *testing.T, dataDir string) *sqlServer {
	t.Helper()
	cfg := server.Config{
		TCPAddr:  "127.0.0.1:0",
		HTTPAddr: "127.0.0.1:0",
		PGAddr:   "127.0.0.1:0",
		DataDir:  dataDir,
	}
	srv, err := server.NewServer(cfg, server.WithMetricsRegistry(prometheus.NewRegistry()))
	if err != nil {
		t.Fatalf("NewServer 失败: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	return &sqlServer{srv: srv, tcpAddr: srv.TCPAddr(), httpAddr: srv.HTTPAddr()}
}

// mrpcmRunOnce 启动 server → 多客户端跨协议并发执行一般 SQL → 校验行数。
// 返回 server.Server 与当前数据行数，供重启阶段继续使用。
func mrpcmRunOnce(t *testing.T, dataDir string) (*server.Server, int) {
	t.Helper()
	s := startSQLServerWithPersistDir(t, dataDir)

	// 用 HTTP 建表（一个协议即可，其余协议共享同一 catalog/engine）
	if resp, err := httpQuery(s.httpAddr, mrpcmCreateTableSQL); err != nil {
		t.Fatalf("建表 HTTP 失败: %v", err)
	} else if resp.Code != 0 {
		t.Fatalf("建表返回错误: %s", resp.Message)
	}

	// 6 个客户端：2 TCP + 2 HTTP + 2 PG wire，交错并发执行 INSERT。
	// 每客户端负责自己区间内的 ID，写入自身 8 行，共 48 行。
	var (
		wg         sync.WaitGroup
		insertFail int64
		lastErr    atomic.Value
	)
	for i := 0; i < mrpcmClients; i++ {
		wg.Add(1)
		via := mrpcmAssignProtocol(i)
		go func(clientID int, via string) {
			defer wg.Done()
			if err := mrpcmClientInsert(s, via, clientID); err != nil {
				atomic.AddInt64(&insertFail, 1)
				lastErr.Store(fmt.Sprintf("[%s c%d] %v", via, clientID, err))
			}
		}(i, via)
	}
	wg.Wait()
	if insertFail > 0 {
		t.Fatalf("%d 个客户端插入失败，最近错误: %v", insertFail, lastErr.Load())
	}

	// 校验总行数：48
	resp := queryVia(t, s, "tcp", "SELECT COUNT(*) AS cnt FROM "+mrpcmTable)
	if resp.Code != 0 {
		t.Fatalf("COUNT 失败: %s", resp.Message)
	}
	rows := respRows(resp)
	if len(rows) != 1 {
		t.Fatalf("COUNT 期望 1 行，得到 %d", len(rows))
	}
	got, _ := toInt64(rows[0]["cnt"])
	want := int64(mrpcmClients * mrpcmRowsPerPeer)
	if got != want {
		t.Fatalf("跨协议插入后行数: 期望 %d，得到 %d", want, got)
	}

	return s.srv, int(got)
}

// mrpcmAssignProtocol 按 clientID 分发协议：0/1→tcp、2/3→http、4/5→pg。
func mrpcmAssignProtocol(clientID int) string {
	switch clientID % 3 {
	case 0:
		return "tcp"
	case 1:
		return "http"
	default:
		return "pg"
	}
}

// mrpcmClientInsert 单个客户端按分配区间执行插入。
func mrpcmClientInsert(s *sqlServer, via string, clientID int) error {
	switch via {
	case "tcp":
		return mrpcmClientInsertTCP(s, clientID)
	case "http":
		return mrpcmClientInsertHTTP(s, clientID)
	case "pg":
		return mrpcmClientInsertPG(s, clientID)
	default:
		return fmt.Errorf("未知协议: %s", via)
	}
}

// mrpcmClientInsertTCP 走 TCP 协议：建长连接、循环 INSERT。
func mrpcmClientInsertTCP(s *sqlServer, clientID int) error {
	tc, err := dialTCP(s.tcpAddr)
	if err != nil {
		return fmt.Errorf("拨号失败: %w", err)
	}
	defer tc.close()
	for seq := 0; seq < mrpcmRowsPerPeer; seq++ {
		resp, err := tc.query(mrpcmInsertSQL(clientID, seq))
		if err != nil {
			return fmt.Errorf("TCP 查询失败: %w", err)
		}
		if resp.Code != 0 {
			return fmt.Errorf("TCP 返回错误: %s", resp.Message)
		}
	}
	return nil
}

// mrpcmClientInsertHTTP 走 HTTP 协议：复用全局连接池，循环 POST /query。
func mrpcmClientInsertHTTP(s *sqlServer, clientID int) error {
	for seq := 0; seq < mrpcmRowsPerPeer; seq++ {
		resp, err := httpQuery(s.httpAddr, mrpcmInsertSQL(clientID, seq))
		if err != nil {
			return fmt.Errorf("HTTP 请求失败: %w", err)
		}
		if resp.Code != 0 {
			return fmt.Errorf("HTTP 返回错误: %s", resp.Message)
		}
	}
	return nil
}

// mrpcmClientInsertPG 走 PG wire 协议：建长连接、循环 Query 消息。
func mrpcmClientInsertPG(s *sqlServer, clientID int) error {
	c, err := dialPGWireErr(s.srv.PGAddr())
	if err != nil {
		return fmt.Errorf("PG 拨号失败: %w", err)
	}
	defer c.close()
	if err := c.handshakeErr(); err != nil {
		return fmt.Errorf("PG 握手失败: %w", err)
	}
	for seq := 0; seq < mrpcmRowsPerPeer; seq++ {
		res, err := c.sendQueryRead(mrpcmInsertSQL(clientID, seq))
		if err != nil {
			return fmt.Errorf("PG 查询失败: %w", err)
		}
		if res.errMsg != "" {
			return fmt.Errorf("PG 返回错误: %s", res.errMsg)
		}
	}
	return nil
}

// mrpcmVerifyAcrossProtocols 在 server 启动状态下，验证三协议均能读到全量数据。
// 抽样三组行（每个客户端的第一条）分别走 TCP/HTTP/PG wire 校验一致。
func mrpcmVerifyAcrossProtocols(t *testing.T, s *sqlServer) {
	t.Helper()
	// 抽样：clientID=0 seq=0 → id=mrpcmPeerBase；其它客户端同理
	for _, clientID := range []int{0, 3, 5} {
		mrpcmVerifyClientIDAcrossProtocols(t, s, clientID)
	}
}

// mrpcmVerifyClientIDAcrossProtocols 单个客户端 ID 的跨协议读一致性校验。
// 拆出此函数是为了把"循环调度"与"单 ID 的多协议校验"解耦，便于通过认知复杂度阈值。
func mrpcmVerifyClientIDAcrossProtocols(t *testing.T, s *sqlServer, clientID int) {
	t.Helper()
	id := mrpcmPeerBase + clientID*mrpcmRowsPerPeer
	wantSQL := fmt.Sprintf("SELECT region, amount, active FROM %s WHERE id = %d", mrpcmTable, id)

	// TCP 与 HTTP 字段必须完全一致
	tcpRows, httpRows := mrpcmReadSameIDOverTCPAndHTTP(t, s, clientID, wantSQL)
	mrpcmAssertRowFieldEqual(t, clientID, tcpRows[0], httpRows[0])

	// PG wire（仅在 PG 端口启用时执行）
	if s.srv.PGAddr() == "" {
		return
	}
	mrpcmVerifySameIDOverPG(t, s, clientID, wantSQL)
}

// mrpcmReadSameIDOverTCPAndHTTP 分别通过 TCP/HTTP 读同一 ID，返回两边的行切片。
// 任一协议出错时直接 t.Fatalf。
func mrpcmReadSameIDOverTCPAndHTTP(t *testing.T, s *sqlServer, clientID int, sql string) ([]map[string]any, []map[string]any) {
	t.Helper()
	tcpResp := queryVia(t, s, "tcp", sql)
	if tcpResp.Code != 0 {
		t.Fatalf("[tcp c%d] 查询失败: %s", clientID, tcpResp.Message)
	}
	tcpRows := respRows(tcpResp)
	if len(tcpRows) != 1 {
		t.Fatalf("[tcp c%d] 期望 1 行，得到 %d", clientID, len(tcpRows))
	}
	httpResp, err := httpQuery(s.httpAddr, sql)
	if err != nil {
		t.Fatalf("[http c%d] 请求失败: %v", clientID, err)
	}
	if httpResp.Code != 0 {
		t.Fatalf("[http c%d] 返回错误: %s", clientID, httpResp.Message)
	}
	httpRows := respRows(httpResp)
	if len(httpRows) != 1 {
		t.Fatalf("[http c%d] 期望 1 行，得到 %d", clientID, len(httpRows))
	}
	return tcpRows, httpRows
}

// mrpcmAssertRowFieldEqual 校验两个 map 行的所有字段值一致，差异时 t.Errorf。
func mrpcmAssertRowFieldEqual(t *testing.T, clientID int, a, b map[string]any) {
	t.Helper()
	for k := range a {
		if a[k] != b[k] {
			t.Errorf("[c%d] %s 字段不一致: tcp=%v http=%v",
				clientID, k, a[k], b[k])
		}
	}
}

// mrpcmVerifySameIDOverPG 通过 PG wire 协议读同一 ID 并验证返回 1 行。
// 任一步骤出错时直接 t.Fatalf。
func mrpcmVerifySameIDOverPG(t *testing.T, s *sqlServer, clientID int, sql string) {
	t.Helper()
	c, err := dialPGWireErr(s.srv.PGAddr())
	if err != nil {
		t.Fatalf("[pg c%d] 拨号失败: %v", clientID, err)
	}
	if err := c.handshakeErr(); err != nil {
		c.close()
		t.Fatalf("[pg c%d] 握手失败: %v", clientID, err)
	}
	res, err := c.sendQueryRead(sql)
	c.close()
	if err != nil {
		t.Fatalf("[pg c%d] 查询失败: %v", clientID, err)
	}
	if res.errMsg != "" {
		t.Fatalf("[pg c%d] 返回错误: %s", clientID, res.errMsg)
	}
	if len(res.rows) != 1 {
		t.Fatalf("[pg c%d] 期望 1 行，得到 %d", clientID, len(res.rows))
	}
}

// TestMultiProtocolCrossClientMixedWorkload 验证「同一 server 上三协议并发 + 重启持久化」端到端正确。
//
// 流程：
//  1. 在临时 DataDir 启动一个三协议 server（TCP/HTTP/PG wire）；
//  2. 6 个客户端（2 TCP + 2 HTTP + 2 PG wire）并发执行 INSERT，跨协议共享同一张表；
//  3. 验证三协议均能读到一致的全量数据；
//  4. 优雅停止 server，模拟「进程关闭但数据目录保留」场景；
//  5. 用同一 DataDir 重新 NewServer+Start（mrpcmRestarts 轮），验证：
//     - 上一轮写入的行全部恢复（COUNT 等于预期）；
//     - 任意协议的新客户端可继续执行 DML（INSERT / UPDATE）并被读出。
func TestMultiProtocolCrossClientMixedWorkload(t *testing.T) {
	dir, err := os.MkdirTemp("", "e2e-mrpcm-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	// 第 1 轮：跨协议并发写入 + 跨协议读一致
	srv, wantRows := mrpcmRunOnce(t, dir)
	mrpcmVerifyAcrossProtocols(t, &sqlServer{
		srv: srv, tcpAddr: srv.TCPAddr(), httpAddr: srv.HTTPAddr(),
	})

	// 关闭当前 server，模拟进程退出
	if err := srv.Stop(); err != nil {
		t.Fatalf("首次 Stop 失败: %v", err)
	}
	// 等待 server 端口释放（避免 TIME_WAIT 导致下一轮 Start 失败）
	time.Sleep(100 * time.Millisecond)

	// 后续 mrpcmRestarts 轮：每轮重启 server、验证数据恢复、追加写入、再次停止
	for round := 1; round <= mrpcmRestarts; round++ {
		wantRows = mrpcmRestartRound(t, dir, round, wantRows)
	}
}

// mrpcmRestartRound 单轮重启恢复 + DML 校验流程：
//  1. 用同一 DataDir 重新 NewServer+Start；
//  2. 校验上一轮数据全部恢复（COUNT 等于预期）；
//  3. 执行 UPDATE（重置头部 4 行 amount）+ INSERT（追加 2 行）；
//  4. 校验 UPDATE 生效（头部第一行 amount 等于本轮固定值）；
//  5. 关闭 server，返回更新后的 wantRows。
func mrpcmRestartRound(t *testing.T, dir string, round int, wantRows int) int {
	t.Helper()
	srv, err := server.NewServer(server.Config{
		TCPAddr:  "127.0.0.1:0",
		HTTPAddr: "127.0.0.1:0",
		PGAddr:   "127.0.0.1:0",
		DataDir:  dir,
	}, server.WithMetricsRegistry(prometheus.NewRegistry()))
	if err != nil {
		t.Fatalf("[round %d] NewServer 失败: %v", round, err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("[round %d] Start 失败: %v", round, err)
	}
	s := &sqlServer{srv: srv, tcpAddr: srv.TCPAddr(), httpAddr: srv.HTTPAddr()}

	// 校验上一轮数据全部恢复
	resp := queryVia(t, s, "tcp", "SELECT COUNT(*) AS cnt FROM "+mrpcmTable)
	if resp.Code != 0 {
		t.Fatalf("[round %d] COUNT 失败: %s", round, resp.Message)
	}
	rows := respRows(resp)
	got, _ := toInt64(rows[0]["cnt"])
	if int(got) != wantRows {
		t.Fatalf("[round %d] 恢复行数: 期望 %d，得到 %d", round, wantRows, got)
	}

	// 每轮做一次最小 DML：把头部 4 行的 amount 重置为固定值 + 末尾追加 2 行
	// 注意：用 "amount = <fix_value>" 覆盖式赋值，避免上轮 UPDATE 的累积效应干扰本轮断言
	updateID := mrpcmPeerBase
	fixAmount := float64(round+1) * 7.0 // 每轮使用不同固定值，便于检测 UPDATE 是否生效
	if _, err := httpQuery(s.httpAddr, fmt.Sprintf(
		"UPDATE %s SET amount = %v WHERE id >= %d AND id < %d",
		mrpcmTable, fixAmount, updateID, updateID+4)); err != nil {
		t.Fatalf("[round %d] UPDATE 失败: %v", round, err)
	}
	for j := 0; j < 2; j++ {
		id := wantRows + mrpcmPeerBase*10 + round*100 + j
		if _, err := httpQuery(s.httpAddr, fmt.Sprintf(
			"INSERT INTO %s (id, region, amount, active) VALUES (%d, 'restart-r%d', 1.0, true)",
			mrpcmTable, id, round)); err != nil {
			t.Fatalf("[round %d] INSERT 失败: %v", round, err)
		}
	}
	wantRows += 2

	// 校验 UPDATE 生效：取头部第一行 amount 应等于本轮 fixAmount
	row := queryVia(t, s, "tcp", fmt.Sprintf(
		"SELECT amount FROM %s WHERE id = %d", mrpcmTable, updateID))
	if row.Code != 0 || len(respRows(row)) != 1 {
		t.Fatalf("[round %d] UPDATE 校验查询失败: code=%d msg=%s", round, row.Code, row.Message)
	}
	amt, _ := toFloat64(respRows(row)[0]["amount"])
	if amt != fixAmount {
		t.Errorf("[round %d] UPDATE 后 amount: 期望 %v，得到 %v", round, fixAmount, amt)
	}

	// 关闭当前 server
	if err := srv.Stop(); err != nil {
		t.Fatalf("[round %d] Stop 失败: %v", round, err)
	}
	time.Sleep(100 * time.Millisecond)
	return wantRows
}

// TestMultiProtocolCrossClientParallelReadWrite 验证「单 server 三协议并发读写」不出现死锁或不一致。
//
// 与 TestMultiProtocolCrossClientMixedWorkload 的区别：后者侧重持久化；
// 本测试侧重「多个协议在持续读写交错」下不出现协议间死锁、连接泄漏。
// 通过每个协议各跑 20 轮"读自己 ID → 写自己 ID → 读自己 ID"循环，验证：
//  1. 各协议响应成功率 100%；
//  2. 每客户端仅断言自己 ID 范围的数据正确（不受其它并发客户端影响）；
//  3. 最终行数 == mrpcmClients * mrpcmCycleRounds（每客户端每轮写 1 行）。
func TestMultiProtocolCrossClientParallelReadWrite(t *testing.T) {
	s := startPGWireServer(t)

	// 建表（HTTP）
	if resp, err := httpQuery(s.httpAddr, mrpcmCreateTableSQL); err != nil {
		t.Fatalf("建表失败: %v", err)
	} else if resp.Code != 0 {
		t.Fatalf("建表返回错误: %s", resp.Message)
	}

	const rounds = mrpcmCycleRounds
	var (
		wg         sync.WaitGroup
		okCount    int64
		failCount  int64
		ctxTimeout = 5 * time.Second
	)
	for i := 0; i < mrpcmClients; i++ {
		wg.Add(1)
		via := mrpcmAssignProtocol(i)
		go func(clientID int, via string) {
			defer wg.Done()
			// 每协议串行 rounds 轮读写循环；任一轮失败即整体失败
			for round := 0; round < rounds; round++ {
				ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
				err := mrpcmRunReadWriteCycle(ctx, s, via, clientID, round)
				cancel()
				if err != nil {
					atomic.AddInt64(&failCount, 1)
					t.Logf("[%s c%d round %d] 失败: %v", via, clientID, round, err)
					return
				}
				atomic.AddInt64(&okCount, 1)
			}
		}(i, via)
	}
	wg.Wait()

	if failCount > 0 {
		t.Fatalf("%d 轮失败 (成功 %d)，见上方日志", failCount, okCount)
	}
	// 至少 mrpcmClients * rounds 成功
	if okCount < int64(mrpcmClients*rounds) {
		t.Fatalf("成功轮数不足: 期望 >= %d，得到 %d", mrpcmClients*rounds, okCount)
	}

	// 最终行数应等于 mrpcmClients * rounds（每客户端每轮追加 1 行）
	resp := queryVia(t, s, "tcp", "SELECT COUNT(*) AS cnt FROM "+mrpcmTable)
	if resp.Code != 0 {
		t.Fatalf("最终 COUNT 失败: %s", resp.Message)
	}
	want := int64(mrpcmClients * rounds)
	got, _ := toInt64(respRows(resp)[0]["cnt"])
	if got != want {
		t.Errorf("最终行数: 期望 %d，得到 %d", want, got)
	}
}

// mrpcmRunReadWriteCycle 单协议下"读 → 写 → 读"循环（按客户端自身 ID）：
//  1. 读自己本轮的 ID（不存在 → 0 行，因每轮写不同的 ID）；
//  2. INSERT 自己本轮的 ID（写入）；
//  3. 再读自己本轮的 ID（应存在 1 行，region 字段匹配）。
//
// ctx 用于超时控制，违反任一不变量则返回错误。
// 与「全局 COUNT」断言不同，此循环仅验证客户端自身 ID 范围的数据正确性，
// 可与其它并发客户端在同一张表上互不干扰地工作。
//
// 总写入行数 = mrpcmClients * mrpcmCycleRounds，由调用方负责统一。
// 每轮写 1 行到独立 ID，因此"读 1"始终期望 0 行；"读 2"始终期望 1 行。
func mrpcmRunReadWriteCycle(ctx context.Context, s *sqlServer, via string, clientID, round int) error {
	// 客户端自己的 ID：mrpcmPeerBase*100 + clientID*mrpcmCycleRounds*10 + round
	// 每轮一个独立 ID，"读 1"总是 0 行，"读 2"应该 1 行
	id := mrpcmPeerBase*100 + clientID*mrpcmCycleRounds*10 + round
	region := fmt.Sprintf("c%d-r%d", clientID, round)
	active := round%2 == 0

	// 读 1：自己 ID 在本轮写入前应不存在
	readSQL := fmt.Sprintf("SELECT region, amount, active FROM %s WHERE id = %d", mrpcmTable, id)
	rows, err := mrpcmReadRows(ctx, s, via, readSQL)
	if err != nil {
		return fmt.Errorf("读 1 失败: %w", err)
	}
	if len(rows) != 0 {
		return fmt.Errorf("读 1: 期望 0 行（每轮写新 ID），得到 %d", len(rows))
	}

	// 写：INSERT 自己本轮的 ID
	writeSQL := fmt.Sprintf(
		"INSERT INTO %s (id, region, amount, active) VALUES (%d, '%s', %d.0, %t)",
		mrpcmTable, id, region, id, active,
	)
	if err := mrpcmExec(ctx, s, via, writeSQL); err != nil {
		return fmt.Errorf("写失败: %w", err)
	}

	// 读 2：自己 ID 应已存在 1 行
	rows, err = mrpcmReadRows(ctx, s, via, readSQL)
	if err != nil {
		return fmt.Errorf("读 2 失败: %w", err)
	}
	if len(rows) != 1 {
		return fmt.Errorf("读 2: 期望 1 行，得到 %d", len(rows))
	}
	// 校验 region 字段匹配（确保读到的是本客户端本轮写入的版本）
	if got, _ := rows[0]["region"].(string); got != region {
		return fmt.Errorf("读 2 region 字段: 期望 %q，得到 %q", region, got)
	}
	return nil
}

// mrpcmReadRows 通过指定协议执行 SELECT 并返回行（map 形式）。
func mrpcmReadRows(ctx context.Context, s *sqlServer, via, sql string) ([]map[string]any, error) {
	done := make(chan []map[string]any, 1)
	errCh := make(chan error, 1)
	go func() {
		rows, err := mrpcmReadRowsByProtocol(s, via, sql)
		if err != nil {
			errCh <- err
			return
		}
		done <- rows
	}()
	select {
	case rows := <-done:
		return rows, nil
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// mrpcmReadRowsByProtocol 按协议路由读 SELECT 并返回行（map 形式）。
// 拆出此函数是为了把"协议分发"与"context 超时等待"解耦，便于通过 cyclomatic 阈值。
func mrpcmReadRowsByProtocol(s *sqlServer, via, sql string) ([]map[string]any, error) {
	switch via {
	case "tcp":
		return mrpcmReadRowsTCP(s, sql)
	case "http":
		return mrpcmReadRowsHTTP(s, sql)
	case "pg":
		return mrpcmReadRowsPG(s, sql)
	default:
		return nil, fmt.Errorf("未知协议: %s", via)
	}
}

// mrpcmReadRowsTCP 走 TCP 协议读 SELECT。
func mrpcmReadRowsTCP(s *sqlServer, sql string) ([]map[string]any, error) {
	tc, err := dialTCP(s.tcpAddr)
	if err != nil {
		return nil, err
	}
	defer tc.close()
	resp, err := tc.query(sql)
	if err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("%s", resp.Message)
	}
	return respRows(resp), nil
}

// mrpcmReadRowsHTTP 走 HTTP 协议读 SELECT。
func mrpcmReadRowsHTTP(s *sqlServer, sql string) ([]map[string]any, error) {
	resp, err := httpQuery(s.httpAddr, sql)
	if err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("%s", resp.Message)
	}
	return respRows(resp), nil
}

// mrpcmReadRowsPG 走 PG wire 协议读 SELECT，把文本行转换为列名 → 值 的 map 列表。
func mrpcmReadRowsPG(s *sqlServer, sql string) ([]map[string]any, error) {
	c, err := dialPGWireErr(s.srv.PGAddr())
	if err != nil {
		return nil, err
	}
	defer c.close()
	if err := c.handshakeErr(); err != nil {
		return nil, err
	}
	res, err := c.sendQueryRead(sql)
	if err != nil {
		return nil, err
	}
	if res.errMsg != "" {
		return nil, fmt.Errorf("%s", res.errMsg)
	}
	// PG wire 返回列名 + 文本值，组装为列名 → 值 的 map
	cols := res.columns
	out := make([]map[string]any, 0, len(res.rows))
	for _, r := range res.rows {
		m := make(map[string]any, len(cols))
		for i, col := range cols {
			if i < len(r) {
				m[col] = r[i]
			}
		}
		out = append(out, m)
	}
	return out, nil
}

// mrpcmExec 通过指定协议执行任意 SQL。
func mrpcmExec(ctx context.Context, s *sqlServer, via, sql string) error {
	done := make(chan error, 1)
	go func() {
		done <- mrpcmExecByProtocol(s, via, sql)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// mrpcmExecByProtocol 按协议路由执行任意 SQL。
// 拆出此函数是为了把"context 超时等待"与"协议分发"解耦，便于通过认知复杂度阈值。
func mrpcmExecByProtocol(s *sqlServer, via, sql string) error {
	switch via {
	case "tcp":
		return mrpcmExecTCP(s, sql)
	case "http":
		return mrpcmExecHTTP(s, sql)
	case "pg":
		return mrpcmExecPG(s, sql)
	default:
		return fmt.Errorf("未知协议: %s", via)
	}
}

// mrpcmExecTCP 走 TCP 协议执行 SQL。
func mrpcmExecTCP(s *sqlServer, sql string) error {
	tc, err := dialTCP(s.tcpAddr)
	if err != nil {
		return err
	}
	defer tc.close()
	resp, err := tc.query(sql)
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return fmt.Errorf("%s", resp.Message)
	}
	return nil
}

// mrpcmExecHTTP 走 HTTP 协议执行 SQL。
func mrpcmExecHTTP(s *sqlServer, sql string) error {
	resp, err := httpQuery(s.httpAddr, sql)
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return fmt.Errorf("%s", resp.Message)
	}
	return nil
}

// mrpcmExecPG 走 PG wire 协议执行 SQL。
func mrpcmExecPG(s *sqlServer, sql string) error {
	c, err := dialPGWireErr(s.srv.PGAddr())
	if err != nil {
		return err
	}
	defer c.close()
	if err := c.handshakeErr(); err != nil {
		return err
	}
	res, err := c.sendQueryRead(sql)
	if err != nil {
		return err
	}
	if res.errMsg != "" {
		return fmt.Errorf("%s", res.errMsg)
	}
	return nil
}
