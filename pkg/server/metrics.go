package server

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const namespace = "widb"

// Metrics 包含所有 Prometheus 监控指标。
type Metrics struct {
	QueriesTotal      *prometheus.CounterVec
	QueryDuration     *prometheus.HistogramVec
	WritesTotal       *prometheus.CounterVec
	WriteDuration     *prometheus.HistogramVec
	MemTableSize      prometheus.Gauge
	SegmentCount      *prometheus.GaugeVec
	L0SegmentCount    prometheus.Gauge
	WALSizeBytes      prometheus.Gauge
	ActiveConns       prometheus.Gauge
	FlushTotal        prometheus.Counter
	CompactTotal      prometheus.Counter
	WALCleanTotal     prometheus.Counter
	CacheHits         *prometheus.CounterVec
	CacheMisses       *prometheus.CounterVec
	CacheSizeBytes    *prometheus.GaugeVec
	CacheEntries      *prometheus.GaugeVec
	HTTPRequestsTotal *prometheus.CounterVec
	HTTPDuration      *prometheus.HistogramVec
	// SlowQueriesTotal 按来源协议统计被记录到 SlowQueryLog 的慢查询次数。
	// 与 widb_query_duration_seconds 互补：前者只统计超过阈值的慢查询，
	// 后者覆盖全量耗时分布。
	SlowQueriesTotal *prometheus.CounterVec
}

// NewMetrics 创建并注册所有 Prometheus 指标。
func NewMetrics(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	factory := promauto.With(reg)

	m := &Metrics{
		QueriesTotal:      newQueriesTotal(factory),
		QueryDuration:     newQueryDuration(factory),
		WritesTotal:       newWritesTotal(factory),
		WriteDuration:     newWriteDuration(factory),
		MemTableSize:      newGauge(factory, "memtable_size_bytes", "当前 MemTable 大小"),
		SegmentCount:      newSegmentCount(factory),
		L0SegmentCount:    newGauge(factory, "l0_segment_count", "L0 Segment 数量"),
		WALSizeBytes:      newGauge(factory, "wal_size_bytes", "WAL 文件大小"),
		ActiveConns:       newGauge(factory, "active_connections", "当前活跃连接数"),
		FlushTotal:        newCounter(factory, "flush_total", "MemTable 刷盘总次数"),
		CompactTotal:      newCounter(factory, "compact_total", "Compaction 总次数"),
		WALCleanTotal:     newCounter(factory, "wal_clean_total", "WAL 清理总次数"),
		CacheHits:         newCacheCounter(factory, "cache_hits_total", "缓存命中次数"),
		CacheMisses:       newCacheCounter(factory, "cache_misses_total", "缓存未命中次数"),
		CacheSizeBytes:    newCacheGauge(factory, "cache_size_bytes", "缓存占用字节数"),
		CacheEntries:      newCacheGauge(factory, "cache_entries", "缓存条目数"),
		HTTPRequestsTotal: newHTTPRequestsTotal(factory),
		HTTPDuration:      newHTTPDuration(factory),
		SlowQueriesTotal:  newSlowQueriesTotal(factory),
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
	m.CacheHits.WithLabelValues("block").Add(0)
	m.CacheHits.WithLabelValues("index").Add(0)
	m.CacheMisses.WithLabelValues("block").Add(0)
	m.CacheMisses.WithLabelValues("index").Add(0)
	m.CacheSizeBytes.WithLabelValues("block").Set(0)
	m.CacheSizeBytes.WithLabelValues("index").Set(0)
	m.CacheEntries.WithLabelValues("block").Set(0)
	m.CacheEntries.WithLabelValues("index").Set(0)
	// 预热 HTTP 耗时直方图：每个端点 + 方法在首次请求前可见
	for _, ep := range []string{"/query", "/write", "/health", "/admin/flush", "/admin/compact", "/admin/stats", "/admin/slow-queries", "other"} {
		m.HTTPDuration.WithLabelValues(ep, "GET").Observe(0)
		m.HTTPDuration.WithLabelValues(ep, "POST").Observe(0)
	}
	// 预热慢查询计数器：每个来源协议在首次记录前可见
	for _, src := range []string{string(SlowQuerySourceHTTP), string(SlowQuerySourceTCP), string(SlowQuerySourcePGWire), string(SlowQuerySourceInProc)} {
		m.SlowQueriesTotal.WithLabelValues(src).Add(0)
	}
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

func newCacheCounter(f promauto.Factory, name, help string) *prometheus.CounterVec {
	return f.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: name, Help: help,
	}, []string{"cache"})
}

func newCacheGauge(f promauto.Factory, name, help string) *prometheus.GaugeVec {
	return f.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace, Name: name, Help: help,
	}, []string{"cache"})
}

// newHTTPRequestsTotal 创建 HTTP 请求计数器，标签为 endpoint/method/status。
// status 取响应状态码的百位（如 200→2xx、404→4xx、500→5xx），控制标签基数。
func newHTTPRequestsTotal(f promauto.Factory) *prometheus.CounterVec {
	return f.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "http_requests_total", Help: "HTTP 请求总数（按端点/方法/状态类别）",
	}, []string{"endpoint", "method", "status"})
}

// newHTTPDuration 创建 HTTP 请求耗时直方图，标签为 endpoint/method。
func newHTTPDuration(f promauto.Factory) *prometheus.HistogramVec {
	return f.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Name: "http_request_duration_seconds",
		Help:    "HTTP 请求耗时分布（按端点/方法）",
		Buckets: prometheus.DefBuckets,
	}, []string{"endpoint", "method"})
}

// newSlowQueriesTotal 创建慢查询计数器，标签为 source（来源协议）。
// 该指标与 SlowQueryLog 配合：SlowQueryLog 保存样本原文，Counter 提供聚合趋势。
func newSlowQueriesTotal(f promauto.Factory) *prometheus.CounterVec {
	return f.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "slow_queries_total", Help: "慢查询总数（按来源协议），执行耗时超过 server.slow_query_threshold_ms 时递增",
	}, []string{"source"})
}
