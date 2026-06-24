// Package server 慢查询运维 HTTP 端点。
//
// 端点：
//   - GET /admin/slow-queries：返回当前 SlowQueryLog 内的全部记录（按时间倒序），
//     同时回显当前阈值与容量，便于运维判断是否需要调整阈值。
//
// 设计要点：
//   - 与 /admin/stats 一致：仅支持 GET，方法错误返回 405。
//   - 慢查询日志未启用时（threshold<=0）仍返回 200 + 空列表，配置回显 threshold=0
//     表示禁用；避免运维误判为「接口异常」。
//   - 响应字段顺序保持稳定：Code、Message、Config、Queries。外部脚本可按字段名解析。
package server

import (
	"net/http"
	"time"
)

// admin slow-queries 端点的常量定义。
const (
	// adminErrSlowQueriesBadMethod 表示 /admin/slow-queries 仅接受 GET 方法。
	adminErrSlowQueriesBadMethod = "仅支持 GET 方法"
	// adminErrSlowQueriesNoServer 表示内部状态缺失（s 为 nil）。
	adminErrSlowQueriesNoServer = "admin slow-queries 失败: server 未就绪"
	// adminMsgSlowQueriesOK 是 /admin/slow-queries 成功时的标准消息。
	adminMsgSlowQueriesOK = "slow queries 查询成功"
)

// slowQueryConfigView 是 /admin/slow-queries 响应里的配置回显块。
// 与 config.ServerConfig 解耦，避免对外暴露全部字段；新增字段请追加在末尾。
type slowQueryConfigView struct {
	// Enabled 反映阈值是否 > 0，便于运维一眼判断当前是否启用。
	Enabled bool `json:"enabled"`
	// ThresholdMS 是当前慢查询判定阈值（毫秒）。0 表示禁用。
	ThresholdMS int `json:"threshold_ms"`
	// Capacity 是当前环形缓冲容量。
	Capacity int `json:"capacity"`
}

// slowQueryEntryView 是单条慢查询记录的对外视图。
// Timestamp 序列化为 RFC3339Nano 字符串，Duration 转为毫秒便于人读；
// 内部 SlowQueryRecord.Duration 仍为 time.Duration，便于 unit test 直接断言。
type slowQueryEntryView struct {
	Timestamp  string        `json:"timestamp"`
	Duration   time.Duration `json:"duration_ns"`
	DurationMS float64       `json:"duration_ms"`
	Source     string        `json:"source"`
	SQL        string        `json:"sql"`
	Error      string        `json:"error,omitempty"`
}

// slowQueriesResponse 是 /admin/slow-queries 的统一 JSON 响应。
type slowQueriesResponse struct {
	Code    int                  `json:"code"`
	Message string               `json:"message,omitempty"`
	Config  slowQueryConfigView  `json:"config"`
	Queries []slowQueryEntryView `json:"queries"`
}

// handleAdminSlowQueries 处理 GET /admin/slow-queries 请求：返回当前慢查询环形缓冲内的全部记录。
// 日志未启用时仍返回 200 + 空 Queries，Config.Enabled=false 即可识别。
func (s *Server) handleAdminSlowQueries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, &adminResponse{
			Code:    -1,
			Message: adminErrSlowQueriesBadMethod,
		})
		return
	}
	if s == nil {
		writeJSON(w, http.StatusInternalServerError, &adminResponse{
			Code:    -1,
			Message: adminErrSlowQueriesNoServer,
		})
		return
	}
	resp := s.collectSlowQueries()
	writeJSON(w, http.StatusOK, resp)
}

// collectSlowQueries 构造 /admin/slow-queries 响应内容。
// 与 HTTP 协议无关，便于测试与未来 CLI 复用。
// slowQueries 为 nil 时仍返回合法响应（Config.Enabled=false, Queries=空切片）。
func (s *Server) collectSlowQueries() *slowQueriesResponse {
	cfg := slowQueryConfigView{
		Enabled:     s.slowQueries != nil && s.slowQueries.Enabled(),
		ThresholdMS: 0,
		Capacity:    0,
	}
	if s.slowQueries != nil {
		cfg.ThresholdMS = int(s.slowQueries.Threshold() / time.Millisecond)
		cfg.Capacity = s.slowQueries.Capacity()
	}
	records := []SlowQueryRecord{}
	if s.slowQueries != nil {
		records = s.slowQueries.Snapshot()
	}
	entries := make([]slowQueryEntryView, 0, len(records))
	for _, r := range records {
		entries = append(entries, slowQueryEntryView{
			Timestamp:  r.Timestamp.Format(time.RFC3339Nano),
			Duration:   r.Duration,
			DurationMS: float64(r.Duration) / float64(time.Millisecond),
			Source:     string(r.Source),
			SQL:        r.SQL,
			Error:      r.Error,
		})
	}
	return &slowQueriesResponse{
		Code:    0,
		Message: adminMsgSlowQueriesOK,
		Config:  cfg,
		Queries: entries,
	}
}
