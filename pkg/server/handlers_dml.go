package server

import (
	"fmt"
	"strings"

	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/query"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// handleDelete 执行 DELETE 语句：按 WHERE 过滤后删除匹配行。
//
// 优化层级（由快到慢，回退安全）：
//  1. 主键等值快路径：当 WHERE 形如 "pk_col = lit" 或全部 PK 列的等值 AND，
//     直接用点查接口（Get）拿到行并删除，O(log n) 而非全表扫描 + 逐行求值。
//  2. 段裁剪路径：谓词下推到 ScanRangeWithPruning，跳过不可能命中的 Segment。
//  3. 全表扫描路径：复杂谓词（OR、LIKE 等）走 EvalRowPredicate 完整求值。
func (s *Server) handleDelete(del *query.DeleteStatement) (*Response, error) {
	if _, err := s.catalog.GetTable(del.Table); err != nil {
		return s.queryErrResp(MetricQueryExecuteError, "表不存在: %v", err), nil
	}

	eng := s.adapter.engineForTable(del.Table)

	// 优化 1：主键等值快路径（仅当 WHERE 是 PK 列的等值 AND 时生效）
	if tbl, tblErr := s.catalog.GetTable(del.Table); tblErr == nil {
		if key, ok := tryBuildKeyFromPKEquality(del.Where, tbl); ok {
			if applied, resp := s.deleteByPK(eng, key); applied {
				return resp, nil
			}
		}
	}

	// 优化 2：谓词下推：让存储层利用稀疏索引跳过无关 Segment
	columnPreds := query.ExtractColumnPredicates(del.Where)
	var entries []storage.ScanEntry
	if len(columnPreds) > 0 {
		entries = eng.ScanRangeWithPruning("", "\xff\xff\xff\xff", columnPreds)
	} else {
		entries = eng.ScanRange("", "\xff\xff\xff\xff")
	}

	deleted := 0
	for _, entry := range entries {
		// 仍需逐行求值完整 WHERE：下推仅过滤了 Segment，未覆盖复杂谓词（OR、LIKE 等）
		if !query.EvalRowPredicate(del.Where, entry.Value.Columns) {
			continue
		}
		if delErr := eng.Delete(entry.Key); delErr != nil {
			return s.queryErrResp(MetricQueryExecuteError, "删除错误: %v", delErr), nil
		}
		deleted++
	}

	s.querySuccessInc()
	return &Response{Code: 0, Rows: deleted}, nil
}

// handleUpdate 执行 UPDATE 语句：按 WHERE 过滤后对匹配行应用 SET 赋值并重新写入。
// 若更新导致主键变更且新主键已存在，返回冲突错误。
//
// 优化层级（由快到慢，回退安全）：
//  1. 主键等值快路径：WHERE 为 PK 列等值 AND 时，直接 Get + 修改 + 写入，
//     避免全表扫描与逐行谓词求值。
//  2. 段裁剪路径：谓词下推到 ScanRangeWithPruning，跳过无关 Segment。
//  3. 全表扫描路径：复杂谓词走 EvalRowPredicate 完整求值。
func (s *Server) handleUpdate(upd *query.UpdateStatement) (*Response, error) {
	tbl, err := s.catalog.GetTable(upd.Table)
	if err != nil {
		return s.queryErrResp(MetricQueryExecuteError, "表不存在: %v", err), nil
	}

	eng := s.adapter.engineForTable(upd.Table)

	// 优化 1：主键等值快路径
	if key, ok := tryBuildKeyFromPKEquality(upd.Where, tbl); ok {
		if applied, resp := s.updateByPK(eng, tbl, key, upd); applied {
			return resp, nil
		}
	}

	// 优化 2/3：段裁剪或全表扫描路径
	entries := scanEntriesForUpdate(eng, upd.Where)
	updated, resp := s.applyUpdatesWithScan(eng, tbl, entries, upd)
	if resp != nil {
		return resp, nil
	}
	s.querySuccessInc()
	return &Response{Code: 0, Rows: updated}, nil
}

// scanEntriesForUpdate 根据 WHERE 谓词返回候选 ScanEntry 列表。
// 谓词可下推时走段裁剪路径；否则退化为全范围扫描。
func scanEntriesForUpdate(eng TableEngine, where query.Expression) []storage.ScanEntry {
	columnPreds := query.ExtractColumnPredicates(where)
	if len(columnPreds) > 0 {
		return eng.ScanRangeWithPruning("", "\xff\xff\xff\xff", columnPreds)
	}
	return eng.ScanRange("", "\xff\xff\xff\xff")
}

// applyUpdatesWithScan 对 ScanEntry 列表执行完整的 UPDATE 流程：
// 谓词求值 → SET 赋值 → 主键冲突检查 → 主键变更处理 → 写入。
// 返回 (updated, nil) 表示成功；返回 (0, resp) 表示中途发生错误。
func (s *Server) applyUpdatesWithScan(
	eng TableEngine, tbl *catalog.Table,
	entries []storage.ScanEntry, upd *query.UpdateStatement,
) (int, *Response) {
	colTypes := tbl.ColTypeMap()
	updated := 0
	for _, entry := range entries {
		ok, resp := s.applyOneUpdate(eng, tbl, entry, upd, colTypes)
		if resp != nil {
			return 0, resp
		}
		if ok {
			updated++
		}
	}
	return updated, nil
}

// applyOneUpdate 处理单行 UPDATE：先求值谓词再应用赋值并写入。
// 返回 (true, nil) 表示成功更新；(false, nil) 表示谓词不命中；
// 返回 (false, resp) 表示发生错误（含主键冲突、写入失败等）。
func (s *Server) applyOneUpdate(
	eng TableEngine, tbl *catalog.Table, entry storage.ScanEntry,
	upd *query.UpdateStatement, colTypes map[string]common.DataType,
) (bool, *Response) {
	// 仍需逐行求值完整 WHERE：下推仅过滤了 Segment，未覆盖复杂谓词（OR、LIKE 等）
	if !query.EvalRowPredicate(upd.Where, entry.Value.Columns) {
		return false, nil
	}
	newValues, uErr := s.applyUpdateAssignments(entry, upd.Assignments, colTypes)
	if uErr != nil {
		return false, s.queryErrResp(MetricQueryExecuteError, "%v", uErr)
	}
	newKey, keyErr := buildPrimaryKeyFromValues(tbl, newValues)
	if keyErr != nil {
		return false, s.queryErrResp(MetricQueryExecuteError, "%v", keyErr)
	}
	if newKey != entry.Key {
		if conflictErr := checkPKConflict(eng, newKey); conflictErr != nil {
			return false, s.queryErrResp(MetricQueryExecuteError, "%v", conflictErr)
		}
		// 主键变更：先删旧行。失败时不再写入新行，避免旧行与新行并存。
		if delErr := eng.Delete(entry.Key); delErr != nil {
			return false, s.queryErrResp(MetricQueryExecuteError, "删除旧主键错误: %v", delErr)
		}
	}
	if wErr := eng.Write(newKey, newValues); wErr != nil {
		return false, s.queryErrResp(MetricQueryExecuteError, "写入错误: %v", wErr)
	}
	return true, nil
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
// INSERT 与 UPDATE 主键变更路径共享此实现，复用 engineGetter 抽象。
func checkPKConflict(eng TableEngine, key string) error {
	if getter := tryEngineGetter(eng); getter != nil {
		if _, exists := getter.Get(key); exists {
			return fmt.Errorf("PRIMARY KEY CONFLICT: key %q 已存在", key)
		}
	}
	return nil
}

// engineGetter 抽象支持点查接口（Get(key) (Row, bool)）的 TableEngine。
// LSM 引擎与内存引擎均实现此方法，路由适配器返回的 TableEngine 通过
// 类型断言自动获得此能力。DELETE/UPDATE 主键等值快路径与 INSERT 主键冲突
// 检查共享此接口。
type engineGetter interface {
	Get(string) (storage.Row, bool)
}

// tryEngineGetter 尝试将 TableEngine 转型为支持 Get 的接口，失败返回 nil。
// 转型失败时调用方应回退到 ScanRange 全表扫描路径。
func tryEngineGetter(eng TableEngine) engineGetter {
	if g, ok := eng.(engineGetter); ok {
		return g
	}
	return nil
}

// tryBuildKeyFromPKEquality 判定 WHERE 是否可化简为「主键列等值 AND」形式，
// 若是则直接拼出存储 key，使 DELETE/UPDATE 走 O(log n) 点查快路径。
//
// 判定规则：
//   - WHERE 必须是顶层 AND 连接的若干个等值比较（OpEq）
//   - 每个等值比较的左/右必有一个是列引用，另一个是字面量（LiteralExpr.Value.Valid）
//   - 所有出现的列必须恰好覆盖 tbl.PrimaryKey 的全部列（无遗漏、无多余）
//   - 复合主键的列顺序：按 tbl.PrimaryKey 中声明的顺序拼接键，与 buildPrimaryKeyFromValues
//     保持完全一致，避免键构造偏差导致 Get 漏命中
//
// 不满足任一条件时返回 ("", false)，调用方应回退到全表扫描路径。
//
// 与 ExtractColumnPredicates 的区别：后者提取所有可下推的列条件（用于段裁剪），
// 接受任意比较运算符、AND 内的非 PK 列等值；而本函数要求 WHERE 完全由 PK 等值
// 构成，是更严格的「全主键点查」判定，避免在复合谓词场景下错误化简。
func tryBuildKeyFromPKEquality(where query.Expression, tbl *catalog.Table) (string, bool) {
	if where == nil || len(tbl.PrimaryKey) == 0 {
		return "", false
	}

	pkSet := make(map[string]int, len(tbl.PrimaryKey))
	for i, pk := range tbl.PrimaryKey {
		pkSet[pk] = i
	}

	// values[i] 记录主键第 i 列对应的字面量值（按 tbl.PrimaryKey 顺序）
	values := make([]common.Value, len(tbl.PrimaryKey))
	covered, ok := collectPKEqualityValues(splitTopLevelAnd(where), pkSet, tbl, values)
	if !ok || covered != len(tbl.PrimaryKey) {
		// 子句非法或 PK 列未被完全覆盖 → 不能化简为点查
		return "", false
	}

	var builder strings.Builder
	for i, v := range values {
		if i > 0 {
			builder.WriteByte(0)
		}
		builder.WriteString(v.String())
	}
	return builder.String(), true
}

// collectPKEqualityValues 将 WHERE 各子句中的 PK 等值字面量填入 values。
// 返回 (覆盖列数, true) 表示全部子句合法，PK 列收集完成；(0, false) 表示遇到
// 不兼容的子句（非等值/非列引用/非主键列/重复 PK 列）需要回退。
func collectPKEqualityValues(conjuncts []query.Expression, pkSet map[string]int, tbl *catalog.Table, values []common.Value) (int, bool) {
	covered := 0
	for _, c := range conjuncts {
		bin, isEq := c.(*query.BinaryExpr)
		if !isEq || bin.Op != query.OpEq {
			return 0, false
		}
		colName, lit, ok := extractEqColumnLiteral(bin)
		if !ok {
			return 0, false
		}
		idx, isPK := pkSet[colName]
		if !isPK {
			// WHERE 包含非主键列 → 不能化简为点查
			return 0, false
		}
		if values[idx].Valid {
			// 同一 PK 列出现两次（矛盾条件）：保守回退
			return 0, false
		}
		// 按表定义类型做强转，确保 storage key 与写入路径完全一致
		if typ, hasType := tbl.ColTypeMap()[colName]; hasType && lit.Valid && lit.Typ != typ {
			lit = coerceValueByType(lit, typ)
		}
		values[idx] = lit
		covered++
	}
	return covered, true
}

// extractEqColumnLiteral 从等值二元表达式中提取 (列名, 字面量值)。
// 支持 column = literal 与 literal = column 两种形式；非等值运算符或非
// 字面量 RHS 返回 false。NULL 字面量不参与化简（避免主键为 NULL 的歧义）。
func extractEqColumnLiteral(bin *query.BinaryExpr) (string, common.Value, bool) {
	if col, ok := bin.Left.(*query.ColumnExpr); ok {
		if lit, ok := bin.Right.(*query.LiteralExpr); ok && lit.Value.Valid {
			return col.Name, lit.Value, true
		}
	}
	if col, ok := bin.Right.(*query.ColumnExpr); ok {
		if lit, ok := bin.Left.(*query.LiteralExpr); ok && lit.Value.Valid {
			return col.Name, lit.Value, true
		}
	}
	return "", common.Value{}, false
}

// splitTopLevelAnd 将顶层 AND 表达式拆为子句列表。query.splitConjuncts
// 为包内私有函数，本助手在 server 层复用其行为，避免新增 query 包导出 API。
// 非 AND 表达式原样返回长度为 1 的切片；nil 返回空切片。
func splitTopLevelAnd(expr query.Expression) []query.Expression {
	if expr == nil {
		return nil
	}
	bin, ok := expr.(*query.BinaryExpr)
	if !ok || bin.Op != query.OpAnd {
		return []query.Expression{expr}
	}
	return append(splitTopLevelAnd(bin.Left), splitTopLevelAnd(bin.Right)...)
}

// deleteByPK 通过点查接口按主键删除单行，返回 (true, resp) 表示快路径已生效。
// 引擎不支持 Get 时返回 (false, nil) 让调用方回退到扫描路径。
// 命中：删除并返回 Rows=1；未命中：返回 Rows=0（与历史「删除不存在行 = 影响 0 行」一致）。
func (s *Server) deleteByPK(eng TableEngine, key string) (bool, *Response) {
	getter := tryEngineGetter(eng)
	if getter == nil {
		return false, nil
	}
	if _, exists := getter.Get(key); !exists {
		s.querySuccessInc()
		return true, &Response{Code: 0, Rows: 0}
	}
	if err := eng.Delete(key); err != nil {
		return true, s.queryErrResp(MetricQueryExecuteError, "删除错误: %v", err)
	}
	s.querySuccessInc()
	return true, &Response{Code: 0, Rows: 1}
}

// updateByPK 通过点查接口按主键更新单行，返回 (true, resp) 表示快路径已生效。
// 引擎不支持 Get 时返回 (false, nil) 让调用方回退到扫描路径。
// 未命中：返回 Rows=0（与历史语义一致）。主键变更时检查目标 key 是否被占用；
// 删除旧主键失败时直接返回错误，避免旧行与新行并存导致数据不一致。
func (s *Server) updateByPK(eng TableEngine, tbl *catalog.Table, key string, upd *query.UpdateStatement) (bool, *Response) {
	getter := tryEngineGetter(eng)
	if getter == nil {
		return false, nil
	}
	entry, exists := getter.Get(key)
	if !exists {
		s.querySuccessInc()
		return true, &Response{Code: 0, Rows: 0}
	}
	scanEntry := storage.ScanEntry{Key: key, Value: entry}
	newValues, uErr := s.applyUpdateAssignments(scanEntry, upd.Assignments, tbl.ColTypeMap())
	if uErr != nil {
		return true, s.queryErrResp(MetricQueryExecuteError, "%v", uErr)
	}
	newKey, keyErr := buildPrimaryKeyFromValues(tbl, newValues)
	if keyErr != nil {
		return true, s.queryErrResp(MetricQueryExecuteError, "%v", keyErr)
	}
	if newKey != key {
		if conflictErr := checkPKConflict(eng, newKey); conflictErr != nil {
			return true, s.queryErrResp(MetricQueryExecuteError, "%v", conflictErr)
		}
		// 主键变更：先删旧行。失败时不再写入新行，避免旧行与新行并存。
		if delErr := eng.Delete(key); delErr != nil {
			return true, s.queryErrResp(MetricQueryExecuteError, "删除旧主键错误: %v", delErr)
		}
	}
	if wErr := eng.Write(newKey, newValues); wErr != nil {
		return true, s.queryErrResp(MetricQueryExecuteError, "写入错误: %v", wErr)
	}
	s.querySuccessInc()
	return true, &Response{Code: 0, Rows: 1}
}
