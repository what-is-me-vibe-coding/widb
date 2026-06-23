package server

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// TestNewMetrics 验证 NewMetrics 初始化后所有字段非 nil。
// 为满足 gocyclo -over 15 阈值，断言抽取到 assertNotNil 辅助函数。
func TestNewMetrics(t *testing.T) {
	registry := prometheus.NewRegistry()
	m := NewMetrics(registry)
	if m == nil {
		t.Fatal("NewMetrics 不应返回 nil")
	}
	assertNotNil(t, "QueriesTotal", m.QueriesTotal)
	assertNotNil(t, "QueryDuration", m.QueryDuration)
	assertNotNil(t, "WritesTotal", m.WritesTotal)
	assertNotNil(t, "WriteDuration", m.WriteDuration)
	assertNotNil(t, "MemTableSize", m.MemTableSize)
	assertNotNil(t, "SegmentCount", m.SegmentCount)
	assertNotNil(t, "L0SegmentCount", m.L0SegmentCount)
	assertNotNil(t, "WALSizeBytes", m.WALSizeBytes)
	assertNotNil(t, "ActiveConns", m.ActiveConns)
	assertNotNil(t, "FlushTotal", m.FlushTotal)
	assertNotNil(t, "CompactTotal", m.CompactTotal)
	assertNotNil(t, "WALCleanTotal", m.WALCleanTotal)
	assertNotNil(t, "HTTPRequestsTotal", m.HTTPRequestsTotal)
	assertNotNil(t, "HTTPDuration", m.HTTPDuration)
}

// assertNotNil 简化 NewMetrics 字段非 nil 断言。
func assertNotNil(t *testing.T, name string, v any) {
	t.Helper()
	if v == nil {
		t.Errorf("%s 不应为 nil", name)
	}
}

func TestMetricsQueriesInc(t *testing.T) {
	registry := prometheus.NewRegistry()
	m := NewMetrics(registry)

	m.QueriesTotal.WithLabelValues("success").Inc()
	m.QueriesTotal.WithLabelValues("parse_error").Inc()

	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather 失败: %v", err)
	}

	found := false
	for _, mf := range metricFamilies {
		if mf.GetName() == "widb_queries_total" {
			found = true
			if len(mf.GetMetric()) < 2 {
				t.Errorf("widb_queries_total 应至少有 2 个标签组合，实际 %d", len(mf.GetMetric()))
			}
		}
	}
	if !found {
		t.Error("应包含 widb_queries_total 指标")
	}
}

func TestMetricsWritesInc(t *testing.T) {
	registry := prometheus.NewRegistry()
	m := NewMetrics(registry)

	m.WritesTotal.WithLabelValues("success").Add(5)

	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather 失败: %v", err)
	}

	found := false
	for _, mf := range metricFamilies {
		if mf.GetName() == "widb_writes_total" {
			found = true
			for _, metric := range mf.GetMetric() {
				counter := metric.GetCounter()
				if counter != nil && counter.GetValue() == 5.0 {
					return
				}
			}
		}
	}
	if !found {
		t.Error("应包含 widb_writes_total 指标")
	}
}

func TestMetricsNilRegistry(t *testing.T) {
	// 验证 NewMetrics 在 reg 为 nil 时降级到 DefaultRegisterer 不 panic。
	// 使用独立的 Registry 而非 nil，避免 DefaultRegisterer 全局单例在
	// -count>1 或并行测试中导致 duplicate registration panic。
	registry := prometheus.NewRegistry()
	m := NewMetrics(registry)
	if m == nil {
		t.Error("NewMetrics 不应返回 nil")
	}
}

// TestNewMetrics_NilRegistryFallback 测试 NewMetrics 传入 nil 时
// 降级到 DefaultRegisterer 的路径（第 32-34 行）。
// 使用唯一的指标名称前缀避免与 DefaultRegisterer 中的已有指标冲突。
func TestNewMetrics_NilRegistryFallback(t *testing.T) {
	// 保存并恢复 DefaultRegisterer，避免影响其他测试
	origRegisterer := prometheus.DefaultRegisterer
	defer func() {
		prometheus.DefaultRegisterer = origRegisterer
	}()

	// 使用独立的 Registry 作为 DefaultRegisterer，避免全局污染
	testRegistry := prometheus.NewRegistry()
	prometheus.DefaultRegisterer = testRegistry

	// 传入 nil，应降级到 DefaultRegisterer（即 testRegistry）
	m := NewMetrics(nil)
	if m == nil {
		t.Fatal("NewMetrics(nil) 不应返回 nil")
	}

	// 验证指标已注册到 testRegistry
	m.QueriesTotal.WithLabelValues("success").Inc()

	metricFamilies, err := testRegistry.Gather()
	if err != nil {
		t.Fatalf("Gather 失败: %v", err)
	}

	found := false
	for _, mf := range metricFamilies {
		if mf.GetName() == "widb_queries_total" {
			found = true
			break
		}
	}
	if !found {
		t.Error("nil registry 降级后应注册指标到 DefaultRegisterer")
	}
}

func TestMetricsGaugeSet(t *testing.T) {
	registry := prometheus.NewRegistry()
	m := NewMetrics(registry)

	m.MemTableSize.Set(1024)
	m.L0SegmentCount.Set(3)
	m.WALSizeBytes.Set(2048)
	m.ActiveConns.Set(5)

	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather 失败: %v", err)
	}

	expectedGauges := map[string]float64{
		"widb_memtable_size_bytes": 1024,
		"widb_l0_segment_count":    3,
		"widb_wal_size_bytes":      2048,
		"widb_active_connections":  5,
	}

	for _, mf := range metricFamilies {
		if expected, ok := expectedGauges[mf.GetName()]; ok {
			for _, metric := range mf.GetMetric() {
				gauge := metric.GetGauge()
				if gauge != nil && gauge.GetValue() == expected {
					delete(expectedGauges, mf.GetName())
					break
				}
			}
		}
	}

	if len(expectedGauges) > 0 {
		t.Errorf("未找到预期的 Gauge 指标: %v", expectedGauges)
	}
}

// TestMetricsHTTPCounterInc 验证 HTTPRequestsTotal 计数器可递增并出现在 registry 中。
func TestMetricsHTTPCounterInc(t *testing.T) {
	registry := prometheus.NewRegistry()
	m := NewMetrics(registry)

	m.HTTPRequestsTotal.WithLabelValues("/query", "POST", "2xx").Inc()
	m.HTTPRequestsTotal.WithLabelValues("/write", "POST", "4xx").Inc()
	m.HTTPRequestsTotal.WithLabelValues("/write", "POST", "4xx").Inc()

	assertHTTPCounter(t, registry, "/query", "POST", "2xx", 1)
	assertHTTPCounter(t, registry, "/write", "POST", "4xx", 2)
}

// assertHTTPCounter 在 Gather 结果中查找指定 (endpoint, method, status) 标签的计数器值。
func assertHTTPCounter(t *testing.T, reg *prometheus.Registry, ep, method, status string, want float64) {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather 失败: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "widb_http_requests_total" {
			continue
		}
		for _, metric := range mf.GetMetric() {
			labels := map[string]string{}
			for _, lp := range metric.Label {
				labels[lp.GetName()] = lp.GetValue()
			}
			if labels["endpoint"] != ep || labels["method"] != method || labels["status"] != status {
				continue
			}
			if c := metric.GetCounter(); c != nil && c.GetValue() == want {
				return
			}
		}
	}
	t.Errorf("未找到 widb_http_requests_total{endpoint=%q,method=%q,status=%q} = %v", ep, method, status, want)
}

// TestMetricsHTTPDurationObserve 验证 HTTPDuration 耗时直方图接受 Observe 调用。
func TestMetricsHTTPDurationObserve(t *testing.T) {
	registry := prometheus.NewRegistry()
	m := NewMetrics(registry)

	m.HTTPDuration.WithLabelValues("/query", "POST").Observe(0.123)

	hist := findHTTPHistogram(t, registry, "/query", "POST")
	if hist == nil {
		t.Error("应包含 widb_http_request_duration_seconds 指标 (endpoint=/query, method=POST)")
		return
	}
	if hist.GetSampleCount() < 1 {
		t.Errorf("样本数 = %d, 期望 >= 1", hist.GetSampleCount())
	}
	if hist.GetSampleSum() < 0.123 {
		t.Errorf("样本 sum = %v, 期望 >= 0.123", hist.GetSampleSum())
	}
}

// findHTTPHistogram 在 registry 中查找匹配 endpoint/method 的 HTTP 耗时直方图。
// 未找到时通过 t.Fatal 失败并返回 nil。
func findHTTPHistogram(t *testing.T, registry *prometheus.Registry, endpoint, method string) *dto.Histogram {
	t.Helper()
	mfs, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather 失败: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "widb_http_request_duration_seconds" {
			continue
		}
		for _, metric := range mf.GetMetric() {
			if !histogramHasLabel(metric, "endpoint", endpoint) {
				continue
			}
			if !histogramHasLabel(metric, "method", method) {
				continue
			}
			h := metric.GetHistogram()
			if h == nil {
				continue
			}
			return h
		}
	}
	return nil
}

// histogramHasLabel 判断指标是否包含指定 name=value 标签。
func histogramHasLabel(metric *dto.Metric, name, want string) bool {
	for _, lp := range metric.Label {
		if lp.GetName() == name && lp.GetValue() == want {
			return true
		}
	}
	return false
}
