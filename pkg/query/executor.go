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
	ColumnMeta() []storage.ColumnMeta
	PrimaryIndex() *index.PrimaryIndex
	SparseIndex() *index.SparseIndex
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
func (e *Executor) scanWithPredicate(scan *ScanNode) []storage.ScanEntry {
	pred := scan.Predicate
	if pred == nil {
		return e.storage.ScanRange("", "\xff\xff\xff\xff")
	}

	keyRange := e.extractKeyRange(pred)
	entries := e.storage.ScanRange(keyRange.start, keyRange.end)

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

// filterEntriesByPredicate 使用谓词过滤扫描结果。
func (e *Executor) filterEntriesByPredicate(entries []storage.ScanEntry, pred Expression, cols []string) []storage.ScanEntry {
	colIdxMap := buildColIdxMap(cols)

	var result []storage.ScanEntry
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
func appendValueSafe(col *storage.ColumnVector, val common.Value, typ common.DataType) {
	if err := col.Append(val); err == nil {
		return
	}
	val = coerceValue(val, typ)
	if err := col.Append(val); err == nil {
		return
	}
	_ = col.Append(common.NewNull())
}

// buildChunksFromEntries 将 ScanEntry 切片转换为 Chunk 切片。
func buildChunksFromEntries(entries []storage.ScanEntry, schema []ColumnDef, chunkSize int) ([]*storage.Chunk, error) {
	if len(entries) == 0 || len(schema) == 0 {
		return nil, nil
	}

	var chunks []*storage.Chunk
	for start := 0; start < len(entries); start += chunkSize {
		end := start + chunkSize
		if end > len(entries) {
			end = len(entries)
		}

		batch := entries[start:end]
		chunk := storage.NewChunk(uint32(chunkSize))

		for colIdx, colDef := range schema {
			col := storage.NewColumnVector(uint32(colIdx), colDef.Type, uint32(len(batch)))
			for _, entry := range batch {
				val, ok := entry.Value.Columns[colDef.Name]
				if !ok {
					val = common.NewNull()
				}
				appendValueSafe(col, val, colDef.Type)
			}
			if err := chunk.AddColumn(col); err != nil {
				return nil, fmt.Errorf("executor scan: add column %d: %w", colIdx, err)
			}
		}

		chunks = append(chunks, chunk)
	}

	return chunks, nil
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
