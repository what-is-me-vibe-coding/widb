package storage

import (
	"fmt"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// decodedColumn 缓存已解码的列数据，避免重复解压和解码。
type decodedColumn struct {
	data   interface{}
	nulls  *common.Bitmap
	typ    common.DataType
	encTyp EncodingType
}

// decodeAllColumns 一次性解压并解码 Segment 的所有列，返回解码缓存。
// 用于范围扫描等需要读取多行的场景，避免每行重复解压解码。
// 如果任一列解压或解码失败，返回错误。
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
			enc.Offsets = make([]uint32, len(src.Offsets))
			copy(enc.Offsets, src.Offsets)
		}
		if len(src.Dict) > 0 {
			enc.Dict = src.Dict // Dict 是只读的，无需深拷贝
		}
		if len(src.Nulls) > 0 {
			enc.Nulls = make([]byte, len(src.Nulls))
			copy(enc.Nulls, src.Nulls)
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
	dc := &cols[colIdx]
	cd := columnData{data: dc.data, nulls: dc.nulls, typ: dc.typ}
	return extractValue(cd, rowIdx)
}

// GetColumnValue 从指定列中提取给定行索引的值，使用副本避免修改原始数据。
func (s *Segment) GetColumnValue(colIdx uint32, rowIdx uint32) (common.Value, error) {
	if int(colIdx) >= len(s.Columns) {
		return common.NewNull(), fmt.Errorf("segment: column index %d out of range", colIdx)
	}
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
		enc.Offsets = make([]uint32, len(src.Offsets))
		copy(enc.Offsets, src.Offsets)
	}
	if len(src.Dict) > 0 {
		enc.Dict = src.Dict // Dict 是只读的，无需深拷贝
	}
	if len(src.Nulls) > 0 {
		enc.Nulls = make([]byte, len(src.Nulls))
		copy(enc.Nulls, src.Nulls)
	}
	if err := DecompressColumn(enc); err != nil {
		return common.NewNull(), fmt.Errorf("segment: decompress column %d: %w", colIdx, err)
	}
	decoded, nulls, err := DecodeColumn(enc)
	if err != nil {
		return common.NewNull(), fmt.Errorf("segment: decode column %d: %w", colIdx, err)
	}
	cd := columnData{data: decoded, nulls: nulls, typ: enc.Type}
	return extractValue(cd, rowIdx), nil
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
