package storage

import (
	"encoding/binary"
	"fmt"
)

// serializeFooter 将 SegmentFooter 序列化为字节流。
func serializeFooter(footer *SegmentFooter) []byte {
	var buf []byte

	colCount := make([]byte, 4)
	binary.LittleEndian.PutUint32(colCount, uint32(len(footer.ColumnStats)))
	buf = append(buf, colCount...)

	for _, stat := range footer.ColumnStats {
		colID := make([]byte, 4)
		binary.LittleEndian.PutUint32(colID, stat.ColumnID)
		buf = append(buf, colID...)

		minLen := make([]byte, 4)
		binary.LittleEndian.PutUint32(minLen, uint32(len(stat.Min)))
		buf = append(buf, minLen...)
		buf = append(buf, stat.Min...)

		maxLen := make([]byte, 4)
		binary.LittleEndian.PutUint32(maxLen, uint32(len(stat.Max)))
		buf = append(buf, maxLen...)
		buf = append(buf, stat.Max...)

		nullCount := make([]byte, 4)
		binary.LittleEndian.PutUint32(nullCount, stat.NullCount)
		buf = append(buf, nullCount...)
	}

	bloomLen := make([]byte, 4)
	binary.LittleEndian.PutUint32(bloomLen, uint32(len(footer.BloomFilter)))
	buf = append(buf, bloomLen...)
	buf = append(buf, footer.BloomFilter...)

	rawKeysLen := make([]byte, 4)
	binary.LittleEndian.PutUint32(rawKeysLen, uint32(len(footer.RawKeys)))
	buf = append(buf, rawKeysLen...)
	buf = append(buf, footer.RawKeys...)

	indexOffset := make([]byte, 8)
	binary.LittleEndian.PutUint64(indexOffset, uint64(footer.IndexOffset))
	buf = append(buf, indexOffset...)

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
