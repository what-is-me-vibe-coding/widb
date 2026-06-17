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

// handleQuery 执行 SQL 查询。
func (s *Server) handleQuery(req *QueryRequest) (*Response, error) {
	start := time.Now()
	defer func() {
		s.metrics.QueryDuration.WithLabelValues("sql").Observe(time.Since(start).Seconds())
	}()

	stmt, err := s.parser.Parse(req.SQL)
	if err != nil {
		s.metrics.QueriesTotal.WithLabelValues("parse_error").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("SQL 解析错误: %v", err)}, nil
	}

	// DDL/DML 语句（CREATE TABLE / INSERT）由服务层直接执行，
	// 不走 analyzer/executor 路径（它们仅处理 SELECT 这类只读查询）。
	switch st := stmt.(type) {
	case *query.CreateTableStatement:
		return s.handleCreateTable(st)
	case *query.InsertStatement:
		return s.handleInsert(st)
	}

	plan, err := s.analyzer.Analyze(stmt)
	if err != nil {
		s.metrics.QueriesTotal.WithLabelValues("analyze_error").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("SQL 分析错误: %v", err)}, nil
	}

	optimized := s.optimizer.Optimize(plan)

	chunks, err := s.executor.Execute(optimized)
	if err != nil {
		s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("SQL 执行错误: %v", err)}, nil
	}

	// 从查询计划的 Schema 中提取列名，用于 JSON 响应的 key
	var colNames []string
	if schema := optimized.Schema(); len(schema) > 0 {
		colNames = make([]string, len(schema))
		for i, col := range schema {
			colNames[i] = col.Name
		}
	}
	data := chunksToRows(chunks, colNames)
	totalRows := countRows(chunks)

	s.metrics.QueriesTotal.WithLabelValues("success").Inc()
	return &Response{Code: 0, Data: data, Rows: totalRows, Columns: colNames}, nil
}

// handleInsert 执行 INSERT 语句，将行数据写入存储引擎。
func (s *Server) handleInsert(ins *query.InsertStatement) (*Response, error) {
	tbl, err := s.catalog.GetTable(ins.Table)
	if err != nil {
		s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("表不存在: %v", err)}, nil
	}

	// 确定列顺序：显式指定列时使用之，否则使用表定义的全部列
	colNames := ins.Columns
	if len(colNames) == 0 {
		colNames = make([]string, len(tbl.Columns))
		for i, c := range tbl.Columns {
			colNames[i] = c.Name
		}
	}

	colTypes := tbl.ColTypeMap()
	writeRows := make([]storage.WriteRow, 0, len(ins.Rows))
	for rowIdx, rowExprs := range ins.Rows {
		if len(rowExprs) != len(colNames) {
			s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
			return &Response{Code: -1, Message: fmt.Sprintf("第 %d 行: 值数量 %d 与列数量 %d 不匹配", rowIdx+1, len(rowExprs), len(colNames))}, nil
		}

		values := make(map[string]common.Value, len(colNames))
		for i, expr := range rowExprs {
			val, convErr := exprToValue(expr)
			if convErr != nil {
				s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
				return &Response{Code: -1, Message: fmt.Sprintf("第 %d 行第 %d 列: %v", rowIdx+1, i+1, convErr)}, nil
			}
			// 按表定义的类型做必要转换
			if typ, ok := colTypes[colNames[i]]; ok && val.Valid && val.Typ != typ {
				val = coerceValueByType(val, typ)
			}
			values[colNames[i]] = val
		}

		key, keyErr := buildPrimaryKeyFromValues(tbl, values)
		if keyErr != nil {
			s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
			return &Response{Code: -1, Message: fmt.Sprintf("第 %d 行: %v", rowIdx+1, keyErr)}, nil
		}
		writeRows = append(writeRows, storage.WriteRow{Key: key, Values: values})
	}

	if err := s.adapter.engineForTable(ins.Table).WriteBatch(writeRows); err != nil {
		s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("写入错误: %v", err)}, nil
	}

	s.metrics.QueriesTotal.WithLabelValues("success").Inc()
	return &Response{Code: 0, Rows: len(writeRows)}, nil
}

// buildColumnMetaFromCatalog 从 catalog 的第一张表构建存储引擎列元数据。
// 当前存储引擎为单表模型，取任意一张表即可。
func buildColumnMetaFromCatalog(cat *catalog.Catalog) []storage.ColumnMeta {
	snap := cat.Snapshot()
	for _, tbl := range snap.Tables {
		return buildColumnMeta(tbl.Columns)
	}
	return nil
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

	if err := s.adapter.engineForTable(req.Table).WriteBatch(writeRows); err != nil {
		s.metrics.WritesTotal.WithLabelValues("write_error").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("写入错误: %v", err)}, nil
	}

	s.metrics.WritesTotal.WithLabelValues("success").Add(float64(len(writeRows)))
	s.metrics.WriteDuration.WithLabelValues("success").Observe(time.Since(start).Seconds())
	return &Response{Code: 0, Rows: len(writeRows)}, nil
}

// handleCreateTable 处理 CREATE TABLE 语句：在 catalog 中注册表，
// 并根据 ENGINE 选项创建对应的存储引擎（memory 或默认 LSM）。
// 若表已存在且未指定 IF NOT EXISTS，返回错误响应。
func (s *Server) handleCreateTable(ct *query.CreateTableStatement) (*Response, error) {
	cols := make([]catalog.ColumnDef, len(ct.Columns))
	for i, c := range ct.Columns {
		cols[i] = catalog.ColumnDef{
			Name:     c.Name,
			Type:     c.Type,
			Nullable: c.Nullable,
		}
	}
	opts := catalog.TableOptions{Engine: ct.Engine}

	if catalog.NormalizeEngine(ct.Engine) == catalog.EngineMemory {
		return s.createMemoryTable(ct, cols, opts)
	}

	// LSM 表：在 catalog 建表，并同步列元数据到存储引擎，使后台调度器自动刷盘能正确编码列。
	if err := s.catalog.CreateTable(ct.Table, cols, ct.PrimaryKey, opts); err != nil {
		return s.createTableErrorResponse(ct, err), nil
	}
	s.storage.SetColumnMeta(buildColumnMeta(cols))
	s.metrics.QueriesTotal.WithLabelValues("success").Inc()
	return &Response{Code: 0, Rows: 0}, nil
}

// createMemoryTable 创建内存引擎表。先注册内存引擎再在 catalog 建表，确保当表在
// catalog 中对外可见时内存引擎已注册，避免并发查询回退到默认 LSM 引擎导致数据写入
// 错误的引擎（修复 registerMemoryEngine 与 catalog.CreateTable 之间的竞态）。
// 若 catalog 建表失败则回滚（注销内存引擎）。
func (s *Server) createMemoryTable(ct *query.CreateTableStatement, cols []catalog.ColumnDef, opts catalog.TableOptions) (*Response, error) {
	// 表已存在时直接返回，避免对已存在的表（如 LSM 表）误注册内存引擎造成短暂错误路由。
	if _, err := s.catalog.GetTable(ct.Table); err == nil {
		return s.tableAlreadyExistsResponse(ct), nil
	}
	eng := createMemoryEngine(cols)
	if err := s.adapter.registerMemoryEngine(ct.Table, eng); err != nil {
		// 该表已注册过内存引擎（如并发建表），视为已存在。
		return s.tableAlreadyExistsResponse(ct), nil
	}
	if err := s.catalog.CreateTable(ct.Table, cols, ct.PrimaryKey, opts); err != nil {
		_ = s.adapter.unregisterMemoryEngine(ct.Table) // 建表失败，回滚内存引擎注册
		return s.createTableErrorResponse(ct, err), nil
	}
	s.metrics.QueriesTotal.WithLabelValues("success").Inc()
	return &Response{Code: 0, Rows: 0}, nil
}

// createTableErrorResponse 根据建表错误与 IF NOT EXISTS 语义构造响应。
func (s *Server) createTableErrorResponse(ct *query.CreateTableStatement, err error) *Response {
	if ct.IfNotExists && strings.Contains(err.Error(), "already exists") {
		s.metrics.QueriesTotal.WithLabelValues("success").Inc()
		return &Response{Code: 0, Rows: 0}
	}
	s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
	return &Response{Code: -1, Message: fmt.Sprintf("创建表错误: %v", err)}
}

// tableAlreadyExistsResponse 根据 IF NOT EXISTS 语义返回“表已存在”的响应。
func (s *Server) tableAlreadyExistsResponse(ct *query.CreateTableStatement) *Response {
	if ct.IfNotExists {
		s.metrics.QueriesTotal.WithLabelValues("success").Inc()
		return &Response{Code: 0, Rows: 0}
	}
	s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
	return &Response{Code: -1, Message: fmt.Sprintf("创建表错误: table %q already exists", ct.Table)}
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
