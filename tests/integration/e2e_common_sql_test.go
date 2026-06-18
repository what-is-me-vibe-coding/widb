// Package integration 端到端集成测试：一般 SQL 通过真实 server 的端到端正确性。
//
// 补充既有集成测试未覆盖的「一般 SQL」语义（经 TCP/HTTP 真实链路）：
//   - LIKE / NOT LIKE 模式匹配（既有仅单元测试覆盖，未走 server）
//   - LIMIT 带 OFFSET 的分页（既有仅测试 LIMIT 截断，未测试 OFFSET）
//   - NULL 值经 /write 写入后经 SELECT 读回（既有仅在存储层测试 NULL）
//   - 错误场景经网络返回非零码：主键冲突、UPDATE 主键冲突、类型不匹配、未知列、缺失主键
//   - 多客户端并发执行上述一般 SQL 工作负载，验证并发稳定性
package integration

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

// execSQLVia 按协议执行 SQL（DDL/DML/SELECT），响应码非零时终止测试。
// 与 queryVia（仅传输错误时终止）互补，用于必须成功的语句。
func execSQLVia(t *testing.T, s *sqlServer, via, sql string) *server.Response {
	t.Helper()
	resp := queryVia(t, s, via, sql)
	if resp.Code != 0 {
		t.Fatalf("SQL 执行失败 [%s]: %s", sql, resp.Message)
	}
	return resp
}

// likeCase 描述一个 WHERE 子句及其期望行数。
type likeCase struct {
	clause string
	want   int
}

// TestServerSQLLike 验证 LIKE / NOT LIKE 模式匹配经 TCP 与 HTTP 的正确性。
//
// sensor 数据：sensor-A×2、sensor-B×2、sensor-C×1，覆盖前缀/后缀/通配/组合/无匹配。
func TestServerSQLLike(t *testing.T) {
	for _, via := range []string{"tcp", "http"} {
		t.Run(via, func(t *testing.T) {
			s := startSQLServer(t)
			seedSensorData(t, s, via)
			cases := []likeCase{
				{"name LIKE 'sensor-A%'", 2},
				{"name LIKE '%-C'", 1},
				{"name NOT LIKE '%-A'", 3},
				{"name LIKE 'sensor-%' AND temperature > 25", 2},
				{"name LIKE 'zzz%'", 0},
			}
			for _, tc := range cases {
				sql := fmt.Sprintf("SELECT * FROM sensor WHERE %s", tc.clause)
				resp := queryVia(t, s, via, sql)
				if resp.Code != 0 {
					t.Fatalf("WHERE %s 查询失败: %s", tc.clause, resp.Message)
				}
				if got := len(respRows(resp)); got != tc.want {
					t.Errorf("WHERE %s: 期望 %d 行，得到 %d", tc.clause, tc.want, got)
				}
			}
		})
	}
}

// limitCase 描述一个 LIMIT 子句及其期望行数。
type limitCase struct {
	limit string
	want  int
}

// TestServerSQLLimitOffset 验证 LIMIT 带 OFFSET 的分页语义经 server 的正确性。
//
// sensor 表共 5 行，覆盖纯 LIMIT、offset+count、offset 越界、WHERE+LIMIT 组合。
func TestServerSQLLimitOffset(t *testing.T) {
	s := startSQLServer(t)
	seedSensorData(t, s, "tcp")
	cases := []limitCase{
		{"LIMIT 2", 2},
		{"LIMIT 2, 2", 2},
		{"LIMIT 0, 3", 3},
		{"LIMIT 10, 5", 0},
		{"LIMIT 1, 2", 2},
	}
	for _, tc := range cases {
		sql := fmt.Sprintf("SELECT * FROM sensor %s", tc.limit)
		resp := queryVia(t, s, "tcp", sql)
		if resp.Code != 0 {
			t.Fatalf("%s 查询失败: %s", tc.limit, resp.Message)
		}
		if got := len(respRows(resp)); got != tc.want {
			t.Errorf("%s: 期望 %d 行，得到 %d", tc.limit, tc.want, got)
		}
	}
	// WHERE 与 LIMIT/OFFSET 组合：id>1 过滤后 4 行，offset 1 取 2 行 → 2 行
	resp := queryVia(t, s, "tcp", "SELECT * FROM sensor WHERE id > 1 LIMIT 1, 2")
	if resp.Code != 0 {
		t.Fatalf("WHERE+LIMIT 查询失败: %s", resp.Message)
	}
	if got := len(respRows(resp)); got != 2 {
		t.Errorf("WHERE id>1 LIMIT 1,2: 期望 2 行，得到 %d", got)
	}
}

// TestServerSQLNullRoundTrip 验证 NULL 值经 /write 写入后经 SELECT 读回为 JSON null。
//
// 覆盖 TCP 与 HTTP，验证 NULL 在写入路径、存储、查询路径与 JSON 序列化全链路的保持。
func TestServerSQLNullRoundTrip(t *testing.T) {
	for _, via := range []string{"tcp", "http"} {
		t.Run(via, func(t *testing.T) {
			s := startSQLServer(t)
			execSQLVia(t, s, via, "CREATE TABLE n (id INT64 NOT NULL, "+
				"name STRING NULL, age INT64 NULL, PRIMARY KEY(id))")
			writeVia(t, s, via, "n", []map[string]any{
				{"id": 1, "name": "alice", "age": 30},
				{"id": 2, "name": nil, "age": nil},
			})

			nullRows := respRows(queryVia(t, s, via, "SELECT * FROM n WHERE id = 2"))
			if len(nullRows) != 1 {
				t.Fatalf("id=2: 期望 1 行，得到 %d", len(nullRows))
			}
			if nullRows[0]["name"] != nil {
				t.Errorf("id=2 name: 期望 nil，得到 %v", nullRows[0]["name"])
			}
			if nullRows[0]["age"] != nil {
				t.Errorf("id=2 age: 期望 nil，得到 %v", nullRows[0]["age"])
			}

			dataRows := respRows(queryVia(t, s, via, "SELECT * FROM n WHERE id = 1"))
			if len(dataRows) != 1 {
				t.Fatalf("id=1: 期望 1 行，得到 %d", len(dataRows))
			}
			if dataRows[0]["name"] != "alice" {
				t.Errorf("id=1 name: 期望 alice，得到 %v", dataRows[0]["name"])
			}
			age, _ := toInt64(dataRows[0]["age"])
			if age != 30 {
				t.Errorf("id=1 age: 期望 30，得到 %v", dataRows[0]["age"])
			}
		})
	}
}

// TestServerSQLErrorsOverWire 验证错误场景经 TCP/HTTP 返回非零码。
//
// 覆盖：INSERT 主键冲突（且原值不被覆盖）、UPDATE 主键冲突、/write 类型不匹配、
// SELECT 未知列、/write 缺失主键。这些路径既有仅在进程内测试，未验证网络序列化。
func TestServerSQLErrorsOverWire(t *testing.T) {
	for _, via := range []string{"tcp", "http"} {
		t.Run(via, func(t *testing.T) {
			s := startSQLServer(t)
			execSQLVia(t, s, via, "CREATE TABLE err_t (id INT64 NOT NULL, "+
				"v STRING NULL, active BOOL NULL, PRIMARY KEY(id))")
			writeVia(t, s, via, "err_t", []map[string]any{
				{"id": 1, "v": "a", "active": true},
				{"id": 2, "v": "b", "active": false},
			})

			// INSERT 主键冲突：重复 id=1 应失败，且原值 'a' 不被覆盖
			dupResp := queryVia(t, s, via, "INSERT INTO err_t (id, v) VALUES (1, 'b')")
			if dupResp.Code == 0 {
				t.Error("重复主键 INSERT 应返回错误")
			}
			rows := respRows(queryVia(t, s, via, "SELECT v FROM err_t WHERE id = 1"))
			if len(rows) != 1 || rows[0]["v"] != "a" {
				t.Errorf("重复 INSERT 后 v: 期望 a，得到 %v", rows)
			}

			// UPDATE 主键冲突：将 id=1 改为已存在的 id=2 应失败
			updResp := queryVia(t, s, via, "UPDATE err_t SET id = 2 WHERE id = 1")
			if updResp.Code == 0 {
				t.Error("UPDATE 导致主键冲突应返回错误")
			}

			// /write 类型不匹配：active 为 BOOL，传入字符串应失败
			assertWriteFails(t, s, via, "err_t",
				[]map[string]any{{"id": 3, "active": "not-a-bool"}}, "类型不匹配")

			// SELECT 未知列应失败
			colResp := queryVia(t, s, via, "SELECT bad_col FROM err_t")
			if colResp.Code == 0 {
				t.Error("查询未知列应返回错误")
			}

			// /write 缺失主键应失败
			assertWriteFails(t, s, via, "err_t",
				[]map[string]any{{"v": "x"}}, "缺失主键")
		})
	}
}

// assertWriteFails 断言 /write 返回非零响应码。
func assertWriteFails(t *testing.T, s *sqlServer, via, table string,
	rows []map[string]any, hint string,
) {
	t.Helper()
	resp, err := rawWrite(s, via, table, rows)
	if err != nil {
		t.Fatalf("%s 写入请求失败: %v", hint, err)
	}
	if resp.Code == 0 {
		t.Errorf("%s 写入应返回错误，但返回成功", hint)
	}
}

// mcCommon 常量：多客户端一般 SQL 并发工作负载参数。
const (
	mcCommonClients = 8 // 并发客户端数
	mcCommonRows    = 6 // 每客户端写入行数
)

// TestMultiClientCommonSQL 验证多客户端并发执行一般 SQL 工作负载的正确性。
//
// 启动一个 server，多个客户端（TCP/HTTP 混合）在互不冲突的 ID 区间上并发执行：
// 写入 → LIKE 过滤 → LIMIT/OFFSET 分页 → UPDATE → DELETE，最终校验总行数。
// 直接验证「一个 server + 多个 client + 一般 SQL」的并发稳定性。
func TestMultiClientCommonSQL(t *testing.T) {
	s := startSQLServer(t)
	execSQLVia(t, s, "tcp", "CREATE TABLE mc_sql (id INT64 NOT NULL, "+
		"name STRING NULL, value FLOAT64 NULL, active BOOL NULL, PRIMARY KEY(id))")

	var wg sync.WaitGroup
	var failCount int64
	var lastErr atomic.Value
	for i := 0; i < mcCommonClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			if err := runCommonSQLClient(s, clientID); err != nil {
				lastErr.Store(err.Error())
				atomic.AddInt64(&failCount, 1)
			}
		}(i)
	}
	wg.Wait()

	if failCount > 0 {
		t.Fatalf("%d 个客户端失败，最后错误: %v", failCount, lastErr.Load())
	}

	// 每客户端写入 mcCommonRows 行并删除 1 行，剩余 mcCommonRows-1 行
	want := int64(mcCommonClients * (mcCommonRows - 1))
	c := dialTCPClient(t, s)
	defer c.close()
	if got := sessCount(t, c, "mc_sql"); got != want {
		t.Errorf("总行数: 期望 %d，得到 %d", want, got)
	}
}

// runCommonSQLClient 在独立 ID 区间上执行一般 SQL 工作负载。
// 返回错误而非调用 t.Fatal，便于在 goroutine 中使用。
func runCommonSQLClient(s *sqlServer, clientID int) error {
	via := "tcp"
	if clientID%2 == 0 {
		via = "http"
	}
	base := clientID*1000 + 100
	rows := make([]map[string]any, mcCommonRows)
	for i := 0; i < mcCommonRows; i++ {
		suffix := 'a'
		if i%2 == 1 {
			suffix = 'b'
		}
		rows[i] = map[string]any{
			"id":     base + i,
			"name":   fmt.Sprintf("c%d-%c", clientID, suffix),
			"value":  float64(base + i),
			"active": i%2 == 0,
		}
	}
	wresp, err := rawWrite(s, via, "mc_sql", rows)
	if err != nil {
		return fmt.Errorf("写入: %w", err)
	}
	if wresp.Code != 0 {
		return fmt.Errorf("写入失败: %s", wresp.Message)
	}
	return verifyCommonSQLClient(s, via, clientID, base)
}

// verifyCommonSQLClient 验证 LIKE、LIMIT/OFFSET、UPDATE、DELETE 在该客户端区间上的正确性。
func verifyCommonSQLClient(s *sqlServer, via string, clientID, base int) error {
	// LIKE 过滤：name 以 'c{id}-a' 开头的行数 = mcCommonRows/2 = 3
	likeSQL := fmt.Sprintf("SELECT * FROM mc_sql WHERE name LIKE 'c%d-a%%'", clientID)
	resp, err := rawQuery(s, via, likeSQL)
	if err != nil {
		return fmt.Errorf("LIKE 查询: %w", err)
	}
	if resp.Code != 0 {
		return fmt.Errorf("LIKE 查询失败: %s", resp.Message)
	}
	wantA := mcCommonRows / 2
	if got := len(respRows(resp)); got != wantA {
		return fmt.Errorf("LIKE c%d-a%%: 期望 %d 行，得到 %d", clientID, wantA, got)
	}

	// LIMIT/OFFSET 分页：该客户端共 mcCommonRows 行，offset 1 取 2 行
	limitSQL := fmt.Sprintf(
		"SELECT * FROM mc_sql WHERE name LIKE 'c%d-%%' LIMIT 1, 2", clientID)
	resp, err = rawQuery(s, via, limitSQL)
	if err != nil {
		return fmt.Errorf("LIMIT 查询: %w", err)
	}
	if resp.Code != 0 {
		return fmt.Errorf("LIMIT 查询失败: %s", resp.Message)
	}
	if got := len(respRows(resp)); got != 2 {
		return fmt.Errorf("LIMIT 1,2: 期望 2 行，得到 %d", got)
	}

	// UPDATE：修改区间首行
	resp, err = rawQuery(s, via,
		fmt.Sprintf("UPDATE mc_sql SET value = 999 WHERE id = %d", base))
	if err != nil {
		return fmt.Errorf("UPDATE: %w", err)
	}
	if resp.Code != 0 || resp.Rows != 1 {
		return fmt.Errorf("UPDATE id=%d: code=%d rows=%d", base, resp.Code, resp.Rows)
	}

	// DELETE：删除区间第二行
	resp, err = rawQuery(s, via,
		fmt.Sprintf("DELETE FROM mc_sql WHERE id = %d", base+1))
	if err != nil {
		return fmt.Errorf("DELETE: %w", err)
	}
	if resp.Code != 0 || resp.Rows != 1 {
		return fmt.Errorf("DELETE id=%d: code=%d rows=%d", base+1, resp.Code, resp.Rows)
	}
	return nil
}
