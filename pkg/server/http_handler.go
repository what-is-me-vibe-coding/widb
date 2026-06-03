package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"
)

// registerHTTPHandlers 注册 HTTP REST 路由。
func (s *Server) registerHTTPHandlers() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/query", s.httpQuery)
	mux.HandleFunc("/write", s.httpWrite)
	mux.HandleFunc("/health", s.httpHealth)
	mux.HandleFunc("/metrics", s.httpMetrics)
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

	resp, _ := s.handleQuery(&req)
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

	resp, _ := s.handleWrite(&req)
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

	health := map[string]interface{}{
		"status":    "ok",
		"timestamp": time.Now().Format(time.RFC3339Nano),
	}
	writeJSON(w, http.StatusOK, health)
}

// writeMetric 写入一条 Prometheus 指标行。
func writeMetric(w http.ResponseWriter, help, typ, line string) {
	_, _ = fmt.Fprint(w, help)
	_, _ = fmt.Fprint(w, typ)
	_, _ = fmt.Fprint(w, line)
}

// httpMetrics 处理 GET /metrics 请求，返回 Prometheus 格式的指标。
func (s *Server) httpMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "仅支持 GET 方法", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	writeMetric(w,
		"# HELP test_db_memtable_size 当前 MemTable 大小\n",
		"# TYPE test_db_memtable_size gauge\n",
		fmt.Sprintf("test_db_memtable_size %d\n", s.storage.MemTableSize()),
	)
	writeMetric(w,
		"# HELP test_db_segment_count Segment 数量\n",
		"# TYPE test_db_segment_count gauge\n",
		fmt.Sprintf("test_db_segment_count %d\n", s.storage.SegmentCount()),
	)
	writeMetric(w,
		"# HELP test_db_l0_segment_count L0 Segment 数量\n",
		"# TYPE test_db_l0_segment_count gauge\n",
		fmt.Sprintf("test_db_l0_segment_count %d\n", s.storage.L0SegmentCount()),
	)
	writeMetric(w,
		"# HELP test_db_go_goroutines 当前 goroutine 数量\n",
		"# TYPE test_db_go_goroutines gauge\n",
		fmt.Sprintf("test_db_go_goroutines %d\n", runtime.NumGoroutine()),
	)
	writeMetric(w,
		"# HELP test_db_go_heap_alloc_bytes Go 堆分配字节数\n",
		"# TYPE test_db_go_heap_alloc_bytes gauge\n",
		fmt.Sprintf("test_db_go_heap_alloc_bytes %d\n", m.HeapAlloc),
	)
}

// writeJSON 将响应以 JSON 格式写入 HTTP 响应。
func writeJSON(w http.ResponseWriter, statusCode int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(v)
}
