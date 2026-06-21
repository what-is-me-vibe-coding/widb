// Package integration 端到端集成测试：真实业务场景 + 多客户端 + 多协议。
//
// 既有 e2e_general_sql_multiclient_* 已覆盖「单表多客户端 DML/查询」，本文件
// 进一步补齐「双表（维度表 + 事实表）+ 业务分析 SQL 模板（总销售额/分组聚合/
// 多条件过滤/LIKE/分页）+ 跨 TCP/HTTP 一致性」端到端场景：
//
//   - 维度表 biz_customers（memory 引擎，热数据、低延迟查询）
//   - 事实表 biz_orders（LSM 引擎，模拟大规模订单存储）
//   - 4 个客户端（2 TCP + 2 HTTP）通过不同协议连接同一 server 并发执行
//     「订单分析」类 SQL：SUM/AVG/COUNT/GROUP BY/WHERE AND/LIKE/LIMIT OFFSET
//   - 最终验证：跨协议读到的聚合结果一致；行数、过滤、聚合均符合期望
//   - DML 阶段：UPDATE 修改部分订单状态、DELETE 删除已取消订单，再校验
//     聚合结果随数据变更正确更新
//
// 与 e2e_olap_multiclient_test.go 的区别：后者聚焦单一 OLAP 表 + 三协议，
// 本文件聚焦「双表（异构引擎）+ 业务分析模板」，是 OLAP 场景的子集，验证
// 分析型查询在异构存储上的端到端正确性。
//
// 注意：列名避免使用 SQL 保留字 level/status 等，使用 tier/state。
package integration

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// biz 测试常量：业务场景参数。
const (
	bizCustomerTable = "biz_customers" // 维度表（memory 引擎）
	bizOrderTable    = "biz_orders"    // 事实表（LSM 引擎）
	bizClientCount   = 4               // 并发客户端数
	bizQueryRounds   = 3               // 每客户端分析查询轮数
	bizRegionCount   = 3               // region 维度基数
	bizLevelCount    = 3               // customer tier 基数
	bizProductCount  = 4               // product 维度基数
	bizStateCount    = 3               // order state 基数
	bizInitialOrders = 24              // 初始订单数
	bizBaseOrderID   = 1000
)

// bizRegion / bizLevel / bizProduct / bizState 定义业务维度字典。
var (
	bizRegions  = [...]string{"cn-east", "cn-west", "us-east"}
	bizTiers    = [...]string{"gold", "silver", "bronze"}
	bizProducts = [...]string{"laptop", "phone", "tablet", "watch"}
	bizStates   = [...]string{"completed", "shipped", "cancelled"}
)

// bizSetupTables 创建双表（维度表 memory、事实表 LSM）并写入初始数据。
//
// 维度表 biz_customers：3 区域 × 3 等级 = 9 行；事实表 biz_orders：24 行
// 订单覆盖 3 个 region、3 个 state、4 个 product 维度，amount 由 (region,
// product) 决定，确保后续聚合数值可被确定性验证。
func bizSetupTables(t *testing.T, s *sqlServer) {
	t.Helper()
	execSQLVia(t, s, "tcp", "CREATE TABLE "+bizCustomerTable+
		" (id INT64 NOT NULL, name STRING NULL, "+
		"region STRING NULL, tier STRING NULL, PRIMARY KEY(id)) ENGINE=memory")
	execSQLVia(t, s, "tcp", "CREATE TABLE "+bizOrderTable+
		" (id INT64 NOT NULL, customer_id INT64 NULL, "+
		"product STRING NULL, amount FLOAT64 NULL, "+
		"state STRING NULL, PRIMARY KEY(id))")
	bizSeedCustomers(t, s)
	bizSeedOrders(t, s)
}

// bizSeedCustomers 灌入 9 行维度数据。
func bizSeedCustomers(t *testing.T, s *sqlServer) {
	t.Helper()
	rows := make([]map[string]any, 0, bizRegionCount*bizLevelCount)
	id := int64(1)
	for _, r := range bizRegions {
		for _, l := range bizTiers {
			rows = append(rows, map[string]any{
				"id": id, "name": fmt.Sprintf("cust-%d", id),
				"region": r, "tier": l,
			})
			id++
		}
	}
	writeVia(t, s, "tcp", bizCustomerTable, rows)
}

// bizSeedOrders 灌入 bizInitialOrders 行订单数据；amount 公式 amount = 50 +
// (regionIndex*30) + (productIndex*5)，保证按 region+product 维度的求和可
// 被精确断言。
func bizSeedOrders(t *testing.T, s *sqlServer) {
	t.Helper()
	rows := make([]map[string]any, bizInitialOrders)
	for i := 0; i < bizInitialOrders; i++ {
		regionIdx := i % bizRegionCount
		productIdx := i % bizProductCount
		stateIdx := i % bizStateCount
		rows[i] = map[string]any{
			"id":          int64(bizBaseOrderID + i),
			"customer_id": int64(regionIdx*bizLevelCount + 1), // 关联到种子客户
			"product":     bizProducts[productIdx],
			"amount":      float64(50 + regionIdx*30 + productIdx*5),
			"state":       bizStates[stateIdx],
		}
	}
	writeVia(t, s, "tcp", bizOrderTable, rows)
}

// bizAmountFor 计算 (regionIdx, productIdx) 维度上的单行 amount，公式与
// bizSeedOrders 保持一致。
func bizAmountFor(regionIdx, productIdx int) float64 {
	return float64(50 + regionIdx*30 + productIdx*5)
}

// bizClientWork 单客户端分析查询工作负载：执行 bizQueryRounds 轮真实业务
// SQL 模板（聚合/过滤/LIKE/分页），每轮通过同一协议客户端。
//
// 不同客户端使用不同协议（按 clientID 奇偶分配 TCP/HTTP），最终由测试
// 主流程对各协议结果做交叉一致性断言。
func bizClientWork(t *testing.T, s *sqlServer, clientID int, errCount *int64) {
	via := "tcp"
	if clientID%2 == 1 {
		via = "http"
	}
	for round := 0; round < bizQueryRounds; round++ {
		bizRunAggregateQueries(t, s, via, errCount)
		bizRunFilterQueries(t, s, via, errCount)
		bizRunPaginationQuery(t, s, via, errCount)
	}
}

// bizRunAggregateQueries 业务聚合查询：总订单数 + 按 region 分组销售额。
//
// 断言要点：
//   - 总订单数 = bizInitialOrders
//   - 按 region 分组求和 = 该 region 在种子数据中行数 × bizAmountFor 之和
func bizRunAggregateQueries(t *testing.T, s *sqlServer, via string, errCount *int64) {
	t.Helper()
	resp := queryVia(t, s, via, "SELECT COUNT(*) AS cnt FROM "+bizOrderTable)
	if resp.Code != 0 {
		atomic.AddInt64(errCount, 1)
		t.Errorf("[%s] COUNT 失败: %s", via, resp.Message)
		return
	}
	got, _ := toInt64(respRows(resp)[0]["cnt"])
	if got != int64(bizInitialOrders) {
		atomic.AddInt64(errCount, 1)
		t.Errorf("[%s] COUNT: 期望 %d，得到 %d", via, bizInitialOrders, got)
	}

	for ri := 0; ri < bizRegionCount; ri++ {
		resp = queryVia(t, s, via, fmt.Sprintf(
			"SELECT SUM(amount) AS total FROM "+bizOrderTable+
				" WHERE customer_id >= %d AND customer_id <= %d",
			ri*bizLevelCount+1, ri*bizLevelCount+bizLevelCount))
		if resp.Code != 0 {
			atomic.AddInt64(errCount, 1)
			t.Errorf("[%s] region-%d SUM 失败: %s", via, ri, resp.Message)
			continue
		}
		rows := respRows(resp)
		if len(rows) != 1 {
			atomic.AddInt64(errCount, 1)
			t.Errorf("[%s] region-%d SUM 期望 1 行，得到 %d", via, ri, len(rows))
			continue
		}
		got, ok := toFloat64(rows[0]["total"])
		if !ok {
			atomic.AddInt64(errCount, 1)
			t.Errorf("[%s] region-%d SUM 类型异常: %T", via, ri, rows[0]["total"])
			continue
		}
		// 期望：region ri 的客户持有 bizInitialOrders/bizRegionCount 行订单，
		// 每行 amount 在 4 个 product 间循环。计算期望值：
		ordersThisRegion := bizInitialOrders / bizRegionCount
		want := 0.0
		for pi := 0; pi < bizProductCount; pi++ {
			count := ordersThisRegion / bizProductCount
			if pi < ordersThisRegion%bizProductCount {
				count++
			}
			want += float64(count) * bizAmountFor(ri, pi)
		}
		if got != want {
			atomic.AddInt64(errCount, 1)
			t.Errorf("[%s] region-%d SUM: 期望 %.2f，得到 %.2f", via, ri, want, got)
		}
	}
}

// bizRunFilterQueries 业务过滤查询：state 过滤 + LIKE 模糊匹配 + 多条件 AND。
func bizRunFilterQueries(t *testing.T, s *sqlServer, via string, errCount *int64) {
	t.Helper()
	// state 过滤：completed 行数 = bizInitialOrders/bizStateCount
	wantCompleted := int64(bizInitialOrders / bizStateCount)
	resp := queryVia(t, s, via,
		"SELECT COUNT(*) AS cnt FROM "+bizOrderTable+" WHERE state = 'completed'")
	if resp.Code != 0 {
		atomic.AddInt64(errCount, 1)
		t.Errorf("[%s] state 过滤失败: %s", via, resp.Message)
		return
	}
	got, _ := toInt64(respRows(resp)[0]["cnt"])
	if got != wantCompleted {
		atomic.AddInt64(errCount, 1)
		t.Errorf("[%s] state='completed': 期望 %d，得到 %d",
			via, wantCompleted, got)
	}

	// LIKE 模糊匹配：product LIKE 'l%' 仅 laptop 命中，期望 1/4 行。
	wantLaptop := int64(bizInitialOrders / bizProductCount)
	resp = queryVia(t, s, via,
		"SELECT COUNT(*) AS cnt FROM "+bizOrderTable+" WHERE product LIKE 'l%'")
	if resp.Code != 0 {
		atomic.AddInt64(errCount, 1)
		t.Errorf("[%s] LIKE 失败: %s", via, resp.Message)
		return
	}
	got, _ = toInt64(respRows(resp)[0]["cnt"])
	if got != wantLaptop {
		atomic.AddInt64(errCount, 1)
		t.Errorf("[%s] LIKE 'l%%': 期望 %d，得到 %d", via, wantLaptop, got)
	}

	// 多条件组合：state=completed AND amount > 100，结果行数应在 [0, bizInitialOrders]。
	resp = queryVia(t, s, via,
		"SELECT COUNT(*) AS cnt FROM "+bizOrderTable+
			" WHERE state = 'completed' AND amount > 100")
	if resp.Code != 0 {
		atomic.AddInt64(errCount, 1)
		t.Errorf("[%s] AND 过滤失败: %s", via, resp.Message)
		return
	}
	got, _ = toInt64(respRows(resp)[0]["cnt"])
	if got < 0 || got > int64(bizInitialOrders) {
		atomic.AddInt64(errCount, 1)
		t.Errorf("[%s] AND 过滤结果越界: %d", via, got)
	}
}

// bizRunPaginationQuery 业务分页查询：LIMIT 5 / LIMIT 0,5 / LIMIT 5,5 / 越界。
//
// 校验：第 1 页 5 行、第 2 页 5 行、offset 越界 0 行。
func bizRunPaginationQuery(t *testing.T, s *sqlServer, via string, errCount *int64) {
	t.Helper()
	cases := []struct {
		limit string
		want  int
	}{
		{"LIMIT 5", 5},
		{"LIMIT 0, 5", 5},
		{"LIMIT 5, 5", 5},
		{"LIMIT 100, 5", 0},
	}
	for _, c := range cases {
		resp := queryVia(t, s, via,
			"SELECT * FROM "+bizOrderTable+" "+c.limit)
		if resp.Code != 0 {
			atomic.AddInt64(errCount, 1)
			t.Errorf("[%s] %s 失败: %s", via, c.limit, resp.Message)
			continue
		}
		if got := len(respRows(resp)); got != c.want {
			atomic.AddInt64(errCount, 1)
			t.Errorf("[%s] %s: 期望 %d 行，得到 %d",
				via, c.limit, c.want, got)
		}
	}
}

// TestRealisticBusinessScenarioSQL 端到端验证「一个 server + 4 客户端 +
// 双表（memory+LSM）+ 业务分析 SQL 模板」组合的正确性与并发稳定性。
//
// 工作流：
//  1. 启动一个 server，创建维度表（memory）+ 事实表（LSM）并灌入种子数据；
//  2. 4 个客户端（2 TCP + 2 HTTP）并发执行 3 轮分析查询（聚合/过滤/分页）；
//  3. 验证所有客户端 SQL 均成功（errCount == 0）；
//  4. DML 阶段：UPDATE 关闭部分订单、DELETE 删除已取消订单；
//  5. 重新跑一次聚合查询，验证数据变更被 server 端正确反映；
//  6. 跨协议读一致性：TCP 与 HTTP 拉取 state 分布，比对结果一致。
func TestRealisticBusinessScenarioSQL(t *testing.T) {
	t.Parallel()
	s := startSQLServer(t)
	bizSetupTables(t, s)

	// 阶段 1：4 客户端 × 3 轮分析查询
	bizRunConcurrentQueryPhase(t, s)
	// 阶段 2：DML 变更 + 阶段 3：行数校验
	bizRunDMLAndVerifyRowCount(t, s)
	// 阶段 4：跨协议读一致性
	bizVerifyCrossProtocolStateConsistency(t, s)
}

// bizRunConcurrentQueryPhase 阶段 1：4 客户端并发执行 3 轮分析查询。
func bizRunConcurrentQueryPhase(t *testing.T, s *sqlServer) {
	var errCount int64
	var wg sync.WaitGroup
	for i := 0; i < bizClientCount; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			bizClientWork(t, s, clientID, &errCount)
		}(i)
	}
	wg.Wait()
	if errCount > 0 {
		t.Fatalf("多客户端分析查询失败 %d 次", errCount)
	}
}

// bizRunDMLAndVerifyRowCount 阶段 2 + 3：执行 UPDATE/DELETE 并校验 DML 后
// 行数与期望一致。期望行数计算：除仍为 cancelled 的行外，其余保留（被 UPDATE
// 的前 3 行均被改成 completed）。
func bizRunDMLAndVerifyRowCount(t *testing.T, s *sqlServer) {
	// 阶段 2：DML 变更
	for i := 0; i < 3; i++ {
		execSQLVia(t, s, "tcp", fmt.Sprintf(
			"UPDATE "+bizOrderTable+" SET state = 'completed' WHERE id = %d",
			bizBaseOrderID+i))
	}
	execSQLVia(t, s, "tcp",
		"DELETE FROM "+bizOrderTable+" WHERE state = 'cancelled'")

	// 阶段 3：行数校验
	wantRemaining := int64(0)
	for i := 0; i < bizInitialOrders; i++ {
		stateIdx := i % bizStateCount
		isUpdatedRow := i < 3
		wasCancelled := bizStates[stateIdx] == "cancelled"
		if isUpdatedRow || !wasCancelled {
			wantRemaining++
		}
	}
	resp := queryVia(t, s, "tcp", "SELECT COUNT(*) AS cnt FROM "+bizOrderTable)
	if resp.Code != 0 {
		t.Fatalf("DML 后 COUNT 失败: %s", resp.Message)
	}
	got, _ := toInt64(respRows(resp)[0]["cnt"])
	if got != wantRemaining {
		t.Errorf("DML 后行数: 期望 %d，得到 %d", wantRemaining, got)
	}
}

// bizVerifyCrossProtocolStateConsistency 阶段 4：TCP 与 HTTP 拉取 state 分
// 布并断言结果一致。任一协议查询失败或分组数不一致均 Fatal。
func bizVerifyCrossProtocolStateConsistency(t *testing.T, s *sqlServer) {
	tcpResp := queryVia(t, s, "tcp",
		"SELECT state, COUNT(*) AS cnt FROM "+bizOrderTable+
			" GROUP BY state")
	httpResp := queryVia(t, s, "http",
		"SELECT state, COUNT(*) AS cnt FROM "+bizOrderTable+
			" GROUP BY state")
	if tcpResp.Code != 0 || httpResp.Code != 0 {
		t.Fatalf("GROUP BY 跨协议失败: tcp=%s http=%s",
			tcpResp.Message, httpResp.Message)
	}
	tcpRows := respRows(tcpResp)
	httpRows := respRows(httpResp)
	if len(tcpRows) != len(httpRows) {
		t.Fatalf("跨协议 GROUP BY 分组数不一致: tcp=%d http=%d",
			len(tcpRows), len(httpRows))
	}
	tcpByState := bizIndexByState(tcpRows)
	httpByState := bizIndexByState(httpRows)
	for state, tcnt := range tcpByState {
		hcnt, ok := httpByState[state]
		if !ok {
			t.Errorf("HTTP 结果缺少 state=%s 分组", state)
			continue
		}
		if tcnt != hcnt {
			t.Errorf("state=%s 跨协议不一致: tcp=%d http=%d",
				state, tcnt, hcnt)
		}
	}
}

// bizIndexByState 将 GROUP BY 结果按 state 字段建立索引，便于跨协议比对。
// 行中 state 字段为 string、cnt 字段为 int64/float64。
func bizIndexByState(rows []map[string]any) map[string]int64 {
	result := make(map[string]int64, len(rows))
	for _, r := range rows {
		state, ok := r["state"].(string)
		if !ok {
			continue
		}
		cnt, _ := toInt64(r["cnt"])
		result[state] = cnt
	}
	return result
}
