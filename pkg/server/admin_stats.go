package server

import (
	"net/http"
	"sort"

	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage/memory"
)

// admin stats 端点的常量定义。
const (
	// adminErrStatsBadMethod 表示 /admin/stats 仅接受 GET 方法。
	adminErrStatsBadMethod = "仅支持 GET 方法"
	// adminErrStatsNoServer 表示内部状态缺失（s / catalog / adapter 为 nil）。
	adminErrStatsNoServer = "admin stats 失败: server 未就绪"
	// adminMsgStatsOK 是 /admin/stats 成功时的标准消息。
	adminMsgStatsOK = "stats 查询成功"
	// statsEngineLSM / statsEngineMemory 是响应里的引擎名归一化值。
	statsEngineLSM    = "lsm"
	statsEngineMemory = "memory"
)

// tableStatsItem 是 /admin/stats 响应中单张表的统计信息。
//
// 字段使用 omitempty 以便在内存引擎表上不输出 SegmentCount / L0SegmentCount
// / MemTableSize 等 LSM 专属字段，反之在 LSM 表上不输出冗余信息。
type tableStatsItem struct {
	Name           string   `json:"name"`
	Engine         string   `json:"engine"`
	Columns        int      `json:"columns"`
	PrimaryKey     []string `json:"primary_key,omitempty"`
	RowCount       int64    `json:"row_count"`
	SegmentCount   int      `json:"segment_count,omitempty"`
	L0SegmentCount int      `json:"l0_segment_count,omitempty"`
	ImmutableCount int      `json:"immutable_count,omitempty"`
	MemTableSize   int64    `json:"memtable_size,omitempty"`
	ActiveRowCount int64    `json:"active_row_count,omitempty"`
	ImmRowCount    int64    `json:"immutable_row_count,omitempty"`
}

// statsSummary 汇总全库的表级统计。
type statsSummary struct {
	TotalTables   int   `json:"total_tables"`
	LSMTables     int   `json:"lsm_tables"`
	MemoryTables  int   `json:"memory_tables"`
	TotalSegments int   `json:"total_segments"`
	TotalRows     int64 `json:"total_rows"`
}

// statsResponse 是 /admin/stats 的统一 JSON 响应。
// 字段顺序保持稳定以便外部脚本解析；新增字段请追加在末尾。
type statsResponse struct {
	Code    int              `json:"code"`
	Message string           `json:"message,omitempty"`
	Summary statsSummary     `json:"summary"`
	Tables  []tableStatsItem `json:"tables"`
}

// handleAdminStats 处理 GET /admin/stats 请求：返回当前数据库中每张表的
// 元信息（列、主键、引擎类型）与运行时统计（行数、Segment 数、MemTable 大小）。
//
// 实现细节：
//   - 遍历 catalog.Snapshot() 拿到全部表的元数据，避开 routingAdapter 的
//     注册顺序，确保「已建表但未注册引擎」等中间态仍能出现在响应里（运行
//     时字段留零值）。
//   - 对每张表尝试从 routingAdapter 取出对应引擎，并按类型断言调用
//     storage.Engine.Stats() / memory.Engine.Stats() 拿到运行时数据。
//   - 任何类型断言失败都安全降级（运行时字段为零值），不阻塞整体响应。
func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, &adminResponse{
			Code:    -1,
			Message: adminErrStatsBadMethod,
		})
		return
	}
	if s == nil || s.catalog == nil || s.adapter == nil {
		writeJSON(w, http.StatusInternalServerError, &adminResponse{
			Code:    -1,
			Message: adminErrStatsNoServer,
		})
		return
	}
	resp := s.collectStats()
	writeJSON(w, http.StatusOK, resp)
}

// collectStats 收集全库表级统计，按表名字典序输出。
// 与 HTTP 协议无关，便于测试与未来 CLI/SQL 复用。
func (s *Server) collectStats() *statsResponse {
	tables, summary := s.collectTableStats()
	return &statsResponse{
		Code:    0,
		Message: adminMsgStatsOK,
		Summary: summary,
		Tables:  tables,
	}
}

// collectTableStats 枚举表并组装统计结果与汇总。
// 复杂度隔离在此处，让 handleAdminStats 仅负责 HTTP 协议层校验。
func (s *Server) collectTableStats() ([]tableStatsItem, statsSummary) {
	names := s.sortedTableNames()
	items := make([]tableStatsItem, 0, len(names))
	summary := statsSummary{TotalTables: len(names)}

	for _, name := range names {
		tbl := s.catalog.Snapshot().Tables[name]
		item := s.buildTableStatsItem(tbl)
		s.applyEngineStats(&item, name)
		summary.TotalRows += item.RowCount
		switch item.Engine {
		case statsEngineMemory:
			summary.MemoryTables++
		default:
			summary.LSMTables++
			summary.TotalSegments += item.SegmentCount
		}
		items = append(items, item)
	}
	return items, summary
}

// sortedTableNames 返回 catalog 中按字典序排列的表名切片。
func (s *Server) sortedTableNames() []string {
	snap := s.catalog.Snapshot()
	names := make([]string, 0, len(snap.Tables))
	for name := range snap.Tables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// buildTableStatsItem 把 catalog.Table 翻译成 tableStatsItem（仅元数据部分）。
// 运行时统计在 applyEngineStats 阶段填充，便于按引擎类型分支。
func (s *Server) buildTableStatsItem(tbl *catalog.Table) tableStatsItem {
	return tableStatsItem{
		Name:       tbl.Name,
		Engine:     normalizeEngineName(tbl),
		Columns:    len(tbl.Columns),
		PrimaryKey: append([]string(nil), tbl.PrimaryKey...),
	}
}

// applyEngineStats 根据引擎类型从 routingAdapter 拉取运行时数据。
// 引擎未注册或类型断言失败时，所有运行时字段保持零值。
func (s *Server) applyEngineStats(item *tableStatsItem, name string) {
	switch item.Engine {
	case statsEngineMemory:
		if eng, ok := s.adapter.memEngines[name]; ok {
			if memEng, ok := eng.(*memory.Engine); ok {
				ms := memEng.Stats()
				item.RowCount = ms.RowCount
			}
		}
	default:
		if eng, ok := s.adapter.lsmEngines[name]; ok {
			if lsmEng, ok := eng.(*storage.Engine); ok {
				es := lsmEng.Stats()
				item.RowCount = es.RowCount
				item.SegmentCount = es.SegmentCount
				item.L0SegmentCount = es.L0SegmentCount
				item.ImmutableCount = es.ImmutableCount
				item.MemTableSize = es.MemTableSize
				item.ActiveRowCount = es.ActiveRowCount
				item.ImmRowCount = es.ImmutableRowCount
			}
		}
	}
}

// normalizeEngineName 把 catalog.Table.Engine 规整为小写字符串。
// 兼容历史数据中可能存在的 "LSM"/"Memory" 大小写混合形式。
// 当字段为空时视为默认 LSM 引擎。
func normalizeEngineName(tbl *catalog.Table) string {
	eng := tbl.Engine
	if eng == "" {
		return statsEngineLSM
	}
	switch eng {
	case statsEngineLSM, "LSM", "Lsm":
		return statsEngineLSM
	case statsEngineMemory, "Memory", "MEMORY":
		return statsEngineMemory
	default:
		// 未知值原样返回（小写化），方便调用方识别
		return eng
	}
}
