package storage

import (
	"encoding/binary"
	"fmt"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func encodeDict(typ common.DataType, data any, rowCount uint32, nulls *common.Bitmap) (*EncodedColumn, error) {
	if typ != common.TypeString {
		return nil, fmt.Errorf("dict encode: only string type supported, got %v", typ)
	}
	strs, ok := data.([]string)
	if !ok {
		return nil, fmt.Errorf("dict encode: expected []string, got %T", data)
	}

	// 预分配 map 和 dict 切片，减少 rehash 和扩容开销。
	// 字典编码适用于低基数列，256 是常见基数上限的合理估计；
	// 若基数更高，map 会自动扩容，不影响正确性。
	dictHint := int(rowCount)
	if dictHint > 256 {
		dictHint = 256
	}
	dictMap := make(map[string]uint32, dictHint)
	dict := make([]string, 0, dictHint)
	indices := make([]uint32, rowCount)
	hasNulls := false
	for i := uint32(0); i < rowCount; i++ {
		if nulls != nil && nulls.Get(i) {
			hasNulls = true
			continue
		}
		idx, exists := dictMap[strs[i]]
		if !exists {
			idx = uint32(len(dict))
			dictMap[strs[i]] = idx
			dict = append(dict, strs[i])
		}
		indices[i] = idx
	}

	idxWidth := indexWidth(uint32(len(dict)), hasNulls)
	nullMarker := nullMarkerForWidth(idxWidth)
	idxBuf := make([]byte, rowCount*uint32(idxWidth))
	for i := uint32(0); i < rowCount; i++ {
		if nulls != nil && nulls.Get(i) {
			writeIndex(idxBuf, i, idxWidth, nullMarker)
		} else {
			writeIndex(idxBuf, i, idxWidth, indices[i])
		}
	}
	return &EncodedColumn{
		Encoding: EncodingDict,
		Type:     typ,
		RowCount: rowCount,
		Data:     idxBuf,
		Dict:     dict,
	}, nil
}

func decodeDict(enc *EncodedColumn) (any, *common.Bitmap, error) {
	idxWidth := indexWidth(uint32(len(enc.Dict)), true)
	nullMarker := nullMarkerForWidth(idxWidth)
	strs := make([]string, enc.RowCount)
	nulls := common.NewBitmap(enc.RowCount)

	for i := uint32(0); i < enc.RowCount; i++ {
		idx := readIndex(enc.Data, i, idxWidth)
		switch {
		case idx == nullMarker:
			nulls.Set(i)
			strs[i] = ""
		case int(idx) < len(enc.Dict):
			strs[i] = enc.Dict[idx]
		default:
			return nil, nil, fmt.Errorf("dict decode: index %d out of range for dict size %d", idx, len(enc.Dict))
		}
	}

	return strs, nulls, nil
}

func indexWidth(dictSize uint32, hasNulls bool) int {
	size := dictSize
	if hasNulls {
		size++
	}
	switch {
	case size <= 256:
		return 1
	case size <= 65536:
		return 2
	default:
		return 4
	}
}

func nullMarkerForWidth(width int) uint32 {
	switch width {
	case 1:
		return 0xFF
	case 2:
		return 0xFFFF
	default:
		return 0xFFFFFFFF
	}
}

func writeIndex(buf []byte, row uint32, width int, idx uint32) {
	pos := row * uint32(width)
	switch width {
	case 1:
		buf[pos] = byte(idx)
	case 2:
		binary.LittleEndian.PutUint16(buf[pos:], uint16(idx))
	case 4:
		binary.LittleEndian.PutUint32(buf[pos:], idx)
	}
}

func readIndex(buf []byte, row uint32, width int) uint32 {
	pos := row * uint32(width)
	switch width {
	case 1:
		return uint32(buf[pos])
	case 2:
		return uint32(binary.LittleEndian.Uint16(buf[pos:]))
	case 4:
		return binary.LittleEndian.Uint32(buf[pos:])
	default:
		return 0
	}
}
