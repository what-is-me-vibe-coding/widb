package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	maxRequestBodySize = 10 << 20 // 10MB，限制 HTTP 请求体大小，防止 OOM
	msgPostOnly        = "仅支持 POST 方法"
	keyTimestamp       = "timestamp"
	// 已知 HTTP 端点：用于将 URL.Path 归一化到有限集合，
	// 避免攻击者通过大量随机路径撑爆 prometheus 标签基数。
	endpointOther = "other"
)

// registerHTTPHandlers 注册 HTTP REST 路由。
// 已知端点（/query、/write、/health、/admin/flush、/admin/compact、/admin/stats、/admin/slow-queries）
// 逐个注册以便指标中间件能按端点打标；其余路径统一归到 "other" 标签。
func (s *Server) registerHTTPHandlers() *http.ServeMux {
	mux := http.NewServeMux()
	// 业务端点逐个挂载，便于指标按 endpoint 打标
	mux.HandleFunc("/query", s.httpMetricsMiddleware("/query", s.httpQuery))
	mux.HandleFunc("/write", s.httpMetricsMiddleware("/write", s.httpWrite))
	mux.HandleFunc("/health", s.httpMetricsMiddleware("/health", s.httpHealth))
	mux.HandleFunc("/admin/flush", s.httpMetricsMiddleware("/admin/flush", s.handleAdminFlush))
	mux.HandleFunc("/admin/compact", s.httpMetricsMiddleware("/admin/compact", s.handleAdminCompact))
	mux.HandleFunc("/admin/stats", s.httpMetricsMiddleware("/admin/stats", s.handleAdminStats))
	mux.HandleFunc("/admin/slow-queries", s.httpMetricsMiddleware("/admin/slow-queries", s.handleAdminSlowQueries))
	// 兜底路由：未匹配路径归到 "other" 标签，避免任意 URL 撑爆基数
	mux.HandleFunc("/", s.httpMetricsMiddleware(endpointOther, s.httpNotFound))

	gatherer := prometheus.DefaultGatherer
	if s.registry != nil {
		if reg, ok := s.registry.(*prometheus.Registry); ok {
			gatherer = reg
		}
	}
	// /metrics 端点自身不打指标（避免 scrape 自身造成自递归）
	mux.Handle("/metrics", promhttp.HandlerFor(
		gatherer,
		promhttp.HandlerOpts{EnableOpenMetrics: true},
	))
	return mux
}

// httpMetricsMiddleware 返回一个 HTTP 中间件，统计请求耗时与状态码分布。
//
// 设计要点：
//   - 通过 statusWriter 包装 ResponseWriter，捕获 WriteHeader 写入的状态码用于打标。
//   - 状态码按百位归类（2xx/4xx/5xx）后再写入 Counter，控制标签基数。
//   - 已知端点由 endpoint 参数显式传入，未知端点统一打 "other" 标签。
//   - 异常情况下即便 handler panic 也通过 defer 写一次指标，便于观测 5xx 触发频率。
func (s *Server) httpMetricsMiddleware(endpoint string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if rec := recover(); rec != nil {
				// handler panic 时也保证指标落盘，状态码记 5xx
				sw.status = http.StatusInternalServerError
				s.observeHTTP(endpoint, r.Method, sw.status, time.Since(start))
				panic(rec)
			}
			s.observeHTTP(endpoint, r.Method, sw.status, time.Since(start))
		}()
		next(sw, r)
	}
}

// observeHTTP 写入 HTTP 计数与耗时指标。statusClass 由状态码的百位生成。
func (s *Server) observeHTTP(endpoint, method string, status int, dur time.Duration) {
	if s.metrics == nil {
		return
	}
	s.metrics.HTTPRequestsTotal.WithLabelValues(endpoint, method, statusClass(status)).Inc()
	s.metrics.HTTPDuration.WithLabelValues(endpoint, method).Observe(dur.Seconds())
}

// statusClass 把 HTTP 状态码归一为百位字符串（2xx/3xx/4xx/5xx）。
// 未知状态码（<100 或 >=1000）回退到 "5xx"，便于异常观测。
func statusClass(status int) string {
	if status < 100 || status >= 1000 {
		return "5xx"
	}
	return strconv.Itoa(status/100) + "xx"
}

// statusWriter 包装 http.ResponseWriter 以捕获实际写入的状态码。
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

// WriteHeader 记录首次写入的状态码；幂等保护避免被多次调用时覆盖。
func (sw *statusWriter) WriteHeader(code int) {
	if !sw.wroteHeader {
		sw.status = code
		sw.wroteHeader = true
	}
	sw.ResponseWriter.WriteHeader(code)
}

// Write 在尚未显式调用 WriteHeader 时默认为 200，与 net/http 行为一致。
func (sw *statusWriter) Write(b []byte) (int, error) {
	if !sw.wroteHeader {
		sw.status = http.StatusOK
		sw.wroteHeader = true
	}
	return sw.ResponseWriter.Write(b)
}

// handlePostJSON 是通用的 POST JSON 请求处理器，封装方法检查、请求解码和响应写入逻辑。
// handler 参数接收解码后的请求，返回响应和可能的内部错误。
func (s *Server) handlePostJSON(w http.ResponseWriter, r *http.Request, req any, handler func() (*Response, error)) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, &Response{
			Code: -1, Message: msgPostOnly,
		})
		return
	}

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBodySize)).Decode(req); err != nil {
		writeJSON(w, http.StatusBadRequest, &Response{
			Code: -1, Message: fmt.Sprintf("解析请求体失败: %v", err),
		})
		return
	}

	resp, err := handler()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, &Response{
			Code: -1, Message: fmt.Sprintf("内部错误: %v", err),
		})
		return
	}
	statusCode := http.StatusOK
	if resp.Code != 0 {
		statusCode = http.StatusBadRequest
	}
	writeJSON(w, statusCode, resp)
}

// httpQuery 处理 POST /query 请求，执行 SQL 查询。
func (s *Server) httpQuery(w http.ResponseWriter, r *http.Request) {
	var req QueryRequest
	s.handlePostJSON(w, r, &req, func() (*Response, error) {
		return s.handleQuery(&req)
	})
}

// httpWrite 处理 POST /write 请求，批量写入数据。
func (s *Server) httpWrite(w http.ResponseWriter, r *http.Request) {
	var req WriteRequest
	s.handlePostJSON(w, r, &req, func() (*Response, error) {
		return s.handleWrite(&req)
	})
}

// httpHealth 处理 GET /health 请求，返回健康状态。
func (s *Server) httpHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, &Response{
			Code: -1, Message: "仅支持 GET 方法",
		})
		return
	}

	health := map[string]any{
		"status":     "ok",
		keyTimestamp: time.Now().Format(time.RFC3339Nano),
	}

	// 附加调度器统计信息
	if stats, ok := s.storage.SchedulerStats(); ok {
		health["scheduler"] = map[string]any{
			"flush_count":     stats.FlushCount,
			"compact_count":   stats.CompactCount,
			"wal_clean_count": stats.WALCleanCount,
			"last_error":      stats.LastError,
		}
	}

	writeJSON(w, http.StatusOK, health)
}

// writeJSON 将响应以 JSON 格式写入 HTTP 响应。
func writeJSON(w http.ResponseWriter, statusCode int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("http: JSON encode error: %v", err)
	}
}

// httpNotFound 是兜底 404 处理器：所有未匹配的 URL 都汇聚到这里。
// 单独成函数便于指标中间件按 "other" 打标。
func (s *Server) httpNotFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, &Response{
		Code: -1, Message: fmt.Sprintf("未知端点: %s", r.URL.Path),
	})
}
