package server

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

const (
	testSelectAll  = "SELECT * FROM users"
	testTable      = "users"
	testName       = "alice"
	testTableName  = "test"
	testListenAddr = "127.0.0.1:0"
	testColScore   = "score"
	testStrHello   = "hello"
	testColName    = "name"
)

// --- handleQuery / handleWrite 直接测试 ---

func TestHandleQuerySelectFromTable(t *testing.T) {
	srv := newTestServerWithTable(t)
	resp, err := srv.handleQuery(&QueryRequest{SQL: testSelectAll})
	if err != nil {
		t.Fatalf("handleQuery 失败: %v", err)
	}
	if resp.Code != 0 {
		t.Errorf("响应 Code = %d, Message = %q", resp.Code, resp.Message)
	}
}

func TestHandleQueryInvalidSQL(t *testing.T) {
	srv := newTestServer(t)
	resp, err := srv.handleQuery(&QueryRequest{SQL: "INVALID SQL !!!"})
	if err != nil {
		t.Fatalf("handleQuery 失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", resp.Code)
	}
}

func TestHandleQueryTableNotExist(t *testing.T) {
	srv := newTestServer(t)
	resp, err := srv.handleQuery(&QueryRequest{SQL: "SELECT * FROM nonexistent"})
	if err != nil {
		t.Fatalf("handleQuery 失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", resp.Code)
	}
}

func TestHandleWriteSuccess(t *testing.T) {
	srv := newTestServerWithTable(t)
	resp, err := srv.handleWrite(&WriteRequest{
		Table: testTable,
		Rows: []map[string]interface{}{
			{"id": float64(1), testColName: testName},
			{"id": float64(2), testColName: "bob"},
		},
	})
	if err != nil {
		t.Fatalf("handleWrite 失败: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("写入响应 Code = %d, Message = %q", resp.Code, resp.Message)
	}
	if resp.Rows != 2 {
		t.Errorf("写入行数 = %d, 期望 2", resp.Rows)
	}
}

func TestHandleWriteTableNotExist(t *testing.T) {
	srv := newTestServer(t)
	resp, err := srv.handleWrite(&WriteRequest{
		Table: "nonexistent",
		Rows:  []map[string]interface{}{{"id": 1}},
	})
	if err != nil {
		t.Fatalf("handleWrite 失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", resp.Code)
	}
}

func TestHandleWriteMissingPK(t *testing.T) {
	srv := newTestServerWithTable(t)
	resp, err := srv.handleWrite(&WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{testColName: testName}},
	})
	if err != nil {
		t.Fatalf("handleWrite 失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", resp.Code)
	}
}

func TestHandleWriteTypeMismatch(t *testing.T) {
	srv := newTestServerWithTable(t)
	resp, err := srv.handleWrite(&WriteRequest{
		Table: testTable,
		Rows:  []map[string]interface{}{{"id": float64(1), testColName: true}},
	})
	if err != nil {
		t.Fatalf("handleWrite 失败: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("响应 Code = %d, 期望 -1", resp.Code)
	}
}

// --- buildPrimaryKey 测试 ---

func TestBuildPrimaryKey(t *testing.T) {
	srv := newTestServer(t)
	tbl := &catalog.Table{Name: testTableName, PrimaryKey: []string{"id"}}

	tests := []struct {
		name    string
		row     map[string]interface{}
		wantKey string
		wantErr bool
	}{
		{"单主键", map[string]interface{}{"id": 1}, "1", false},
		{"缺失主键", map[string]interface{}{testColName: testTableName}, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := srv.buildPrimaryKey(tbl, tt.row)
			if (err != nil) != tt.wantErr {
				t.Errorf("buildPrimaryKey() 错误 = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if key != tt.wantKey {
				t.Errorf("buildPrimaryKey() = %q, 期望 %q", key, tt.wantKey)
			}
		})
	}
}

func TestBuildCompositePrimaryKey(t *testing.T) {
	srv := newTestServer(t)
	tbl := &catalog.Table{Name: testTableName, PrimaryKey: []string{"region", "id"}}

	row := map[string]interface{}{"region": "us", "id": 42}
	key, err := srv.buildPrimaryKey(tbl, row)
	if err != nil {
		t.Fatalf("buildPrimaryKey 失败: %v", err)
	}
	if key != "us|42" {
		t.Errorf("复合主键 = %q, 期望 'us|42'", key)
	}
}

// --- StorageAdapter 测试 ---

func TestStorageAdapter(t *testing.T) {
	dir, err := os.MkdirTemp("", "testdb-adapter-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	eng, err := storage.NewEngine(storage.EngineConfig{
		DataDir: dir, MaxMemTableSize: 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sa := &storageAdapter{engine: eng}
	_ = sa.ScanRange("", "\xff\xff\xff\xff")
	_ = sa.ColumnMeta()

	if sa.PrimaryIndex() == nil {
		t.Error("PrimaryIndex 不应为 nil")
	}
	if sa.SparseIndex() == nil {
		t.Error("SparseIndex 不应为 nil")
	}
}

// --- 默认配置测试 ---

func TestNewServerDefaultConfig(t *testing.T) {
	dir, err := os.MkdirTemp("", "testdb-cfg-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cfg := Config{
		TCPAddr:  testListenAddr,
		HTTPAddr: testListenAddr,
		DataDir:  filepath.Join(dir, "data"),
	}

	srv, err := NewServer(cfg, WithMetricsRegistry(prometheus.NewRegistry()))
	if err != nil {
		t.Fatalf("NewServer 失败: %v", err)
	}
	if srv.cfg.MaxMemTableSize != 64*1024*1024 {
		t.Errorf("MaxMemTableSize = %d, 期望 %d", srv.cfg.MaxMemTableSize, 64*1024*1024)
	}
	if err := srv.Stop(); err != nil {
		t.Logf("Stop 错误: %v", err)
	}
}

// --- convertWriteRow 测试 ---

func TestConvertWriteRowIgnoreUnknownColumn(t *testing.T) {
	srv := newTestServerWithTable(t)
	tbl, _ := srv.catalog.GetTable(testTable)

	key, values, err := srv.convertWriteRow(tbl, map[string]interface{}{
		"id":        float64(1),
		testColName: testName,
		"unknown":   "value",
	})
	if err != nil {
		t.Fatalf("convertWriteRow 失败: %v", err)
	}
	if key != "1" {
		t.Errorf("key = %q, 期望 '1'", key)
	}
	if _, ok := values["unknown"]; ok {
		t.Error("未知列应被忽略")
	}
	if len(values) != 2 {
		t.Errorf("values 长度 = %d, 期望 2", len(values))
	}
}

// --- isClosedConnErr 测试 ---

func TestIsClosedConnErrWithTimeout(t *testing.T) {
	opErr := &net.OpError{Err: timeoutError{}}
	if !isClosedConnErr(opErr) {
		t.Error("超时 OpError 应返回 true")
	}

	opErr2 := &net.OpError{Err: fmt.Errorf("connection refused")}
	if isClosedConnErr(opErr2) {
		t.Error("非超时 OpError 应返回 false")
	}
}

// timeoutError 实现 net.Error 接口用于测试。
type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return false }
