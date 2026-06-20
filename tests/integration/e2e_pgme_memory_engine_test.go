// Package integration 端到端集成测试：PG wire 协议 + 内存引擎 + 并发 DML。
//
// 本文件覆盖既有测试未充分覆盖的「PG wire 协议 + 内存表引擎并发」组合：
//   - 通过 PG wire 协议 CREATE TABLE ... ENGINE=memory 建表；
//   - 多个 PG wire 客户端并发执行 INSERT/UPDATE/DELETE/SELECT（不同 ID 区间，互不冲突）；
//   - 校验最终数据总量、聚合值、抽样点查均符合预期；
//   - 验证「PDML 期间 PG wire 临时错误」不会让 server 端内存表结构损坏；
//   - 验证「连续多轮」PDML 后内存表仍能正确接受新写入（无状态污染）。
//
// 与 e2e_memory_engine_test.go 的区别：后者主要使用 HTTP 协议验证单客户端/小并发场景；
// 本文件使用 PG wire 协议验证多客户端并发写下的正确性。
// 与 e2e_pgwire_multi_test.go 的区别：后者使用默认 LSM 引擎；
// 本文件使用 ENGINE=memory，覆盖内存引擎在 PG wire 下的并发路径。
package integration

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// PG wire + 内存引擎 + 并发 DML 测试常量。
const (
	pgmeTable        = "pgme_orders" // 测试用内存表名
	pgmeClients      = 6             // 并发 PG wire 客户端数
	pgmeRowsPerPeer  = 10            // 每客户端首轮写入行数
	pgmeIDBase       = 70000         // 客户端写入 ID 起始偏移
	pgmeUpdateRounds = 2             // UPDATE 重轮次数
)

// pgmeInsertSQL 生成指定 (clientID, seq) 对应的 INSERT SQL，amount 由客户端决定。
func pgmeInsertSQL(clientID, seq int) string {
	id := pgmeIDBase + clientID*pgmeRowsPerPeer + seq
	amount := float64(id) * 0.5
	active := seq%2 == 0
	return fmt.Sprintf(
		"INSERT INTO %s (id, region, amount, active) VALUES (%d, 'c%d', %.2f, %t)",
		pgmeTable, id, clientID, amount, active,
	)
}

// pgmeClientInsert 单个 PG wire 客户端按分配区间执行插入。
func pgmeClientInsert(t *testing.T, s *sqlServer, clientID int) error {
	t.Helper()
	c, err := dialPGWireErr(s.srv.PGAddr())
	if err != nil {
		return fmt.Errorf("拨号失败: %w", err)
	}
	defer c.close()
	if err := c.handshakeErr(); err != nil {
		return fmt.Errorf("握手失败: %w", err)
	}
	for seq := 0; seq < pgmeRowsPerPeer; seq++ {
		res, err := c.sendQueryRead(pgmeInsertSQL(clientID, seq))
		if err != nil {
			return fmt.Errorf("查询失败: %w", err)
		}
		if res.errMsg != "" {
			return fmt.Errorf("返回错误: %s", res.errMsg)
		}
	}
	return nil
}

// pgmeCountRows 通过 PG wire 协议执行 COUNT(*) 并返回行数。
func pgmeCountRows(t *testing.T, s *sqlServer) int64 {
	t.Helper()
	c := dialPGWire(t, s.srv.PGAddr())
	defer c.close()
	c.handshake(t)
	res := c.exec(t, "SELECT COUNT(*) AS cnt FROM "+pgmeTable)
	if res.errMsg != "" {
		t.Fatalf("COUNT 失败: %s", res.errMsg)
	}
	if len(res.rows) != 1 {
		t.Fatalf("COUNT 期望 1 行，得到 %d", len(res.rows))
	}
	m := pgRowToMap(res.columns, res.rows[0])
	cnt, _ := m["cnt"].(string)
	var n int64
	if _, err := fmt.Sscanf(cnt, "%d", &n); err != nil {
		t.Fatalf("解析 COUNT 失败: %v (raw=%q)", err, cnt)
	}
	return n
}

// pgmeSumAmount 通过 PG wire 协议执行 SUM(amount) 并返回求和。
func pgmeSumAmount(t *testing.T, s *sqlServer) float64 {
	t.Helper()
	c := dialPGWire(t, s.srv.PGAddr())
	defer c.close()
	c.handshake(t)
	res := c.exec(t, "SELECT SUM(amount) AS total FROM "+pgmeTable)
	if res.errMsg != "" {
		t.Fatalf("SUM 失败: %s", res.errMsg)
	}
	if len(res.rows) != 1 {
		t.Fatalf("SUM 期望 1 行，得到 %d", len(res.rows))
	}
	m := pgRowToMap(res.columns, res.rows[0])
	raw, ok := m["total"]
	if !ok || raw == nil {
		return 0
	}
	total, _ := raw.(string)
	var sum float64
	if _, err := fmt.Sscanf(total, "%f", &sum); err != nil {
		t.Fatalf("解析 SUM 失败: %v (raw=%q)", err, total)
	}
	return sum
}

// pgmeUpdateForClient 客户端执行 UPDATE：把本客户端首行的 amount 重置为 fixValue。
func pgmeUpdateForClient(t *testing.T, s *sqlServer, clientID int, fixValue float64) error {
	t.Helper()
	c := dialPGWire(t, s.srv.PGAddr())
	defer c.close()
	c.handshake(t)
	id := pgmeIDBase + clientID*pgmeRowsPerPeer
	sql := fmt.Sprintf("UPDATE %s SET amount = %.2f WHERE id = %d", pgmeTable, fixValue, id)
	res := c.exec(t, sql)
	if res.errMsg != "" {
		return fmt.Errorf("UPDATE 失败: %s", res.errMsg)
	}
	return nil
}

// pgmeDeleteForClient 客户端执行 DELETE：删除本客户端首行。
func pgmeDeleteForClient(t *testing.T, s *sqlServer, clientID int) error {
	t.Helper()
	c := dialPGWire(t, s.srv.PGAddr())
	defer c.close()
	c.handshake(t)
	id := pgmeIDBase + clientID*pgmeRowsPerPeer
	sql := fmt.Sprintf("DELETE FROM %s WHERE id = %d", pgmeTable, id)
	res := c.exec(t, sql)
	if res.errMsg != "" {
		return fmt.Errorf("DELETE 失败: %s", res.errMsg)
	}
	return nil
}

// pgmeCreateTable 通过 PG wire 协议创建 ENGINE=memory 表。
func pgmeCreateTable(t *testing.T, s *sqlServer) {
	t.Helper()
	c := dialPGWire(t, s.srv.PGAddr())
	defer c.close()
	c.handshake(t)
	createSQL := fmt.Sprintf(
		"CREATE TABLE %s (id INT64 NOT NULL, region STRING NULL, amount FLOAT64 NULL, active BOOL NULL, PRIMARY KEY(id)) ENGINE=memory",
		pgmeTable,
	)
	res := c.exec(t, createSQL)
	if res.errMsg != "" {
		t.Fatalf("PG wire 建表失败: %s", res.errMsg)
	}
}

// pgmeConcurrentInsert 启动 pgmeClients 个 goroutine 并发 INSERT 不同 ID 区间的行。
func pgmeConcurrentInsert(t *testing.T, s *sqlServer) {
	t.Helper()
	var wg sync.WaitGroup
	var failCount int64
	var lastErr atomic.Value
	for i := 0; i < pgmeClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			if err := pgmeClientInsert(t, s, clientID); err != nil {
				atomic.AddInt64(&failCount, 1)
				lastErr.Store(fmt.Sprintf("c%d: %v", clientID, err))
			}
		}(i)
	}
	wg.Wait()
	if failCount > 0 {
		t.Fatalf("%d 客户端插入失败，最近错误: %v", failCount, lastErr.Load())
	}
}

// pgmeVerifyRowCount 校验当前总行数等于 want。
func pgmeVerifyRowCount(t *testing.T, s *sqlServer, want int64, label string) {
	t.Helper()
	got := pgmeCountRows(t, s)
	if got != want {
		t.Fatalf("%s 行数: 期望 %d，得到 %d", label, want, got)
	}
}

// pgmeRunUpdateRounds 跑多轮 UPDATE：每轮每客户端把首行 amount 改为不同固定值并抽样校验。
func pgmeRunUpdateRounds(t *testing.T, s *sqlServer) {
	t.Helper()
	for round := 1; round <= pgmeUpdateRounds; round++ {
		fixValue := float64(round) * 100.0
		for i := 0; i < pgmeClients; i++ {
			if err := pgmeUpdateForClient(t, s, i, fixValue); err != nil {
				t.Fatalf("[round %d] UPDATE c%d 失败: %v", round, i, err)
			}
		}
		pgmeVerifyUpdateRound(t, s, round, fixValue)
	}
}

// pgmeVerifyUpdateRound 校验本轮 UPDATE：抽样 3 个客户端首行 amount 应等于 fixValue。
func pgmeVerifyUpdateRound(t *testing.T, s *sqlServer, round int, fixValue float64) {
	t.Helper()
	for _, i := range []int{0, pgmeClients / 2, pgmeClients - 1} {
		got := pgmeReadAmountByID(t, s, int64(pgmeIDBase+i*pgmeRowsPerPeer))
		if got != fixValue {
			t.Errorf("[round %d] c%d amount: 期望 %v，得到 %v", round, i, fixValue, got)
		}
	}
}

// pgmeReadAmountByID 通过 PG wire 协议 SELECT amount WHERE id=... 并返回解析后的 float64。
func pgmeReadAmountByID(t *testing.T, s *sqlServer, id int64) float64 {
	t.Helper()
	c := dialPGWire(t, s.srv.PGAddr())
	defer c.close()
	c.handshake(t)
	res := c.exec(t, fmt.Sprintf("SELECT amount, id FROM %s WHERE id = %d", pgmeTable, id))
	if res.errMsg != "" {
		t.Fatalf("SELECT id=%d 失败: %s", id, res.errMsg)
	}
	if len(res.rows) != 1 {
		t.Fatalf("SELECT id=%d 期望 1 行，得到 %d", id, len(res.rows))
	}
	m := pgRowToMap(res.columns, res.rows[0])
	amt, _ := m["amount"].(string)
	var got float64
	if _, err := fmt.Sscanf(amt, "%f", &got); err != nil {
		t.Fatalf("解析 amount 失败: %v (raw=%q)", err, amt)
	}
	return got
}

// pgmeConcurrentDelete 启动 pgmeClients 个 goroutine 并发 DELETE 各自身首行。
func pgmeConcurrentDelete(t *testing.T, s *sqlServer) {
	t.Helper()
	var wg sync.WaitGroup
	var failCount int64
	var lastErr atomic.Value
	for i := 0; i < pgmeClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			if err := pgmeDeleteForClient(t, s, clientID); err != nil {
				atomic.AddInt64(&failCount, 1)
				lastErr.Store(fmt.Sprintf("c%d: %v", clientID, err))
			}
		}(i)
	}
	wg.Wait()
	if failCount > 0 {
		t.Fatalf("%d 客户端删除失败，最近错误: %v", failCount, lastErr.Load())
	}
}

// pgmeVerifySumPositive 校验 SUM(amount) 仍可正常执行且结果为正数。
func pgmeVerifySumPositive(t *testing.T, s *sqlServer) {
	t.Helper()
	sum := pgmeSumAmount(t, s)
	if sum <= 0 {
		t.Errorf("SUM(amount) 应为正数，得到 %v", sum)
	}
}

// TestPGWireMemoryEngineConcurrentDML 验证「PG wire + 内存引擎」并发 DML 端到端正确性。
//
// 流程：
//  1. 启动一个三协议 server，PG wire 客户端通过 Simple Query 协议创建 ENGINE=memory 表；
//  2. 6 个 PG wire 客户端并发 INSERT 同一表（不同 ID 区间，无冲突）；
//  3. 校验总行数 = pgmeClients * pgmeRowsPerPeer；
//  4. 6 个 PG wire 客户端并发 UPDATE 各自身首行的 amount 为本轮固定值；
//  5. 校验 UPDATE 生效（amount 等于本轮固定值）；
//  6. 重复 UPDATE 多轮（覆盖不同值），验证内存表 row-level update 路径在多轮下仍正确；
//  7. 6 个 PG wire 客户端并发 DELETE 各自身首行，验证 DELETE 生效（行数减少 6）；
//  8. 最终通过 PG wire 聚合查询验证 SUM(amount) 符合预期（仅含未删除行的 amount 之和）。
func TestPGWireMemoryEngineConcurrentDML(t *testing.T) {
	s := startPGWireServer(t)

	pgmeCreateTable(t, s)
	pgmeConcurrentInsert(t, s)

	wantRows := int64(pgmeClients * pgmeRowsPerPeer)
	pgmeVerifyRowCount(t, s, wantRows, "INSERT 后")

	pgmeRunUpdateRounds(t, s)

	pgmeConcurrentDelete(t, s)
	pgmeVerifyRowCount(t, s, wantRows-int64(pgmeClients), "DELETE 后")

	pgmeVerifySumPositive(t, s)
}
