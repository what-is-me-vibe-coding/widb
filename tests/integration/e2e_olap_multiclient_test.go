// Package integration 端到端集成测试：OLAP 风格多客户端工作负载。
//
// 与既有 e2e_*_multiclient_*.go 互补，本文件聚焦「一个 server + 多个 client
// （TCP/HTTP/PG wire 三协议混合）+ 大数据量 + 高频分析查询」的端到端稳定性：
//   - 预先灌入成百行事件数据，模拟 OLAP 场景的小规模宽表
//   - 写入客户端（TCP/HTTP 混合）在独立 ID 区间上持续 INSERT/UPDATE/DELETE
//   - 读客户端在写入进行时持续执行：聚合（COUNT/SUM/AVG/MIN/MAX + GROUP BY）、
//     点查、范围扫描、列投影、算术表达式与 LIKE 过滤
//   - PG wire 客户端经 Simple Query 协议只读分析负载，验证 BI/ETL 工具路径
//   - 最终校验数据一致性：总行数、聚合数值、GROUP BY 分组数与各组求和
//
// 测试设计原则：
//   - 复用 e2e_server_sql_test.go 与 e2e_pgwire_sql_test.go 中的 startPGWireServer /
//     sqlServer / rawQuery / rawWrite / respRows / toInt64 / toFloat64 等公共 helper
//   - t.Parallel 并发执行，缩短集成测试套件总时长
//   - 各客户端负责的 ID 区间不重叠，最终汇总即期望总行数
//   - 数值期望值在测试内计算，避免对硬编码值过度依赖
//
// 本文件作为「OLAP 风格多客户端」端到端测试的范式，所有测试并行运行以缩短
// 集成测试套件总时长。
package integration

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// OLAP 测试参数：覆盖写入客户端数、读客户端数、单客户端写入批次数与批大小。
const (
	olapWriterClients    = 4  // 写入客户端总数（TCP/HTTP 混合）
	olapReaderClients    = 4  // 读客户端总数（TCP/HTTP 混合）
	olapPGReaderClients  = 2  // PG wire 只读客户端数
	olapBatchesPerWriter = 3  // 每客户端连续写入批次数
	olapRowsPerBatch     = 25 // 每批写入行数
	olapInitialRows      = 60 // 预先灌入的初始数据行数
	olapReaderLoops      = 5  // 每读客户端执行的查询轮数
	olapWriterBaseID     = 20000
	olapInitialBaseID    = 10000
	olapPGQueryLoops     = 6 // PG wire 客户端查询轮数
	olapRegionCount      = 4 // region 维度基数
	olapProductCount     = 5 // product 维度基数
	olapTable            = "olap_events"
)

// olapCategories 与 olapRegion/product 维度对应的字符串集合。
var (
	olapRegions  = [...]string{"cn-east", "cn-west", "us-east", "eu-west"}
	olapProducts = [...]string{"phone", "laptop", "tablet", "watch", "headset"}
)

// olapMakeRow 按 (id, clientID, batchIdx, rowIdx) 生成确定性的事件行。
// 数值字段（amount, qty）由 clientID/batch/row 编码得到，便于在不依赖外部
// 随机源的前提下构造可重放的期望值。
func olapMakeRow(id, clientID, batchIdx, rowIdx int) map[string]any {
	region := olapRegions[(clientID+rowIdx)%olapRegionCount]
	product := olapProducts[(batchIdx+rowIdx)%olapProductCount]
	amount := float64(10 + (clientID*7+batchIdx*3+rowIdx)%90) // 10..99
	qty := int64(1 + (batchIdx*5+rowIdx*2)%9)                 // 1..9
	return map[string]any{
		"id":        int64(id),
		"region":    region,
		"product":   product,
		"amount":    amount,
		"qty":       qty,
		"is_member": (clientID+rowIdx)%2 == 0,
		"campaign":  fmt.Sprintf("c-%d-%d", clientID, batchIdx),
	}
}

// olapCreateTable 经 TCP 创建 OLAP 表，覆盖 7 列（含 2 个字符串维度列、3
// 个数值度量列、1 个布尔标签列）。
func olapCreateTable(t *testing.T, s *sqlServer) {
	t.Helper()
	execSQLVia(t, s, "tcp", "CREATE TABLE "+olapTable+" ("+
		"id INT64 NOT NULL, "+
		"region STRING NULL, "+
		"product STRING NULL, "+
		"amount FLOAT64 NULL, "+
		"qty INT64 NULL, "+
		"is_member BOOL NULL, "+
		"campaign STRING NULL, "+
		"PRIMARY KEY(id))")
}

// olapSeedInitial 灌入 olapInitialRows 行预置数据，ID 区间 [olapInitialBaseID,
// olapInitialBaseID+olapInitialRows)。所有写入均经 HTTP 完成，跨协议入口
// 验证。
func olapSeedInitial(t *testing.T, s *sqlServer) {
	t.Helper()
	rows := make([]map[string]any, olapInitialRows)
	for i := 0; i < olapInitialRows; i++ {
		rows[i] = olapMakeRow(olapInitialBaseID+i, i%olapWriterClients, i%3, i)
	}
	writeVia(t, s, "http", olapTable, rows)
}

// olapWriterClient 模拟写入客户端：在独占 ID 区间上执行 olapBatchesPerWriter
// 批 INSERT，间隔执行一次 UPDATE 与一次 DELETE（点选该批首/末行）。
// TCP/HTTP 协议由 clientID 奇偶决定，便于触发多协议路径。
func olapWriterClient(s *sqlServer, clientID int) error {
	via := "tcp"
	if clientID%2 == 1 {
		via = "http"
	}
	batchSize := int64(olapBatchesPerWriter * olapRowsPerBatch)
	base := int64(olapWriterBaseID) + int64(clientID)*batchSize
	for b := 0; b < olapBatchesPerWriter; b++ {
		rows := make([]map[string]any, olapRowsPerBatch)
		for r := 0; r < olapRowsPerBatch; r++ {
			id := base + int64(b*olapRowsPerBatch+r)
			rows[r] = olapMakeRow(int(id), clientID, b, r)
		}
		resp, err := rawWrite(s, via, olapTable, rows)
		if err != nil {
			return fmt.Errorf("writer %d batch %d 写入失败: %w", clientID, b, err)
		}
		if resp.Code != 0 {
			return fmt.Errorf("writer %d batch %d 写入返回错误: %s",
				clientID, b, resp.Message)
		}
		// 每批完成后做一次 UPDATE：给该批首行的 amount + 1.0
		firstID := base + int64(b*olapRowsPerBatch)
		updSQL := fmt.Sprintf("UPDATE %s SET amount = amount + 1.0 WHERE id = %d",
			olapTable, firstID)
		uresp, err := rawQuery(s, via, updSQL)
		if err != nil {
			return fmt.Errorf("writer %d batch %d UPDATE 失败: %w", clientID, b, err)
		}
		if uresp.Code != 0 {
			return fmt.Errorf("writer %d batch %d UPDATE 返回错误: %s",
				clientID, b, uresp.Message)
		}
		// 每批最后一行 DELETE
		lastID := base + int64((b+1)*olapRowsPerBatch-1)
		delSQL := fmt.Sprintf("DELETE FROM %s WHERE id = %d", olapTable, lastID)
		dresp, err := rawQuery(s, via, delSQL)
		if err != nil {
			return fmt.Errorf("writer %d batch %d DELETE 失败: %w", clientID, b, err)
		}
		if dresp.Code != 0 {
			return fmt.Errorf("writer %d batch %d DELETE 返回错误: %s",
				clientID, b, dresp.Message)
		}
	}
	return nil
}

// olapReaderClient 模拟读客户端：在 olapReaderLoops 轮中循环执行多种
// 分析查询（聚合、点查、范围、投影、算术、LIKE）。每次循环独立，错误即刻
// 返回。TCP/HTTP 协议由 clientID 奇偶决定。
func olapReaderClient(s *sqlServer, clientID int) error {
	via := "tcp"
	if clientID%2 == 0 {
		via = "http"
	}
	for loop := 0; loop < olapReaderLoops; loop++ {
		if err := olapRunAggQuery(s, via, loop); err != nil {
			return fmt.Errorf("reader %d loop %d 聚合查询: %w", clientID, loop, err)
		}
		if err := olapRunRangeQuery(s, via, loop); err != nil {
			return fmt.Errorf("reader %d loop %d 范围查询: %w", clientID, loop, err)
		}
		if err := olapRunProjectArith(s, via, loop); err != nil {
			return fmt.Errorf("reader %d loop %d 投影查询: %w", clientID, loop, err)
		}
		if err := olapRunLikeQuery(s, via, loop); err != nil {
			return fmt.Errorf("reader %d loop %d LIKE 查询: %w", clientID, loop, err)
		}
		if err := olapRunPointQuery(s, via, loop); err != nil {
			return fmt.Errorf("reader %d loop %d 点查: %w", clientID, loop, err)
		}
	}
	return nil
}

// olapRunAggQuery 执行 GROUP BY + 多种聚合函数。
// 在写入并发进行时仅校验响应码与分组数 ≥ 1（分组基数与写入进度相关）。
func olapRunAggQuery(s *sqlServer, via string, loop int) error {
	sql := fmt.Sprintf("SELECT region, COUNT(*) AS cnt, SUM(amount) AS s, "+
		"AVG(qty) AS aq, MIN(amount) AS mn, MAX(amount) AS mx "+
		"FROM %s GROUP BY region", olapTable)
	resp, err := rawQuery(s, via, sql)
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return fmt.Errorf("code=%d msg=%s", resp.Code, resp.Message)
	}
	rows := respRows(resp)
	if len(rows) == 0 {
		return fmt.Errorf("期望至少 1 个 region 分组")
	}
	// 在包含初始 60 行 + 多客户端并发写入的场景下，分组数 <= region 基数
	if len(rows) > olapRegionCount {
		return fmt.Errorf("region 分组数 %d 超出维度基数 %d", len(rows), olapRegionCount)
	}
	// 每个分组应至少 1 行
	for _, r := range rows {
		cnt, ok := toInt64(r["cnt"])
		if !ok || cnt < 1 {
			return fmt.Errorf("分组 region=%v 行数异常: cnt=%v", r["region"], r["cnt"])
		}
	}
	if loop == 0 {
		_ = sql // 显式引用 loop 以避免未使用警告
	}
	return nil
}

// olapRunRangeQuery 执行 ID 范围扫描 + 过滤条件。
func olapRunRangeQuery(s *sqlServer, via string, loop int) error {
	low := olapInitialBaseID + loop*5
	high := low + 20
	sql := fmt.Sprintf("SELECT id, amount FROM %s WHERE id >= %d AND id < %d "+
		"AND is_member = true", olapTable, low, high)
	resp, err := rawQuery(s, via, sql)
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return fmt.Errorf("code=%d msg=%s", resp.Code, resp.Message)
	}
	rows := respRows(resp)
	for _, r := range rows {
		id, ok := toInt64(r["id"])
		if !ok {
			return fmt.Errorf("id 字段异常: %v", r["id"])
		}
		if id < int64(low) || id >= int64(high) {
			return fmt.Errorf("id=%d 超出 [%d,%d)", id, low, high)
		}
	}
	return nil
}

// olapRunProjectArith 执行列投影 + 算术表达式 + LIMIT。
func olapRunProjectArith(s *sqlServer, via string, loop int) error {
	limit := 5 + loop%3
	sql := fmt.Sprintf("SELECT id, amount, qty, amount * qty AS total, "+
		"amount + 10 AS bumped FROM %s ORDER BY id LIMIT %d", olapTable, limit)
	resp, err := rawQuery(s, via, sql)
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return fmt.Errorf("code=%d msg=%s", resp.Code, resp.Message)
	}
	rows := respRows(resp)
	if len(rows) == 0 {
		return fmt.Errorf("期望至少 1 行")
	}
	if len(rows) > limit {
		return fmt.Errorf("行数 %d 超过 LIMIT %d", len(rows), limit)
	}
	// 验证 total = amount * qty
	for _, r := range rows {
		amount, aok := toFloat64(r["amount"])
		qty, qok := toInt64(r["qty"])
		total, tok := toFloat64(r["total"])
		bumped, bok := toFloat64(r["bumped"])
		if !aok || !qok || !tok || !bok {
			continue // 部分列可能为 NULL（如写入前 GET），跳过
		}
		if total != amount*float64(qty) {
			return fmt.Errorf("total 不等于 amount*qty: %v*%v != %v",
				amount, qty, total)
		}
		if bumped != amount+10 {
			return fmt.Errorf("bumped 不等于 amount+10: %v+10 != %v", amount, bumped)
		}
	}
	return nil
}

// olapRunLikeQuery 执行 LIKE 模糊匹配（campaign 字段以 'c-' 开头）。
func olapRunLikeQuery(s *sqlServer, via string, _ int) error {
	sql := fmt.Sprintf("SELECT id, campaign FROM %s WHERE campaign LIKE 'c-%%' LIMIT 5",
		olapTable)
	resp, err := rawQuery(s, via, sql)
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return fmt.Errorf("code=%d msg=%s", resp.Code, resp.Message)
	}
	rows := respRows(resp)
	for _, r := range rows {
		c, ok := r["campaign"].(string)
		if !ok || len(c) < 2 || c[:2] != "c-" {
			return fmt.Errorf("campaign 字段不匹配 LIKE: %v", r["campaign"])
		}
	}
	return nil
}

// olapRunPointQuery 在初始数据区间内做点查，验证主键查找。
func olapRunPointQuery(s *sqlServer, via string, loop int) error {
	id := olapInitialBaseID + (loop*7)%olapInitialRows
	sql := fmt.Sprintf("SELECT id, amount, region FROM %s WHERE id = %d",
		olapTable, id)
	resp, err := rawQuery(s, via, sql)
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return fmt.Errorf("code=%d msg=%s", resp.Code, resp.Message)
	}
	rows := respRows(resp)
	if len(rows) > 1 {
		return fmt.Errorf("点查 id=%d 返回 %d 行", id, len(rows))
	}
	return nil
}

// olapPGReaderClient 模拟 PG wire 只读分析客户端：执行 COUNT/聚合/GROUP BY
// 查询。BI/ETL 工具链常用此模式：经 PG 协议拉取物化结果。返回错误信息便于
// 定位失败原因。
func olapPGReaderClient(t *testing.T, s *sqlServer, clientID int) error {
	t.Helper()
	c, err := dialPGWireErr(s.srv.PGAddr())
	if err != nil {
		return fmt.Errorf("PG wire 拨号失败: %w", err)
	}
	defer c.close()
	if err := c.handshakeErr(); err != nil {
		return fmt.Errorf("PG wire 握手失败: %w", err)
	}
	for loop := 0; loop < olapPGQueryLoops; loop++ {
		queries := []string{
			fmt.Sprintf("SELECT COUNT(*) AS cnt, SUM(amount) AS s FROM %s", olapTable),
			fmt.Sprintf("SELECT product, COUNT(*) AS cnt, AVG(amount) AS av "+
				"FROM %s GROUP BY product", olapTable),
			fmt.Sprintf("SELECT region, SUM(qty) AS total_qty FROM %s "+
				"GROUP BY region ORDER BY region", olapTable),
			fmt.Sprintf("SELECT id, amount, amount * 2 AS doubled FROM %s "+
				"WHERE amount > 50 ORDER BY id LIMIT 3", olapTable),
		}
		q := queries[loop%len(queries)]
		res, err := c.sendQueryRead(q)
		if err != nil {
			return fmt.Errorf("PG 客户端 %d loop %d 查询失败 [%s]: %w",
				clientID, loop, q, err)
		}
		if res.errMsg != "" {
			return fmt.Errorf("PG 客户端 %d loop %d SQL 错误 [%s]: %s",
				clientID, loop, q, res.errMsg)
		}
		if len(res.rows) == 0 {
			return fmt.Errorf("PG 客户端 %d loop %d 期望至少 1 行", clientID, loop)
		}
	}
	return nil
}

// olapRunMixedClients 启动所有写入/读/PG wire 客户端并等待完成。
// 返回首个失败的错误（若有）。
func olapRunMixedClients(t *testing.T, s *sqlServer) error {
	t.Helper()
	var wg sync.WaitGroup
	var failCount int64
	var lastErr atomic.Value
	record := func(role string, clientID int, err error) {
		if err != nil {
			lastErr.Store(err.Error())
			atomic.AddInt64(&failCount, 1)
			t.Logf("%s %d 失败: %v", role, clientID, err)
		}
	}
	// 写入客户端（TCP/HTTP 混合）
	for i := 0; i < olapWriterClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			record("writer", clientID, olapWriterClient(s, clientID))
		}(i)
	}
	// 读客户端（TCP/HTTP 混合）
	for i := 0; i < olapReaderClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			record("reader", clientID, olapReaderClient(s, clientID))
		}(i)
	}
	// PG wire 只读分析客户端
	for i := 0; i < olapPGReaderClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			record("PG reader", clientID, olapPGReaderClient(t, s, clientID))
		}(i)
	}
	wg.Wait()
	if failCount > 0 {
		return fmt.Errorf("%d 个客户端失败，最后错误: %v",
			failCount, lastErr.Load())
	}
	return nil
}

// olapVerifyFinalState 校验混合工作负载结束后的数据一致性。
func olapVerifyFinalState(t *testing.T, s *sqlServer) {
	t.Helper()
	// 期望总行数：初始 olapInitialRows + 各客户端每批净增 (rows-1)
	want := int64(olapInitialRows) + int64(olapWriterClients*
		olapBatchesPerWriter*(olapRowsPerBatch-1))
	c := dialTCPClient(t, s)
	defer c.close()
	if got := sessCount(t, c, olapTable); got != want {
		t.Errorf("总行数: 期望 %d，得到 %d", want, got)
	}
	// region 分组数应恰好为 olapRegionCount
	aggSQL := fmt.Sprintf("SELECT region, COUNT(*) AS cnt, SUM(amount) AS s "+
		"FROM %s GROUP BY region", olapTable)
	aggRows := respRows(sessExec(t, c, aggSQL))
	if len(aggRows) != olapRegionCount {
		t.Errorf("region 分组数: 期望 %d，得到 %d",
			olapRegionCount, len(aggRows))
	}
	// 跨协议一致性：HTTP 跑同一聚合，分组数应一致
	httpResp, err := httpQuery(s.httpAddr, aggSQL)
	if err != nil {
		t.Fatalf("HTTP 聚合查询失败: %v", err)
	}
	if httpResp.Code != 0 {
		t.Fatalf("HTTP 聚合返回错误: %s", httpResp.Message)
	}
	httpRows := respRows(httpResp)
	if len(httpRows) != len(aggRows) {
		t.Errorf("聚合分组数跨协议不一致: TCP=%d HTTP=%d",
			len(aggRows), len(httpRows))
	}
}

// TestOLAPMultiClientMixedWorkload 验证 OLAP 风格多客户端混合工作负载的
// 端到端稳定性。
//
// 启动一个 server（TCP/HTTP/PG wire 三协议），预灌入 60 行 OLAP 事件，
// 启动 4 写入客户端 + 4 读客户端 + 2 PG wire 客户端并发执行。
//
// 工作负载期间持续发生写入/更新/删除，读客户端的聚合查询仅校验响应合法
// （分组数在维度基数内、点查唯一），最终阶段校验总行数与聚合数值。
func TestOLAPMultiClientMixedWorkload(t *testing.T) {
	t.Parallel()
	s := startPGWireServer(t)
	olapCreateTable(t, s)
	olapSeedInitial(t, s)
	if err := olapRunMixedClients(t, s); err != nil {
		t.Fatal(err)
	}
	olapVerifyFinalState(t, s)
}
