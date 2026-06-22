package pgwire

import (
	"errors"
	"testing"
)

// runExtendedQuery 发送一个完整的 extended query 周期（Parse+Bind+Describe+Execute+Sync）
// 并返回响应消息类型序列与最终的 ReadyForQuery 状态。
// 假定已与服务器完成 Startup 握手。
func runExtendedQuery(t *testing.T, c *pgClient, sql string) []byte {
	t.Helper()
	if err := c.sendParse("", sql, nil); err != nil {
		t.Fatalf("sendParse 失败: %v", err)
	}
	if err := c.sendBind("", "", nil, []int16{0, 0}); err != nil {
		t.Fatalf("sendBind 失败: %v", err)
	}
	if err := c.sendDescribe('P', ""); err != nil {
		t.Fatalf("sendDescribe 失败: %v", err)
	}
	if err := c.sendExecute("", 0); err != nil {
		t.Fatalf("sendExecute 失败: %v", err)
	}
	if err := c.sendSync(); err != nil {
		t.Fatalf("sendSync 失败: %v", err)
	}
	types, err := c.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	return types
}

// TestExtendedQuerySelect 验证 extended query 协议下 SELECT 正常返回数据。
// 修复 issue #234：此前服务端忽略 Parse/Bind/Describe/Execute，导致客户端
// （如 DBeaver/pgAdmin/Navicat 默认走 extended query）"查询没有传回任何结果"。
func TestExtendedQuerySelect(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns: []string{"id", "name"},
		Rows: []map[string]any{
			{"id": int64(1), "name": "alice"},
			{"id": int64(2), "name": "bob"},
		},
		IsQuery: true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	c := newPGClient(t, srv.Addr())
	defer c.close()
	if err := c.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := c.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	types := runExtendedQuery(t, c, "SELECT id, name FROM t")
	// 期望序列: ParseComplete(1) + BindComplete(2) + NoData(n)
	// + RowDescription(T) + 2*DataRow(D) + CommandComplete(C) + ReadyForQuery(Z)
	if len(types) < 7 {
		t.Fatalf("消息过少, got %v", types)
	}
	if types[0] != '1' {
		t.Errorf("期望 ParseComplete('1') 开头, got %c", types[0])
	}
	if types[1] != '2' {
		t.Errorf("期望 BindComplete('2') 第二个, got %c", types[1])
	}
	if types[2] != 'n' {
		t.Errorf("期望 NoData('n') 第三个, got %c", types[2])
	}
	if types[3] != 'T' {
		t.Errorf("期望 RowDescription('T') 第四个, got %c", types[3])
	}
	if types[4] != 'D' || types[5] != 'D' {
		t.Errorf("期望 2 个 DataRow, got %v", types[4:6])
	}
	if types[6] != 'C' {
		t.Errorf("期望 CommandComplete('C'), got %c", types[6])
	}
	if types[len(types)-1] != 'Z' {
		t.Errorf("应以 ReadyForQuery('Z') 结尾, got %c", types[len(types)-1])
	}
	// executor 应收到一次 ExecuteSQL 调用
	if got := exec.lastQuery(); got != "SELECT id, name FROM t" {
		t.Errorf("executor 收到 %q, 期望 %q", got, "SELECT id, name FROM t")
	}
}

// TestExtendedQueryShowTables 验证 extended query 协议下 SHOW TABLES 返回结果。
// 复现 issue #234 中 "show tables" 工作但 "select" 不工作的差异。
func TestExtendedQueryShowTables(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns: []string{"table"},
		Rows:    []map[string]any{{"table": "t"}},
		IsQuery: true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	c := newPGClient(t, srv.Addr())
	defer c.close()
	if err := c.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := c.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	types := runExtendedQuery(t, c, "show tables")
	// 期望包含 RowDescription + DataRow + CommandComplete + ReadyForQuery
	hasT, hasD, hasC := false, false, false
	for _, m := range types {
		switch m {
		case 'T':
			hasT = true
		case 'D':
			hasD = true
		case 'C':
			hasC = true
		}
	}
	if !hasT {
		t.Errorf("应包含 RowDescription('T'), got %v", types)
	}
	if !hasD {
		t.Errorf("应包含 DataRow('D'), got %v", types)
	}
	if !hasC {
		t.Errorf("应包含 CommandComplete('C'), got %v", types)
	}
}

// TestExtendedQueryNonQuery 验证 extended query 协议下 INSERT 等非查询语句不发送 RowDescription。
func TestExtendedQueryNonQuery(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{RowsAffected: 1, CommandTag: "INSERT 0 1"}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	c := newPGClient(t, srv.Addr())
	defer c.close()
	if err := c.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := c.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	types := runExtendedQuery(t, c, "INSERT INTO t VALUES (1, 'x')")
	// 期望: 1+2+n + CommandComplete(C) + ReadyForQuery(Z)，不应有 T/D
	for _, m := range types {
		if m == 'T' {
			t.Errorf("INSERT 不应发送 RowDescription, got %v", types)
		}
		if m == 'D' {
			t.Errorf("INSERT 不应发送 DataRow, got %v", types)
		}
	}
	hasC := false
	for _, m := range types {
		if m == 'C' {
			hasC = true
		}
	}
	if !hasC {
		t.Errorf("应包含 CommandComplete, got %v", types)
	}
}

// TestExtendedQueryExecutorError 验证 extended query 协议下执行错误返回 ErrorResponse。
// 重要：Execute 错误不应污染 extended query 错误状态（即后续消息仍能处理）。
func TestExtendedQueryExecutorError(t *testing.T) {
	exec := &mockExecutor{err: errors.New("syntax error at or near FOO")}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	c := newPGClient(t, srv.Addr())
	defer c.close()
	if err := c.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := c.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	types := runExtendedQuery(t, c, "BAD SQL")
	hasE := false
	for _, m := range types {
		if m == 'E' {
			hasE = true
		}
	}
	if !hasE {
		t.Errorf("应包含 ErrorResponse('E'), got %v", types)
	}
	// 应以 ReadyForQuery 结尾
	if types[len(types)-1] != 'Z' {
		t.Errorf("应以 ReadyForQuery 结尾, got %c", types[len(types)-1])
	}
}

// TestExtendedQueryBindMissingStmt 验证 Bind 引用不存在的 prepared statement 时进入错误状态。
// 错误状态会在下次 Sync 时清除。
func TestExtendedQueryBindMissingStmt(t *testing.T) {
	srv := startTestServer(t, &mockExecutor{})
	defer srv.Stop()

	c := newPGClient(t, srv.Addr())
	defer c.close()
	if err := c.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := c.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	// Bind 不存在的 statement
	if err := c.sendBind("", "no_such_stmt", nil, nil); err != nil {
		t.Fatalf("sendBind 失败: %v", err)
	}
	if err := c.sendSync(); err != nil {
		t.Fatalf("sendSync 失败: %v", err)
	}
	types, err := c.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	hasE := false
	for _, m := range types {
		if m == 'E' {
			hasE = true
		}
	}
	if !hasE {
		t.Errorf("Bind 未知 statement 应返回 ErrorResponse, got %v", types)
	}
	if types[len(types)-1] != 'Z' {
		t.Errorf("应以 ReadyForQuery 结尾, got %c", types[len(types)-1])
	}
}

// TestExtendedQueryDescribeMissingPortal 验证 Describe(P) 引用不存在的 portal 返回错误。
func TestExtendedQueryDescribeMissingPortal(t *testing.T) {
	srv := startTestServer(t, &mockExecutor{})
	defer srv.Stop()

	c := newPGClient(t, srv.Addr())
	defer c.close()
	if err := c.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := c.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	if err := c.sendDescribe('P', "no_such_portal"); err != nil {
		t.Fatalf("sendDescribe 失败: %v", err)
	}
	if err := c.sendSync(); err != nil {
		t.Fatalf("sendSync 失败: %v", err)
	}
	types, err := c.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	hasE := false
	for _, m := range types {
		if m == 'E' {
			hasE = true
		}
	}
	if !hasE {
		t.Errorf("Describe 未知 portal 应返回 ErrorResponse, got %v", types)
	}
}

// TestExtendedQueryDescribeStatement 验证 Describe(S) 返回 ParameterDescription + NoData。
func TestExtendedQueryDescribeStatement(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	c := newPGClient(t, srv.Addr())
	defer c.close()
	if err := c.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := c.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	// Parse + Describe(S) + Sync
	if err := c.sendParse("", "SELECT $1", nil); err != nil {
		t.Fatalf("sendParse 失败: %v", err)
	}
	if err := c.sendDescribe('S', ""); err != nil {
		t.Fatalf("sendDescribe 失败: %v", err)
	}
	if err := c.sendSync(); err != nil {
		t.Fatalf("sendSync 失败: %v", err)
	}
	types, err := c.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	// 期望: ParseComplete(1) + ParameterDescription(t) + NoData(n) + ReadyForQuery(Z)
	if types[0] != '1' {
		t.Errorf("期望 ParseComplete, got %c", types[0])
	}
	if types[1] != 't' {
		t.Errorf("期望 ParameterDescription, got %c", types[1])
	}
	if types[2] != 'n' {
		t.Errorf("期望 NoData, got %c", types[2])
	}
	if types[len(types)-1] != 'Z' {
		t.Errorf("应以 ReadyForQuery 结尾, got %c", types[len(types)-1])
	}
}

// TestExtendedQueryClose 验证 Close 消息返回 CloseComplete 并删除对应对象。
func TestExtendedQueryClose(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns: []string{"x"},
		Rows:    []map[string]any{{"x": int64(1)}},
		IsQuery: true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	c := newPGClient(t, srv.Addr())
	defer c.close()
	if err := c.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := c.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	// Parse + Bind + Close(stmt) + Close(portal) + Sync
	if err := c.sendParse("", "SELECT 1", nil); err != nil {
		t.Fatalf("sendParse 失败: %v", err)
	}
	if err := c.sendBind("", "", nil, nil); err != nil {
		t.Fatalf("sendBind 失败: %v", err)
	}
	if err := c.sendClose('S', ""); err != nil {
		t.Fatalf("sendClose 失败: %v", err)
	}
	if err := c.sendClose('P', ""); err != nil {
		t.Fatalf("sendClose 失败: %v", err)
	}
	if err := c.sendSync(); err != nil {
		t.Fatalf("sendSync 失败: %v", err)
	}
	types, err := c.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	// 期望 1 + 2 + 3 + 3 + Z
	if len(types) < 5 {
		t.Fatalf("消息过少, got %v", types)
	}
	if types[0] != '1' || types[1] != '2' || types[2] != '3' || types[3] != '3' {
		t.Errorf("期望 ParseComplete+BindComplete+CloseComplete+CloseComplete, got %v", types[:4])
	}
	if types[4] != 'Z' {
		t.Errorf("应以 ReadyForQuery 结尾, got %c", types[4])
	}
}

// TestExtendedQueryBatch 验证多个 Parse/Bind/Execute 在一次 Sync 内全部处理。
// 真实 PG 客户端（如 DBeaver）经常以这种批量方式发送查询。
func TestExtendedQueryBatch(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns: []string{"x"},
		Rows:    []map[string]any{{"x": int64(1)}},
		IsQuery: true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	c := newPGClient(t, srv.Addr())
	defer c.close()
	if err := c.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := c.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	// 3 条语句合并到一个 Sync
	for i := 0; i < 3; i++ {
		if err := c.sendParse("", "SELECT 1", nil); err != nil {
			t.Fatalf("sendParse %d 失败: %v", i, err)
		}
		if err := c.sendBind("", "", nil, nil); err != nil {
			t.Fatalf("sendBind %d 失败: %v", i, err)
		}
		if err := c.sendDescribe('P', ""); err != nil {
			t.Fatalf("sendDescribe %d 失败: %v", i, err)
		}
		if err := c.sendExecute("", 0); err != nil {
			t.Fatalf("sendExecute %d 失败: %v", i, err)
		}
	}
	if err := c.sendSync(); err != nil {
		t.Fatalf("sendSync 失败: %v", err)
	}
	types, err := c.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	// 期望 3 组 (1+2+n+T+D+C) + Z = 18 个消息
	if len(types) != 19 {
		t.Errorf("期望 19 个消息, got %d (%v)", len(types), types)
	}
	if types[len(types)-1] != 'Z' {
		t.Errorf("应以 ReadyForQuery 结尾, got %c", types[len(types)-1])
	}
	// executor 应被调用 3 次
	if queries := exec.queryCount(); queries != 3 {
		t.Errorf("executor 应被调用 3 次, got %d", queries)
	}
}

// TestExtendedQueryExecuteMissingPortal 验证 Execute 引用不存在的 portal 返回错误并进入错误状态。
func TestExtendedQueryExecuteMissingPortal(t *testing.T) {
	srv := startTestServer(t, &mockExecutor{})
	defer srv.Stop()

	c := newPGClient(t, srv.Addr())
	defer c.close()
	if err := c.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := c.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	if err := c.sendExecute("no_such_portal", 0); err != nil {
		t.Fatalf("sendExecute 失败: %v", err)
	}
	if err := c.sendSync(); err != nil {
		t.Fatalf("sendSync 失败: %v", err)
	}
	types, err := c.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	hasE := false
	for _, m := range types {
		if m == 'E' {
			hasE = true
		}
	}
	if !hasE {
		t.Errorf("Execute 未知 portal 应返回 ErrorResponse, got %v", types)
	}
}

// TestExtendedQueryMaxRows 验证 Execute 的 maxRows 限制返回行数。
func TestExtendedQueryMaxRows(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns: []string{"x"},
		Rows: []map[string]any{
			{"x": int64(1)},
			{"x": int64(2)},
			{"x": int64(3)},
		},
		IsQuery: true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	c := newPGClient(t, srv.Addr())
	defer c.close()
	if err := c.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := c.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	if err := c.sendParse("", "SELECT x FROM t", nil); err != nil {
		t.Fatalf("sendParse 失败: %v", err)
	}
	if err := c.sendBind("", "", nil, nil); err != nil {
		t.Fatalf("sendBind 失败: %v", err)
	}
	// maxRows=2: 仅返回 2 行
	if err := c.sendExecute("", 2); err != nil {
		t.Fatalf("sendExecute 失败: %v", err)
	}
	if err := c.sendSync(); err != nil {
		t.Fatalf("sendSync 失败: %v", err)
	}
	types, err := c.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	// 应只有 1 个 DataRow（maxRows=2 限制为 2 行以内，但实际只读了 1 个 D）
	// 实际 2 行，所以应有两个 D
	dataRowCount := 0
	for _, m := range types {
		if m == 'D' {
			dataRowCount++
		}
	}
	if dataRowCount != 2 {
		t.Errorf("maxRows=2 应返回 2 行, got %d DataRow", dataRowCount)
	}
}
