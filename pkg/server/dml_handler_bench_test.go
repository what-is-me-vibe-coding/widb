package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- INSERT 端到端基准测试 ---

// BenchmarkInsert 衡量单行 INSERT 经 HTTP /query 入口的耗时与分配。
// 每次迭代使用唯一 id 避免主键冲突，模拟稳态写入路径。
func BenchmarkInsert(b *testing.B) {
	srv := newBenchServerWithTable(b)
	defer func() { _ = srv.Stop() }()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sql := fmt.Sprintf(`{"sql":"INSERT INTO %s VALUES (%d, 'x')"}`, benchTableName, i+1)
		req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(sql))
		w := httptest.NewRecorder()
		srv.httpQuery(w, req)
	}
	b.ReportAllocs()
}

// BenchmarkInsertBatch 衡量批量 INSERT 经 HTTP /write 入口的耗时与分配。
// 每批 100 行，与单行 INSERT 对照，可观察到 WriteBatch 相对逐行 INSERT 的摊销收益。
// 每次迭代按 i*batchSize 偏移主键 id，避免与上一轮产生主键冲突。
func BenchmarkInsertBatch(b *testing.B) {
	srv := newBenchServerWithTable(b)
	defer func() { _ = srv.Stop() }()

	const batchSize = 100
	rows := make([]map[string]any, batchSize)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 调整 id 偏移避免与上一轮主键冲突：第 i 轮 id 范围 [i*batchSize+1, (i+1)*batchSize]
		offset := int64(i) * batchSize
		for j := 0; j < batchSize; j++ {
			rows[j] = map[string]any{"id": offset + int64(j) + 1, benchColName: "x"}
		}
		body, _ := encodeBenchWriteBody(benchTableName, rows)
		req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(body))
		w := httptest.NewRecorder()
		srv.httpWrite(w, req)
	}
	b.ReportAllocs()
}

// --- DELETE/UPDATE 扫描路径基准测试 ---

// BenchmarkDeleteScan 衡量「DELETE WHERE 非主键列」扫描路径的耗时。
// 与 BenchmarkDeleteByPK 对照可量化 PK 快路径相对全表扫描的加速比。
// 使用 name = 'missing' 使谓词永不命中，避免迭代间表数据被清空。
func BenchmarkDeleteScan(b *testing.B) {
	srv := newBenchServerWithTable(b)
	defer func() { _ = srv.Stop() }()
	seedBenchPKTable(b, srv)

	sql := fmt.Sprintf(`{"sql":"DELETE FROM %s WHERE %s = 'missing'"}`, benchTableName, benchColName)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(sql))
		w := httptest.NewRecorder()
		srv.httpQuery(w, req)
	}
	b.ReportAllocs()
}

// BenchmarkUpdateScan 衡量「UPDATE WHERE 非主键列」扫描路径的耗时。
// 与 BenchmarkUpdateByPK 对照可量化 PK 快路径相对全表扫描的加速比。
// 使用 name = 'missing' 使谓词永不命中，避免迭代间 SET 重复修改同一行。
func BenchmarkUpdateScan(b *testing.B) {
	srv := newBenchServerWithTable(b)
	defer func() { _ = srv.Stop() }()
	seedBenchPKTable(b, srv)

	sql := fmt.Sprintf(`{"sql":"UPDATE %s SET %s = 'y' WHERE %s = 'missing'"}`,
		benchTableName, benchColName, benchColName)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(sql))
		w := httptest.NewRecorder()
		srv.httpQuery(w, req)
	}
	b.ReportAllocs()
}

// --- SELECT 全表扫描基准测试 ---

// BenchmarkSelectAll 衡量无 WHERE 的全表 SELECT 经 HTTP /query 入口的耗时。
// 在 seedBenchPKTable 规模（1000 行）下作为「扫描路径开销」的基线参考。
func BenchmarkSelectAll(b *testing.B) {
	srv := newBenchServerWithTable(b)
	defer func() { _ = srv.Stop() }()
	seedBenchPKTable(b, srv)

	sql := fmt.Sprintf(`{"sql":"SELECT * FROM %s"}`, benchTableName)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(sql))
		w := httptest.NewRecorder()
		srv.httpQuery(w, req)
	}
	b.ReportAllocs()
}

// --- 辅助函数 ---

// encodeBenchWriteBody 将 table+rows 编码为 /write 端点期望的 JSON 字符串。
// 集中处理便于在 BenchmarkInsertBatch 中调整主键偏移，避免重复的 JSON 编码样板。
// 极简编码：只处理本基准使用的 id + name 两列；不引通用 encoder 是为了
// 避免对 encoding/json 自身做基准测量时与 INSERTBatch 路径相互污染。
func encodeBenchWriteBody(table string, rows []map[string]any) (string, error) {
	buf := strings.Builder{}
	buf.WriteString(`{"table":"`)
	buf.WriteString(table)
	buf.WriteString(`","rows":[`)
	for i, r := range rows {
		if i > 0 {
			buf.WriteByte(',')
		}
		fmt.Fprintf(&buf, `{"id":%d,"%s":"x"}`, toInt64(r["id"]), benchColName)
	}
	buf.WriteString(`]}`)
	return buf.String(), nil
}

// toInt64 将 int / int64 统一转 int64，避免 BenchmarkInsertBatch 中重复类型断言。
func toInt64(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	default:
		return 0
	}
}
