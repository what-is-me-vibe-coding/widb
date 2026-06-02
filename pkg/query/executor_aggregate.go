package query

import (
	"fmt"
	"strings"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// groupRow 存储分组键对应的原始行数据。
type groupRow struct {
	key    string
	values map[string]common.Value
}

// accumulator 聚合函数累加器。
type accumulator struct {
	funcType AggregateFunc
	count    int64
	sum      float64
	minVal   common.Value
	maxVal   common.Value
	hasValue bool
}

func newAccumulators(aggs []AggregateExpr) []accumulator {
	accs := make([]accumulator, len(aggs))
	for i, agg := range aggs {
		accs[i].funcType = agg.Func
	}
	return accs
}

func (a *accumulator) update(val common.Value) {
	switch a.funcType {
	case AggCount:
		a.count++
	case AggSum:
		if val.Valid {
			a.count++
			a.sum += toFloat64(val)
		}
	case AggMin:
		if val.Valid {
			if !a.hasValue || val.Less(a.minVal) {
				a.minVal = val
			}
			a.hasValue = true
		}
	case AggMax:
		if val.Valid {
			if !a.hasValue || a.maxVal.Less(val) {
				a.maxVal = val
			}
			a.hasValue = true
		}
	case AggAvg:
		if val.Valid {
			a.count++
			a.sum += toFloat64(val)
		}
	}
}

func (a *accumulator) result() common.Value {
	switch a.funcType {
	case AggCount:
		return common.NewInt64(a.count)
	case AggSum:
		if a.count == 0 {
			return common.NewNull()
		}
		return common.NewFloat64(a.sum)
	case AggMin:
		if !a.hasValue {
			return common.NewNull()
		}
		return a.minVal
	case AggMax:
		if !a.hasValue {
			return common.NewNull()
		}
		return a.maxVal
	case AggAvg:
		if a.count == 0 {
			return common.NewNull()
		}
		return common.NewFloat64(a.sum / float64(a.count))
	}
	return common.NewNull()
}

// executeAggregate 执行 AggregateNode。
func (e *Executor) executeAggregate(agg *AggregateNode) (*execResult, error) {
	childResult, err := e.executeNode(agg.Child)
	if err != nil {
		return nil, err
	}

	inputSchema := childResult.schema
	colIdxMap := buildColIdxMapFromSchema(inputSchema)

	groupAccum, groupRows, groupOrder := e.aggregateRows(agg, childResult, inputSchema, colIdxMap)

	schema := agg.Schema()
	outputCols := e.buildAggregateOutput(agg, schema, groupAccum, groupRows, groupOrder, colIdxMap)

	output := storage.NewChunk(defaultChunkSize)
	for _, col := range outputCols {
		if err := output.AddColumn(col); err != nil {
			return nil, fmt.Errorf("executor aggregate: %w", err)
		}
	}

	return &execResult{chunks: []*storage.Chunk{output}, schema: schema}, nil
}

func (e *Executor) aggregateRows(agg *AggregateNode, childResult *execResult, inputSchema []ColumnDef, colIdxMap map[string]int) (map[string][]accumulator, map[string]*groupRow, []string) {
	groupAccum := make(map[string][]accumulator)
	groupRows := make(map[string]*groupRow)
	groupOrder := make([]string, 0)

	for _, chunk := range childResult.chunks {
		for row := uint32(0); row < chunk.RowCount(); row++ {
			rowVals := buildRowValues(chunk, inputSchema, row)
			groupKey := buildGroupKey(agg.GroupBy, rowVals, colIdxMap)

			if _, ok := groupAccum[groupKey]; !ok {
				groupAccum[groupKey] = newAccumulators(agg.Aggregates)
				groupRows[groupKey] = &groupRow{key: groupKey, values: rowVals}
				groupOrder = append(groupOrder, groupKey)
			}

			for i := range groupAccum[groupKey] {
				var val common.Value
				if agg.Aggregates[i].Arg != nil {
					val, _ = evalExpr(agg.Aggregates[i].Arg, rowVals, colIdxMap)
				}
				groupAccum[groupKey][i].update(val)
			}
		}
	}

	if len(groupOrder) == 0 {
		groupOrder = append(groupOrder, "")
		groupAccum[""] = newAccumulators(agg.Aggregates)
		groupRows[""] = &groupRow{key: "", values: nil}
	}

	return groupAccum, groupRows, groupOrder
}

func (e *Executor) buildAggregateOutput(agg *AggregateNode, schema []ColumnDef, groupAccum map[string][]accumulator, groupRows map[string]*groupRow, groupOrder []string, colIdxMap map[string]int) []*storage.ColumnVector {
	outputCols := make([]*storage.ColumnVector, len(schema))
	for i, colDef := range schema {
		outputCols[i] = storage.NewColumnVector(uint32(i), colDef.Type, uint32(len(groupOrder)))
	}

	for _, groupKey := range groupOrder {
		accs := groupAccum[groupKey]
		gr := groupRows[groupKey]
		colIdx := 0

		for _, gb := range agg.GroupBy {
			val, _ := evalExpr(gb, gr.values, colIdxMap)
			_ = outputCols[colIdx].Append(coerceValue(val, schema[colIdx].Type))
			colIdx++
		}

		for _, acc := range accs {
			val := acc.result()
			_ = outputCols[colIdx].Append(coerceValue(val, schema[colIdx].Type))
			colIdx++
		}
	}

	return outputCols
}

// buildGroupKey 构建分组键。
func buildGroupKey(groupBy []Expression, row map[string]common.Value, colIdxMap map[string]int) string {
	if len(groupBy) == 0 {
		return ""
	}
	parts := make([]string, len(groupBy))
	for i, gb := range groupBy {
		val, _ := evalExpr(gb, row, colIdxMap)
		parts[i] = val.String()
	}
	return strings.Join(parts, "|")
}
