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
		QueriesTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "queries_total",
			Help:      "查询总数",
		}, []string{"type"}),
		QueryDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "query_duration_seconds",
			Help:      "查询耗时分布",
			Buckets:   prometheus.DefBuckets,
		}, []string{"type"}),
		WritesTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "writes_total",
			Help:      "写入总数",
		}, []string{"result"}),
		WriteDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "write_duration_seconds",
			Help:      "写入耗时分布",
			Buckets:   prometheus.DefBuckets,
		}, []string{"result"}),
		MemTableSize: factory.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "memtable_size_bytes",
			Help:      "当前 MemTable 大小",
		}),
		SegmentCount: factory.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "segment_count",
			Help:      "Segment 数量",
		}, []string{"level"}),
		L0SegmentCount: factory.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "l0_segment_count",
			Help:      "L0 Segment 数量",
		}),
		WALSizeBytes: factory.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "wal_size_bytes",
			Help:      "WAL 文件大小",
		}),
		ActiveConns: factory.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "active_connections",
			Help:      "当前活跃连接数",
		}),
		FlushTotal: factory.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "flush_total",
			Help:      "MemTable 刷盘总次数",
		}),
		CompactTotal: factory.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "compact_total",
			Help:      "Compaction 总次数",
		}),
		WALCleanTotal: factory.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "wal_clean_total",
			Help:      "WAL 清理总次数",
		}),
	}

	// 初始化标签组合，确保指标在未使用时也可见
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

	return m
}
