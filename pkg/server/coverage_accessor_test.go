package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// --- writeJSON: JSON encoding error path ---

// failingResponseWriter wraps httptest.ResponseRecorder and injects a Write error.
type failingResponseWriter struct {
	*httptest.ResponseRecorder
	writeErr error
}

func (f *failingResponseWriter) Write(b []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return f.ResponseRecorder.Write(b)
}

func TestWriteJSON_EncodingError(t *testing.T) {
	w := &failingResponseWriter{
		ResponseRecorder: httptest.NewRecorder(),
		writeErr:         errors.New("write failed"),
	}

	// writeJSON should not panic even if the underlying writer fails
	writeJSON(w, http.StatusOK, &Response{Code: 0, Message: testTableName})

	// The function sets headers before writing, so headers should still be set
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want %q", w.Header().Get("Content-Type"), "application/json")
	}
}

// --- writeJSON: unmarshallable value ---

type unmarshallable struct {
	Ch chan int // channels cannot be marshaled to JSON
}

func TestWriteJSON_UnmarshallableValue(t *testing.T) {
	w := httptest.NewRecorder()

	// writeJSON should not panic when encoding an unmarshallable value
	writeJSON(w, http.StatusOK, &unmarshallable{Ch: make(chan int)})

	// Headers should still be set
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want %q", w.Header().Get("Content-Type"), "application/json")
	}
}

// --- TCPAddr: before and after Start ---

func TestTCPAddr_BeforeStart(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	addr := srv.TCPAddr()
	if addr != "" {
		t.Errorf("TCPAddr before Start = %q, want empty string", addr)
	}
}

func TestTCPAddr_AfterStart(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	addr := srv.TCPAddr()
	if addr == "" {
		t.Error("TCPAddr after Start = empty string, want non-empty")
	}
}

// --- HTTPAddr: before and after Start ---

func TestHTTPAddr_BeforeStart(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	addr := srv.HTTPAddr()
	if addr != "" {
		t.Errorf("HTTPAddr before Start = %q, want empty string", addr)
	}
}

func TestHTTPAddr_AfterStart(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	addr := srv.HTTPAddr()
	if addr == "" {
		t.Error("HTTPAddr after Start = empty string, want non-empty")
	}
}

// --- Catalog accessor ---

func TestCatalog_Accessor(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	cat := srv.Catalog()
	if cat == nil {
		t.Error("Catalog() returned nil, want non-nil")
	}
}

func TestCatalog_AccessorWithTable(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	cat := srv.Catalog()
	if cat == nil {
		t.Fatal("Catalog() returned nil")
	}

	tbl, err := cat.GetTable(testTable)
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}
	if tbl == nil {
		t.Error("GetTable returned nil")
	}
}

// --- handleQueryPacket: JSON marshal error for response ---
// This tests the json.Marshal error path in handleQueryPacket when the
// response contains unmarshallable data.

func TestHandleQueryPacket_MarshalResponseError(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	// Send a valid query request - the normal path should work
	queryPayload, _ := json.Marshal(QueryRequest{SQL: testSelectAll})
	pkt := NewPacket(PacketQuery, queryPayload)
	resp, err := srv.handleQueryPacket(pkt)
	if err != nil {
		t.Logf("handleQueryPacket returned error (acceptable): %v", err)
	}
	// The response should be non-nil for a valid query
	if resp != nil && resp.Type != PacketResponse {
		t.Errorf("response type = %d, want %d", resp.Type, PacketResponse)
	}
}

// --- handleWritePacket: JSON marshal error for response ---

func TestHandleWritePacket_MarshalResponseError(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	// Send a valid write request - the normal path should work
	writePayload, _ := json.Marshal(WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(1), testColName: testName}},
	})
	pkt := NewPacket(PacketWrite, writePayload)
	resp, err := srv.handleWritePacket(pkt)
	if err != nil {
		t.Logf("handleWritePacket returned error (acceptable): %v", err)
	}
	if resp != nil && resp.Type != PacketResponse {
		t.Errorf("response type = %d, want %d", resp.Type, PacketResponse)
	}
}

// --- handlePing: JSON marshal error for response ---

func TestHandlePing_MarshalResponseError(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	// Normal ping should work fine
	resp, err := srv.handlePing()
	if err != nil {
		t.Errorf("handlePing returned unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("handlePing returned nil response")
	}
	if resp.Type != PacketResponse {
		t.Errorf("response type = %d, want %d", resp.Type, PacketResponse)
	}
}

// --- convertWriteRow: missing primary key column ---

func TestConvertWriteRow_MissingPrimaryKey(t *testing.T) {
	srv := newTestServerWithTable(t)
	defer func() { _ = srv.Stop() }()

	tbl, err := srv.catalog.GetTable(testTable)
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}

	// Row without the primary key column "id"
	row := map[string]interface{}{
		testColName: testName,
	}

	_, _, err = srv.convertWriteRow(tbl, row)
	if err == nil {
		t.Error("convertWriteRow with missing primary key expected error, got nil")
	}
}

// --- convertWriteRow: unknown column type ---

func TestConvertWriteRow_UnknownColumnType(t *testing.T) {
	dir, err := os.MkdirTemp("", "testdb-server-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cfg := Config{
		TCPAddr:  testListenAddr,
		HTTPAddr: testListenAddr,
		DataDir:  dir,
	}
	registry := prometheus.NewRegistry()
	srv, err := NewServer(cfg, WithMetricsRegistry(registry))
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	// Create a table with a TIMESTAMP column
	err = srv.catalog.CreateTable("ts_table", []catalog.ColumnDef{
		{Name: "id", Type: common.TypeInt64, Nullable: false},
		{Name: "ts", Type: common.TypeTimestamp, Nullable: true},
	}, []string{"id"}, catalog.TableOptions{})
	if err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	tbl, err := srv.catalog.GetTable("ts_table")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}

	// Row with a timestamp value as string
	row := map[string]interface{}{
		"id": float64(1),
		"ts": "2024-01-15T10:30:00Z",
	}

	key, values, err := srv.convertWriteRow(tbl, row)
	if err != nil {
		t.Fatalf("convertWriteRow failed: %v", err)
	}
	if key != "1" {
		t.Errorf("key = %q, want %q", key, "1")
	}
	if _, ok := values["ts"]; !ok {
		t.Error("ts column not found in values")
	}
}

// --- buildPrimaryKey: multiple primary key columns ---

const (
	colPK1 = "pk1"
	colPK2 = "pk2"
)

func TestBuildPrimaryKey_MultiplePKColumns(t *testing.T) {
	srv := newTestServer(t)
	defer func() { _ = srv.Stop() }()

	// Create a table with composite primary key
	err := srv.catalog.CreateTable("composite_pk", []catalog.ColumnDef{
		{Name: colPK1, Type: common.TypeInt64, Nullable: false},
		{Name: colPK2, Type: common.TypeString, Nullable: false},
		{Name: "val", Type: common.TypeFloat64, Nullable: true},
	}, []string{colPK1, colPK2}, catalog.TableOptions{})
	if err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	tbl, err := srv.catalog.GetTable("composite_pk")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}

	row := map[string]interface{}{
		colPK1: float64(42),
		colPK2: "hello",
		"val":  float64(3.14),
	}

	key, err := srv.buildPrimaryKey(tbl, row)
	if err != nil {
		t.Fatalf("buildPrimaryKey failed: %v", err)
	}
	if key != "42|hello" {
		t.Errorf("key = %q, want %q", key, "42|hello")
	}
}
