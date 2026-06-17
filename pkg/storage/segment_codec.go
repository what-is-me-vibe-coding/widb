package storage

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// --- Segment Decode ---

// decodedColumn 缓存已解码的列数据，避免重复解压和解码。
type decodedColumn struct {
	data   any
	nulls  *common.Bitmap
	typ    common.DataType
	encTyp EncodingType
}

// prepareEncodedColumn 准备 EncodedColumn 的可变副本用于解码。
// 优化：Data 字段无需深拷贝，因为 DecompressColumn 会直接替换 enc.Data
// 为新分配的解压切片，不会修改原始压缩数据。Offsets/Dict/Nulls 只读可共享。
func prepareEncodedColumn(src *EncodedColumn) *EncodedColumn {
	return &EncodedColumn{
		Encoding: src.Encoding,
		Type:     src.Type,
		RowCount: src.RowCount,
		Data:     src.Data,    // 共享引用，DecompressColumn 会替换为新切片
		Offsets:  src.Offsets, // 只读，共享引用
		Dict:     src.Dict,    // 只读，共享引用
		Nulls:    src.Nulls,   // 只读，共享引用
	}
}

// decodeColumnFromEncoded 解压并解码单个 EncodedColumn，返回 decodedColumn。
// colIdx 仅用于错误信息上下文。
func decodeColumnFromEncoded(src *EncodedColumn, colIdx int) (decodedColumn, error) {
	enc := prepareEncodedColumn(src)
	if err := DecompressColumn(enc); err != nil {
		return decodedColumn{}, fmt.Errorf("decompress column %d: %w", colIdx, err)
	}
	data, nulls, err := DecodeColumn(enc)
	if err != nil {
		return decodedColumn{}, fmt.Errorf("decode column %d: %w", colIdx, err)
	}
	return decodedColumn{data: data, nulls: nulls, typ: src.Type, encTyp: src.Encoding}, nil
}

// extractValue 从已解码的列数据中提取指定行的值。
func extractValue(dc decodedColumn, row uint32) common.Value {
	if dc.nulls != nil && dc.nulls.Get(row) {
		return common.NewNull()
	}

	switch dc.typ {
	case common.TypeInt64, common.TypeInt8, common.TypeInt16,
		common.TypeInt32, common.TypeUint64, common.TypeDate:
		return extractIntFamilyValue(dc.typ, dc.data, row)
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

// extractIntFamilyValue 从已解码的 int64 数组中提取指定行，并按列类型构造 Value。
func extractIntFamilyValue(typ common.DataType, data any, row uint32) common.Value {
	if ints, ok := data.([]int64); ok && row < uint32(len(ints)) {
		return common.NewIntFamilyValue(typ, ints[row])
	}
	return common.NewNull()
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
		dc, err := decodeColumnFromEncoded(&s.Columns[i], i)
		if err != nil {
			return nil, fmt.Errorf("segment: %w", err)
		}
		columns[i] = dc
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
		dc, err := decodeColumnFromEncoded(&s.Columns[colIdx], int(colIdx))
		if err != nil {
			ds.mu.Unlock()
			return common.NewNull(), fmt.Errorf("segment: %w", err)
		}
		s.colCache[colIdx] = dc
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

// --- Segment Footer Serialize ---

// serializeFooter 将 SegmentFooter 序列化为字节流。
// 使用栈上临时缓冲区减少堆分配。
func serializeFooter(footer *SegmentFooter) []byte {
	// 预估总大小：列统计 + bloom + rawKeys + indexOffset
	estSize := 4 + 8*len(footer.ColumnStats) + 4 + len(footer.BloomFilter) + 4 + len(footer.RawKeys) + 8
	buf := make([]byte, 0, estSize)
	var tmp [8]byte // 栈上临时缓冲区，消除每字段 make([]byte, N) 的堆分配

	binary.LittleEndian.PutUint32(tmp[:4], uint32(len(footer.ColumnStats)))
	buf = append(buf, tmp[:4]...)

	for _, stat := range footer.ColumnStats {
		binary.LittleEndian.PutUint32(tmp[:4], stat.ColumnID)
		buf = append(buf, tmp[:4]...)

		binary.LittleEndian.PutUint32(tmp[:4], uint32(len(stat.Min)))
		buf = append(buf, tmp[:4]...)
		buf = append(buf, stat.Min...)

		binary.LittleEndian.PutUint32(tmp[:4], uint32(len(stat.Max)))
		buf = append(buf, tmp[:4]...)
		buf = append(buf, stat.Max...)

		binary.LittleEndian.PutUint32(tmp[:4], stat.NullCount)
		buf = append(buf, tmp[:4]...)
	}

	binary.LittleEndian.PutUint32(tmp[:4], uint32(len(footer.BloomFilter)))
	buf = append(buf, tmp[:4]...)
	buf = append(buf, footer.BloomFilter...)

	binary.LittleEndian.PutUint32(tmp[:4], uint32(len(footer.RawKeys)))
	buf = append(buf, tmp[:4]...)
	buf = append(buf, footer.RawKeys...)

	binary.LittleEndian.PutUint64(tmp[:8], uint64(footer.IndexOffset))
	buf = append(buf, tmp[:8]...)

	return buf
}

// deserializeFooter 从字节流反序列化 SegmentFooter。
func deserializeFooter(data []byte) (*SegmentFooter, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("segment: footer too short: %d bytes", len(data))
	}

	pos := 0
	colCount := binary.LittleEndian.Uint32(data[pos:])
	pos += 4

	footer := &SegmentFooter{
		ColumnStats: make([]ColumnStat, 0, colCount),
	}

	for i := uint32(0); i < colCount; i++ {
		stat, newPos, err := readColumnStat(data, pos, i)
		if err != nil {
			return nil, err
		}
		pos = newPos
		footer.ColumnStats = append(footer.ColumnStats, stat)
	}

	var err error
	pos, footer.BloomFilter, err = readBloomFilter(data, pos)
	if err != nil {
		return nil, err
	}

	pos, footer.RawKeys, err = readRawKeys(data, pos)
	if err != nil {
		return nil, err
	}

	if pos+8 > len(data) {
		return nil, fmt.Errorf("segment: footer truncated at index offset")
	}
	footer.IndexOffset = int64(binary.LittleEndian.Uint64(data[pos:]))

	return footer, nil
}

func readColumnStat(data []byte, pos int, idx uint32) (ColumnStat, int, error) {
	if pos+4 > len(data) {
		return ColumnStat{}, pos, fmt.Errorf("segment: footer truncated at column %d", idx)
	}
	stat := ColumnStat{}
	stat.ColumnID = binary.LittleEndian.Uint32(data[pos:])
	pos += 4

	var err error
	pos, stat.Min, err = readStatBytes(data, pos, "min", idx)
	if err != nil {
		return ColumnStat{}, pos, err
	}
	pos, stat.Max, err = readStatBytes(data, pos, "max", idx)
	if err != nil {
		return ColumnStat{}, pos, err
	}

	if pos+4 > len(data) {
		return ColumnStat{}, pos, fmt.Errorf("segment: footer truncated at null count for column %d", idx)
	}
	stat.NullCount = binary.LittleEndian.Uint32(data[pos:])
	pos += 4

	return stat, pos, nil
}

func readStatBytes(data []byte, pos int, field string, idx uint32) (int, []byte, error) {
	if pos+4 > len(data) {
		return pos, nil, fmt.Errorf("segment: footer truncated at %s length for column %d", field, idx)
	}
	byteLen := binary.LittleEndian.Uint32(data[pos:])
	pos += 4
	// 校验单列统计值长度上限，防止损坏数据导致 OOM
	const maxStatSize = 1 << 20 // 1MB
	if byteLen > maxStatSize {
		return pos, nil, fmt.Errorf("segment: %s length %d for column %d exceeds max %d, possibly corrupted", field, byteLen, idx, maxStatSize)
	}
	if byteLen > 0 {
		if pos+int(byteLen) > len(data) {
			return pos, nil, fmt.Errorf("segment: footer truncated at %s data for column %d", field, idx)
		}
		b := make([]byte, byteLen)
		copy(b, data[pos:pos+int(byteLen)])
		pos += int(byteLen)
		return pos, b, nil
	}
	return pos, nil, nil
}

func readBloomFilter(data []byte, pos int) (int, []byte, error) {
	if pos+4 > len(data) {
		return pos, nil, fmt.Errorf("segment: footer truncated at bloom length")
	}
	bloomLen := binary.LittleEndian.Uint32(data[pos:])
	pos += 4
	// 校验 bloom filter 长度上限，防止损坏数据导致 OOM
	const maxBloomSize = 16 << 20 // 16MB，远超正常布隆过滤器大小
	if bloomLen > maxBloomSize {
		return pos, nil, fmt.Errorf("segment: bloom filter length %d exceeds max %d, possibly corrupted", bloomLen, maxBloomSize)
	}
	if bloomLen > 0 {
		if pos+int(bloomLen) > len(data) {
			return pos, nil, fmt.Errorf("segment: footer truncated at bloom data")
		}
		b := make([]byte, bloomLen)
		copy(b, data[pos:pos+int(bloomLen)])
		pos += int(bloomLen)
		return pos, b, nil
	}
	return pos, nil, nil
}

func readRawKeys(data []byte, pos int) (int, []byte, error) {
	if pos+4 > len(data) {
		return pos, nil, nil
	}
	rawKeysLen := binary.LittleEndian.Uint32(data[pos:])
	pos += 4
	// 校验 rawKeys 长度上限，防止损坏数据导致 OOM
	const maxRawKeysSize = 64 << 20 // 64MB
	if rawKeysLen > maxRawKeysSize {
		return pos, nil, fmt.Errorf("segment: raw keys length %d exceeds max %d, possibly corrupted", rawKeysLen, maxRawKeysSize)
	}
	if rawKeysLen > 0 {
		if pos+int(rawKeysLen) > len(data) {
			return pos, nil, fmt.Errorf("segment: footer truncated at raw keys data")
		}
		b := make([]byte, rawKeysLen)
		copy(b, data[pos:pos+int(rawKeysLen)])
		pos += int(rawKeysLen)
		return pos, b, nil
	}
	return pos, nil, nil
}
