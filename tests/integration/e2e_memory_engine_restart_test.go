// Package integration 端到端集成测试：内存引擎 (ENGINE=memory) 的「重启后数据丢失」语义。
//
// 本文件专门覆盖一个关键的不变量：
//   - LSM 引擎表的数据会持久化到磁盘并经 WAL/Segment 在重启后完整恢复；
//   - 内存引擎表只保存于进程内存中，重启后数据全部丢失（设计目标）。
//
// 这一对不变量既要求「元数据可恢复」（catalog.json 仍记得有这张内存表），
// 又要求「数据不可恢复」（重启后表里查不到任何行），是 catalog 恢复路径 +
// 引擎路由路径必须同时满足的契约。既有 e2e_memory_engine_test.go 与
// e2e_concurrent_ddl_dml_test.go 都没覆盖「跨进程重启」场景。
//
// 测试使用临时目录中两个独立 server 进程（两个 *server.Server 实例）顺序
// 模拟「先启动 → 写数据 → Stop → 复用同一 DataDir 启动新 Server」：
//   - 第一个 server：建 LSM 表与内存表各一张，写入数据并 Stop；
//   - 第二个 server：恢复 catalog 后通过 SQL 校验可见性与数据。
//
// 断言矩阵：
//   - 内存表行数：0（数据丢失，DESCRIBE 仍可见表结构）
//   - LSM 表行数：N（数据完整恢复）
//   - SHOW TABLES：两张表都存在（catalog.json 持久化了 Engine 字段）
package integration

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

// restartMemoryTableName 是测试用的内存表名，集中放置便于排查日志。
const restartMemoryTableName = "restart_mem"

// restartLSMTableName 是测试用的 LSM 表名，集中放置便于排查日志。
const restartLSMTableName = "restart_lsm"

// restartLSMRows 是测试第一阶段写入到 LSM 表的行集合。
func restartLSMRows() []map[string]any {
	return []map[string]any{
		{"id": 1, "name": "lsm-a"},
		{"id": 2, "name": "lsm-b"},
		{"id": 3, "name": "lsm-c"},
	}
}

// restartMemoryRows 是测试第一阶段写入到内存表的行集合。
func restartMemoryRows() []map[string]any {
	return []map[string]any{
		{"id": 10, "name": "mem-a"},
		{"id": 11, "name": "mem-b"},
		{"id": 12, "name": "mem-c"},
		{"id": 13, "name": "mem-d"},
		{"id": 14, "name": "mem-e"},
	}
}

// startSQLServerWithDir 在指定的 dataDir 上启动一个 server 实例，便于「重启」场景复用同一目录。
// 返回的 *sqlServer 持有 server 句柄与监听地址；调用方负责在测试结束时调用 srv.Stop()。
//
// dataDir 应由调用方持有（推荐 t.TempDir()，在整测试结束后自动清理），本函数不再单独
// 做 RemoveAll，避免与调用方 Cleanup 顺序耦合。
func startSQLServerWithDir(t *testing.T, dataDir string) *sqlServer {
	t.Helper()
	cfg := server.Config{
		TCPAddr:  "127.0.0.1:0",
		HTTPAddr: "127.0.0.1:0",
		DataDir:  dataDir,
	}
	srv, err := server.NewServer(cfg, server.WithMetricsRegistry(prometheus.NewRegistry()))
	if err != nil {
		t.Fatalf("NewServer 失败: %v", err)
	}
	if err := srv.Start(); err != nil {
		// Start 失败时 Best-effort 释放已分配资源；t.Fatalf 会终止测试，
		// 此处 Stop 的错误即便发生也无法继续上报，忽略是合理的。
		_ = srv.Stop()
		t.Fatalf("Start 失败: %v", err)
	}
	return &sqlServer{srv: srv, tcpAddr: srv.TCPAddr(), httpAddr: srv.HTTPAddr()}
}

// stopSQLServer 关闭 server 并等待资源释放；与 t.Cleanup 注册的 srv.Stop() 不冲突。
func stopSQLServer(t *testing.T, s *sqlServer) {
	t.Helper()
	if s == nil || s.srv == nil {
		return
	}
	if err := s.srv.Stop(); err != nil {
		t.Logf("停止 server 警告: %v", err)
	}
}

// restartSeedData 在 server #1 上建 LSM 表与内存表并写入测试数据。
// 数据量与内容由 restartLSMRows / restartMemoryRows 给出。
func restartSeedData(t *testing.T, s *sqlServer) {
	t.Helper()
	// 建 LSM 表（默认引擎），写入 3 行。
	createLSM := queryVia(t, s, "tcp",
		"CREATE TABLE "+restartLSMTableName+" (id INT64 NOT NULL, name STRING NULL, PRIMARY KEY(id))")
	if createLSM.Code != 0 {
		t.Fatalf("建 LSM 表失败: %s", createLSM.Message)
	}
	writeVia(t, s, "tcp", restartLSMTableName, restartLSMRows())

	// 建内存表（ENGINE=memory），写入 5 行。
	createMem := queryVia(t, s, "tcp",
		"CREATE TABLE "+restartMemoryTableName+" (id INT64 NOT NULL, name STRING NULL, PRIMARY KEY(id)) ENGINE=memory")
	if createMem.Code != 0 {
		t.Fatalf("建内存表失败: %s", createMem.Message)
	}
	writeVia(t, s, "tcp", restartMemoryTableName, restartMemoryRows())
	// 不需要显式 FLUSH：每次写入在 engine 层已 Sync WAL，
	// Stop 会关闭所有引擎与 WAL，新启动时 replay 即可恢复。
}

// restartCollectTableNames 执行 SHOW TABLES 并返回表名集合，便于后续断言。
func restartCollectTableNames(t *testing.T, s *sqlServer) map[string]bool {
	t.Helper()
	resp := queryVia(t, s, "tcp", "SHOW TABLES")
	if resp.Code != 0 {
		t.Fatalf("SHOW TABLES 失败: %s", resp.Message)
	}
	names := make(map[string]bool)
	for _, r := range respRows(resp) {
		if name, ok := r["table"].(string); ok {
			names[name] = true
		}
	}
	return names
}

// restartAssertCount 断言指定表的 COUNT(*) 等于 want。
func restartAssertCount(t *testing.T, s *sqlServer, table string, want float64) {
	t.Helper()
	resp := queryVia(t, s, "tcp", "SELECT COUNT(*) AS cnt FROM "+table)
	if resp.Code != 0 {
		t.Fatalf("%s COUNT 失败: %s", table, resp.Message)
	}
	rows := respRows(resp)
	if len(rows) != 1 {
		t.Fatalf("%s COUNT 期望 1 行聚合结果，得到 %d", table, len(rows))
	}
	assertFloat(t, table, "cnt", rows[0]["cnt"], want)
}

// TestMemoryEngineRestartDropsData 验证内存引擎表在 server 重启后行数据全部丢失，
// 同时 LSM 表数据完整恢复，catalog 中两张表的元数据均可见。
//
// 流程：
//  1. 启动 server #1，建内存表 + LSM 表，各写入若干行；
//  2. Stop server #1（关闭监听、关闭所有引擎、刷盘 LSM 元数据到 catalog.json）；
//  3. 在同一 DataDir 启动 server #2，复用 catalog.json 与 LSM Segment/WAL；
//  4. 通过 SQL 校验：内存表 COUNT=0、LSM 表 COUNT>0、SHOW TABLES 包含两张表。
func TestMemoryEngineRestartDropsData(t *testing.T) {
	// 使用 t.TempDir()：在整测试（含 Cleanup）结束前一直存在，可被两个 server 阶段复用，
	// 测试结束后由 testing 框架自动清理；同时避免并发 CI job 或重复运行时的目录污染。
	dataDir := t.TempDir()

	// ---------- 阶段 1：写入阶段 ----------
	s1 := startSQLServerWithDir(t, dataDir)
	t.Cleanup(func() { stopSQLServer(t, s1) })
	restartSeedData(t, s1)
	stopSQLServer(t, s1)
	s1 = nil

	// ---------- 阶段 2：重启后校验 ----------
	s2 := startSQLServerWithDir(t, dataDir)
	t.Cleanup(func() { stopSQLServer(t, s2) })

	// 校验 1：catalog 恢复后 SHOW TABLES 仍能列出两张表（engine 字段随 catalog 一起持久化）。
	tableNames := restartCollectTableNames(t, s2)
	if !tableNames[restartLSMTableName] {
		t.Errorf("SHOW TABLES 应包含 LSM 表 %q，实际: %v", restartLSMTableName, tableNames)
	}
	if !tableNames[restartMemoryTableName] {
		t.Errorf("SHOW TABLES 应包含内存表 %q（catalog 持久化 engine 字段），实际: %v",
			restartMemoryTableName, tableNames)
	}

	// 校验 2：LSM 表数据完整恢复，COUNT=3。
	restartAssertCount(t, s2, restartLSMTableName, 3)

	// 校验 3：内存表行数据应全部丢失，COUNT=0（核心不变量）。
	restartAssertCount(t, s2, restartMemoryTableName, 0)

	// 校验 4：内存表行数 0 之外，再做一次 SELECT * 双重确认无残留行。
	allMem := queryVia(t, s2, "tcp", "SELECT * FROM "+restartMemoryTableName)
	if allMem.Code != 0 {
		t.Fatalf("内存表 SELECT 失败: %s", allMem.Message)
	}
	if got := len(respRows(allMem)); got != 0 {
		t.Errorf("内存表重启后应无残留行，得到 %d 行", got)
	}

	// 校验 5：内存表元数据（列、主键）仍可由 DESCRIBE 看到。
	descStmt := queryVia(t, s2, "tcp", "DESCRIBE "+restartMemoryTableName)
	if descStmt.Code != 0 {
		t.Fatalf("内存表 DESCRIBE 失败: %s", descStmt.Message)
	}
	if len(respRows(descStmt)) == 0 {
		t.Errorf("内存表 DESCRIBE 应至少返回 1 列，实际为空")
	}

	// 校验 6：内存表重启后仍可写入新数据（catalog → engine 路由正常恢复）。
	writeVia(t, s2, "tcp", restartMemoryTableName, []map[string]any{
		{"id": 100, "name": "mem-after-restart"},
	})
	afterWrite := queryVia(t, s2, "tcp",
		"SELECT * FROM "+restartMemoryTableName+" WHERE id = 100")
	if afterWrite.Code != 0 {
		t.Fatalf("重启后内存表写入校验失败: %s", afterWrite.Message)
	}
	rows := respRows(afterWrite)
	if len(rows) != 1 {
		t.Fatalf("重启后内存表写入期望 1 行，得到 %d", len(rows))
	}
	if name, _ := rows[0]["name"].(string); !strings.Contains(name, "mem-after-restart") {
		t.Errorf("重启后内存表写入内容异常: name=%v", rows[0]["name"])
	}
}

// TestMemoryEngineRestartAfterDropIsClean 验证先 DROP 内存表再启动新 server 后，
// catalog 不应残留该内存表（避免「幽灵表」导致后续 CREATE 失败）。
//
// 流程：
//  1. 启动 server，建内存表 → DROP 内存表；
//  2. Stop 后重启；
//  3. SHOW TABLES 不应列出已 DROP 的内存表，且同名 CREATE 应当成功。
func TestMemoryEngineRestartAfterDropIsClean(t *testing.T) {
	// 同 TestMemoryEngineRestartDropsData，使用 t.TempDir() 即可。
	dataDir := t.TempDir()

	// 阶段 1：建表 → DROP。
	s1 := startSQLServerWithDir(t, dataDir)
	t.Cleanup(func() { stopSQLServer(t, s1) })

	createResp := queryVia(t, s1, "tcp",
		"CREATE TABLE ghost_mem (id INT64 NOT NULL, PRIMARY KEY(id)) ENGINE=memory")
	if createResp.Code != 0 {
		t.Fatalf("建表失败: %s", createResp.Message)
	}
	dropResp := queryVia(t, s1, "tcp", "DROP TABLE ghost_mem")
	if dropResp.Code != 0 {
		t.Fatalf("DROP 失败: %s", dropResp.Message)
	}

	stopSQLServer(t, s1)
	s1 = nil

	// 阶段 2：重启后校验。
	s2 := startSQLServerWithDir(t, dataDir)
	t.Cleanup(func() { stopSQLServer(t, s2) })

	showResp := queryVia(t, s2, "tcp", "SHOW TABLES")
	if showResp.Code != 0 {
		t.Fatalf("SHOW TABLES 失败: %s", showResp.Message)
	}
	for _, r := range respRows(showResp) {
		if name, _ := r["table"].(string); name == "ghost_mem" {
			t.Errorf("DROP 后的内存表不应出现在 SHOW TABLES 中，实际: %v", r)
		}
	}

	// 同名重建：不应报「表已存在」。
	recreateResp := queryVia(t, s2, "tcp",
		"CREATE TABLE ghost_mem (id INT64 NOT NULL, PRIMARY KEY(id)) ENGINE=memory")
	if recreateResp.Code != 0 {
		t.Errorf("DROP 后同名重建失败: %s", recreateResp.Message)
	}
}
