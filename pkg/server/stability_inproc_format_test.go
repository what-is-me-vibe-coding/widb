package server

import (
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/query"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// 本文件补充 pkg/server 中覆盖率不足的导出/内部函数的直接单元测试：
// FormatResponse、Ping、ExecuteWrite、PGAddr、createTableErrorResponse、
// tableAlreadyExistsResponse，以及 routingAdapter / engineAdapter 的委托方法。

// --- FormatResponse ---

func TestFormatResponseAllBranches(t *testing.T) {
	tests := []struct {
		name string
		resp *Response
		want string
	}{
		{"错误响应", &Response{Code: -1, Message: "表不存在"}, "错误: 表不存在"},
		{"数据带行数", &Response{Code: 0, Rows: 2, Data: []map[string]any{{"id": int64(1)}}}, "2 行:\n"},
		{"数据无行数", &Response{Code: 0, Data: map[string]any{"k": "v"}}, `"k":`},
		{"仅行数", &Response{Code: 0, Rows: 5}, "成功，影响 5 行"},
		{"仅消息", &Response{Code: 0, Message: "pong"}, "pong"},
		{"空响应", &Response{Code: 0}, "成功"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatResponse(tt.resp)
			if tt.want == "" {
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("FormatResponse = %q, 期望包含 %q", got, tt.want)
			}
		})
	}
}

// --- Ping ---

func TestServerPing(t *testing.T) {
	srv := newTestServer(t)
	if got := srv.Ping(); got != msgPong {
		t.Errorf("Ping() = %q, 期望 %q", got, msgPong)
	}
}

// --- ExecuteWrite ---

func TestExecuteWriteSuccess(t *testing.T) {
	srv := newTestServerWithTable(t)
	t.Cleanup(func() { _ = srv.Stop() })

	resp, err := srv.ExecuteWrite(testTable, []map[string]any{
		{"id": float64(1), testColName: testName},
	})
	if err != nil {
		t.Fatalf("ExecuteWrite 失败: %v", err)
	}
	if resp.Code != 0 || resp.Rows != 1 {
		t.Errorf("ExecuteWrite resp = %+v, 期望 Code=0 Rows=1", resp)
	}
}

func TestExecuteWriteTableNotExist(t *testing.T) {
	srv := newTestServer(t)
	t.Cleanup(func() { _ = srv.Stop() })

	resp, err := srv.ExecuteWrite("missing", []map[string]any{{"id": float64(1)}})
	if err != nil {
		t.Fatalf("ExecuteWrite 返回错误: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("期望 Code=-1, 得到 %d (%q)", resp.Code, resp.Message)
	}
}

func TestExecuteWriteMissingPrimaryKey(t *testing.T) {
	srv := newTestServerWithTable(t)
	t.Cleanup(func() { _ = srv.Stop() })

	// 行缺少主键列 id，convertWriteRow 应返回转换错误
	resp, err := srv.ExecuteWrite(testTable, []map[string]any{{testColName: testName}})
	if err != nil {
		t.Fatalf("ExecuteWrite 返回错误: %v", err)
	}
	if resp.Code != -1 {
		t.Errorf("期望 Code=-1, 得到 %d", resp.Code)
	}
	if !strings.Contains(resp.Message, "主键") {
		t.Errorf("期望消息包含 '主键', 得到 %q", resp.Message)
	}
}

// --- PGAddr ---

func TestPGAddrEmptyBeforeStart(t *testing.T) {
	srv := newTestServer(t)
	if got := srv.PGAddr(); got != "" {
		t.Errorf("启动前 PGAddr() = %q, 期望空", got)
	}
}

func TestPGAddrAfterStart(t *testing.T) {
	dir := t.TempDir()
	registry := prometheus.NewRegistry()
	srv, err := NewServer(Config{
		TCPAddr:  testListenAddr,
		HTTPAddr: testListenAddr,
		PGAddr:   testListenAddr,
		DataDir:  dir,
	}, WithMetricsRegistry(registry))
	if err != nil {
		t.Fatalf("NewServer 失败: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	if got := srv.PGAddr(); got == "" {
		t.Error("启动后 PGAddr() 不应为空")
	}
}

// --- createTableErrorResponse / tableAlreadyExistsResponse ---

func TestCreateTableErrorResponse(t *testing.T) {
	srv := newTestServer(t)
	ct := &query.CreateTableStatement{Table: "t"}

	// 非 "already exists" 错误：返回错误响应
	resp := srv.createTableErrorResponse(ct, errors.New("invalid column"))
	if resp.Code != -1 {
		t.Errorf("非已存在错误: 期望 Code=-1, 得到 %d", resp.Code)
	}
	if !strings.Contains(resp.Message, "invalid column") {
		t.Errorf("期望消息包含原始错误, 得到 %q", resp.Message)
	}

	// IfNotExists + "already exists"：视为成功
	ct.IfNotExists = true
	resp = srv.createTableErrorResponse(ct, errors.New("table \"t\" already exists"))
	if resp.Code != 0 {
		t.Errorf("IF NOT EXISTS + already exists: 期望 Code=0, 得到 %d", resp.Code)
	}

	// IfNotExists 但非 already exists 错误：仍返回错误
	resp = srv.createTableErrorResponse(ct, errors.New("disk full"))
	if resp.Code != -1 {
		t.Errorf("IF NOT EXISTS + 其他错误: 期望 Code=-1, 得到 %d", resp.Code)
	}
}

func TestTableAlreadyExistsResponse(t *testing.T) {
	srv := newTestServer(t)
	ct := &query.CreateTableStatement{Table: "t"}

	if resp := srv.tableAlreadyExistsResponse(ct); resp.Code != -1 {
		t.Errorf("无 IF NOT EXISTS: 期望 Code=-1, 得到 %d", resp.Code)
	}

	ct.IfNotExists = true
	if resp := srv.tableAlreadyExistsResponse(ct); resp.Code != 0 {
		t.Errorf("IF NOT EXISTS: 期望 Code=0, 得到 %d", resp.Code)
	}
}

// --- routingAdapter / engineAdapter 委托方法 ---

func TestRoutingAdapterDelegatesToDefault(t *testing.T) {
	srv := newTestServerWithTable(t)
	t.Cleanup(func() { _ = srv.Stop() })

	// 写入若干行到默认引擎，使 ScanRangeWithPruning 有数据可扫
	if err := srv.adapter.defaultEng.WriteBatch([]storage.WriteRow{
		{Key: "k1", Values: map[string]common.Value{"id": common.NewInt64(1)}},
		{Key: "k2", Values: map[string]common.Value{"id": common.NewInt64(2)}},
	}); err != nil {
		t.Fatalf("WriteBatch 失败: %v", err)
	}

	preds := []storage.ColumnPredicate{{ColumnName: "id", Op: 0, Value: common.NewInt64(1)}}
	got := srv.adapter.ScanRangeWithPruning("", "\xff\xff\xff\xff", preds)
	if len(got) != 2 {
		t.Errorf("ScanRangeWithPruning 返回 %d 行, 期望 2", len(got))
	}

	// 委托方法应返回与默认引擎一致的值
	if meta := srv.adapter.ColumnMeta(); len(meta) != len(srv.adapter.defaultEng.ColumnMeta()) {
		t.Errorf("ColumnMeta 委托不一致: adapter=%d default=%d", len(meta), len(srv.adapter.defaultEng.ColumnMeta()))
	}
	if srv.adapter.PrimaryIndex() != srv.adapter.defaultEng.PrimaryIndex() {
		t.Error("PrimaryIndex 委托应返回默认引擎的索引")
	}
	if srv.adapter.SparseIndex() != srv.adapter.defaultEng.SparseIndex() {
		t.Error("SparseIndex 委托应返回默认引擎的索引")
	}
}

func TestEngineAdapterDelegates(t *testing.T) {
	srv := newTestServerWithTable(t)
	t.Cleanup(func() { _ = srv.Stop() })

	if err := srv.adapter.defaultEng.WriteBatch([]storage.WriteRow{
		{Key: "k1", Values: map[string]common.Value{"id": common.NewInt64(1)}},
	}); err != nil {
		t.Fatalf("WriteBatch 失败: %v", err)
	}

	// ForTable 对未注册的表返回包装默认引擎的 engineAdapter
	adapter, ok := srv.adapter.ForTable("unknown").(*engineAdapter)
	if !ok {
		t.Fatalf("ForTable 应返回 *engineAdapter, 得到 %T", srv.adapter.ForTable("unknown"))
	}
	if got := adapter.ScanRange("", "\xff\xff\xff\xff"); len(got) != 1 {
		t.Errorf("engineAdapter.ScanRange 返回 %d 行, 期望 1", len(got))
	}
	if got := adapter.ScanRangeWithPruning("", "\xff\xff\xff\xff", nil); len(got) != 1 {
		t.Errorf("engineAdapter.ScanRangeWithPruning 返回 %d 行, 期望 1", len(got))
	}
	if adapter.ColumnMeta() == nil {
		t.Error("engineAdapter.ColumnMeta() 不应为 nil")
	}
}

// --- buildColumnMeta / createMemoryEngine 辅助函数 ---

func TestBuildColumnMeta(t *testing.T) {
	cols := []catalog.ColumnDef{
		{Name: "id", Type: common.TypeInt64},
		{Name: "name", Type: common.TypeString},
	}
	meta := buildColumnMeta(cols)
	if len(meta) != 2 {
		t.Fatalf("期望 2 列元数据, 得到 %d", len(meta))
	}
	if meta[0].ID != 0 || meta[0].Name != "id" || meta[0].Type != common.TypeInt64 {
		t.Errorf("第 0 列元数据异常: %+v", meta[0])
	}
	if meta[1].ID != 1 || meta[1].Name != "name" {
		t.Errorf("第 1 列元数据异常: %+v", meta[1])
	}
}

func TestCreateMemoryEngineSetsMeta(t *testing.T) {
	cols := []catalog.ColumnDef{{Name: "id", Type: common.TypeInt64}}
	eng := createMemoryEngine(cols)
	if meta := eng.ColumnMeta(); len(meta) != 1 || meta[0].Name != "id" {
		t.Errorf("createMemoryEngine 未正确设置列元数据: %+v", meta)
	}
}
