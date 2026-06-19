// Package integration 端到端集成测试：整数族类型 + DATE + TIMESTAMP 经多客户端并发验证。
//
// 既有集成测试仅覆盖 INT64/FLOAT64/STRING/BOOL 四种类型，整数族窄类型
// （INT8/INT16/INT32/UINT64）与 DATE/TIMESTAMP 仅在单元测试中出现，
// 未经过「真实 server + 多客户端并发 + SQL 查询」的端到端验证。
//
// 本文件补充该缺口：
//   - 启动一个 server，创建含全部整数族 + DATE + TIMESTAMP 的表
//   - 多个客户端（TCP/HTTP 混合）并发执行写入、点查、范围扫描、聚合查询
//   - 验证跨协议（TCP vs HTTP）查询结果一致性
//   - 验证 SQL INSERT 对整数族类型的 round-trip 正确性
package integration

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// typed 测试常量。
const (
	typedTable         = "typed_events"
	typedNumClients    = 10 // 并发客户端总数
	typedRowsPerWriter = 3  // 每个写入客户端写入的行数
	typedWriterBaseID  = 1000
)

// typedCreateTable 经 SQL 创建含全部整数族 + DATE + TIMESTAMP 的表。
// 列类型映射：BIGINT→INT64、SMALLINT→INT16、MEDIUMINT→INT32、
// TINYINT UNSIGNED→INT8、BIGINT UNSIGNED→UINT64、DATE→DATE、TIMESTAMP→TIMESTAMP。
func typedCreateTable(t *testing.T, s *sqlServer) {
	t.Helper()
	execSQLVia(t, s, "tcp",
		"CREATE TABLE "+typedTable+" (id BIGINT NOT NULL, "+
			"small SMALLINT NULL, medium MEDIUMINT NULL, "+
			"tiny TINYINT UNSIGNED NULL, bigu BIGINT UNSIGNED NULL, "+
			"d DATE NULL, ts TIMESTAMP NULL, "+
			"score FLOAT64 NULL, name STRING NULL, "+
			"PRIMARY KEY(id))")
}

// typedSeedRows 返回初始数据（经 /write 写入，保证 DATE/TIMESTAMP 类型正确）。
// /write 路径的 interfaceToValue 会将 "YYYY-MM-DD" 字符串转为 DATE、
// RFC3339 字符串转为 TIMESTAMP；SQL INSERT 路径当前不支持该转换。
func typedSeedRows() []map[string]any {
	return []map[string]any{
		{"id": 1, "small": 10, "medium": 1000, "tiny": 200, "bigu": 50000,
			"d": "2024-01-15", "ts": "2024-01-15T08:00:00Z", "score": 9.5, "name": "alpha"},
		{"id": 2, "small": 20, "medium": 2000, "tiny": 100, "bigu": 60000,
			"d": "2024-02-20", "ts": "2024-02-20T12:30:00Z", "score": 7.25, "name": "beta"},
		{"id": 3, "small": 30, "medium": 3000, "tiny": 250, "bigu": 70000,
			"d": "2024-03-10", "ts": "2024-03-10T18:45:00Z", "score": 8.0, "name": "gamma"},
		{"id": 4, "small": 15, "medium": 1500, "tiny": 50, "bigu": 80000,
			"d": "2024-04-05", "ts": "2024-04-05T06:15:00Z", "score": 6.5, "name": "delta"},
		{"id": 5, "small": 25, "medium": 2500, "tiny": 150, "bigu": 90000,
			"d": "2024-05-12", "ts": "2024-05-12T20:00:00Z", "score": 8.75, "name": "epsilon"},
	}
}

// typedSeedData 经 /write 写入初始数据。
func typedSeedData(t *testing.T, s *sqlServer) {
	t.Helper()
	writeVia(t, s, "tcp", typedTable, typedSeedRows())
}

// typedWriterWork 写入客户端：向表写入唯一 ID 的行（经 /write 保证类型正确）。
// 列值刻意避开 reader 查询条件（small<20、medium<=1500、tiny>150、bigu!=70000），
// 使 reader 的精确计数查询不受并发写入影响。
func typedWriterWork(s *sqlServer, via string, clientID int) error {
	rows := make([]map[string]any, typedRowsPerWriter)
	for i := 0; i < typedRowsPerWriter; i++ {
		id := typedWriterBaseID + clientID*typedRowsPerWriter + i
		rows[i] = map[string]any{
			"id":     id,
			"small":  int16(clientID % 10),
			"medium": int32(clientID * 10),
			"tiny":   uint8(201 + clientID%50),
			"bigu":   uint64(clientID),
			"d":      fmt.Sprintf("2024-06-%02d", (id%28)+1),
			"ts":     fmt.Sprintf("2024-06-%02dT10:00:00Z", (id%28)+1),
			"score":  float64(id) * 0.1,
			"name":   fmt.Sprintf("cli-%d", clientID),
		}
	}
	resp, err := rawWrite(s, via, typedTable, rows)
	if err != nil {
		return fmt.Errorf("写入失败: %w", err)
	}
	if resp.Code != 0 {
		return fmt.Errorf("写入返回错误: %s", resp.Message)
	}
	return nil
}

// typedPointReaderWork 点查客户端：对整数族列做 WHERE 等值查询。
// 整数族跨类型比较（INT64 字面量 vs INT8/16/32/UINT64 列）应正确命中。
// writer 列值刻意避开这些查询值，故精确计数不受并发写入影响。
func typedPointReaderWork(s *sqlServer, via string, _ int) error {
	cases := []struct {
		sql  string
		want int
	}{
		{"SELECT * FROM " + typedTable + " WHERE id = 3", 1},
		{"SELECT * FROM " + typedTable + " WHERE small = 20", 1},
		{"SELECT * FROM " + typedTable + " WHERE tiny = 200", 1},
		{"SELECT * FROM " + typedTable + " WHERE bigu = 70000", 1},
	}
	for _, tc := range cases {
		resp, err := rawQuery(s, via, tc.sql)
		if err != nil {
			return fmt.Errorf("点查失败 [%s]: %w", tc.sql, err)
		}
		if resp.Code != 0 {
			return fmt.Errorf("点查错误 [%s]: %s", tc.sql, resp.Message)
		}
		if got := len(respRows(resp)); got != tc.want {
			return fmt.Errorf("点查 [%s]: 期望 %d 行，得到 %d", tc.sql, tc.want, got)
		}
	}
	return nil
}

// typedRangeReaderWork 范围查询客户端：WHERE 范围 + 多列组合条件。
// writer 列值刻意避开（small<20、medium<=1500、tiny>150），故精确计数不受并发写入影响。
func typedRangeReaderWork(s *sqlServer, via string, _ int) error {
	// 范围查询：medium > 1500 应返回 id=2,3,5（medium=2000,3000,2500）
	resp, err := rawQuery(s, via,
		"SELECT id FROM "+typedTable+" WHERE medium > 1500")
	if err != nil {
		return fmt.Errorf("范围查询失败: %w", err)
	}
	if resp.Code != 0 {
		return fmt.Errorf("范围查询错误: %s", resp.Message)
	}
	rows := respRows(resp)
	if len(rows) != 3 {
		return fmt.Errorf("范围查询期望 3 行，得到 %d", len(rows))
	}
	// 组合条件：small >= 20 AND tiny <= 150 → id=2(small20,tiny100),5(small25,tiny150)
	resp2, err := rawQuery(s, via,
		"SELECT id FROM "+typedTable+" WHERE small >= 20 AND tiny <= 150")
	if err != nil {
		return fmt.Errorf("组合查询失败: %w", err)
	}
	if resp2.Code != 0 {
		return fmt.Errorf("组合查询错误: %s", resp2.Message)
	}
	if got := len(respRows(resp2)); got != 2 {
		return fmt.Errorf("组合查询期望 2 行，得到 %d", got)
	}
	return nil
}

// typedAggReaderWork 聚合客户端：COUNT/SUM/MIN/MAX/AVG + GROUP BY。
func typedAggReaderWork(s *sqlServer, via string, _ int) error {
	// COUNT + SUM + MIN + MAX + AVG on 整数族列
	resp, err := rawQuery(s, via,
		"SELECT COUNT(*) AS cnt, SUM(small) AS s, MIN(medium) AS mn, "+
			"MAX(bigu) AS mx, AVG(score) AS av FROM "+typedTable)
	if err != nil {
		return fmt.Errorf("聚合查询失败: %w", err)
	}
	if resp.Code != 0 {
		return fmt.Errorf("聚合查询错误: %s", resp.Message)
	}
	rows := respRows(resp)
	if len(rows) != 1 {
		return fmt.Errorf("聚合期望 1 行，得到 %d", len(rows))
	}
	cnt, ok := toInt64(rows[0]["cnt"])
	if !ok || cnt < 5 {
		return fmt.Errorf("COUNT 期望 >=5，得到 %v", rows[0]["cnt"])
	}
	// GROUP BY name 聚合
	gresp, err := rawQuery(s, via,
		"SELECT name, COUNT(*) AS cnt FROM "+typedTable+" GROUP BY name")
	if err != nil {
		return fmt.Errorf("GROUP BY 失败: %w", err)
	}
	if gresp.Code != 0 {
		return fmt.Errorf("GROUP BY 错误: %s", gresp.Message)
	}
	if len(respRows(gresp)) < 5 {
		return fmt.Errorf("GROUP BY 期望 >=5 组，得到 %d", len(respRows(gresp)))
	}
	return nil
}

// typedRunClient 按角色分发客户端工作负载。
func typedRunClient(s *sqlServer, via string, clientID, role int) error {
	switch role {
	case 0:
		return typedWriterWork(s, via, clientID)
	case 1:
		return typedPointReaderWork(s, via, clientID)
	case 2:
		return typedRangeReaderWork(s, via, clientID)
	default:
		return typedAggReaderWork(s, via, clientID)
	}
}

// TestMultiClientTypedSQL 验证多客户端并发操作整数族+DATE+TIMESTAMP 表的正确性。
//
// 启动一个 server，创建含全部整数族 + DATE + TIMESTAMP 的表，10 个客户端
// （TCP/HTTP 混合）并发执行写入、点查、范围扫描、聚合查询，最终校验数据完整性。
func TestMultiClientTypedSQL(t *testing.T) {
	s := startSQLServer(t)
	typedCreateTable(t, s)
	typedSeedData(t, s)

	var wg sync.WaitGroup
	var failCount int64
	var lastErr atomic.Value

	for i := 0; i < typedNumClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			via := "tcp"
			if clientID%3 == 0 {
				via = "http"
			}
			role := clientID % 4
			if err := typedRunClient(s, via, clientID, role); err != nil {
				t.Logf("client %d (%s, role %d) 失败: %v", clientID, via, role, err)
				lastErr.Store(err.Error())
				atomic.AddInt64(&failCount, 1)
			}
		}(i)
	}
	wg.Wait()

	if failCount > 0 {
		t.Fatalf("%d 个客户端失败，最后错误: %v", failCount, lastErr.Load())
	}
	typedVerifyFinalState(t, s)
}

// typedVerifyFinalState 校验全部客户端完成后的数据完整性。
func typedVerifyFinalState(t *testing.T, s *sqlServer) {
	t.Helper()
	// 10 个客户端按 clientID%4 分配角色：role 0=writer 出现 3 次（clientID 0,4,8）
	numWriters := 3
	wantTotal := int64(5 + numWriters*typedRowsPerWriter)
	resp := queryVia(t, s, "tcp", "SELECT COUNT(*) AS cnt FROM "+typedTable)
	if resp.Code != 0 {
		t.Fatalf("最终 COUNT 失败: %s", resp.Message)
	}
	rows := respRows(resp)
	got, _ := toInt64(rows[0]["cnt"])
	if got != wantTotal {
		t.Errorf("总行数: 期望 %d，得到 %d", wantTotal, got)
	}
	// 验证写入的行可查
	wantWritten := numWriters * typedRowsPerWriter
	wresp := queryVia(t, s, "tcp",
		"SELECT * FROM "+typedTable+" WHERE id >= 1000 LIMIT 100")
	if wresp.Code != 0 {
		t.Fatalf("查询写入行失败: %s", wresp.Message)
	}
	if len(respRows(wresp)) != wantWritten {
		t.Errorf("写入行数: 期望 %d，得到 %d", wantWritten, len(respRows(wresp)))
	}
	// 验证 DATE 列经 /write 写入后可正确读回（非 NULL）
	dresp := queryVia(t, s, "tcp",
		"SELECT d FROM "+typedTable+" WHERE id = 1")
	if dresp.Code != 0 {
		t.Fatalf("查询 DATE 失败: %s", dresp.Message)
	}
	drows := respRows(dresp)
	if len(drows) != 1 {
		t.Fatalf("DATE 查询期望 1 行，得到 %d", len(drows))
	}
	if drows[0]["d"] == nil {
		t.Errorf("id=1 的 DATE 列不应为 NULL")
	}
}

// TestTypedSQLInsertRoundTrip 验证 SQL INSERT 对整数族类型的 round-trip 正确性。
//
// SQL INSERT 的整数字面量被解析为 INT64，存储时 coerceValueByType 不做窄类型转换，
// 但 SELECT 读取时 fillColumnValues→coerceValue 会将 INT64 强制为目标整数族类型，
// 保证 round-trip 正确。DATE/TIMESTAMP 经 SQL INSERT 当前不可用（见 probe），
// 本测试仅覆盖整数族。
func TestTypedSQLInsertRoundTrip(t *testing.T) {
	s := startSQLServer(t)
	execSQLVia(t, s, "tcp",
		"CREATE TABLE ins_typed (id BIGINT NOT NULL, "+
			"small SMALLINT NULL, medium MEDIUMINT NULL, "+
			"tiny TINYINT UNSIGNED NULL, bigu BIGINT UNSIGNED NULL, "+
			"PRIMARY KEY(id))")
	execSQLVia(t, s, "tcp",
		"INSERT INTO ins_typed (id, small, medium, tiny, bigu) VALUES "+
			"(1, 7, 100, 200, 99999), (2, 14, 200, 100, 88888)")

	cases := []struct {
		sql   string
		field string
		want  int64
	}{
		{"SELECT small FROM ins_typed WHERE id = 1", "small", 7},
		{"SELECT medium FROM ins_typed WHERE id = 1", "medium", 100},
		{"SELECT tiny FROM ins_typed WHERE id = 1", "tiny", 200},
		{"SELECT bigu FROM ins_typed WHERE id = 1", "bigu", 99999},
		{"SELECT COUNT(*) AS c FROM ins_typed WHERE small >= 10", "c", 1},
		{"SELECT SUM(medium) AS s FROM ins_typed", "s", 300},
	}
	for _, tc := range cases {
		resp := queryVia(t, s, "tcp", tc.sql)
		if resp.Code != 0 {
			t.Fatalf("[%s] 查询失败: %s", tc.sql, resp.Message)
		}
		rows := respRows(resp)
		if len(rows) != 1 {
			t.Fatalf("[%s] 期望 1 行，得到 %d", tc.sql, len(rows))
		}
		got, ok := toInt64(rows[0][tc.field])
		if !ok {
			t.Fatalf("[%s] 字段 %s 缺失或类型异常: %v", tc.sql, tc.field, rows[0])
		}
		if got != tc.want {
			t.Errorf("[%s] 期望 %d，得到 %d", tc.sql, tc.want, got)
		}
	}
}

// TestTypedCrossProtocolConsistency 验证 TCP 与 HTTP 查询结果一致。
func TestTypedCrossProtocolConsistency(t *testing.T) {
	s := startSQLServer(t)
	typedCreateTable(t, s)
	typedSeedData(t, s)

	queries := []string{
		"SELECT * FROM " + typedTable + " WHERE id = 2",
		"SELECT id, small, bigu FROM " + typedTable + " WHERE medium >= 2000",
		"SELECT COUNT(*) AS cnt, SUM(score) AS s FROM " + typedTable,
		"SELECT name, AVG(score) AS av FROM " + typedTable + " GROUP BY name",
	}
	for _, sql := range queries {
		tcpResp := queryVia(t, s, "tcp", sql)
		httpResp := queryVia(t, s, "http", sql)
		if tcpResp.Code != 0 {
			t.Fatalf("[%s] TCP 查询失败: %s", sql, tcpResp.Message)
		}
		if httpResp.Code != 0 {
			t.Fatalf("[%s] HTTP 查询失败: %s", sql, httpResp.Message)
		}
		tcpRows := respRows(tcpResp)
		httpRows := respRows(httpResp)
		if len(tcpRows) != len(httpRows) {
			t.Errorf("[%s] 行数不一致: TCP=%d HTTP=%d", sql, len(tcpRows), len(httpRows))
			continue
		}
		typedAssertRowsEqual(t, sql, tcpRows, httpRows)
	}
}

// typedAssertRowsEqual 断言两份查询结果的关键数值字段一致。
// DATE/TIMESTAMP 经 /write 写入后以字符串形式返回，跨协议应相同。
func typedAssertRowsEqual(t *testing.T, sql string, a, b []map[string]any) {
	t.Helper()
	for i := 0; i < len(a); i++ {
		// 按 id 对齐（若存在）
		var aID, bID int64
		if v, ok := a[i]["id"]; ok {
			aID, _ = toInt64(v)
			bID, _ = toInt64(b[i]["id"])
			if aID != bID {
				t.Errorf("[%s] 第 %d 行 id 不一致: TCP=%d HTTP=%d", sql, i, aID, bID)
			}
		}
		// 比较所有键的值（数值按 float64 归一比较）
		for k, av := range a[i] {
			bv, ok := b[i][k]
			if !ok {
				t.Errorf("[%s] 第 %d 行缺少列 %s", sql, i, k)
				continue
			}
			if !typedValEqual(av, bv) {
				t.Errorf("[%s] 第 %d 行 %s 不一致: TCP=%v HTTP=%v", sql, i, k, av, bv)
			}
		}
	}
}

// typedValEqual 比较两个 any 值是否相等（数值按 float64 归一）。
func typedValEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	af, aok := toFloat64(a)
	bf, bok := toFloat64(b)
	if aok && bok {
		return af == bf
	}
	return a == b
}
