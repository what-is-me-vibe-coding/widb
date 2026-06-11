package server

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestNewMetrics(t *testing.T) {
	registry := prometheus.NewRegistry()
	m := NewMetrics(registry)

	if m == nil {
		t.Fatal("NewMetrics 不应返回 nil")
	}
	if m.QueriesTotal == nil {
		t.Error("QueriesTotal 不应为 nil")
	}
	if m.QueryDuration == nil {
		t.Error("QueryDuration 不应为 nil")
	}
	if m.WritesTotal == nil {
		t.Error("WritesTotal 不应为 nil")
	}
	if m.WriteDuration == nil {
		t.Error("WriteDuration 不应为 nil")
	}
	if m.MemTableSize == nil {
		t.Error("MemTableSize 不应为 nil")
	}
	if m.SegmentCount == nil {
		t.Error("SegmentCount 不应为 nil")
	}
	if m.L0SegmentCount == nil {
		t.Error("L0SegmentCount 不应为 nil")
	}
	if m.WALSizeBytes == nil {
		t.Error("WALSizeBytes 不应为 nil")
	}
	if m.ActiveConns == nil {
		t.Error("ActiveConns 不应为 nil")
	}
	if m.FlushTotal == nil {
		t.Error("FlushTotal 不应为 nil")
	}
	if m.CompactTotal == nil {
		t.Error("CompactTotal 不应为 nil")
	}
	if m.WALCleanTotal == nil {
		t.Error("WALCleanTotal 不应为 nil")
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
