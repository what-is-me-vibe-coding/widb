package server

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const namespace = "widb"

// Metrics 包含所有 Prometheus 监控指标。
type Metrics struct {
	QueriesTotal   *prometheus.CounterVec
	QueryDuration  *prometheus.HistogramVec
	WritesTotal    *prometheus.CounterVec
	WriteDuration  *prometheus.HistogramVec
	MemTableSize   prometheus.Gauge
	SegmentCount   *prometheus.GaugeVec
	L0SegmentCount prometheus.Gauge
	WALSizeBytes   prometheus.Gauge
	ActiveConns    prometheus.Gauge
	FlushTotal     prometheus.Counter
	CompactTotal   prometheus.Counter
	WALCleanTotal  prometheus.Counter
}

// NewMetrics 创建并注册所有 Prometheus 指标。
func NewMetrics(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	factory := promauto.With(reg)

	m := &Metrics{
		QueriesTotal:   newQueriesTotal(factory),
		QueryDuration:  newQueryDuration(factory),
		WritesTotal:    newWritesTotal(factory),
		WriteDuration:  newWriteDuration(factory),
		MemTableSize:   newGauge(factory, "memtable_size_bytes", "当前 MemTable 大小"),
		SegmentCount:   newSegmentCount(factory),
		L0SegmentCount: newGauge(factory, "l0_segment_count", "L0 Segment 数量"),
		WALSizeBytes:   newGauge(factory, "wal_size_bytes", "WAL 文件大小"),
		ActiveConns:    newGauge(factory, "active_connections", "当前活跃连接数"),
		FlushTotal:     newCounter(factory, "flush_total", "MemTable 刷盘总次数"),
		CompactTotal:   newCounter(factory, "compact_total", "Compaction 总次数"),
		WALCleanTotal:  newCounter(factory, "wal_clean_total", "WAL 清理总次数"),
	}

	m.initLabels()
	return m
}

// initLabels 初始化标签组合，确保指标在未使用时也可见。
func (m *Metrics) initLabels() {
	m.QueriesTotal.WithLabelValues("success").Add(0)
	m.QueriesTotal.WithLabelValues("parse_error").Add(0)
	m.QueriesTotal.WithLabelValues("analyze_error").Add(0)
	m.QueriesTotal.WithLabelValues("execute_error").Add(0)
	m.QueryDuration.WithLabelValues("sql").Observe(0)
	m.WritesTotal.WithLabelValues("success").Add(0)
	m.WritesTotal.WithLabelValues("table_not_found").Add(0)
	m.WritesTotal.WithLabelValues("convert_error").Add(0)
	m.WritesTotal.WithLabelValues("write_error").Add(0)
	m.WriteDuration.WithLabelValues("success").Observe(0)
	m.SegmentCount.WithLabelValues("l0").Set(0)
	m.SegmentCount.WithLabelValues("l1").Set(0)
}

func newQueriesTotal(f promauto.Factory) *prometheus.CounterVec {
	return f.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "queries_total", Help: "查询总数",
	}, []string{"type"})
}

func newQueryDuration(f promauto.Factory) *prometheus.HistogramVec {
	return f.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Name: "query_duration_seconds",
		Help: "查询耗时分布", Buckets: prometheus.DefBuckets,
	}, []string{"type"})
}

func newWritesTotal(f promauto.Factory) *prometheus.CounterVec {
	return f.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "writes_total", Help: "写入总数",
	}, []string{"result"})
}

func newWriteDuration(f promauto.Factory) *prometheus.HistogramVec {
	return f.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Name: "write_duration_seconds",
		Help: "写入耗时分布", Buckets: prometheus.DefBuckets,
	}, []string{"result"})
}

func newSegmentCount(f promauto.Factory) *prometheus.GaugeVec {
	return f.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace, Name: "segment_count", Help: "Segment 数量",
	}, []string{"level"})
}

func newGauge(f promauto.Factory, name, help string) prometheus.Gauge {
	return f.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace, Name: name, Help: help,
	})
}

func newCounter(f promauto.Factory, name, help string) prometheus.Counter {
	return f.NewCounter(prometheus.CounterOpts{
		Namespace: namespace, Name: name, Help: help,
	})
}
