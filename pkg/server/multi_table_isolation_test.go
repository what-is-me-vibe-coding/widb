package server

import "testing"

// TestMultiTableIsolationIssue181 复现 issue #181：多表情况下数据隔离失效。
//
// 场景：test 表已有 3 行，新建 test2 后：
//   - insert into test2 values(1) 误报 PRIMARY KEY CONFLICT（test2 为空，key "1" 来自 test）
//   - select * from test2 返回 test 的 3 行（id 全为 null，因 test2 schema 仅有 id 列）
//
// 根因：所有 LSM 表共享同一个 *storage.Engine，主键未按表名隔离。
func TestMultiTableIsolationIssue181(t *testing.T) {
	srv := newTestServer(t)

	// 1. 创建 test 表（LSM 默认引擎），写入 3 行
	if resp, _ := srv.handleQuery(&QueryRequest{
		SQL: "CREATE TABLE test (k STRING, v STRING, PRIMARY KEY (k))",
	}); resp.Code != 0 {
		t.Fatalf("CREATE TABLE test 失败: %s", resp.Message)
	}
	if resp, _ := srv.handleQuery(&QueryRequest{
		SQL: "INSERT INTO test (k, v) VALUES ('1', '2'), ('2', '3'), ('3', '4')",
	}); resp.Code != 0 {
		t.Fatalf("INSERT test 失败: %s", resp.Message)
	}
	if resp, _ := srv.handleQuery(&QueryRequest{SQL: "SELECT k, v FROM test"}); resp.Code != 0 || resp.Rows != 3 {
		t.Fatalf("test 应有 3 行, got code=%d rows=%d msg=%s", resp.Code, resp.Rows, resp.Message)
	}

	// 2. 创建 test2 表（LSM 默认引擎），与 test 共享存储引擎
	if resp, _ := srv.handleQuery(&QueryRequest{
		SQL: "CREATE TABLE test2 (id INT64 PRIMARY KEY)",
	}); resp.Code != 0 {
		t.Fatalf("CREATE TABLE test2 失败: %s", resp.Message)
	}

	// 3. 向 test2 插入 key=1：test2 为空，不应触发主键冲突
	resp, _ := srv.handleQuery(&QueryRequest{
		SQL: "INSERT INTO test2 (id) VALUES (1)",
	})
	if resp.Code != 0 {
		t.Fatalf("INSERT test2 values(1) 应成功，但失败: %s（多表主键未隔离）", resp.Message)
	}

	// 4. select * from test2 应仅返回 1 行（id=1），而非 test 的 3 行
	resp, _ = srv.handleQuery(&QueryRequest{SQL: "SELECT id FROM test2"})
	if resp.Code != 0 {
		t.Fatalf("SELECT test2 失败: %s", resp.Message)
	}
	if resp.Rows != 1 {
		t.Fatalf("test2 应仅 1 行，实际 %d 行（多表数据未隔离）", resp.Rows)
	}

	// 5. test 表数据不应受影响，仍为 3 行
	resp, _ = srv.handleQuery(&QueryRequest{SQL: "SELECT k, v FROM test"})
	if resp.Code != 0 || resp.Rows != 3 {
		t.Fatalf("test 仍应有 3 行, got code=%d rows=%d", resp.Code, resp.Rows)
	}

	// 6. 向 test2 再插入 key=2，应成功；key=1 应冲突（同表内）
	if resp, _ := srv.handleQuery(&QueryRequest{SQL: "INSERT INTO test2 (id) VALUES (2)"}); resp.Code != 0 {
		t.Fatalf("INSERT test2 values(2) 应成功: %s", resp.Message)
	}
	if resp, _ := srv.handleQuery(&QueryRequest{SQL: "INSERT INTO test2 (id) VALUES (1)"}); resp.Code == 0 {
		t.Fatal("INSERT test2 values(1) 重复主键应冲突，但成功")
	}
}
