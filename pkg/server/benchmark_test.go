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
		{Name: "score", Type: common.TypeFloat64, Nullable: true},
	}, []string{"id"}, catalog.TableOptions{})
	if err != nil {
		b.Fatalf("CreateTable 失败: %v", err)
	}

	return srv
}
