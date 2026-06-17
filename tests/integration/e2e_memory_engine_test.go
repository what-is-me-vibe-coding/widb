// Package integration 端到端集成测试。
//
// 本文件验证内存表引擎（ENGINE=memory）的完整链路：
//   - 通过 SQL CREATE TABLE ... ENGINE=memory 建表
//   - 写入数据后通过 SELECT 查询验证
//   - 内存表与 LSM 表在同一 Server 中共存
//   - 多客户端并发读写内存表
package integration

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// memoryTableRows 返回内存表测试用的基础数据。
func memoryTableRows() []map[string]any {
	return []map[string]any{
		{"id": 1, "name": "alpha", "value": 10.5},
		{"id": 2, "name": "beta", "value": 20.0},
		{"id": 3, "name": "gamma", "value": 30.25},
	}
}

// createMemoryTableViaSQL 通过 SQL 创建内存引擎表。
func createMemoryTableViaSQL(t *testing.T, s *sqlServer, via string) {
	t.Helper()
	resp := queryVia(t, s, via,
		"CREATE TABLE mem_test (id INT64 NOT NULL, name STRING NULL, value FLOAT64 NULL, PRIMARY KEY(id)) ENGINE=memory")
	if resp.Code != 0 {
		t.Fatalf("创建内存表失败: %s", resp.Message)
	}
}

// verifyMemoryRows 验证响应中的行数据与期望一致。
func verifyMemoryRows(t *testing.T, rows []map[string]any) {
	t.Helper()
	if len(rows) != 3 {
		t.Fatalf("期望 3 行，得到 %d", len(rows))
	}
	byID := rowsByID(rows)
	for _, exp := range memoryTableRows() {
		id, _ := toInt64(exp["id"])
		row, ok := byID[id]
		if !ok {
			t.Errorf("缺少 id=%d 的行", id)
			continue
		}
		if row["name"] != exp["name"] {
			t.Errorf("id=%d name: 期望 %v，得到 %v", id, exp["name"], row["name"])
		}
		expVal, _ := toFloat64(exp["value"])
		if val, ok := toFloat64(row["value"]); !ok || val != expVal {
			t.Errorf("id=%d value: 期望 %v，得到 %v", id, expVal, row["value"])
		}
	}
}

// TestMemoryEngineCreateAndQuery 验证通过 SQL 创建内存表并查询的完整流程。
// 覆盖 TCP 与 HTTP 两种协议。
func TestMemoryEngineCreateAndQuery(t *testing.T) {
	for _, via := range []string{"tcp", "http"} {
		t.Run(via, func(t *testing.T) {
			s := startSQLServer(t)
			createMemoryTableViaSQL(t, s, via)
			writeVia(t, s, via, "mem_test", memoryTableRows())

			resp := queryVia(t, s, via, "SELECT * FROM mem_test")
			if resp.Code != 0 {
				t.Fatalf("查询内存表失败: %s", resp.Message)
			}
			verifyMemoryRows(t, respRows(resp))
		})
	}
}

// TestMemoryEnginePointQuery 验证内存表的 WHERE 等值点查。
func TestMemoryEnginePointQuery(t *testing.T) {
	s := startSQLServer(t)
	createMemoryTableViaSQL(t, s, "tcp")
	writeVia(t, s, "tcp", "mem_test", memoryTableRows())

	resp := queryVia(t, s, "tcp", "SELECT * FROM mem_test WHERE id = 2")
	if resp.Code != 0 {
		t.Fatalf("点查失败: %s", resp.Message)
	}
	rows := respRows(resp)
	if len(rows) != 1 {
		t.Fatalf("期望 1 行，得到 %d", len(rows))
	}
	if name := rows[0]["name"]; name != "beta" {
		t.Errorf("期望 name=beta，得到 %v", name)
	}
}

// TestMemoryEngineRangeQuery 验证内存表的范围查询。
func TestMemoryEngineRangeQuery(t *testing.T) {
	s := startSQLServer(t)
	createMemoryTableViaSQL(t, s, "tcp")
	writeVia(t, s, "tcp", "mem_test", memoryTableRows())

	resp := queryVia(t, s, "tcp", "SELECT * FROM mem_test WHERE id > 1")
	if resp.Code != 0 {
		t.Fatalf("范围查询失败: %s", resp.Message)
	}
	rows := respRows(resp)
	if len(rows) != 2 {
		t.Fatalf("期望 2 行，得到 %d", len(rows))
	}
}

// TestMemoryEngineUpdate 验证内存表的覆写更新（同主键）。
func TestMemoryEngineUpdate(t *testing.T) {
	s := startSQLServer(t)
	createMemoryTableViaSQL(t, s, "tcp")
	writeVia(t, s, "tcp", "mem_test", memoryTableRows())

	// 覆盖 id=1 的行
	writeVia(t, s, "tcp", "mem_test", []map[string]any{
		{"id": 1, "name": "updated", "value": 99.9},
	})

	resp := queryVia(t, s, "tcp", "SELECT * FROM mem_test WHERE id = 1")
	if resp.Code != 0 {
		t.Fatalf("查询失败: %s", resp.Message)
	}
	rows := respRows(resp)
	if len(rows) != 1 {
		t.Fatalf("期望 1 行，得到 %d", len(rows))
	}
	if name := rows[0]["name"]; name != "updated" {
		t.Errorf("期望 name=updated，得到 %v", name)
	}
	if val, _ := toFloat64(rows[0]["value"]); val != 99.9 {
		t.Errorf("期望 value=99.9，得到 %v", rows[0]["value"])
	}

	// 总行数应仍为 3（覆写不新增行）
	allResp := queryVia(t, s, "tcp", "SELECT * FROM mem_test")
	if got := len(respRows(allResp)); got != 3 {
		t.Errorf("覆写后总行数: 期望 3，得到 %d", got)
	}
}

// TestMemoryEngineAggregation 验证内存表上的聚合查询。
func TestMemoryEngineAggregation(t *testing.T) {
	s := startSQLServer(t)
	createMemoryTableViaSQL(t, s, "tcp")
	writeVia(t, s, "tcp", "mem_test", memoryTableRows())

	resp := queryVia(t, s, "tcp",
		"SELECT COUNT(*) AS cnt, SUM(value) AS total, AVG(value) AS avg_val FROM mem_test")
	if resp.Code != 0 {
		t.Fatalf("聚合查询失败: %s", resp.Message)
	}
	rows := respRows(resp)
	if len(rows) != 1 {
		t.Fatalf("期望 1 行聚合结果，得到 %d", len(rows))
	}
	assertFloat(t, "mem", "cnt", rows[0]["cnt"], 3)
	assertFloat(t, "mem", "total", rows[0]["total"], 60.75)
	assertFloat(t, "mem", "avg_val", rows[0]["avg_val"], 20.25)
}

// TestMemoryAndLSMCoexistence 验证内存表与 LSM 表可在同一 Server 中共存且互不干扰。
func TestMemoryAndLSMCoexistence(t *testing.T) {
	s := startSQLServer(t)

	// 创建 LSM 表（默认引擎）
	resp := queryVia(t, s, "tcp",
		"CREATE TABLE lsm_test (id INT64 NOT NULL, name STRING NULL, PRIMARY KEY(id))")
	if resp.Code != 0 {
		t.Fatalf("创建 LSM 表失败: %s", resp.Message)
	}

	// 创建内存表
	createMemoryTableViaSQL(t, s, "tcp")

	// 分别写入数据
	writeVia(t, s, "tcp", "lsm_test", []map[string]any{
		{"id": 100, "name": "lsm-row"},
	})
	writeVia(t, s, "tcp", "mem_test", memoryTableRows())

	// 验证 LSM 表查询
	lsmResp := queryVia(t, s, "tcp", "SELECT * FROM lsm_test")
	if lsmResp.Code != 0 {
		t.Fatalf("LSM 表查询失败: %s", lsmResp.Message)
	}
	if got := len(respRows(lsmResp)); got != 1 {
		t.Errorf("LSM 表期望 1 行，得到 %d", got)
	}

	// 验证内存表查询
	memResp := queryVia(t, s, "tcp", "SELECT * FROM mem_test")
	if memResp.Code != 0 {
		t.Fatalf("内存表查询失败: %s", memResp.Message)
	}
	if got := len(respRows(memResp)); got != 3 {
		t.Errorf("内存表期望 3 行，得到 %d", got)
	}
}

// TestMemoryEngineMultiClientConcurrent 验证多客户端并发写入内存表。
func TestMemoryEngineMultiClientConcurrent(t *testing.T) {
	s := startSQLServer(t)
	createMemoryTableViaSQL(t, s, "tcp")

	const numClients = 8
	const rowsPerClient = 20
	var wg sync.WaitGroup
	var failCount int64

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			via := "tcp"
			if clientID%2 == 0 {
				via = "http"
			}
			rows := make([]map[string]any, rowsPerClient)
			for j := 0; j < rowsPerClient; j++ {
				id := clientID*rowsPerClient + j + 1
				rows[j] = map[string]any{
					"id":    id,
					"name":  fmt.Sprintf("c%d", clientID),
					"value": float64(id),
				}
			}
			resp, err := rawWrite(s, via, "mem_test", rows)
			if err != nil {
				atomic.AddInt64(&failCount, 1)
				return
			}
			if resp.Code != 0 {
				atomic.AddInt64(&failCount, 1)
				return
			}
		}(i)
	}
	wg.Wait()

	if failCount > 0 {
		t.Fatalf("%d 个客户端失败", failCount)
	}

	resp := queryVia(t, s, "tcp", "SELECT * FROM mem_test")
	if resp.Code != 0 {
		t.Fatalf("最终查询失败: %s", resp.Message)
	}
	want := numClients * rowsPerClient
	if got := len(respRows(resp)); got != want {
		t.Errorf("期望 %d 行，得到 %d", want, got)
	}
}

// TestMemoryEngineCreateTableIfNotExists 验证 IF NOT EXISTS 语义对内存表生效。
func TestMemoryEngineCreateTableIfNotExists(t *testing.T) {
	s := startSQLServer(t)

	// 首次创建应成功
	resp := queryVia(t, s, "tcp",
		"CREATE TABLE IF NOT EXISTS mem_dup (id INT64 NOT NULL, PRIMARY KEY(id)) ENGINE=memory")
	if resp.Code != 0 {
		t.Fatalf("首次建表失败: %s", resp.Message)
	}

	// 重复创建 IF NOT EXISTS 应视为成功（不报错）
	resp = queryVia(t, s, "tcp",
		"CREATE TABLE IF NOT EXISTS mem_dup (id INT64 NOT NULL, PRIMARY KEY(id)) ENGINE=memory")
	if resp.Code != 0 {
		t.Fatalf("重复建表 IF NOT EXISTS 应成功: %s", resp.Message)
	}
}

// TestMemoryEngineDuplicateCreateError 验证未指定 IF NOT EXISTS 时重复建表返回错误。
func TestMemoryEngineDuplicateCreateError(t *testing.T) {
	s := startSQLServer(t)
	createMemoryTableViaSQL(t, s, "tcp")

	resp := queryVia(t, s, "tcp",
		"CREATE TABLE mem_test (id INT64 NOT NULL, name STRING NULL, value FLOAT64 NULL, PRIMARY KEY(id)) ENGINE=memory")
	if resp.Code == 0 {
		t.Error("重复建表应返回错误")
	}
}
