package server

import (
	"fmt"
	"sort"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/query"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// handleDelete 执行 DELETE 语句：扫描表中所有行，按 WHERE 过滤后删除匹配行。
// 无 WHERE 子句时删除全表数据（保留表结构）。
func (s *Server) handleDelete(del *query.DeleteStatement) (*Response, error) {
	if _, err := s.catalog.GetTable(del.Table); err != nil {
		s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("表不存在: %v", err)}, nil
	}

	eng := s.adapter.engineForTable(del.Table)
	entries := eng.ScanRange("", "\xff\xff\xff\xff")

	deleted := 0
	for _, entry := range entries {
		if !query.EvalRowPredicate(del.Where, entry.Value.Columns) {
			continue
		}
		if delErr := eng.Delete(entry.Key); delErr != nil {
			s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
			return &Response{Code: -1, Message: fmt.Sprintf("删除错误: %v", delErr)}, nil
		}
		deleted++
	}

	s.metrics.QueriesTotal.WithLabelValues("success").Inc()
	return &Response{Code: 0, Rows: deleted}, nil
}

// handleUpdate 执行 UPDATE 语句：扫描表中所有行，按 WHERE 过滤后对匹配行
// 应用 SET 赋值并重新写入。若更新导致主键变更且新主键已存在，返回冲突错误。
func (s *Server) handleUpdate(upd *query.UpdateStatement) (*Response, error) {
	tbl, err := s.catalog.GetTable(upd.Table)
	if err != nil {
		s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("表不存在: %v", err)}, nil
	}

	eng := s.adapter.engineForTable(upd.Table)
	colTypes := tbl.ColTypeMap()
	entries := eng.ScanRange("", "\xff\xff\xff\xff")

	updated := 0
	for _, entry := range entries {
		if !query.EvalRowPredicate(upd.Where, entry.Value.Columns) {
			continue
		}
		newValues, uErr := s.applyUpdateAssignments(entry, upd.Assignments, colTypes)
		if uErr != nil {
			s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
			return &Response{Code: -1, Message: uErr.Error()}, nil
		}
		newKey, keyErr := buildPrimaryKeyFromValues(tbl, newValues)
		if keyErr != nil {
			s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
			return &Response{Code: -1, Message: keyErr.Error()}, nil
		}
		if newKey != entry.Key {
			if conflictErr := checkPKConflict(eng, newKey); conflictErr != nil {
				s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
				return &Response{Code: -1, Message: conflictErr.Error()}, nil
			}
			_ = eng.Delete(entry.Key)
		}
		if wErr := eng.Write(newKey, newValues); wErr != nil {
			s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
			return &Response{Code: -1, Message: fmt.Sprintf("写入错误: %v", wErr)}, nil
		}
		updated++
	}

	s.metrics.QueriesTotal.WithLabelValues("success").Inc()
	return &Response{Code: 0, Rows: updated}, nil
}

// applyUpdateAssignments 将 SET 赋值应用到行数据上，返回更新后的值 map。
// 未被 SET 覆盖的列保留原值。
func (s *Server) applyUpdateAssignments(
	entry storage.ScanEntry, assignments []query.UpdateAssignment,
	colTypes map[string]common.DataType,
) (map[string]common.Value, error) {
	newValues := make(map[string]common.Value, len(entry.Value.Columns))
	for k, v := range entry.Value.Columns {
		newValues[k] = v
	}
	for _, a := range assignments {
		val, err := query.EvalExprOnRow(a.Value, entry.Value.Columns)
		if err != nil {
			return nil, fmt.Errorf("列 %s 求值错误: %w", a.Column, err)
		}
		if typ, ok := colTypes[a.Column]; ok && val.Valid && val.Typ != typ {
			val = coerceValueByType(val, typ)
		}
		newValues[a.Column] = val
	}
	return newValues, nil
}

// checkPKConflict 检查主键是否已存在，存在则返回冲突错误。
// 通过引擎的 Get 接口检查；不支持 Get 的引擎（无该接口）则跳过检查。
// INSERT 与 UPDATE 主键变更路径共享此实现。
func checkPKConflict(eng TableEngine, key string) error {
	if getter, ok := eng.(interface {
		Get(string) (storage.Row, bool)
	}); ok {
		if _, exists := getter.Get(key); exists {
			return fmt.Errorf("PRIMARY KEY CONFLICT: key %q 已存在", key)
		}
	}
	return nil
}

// handleDropTable 执行 DROP TABLE 语句：从 catalog 中删除表元数据，
// 并注销对应的存储引擎（LSM 表的独立引擎或内存引擎）。
func (s *Server) handleDropTable(dt *query.DropTableStatement) (*Response, error) {
	if _, err := s.catalog.GetTable(dt.Table); err != nil {
		if dt.IfExists {
			s.metrics.QueriesTotal.WithLabelValues("success").Inc()
			return &Response{Code: 0, Rows: 0}, nil
		}
		s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("表不存在: %v", err)}, nil
	}

	// 注销该表的独立引擎（LSM 或内存）；使用默认引擎的表无独立引擎可注销。
	_ = s.adapter.unregisterLSMEngine(dt.Table)
	_ = s.adapter.unregisterMemoryEngine(dt.Table)

	if err := s.catalog.DropTable(dt.Table); err != nil {
		s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("删除表错误: %v", err)}, nil
	}

	s.metrics.QueriesTotal.WithLabelValues("success").Inc()
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

	s.metrics.QueriesTotal.WithLabelValues("success").Inc()
	return &Response{Code: 0, Columns: []string{"table"}, Data: rows, Rows: len(rows)}, nil
}

// DESCRIBE 语句返回的列名常量，避免 goconst 重复字符串告警。
const (
	descColField = "field"
	descColType  = "type"
	descColNull  = "null"
	descColKey   = "key"
)

// handleDescribe 执行 DESCRIBE 语句，返回表的列结构信息。
// 每行包含：field（列名）、type（类型）、null（是否可空）、key（是否为主键）。
func (s *Server) handleDescribe(desc *query.DescribeStatement) (*Response, error) {
	tbl, err := s.catalog.GetTable(desc.Table)
	if err != nil {
		s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("表不存在: %v", err)}, nil
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

	s.metrics.QueriesTotal.WithLabelValues("success").Inc()
	return &Response{
		Code:    0,
		Columns: []string{descColField, descColType, descColNull, descColKey},
		Data:    rows,
		Rows:    len(rows),
	}, nil
}
