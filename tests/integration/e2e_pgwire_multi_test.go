package integration

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// 多客户端测试常量。
const (
	pgmcNumClients    = 10 // 并发 PG wire 客户端总数
	pgmcRowsPerClient = 6  // 每个客户端写入的行数
	pgmcIDBase        = 1000
)

// TestPGWireMultiClientConcurrent 验证多个 PG wire 客户端并发执行
// INSERT/SELECT/UPDATE/DELETE 的正确性。
//
// 启动一个 server，10 个客户端各自在互不冲突的 ID 区间内并发执行
// 写入、点查、更新、删除，最终校验数据总量与残留数据正确性。
func TestPGWireMultiClientConcurrent(t *testing.T) {
	s := startPGWireServer(t)
	c := dialPGWire(t, s.srv.PGAddr())
	defer c.close()
	c.handshake(t)
	c.execOK(t, "CREATE TABLE mc (id INT64 NOT NULL, name STRING NULL, "+
		"qty INT64 NULL, PRIMARY KEY(id))")

	var wg sync.WaitGroup
	var failCount int64
	var lastErr atomic.Value

	for i := 0; i < pgmcNumClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			if err := pgmcClientWork(s, clientID); err != nil {
				t.Logf("PG 客户端 %d 失败: %v", clientID, err)
				lastErr.Store(err.Error())
				atomic.AddInt64(&failCount, 1)
			}
		}(i)
	}
	wg.Wait()

	if failCount > 0 {
		t.Fatalf("%d 个 PG 客户端失败，最后错误: %v", failCount, lastErr.Load())
	}
	pgmcVerifyFinalState(t, s)
}

// pgmcClientWork 单个客户端工作负载：写入一批 → 点查 → 更新一行 → 删除一行。
func pgmcClientWork(s *sqlServer, clientID int) error {
	c, err := dialPGWireErr(s.srv.PGAddr())
	if err != nil {
		return fmt.Errorf("拨号失败: %w", err)
	}
	defer c.close()
	if err := c.handshakeErr(); err != nil {
		return err
	}
	if err := pgmcInsert(c, clientID); err != nil {
		return err
	}
	if err := pgmcPointRead(c, clientID); err != nil {
		return err
	}
	return pgmcUpdateAndDelete(c, clientID)
}

// pgmcInsert 写入 clientID 区间内的行。
func pgmcInsert(c *pgWireClient, clientID int) error {
	sql := pgmcInsertSQL(clientID)
	res, err := c.sendQueryRead(sql)
	if err != nil {
		return fmt.Errorf("写入失败: %w", err)
	}
	if res.errMsg != "" {
		return fmt.Errorf("写入错误: %s", res.errMsg)
	}
	return nil
}

// pgmcPointRead 点查首行并校验。
func pgmcPointRead(c *pgWireClient, clientID int) error {
	firstID := pgmcIDBase + clientID*pgmcRowsPerClient
	res, err := c.sendQueryRead(fmt.Sprintf("SELECT * FROM mc WHERE id = %d", firstID))
	if err != nil {
		return fmt.Errorf("点查失败: %w", err)
	}
	if res.errMsg != "" {
		return fmt.Errorf("点查错误: %s", res.errMsg)
	}
	if len(res.rows) != 1 {
		return fmt.Errorf("点查期望 1 行，得到 %d", len(res.rows))
	}
	return nil
}

// pgmcUpdateAndDelete 更新一行并删除另一行。
func pgmcUpdateAndDelete(c *pgWireClient, clientID int) error {
	base := pgmcIDBase + clientID*pgmcRowsPerClient
	upd, err := c.sendQueryRead(fmt.Sprintf("UPDATE mc SET qty = 999 WHERE id = %d", base))
	if err != nil {
		return fmt.Errorf("更新失败: %w", err)
	}
	if upd.errMsg != "" {
		return fmt.Errorf("更新错误: %s", upd.errMsg)
	}
	del, err := c.sendQueryRead(fmt.Sprintf("DELETE FROM mc WHERE id = %d", base+1))
	if err != nil {
		return fmt.Errorf("删除失败: %w", err)
	}
	if del.errMsg != "" {
		return fmt.Errorf("删除错误: %s", del.errMsg)
	}
	return nil
}

// pgmcInsertSQL 构造多行 INSERT 语句。
func pgmcInsertSQL(clientID int) string {
	base := pgmcIDBase + clientID*pgmcRowsPerClient
	sql := "INSERT INTO mc (id, name, qty) VALUES "
	for i := 0; i < pgmcRowsPerClient; i++ {
		if i > 0 {
			sql += ", "
		}
		id := base + i
		sql += fmt.Sprintf("(%d, 'c%d', %d)", id, clientID, id%100)
	}
	return sql
}

// pgmcVerifyFinalState 校验全部客户端完成后的数据完整性。
func pgmcVerifyFinalState(t *testing.T, s *sqlServer) {
	t.Helper()
	c := dialPGWire(t, s.srv.PGAddr())
	defer c.close()
	c.handshake(t)

	// 每个客户端写入 6 行，删除 1 行，残留 5 行；10 个客户端共 50 行。
	want := int64(pgmcNumClients * (pgmcRowsPerClient - 1))
	res := c.execOK(t, "SELECT COUNT(*) AS cnt FROM mc")
	if cnt, _ := pgInt(pgRowToMap(res.columns, res.rows[0])["cnt"]); cnt != want {
		t.Errorf("最终 COUNT 期望 %d，得到 %d", want, cnt)
	}

	// 校验更新生效：每个客户端首行 qty 应为 999。
	for i := 0; i < pgmcNumClients; i++ {
		id := pgmcIDBase + i*pgmcRowsPerClient
		r := c.execOK(t, fmt.Sprintf("SELECT qty FROM mc WHERE id = %d", id))
		if len(r.rows) != 1 {
			t.Errorf("id=%d 期望 1 行，得到 %d", id, len(r.rows))
			continue
		}
		if q, _ := pgInt(pgRowToMap(r.columns, r.rows[0])["qty"]); q != 999 {
			t.Errorf("id=%d qty 期望 999，得到 %d", id, q)
		}
	}

	// 校验删除生效：每个客户端第二行应不存在。
	for i := 0; i < pgmcNumClients; i++ {
		id := pgmcIDBase + i*pgmcRowsPerClient + 1
		r := c.execOK(t, fmt.Sprintf("SELECT id FROM mc WHERE id = %d", id))
		if len(r.rows) != 0 {
			t.Errorf("id=%d 应已被删除，但查到 %d 行", id, len(r.rows))
		}
	}
}

// TestPGWireCrossProtocolConsistency 验证 PG wire 与 TCP/HTTP 协议共享同一存储：
// PG wire 写入的数据可被 TCP/HTTP 读出，TCP 写入的数据可被 PG wire 读出。
func TestPGWireCrossProtocolConsistency(t *testing.T) {
	s := startPGWireServer(t)

	// 通过 PG wire 建表并写入
	pg := dialPGWire(t, s.srv.PGAddr())
	defer pg.close()
	pg.handshake(t)
	pg.execOK(t, "CREATE TABLE xproto (id INT64 NOT NULL, name STRING NULL, "+
		"PRIMARY KEY(id))")
	pg.execOK(t, "INSERT INTO xproto (id, name) VALUES (1, 'from-pg'), (2, 'from-pg')")

	// TCP 读取 PG 写入的数据
	tcpResp := queryVia(t, s, "tcp", "SELECT * FROM xproto")
	if tcpResp.Code != 0 {
		t.Fatalf("TCP 读取失败: %s", tcpResp.Message)
	}
	if len(respRows(tcpResp)) != 2 {
		t.Errorf("TCP 读取 PG 写入数据期望 2 行，得到 %d", len(respRows(tcpResp)))
	}

	// HTTP 读取 PG 写入的数据
	httpResp := queryVia(t, s, "http", "SELECT * FROM xproto WHERE id = 1")
	if httpResp.Code != 0 {
		t.Fatalf("HTTP 读取失败: %s", httpResp.Message)
	}
	hrows := respRows(httpResp)
	if len(hrows) != 1 || hrows[0]["name"] != "from-pg" {
		t.Errorf("HTTP 读取 PG 数据不匹配: %v", hrows)
	}

	// TCP 写入新数据
	writeVia(t, s, "tcp", "xproto", []map[string]any{
		{"id": 3, "name": "from-tcp"},
	})

	// PG wire 读取 TCP 写入的数据
	res := pg.execOK(t, "SELECT name FROM xproto WHERE id = 3")
	if len(res.rows) != 1 {
		t.Fatalf("PG 读取 TCP 写入数据期望 1 行，得到 %d", len(res.rows))
	}
	if got := pgRowToMap(res.columns, res.rows[0])["name"]; got != "from-tcp" {
		t.Errorf("PG 读取 TCP 数据期望 from-tcp，得到 %v", got)
	}

	// 三协议汇总 COUNT 一致
	pgCount := pg.execOK(t, "SELECT COUNT(*) AS cnt FROM xproto")
	pgCnt, _ := pgInt(pgRowToMap(pgCount.columns, pgCount.rows[0])["cnt"])
	tcpCount := queryVia(t, s, "tcp", "SELECT COUNT(*) AS cnt FROM xproto")
	tcpCnt, _ := toInt64(respRows(tcpCount)[0]["cnt"])
	if pgCnt != 3 || tcpCnt != 3 {
		t.Errorf("三协议 COUNT 不一致: pg=%d tcp=%d", pgCnt, tcpCnt)
	}
}
