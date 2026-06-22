package pgwire

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// TestConnQuerySelect 验证 SELECT 查询流程。
func TestConnQuerySelect(t *testing.T) {
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

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendQuery("SELECT * FROM t"); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	// 期望: RowDescription('T') + DataRow*('D') + CommandComplete('C') + ReadyForQuery('Z')
	types, err := client.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取查询响应失败: %v", err)
	}
	if len(types) < 4 {
		t.Fatalf("消息过少: %v", types)
	}
	if types[0] != 'T' {
		t.Errorf("第一个应为 RowDescription('T'), got %c", types[0])
	}
	// 2 个 DataRow
	if types[1] != 'D' || types[2] != 'D' {
		t.Errorf("应为 2 个 DataRow('D'), got %v", types[1:3])
	}
	if types[3] != 'C' {
		t.Errorf("应为 CommandComplete('C'), got %c", types[3])
	}
	if types[4] != 'Z' {
		t.Errorf("应为 ReadyForQuery('Z'), got %c", types[4])
	}
	// 验证 executor 收到了 SQL
	if got := exec.lastQuery(); got != "SELECT * FROM t" {
		t.Errorf("executor 收到 %q, 期望 SELECT * FROM t", got)
	}
}

// TestConnQueryError 验证查询错误返回 ErrorResponse。
func TestConnQueryError(t *testing.T) {
	exec := &mockExecutor{err: errors.New("table not found")}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendQuery("SELECT * FROM missing"); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	types, err := client.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	hasError := false
	for _, c := range types {
		if c == 'E' {
			hasError = true
		}
	}
	if !hasError {
		t.Errorf("应包含 ErrorResponse('E'), got %v", types)
	}
	if types[len(types)-1] != 'Z' {
		t.Errorf("应以 ReadyForQuery 结尾, got %c", types[len(types)-1])
	}
}

// TestConnEmptyQuery 验证空查询返回 EmptyQueryResponse。
func TestConnEmptyQuery(t *testing.T) {
	exec := &mockExecutor{}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendQuery("   "); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	types, err := client.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	if len(types) != 2 {
		t.Fatalf("期望 2 个消息, got %v", types)
	}
	if types[0] != 'I' { // EmptyQueryResponse
		t.Errorf("期望 EmptyQueryResponse('I'), got %c", types[0])
	}
	if types[1] != 'Z' {
		t.Errorf("期望 ReadyForQuery('Z'), got %c", types[1])
	}
}

// TestConnNonQueryCommand 验证非查询命令（如 INSERT）返回 CommandComplete。
func TestConnNonQueryCommand(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		RowsAffected: 3,
		CommandTag:   "INSERT 0 3",
		IsQuery:      false,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendQuery("INSERT INTO t VALUES (1)"); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	types, err := client.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	if len(types) != 2 {
		t.Fatalf("期望 2 个消息, got %v", types)
	}
	if types[0] != 'C' { // CommandComplete
		t.Errorf("期望 CommandComplete('C'), got %c", types[0])
	}
}

// TestConnMultipleQueries 验证连接可处理多个连续查询。
func TestConnMultipleQueries(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns: []string{"v"},
		Rows:    []map[string]any{{"v": int64(1)}},
		IsQuery: true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := client.sendQuery("SELECT 1"); err != nil {
			t.Fatalf("第 %d 次 sendQuery 失败: %v", i, err)
		}
		types, err := client.readUntilReadyForQuery()
		if err != nil {
			t.Fatalf("第 %d 次读取响应失败: %v", i, err)
		}
		if len(types) < 3 {
			t.Errorf("第 %d 次响应消息过少: %v", i, types)
		}
	}
}

// TestConnQueryWithNilValues 验证含 NULL 值的查询结果。
func TestConnQueryWithNilValues(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns: []string{"id", "name"},
		Rows: []map[string]any{
			{"id": int64(1), "name": nil},
			{"id": nil, "name": "bob"},
		},
		IsQuery: true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendQuery("SELECT * FROM t"); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	types, err := client.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	// T + D + D + C + Z
	if len(types) != 5 {
		t.Errorf("期望 5 个消息, got %v", types)
	}
}

// TestConnEmptyResultQuery 验证空结果集查询。
func TestConnEmptyResultQuery(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns: []string{"id"},
		Rows:    nil,
		IsQuery: true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendQuery("SELECT * FROM empty"); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	types, err := client.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	// T + C + Z (无 DataRow)
	if len(types) != 3 {
		t.Errorf("期望 3 个消息, got %v", types)
	}
	if types[0] != 'T' {
		t.Errorf("期望 RowDescription('T'), got %c", types[0])
	}
	if types[1] != 'C' {
		t.Errorf("期望 CommandComplete('C'), got %c", types[1])
	}
}

// TestConnConcurrentConnections 验证并发连接处理。
func TestConnConcurrentConnections(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns: []string{"v"},
		Rows:    []map[string]any{{"v": int64(1)}},
		IsQuery: true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	const n = 10
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			client := newPGClient(t, srv.Addr())
			defer client.close()
			if err := client.sendStartupMessage(); err != nil {
				errs <- fmt.Errorf("client %d startup: %w", idx, err)
				return
			}
			if _, err := client.readUntilReadyForQuery(); err != nil {
				errs <- fmt.Errorf("client %d handshake: %w", idx, err)
				return
			}
			if err := client.sendQuery("SELECT 1"); err != nil {
				errs <- fmt.Errorf("client %d query: %w", idx, err)
				return
			}
			if _, err := client.readUntilReadyForQuery(); err != nil {
				errs <- fmt.Errorf("client %d response: %w", idx, err)
				return
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestConnQueryResultWithAllTypes 验证所有支持类型的查询结果。
func TestConnQueryResultWithAllTypes(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns: []string{"b", "i", "f", "s"},
		Rows: []map[string]any{
			{"b": true, "i": int64(42), "f": float64(3.14), "s": "text"},
		},
		IsQuery: true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendQuery("SELECT * FROM types"); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	types, err := client.readUntilReadyForQuery()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	// T + D + C + Z
	if len(types) != 4 {
		t.Errorf("期望 4 个消息, got %v", types)
	}
}
