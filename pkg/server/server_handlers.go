package server

import (
	"fmt"
	"strconv"
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
		return s.queryErrResp(MetricQueryParseError, "SQL 解析错误: %v", err), nil
	}

	// DDL/DML 语句（CREATE TABLE / INSERT / UPDATE / DELETE / DROP TABLE /
	// SHOW TABLES / DESCRIBE）由服务层直接执行，
	// 不走 analyzer/executor 路径（它们仅处理 SELECT 这类只读查询）。
	switch st := stmt.(type) {
	case *query.CreateTableStatement:
		return s.handleCreateTable(st)
	case *query.InsertStatement:
		return s.handleInsert(st)
	case *query.UpdateStatement:
		return s.handleUpdate(st)
	case *query.DeleteStatement:
		return s.handleDelete(st)
	case *query.DropTableStatement:
		return s.handleDropTable(st)
	case *query.ShowTablesStatement:
		return s.handleShowTables()
	case *query.DescribeStatement:
		return s.handleDescribe(st)
	case *query.ExplainStatement:
		return s.handleExplain(st)
	}

	plan, err := s.analyzer.Analyze(stmt)
	if err != nil {
		return s.queryErrResp(MetricQueryAnalyzeError, "SQL 分析错误: %v", err), nil
	}

	optimized := s.optimizer.Optimize(plan)

	chunks, err := s.executor.Execute(optimized)
	if err != nil {
		return s.queryErrResp(MetricQueryExecuteError, "SQL 执行错误: %v", err), nil
	}

	// 从查询计划的 Schema 中提取列名与列类型，用于 JSON 响应的 key 与 pgwire 类型映射
	var colNames []string
	var colTypes []common.DataType
	if schema := optimized.Schema(); len(schema) > 0 {
		colNames = make([]string, len(schema))
		colTypes = make([]common.DataType, len(schema))
		for i, col := range schema {
			colNames[i] = col.Name
			colTypes[i] = col.Type
		}
	}
	data := chunksToRows(chunks, colNames)
	totalRows := countRows(chunks)

	s.querySuccessInc()
	return &Response{Code: 0, Data: data, Rows: totalRows, Columns: colNames, ColumnTypes: colTypes}, nil
}

// handleInsert 执行 INSERT 语句，将行数据写入存储引擎。
func (s *Server) handleInsert(ins *query.InsertStatement) (*Response, error) {
	tbl, err := s.catalog.GetTable(ins.Table)
	if err != nil {
		return s.queryErrResp(MetricQueryExecuteError, "表不存在: %v", err), nil
	}

	// 确定列顺序：显式指定列时使用之，否则使用表定义的全部列
	colNames := ins.Columns
	if len(colNames) == 0 {
		colNames = make([]string, len(tbl.Columns))
		for i, c := range tbl.Columns {
			colNames[i] = c.Name
		}
	}

	eng := s.adapter.engineForTable(ins.Table)
	colTypes := tbl.ColTypeMap()
	writeRows := make([]storage.WriteRow, 0, len(ins.Rows))
	for rowIdx, rowExprs := range ins.Rows {
		wr, rErr := s.buildInsertWriteRow(tbl, colNames, colTypes, rowExprs, rowIdx, eng)
		if rErr != nil {
			return s.queryErrResp(MetricQueryExecuteError, "%v", rErr), nil
		}
		writeRows = append(writeRows, wr)
	}

	if err := eng.WriteBatch(writeRows); err != nil {
		return s.queryErrResp(MetricQueryExecuteError, "写入错误: %v", err), nil
	}

	s.querySuccessInc()
	return &Response{Code: 0, Rows: len(writeRows)}, nil
}

// buildInsertWriteRow 构造单行 INSERT 的 WriteRow，包含列值转换、主键生成与冲突检查。
// 返回错误时附带面向用户的中文错误信息（含行号）。
func (s *Server) buildInsertWriteRow(
	tbl *catalog.Table, colNames []string, colTypes map[string]common.DataType,
	rowExprs []query.Expression, rowIdx int, eng TableEngine,
) (storage.WriteRow, error) {
	if len(rowExprs) != len(colNames) {
		return storage.WriteRow{}, fmt.Errorf("第 %d 行: 值数量 %d 与列数量 %d 不匹配", rowIdx+1, len(rowExprs), len(colNames))
	}

	values := make(map[string]common.Value, len(colNames))
	for i, expr := range rowExprs {
		val, convErr := exprToValue(expr)
		if convErr != nil {
			return storage.WriteRow{}, fmt.Errorf("第 %d 行第 %d 列: %v", rowIdx+1, i+1, convErr)
		}
		// 按表定义的类型做必要转换
		if typ, ok := colTypes[colNames[i]]; ok && val.Valid && val.Typ != typ {
			val = coerceValueByType(val, typ)
		}
		values[colNames[i]] = val
	}

	key, keyErr := buildPrimaryKeyFromValues(tbl, values)
	if keyErr != nil {
		return storage.WriteRow{}, fmt.Errorf("第 %d 行: %v", rowIdx+1, keyErr)
	}
	// 主键冲突检查：若 key 已存在则拒绝插入
	if conflictErr := checkPKConflict(eng, key); conflictErr != nil {
		return storage.WriteRow{}, fmt.Errorf("第 %d 行: %v", rowIdx+1, conflictErr)
	}
	return storage.WriteRow{Key: key, Values: values}, nil
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
		// FLOAT64 字面量存于 Float64 字段，直接读 Int64 会得到零值从而恒为 false。
		// 其余整数族类型（含 INT8/16/32/UINT64/DATE）统一存于 Int64 字段，按 Int64 判断。
		switch val.Typ {
		case common.TypeFloat64:
			return common.NewBool(val.Float64 != 0)
		default:
			return common.NewBool(val.Int64 != 0)
		}
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
		return s.writeErrResp(MetricWriteTableNotFound, "表不存在: %v", err), nil
	}

	writeRows := make([]storage.WriteRow, 0, len(req.Rows))
	for _, row := range req.Rows {
		key, values, convErr := s.convertWriteRow(tbl, row)
		if convErr != nil {
			return s.writeErrResp(MetricWriteConvertError, "行数据转换错误: %v", convErr), nil
		}
		writeRows = append(writeRows, storage.WriteRow{Key: key, Values: values})
	}

	if err := s.adapter.engineForTable(req.Table).WriteBatch(writeRows); err != nil {
		return s.writeErrResp(MetricWriteError, "写入错误: %v", err), nil
	}

	s.writeSuccessInc(len(writeRows))
	s.metrics.WriteDuration.WithLabelValues("success").Observe(time.Since(start).Seconds())
	return &Response{Code: 0, Rows: len(writeRows)}, nil
}

// handleCreateTable 处理 CREATE TABLE 语句：在 catalog 中注册表，
// 并根据 ENGINE 选项创建对应的存储引擎（memory 或默认 LSM）。
// LSM 表获得独立的 *storage.Engine（位于 dataDir/tables/<name>/），
// 实现表间数据隔离；内存表获得独立的内存引擎。若表已存在且未指定 IF NOT EXISTS，返回错误响应。
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

	return s.createLSMTable(ct, cols, opts)
}

// createLSMTable 创建一张 LSM 表：先创建独立引擎并注册，再在 catalog 建表，
// 确保表对外可见时引擎已就绪（避免并发查询回退到默认引擎）。
// catalog 建表失败则回滚（注销引擎）。若启用调度器，为新引擎启动后台任务。
func (s *Server) createLSMTable(ct *query.CreateTableStatement, cols []catalog.ColumnDef, opts catalog.TableOptions) (*Response, error) {
	if _, err := s.catalog.GetTable(ct.Table); err == nil {
		return s.tableAlreadyExistsResponse(ct), nil
	}
	eng, err := s.createLSMEngineForTable(ct.Table, cols)
	if err != nil {
		return s.queryErrResp(MetricQueryExecuteError, "创建表错误: %v", err), nil
	}
	if err := s.adapter.registerLSMEngine(ct.Table, eng); err != nil {
		_ = eng.Close()
		return s.tableAlreadyExistsResponse(ct), nil
	}
	if err := s.catalog.CreateTable(ct.Table, cols, ct.PrimaryKey, opts); err != nil {
		_ = s.adapter.unregisterLSMEngine(ct.Table)
		return s.createTableErrorResponse(ct, err), nil
	}
	if s.cfg.EnableScheduler {
		eng.StartScheduler(s.cfg.SchedulerConfig)
	}
	s.querySuccessInc()
	return &Response{Code: 0, Rows: 0}, nil
}

// createLSMEngineForTable 为指定表创建独立的 LSM 引擎，数据目录位于
// dataDir/tables/<escaped-name>/，并设置该表的列元数据。
func (s *Server) createLSMEngineForTable(table string, cols []catalog.ColumnDef) (*storage.Engine, error) {
	eng, err := storage.NewEngine(storage.EngineConfig{
		DataDir:         s.tableDataDir(table),
		MaxMemTableSize: s.cfg.MaxMemTableSize,
	})
	if err != nil {
		return nil, fmt.Errorf("create lsm engine for table %q: %w", table, err)
	}
	eng.SetColumnMeta(buildColumnMeta(cols))
	return eng, nil
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
	s.querySuccessInc()
	return &Response{Code: 0, Rows: 0}, nil
}

// createTableErrorResponse 根据建表错误与 IF NOT EXISTS 语义构造响应。
func (s *Server) createTableErrorResponse(ct *query.CreateTableStatement, err error) *Response {
	if ct.IfNotExists && strings.Contains(err.Error(), "already exists") {
		s.querySuccessInc()
		return &Response{Code: 0, Rows: 0}
	}
	return s.queryErrResp(MetricQueryExecuteError, "创建表错误: %v", err)
}

// tableAlreadyExistsResponse 根据 IF NOT EXISTS 语义返回“表已存在”的响应。
func (s *Server) tableAlreadyExistsResponse(ct *query.CreateTableStatement) *Response {
	if ct.IfNotExists {
		s.querySuccessInc()
		return &Response{Code: 0, Rows: 0}
	}
	return s.queryErrResp(MetricQueryExecuteError, "创建表错误: table %q already exists", ct.Table)
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
//
// 性能：主键格式化的输出需与 fmt.Fprintf("%v", v) 完全一致（键空间一致性）。
// JSON 解码的常见类型（string / int / float64 / bool）走类型 switch + strconv
// 快速路径，避免 fmt 的反射开销与中间分配；其它类型（time.Time / 自定义类型
// 等）回退到 fmt.Fprintf，保证向后兼容。
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
		if !writePKValue(&builder, rawVal) {
			// 未匹配的非常见类型（如 time.Time、自定义结构体）回退到 fmt 反射路径
			fmt.Fprintf(&builder, "%v", rawVal)
		}
	}
	return builder.String(), nil
}

// writePKValue 将常见主键值类型写入 builder，返回 true 表示命中快速路径。
// 支持的类型与 fmt.Fprintf("%v", v) 的输出完全等价（基于 strconv 与字面量拼接）。
// 整型按有符号/无符号分组，浮点按位宽分组，避免巨型 type switch 推高
// gocyclo 复杂度。
func writePKValue(b *strings.Builder, v any) bool {
	switch val := v.(type) {
	case string:
		b.WriteString(val)
		return true
	case bool:
		writePKBool(b, val)
		return true
	case int, int8, int16, int32, int64:
		writePKInt(b, val)
		return true
	case uint, uint8, uint16, uint32, uint64:
		writePKUint(b, val)
		return true
	case float32, float64:
		writePKFloat(b, val)
		return true
	default:
		return false
	}
}

// writePKBool 将 bool 主键值按 fmt.Fprintf("%v", v) 的输出写入 builder。
func writePKBool(b *strings.Builder, v bool) {
	if v {
		b.WriteString("true")
	} else {
		b.WriteString("false")
	}
}

// writePKInt 将有符号整数主键值按 fmt.Fprintf("%v", v) 的输出写入 builder。
func writePKInt(b *strings.Builder, v any) {
	var n int64
	switch x := v.(type) {
	case int:
		n = int64(x)
	case int8:
		n = int64(x)
	case int16:
		n = int64(x)
	case int32:
		n = int64(x)
	case int64:
		n = x
	}
	b.WriteString(strconv.FormatInt(n, 10))
}

// writePKUint 将无符号整数主键值按 fmt.Fprintf("%v", v) 的输出写入 builder。
func writePKUint(b *strings.Builder, v any) {
	var n uint64
	switch x := v.(type) {
	case uint:
		n = uint64(x)
	case uint8:
		n = uint64(x)
	case uint16:
		n = uint64(x)
	case uint32:
		n = uint64(x)
	case uint64:
		n = x
	}
	b.WriteString(strconv.FormatUint(n, 10))
}

// writePKFloat 将浮点主键值按 fmt.Fprintf("%v", v) 的输出写入 builder。
func writePKFloat(b *strings.Builder, v any) {
	switch x := v.(type) {
	case float32:
		// FormatFloat 走通用转换路径，避免 %g 在小数点形式下的精度差异
		b.WriteString(strconv.FormatFloat(float64(x), 'g', -1, 32))
	case float64:
		b.WriteString(strconv.FormatFloat(x, 'g', -1, 64))
	}
}
