package storage

import (
	"encoding/binary"
	"fmt"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// SerializeColumnBlock 将 EncodedColumn 序列化为字节流。
func SerializeColumnBlock(enc *EncodedColumn) []byte {
	var buf []byte

	colID := make([]byte, 4)
	buf = append(buf, colID...)

	buf = append(buf, byte(enc.Encoding))

	compressed := byte(0)
	buf = append(buf, compressed)

	buf = append(buf, byte(enc.Type))

	rowCount := make([]byte, 4)
	binary.LittleEndian.PutUint32(rowCount, enc.RowCount)
	buf = append(buf, rowCount...)

	nullsLen := make([]byte, 4)
	binary.LittleEndian.PutUint32(nullsLen, uint32(len(enc.Nulls)))
	buf = append(buf, nullsLen...)
	buf = append(buf, enc.Nulls...)

	dataLen := make([]byte, 4)
	binary.LittleEndian.PutUint32(dataLen, uint32(len(enc.Data)))
	buf = append(buf, dataLen...)
	buf = append(buf, enc.Data...)

	offsetsBytes := make([]byte, len(enc.Offsets)*4)
	for i, off := range enc.Offsets {
		binary.LittleEndian.PutUint32(offsetsBytes[i*4:], off)
	}
	offsetsLen := make([]byte, 4)
	binary.LittleEndian.PutUint32(offsetsLen, uint32(len(enc.Offsets)))
	buf = append(buf, offsetsLen...)
	buf = append(buf, offsetsBytes...)

	dictLen := make([]byte, 4)
	binary.LittleEndian.PutUint32(dictLen, uint32(len(enc.Dict)))
	buf = append(buf, dictLen...)
	for _, s := range enc.Dict {
		strBytes := []byte(s)
		strLen := make([]byte, 4)
		binary.LittleEndian.PutUint32(strLen, uint32(len(strBytes)))
		buf = append(buf, strLen...)
		buf = append(buf, strBytes...)
	}

	return buf
}

// DeserializeColumnBlock 从字节流反序列化 EncodedColumn。
func DeserializeColumnBlock(data []byte) (*EncodedColumn, error) {
	if len(data) < 16 {
		return nil, fmt.Errorf("segment: column block too short: %d bytes", len(data))
	}

	pos := 4

	enc := &EncodedColumn{}
	enc.Encoding = EncodingType(data[pos])
	pos += 2

	enc.Type = common.DataType(data[pos])
	pos++

	enc.RowCount = binary.LittleEndian.Uint32(data[pos:])
	pos += 4

	var err error
	pos, err = readNulls(data, pos, enc)
	if err != nil {
		return nil, err
	}
	pos, err = readColumnData(data, pos, enc)
	if err != nil {
		return nil, err
	}
	pos, err = readOffsets(data, pos, enc)
	if err != nil {
		return nil, err
	}
	_, err = readDict(data, pos, enc)
	if err != nil {
		return nil, err
	}

	return enc, nil
}

func readNulls(data []byte, pos int, enc *EncodedColumn) (int, error) {
	nullsLen := binary.LittleEndian.Uint32(data[pos:])
	pos += 4
	if nullsLen > 0 {
		if pos+int(nullsLen) > len(data) {
			return pos, fmt.Errorf("segment: nulls data exceeds buffer")
		}
		enc.Nulls = make([]byte, nullsLen)
		copy(enc.Nulls, data[pos:pos+int(nullsLen)])
		pos += int(nullsLen)
	}
	return pos, nil
}

func readColumnData(data []byte, pos int, enc *EncodedColumn) (int, error) {
	if pos+4 > len(data) {
		return pos, fmt.Errorf("segment: data length field exceeds buffer")
	}
	dataLen := binary.LittleEndian.Uint32(data[pos:])
	pos += 4
	if dataLen > 0 {
		if pos+int(dataLen) > len(data) {
			return pos, fmt.Errorf("segment: column data exceeds buffer")
		}
		enc.Data = make([]byte, dataLen)
		copy(enc.Data, data[pos:pos+int(dataLen)])
		pos += int(dataLen)
	}
	return pos, nil
}

func readOffsets(data []byte, pos int, enc *EncodedColumn) (int, error) {
	if pos+4 > len(data) {
		return pos, fmt.Errorf("segment: offsets length field exceeds buffer")
	}
	offsetsLen := binary.LittleEndian.Uint32(data[pos:])
	pos += 4
	if offsetsLen > 0 {
		if pos+int(offsetsLen)*4 > len(data) {
			return pos, fmt.Errorf("segment: offsets data exceeds buffer")
		}
		enc.Offsets = make([]uint32, offsetsLen)
		for i := uint32(0); i < offsetsLen; i++ {
			enc.Offsets[i] = binary.LittleEndian.Uint32(data[pos:])
			pos += 4
		}
	}
	return pos, nil
}

func readDict(data []byte, pos int, enc *EncodedColumn) (int, error) {
	if pos+4 > len(data) {
		return pos, fmt.Errorf("segment: dict length field exceeds buffer")
	}
	dictLen := binary.LittleEndian.Uint32(data[pos:])
	pos += 4
	if dictLen > 0 {
		enc.Dict = make([]string, 0, dictLen)
		for i := uint32(0); i < dictLen; i++ {
			if pos+4 > len(data) {
				return pos, fmt.Errorf("segment: dict string length field exceeds buffer")
			}
			strLen := binary.LittleEndian.Uint32(data[pos:])
			pos += 4
			if strLen > 0 {
				if pos+int(strLen) > len(data) {
					return pos, fmt.Errorf("segment: dict string data exceeds buffer")
				}
				enc.Dict = append(enc.Dict, string(data[pos:pos+int(strLen)]))
				pos += int(strLen)
			} else {
				enc.Dict = append(enc.Dict, "")
			}
		}
	}
	return pos, nil
}

// Serialize 将 Segment 序列化为完整的文件字节流。
func (s *Segment) Serialize() ([]byte, error) {
	var buf []byte

	magic := make([]byte, 4)
	binary.LittleEndian.PutUint32(magic, segmentMagic)
	buf = append(buf, magic...)

	version := make([]byte, 2)
	binary.LittleEndian.PutUint16(version, segmentVersion)
	buf = append(buf, version...)

	for i := range s.Columns {
		colID := make([]byte, 4)
		binary.LittleEndian.PutUint32(colID, uint32(i))
		colData := SerializeColumnBlock(&s.Columns[i])
		colData[0] = colID[0]
		colData[1] = colID[1]
		colData[2] = colID[2]
		colData[3] = colID[3]
		buf = append(buf, colData...)
	}

	footerBytes := serializeFooter(&s.Footer)
	footerLen := make([]byte, 4)
	binary.LittleEndian.PutUint32(footerLen, uint32(len(footerBytes)))
	buf = append(buf, footerLen...)
	buf = append(buf, footerBytes...)

	footerOffset := uint64(len(buf) - len(footerBytes))
	footerOffsetBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(footerOffsetBytes, footerOffset)
	buf = append(buf, footerOffsetBytes...)

	return buf, nil
}

// DeserializeSegment 从字节流反序列化 Segment。
func DeserializeSegment(data []byte) (*Segment, error) {
	if len(data) < 22 {
		return nil, fmt.Errorf("segment: data too short: %d bytes", len(data))
	}

	magic := binary.LittleEndian.Uint32(data[0:])
	if magic != segmentMagic {
		return nil, fmt.Errorf("segment: invalid magic %08x, expected %08x", magic, segmentMagic)
	}

	footerOffsetPos := len(data) - 8
	footerOffset := int64(binary.LittleEndian.Uint64(data[footerOffsetPos:]))

	footerLen := binary.LittleEndian.Uint32(data[footerOffset-4:])
	footerStart := footerOffset
	footerEnd := footerStart + int64(footerLen)
	if footerEnd > int64(footerOffsetPos) {
		return nil, fmt.Errorf("segment: footer data exceeds footer offset")
	}

	footer, err := deserializeFooter(data[footerStart:footerEnd])
	if err != nil {
		return nil, fmt.Errorf("segment: deserialize footer: %w", err)
	}

	seg := &Segment{
		Footer: *footer,
		Keys:   deserializeKeys(footer.RawKeys),
	}

	columns, err := readColumns(data, footerOffset)
	if err != nil {
		return nil, err
	}
	seg.Columns = columns

	if len(seg.Columns) > 0 {
		seg.RowCount = seg.Columns[0].RowCount
	}

	return seg, nil
}

func readColumns(data []byte, footerOffset int64) ([]EncodedColumn, error) {
	columnsStart := 6
	columnsEnd := int(footerOffset) - 4

	var columns []EncodedColumn
	colPos := columnsStart
	for colPos < columnsEnd {
		col, err := DeserializeColumnBlock(data[colPos:columnsEnd])
		if err != nil {
			return nil, fmt.Errorf("segment: deserialize column at offset %d: %w", colPos, err)
		}
		columns = append(columns, *col)

		nextOff := skipColumnBlock(data, colPos, columnsEnd)
		colPos += nextOff
	}

	return columns, nil
}

func skipColumnBlock(data []byte, colPos, columnsEnd int) int {
	nextOff := 4 + 1 + 1 + 1 + 4

	nullsLen := uint32(0)
	if colPos+nextOff+4 <= columnsEnd {
		nullsLen = binary.LittleEndian.Uint32(data[colPos+nextOff:])
	}
	nextOff += 4 + int(nullsLen)

	dataLen := uint32(0)
	if colPos+nextOff+4 <= columnsEnd {
		dataLen = binary.LittleEndian.Uint32(data[colPos+nextOff:])
	}
	nextOff += 4 + int(dataLen)

	offsetsLen := uint32(0)
	if colPos+nextOff+4 <= columnsEnd {
		offsetsLen = binary.LittleEndian.Uint32(data[colPos+nextOff:])
	}
	nextOff += 4 + int(offsetsLen)*4

	dictLen := uint32(0)
	if colPos+nextOff+4 <= columnsEnd {
		dictLen = binary.LittleEndian.Uint32(data[colPos+nextOff:])
	}
	nextOff += 4
	for i := uint32(0); i < dictLen; i++ {
		if colPos+nextOff+4 > columnsEnd {
			break
		}
		strLen := binary.LittleEndian.Uint32(data[colPos+nextOff:])
		nextOff += 4 + int(strLen)
	}

	return nextOff
}
