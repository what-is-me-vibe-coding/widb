package storage

import (
	"fmt"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// decodedColumn 缓存已解码的列数据，避免重复解压和解码。
type decodedColumn struct {
	data   any
	nulls  *common.Bitmap
	typ    common.DataType
	encTyp EncodingType
}

// extractValue 从已解码的列数据中提取指定行的值。
func extractValue(dc decodedColumn, row uint32) common.Value {
	if dc.nulls != nil && dc.nulls.Get(row) {
		return common.NewNull()
	}

	switch dc.typ {
	case common.TypeInt64:
		return extractInt64Value(dc.data, row)
	case common.TypeFloat64:
		return extractFloat64Value(dc.data, row)
	case common.TypeBool:
		return extractBoolValue(dc.data, row)
	case common.TypeString:
		return extractStringValue(dc.data, row)
	case common.TypeTimestamp:
		return extractTimestampValue(dc.data, row)
	default:
		return common.NewNull()
	}
}

func extractInt64Value(data any, row uint32) common.Value {
	if ints, ok := data.([]int64); ok && row < uint32(len(ints)) {
		return common.NewInt64(ints[row])
	}
	return common.NewNull()
}

func extractFloat64Value(data any, row uint32) common.Value {
	if floats, ok := data.([]float64); ok && row < uint32(len(floats)) {
		return common.NewFloat64(floats[row])
	}
	return common.NewNull()
}

func extractBoolValue(data any, row uint32) common.Value {
	if bools, ok := data.([]uint64); ok && row < uint32(len(bools)) {
		return common.NewBool(bools[row] != 0)
	}
	return common.NewNull()
}

func extractStringValue(data any, row uint32) common.Value {
	if strs, ok := data.([]string); ok && row < uint32(len(strs)) {
		return common.NewString(strs[row])
	}
	return common.NewNull()
}

func extractTimestampValue(data any, row uint32) common.Value {
	if times, ok := data.([]int64); ok && row < uint32(len(times)) {
		return common.NewTimestamp(time.Unix(0, times[row]))
	}
	return common.NewNull()
}

// decodeAllColumns 一次性解压并解码 Segment 的所有列，返回解码缓存。
func (s *Segment) decodeAllColumns() ([]decodedColumn, error) {
	columns := make([]decodedColumn, len(s.Columns))
	for i := range s.Columns {
		src := &s.Columns[i]
		enc := &EncodedColumn{
			Encoding: src.Encoding,
			Type:     src.Type,
			RowCount: src.RowCount,
		}
		if len(src.Data) > 0 {
			enc.Data = make([]byte, len(src.Data))
			copy(enc.Data, src.Data)
		}
		if len(src.Offsets) > 0 {
			enc.Offsets = src.Offsets
		}
		if len(src.Dict) > 0 {
			enc.Dict = src.Dict
		}
		if len(src.Nulls) > 0 {
			enc.Nulls = src.Nulls
		}
		if err := DecompressColumn(enc); err != nil {
			return nil, fmt.Errorf("segment: decompress column %d: %w", i, err)
		}
		decoded, nulls, err := DecodeColumn(enc)
		if err != nil {
			return nil, fmt.Errorf("segment: decode column %d: %w", i, err)
		}
		columns[i] = decodedColumn{data: decoded, nulls: nulls, typ: src.Type, encTyp: src.Encoding}
	}
	return columns, nil
}

// getColumnValueFromDecoded 从已解码的列缓存中提取指定行的值。
func (s *Segment) getColumnValueFromDecoded(cols []decodedColumn, colIdx uint32, rowIdx uint32) common.Value {
	if int(colIdx) >= len(cols) {
		return common.NewNull()
	}
	return extractValue(cols[colIdx], rowIdx)
}

// ensureColCache 初始化逐列解码缓存，使用 sync.Once 保证线程安全。
func (s *Segment) ensureColCache() {
	s.cacheInit.Do(func() {
		s.colCache = make([]decodedColumn, len(s.Columns))
		s.colDecodeState = make([]colDecodeState, len(s.Columns))
	})
}

// GetColumnValue 从指定列中提取给定行索引的值。
// 使用逐列延迟解码：仅解码请求的列，避免点查时解码所有列的开销。
// 解码结果缓存在 Segment 级别的 colCache 中，后续调用直接从缓存读取。
// 解码失败时不标记为已完成，允许后续调用重试。
func (s *Segment) GetColumnValue(colIdx uint32, rowIdx uint32) (common.Value, error) {
	if int(colIdx) >= len(s.Columns) {
		return common.NewNull(), fmt.Errorf("segment: column index %d out of range", colIdx)
	}

	s.ensureColCache()

	ds := &s.colDecodeState[colIdx]
	ds.mu.Lock()
	if !ds.decoded {
		src := &s.Columns[colIdx]
		enc := &EncodedColumn{
			Encoding: src.Encoding,
			Type:     src.Type,
			RowCount: src.RowCount,
		}
		if len(src.Data) > 0 {
			enc.Data = make([]byte, len(src.Data))
			copy(enc.Data, src.Data)
		}
		if len(src.Offsets) > 0 {
			enc.Offsets = src.Offsets
		}
		if len(src.Dict) > 0 {
			enc.Dict = src.Dict
		}
		if len(src.Nulls) > 0 {
			enc.Nulls = src.Nulls
		}
		if err := DecompressColumn(enc); err != nil {
			ds.mu.Unlock()
			return common.NewNull(), fmt.Errorf("segment: decompress column %d: %w", colIdx, err)
		}
		decoded, nulls, err := DecodeColumn(enc)
		if err != nil {
			ds.mu.Unlock()
			return common.NewNull(), fmt.Errorf("segment: decode column %d: %w", colIdx, err)
		}
		s.colCache[colIdx] = decodedColumn{data: decoded, nulls: nulls, typ: src.Type, encTyp: src.Encoding}
		ds.decoded = true
	}
	ds.mu.Unlock()

	dc := s.colCache[colIdx]
	if dc.data == nil {
		return common.NewNull(), fmt.Errorf("segment: column %d decode unavailable", colIdx)
	}

	return extractValue(dc, rowIdx), nil
}

// getColCache returns the decoded column cache entry for the given column index.
func (s *Segment) getColCache(colIdx uint32) (decodedColumn, bool) {
	if int(colIdx) >= len(s.colCache) {
		return decodedColumn{}, false
	}
	dc := s.colCache[colIdx]
	if dc.data == nil {
		return decodedColumn{}, false
	}
	return dc, true
}

// GetAllColumnValues 提取指定行所有列的值。
func (s *Segment) GetAllColumnValues(rowIdx uint32, colMeta []ColumnMeta) (map[string]common.Value, error) {
	values := make(map[string]common.Value, len(colMeta))
	for colIdx, col := range colMeta {
		val, err := s.GetColumnValue(uint32(colIdx), rowIdx)
		if err != nil {
			continue
		}
		values[col.Name] = val
	}
	return values, nil
}
