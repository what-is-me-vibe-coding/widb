package storage

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const rleThreshold = 0.5

// EncodingType 表示列数据的编码方式。
type EncodingType byte

// EncodingType 常量定义。
const (
	EncodingPlain  EncodingType = 0
	EncodingDict   EncodingType = 1
	EncodingRLE    EncodingType = 2
	EncodingBitmap EncodingType = 3
)

func (e EncodingType) String() string {
	switch e {
	case EncodingPlain:
		return "Plain"
	case EncodingDict:
		return "Dict"
	case EncodingRLE:
		return "RLE"
	case EncodingBitmap:
		return "Bitmap"
	default:
		return fmt.Sprintf("Unknown(%d)", e)
	}
}

// EncodedColumn 表示编码后的列数据。
type EncodedColumn struct {
	Encoding EncodingType
	Type     common.DataType
	RowCount uint32
	Data     []byte
	Dict     []string
	Offsets  []uint32
	Nulls    []byte
}

// EncodeColumn 根据数据类型和编码策略对列数据进行编码。
func EncodeColumn(typ common.DataType, data any, rowCount uint32, nulls *common.Bitmap) (*EncodedColumn, error) {
	encoding := selectEncoding(typ, data, rowCount)
	switch encoding {
	case EncodingPlain:
		return encodePlain(typ, data, rowCount, nulls)
	case EncodingDict:
		return encodeDict(typ, data, rowCount, nulls)
	case EncodingRLE:
		return encodeRLE(typ, data, rowCount, nulls)
	case EncodingBitmap:
		return encodeBitmap(data, rowCount, nulls)
	default:
		return nil, fmt.Errorf("unknown encoding: %v", encoding)
	}
}

// DecodeColumn 解码 EncodedColumn 为原始数据。
func DecodeColumn(enc *EncodedColumn) (any, *common.Bitmap, error) {
	switch enc.Encoding {
	case EncodingPlain:
		return decodePlain(enc)
	case EncodingDict:
		return decodeDict(enc)
	case EncodingRLE:
		return decodeRLE(enc)
	case EncodingBitmap:
		return decodeBitmap(enc)
	default:
		return nil, nil, fmt.Errorf("unknown encoding: %v", enc.Encoding)
	}
}

func selectEncoding(typ common.DataType, data any, rowCount uint32) EncodingType {
	if typ == common.TypeBool {
		return EncodingBitmap
	}
	if typ == common.TypeString {
		return EncodingDict
	}
	if typ == common.TypeInt64 {
		if isRLEInt64(data, rowCount) {
			return EncodingRLE
		}
	}
	return EncodingPlain
}

func isRLEInt64(data any, rowCount uint32) bool {
	ints, ok := data.([]int64)
	if !ok || len(ints) < 2 {
		return false
	}
	runCount := 1
	for i := uint32(1); i < rowCount && i < uint32(len(ints)); i++ {
		if ints[i] != ints[i-1] {
			runCount++
		}
	}
	return float64(runCount)/float64(rowCount) <= rleThreshold
}

func encodePlain(typ common.DataType, data any, rowCount uint32, nulls *common.Bitmap) (*EncodedColumn, error) {
	if rowCount == 0 {
		return &EncodedColumn{Encoding: EncodingPlain, Type: typ, RowCount: 0}, nil
	}

	var buf []byte
	switch typ {
	case common.TypeInt64:
		ints, ok := data.([]int64)
		if !ok {
			return nil, fmt.Errorf("plain encode: expected []int64, got %T", data)
		}
		buf = make([]byte, rowCount*8)
		for i := uint32(0); i < rowCount; i++ {
			binary.LittleEndian.PutUint64(buf[i*8:], uint64(ints[i]))
		}
	case common.TypeFloat64:
		floats, ok := data.([]float64)
		if !ok {
			return nil, fmt.Errorf("plain encode: expected []float64, got %T", data)
		}
		buf = make([]byte, rowCount*8)
		for i := uint32(0); i < rowCount; i++ {
			binary.LittleEndian.PutUint64(buf[i*8:], math.Float64bits(floats[i]))
		}
	case common.TypeTimestamp:
		times, ok := data.([]int64)
		if !ok {
			return nil, fmt.Errorf("plain encode: expected []int64 (unix nanos), got %T", data)
		}
		buf = make([]byte, rowCount*8)
		for i := uint32(0); i < rowCount; i++ {
			binary.LittleEndian.PutUint64(buf[i*8:], uint64(times[i]))
		}
	case common.TypeString:
		strs, ok := data.([]string)
		if !ok {
			return nil, fmt.Errorf("plain encode: expected []string, got %T", data)
		}
		return encodePlainStrings(strs, rowCount, nulls)
	default:
		return nil, fmt.Errorf("plain encode: unsupported type %v", typ)
	}

	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     typ,
		RowCount: rowCount,
		Data:     buf,
	}
	if nulls != nil && !nulls.IsEmpty() {
		enc.Nulls = nulls.ToBytes()
	}
	return enc, nil
}

func encodePlainStrings(strs []string, rowCount uint32, nulls *common.Bitmap) (*EncodedColumn, error) {
	offsets := make([]uint32, rowCount+1)
	var dataBuf []byte

	for i := uint32(0); i < rowCount; i++ {
		offsets[i] = uint32(len(dataBuf))
		if nulls == nil || !nulls.Get(i) {
			dataBuf = append(dataBuf, []byte(strs[i])...)
		}
	}
	offsets[rowCount] = uint32(len(dataBuf))

	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeString,
		RowCount: rowCount,
		Data:     dataBuf,
		Offsets:  offsets,
	}
	if nulls != nil && !nulls.IsEmpty() {
		enc.Nulls = nulls.ToBytes()
	}
	return enc, nil
}

func encodeDict(typ common.DataType, data any, rowCount uint32, nulls *common.Bitmap) (*EncodedColumn, error) {
	if typ != common.TypeString {
		return nil, fmt.Errorf("dict encode: only string type supported, got %v", typ)
	}

	strs, ok := data.([]string)
	if !ok {
		return nil, fmt.Errorf("dict encode: expected []string, got %T", data)
	}

	dictMap := make(map[string]uint32)
	dict := make([]string, 0)
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

func encodeRLE(typ common.DataType, data any, rowCount uint32, nulls *common.Bitmap) (*EncodedColumn, error) {
	if typ != common.TypeInt64 {
		return nil, fmt.Errorf("rle encode: only int64 type supported, got %v", typ)
	}

	ints, ok := data.([]int64)
	if !ok {
		return nil, fmt.Errorf("rle encode: expected []int64, got %T", data)
	}

	var runs []rleRun
	for i := uint32(0); i < rowCount; i++ {
		isNull := nulls != nil && nulls.Get(i)
		val := int64(0)
		if !isNull {
			val = ints[i]
		}

		if len(runs) == 0 || runs[len(runs)-1].value != val || runs[len(runs)-1].isNull != isNull {
			runs = append(runs, rleRun{value: val, count: 1, isNull: isNull})
		} else {
			runs[len(runs)-1].count++
		}
	}

	buf := make([]byte, len(runs)*16)
	for i, r := range runs {
		pos := i * 16
		binary.LittleEndian.PutUint64(buf[pos:], uint64(r.value))
		binary.LittleEndian.PutUint32(buf[pos+8:], r.count)
		if r.isNull {
			buf[pos+12] = 1
		}
	}

	return &EncodedColumn{
		Encoding: EncodingRLE,
		Type:     typ,
		RowCount: rowCount,
		Data:     buf,
	}, nil
}

func encodeBitmap(data any, rowCount uint32, nulls *common.Bitmap) (*EncodedColumn, error) {
	bools, ok := data.([]uint64)
	if !ok {
		return nil, fmt.Errorf("bitmap encode: expected []uint64, got %T", data)
	}

	bm := common.NewBitmap(rowCount)
	for i := uint32(0); i < rowCount && i < uint32(len(bools)); i++ {
		if bools[i] != 0 {
			bm.Set(i)
		}
	}

	enc := &EncodedColumn{
		Encoding: EncodingBitmap,
		Type:     common.TypeBool,
		RowCount: rowCount,
		Data:     bm.ToBytes(),
	}
	if nulls != nil && !nulls.IsEmpty() {
		enc.Nulls = nulls.ToBytes()
	}
	return enc, nil
}

func decodePlain(enc *EncodedColumn) (any, *common.Bitmap, error) {
	var nulls *common.Bitmap
	if len(enc.Nulls) > 0 {
		nulls = common.NewBitmapFromBytes(enc.Nulls)
	}

	switch enc.Type {
	case common.TypeInt64:
		count := len(enc.Data) / 8
		ints := make([]int64, count)
		for i := 0; i < count; i++ {
			ints[i] = int64(binary.LittleEndian.Uint64(enc.Data[i*8:]))
		}
		return ints, nulls, nil
	case common.TypeFloat64:
		count := len(enc.Data) / 8
		floats := make([]float64, count)
		for i := 0; i < count; i++ {
			floats[i] = math.Float64frombits(binary.LittleEndian.Uint64(enc.Data[i*8:]))
		}
		return floats, nulls, nil
	case common.TypeTimestamp:
		count := len(enc.Data) / 8
		times := make([]int64, count)
		for i := 0; i < count; i++ {
			times[i] = int64(binary.LittleEndian.Uint64(enc.Data[i*8:]))
		}
		return times, nulls, nil
	case common.TypeString:
		strs := make([]string, enc.RowCount)
		for i := uint32(0); i < enc.RowCount; i++ {
			start := enc.Offsets[i]
			end := enc.Offsets[i+1]
			strs[i] = string(enc.Data[start:end])
		}
		return strs, nulls, nil
	default:
		return nil, nil, fmt.Errorf("plain decode: unsupported type %v", enc.Type)
	}
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

func decodeRLE(enc *EncodedColumn) (any, *common.Bitmap, error) {
	runCount := len(enc.Data) / 16
	ints := make([]int64, enc.RowCount)
	nulls := common.NewBitmap(enc.RowCount)

	pos := uint32(0)
	for r := 0; r < runCount && pos < enc.RowCount; r++ {
		off := r * 16
		val := int64(binary.LittleEndian.Uint64(enc.Data[off:]))
		count := binary.LittleEndian.Uint32(enc.Data[off+8:])
		isNull := enc.Data[off+12] == 1

		for c := uint32(0); c < count && pos < enc.RowCount; c++ {
			if isNull {
				nulls.Set(pos)
			}
			ints[pos] = val
			pos++
		}
	}

	return ints, nulls, nil
}

func decodeBitmap(enc *EncodedColumn) (any, *common.Bitmap, error) {
	bm := common.NewBitmapFromBytes(enc.Data)
	bools := make([]uint64, enc.RowCount)
	for i := uint32(0); i < enc.RowCount; i++ {
		if bm.Get(i) {
			bools[i] = 1
		}
	}
	var nulls *common.Bitmap
	if len(enc.Nulls) > 0 {
		nulls = common.NewBitmapFromBytes(enc.Nulls)
	}
	return bools, nulls, nil
}

type rleRun struct {
	value  int64
	count  uint32
	isNull bool
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
