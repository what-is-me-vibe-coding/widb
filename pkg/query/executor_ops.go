package query

import (
	"fmt"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// executeFilter 执行 FilterNode，对输入数据进行向量化过滤。
func (e *Executor) executeFilter(filter *FilterNode) (*execResult, error) {
	childResult, err := e.executeNode(filter.Child)
	if err != nil {
		return nil, err
	}

	schema := childResult.schema
	colIdxMap := buildColIdxMapFromSchema(schema)

	var chunks []*storage.Chunk
	for _, input := range childResult.chunks {
		output, err := filterChunk(input, filter.Condition, schema, colIdxMap)
		if err != nil {
			return nil, err
		}
		if output.RowCount() > 0 {
			chunks = append(chunks, output)
		}
	}

	return &execResult{chunks: chunks, schema: schema}, nil
}

// fillRowVals 将 chunk 中指定行的列值填入 rowVals map（复用已有 map）。
// 统一使用 buildRowValues 实现，避免重复逻辑。
func fillRowVals(rowVals map[string]common.Value, chunk *storage.Chunk, schema []ColumnDef, row uint32) {
	buildRowValues(chunk, schema, row, rowVals)
}

// filterChunk 对单个 Chunk 执行向量化过滤。
// 优化：对简单的 column op literal 比较条件使用快速路径，
// 直接遍历列向量避免 per-row map 构建开销。
func filterChunk(input *storage.Chunk, cond Expression, schema []ColumnDef, colIdxMap map[string]int) (*storage.Chunk, error) {
	rowCount := input.RowCount()
	if rowCount == 0 {
		return storage.NewChunk(defaultChunkSize), nil
	}

	// 尝试快速路径：简单的 column op literal 比较
	if sel, ok := tryFastFilter(input, cond, schema, rowCount); ok {
		if len(sel) == 0 {
			return storage.NewChunk(defaultChunkSize), nil
		}
		return buildFilteredOutput(input, sel), nil
	}

	// 慢速路径：逐行 map 求值
	rowVals := make(map[string]common.Value, len(schema))
	selection := make([]uint32, 0, rowCount)
	for row := uint32(0); row < rowCount; row++ {
		fillRowVals(rowVals, input, schema, row)
		val, err := evalExpr(cond, rowVals, colIdxMap)
		if err != nil {
			continue
		}
		if isTruthyValue(val) {
			selection = append(selection, row)
		}
	}

	if len(selection) == 0 {
		return storage.NewChunk(defaultChunkSize), nil
	}

	return buildFilteredOutput(input, selection), nil
}

// tryFastFilter 尝试对简单的 column op literal 比较使用向量化快速路径。
// 返回 (selection, true) 表示成功，(nil, false) 表示需要回退到慢速路径。
func tryFastFilter(input *storage.Chunk, cond Expression, schema []ColumnDef, rowCount uint32) ([]uint32, bool) {
	bin, ok := cond.(*BinaryExpr)
	if !ok {
		return nil, false
	}

	// 仅支持比较运算符
	switch bin.Op {
	case OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
	default:
		return nil, false
	}

	// 识别 column op literal 模式
	colIdx, _ := resolveColumnIndex(bin.Left, schema)
	if colIdx < 0 {
		return nil, false
	}
	lit, ok := bin.Right.(*LiteralExpr)
	if !ok || !lit.Value.Valid {
		return nil, false
	}

	cols := input.Columns()
	if colIdx >= len(cols) {
		return nil, false
	}
	col := cols[colIdx]

	selection := make([]uint32, 0, rowCount)
	for row := uint32(0); row < rowCount; row++ {
		v := col.GetValue(row)
		if !v.Valid {
			continue
		}
		if evalCompareOp(bin.Op, v, lit.Value) {
			selection = append(selection, row)
		}
	}
	return selection, true
}

// resolveColumnIndex 从表达式中解析列索引，返回 (-1, "") 表示无法解析。
func resolveColumnIndex(expr Expression, schema []ColumnDef) (int, string) {
	switch e := expr.(type) {
	case *ResolvedColumnExpr:
		if e.Idx >= 0 && e.Idx < len(schema) {
			return e.Idx, e.Name
		}
	case *ColumnExpr:
		for i, col := range schema {
			if col.Name == e.Name {
				return i, e.Name
			}
		}
	}
	return -1, ""
}

// evalCompareOp 对两个有效值执行比较运算。
func evalCompareOp(op BinaryOp, left, right common.Value) bool {
	switch op {
	case OpEq:
		return left.Equal(right)
	case OpNe:
		return !left.Equal(right)
	case OpLt:
		return left.Less(right)
	case OpGt:
		return right.Less(left)
	case OpLe:
		return !right.Less(left)
	case OpGe:
		return !left.Less(right)
	}
	return false
}

// buildFilteredOutput 根据 selection 向量构建过滤后的 Chunk 输出。
// 使用 SetValue 替代 Append 跳过 ensureCapacity 检查，提升吞吐。
func buildFilteredOutput(input *storage.Chunk, selection []uint32) *storage.Chunk {
	selLen := uint32(len(selection))
	output := storage.NewChunk(selLen)
	for _, col := range input.Columns() {
		newCol := storage.NewColumnVector(col.ColumnID, col.Typ, selLen)
		for dstIdx, srcIdx := range selection {
			v := col.GetValue(srcIdx)
			if v.Valid {
				_ = newCol.SetValue(uint32(dstIdx), v)
			} else {
				newCol.SetNull(uint32(dstIdx))
			}
		}
		newCol.SetLen(selLen)
		_ = output.AddColumn(newCol)
	}
	return output
}

// executeProject 执行 ProjectNode，对输入数据进行投影。
func (e *Executor) executeProject(proj *ProjectNode) (*execResult, error) {
	childResult, err := e.executeNode(proj.Child)
	if err != nil {
		return nil, err
	}

	inputSchema := childResult.schema
	colIdxMap := buildColIdxMapFromSchema(inputSchema)
	outputSchema := proj.Schema()

	var chunks []*storage.Chunk
	for _, input := range childResult.chunks {
		output, err := projectChunk(input, proj.Expressions, inputSchema, outputSchema, colIdxMap)
		if err != nil {
			return nil, err
		}
		if output.RowCount() > 0 {
			chunks = append(chunks, output)
		}
	}

	return &execResult{chunks: chunks, schema: outputSchema}, nil
}

// appendProjectValue 尝试将值追加到列向量，必要时进行类型强制转换。
func appendProjectValue(col *storage.ColumnVector, val common.Value, colDef ColumnDef, row uint32) error {
	if err := col.Append(val); err != nil {
		typedVal := coerceValue(val, colDef.Type)
		if err2 := col.Append(typedVal); err2 != nil {
			return fmt.Errorf("executor project: row %d: %w", row, err)
		}
	}
	return nil
}

// projectChunk 对单个 Chunk 执行投影。
func projectChunk(input *storage.Chunk, exprs []Expression, inputSchema, outputSchema []ColumnDef, colIdxMap map[string]int) (*storage.Chunk, error) {
	rowCount := input.RowCount()
	output := storage.NewChunk(defaultChunkSize)

	rowVals := make(map[string]common.Value, len(inputSchema))

	for exprIdx, expr := range exprs {
		colDef := outputSchema[exprIdx]
		newCol := storage.NewColumnVector(uint32(exprIdx), colDef.Type, rowCount)

		for row := uint32(0); row < rowCount; row++ {
			fillRowVals(rowVals, input, inputSchema, row)
			val, err := evalExpr(expr, rowVals, colIdxMap)
			if err != nil {
				return nil, fmt.Errorf("executor project: row %d: %w", row, err)
			}
			if err := appendProjectValue(newCol, val, colDef, row); err != nil {
				return nil, err
			}
		}

		if err := output.AddColumn(newCol); err != nil {
			return nil, fmt.Errorf("executor project: %w", err)
		}
	}

	return output, nil
}

// coerceValue 将值强制转换为指定类型。
func coerceValue(val common.Value, target common.DataType) common.Value {
	if !val.Valid {
		return common.NewNull()
	}
	if val.Typ == target {
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
		return common.NewBool(isTruthyValue(val))
	}
	return val
}

// executeLimit 执行 LimitNode。
func (e *Executor) executeLimit(limit *LimitNode) (*execResult, error) {
	childResult, err := e.executeNode(limit.Child)
	if err != nil {
		return nil, err
	}

	var chunks []*storage.Chunk
	skipped := uint64(0)
	returned := uint64(0)

	for _, chunk := range childResult.chunks {
		rowCount := uint64(chunk.RowCount())

		if skipped+rowCount <= limit.Offset {
			skipped += rowCount
			continue
		}

		startRow := uint32(0)
		if skipped < limit.Offset {
			startRow = uint32(limit.Offset - skipped)
			skipped = limit.Offset
		}

		remaining := limit.Count - returned
		endRow := uint32(min(uint64(chunk.RowCount()), uint64(startRow)+remaining))

		if startRow >= endRow {
			break
		}

		limited, err := sliceChunk(chunk, startRow, endRow)
		if err != nil {
			return nil, err
		}
		if limited.RowCount() > 0 {
			chunks = append(chunks, limited)
			returned += uint64(endRow - startRow)
		}

		if returned >= limit.Count {
			break
		}
	}

	return &execResult{chunks: chunks, schema: childResult.schema}, nil
}

// sliceChunk 从 Chunk 中截取指定行范围。
// 使用 ColumnVector.Slice 直接内存拷贝，避免逐行 Append 的开销。
func sliceChunk(chunk *storage.Chunk, startRow, endRow uint32) (*storage.Chunk, error) {
	result := storage.NewChunk(endRow - startRow)
	for _, col := range chunk.Columns() {
		sliced, err := col.Slice(startRow, endRow)
		if err != nil {
			return nil, fmt.Errorf("executor limit: slice column %d: %w", col.ColumnID, err)
		}
		if err := result.AddColumn(sliced); err != nil {
			return nil, fmt.Errorf("executor limit: add column %d: %w", col.ColumnID, err)
		}
	}
	return result, nil
}
