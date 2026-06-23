package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

const (
	benchSelectAllSQL = `{"sql":"SELECT * FROM users"}`
	benchTableName    = "users"
	benchColName      = "name"
)

// --- HTTP 处理器基准测试 ---

func BenchmarkHTTPHealth(b *testing.B) {
	srv := newBenchServer(b)
	defer func() { _ = srv.Stop() }()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()
		srv.httpHealth(w, req)
	}
	b.ReportAllocs()
}

func BenchmarkHTTPQuery(b *testing.B) {
	srv := newBenchServerWithTable(b)
	defer func() { _ = srv.Stop() }()

	body := benchSelectAllSQL

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body))
		w := httptest.NewRecorder()
		srv.httpQuery(w, req)
	}
	b.ReportAllocs()
}

func BenchmarkHTTPWrite(b *testing.B) {
	srv := newBenchServerWithTable(b)
	defer func() { _ = srv.Stop() }()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		body := fmt.Sprintf(`{"table":"users","rows":[{"id":%d,"name":"user_%d"}]}`, i, i)
		req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(body))
		w := httptest.NewRecorder()
		srv.httpWrite(w, req)
	}
	b.ReportAllocs()
}

func BenchmarkHTTPWriteBatch(b *testing.B) {
	srv := newBenchServerWithTable(b)
	defer func() { _ = srv.Stop() }()

	batchSize := 100
	rows := make([]map[string]interface{}, batchSize)
	for i := 0; i < batchSize; i++ {
		rows[i] = map[string]interface{}{
			"id":         float64(i),
			benchColName: fmt.Sprintf("user_%d", i),
		}
	}

	writeReq := WriteRequest{Table: benchTableName, Rows: rows}
	reqBody, _ := json.Marshal(writeReq)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/write", bytes.NewReader(reqBody))
		w := httptest.NewRecorder()
		srv.httpWrite(w, req)
	}
	b.ReportAllocs()
}

// --- TCP 协议编解码基准测试 ---

func BenchmarkPacketEncode(b *testing.B) {
	payload := []byte(`{"sql":"SELECT * FROM users WHERE id = 1"}`)
	pkt := NewPacket(PacketQuery, payload)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pkt.Encode()
	}
	b.ReportAllocs()
}

func BenchmarkPacketDecode(b *testing.B) {
	payload := []byte(`{"sql":"SELECT * FROM users WHERE id = 1"}`)
	pkt := NewPacket(PacketQuery, payload)
	encoded := pkt.Encode()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = DecodePacket(bytes.NewReader(encoded))
	}
	b.ReportAllocs()
}

// --- 数据转换基准测试 ---

// benchBuildChunk 构建一个包含多列多行的 Chunk，用于 chunksToRows 基准测试。
// 列类型覆盖 INT64/STRING/FLOAT64/BOOL/TIMESTAMP，模拟真实查询结果。
func benchBuildChunk(b *testing.B, rowCount uint32) *storage.Chunk {
	b.Helper()
	chunk := storage.NewChunk(rowCount)

	cols := []struct {
		id  uint32
		typ common.DataType
	}{
		{0, common.TypeInt64},
		{1, common.TypeString},
		{2, common.TypeFloat64},
		{3, common.TypeBool},
		{4, common.TypeTimestamp},
	}
	for _, c := range cols {
		col := storage.NewColumnVector(c.id, c.typ, rowCount)
		for i := uint32(0); i < rowCount; i++ {
			switch c.typ {
			case common.TypeInt64:
				_ = col.Append(common.NewInt64(int64(i)))
			case common.TypeString:
				_ = col.Append(common.NewString(fmt.Sprintf("user_%d", i)))
			case common.TypeFloat64:
				_ = col.Append(common.NewFloat64(float64(i) * 1.5))
			case common.TypeBool:
				_ = col.Append(common.NewBool(i%2 == 0))
			case common.TypeTimestamp:
				_ = col.Append(common.NewTimestamp(time.Unix(int64(i), 0)))
			}
		}
		_ = chunk.AddColumn(col)
	}
	return chunk
}

// BenchmarkChunksToRows 测试查询结果物化的性能（SELECT 结果转 []map）。
// 这是每个返回行的 SELECT 查询的热点路径。
func BenchmarkChunksToRows(b *testing.B) {
	chunk := benchBuildChunk(b, 1024)
	chunks := []*storage.Chunk{chunk}
	colNames := []string{"id", "name", "score", "active", "ts"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = chunksToRows(chunks, colNames)
	}
	b.ReportAllocs()
}

// BenchmarkChunksToRowsMultiChunk 测试多 Chunk 场景下的物化性能。
func BenchmarkChunksToRowsMultiChunk(b *testing.B) {
	chunks := make([]*storage.Chunk, 0, 4)
	for k := 0; k < 4; k++ {
		chunks = append(chunks, benchBuildChunk(b, 256))
	}
	colNames := []string{"id", "name", "score", "active", "ts"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = chunksToRows(chunks, colNames)
	}
	b.ReportAllocs()
}

// BenchmarkChunksToRowsWideTable 测试宽表场景下的物化性能。
// 项目目标支持 ≥10000 列，宽表下列数较多，逐行 GetColumn 与 columnName
// 的累积开销更显著，是验证列遍历优化的关键场景。
func BenchmarkChunksToRowsWideTable(b *testing.B) {
	const colCount = 20
	const rowCount = 512
	chunk := storage.NewChunk(rowCount)
	colNames := make([]string, colCount)
	for c := 0; c < colCount; c++ {
		col := storage.NewColumnVector(uint32(c), common.TypeInt64, rowCount)
		for i := uint32(0); i < rowCount; i++ {
			_ = col.Append(common.NewInt64(int64(int(i) + c)))
		}
		_ = chunk.AddColumn(col)
		colNames[c] = fmt.Sprintf("col_%d", c)
	}
	chunks := []*storage.Chunk{chunk}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = chunksToRows(chunks, colNames)
	}
	b.ReportAllocs()
}

func BenchmarkInterfaceToValue(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = interfaceToValue(float64(42), common.TypeInt64)
	}
	b.ReportAllocs()
}

func BenchmarkInterfaceToValueString(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = interfaceToValue("hello world", common.TypeString)
	}
	b.ReportAllocs()
}

func BenchmarkConvertWriteRow(b *testing.B) {
	srv := newBenchServerWithTable(b)
	defer func() { _ = srv.Stop() }()

	tbl, _ := srv.catalog.GetTable(benchTableName)
	row := map[string]interface{}{
		"id":         float64(1),
		benchColName: "alice",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = srv.convertWriteRow(tbl, row)
	}
	b.ReportAllocs()
}

// --- TCP 端到端基准测试 ---

func BenchmarkTCPPing(b *testing.B) {
	srv := newBenchServer(b)
	if err := srv.Start(); err != nil {
		b.Fatal(err)
	}
	defer func() { _ = srv.Stop() }()

	conn, err := net.DialTimeout("tcp", srv.tcpListener.Addr().String(), 2*time.Second)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	pingPkt := NewPacket(PacketPing, nil)
	reader := bufio.NewReader(conn)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := conn.Write(pingPkt.Encode()); err != nil {
			b.Fatal(err)
		}
		if _, err := DecodePacket(reader); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
}

// --- 辅助函数 ---

func newBenchServer(b *testing.B) *Server {
	b.Helper()

	dir, err := os.MkdirTemp("", "bench-server-*")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(dir) })

	cfg := Config{
		TCPAddr:  "127.0.0.1:0",
		HTTPAddr: "127.0.0.1:0",
		DataDir:  dir,
	}

	srv, err := NewServer(cfg, WithMetricsRegistry(prometheus.NewRegistry()))
	if err != nil {
		b.Fatal(err)
	}
	return srv
}

func newBenchServerWithTable(b *testing.B) *Server {
	b.Helper()

	srv := newBenchServer(b)

	err := srv.catalog.CreateTable(benchTableName, []catalog.ColumnDef{
		{Name: "id", Type: common.TypeInt64, Nullable: false},
		{Name: benchColName, Type: common.TypeString, Nullable: true},
		{Name: testColScore, Type: common.TypeFloat64, Nullable: true},
	}, []string{"id"}, catalog.TableOptions{})
	if err != nil {
		b.Fatalf("CreateTable 失败: %v", err)
	}

	return srv
}

// --- DELETE/UPDATE 主键等值快路径基准测试 ---

// benchPKRowCount 控制基准测试中预置数据行数，使 DELETE/UPDATE 全表扫描
// 路径与 PK 等值快路径的耗时差距在不同规模下都可观察。
const benchPKRowCount = 1000

// seedBenchPKTable 预置 N 行 id=i 的数据用于基准测试。
// 列名使用 benchColName（"name"）与 newBenchServerWithTable 创建的表结构一致。
func seedBenchPKTable(b *testing.B, srv *Server) {
	b.Helper()
	rows := make([]map[string]any, benchPKRowCount)
	for i := 0; i < benchPKRowCount; i++ {
		rows[i] = map[string]any{"id": int64(i + 1), benchColName: "x"}
	}
	body, _ := json.Marshal(map[string]any{"table": benchTableName, "rows": rows})
	req := httptest.NewRequest(http.MethodPost, "/write", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.httpWrite(w, req)
	if w.Code != http.StatusOK {
		b.Fatalf("seed data failed: code=%d body=%s", w.Code, w.Body.String())
	}
}

// BenchmarkDeleteByPK 衡量「DELETE FROM t WHERE id = lit」在不同数据规模下
// 的耗时。主键等值快路径使耗时与行数 N 解耦（O(log n)），全表扫描路径则线性增长。
func BenchmarkDeleteByPK(b *testing.B) {
	srv := newBenchServerWithTable(b)
	defer func() { _ = srv.Stop() }()
	seedBenchPKTable(b, srv)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := int64((i % benchPKRowCount) + 1)
		sql := fmt.Sprintf(`{"sql":"DELETE FROM %s WHERE id = %d"}`, benchTableName, id)
		req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(sql))
		w := httptest.NewRecorder()
		srv.httpQuery(w, req)
	}
	b.ReportAllocs()
}

// BenchmarkUpdateByPK 衡量「UPDATE t SET name = lit WHERE id = pk」耗时，
// 与 BenchmarkDeleteByPK 同样验证点查快路径的开销稳定性。
func BenchmarkUpdateByPK(b *testing.B) {
	srv := newBenchServerWithTable(b)
	defer func() { _ = srv.Stop() }()
	seedBenchPKTable(b, srv)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := int64((i % benchPKRowCount) + 1)
		sql := fmt.Sprintf(`{"sql":"UPDATE %s SET %s = 'y' WHERE id = %d"}`, benchTableName, benchColName, id)
		req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(sql))
		w := httptest.NewRecorder()
		srv.httpQuery(w, req)
	}
	b.ReportAllocs()
}

// --- GetTable 微基准测试（验证冗余调用去重收益）---

// BenchmarkCatalogGetTable 衡量 catalog.GetTable 单次调用的耗时与分配，
// 用于评估 handleDelete 合并两次 GetTable 为一次的优化收益。
// 每次 GetTable 都会深拷贝 Columns/PrimaryKey/SegmentList 并将 colTypeMap 置 nil，
// 因此开销主要来自切片分配与拷贝；SegmentList 越长，开销越大。
func BenchmarkCatalogGetTable(b *testing.B) {
	srv := newBenchServerWithTable(b)
	defer func() { _ = srv.Stop() }()

	// 预热 ColTypeMap 的懒初始化
	if _, err := srv.catalog.GetTable(benchTableName); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tbl, err := srv.catalog.GetTable(benchTableName)
		if err != nil {
			b.Fatal(err)
		}
		_ = tbl.ColTypeMap()
	}
	b.ReportAllocs()
}
