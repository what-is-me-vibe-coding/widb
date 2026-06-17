package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/query"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// handleInsert 执行 INSERT 语句，将行数据写入存储引擎。
func (s *Server) handleInsert(ins *query.InsertStatement) (*Response, error) {
	tbl, err := s.catalog.GetTable(ins.Table)
	if err != nil {
		s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("表不存在: %v", err)}, nil
	}

	colNames := ins.Columns
	if len(colNames) == 0 {
		colNames = make([]string, len(tbl.Columns))
		for i, c := range tbl.Columns {
			colNames[i] = c.Name
		}
	}

	colTypes := tbl.ColTypeMap()
	writeRows, err := buildInsertRows(ins, colNames, colTypes, tbl)
	if err != nil {
		s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
		return &Response{Code: -1, Message: err.Error()}, nil
	}

	if err := s.storage.WriteBatch(writeRows); err != nil {
		s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("写入错误: %v", err)}, nil
	}

	s.metrics.QueriesTotal.WithLabelValues("success").Inc()
	return &Response{Code: 0, Rows: len(writeRows)}, nil
}

// buildInsertRows 将 INSERT 语句的行表达式转换为存储引擎写入行。
func buildInsertRows(
	ins *query.InsertStatement, colNames []string,
	colTypes map[string]common.DataType, tbl *catalog.Table,
) ([]storage.WriteRow, error) {
	writeRows := make([]storage.WriteRow, 0, len(ins.Rows))
	for rowIdx, rowExprs := range ins.Rows {
		if len(rowExprs) != len(colNames) {
			return nil, fmt.Errorf("第 %d 行: 值数量 %d 与列数量 %d 不匹配", rowIdx+1, len(rowExprs), len(colNames))
		}

		values := make(map[string]common.Value, len(colNames))
		for i, expr := range rowExprs {
			val, convErr := exprToValue(expr)
			if convErr != nil {
				return nil, fmt.Errorf("第 %d 行第 %d 列: %w", rowIdx+1, i+1, convErr)
			}
			if typ, ok := colTypes[colNames[i]]; ok && val.Valid && val.Typ != typ {
				val = coerceValueByType(val, typ)
			}
			values[colNames[i]] = val
		}

		key, keyErr := buildPrimaryKeyFromValues(tbl, values)
		if keyErr != nil {
			return nil, fmt.Errorf("第 %d 行: %w", rowIdx+1, keyErr)
		}
		writeRows = append(writeRows, storage.WriteRow{Key: key, Values: values})
	}
	return writeRows, nil
}

// handleWrite 批量写入数据。
func (s *Server) handleWrite(req *WriteRequest) (*Response, error) {
	start := time.Now()

	tbl, err := s.catalog.GetTable(req.Table)
	if err != nil {
		s.metrics.WritesTotal.WithLabelValues("table_not_found").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("表不存在: %v", err)}, nil
	}

	writeRows := make([]storage.WriteRow, 0, len(req.Rows))
	for _, row := range req.Rows {
		key, values, convErr := s.convertWriteRow(tbl, row)
		if convErr != nil {
			s.metrics.WritesTotal.WithLabelValues("convert_error").Inc()
			return &Response{Code: -1, Message: fmt.Sprintf("行数据转换错误: %v", convErr)}, nil
		}
		writeRows = append(writeRows, storage.WriteRow{Key: key, Values: values})
	}

	if err := s.storage.WriteBatch(writeRows); err != nil {
		s.metrics.WritesTotal.WithLabelValues("write_error").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("写入错误: %v", err)}, nil
	}

	s.metrics.WritesTotal.WithLabelValues("success").Add(float64(len(writeRows)))
	s.metrics.WriteDuration.WithLabelValues("success").Observe(time.Since(start).Seconds())
	return &Response{Code: 0, Rows: len(writeRows)}, nil
}

// convertWriteRow 将 JSON 行数据转换为存储引擎需要的格式。
func (s *Server) convertWriteRow(
	tbl *catalog.Table, row map[string]any,
) (string, map[string]common.Value, error) {
	values := make(map[string]common.Value, len(row))
	colTypes := tbl.ColTypeMap()

	for colName, rawVal := range row {
		colType, ok := colTypes[colName]
		if !ok {
			continue
		}
		val, err := interfaceToValue(rawVal, colType)
		if err != nil {
			return "", nil, fmt.Errorf("列 %s: %w", colName, err)
		}
		values[colName] = val
	}

	key, err := s.buildPrimaryKey(tbl, row)
	if err != nil {
		return "", nil, err
	}

	return key, values, nil
}

// buildPrimaryKey 从行数据中提取主键值，拼接为存储 key。
// 使用 \x00 作为分隔符，避免主键值包含分隔符时产生碰撞。
func (s *Server) buildPrimaryKey(
	tbl *catalog.Table, row map[string]any,
) (string, error) {
	var builder strings.Builder
	for i, pk := range tbl.PrimaryKey {
		rawVal, ok := row[pk]
		if !ok {
			return "", fmt.Errorf("主键列 %s 缺失", pk)
		}
		if i > 0 {
			builder.WriteByte(0)
		}
		fmt.Fprintf(&builder, "%v", rawVal)
	}
	return builder.String(), nil
}

// exprToValue 从 INSERT VALUES 中的字面量表达式提取 common.Value。
func exprToValue(expr query.Expression) (common.Value, error) {
	switch e := expr.(type) {
	case *query.LiteralExpr:
		return e.Value, nil
	default:
		return common.NewNull(), fmt.Errorf("INSERT 仅支持字面量值，不支持 %T", expr)
	}
}

// coerceValueByType 按目标类型做简单转换，转换失败返回原值。
func coerceValueByType(val common.Value, target common.DataType) common.Value {
	if !val.Valid || val.Typ == target {
		return val
	}
	switch target {
	case common.TypeInt64:
		switch val.Typ {
		case common.TypeFloat64:
			return common.NewInt64(int64(val.Float64))
		case common.TypeBool:
			return common.NewInt64(val.Int64)
		}
	case common.TypeFloat64:
		switch val.Typ {
		case common.TypeInt64:
			return common.NewFloat64(float64(val.Int64))
		case common.TypeBool:
			return common.NewFloat64(float64(val.Int64))
		}
	case common.TypeBool:
		return common.NewBool(val.Int64 != 0)
	}
	return val
}

// buildPrimaryKeyFromValues 从列值 map 中提取主键并拼接为存储 key。
// 使用 \x00 作为分隔符，与 HTTP 写入路径保持一致。
func buildPrimaryKeyFromValues(tbl *catalog.Table, values map[string]common.Value) (string, error) {
	var builder strings.Builder
	for i, pk := range tbl.PrimaryKey {
		val, ok := values[pk]
		if !ok || !val.Valid {
			return "", fmt.Errorf("主键列 %s 缺失", pk)
		}
		if i > 0 {
			builder.WriteByte(0)
		}
		builder.WriteString(val.String())
	}
	return builder.String(), nil
}
