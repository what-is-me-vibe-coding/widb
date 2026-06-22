package server

import (
	"sort"

	"github.com/what-is-me-vibe-coding/test-db/pkg/query"
)

// DESCRIBE 语句返回的列名常量，避免 goconst 重复字符串告警。
const (
	descColField = "field"
	descColType  = "type"
	descColNull  = "null"
	descColKey   = "key"
)

// handleDropTable 执行 DROP TABLE 语句：从 catalog 中删除表元数据，
// 并注销对应的存储引擎（LSM 表的独立引擎或内存引擎）。
func (s *Server) handleDropTable(dt *query.DropTableStatement) (*Response, error) {
	if _, err := s.catalog.GetTable(dt.Table); err != nil {
		if dt.IfExists {
			s.querySuccessInc()
			return &Response{Code: 0, Rows: 0}, nil
		}
		return s.queryErrResp(MetricQueryExecuteError, "表不存在: %v", err), nil
	}

	// 注销该表的独立引擎（LSM 或内存）；使用默认引擎的表无独立引擎可注销。
	_ = s.adapter.unregisterLSMEngine(dt.Table)
	_ = s.adapter.unregisterMemoryEngine(dt.Table)

	if err := s.catalog.DropTable(dt.Table); err != nil {
		return s.queryErrResp(MetricQueryExecuteError, "删除表错误: %v", err), nil
	}

	s.querySuccessInc()
	return &Response{Code: 0, Rows: 0}, nil
}

// handleShowTables 执行 SHOW TABLES 语句，返回当前数据库中所有表名列表。
func (s *Server) handleShowTables() (*Response, error) {
	snap := s.catalog.Snapshot()
	names := make([]string, 0, len(snap.Tables))
	for name := range snap.Tables {
		names = append(names, name)
	}
	sort.Strings(names)

	rows := make([]map[string]any, 0, len(names))
	for _, name := range names {
		rows = append(rows, map[string]any{"table": name})
	}

	s.querySuccessInc()
	return &Response{Code: 0, Columns: []string{"table"}, Data: rows, Rows: len(rows)}, nil
}

// handleDescribe 执行 DESCRIBE 语句，返回表的列结构信息。
// 每行包含：field（列名）、type（类型）、null（是否可空）、key（是否为主键）。
func (s *Server) handleDescribe(desc *query.DescribeStatement) (*Response, error) {
	tbl, err := s.catalog.GetTable(desc.Table)
	if err != nil {
		return s.queryErrResp(MetricQueryExecuteError, "表不存在: %v", err), nil
	}

	pkSet := make(map[string]bool, len(tbl.PrimaryKey))
	for _, pk := range tbl.PrimaryKey {
		pkSet[pk] = true
	}

	rows := make([]map[string]any, 0, len(tbl.Columns))
	for _, col := range tbl.Columns {
		rows = append(rows, map[string]any{
			descColField: col.Name,
			descColType:  col.Type.String(),
			descColNull:  col.Nullable,
			descColKey:   pkSet[col.Name],
		})
	}

	s.querySuccessInc()
	return &Response{
		Code:    0,
		Columns: []string{descColField, descColType, descColNull, descColKey},
		Data:    rows,
		Rows:    len(rows),
	}, nil
}
