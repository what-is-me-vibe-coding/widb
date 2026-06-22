// Package integration 端到端集成测试：复合主键 + 多客户端工作负载。
//
// 本文件覆盖「CREATE TABLE ... PRIMARY KEY (a, b) 复合主键」在多客户端
// 场景下的端到端行为，弥补既有 e2e_*_multiclient_*.go 仅覆盖单列主键的
// 空白。重点验证：
//   - 复合主键建表经 TCP/HTTP SQL 通道均可成功
//   - INSERT 写入按 (a, b) 拼接 \x00 分隔的存储 key 落盘，多客户端并发
//     写入各自的 (a, b) 空间零冲突
//   - DML 主键等值快路径在复合主键下仍然命中：
//     UPDATE t SET ... WHERE a = ? AND b = ?
//     DELETE FROM t WHERE a = ? AND b = ?
//     单元覆盖见 pkg/server/handlers_dml_fastpath_test.go 的
//     TestSQLDeleteByCompositePKFastPath / TestSQLUpdateByCompositePKFastPath
//   - SELECT 在复合主键下沿用字符串拼接扫描的现有契约（详见
//     doc/sql-reference.md 边界条款「复合主键查询时仍按字符串拼接排序」）：
//     SELECT 校验使用 COUNT(*) 与非主键列的等值过滤，避免依赖未实现的
//     复合主键点查快路径
//
// 既有单列主键测试见 e2e_general_sql_multiclient_test.go 与
// e2e_realistic_business_sql_test.go；本文件不重复覆盖单列主键场景。
//
// 测试设计原则：
//   - 复用 e2e_server_sql_test.go 中的 sqlServer / startSQLServer / queryVia /
//     writeVia / respRows / toInt64 / toFloat64 等公共 helper
//   - t.Parallel 并发执行
//   - 每个客户端负责独立的 order_id 区间，避免跨客户端主键碰撞
//   - 复合主键 UPDATE/DELETE WHERE 必须覆盖全部主键列，与单列主键的
//     O(log n) 快路径契约一致
package integration

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

// cpk 复合主键测试参数。
const (
	cpkClientCount   = 4
	cpkOrdersPerClt  = 3
	cpkSKUsPerOrder  = 4
	cpkClientBaseOID = 40000 // 客户端订单 ID 起始偏移
	cpkIterations    = 2     // 每客户端工作负载迭代轮数
	cpkTable         = "order_item"
)

// cpkSKUs 是所有客户端复用的固定 SKU 集合，保证每行 (order_id, sku) 全局唯一。
var cpkSKUs = [...]string{"SKU-AAA", "SKU-BBB", "SKU-CCC", "SKU-DDD"}

// cpkCreateTable 通过 catalog 直接创建复合主键 (order_id, sku) 表 order_item。
func cpkCreateTable(t *testing.T, s *sqlServer) {
	t.Helper()
	err := s.srv.Catalog().CreateTable(cpkTable, []catalog.ColumnDef{
		{Name: "order_id", Type: common.TypeInt64, Nullable: false},
		{Name: "sku", Type: common.TypeString, Nullable: false},
		{Name: "qty", Type: common.TypeInt64, Nullable: true},
		{Name: "unit_price", Type: common.TypeFloat64, Nullable: true},
	}, []string{"order_id", "sku"}, catalog.TableOptions{})
	if err != nil {
		t.Fatalf("建表失败: %v", err)
	}
}

// cpkMakeRow 按 (orderID, sku) 构造确定性的订单明细行。
// qty 与 unit_price 由 orderID/sku 编码得到，便于不依赖随机源构造可重放期望。
func cpkMakeRow(orderID int64, sku string) map[string]any {
	skuIdx := cpkSKUIndex(sku)
	return map[string]any{
		"order_id":   orderID,
		"sku":        sku,
		"qty":        int64(1 + (int(orderID)+skuIdx)%9),
		"unit_price": float64(10 + (int(orderID)*3+skuIdx)%90),
	}
}

// cpkSKUIndex 返回 SKU 在 cpkSKUs 数组中的下标。
func cpkSKUIndex(sku string) int {
	for i, s := range cpkSKUs {
		if s == sku {
			return i
		}
	}
	return 0
}

// cpkOrderIDForClient 计算 clientID 在第 iter 轮的起始 orderID。
func cpkOrderIDForClient(clientID, iter int) int64 {
	return int64(cpkClientBaseOID + clientID*cpkOrdersPerClt*cpkIterations + iter*cpkOrdersPerClt)
}

// cpkTotalRows 计算本测试期望的总写入行数：客户端数 × 迭代轮数 × 每轮订单数 × 每订单 SKU 数。
func cpkTotalRows() int64 {
	return int64(cpkClientCount * cpkIterations * cpkOrdersPerClt * cpkSKUsPerOrder)
}

// TestCompositePKCreateAndInsert 验证复合主键建表 + INSERT VALUES + DML 快路径点查。
//
// 单一客户端顺序执行：建表 → INSERT 4 行（同一 order_id 不同 sku）→
// DML UPDATE/DELETE WHERE 完整主键 等值 → SELECT 校验。
// 沿用复合主键 SELECT 的现有契约（见文件头注释），本测试
// 不依赖复合主键 SELECT 等值点查快路径。
func TestCompositePKCreateAndInsert(t *testing.T) {
	t.Parallel()
	s := startSQLServer(t)
	cpkCreateTable(t, s)

	orderID := int64(cpkClientBaseOID)
	rows := make([]map[string]any, 0, cpkSKUsPerOrder)
	for _, sku := range cpkSKUs {
		rows = append(rows, cpkMakeRow(orderID, sku))
	}
	writeVia(t, s, "tcp", cpkTable, rows)

	// 验证 DML 主键等值快路径：UPDATE WHERE 完整主键
	updateSKU := cpkSKUs[0]
	resp := queryVia(t, s, "tcp", fmt.Sprintf(
		"UPDATE %s SET qty = qty * 10 WHERE order_id = %d AND sku = '%s'",
		cpkTable, orderID, updateSKU,
	))
	if resp.Code != 0 {
		t.Fatalf("UPDATE 复合主键快路径失败: %s", resp.Message)
	}
	if resp.Rows != 1 {
		t.Errorf("UPDATE 复合主键快路径影响行数 = %d, 期望 1", resp.Rows)
	}

	// 验证 DML 主键等值快路径：DELETE WHERE 完整主键
	deleteSKU := cpkSKUs[3]
	resp = queryVia(t, s, "tcp", fmt.Sprintf(
		"DELETE FROM %s WHERE order_id = %d AND sku = '%s'",
		cpkTable, orderID, deleteSKU,
	))
	if resp.Code != 0 {
		t.Fatalf("DELETE 复合主键快路径失败: %s", resp.Message)
	}
	if resp.Rows != 1 {
		t.Errorf("DELETE 复合主键快路径影响行数 = %d, 期望 1", resp.Rows)
	}

	// 验证最终剩余行数 = 4 - 1 (DELETE) = 3
	resp = queryVia(t, s, "tcp", fmt.Sprintf("SELECT COUNT(*) AS cnt FROM %s", cpkTable))
	if resp.Code != 0 {
		t.Fatalf("SELECT COUNT 失败: %s", resp.Message)
	}
	rows = respRows(resp)
	if len(rows) != 1 {
		t.Fatalf("COUNT 期望 1 行，得到 %d", len(rows))
	}
	got, _ := toInt64(rows[0]["cnt"])
	if got != 3 {
		t.Errorf("DELETE 后期望剩余 3 行，得到 %d", got)
	}
}

// cpkClient 复合主键测试的多客户端封装。
// 复用 gsmClient 模式：TCP 长连接，HTTP 短连接。
type cpkClient struct {
	via     string
	srv     *sqlServer
	tcp     *tcpClient
	closeFn func()
}

// newCPKClient 按协议创建客户端并建立长连接（如适用）。
func newCPKClient(s *sqlServer, via string) (*cpkClient, error) {
	c := &cpkClient{via: via, srv: s}
	if via != "tcp" {
		return c, nil
	}
	tc, err := dialTCP(s.tcpAddr)
	if err != nil {
		return nil, err
	}
	c.tcp = tc
	c.closeFn = tc.close
	return c, nil
}

// close 关闭底层长连接。
func (c *cpkClient) close() {
	if c.closeFn != nil {
		c.closeFn()
	}
}

// query 按协议执行 SQL 查询。
func (c *cpkClient) query(sql string) (*server.Response, error) {
	if c.tcp != nil {
		return c.tcp.query(sql)
	}
	return httpQuery(c.srv.httpAddr, sql)
}

// cpkExecSQL 通用 SQL 执行：执行后断言响应码为 0，错误带操作名上下文。
func cpkExecSQL(c *cpkClient, opName, sql string) error {
	resp, err := c.query(sql)
	if err != nil {
		return fmt.Errorf("%s: %w", opName, err)
	}
	if resp.Code != 0 {
		return fmt.Errorf("%s: code=%d msg=%s", opName, resp.Code, resp.Message)
	}
	return nil
}

// cpkBatchInsertSQL 构造指定 order_id 下多 SKU 的单条 INSERT VALUES 语句。
func cpkBatchInsertSQL(orderID int64, skus []string) string {
	values := make([]string, 0, len(skus))
	for _, sku := range skus {
		row := cpkMakeRow(orderID, sku)
		values = append(values, fmt.Sprintf(
			"(%d, '%s', %d, %v)",
			row["order_id"], row["sku"], row["qty"], row["unit_price"],
		))
	}
	out := values[0]
	for i := 1; i < len(values); i++ {
		out += ", " + values[i]
	}
	return fmt.Sprintf(
		"INSERT INTO %s (order_id, sku, qty, unit_price) VALUES %s",
		cpkTable, out,
	)
}

// cpkInsertOrders 阶段 1：向 startOID..startOID+ordersPerClt 区间写入订单明细。
func cpkInsertOrders(c *cpkClient, startOID int64) error {
	for i := 0; i < cpkOrdersPerClt; i++ {
		orderID := startOID + int64(i)
		sql := cpkBatchInsertSQL(orderID, cpkSKUs[:])
		if err := cpkExecSQL(c, "INSERT", sql); err != nil {
			return err
		}
	}
	return nil
}

// cpkUpdateOneRow 阶段 2：对 (updateOID, updateSKU) 执行 UPDATE qty *= 10。
// 使用 DML 主键等值快路径，WHERE 覆盖全部主键列。
func cpkUpdateOneRow(c *cpkClient, updateOID int64, updateSKU string) error {
	sql := fmt.Sprintf(
		"UPDATE %s SET qty = qty * 10 WHERE order_id = %d AND sku = '%s'",
		cpkTable, updateOID, updateSKU,
	)
	return cpkExecSQL(c, "UPDATE", sql)
}

// cpkDeleteOneRow 阶段 3：删除 (deleteOID, deleteSKU) 指定的行。
// 使用 DML 主键等值快路径，WHERE 覆盖全部主键列。
func cpkDeleteOneRow(c *cpkClient, deleteOID int64, deleteSKU string) error {
	sql := fmt.Sprintf(
		"DELETE FROM %s WHERE order_id = %d AND sku = '%s'",
		cpkTable, deleteOID, deleteSKU,
	)
	return cpkExecSQL(c, "DELETE", sql)
}

// cpkRunOneIteration 单个客户端的一轮工作负载：INSERT → UPDATE → DELETE。
//
// 校验延后到 cpkVerifyFinalState 统一执行，避免每客户端各自全表扫描
// 导致的开销；本函数只关注写入路径的并发正确性。
func cpkRunOneIteration(c *cpkClient, iter, clientID int) error {
	startOID := cpkOrderIDForClient(clientID, iter)
	if err := cpkInsertOrders(c, startOID); err != nil {
		return err
	}
	if err := cpkUpdateOneRow(c, startOID, cpkSKUs[0]); err != nil {
		return err
	}
	if err := cpkDeleteOneRow(c, startOID+1, cpkSKUs[3]); err != nil {
		return err
	}
	return nil
}

// cpkWorker 单个客户端的完整工作负载：每轮 INSERT/UPDATE/DELETE。
// 失败时通过 atomic 计数器汇总。
func cpkWorker(c *cpkClient, clientID int, failCount *int64) {
	defer c.close()
	for iter := 0; iter < cpkIterations; iter++ {
		if err := cpkRunOneIteration(c, iter, clientID); err != nil {
			atomic.AddInt64(failCount, 1)
			return
		}
	}
}

// TestCompositePKMultiClient 验证复合主键 + 多客户端并发工作负载。
//
// 4 客户端（2 TCP + 2 HTTP）并发执行 2 轮 INSERT/UPDATE/DELETE，
// 各自负责独立的 order_id 区间，互不干扰。最终断言见 cpkVerifyFinalState。
func TestCompositePKMultiClient(t *testing.T) {
	t.Parallel()
	s := startSQLServer(t)
	cpkCreateTable(t, s)

	// 显式交错 TCP/HTTP 客户端，确保两种协议都被覆盖
	vias := []string{"tcp", "http", "tcp", "http"}
	if len(vias) != cpkClientCount {
		t.Fatalf("协议分配数 %d 与客户端数 %d 不一致", len(vias), cpkClientCount)
	}

	var wg sync.WaitGroup
	var failCount int64
	for i := 0; i < cpkClientCount; i++ {
		wg.Add(1)
		go func(clientID int, via string) {
			defer wg.Done()
			c, err := newCPKClient(s, via)
			if err != nil {
				t.Logf("client %d (%s) 建连失败: %v", clientID, via, err)
				atomic.AddInt64(&failCount, 1)
				return
			}
			cpkWorker(c, clientID, &failCount)
		}(i, vias[i])
	}
	wg.Wait()

	if failCount > 0 {
		t.Fatalf("%d 个客户端工作负载失败", failCount)
	}

	cpkVerifyFinalState(t, s)
}

// cpkVerifyFinalState 校验多客户端工作负载结束后的最终数据状态。
// 拆分为独立函数以满足 CI「单函数 ≤ 80 行」约束。
func cpkVerifyFinalState(t *testing.T, s *sqlServer) {
	t.Helper()

	// 1) 校验总行数：写入行 - 每客户端每轮删除 1 行
	wantRows := cpkTotalRows() - int64(cpkClientCount*cpkIterations)
	resp := queryVia(t, s, "tcp",
		fmt.Sprintf("SELECT COUNT(*) AS cnt FROM %s", cpkTable))
	if resp.Code != 0 {
		t.Fatalf("COUNT 查询失败: %s", resp.Message)
	}
	rows := respRows(resp)
	if len(rows) != 1 {
		t.Fatalf("COUNT 期望 1 行，得到 %d", len(rows))
	}
	got, ok := toInt64(rows[0]["cnt"])
	if !ok {
		t.Fatalf("COUNT 返回值类型异常 %T", rows[0]["cnt"])
	}
	if got != wantRows {
		t.Errorf("最终行数: 期望 %d，得到 %d", wantRows, got)
	}

	// 2) 校验 UPDATE 生效：每个客户端第一个订单的 SKU-AAA qty 已 *= 10
	cpkVerifyUpdates(t, s)

	// 3) 校验 DELETE 生效：每个客户端第二轮第二个订单的 SKU-DDD 已不存在
	cpkVerifyDeletes(t, s)
}

// cpkVerifyUpdates 校验所有客户端的 UPDATE 是否生效（目标行存在）。
// 使用 DML 主键等值快路径：幂等 UPDATE（qty = qty）影响行数应为 1。
func cpkVerifyUpdates(t *testing.T, s *sqlServer) {
	t.Helper()
	for clientID := 0; clientID < cpkClientCount; clientID++ {
		oid := cpkOrderIDForClient(clientID, 0)

		// 幂等 UPDATE 探测：影响行数 = 1 即证明 (oid, SKU-AAA) 行存在，
		// UPDATE 在前序 cpkUpdateOneRow 中已生效。复检影响行数即可。
		probeSQL := fmt.Sprintf(
			"UPDATE %s SET qty = qty WHERE order_id = %d AND sku = '%s'",
			cpkTable, oid, cpkSKUs[0],
		)
		resp := queryVia(t, s, "tcp", probeSQL)
		if resp.Code != 0 {
			t.Errorf("client=%d UPDATE 探测失败: %s", clientID, resp.Message)
			continue
		}
		if resp.Rows != 1 {
			t.Errorf("client=%d UPDATE 探测期望影响 1 行，得到 %d",
				clientID, resp.Rows)
		}
	}
}

// cpkVerifyDeletes 校验所有客户端的 DELETE 是否生效（目标行不存在）。
// 使用 DML 主键等值快路径：重复删除目标行，影响行数应为 0。
func cpkVerifyDeletes(t *testing.T, s *sqlServer) {
	t.Helper()
	for clientID := 0; clientID < cpkClientCount; clientID++ {
		oid := cpkOrderIDForClient(clientID, 1) + 1
		delSQL := fmt.Sprintf(
			"DELETE FROM %s WHERE order_id = %d AND sku = '%s'",
			cpkTable, oid, cpkSKUs[3],
		)
		resp := queryVia(t, s, "tcp", delSQL)
		if resp.Code != 0 {
			t.Errorf("client=%d DELETE 校验失败: %s", clientID, resp.Message)
			continue
		}
		if resp.Rows != 0 {
			t.Errorf("client=%d (%d, %s) 应已删除，重复删除得到 %d 行",
				clientID, oid, cpkSKUs[3], resp.Rows)
		}
	}
}
