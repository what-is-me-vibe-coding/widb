package server

import (
	"fmt"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// interfaceToValue 将 any 转换为 common.Value。
func interfaceToValue(raw any, typ common.DataType) (common.Value, error) {
	if raw == nil {
		return common.NewNull(), nil
	}

	switch typ {
	case common.TypeBool:
		v, ok := raw.(bool)
		if !ok {
			return common.NewNull(), fmt.Errorf("%w: expected bool, got %T", common.ErrTypeMismatch, raw)
		}
		return common.NewBool(v), nil
	case common.TypeInt64:
		return toInt64Value(raw)
	case common.TypeFloat64:
		return toFloat64Value(raw)
	case common.TypeString:
		v, ok := raw.(string)
		if !ok {
			return common.NewNull(), fmt.Errorf("%w: expected string, got %T", common.ErrTypeMismatch, raw)
		}
		return common.NewString(v), nil
	case common.TypeTimestamp:
		return toTimestampValue(raw)
	default:
		return common.NewNull(), fmt.Errorf("不支持的数据类型: %s", typ)
	}
}

// toInt64Value 将 any 转换为 INT64 Value。
func toInt64Value(raw any) (common.Value, error) {
	switch v := raw.(type) {
	case float64:
		return common.NewInt64(int64(v)), nil
	case int64:
		return common.NewInt64(v), nil
	case int:
		return common.NewInt64(int64(v)), nil
	default:
		return common.NewNull(), fmt.Errorf("%w: expected int64, got %T", common.ErrTypeMismatch, raw)
	}
}

// toFloat64Value 将 any 转换为 FLOAT64 Value。
func toFloat64Value(raw any) (common.Value, error) {
	switch v := raw.(type) {
	case float64:
		return common.NewFloat64(v), nil
	case int64:
		return common.NewFloat64(float64(v)), nil
	case int:
		return common.NewFloat64(float64(v)), nil
	default:
		return common.NewNull(), fmt.Errorf("%w: expected float64, got %T", common.ErrTypeMismatch, raw)
	}
}

// toTimestampValue 将 any 转换为 TIMESTAMP Value。
func toTimestampValue(raw any) (common.Value, error) {
	v, ok := raw.(string)
	if !ok {
		return common.NewNull(), fmt.Errorf("%w: expected timestamp string, got %T",
			common.ErrTypeMismatch, raw)
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return common.NewNull(), fmt.Errorf("解析时间戳: %w", err)
	}
	return common.NewTimestamp(t), nil
}

// chunksToRows 将 Chunk 切片转换为可 JSON 序列化的行数据。
func chunksToRows(chunks []*storage.Chunk) []map[string]any {
	var result []map[string]any
	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		for i := uint32(0); i < chunk.RowCount(); i++ {
			rowMap := make(map[string]any)
			for colIdx := 0; colIdx < chunk.ColumnCount(); colIdx++ {
				col, err := chunk.GetColumn(colIdx)
				if err != nil {
					continue
				}
				if i < col.Len() {
					val := col.GetValue(i)
					rowMap[fmt.Sprintf("col_%d", colIdx)] = valueToInterface(val)
				}
			}
			result = append(result, rowMap)
		}
	}
	return result
}

// valueToInterface 将 common.Value 转换为 any。
func valueToInterface(v common.Value) any {
	if !v.Valid {
		return nil
	}
	switch v.Typ {
	case common.TypeBool:
		return v.Int64 != 0
	case common.TypeInt64:
		return v.Int64
	case common.TypeFloat64:
		return v.Float64
	case common.TypeString:
		return v.Str
	case common.TypeTimestamp:
		return v.Time.Format(time.RFC3339Nano)
	default:
		return nil
	}
}

// countRows 统计 Chunk 切片中的总行数。
func countRows(chunks []*storage.Chunk) int {
	total := 0
	for _, chunk := range chunks {
		if chunk != nil {
			total += int(chunk.RowCount())
		}
	}
	return total
}
