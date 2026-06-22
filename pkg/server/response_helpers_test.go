package server

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// readCounter 读取指定 metric 与 label 组合的当前计数（仅用于测试）。
// 不使用 prometheus/testutil 以避免新增间接依赖。
func readCounter(t *testing.T, reg *prometheus.Registry, name string, labelKey, labelVal string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather 失败: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == labelKey && lp.GetValue() == labelVal {
					c := m.GetCounter()
					if c == nil {
						return 0
					}
					return c.GetValue()
				}
			}
		}
	}
	return 0
}

// TestQueryErrRespIncrementsMetric 验证 queryErrResp 在返回错误响应的同时
// 按预期递增对应类别的 QueriesTotal 指标。
func TestQueryErrRespIncrementsMetric(t *testing.T) {
	reg := prometheus.NewRegistry()
	srv := &Server{metrics: NewMetrics(reg)}

	// 抓取初始的失败计数（initLabels 阶段已 Add(0) 占位）。
	before := readCounter(t, reg, "widb_queries_total", "type", MetricQueryExecuteError)

	resp := srv.queryErrResp(MetricQueryExecuteError, "测试错误: %d", 42)
	if resp == nil {
		t.Fatalf("queryErrResp 返回 nil")
	}
	if resp.Code != -1 {
		t.Errorf("Code = %d, want -1", resp.Code)
	}
	if !strings.Contains(resp.Message, "测试错误: 42") {
		t.Errorf("Message = %q, 期望包含 %q", resp.Message, "测试错误: 42")
	}

	after := readCounter(t, reg, "widb_queries_total", "type", MetricQueryExecuteError)
	if after-before != 1 {
		t.Errorf("execute_error 计数增量 = %v, want 1", after-before)
	}
}

// TestQueryErrRespAllKinds 表驱动覆盖 4 种查询指标类别，每种均应正确递增。
func TestQueryErrRespAllKinds(t *testing.T) {
	cases := []struct {
		name string
		kind string
	}{
		{"parse_error", MetricQueryParseError},
		{"analyze_error", MetricQueryAnalyzeError},
		{"execute_error", MetricQueryExecuteError},
		{"success", MetricQuerySuccess}, // success 也可被该函数使用，递增计数即可
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			srv := &Server{metrics: NewMetrics(reg)}
			before := readCounter(t, reg, "widb_queries_total", "type", tc.kind)
			srv.queryErrResp(tc.kind, "msg")
			after := readCounter(t, reg, "widb_queries_total", "type", tc.kind)
			if after-before != 1 {
				t.Errorf("kind=%s 增量=%v, want 1", tc.kind, after-before)
			}
		})
	}
}

// TestQuerySuccessInc 验证 querySuccessInc 正确递增 success 计数。
func TestQuerySuccessInc(t *testing.T) {
	reg := prometheus.NewRegistry()
	srv := &Server{metrics: NewMetrics(reg)}
	before := readCounter(t, reg, "widb_queries_total", "type", MetricQuerySuccess)
	srv.querySuccessInc()
	after := readCounter(t, reg, "widb_queries_total", "type", MetricQuerySuccess)
	if after-before != 1 {
		t.Errorf("success 增量 = %v, want 1", after-before)
	}
}

// TestWriteErrRespIncrementsMetric 验证 writeErrResp 在返回错误响应的同时
// 正确递增对应类别的 WritesTotal 指标。
func TestWriteErrRespIncrementsMetric(t *testing.T) {
	reg := prometheus.NewRegistry()
	srv := &Server{metrics: NewMetrics(reg)}
	before := readCounter(t, reg, "widb_writes_total", "result", MetricWriteTableNotFound)

	resp := srv.writeErrResp(MetricWriteTableNotFound, "缺失表: %s", "t1")
	if resp == nil {
		t.Fatalf("writeErrResp 返回 nil")
	}
	if resp.Code != -1 {
		t.Errorf("Code = %d, want -1", resp.Code)
	}
	if !strings.Contains(resp.Message, "缺失表: t1") {
		t.Errorf("Message = %q, 期望包含 %q", resp.Message, "缺失表: t1")
	}

	after := readCounter(t, reg, "widb_writes_total", "result", MetricWriteTableNotFound)
	if after-before != 1 {
		t.Errorf("table_not_found 计数增量 = %v, want 1", after-before)
	}
}

// TestWriteSuccessInc 表驱动验证 writeSuccessInc 按传入行数加权递增。
func TestWriteSuccessInc(t *testing.T) {
	cases := []struct {
		name string
		n    int
	}{
		{"零行", 0},
		{"单行", 1},
		{"多行", 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			srv := &Server{metrics: NewMetrics(reg)}
			before := readCounter(t, reg, "widb_writes_total", "result", MetricWriteSuccess)
			srv.writeSuccessInc(tc.n)
			after := readCounter(t, reg, "widb_writes_total", "result", MetricWriteSuccess)
			if got := after - before; int(got) != tc.n {
				t.Errorf("writeSuccessInc(%d) 增量 = %v, want %d", tc.n, got, tc.n)
			}
		})
	}
}

// TestMetricLabelConstants 验证 Metric* 常量值与 Prometheus 指标注册时使用的
// 字符串一致，避免 label 漂移导致指标维度分裂（被分到不同的 series）。
func TestMetricLabelConstants(t *testing.T) {
	// 与 metrics.go initLabels 中 WithLabelValues(...) 的字符串一一对应
	want := map[string]string{
		"MetricQuerySuccess":       "success",
		"MetricQueryParseError":    "parse_error",
		"MetricQueryAnalyzeError":  "analyze_error",
		"MetricQueryExecuteError":  "execute_error",
		"MetricWriteSuccess":       "success",
		"MetricWriteTableNotFound": "table_not_found",
		"MetricWriteConvertError":  "convert_error",
		"MetricWriteError":         "write_error",
	}
	got := map[string]string{
		"MetricQuerySuccess":       MetricQuerySuccess,
		"MetricQueryParseError":    MetricQueryParseError,
		"MetricQueryAnalyzeError":  MetricQueryAnalyzeError,
		"MetricQueryExecuteError":  MetricQueryExecuteError,
		"MetricWriteSuccess":       MetricWriteSuccess,
		"MetricWriteTableNotFound": MetricWriteTableNotFound,
		"MetricWriteConvertError":  MetricWriteConvertError,
		"MetricWriteError":         MetricWriteError,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("常量 %s = %q, want %q（与 metrics.go 中 label 字符串漂移）", k, got[k], v)
		}
	}
}
