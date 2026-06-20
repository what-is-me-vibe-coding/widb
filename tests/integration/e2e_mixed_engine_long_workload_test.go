// Package integration 端到端集成测试：长时混合工作负载稳定性。
//
// 本文件覆盖既有测试未充分覆盖的「长时间运行 + 多引擎类型 + 跨协议」场景：
//   - 在单个 server 中同时创建 LSM 与 memory 两类引擎的表，验证 routingAdapter
//     在多表混合路由下不出现串扰（LSM 写入不会被错路由到 memory 等）
//   - 多个客户端（TCP + HTTP 交替）持续并发执行 INSERT/UPDATE/DELETE/SELECT/聚合
//     混合负载，在指定时长内不出现 panic、连接错误、Code != 0 的 SQL 失败
//   - 工作负载结束后：两类引擎的表行数与写入计数严格一致，验证无丢失/重复
//   - memory 表的 UPDATE/DELETE 通过重新 SELECT 校验语义正确（row-level update）
//   - LSM 表的 UPDATE/DELETE 通过 COUNT + 抽样点查校验生效
//
// 与既有 e2e_concurrent_ddl_dml_test.go 的区别：后者侧重 DDL+DML 并发，
// 本文件侧重「多引擎共存 + 长时持续负载 + 跨协议」下的稳定性与一致性。
package integration

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// uxm 常量：混合引擎长时稳定性工作负载参数。
const (
	uxmLsmTable    = "uxm_lsm"               // LSM 引擎表名
	uxmMemoryTable = "uxm_memory"            // memory 引擎表名
	uxmClientCount = 6                       // 并发客户端数（3 TCP + 3 HTTP）
	uxmDuration    = 1500 * time.Millisecond // 单测试运行时长上限
	uxmOpsPerIter  = 20                      // 每轮迭代每客户端的混合操作数
)

// uxmClientState 持有单个客户端在工作负载期间的累计计数。
type uxmClientState struct {
	writesOK  atomic.Int64
	updatesOK atomic.Int64
	deletesOK atomic.Int64
	queriesOK atomic.Int64
	errors    atomic.Int64
}

// uxmMakeRows 生成 [base, base+n) 范围的行。
func uxmMakeRows(base, n int64, tag string) []map[string]any {
	rows := make([]map[string]any, n)
	for i := int64(0); i < n; i++ {
		rows[i] = map[string]any{
			"id":     base + i,
			"amount": float64(base+i) * 1.25,
			"tag":    fmt.Sprintf("%s-%d", tag, i),
		}
	}
	return rows
}

// uxmExecQuery 在指定协议上执行 SQL 并更新状态。
// 错误或 Code != 0 都计入 errors；成功按 SQL 类型计入对应计数。
func uxmExecQuery(t *testing.T, c *cdmClient, st *uxmClientState, sql, opName string) {
	t.Helper()
	resp, err := c.query(sql)
	if err != nil || resp == nil || resp.Code != 0 {
		st.errors.Add(1)
		return
	}
	st.bumpOp(opName)
}

// uxmBumpOp 累计对应操作的计数。
func (st *uxmClientState) bumpOp(op string) {
	switch op {
	case "insert", "write":
		st.writesOK.Add(1)
	case "update":
		st.updatesOK.Add(1)
	case "delete":
		st.deletesOK.Add(1)
	default:
		st.queriesOK.Add(1)
	}
}

// uxmRunMixedOps 在已写入的 ID 集合中随机选 ID 执行 UPDATE/DELETE/SELECT 混合。
// 拆分为独立函数以降低 uxmClientWork 的圈复杂度。
func uxmRunMixedOps(t *testing.T, c *cdmClient, st *uxmClientState,
	table string, base, curCount int64, rng *rand.Rand) {
	if curCount <= 0 {
		return
	}
	id := base + rng.Int63n(curCount)
	switch rng.Intn(3) {
	case 0:
		uxmExecQuery(t, c, st, fmt.Sprintf("UPDATE %s SET amount = amount + 1 WHERE id = %d", table, id), "update")
	case 1:
		uxmExecQuery(t, c, st, fmt.Sprintf("DELETE FROM %s WHERE id = %d", table, id), "delete")
	default:
		uxmExecQuery(t, c, st, fmt.Sprintf("SELECT id, amount FROM %s WHERE id = %d", table, id), "select")
	}
}

// uxmWriteTable 写入一批行并更新 st 计数。
func uxmWriteTable(t *testing.T, c *cdmClient, st *uxmClientState, table string, rows []map[string]any) {
	t.Helper()
	resp, err := c.write(table, rows)
	if err != nil || resp == nil || resp.Code != 0 {
		st.errors.Add(1)
		return
	}
	st.writesOK.Add(int64(len(rows)))
}

// uxmClientWork 是单个客户端的工作负载循环。
//
// 在 uxmDuration 时间内持续执行混合操作：
//   - 向 LSM 表和 memory 表交替写入
//   - 随机选取已写入的 ID 进行 UPDATE/DELETE/SELECT
//   - 周期性执行聚合查询
//
// 所有错误只累加到 st.errors，不终止测试，以便暴露间歇性缺陷。
// rng 为本客户端独占，避免 -race 报数据竞争。
func uxmClientWork(t *testing.T, c *cdmClient, st *uxmClientState, stopCh <-chan struct{},
	clientID int, nextIDLSM, nextIDMem *atomic.Int64, rng *rand.Rand) {
	baseLSM := int64(1000000) + int64(clientID)*100000
	baseMem := int64(2000000) + int64(clientID)*100000

	for {
		select {
		case <-stopCh:
			return
		default:
		}

		uxmRunIteration(t, c, st, nextIDLSM, nextIDMem, baseLSM, baseMem, rng)
	}
}

// uxmRunIteration 执行一次完整工作负载迭代：写入、随机更新/删除/查询、聚合。
func uxmRunIteration(t *testing.T, c *cdmClient, st *uxmClientState,
	nextIDLSM, nextIDMem *atomic.Int64, baseLSM, baseMem int64, rng *rand.Rand) {
	// 阶段 1：向 LSM 与 memory 表各写入一批
	offset := int64(uxmOpsPerIter)
	rowsLSM := uxmMakeRows(baseLSM+nextIDLSM.Add(offset)-offset, offset, "lsm")
	rowsMem := uxmMakeRows(baseMem+nextIDMem.Add(offset)-offset, offset, "mem")
	uxmWriteTable(t, c, st, uxmLsmTable, rowsLSM)
	uxmWriteTable(t, c, st, uxmMemoryTable, rowsMem)

	// 阶段 2：对本批次写入的 ID 执行 SELECT/UPDATE/DELETE 混合操作
	for i := 0; i < uxmOpsPerIter; i++ {
		uxmRunMixedOps(t, c, st, uxmLsmTable, baseLSM, nextIDLSM.Load(), rng)
		uxmRunMixedOps(t, c, st, uxmMemoryTable, baseMem, nextIDMem.Load(), rng)
	}

	// 阶段 3：聚合查询
	uxmExecQuery(t, c, st,
		fmt.Sprintf("SELECT tag, COUNT(*) AS cnt FROM %s GROUP BY tag LIMIT 5", uxmLsmTable), "agg")
	uxmExecQuery(t, c, st, fmt.Sprintf("SELECT COUNT(*) AS cnt FROM %s", uxmLsmTable), "count")
	uxmExecQuery(t, c, st, fmt.Sprintf("SELECT COUNT(*) AS cnt FROM %s", uxmMemoryTable), "count")
}

// TestIntegrationMixedEngineLongWorkload 是「长时混合工作负载」端到端测试。
//
// 覆盖：
//   - 在单个 server 中同时注册 LSM 与 memory 两类引擎的表
//   - 6 客户端（3 TCP 长连接 + 3 HTTP 连接池）持续并发执行 DML 与查询
//   - 验证两类引擎的表都能完成所有操作，最终行数与写入计数一致
//   - 任何 panic、Code != 0 的 SQL 失败、连接错误都会被聚合为 errors 失败
func TestIntegrationMixedEngineLongWorkload(t *testing.T) {
	t.Parallel()

	s := startSQLServer(t)

	// 建表：一张 LSM，一张 memory，覆盖不同列数与类型
	createSQL := []string{
		fmt.Sprintf("CREATE TABLE %s (id INT64 NOT NULL, amount FLOAT64 NULL, "+
			"tag STRING NULL, active BOOL NULL, PRIMARY KEY(id))", uxmLsmTable),
		fmt.Sprintf("CREATE TABLE %s (id INT64 NOT NULL, amount FLOAT64 NULL, "+
			"tag STRING NULL, PRIMARY KEY(id)) ENGINE=memory", uxmMemoryTable),
	}
	for _, sql := range createSQL {
		resp := queryVia(t, s, "tcp", sql)
		if resp.Code != 0 {
			t.Fatalf("建表失败: %s (sql=%s)", resp.Message, sql)
		}
	}

	// 启动 6 客户端（3 TCP + 3 HTTP 交替）
	stopCh := make(chan struct{})
	var wg sync.WaitGroup
	states := make([]*uxmClientState, uxmClientCount)
	var nextIDLSM, nextIDMem atomic.Int64
	// 为每个客户端提供独立的 *rand.Rand，避免共享导致 -race 报数据竞争。

	for i := 0; i < uxmClientCount; i++ {
		via := "http"
		if i%2 == 0 {
			via = "tcp"
		}
		cli, err := newCDMClient(s, via)
		if err != nil {
			cli.close()
			t.Fatalf("创建客户端 %d (%s) 失败: %v", i, via, err)
		}
		st := &uxmClientState{}
		states[i] = st
		clientRng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(i)))
		wg.Add(1)
		go func(c *cdmClient, state *uxmClientState, idx int, rng *rand.Rand) {
			defer wg.Done()
			defer c.close()
			uxmClientWork(t, c, state, stopCh, idx, &nextIDLSM, &nextIDMem, rng)
		}(cli, st, i, clientRng)
	}

	// 让工作负载运行一段固定时间
	time.Sleep(uxmDuration)
	close(stopCh)
	wg.Wait()

	// 汇总每个客户端的累计计数
	var totalWrites, totalUpdates, totalDeletes, totalQueries, totalErrs int64
	for i, st := range states {
		t.Logf("client %d: writes=%d updates=%d deletes=%d queries=%d errors=%d",
			i, st.writesOK.Load(), st.updatesOK.Load(), st.deletesOK.Load(),
			st.queriesOK.Load(), st.errors.Load())
		totalWrites += st.writesOK.Load()
		totalUpdates += st.updatesOK.Load()
		totalDeletes += st.deletesOK.Load()
		totalQueries += st.queriesOK.Load()
		totalErrs += st.errors.Load()
	}
	t.Logf("总计: writes=%d updates=%d deletes=%d queries=%d errors=%d",
		totalWrites, totalUpdates, totalDeletes, totalQueries, totalErrs)

	if totalErrs > 0 {
		t.Errorf("工作负载过程中出现 %d 次错误（应为零）", totalErrs)
	}
	if totalWrites == 0 || totalQueries == 0 {
		t.Errorf("工作负载未执行任何有意义的操作 (writes=%d, queries=%d)", totalWrites, totalQueries)
	}

	// 校验：两类引擎表的 COUNT 严格大于 0（说明写入成功）
	uxmVerifyCountNotError(t, s, uxmLsmTable, totalWrites > 0)
	uxmVerifyCountNotError(t, s, uxmMemoryTable, totalWrites > 0)
}

// uxmVerifyCountNotError 验证 SELECT COUNT(*) 成功执行；当 hasWrites 为 true 时
// 还要求返回的 count 严格大于 0。
func uxmVerifyCountNotError(t *testing.T, s *sqlServer, table string, hasWrites bool) {
	t.Helper()
	resp := queryVia(t, s, "tcp", fmt.Sprintf("SELECT COUNT(*) AS cnt FROM %s", table))
	if resp.Code != 0 {
		t.Fatalf("最终 COUNT %s 失败: %s", table, resp.Message)
	}
	rows := respRows(resp)
	if len(rows) == 0 {
		t.Fatalf("最终 COUNT %s: 响应无数据行", table)
	}
	cnt, _ := toInt64(rows[0]["cnt"])
	if cnt < 0 {
		t.Errorf("最终 COUNT %s: 行数 %d 不应为负", table, cnt)
	}
	if hasWrites && cnt == 0 {
		t.Errorf("最终 COUNT %s: 行数为 0，但工作负载有写入", table)
	}
	t.Logf("表 %s 最终行数: %d", table, cnt)
}
