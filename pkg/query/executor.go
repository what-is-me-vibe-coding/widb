package query

import (
	"fmt"
	"log"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

const defaultChunkSize = 1024

// StorageProvider 提供查询执行所需的存储引擎访问能力。
type StorageProvider interface {
	ScanRange(start, end string) []storage.ScanEntry
	ScanRangeWithPruning(start, end string, predicates []storage.ColumnPredicate) []storage.ScanEntry
	ColumnMeta() []storage.ColumnMeta
	PrimaryIndex() *index.PrimaryIndex
	SparseIndex() *index.SparseIndex
}

// TableStorageProvider 扩展 StorageProvider，支持按表名路由到不同的存储引擎。
// 当 Executor 持有的 StorageProvider 实现此接口时，ScanNode 会根据其 Table 字段
// 选择对应表的引擎进行扫描；否则回退到统一的 StorageProvider（保持向后兼容）。
// 这使得 LSM 引擎表与内存引擎表可在同一 Server 中共存。
type TableStorageProvider interface {
	StorageProvider
	ForTable(table string) StorageProvider
}

// Executor 执行查询计划，返回结果 Chunk 流。
type Executor struct {
	storage StorageProvider
}

// NewExecutor 创建一个新的 Executor。
func NewExecutor(sp StorageProvider) *Executor {
	return &Executor{storage: sp}
}

// Execute 执行查询计划节点，返回结果 Chunk 切片。
func (e *Executor) Execute(node PlanNode) ([]*storage.Chunk, error) {
	result, err := e.executeNode(node)
	if err != nil {
		return nil, err
	}
	return result.chunks, nil
}

// execResult 是执行结果，携带 Chunk 切片和对应的 schema。
type execResult struct {
	chunks []*storage.Chunk
	schema []ColumnDef
}

func (e *Executor) executeNode(node PlanNode) (*execResult, error) {
	switch n := node.(type) {
	case *ScanNode:
		return e.executeScan(n)
	case *FilterNode:
		return e.executeFilter(n)
	case *ProjectNode:
		return e.executeProject(n)
	case *AggregateNode:
		return e.executeAggregate(n)
	case *LimitNode:
		return e.executeLimit(n)
	default:
		return nil, fmt.Errorf("executor: unsupported plan node type %T", node)
	}
}

// executeScan 执行 ScanNode，从存储引擎读取数据并转换为 Chunk。
func (e *Executor) executeScan(scan *ScanNode) (*execResult, error) {
	entries := e.scanWithPredicate(scan)
	schema := scan.Schema()

	if len(entries) == 0 {
		return &execResult{chunks: nil, schema: schema}, nil
	}

	chunks, err := buildChunksFromEntries(entries, schema, defaultChunkSize)
	if err != nil {
		return nil, err
	}
	return &execResult{chunks: chunks, schema: schema}, nil
}

// scanWithPredicate 根据谓词从存储引擎获取数据。
// 优化：从谓词中提取列级条件，利用稀疏索引进行段裁剪，
// 跳过不可能包含匹配数据的段，减少 I/O 和解码开销。
//
// 表路由：若 Executor 持有的 StorageProvider 实现了 TableStorageProvider，
// 则按 scan.Table 选择对应表的引擎，使 LSM 表与内存表可共存。
func (e *Executor) scanWithPredicate(scan *ScanNode) []storage.ScanEntry {
	sp := e.storage
	if tsp, ok := e.storage.(TableStorageProvider); ok && scan.Table != "" {
		sp = tsp.ForTable(scan.Table)
	}

	pred := scan.Predicate
	if pred == nil {
		return sp.ScanRange("", "\xff\xff\xff\xff")
	}

	keyRange := e.extractKeyRange(pred)

	// 从谓词中提取列级条件用于段裁剪
	columnPreds := e.extractColumnPredicates(pred)
	if len(columnPreds) > 0 {
		entries := sp.ScanRangeWithPruning(keyRange.start, keyRange.end, columnPreds)
		return e.filterEntriesByPredicate(entries, pred, scan.Columns)
	}

	entries := sp.ScanRange(keyRange.start, keyRange.end)
	return e.filterEntriesByPredicate(entries, pred, scan.Columns)
}

// keyRange 表示主键范围。
type keyRange struct {
	start string
	end   string
}

// extractKeyRange 从谓词中提取主键范围，用于缩小扫描范围。
func (e *Executor) extractKeyRange(pred Expression) keyRange {
	kr := keyRange{start: "", end: "\xff\xff\xff\xff"}

	conjuncts := splitConjuncts(pred)
	for _, c := range conjuncts {
		bin, ok := c.(*BinaryExpr)
		if !ok {
			continue
		}

		col, ok := bin.Left.(*ResolvedColumnExpr)
		if !ok {
			continue
		}

		if col.Idx != 0 {
			continue
		}

		lit, ok := bin.Right.(*LiteralExpr)
		if !ok || !lit.Value.Valid {
			continue
		}

		keyStr := lit.Value.String()
		switch bin.Op {
		case OpEq:
			kr.start = maxStr(kr.start, keyStr)
			kr.end = minStr(kr.end, keyStr)
		case OpGe:
			kr.start = maxStr(kr.start, keyStr)
		case OpGt:
			kr.start = maxStr(kr.start, keyStr)
		case OpLe:
			kr.end = minStr(kr.end, keyStr)
		case OpLt:
			kr.end = minStr(kr.end, keyStr)
		}
	}

	return kr
}

// extractColumnPredicates 从谓词中提取可用于段裁剪的列级条件。
// 仅提取形如 "column op literal" 的简单比较谓词（AND 连接），
// 复杂表达式（OR、嵌套、函数调用等）不参与段裁剪，保证安全性。
func (e *Executor) extractColumnPredicates(pred Expression) []storage.ColumnPredicate {
	conjuncts := splitConjuncts(pred)
	var preds []storage.ColumnPredicate
	for _, c := range conjuncts {
		bin, ok := c.(*BinaryExpr)
		if !ok {
			continue
		}
		colPred, ok := e.binaryExprToColumnPredicate(bin)
		if !ok {
			continue
		}
		preds = append(preds, colPred)
	}
	return preds
}

// binaryExprToColumnPredicate 将二元表达式转换为列谓词。
// 仅处理 "column op literal" 或 "literal op column" 形式的比较表达式。
func (e *Executor) binaryExprToColumnPredicate(bin *BinaryExpr) (storage.ColumnPredicate, bool) {
	// 仅处理比较运算符
	var colName string
	var value common.Value
	var op index.PredicateOp
	var ok bool

	// 尝试 "column op literal" 形式
	if col, isCol := bin.Left.(*ResolvedColumnExpr); isCol {
		if lit, isLit := bin.Right.(*LiteralExpr); isLit && lit.Value.Valid {
			colName = col.Name
			value = lit.Value
			op, ok = queryOpToIndexOp(bin.Op)
			if !ok {
				return storage.ColumnPredicate{}, false
			}
			return storage.ColumnPredicate{ColumnName: colName, Op: op, Value: value}, true
		}
	}

	// 尝试 "literal op column" 形式（需要翻转运算符）
	if col, isCol := bin.Right.(*ResolvedColumnExpr); isCol {
		if lit, isLit := bin.Left.(*LiteralExpr); isLit && lit.Value.Valid {
			colName = col.Name
			value = lit.Value
			op, ok = queryOpToIndexOpFlip(bin.Op)
			if !ok {
				return storage.ColumnPredicate{}, false
			}
			return storage.ColumnPredicate{ColumnName: colName, Op: op, Value: value}, true
		}
	}

	return storage.ColumnPredicate{}, false
}

// queryOpToIndexOp 将查询层的 BinaryOp 映射为索引层的 PredicateOp。
func queryOpToIndexOp(op BinaryOp) (index.PredicateOp, bool) {
	switch op {
	case OpEq:
		return index.OpEqual, true
	case OpNe:
		return index.OpNotEqual, true
	case OpLt:
		return index.OpLess, true
	case OpLe:
		return index.OpLessEqual, true
	case OpGt:
		return index.OpGreater, true
	case OpGe:
		return index.OpGreaterEqual, true
	default:
		return 0, false
	}
}

// queryOpToIndexOpFlip 将翻转后的运算符映射为索引层的 PredicateOp。
// 例如 "literal < column" 等价于 "column > literal"。
func queryOpToIndexOpFlip(op BinaryOp) (index.PredicateOp, bool) {
	switch op {
	case OpLt:
		return index.OpGreater, true
	case OpLe:
		return index.OpGreaterEqual, true
	case OpGt:
		return index.OpLess, true
	case OpGe:
		return index.OpLessEqual, true
	case OpEq:
		return index.OpEqual, true
	case OpNe:
		return index.OpNotEqual, true
	default:
		return 0, false
	}
}

// filterEntriesByPredicate 使用谓词过滤扫描结果。
func (e *Executor) filterEntriesByPredicate(entries []storage.ScanEntry, pred Expression, cols []string) []storage.ScanEntry {
	colIdxMap := buildColIdxMap(cols)

	// 预分配结果切片，假设约一半条目通过过滤，减少扩容开销
	result := make([]storage.ScanEntry, 0, len(entries)/2+1)
	for _, entry := range entries {
		val, err := evalExpr(pred, entry.Value.Columns, colIdxMap)
		if err != nil {
			log.Printf("executor: filter predicate eval error for key %s: %v", entry.Key, err)
			continue
		}
		if isTruthyValue(val) {
			result = append(result, entry)
		}
	}
	return result
}

// appendValueSafe 安全地向列向量追加值，类型不匹配时尝试转换，仍失败则用 NULL 填充。
// 如果 NULL 追加也失败（如容量不足），记录警告，避免列数据不对齐导致后续行偏移。
func appendValueSafe(col *storage.ColumnVector, val common.Value, typ common.DataType) {
	if err := col.Append(val); err == nil {
		return
	}
	val = coerceValue(val, typ)
	if err := col.Append(val); err == nil {
		return
	}
	if err := col.Append(common.NewNull()); err != nil {
		log.Printf("executor: failed to append NULL to column %d: %v", col.ColumnID, err)
	}
}

// buildChunksFromEntries 将 ScanEntry 切片转换为 Chunk 切片。
// 优化：直接使用 SetValue 而非 Append，跳过 ensureCapacity 检查，
// 因为 ColumnVector 已预分配了足够的容量。
func buildChunksFromEntries(entries []storage.ScanEntry, schema []ColumnDef, chunkSize int) ([]*storage.Chunk, error) {
	if len(entries) == 0 || len(schema) == 0 {
		return nil, nil
	}

	numChunks := (len(entries) + chunkSize - 1) / chunkSize
	chunks := make([]*storage.Chunk, 0, numChunks)
	for start := 0; start < len(entries); start += chunkSize {
		end := start + chunkSize
		if end > len(entries) {
			end = len(entries)
		}

		batch := entries[start:end]
		batchLen := uint32(len(batch))
		chunk := storage.NewChunk(batchLen)

		for colIdx, colDef := range schema {
			col := storage.NewColumnVector(uint32(colIdx), colDef.Type, batchLen)
			fillColumnValues(col, batch, colDef)
			col.SetLen(batchLen)
			if err := chunk.AddColumn(col); err != nil {
				return nil, fmt.Errorf("executor scan: add column %d: %w", colIdx, err)
			}
		}

		chunks = append(chunks, chunk)
	}

	return chunks, nil
}

// fillColumnValues 将 batch 中每行对应列的值直接写入 ColumnVector。
// 使用 SetValue 替代 Append，跳过 ensureCapacity 开销。
func fillColumnValues(col *storage.ColumnVector, batch []storage.ScanEntry, colDef ColumnDef) {
	for rowIdx, entry := range batch {
		val, ok := entry.Value.Columns[colDef.Name]
		if !ok {
			col.SetNull(uint32(rowIdx))
			continue
		}
		if val.Typ != colDef.Type && val.Valid {
			val = coerceValue(val, colDef.Type)
			// coerceValue 未匹配到转换规则时返回原值，类型仍不匹配，
			// 此时 SetValue 可能写入错误类型数据，应标记为 Null。
			if val.Typ != colDef.Type {
				col.SetNull(uint32(rowIdx))
				continue
			}
		}
		if err := col.SetValue(uint32(rowIdx), val); err != nil {
			col.SetNull(uint32(rowIdx))
		}
	}
}

// buildColIdxMap 构建列名到索引的映射。
func buildColIdxMap(cols []string) map[string]int {
	m := make(map[string]int, len(cols))
	for i, col := range cols {
		m[col] = i
	}
	return m
}

// buildColIdxMapFromSchema 从 ColumnDef 列表构建列名到索引的映射。
func buildColIdxMapFromSchema(schema []ColumnDef) map[string]int {
	m := make(map[string]int, len(schema))
	for i, col := range schema {
		m[col.Name] = i
	}
	return m
}

func maxStr(a, b string) string {
	if a > b {
		return a
	}
	return b
}

func minStr(a, b string) string {
	if a < b {
		return a
	}
	return b
}
