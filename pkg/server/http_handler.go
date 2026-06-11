package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// registerHTTPHandlers 注册 HTTP REST 路由。
func (s *Server) registerHTTPHandlers() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/query", s.httpQuery)
	mux.HandleFunc("/write", s.httpWrite)
	mux.HandleFunc("/health", s.httpHealth)

	gatherer := prometheus.DefaultGatherer
	if s.registry != nil {
		if reg, ok := s.registry.(*prometheus.Registry); ok {
			gatherer = reg
		}
	}
	mux.Handle("/metrics", promhttp.HandlerFor(
		gatherer,
		promhttp.HandlerOpts{EnableOpenMetrics: true},
	))
	return mux
}

// httpQuery 处理 POST /query 请求，执行 SQL 查询。
func (s *Server) httpQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, &Response{
			Code: -1, Message: "仅支持 POST 方法",
		})
		return
	}

	var req QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, &Response{
			Code: -1, Message: fmt.Sprintf("解析请求体失败: %v", err),
		})
		return
	}

	resp, err := s.handleQuery(&req)
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

// httpWrite 处理 POST /write 请求，批量写入数据。
func (s *Server) httpWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, &Response{
			Code: -1, Message: "仅支持 POST 方法",
		})
		return
	}

	var req WriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, &Response{
			Code: -1, Message: fmt.Sprintf("解析请求体失败: %v", err),
		})
		return
	}

	resp, err := s.handleWrite(&req)
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

// httpHealth 处理 GET /health 请求，返回健康状态。
func (s *Server) httpHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, &Response{
			Code: -1, Message: "仅支持 GET 方法",
		})
		return
	}

	health := map[string]any{
		"status":    "ok",
		"timestamp": time.Now().Format(time.RFC3339Nano),
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
