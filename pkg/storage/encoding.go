package storage

import (
	"encoding/binary"
	"fmt"
	"math"
	"unsafe"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const rleThreshold = 0.5

// EncodingType 表示列数据的编码方式。
type EncodingType byte

// EncodingType 常量定义
const (
	EncodingPlain  EncodingType = 0
	EncodingDict   EncodingType = 1
	EncodingRLE    EncodingType = 2
	EncodingBitmap EncodingType = 3
)

// 编码类型名称常量
const (
	encodingPlainName  = "Plain"
	encodingDictName   = "Dict"
	encodingRLEName    = "RLE"
	encodingBitmapName = "Bitmap"
)

func (e EncodingType) String() string {
	switch e {
	case EncodingPlain:
		return encodingPlainName
	case EncodingDict:
		return encodingDictName
	case EncodingRLE:
		return encodingRLEName
	case EncodingBitmap:
		return encodingBitmapName
	default:
		return fmt.Sprintf("Unknown(%d)", e)
	}
}

// EncodedColumn 表示编码后的列数据
type EncodedColumn struct {
	Encoding EncodingType
	Type     common.DataType
	RowCount uint32
	Data     []byte
	Dict     []string
	Offsets  []uint32
	Nulls    []byte
}

// EncodeColumn 根据数据类型和编码策略对列数据进行编码
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

// DecodeColumn 解码 EncodedColumn 为原始数据
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
	if typ.IsIntFamily() {
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

	switch typ {
	case common.TypeInt64, common.TypeInt8, common.TypeInt16,
		common.TypeInt32, common.TypeUint64, common.TypeDate:
		return encodePlainInt64(typ, data, rowCount, nulls)
	case common.TypeFloat64:
		return encodePlainFloat64(data, rowCount, nulls)
	case common.TypeTimestamp:
		return encodePlainTimestamp(data, rowCount, nulls)
	case common.TypeString:
		strs, ok := data.([]string)
		if !ok {
			return nil, fmt.Errorf("plain encode: expected []string, got %T", data)
		}
		return encodePlainStrings(strs, rowCount, nulls)
	default:
		return nil, fmt.Errorf("plain encode: unsupported type %v", typ)
	}
}

// encodePlainInt64 将整数族列编码为 Plain 格式，保留原始类型标签。
func encodePlainInt64(typ common.DataType, data any, rowCount uint32, nulls *common.Bitmap) (*EncodedColumn, error) {
	ints, ok := data.([]int64)
	if !ok {
		return nil, fmt.Errorf("plain encode: expected []int64, got %T", data)
	}
	buf := encodeUint64Batch(ints, rowCount)
	return newPlainEncodedColumn(typ, rowCount, buf, nulls), nil
}

// encodePlainFloat64 将 float64 列编码为 Plain 格式
func encodePlainFloat64(data any, rowCount uint32, nulls *common.Bitmap) (*EncodedColumn, error) {
	floats, ok := data.([]float64)
	if !ok {
		return nil, fmt.Errorf("plain encode: expected []float64, got %T", data)
	}
	buf := encodeFloat64Batch(floats, rowCount)
	return newPlainEncodedColumn(common.TypeFloat64, rowCount, buf, nulls), nil
}

// encodePlainTimestamp 将 timestamp 列编码为 Plain 格式
func encodePlainTimestamp(data any, rowCount uint32, nulls *common.Bitmap) (*EncodedColumn, error) {
	times, ok := data.([]int64)
	if !ok {
		return nil, fmt.Errorf("plain encode: expected []int64 (unix nanos), got %T", data)
	}
	buf := encodeUint64Batch(times, rowCount)
	return newPlainEncodedColumn(common.TypeTimestamp, rowCount, buf, nulls), nil
}

// encodeLE64Batch 将 8 字节定长切片编码为小端字节序列。
// 在小端架构（x86/ARM）上使用 unsafe 零拷贝转换，避免逐元素 binary.LittleEndian 调用；
// 在大端架构上回退到逐元素转换保证正确性。
// 调用方需保证 T 为 8 字节定长类型（int64/float64），否则小端快路径的内存视图长度不正确。
func encodeLE64Batch[T any](vals []T, rowCount uint32, toBits func(T) uint64) []byte {
	buf := make([]byte, rowCount*8)
	if rowCount == 0 {
		return buf
	}
	// 小端架构：直接内存拷贝，零转换开销
	if isLittleEndian() {
		src := unsafe.Slice((*byte)(unsafe.Pointer(&vals[0])), int(rowCount)*8)
		copy(buf, src)
		return buf
	}
	// 大端架构回退
	for i := uint32(0); i < rowCount; i++ {
		binary.LittleEndian.PutUint64(buf[i*8:], toBits(vals[i]))
	}
	return buf
}

// encodeUint64Batch 将 int64 切片编码为小端字节序列。
// 保留为具名入口以供测试与文档引用，内部委托给泛型实现。
func encodeUint64Batch(ints []int64, rowCount uint32) []byte {
	return encodeLE64Batch(ints, rowCount, func(v int64) uint64 { return uint64(v) })
}

// encodeFloat64Batch 将 float64 切片编码为小端字节序列。
// 保留为具名入口以供测试与文档引用，内部委托给泛型实现。
func encodeFloat64Batch(floats []float64, rowCount uint32) []byte {
	return encodeLE64Batch(floats, rowCount, math.Float64bits)
}

// isLittleEndian 检测当前系统是否为小端字节序。
// 结果在进程生命周期内不变，适合缓存为包级变量。
func isLittleEndian() bool {
	// 缓存结果避免重复检测
	return isLE
}

var isLE = detectLittleEndian()

func detectLittleEndian() bool {
	var v uint16 = 1
	return *(*byte)(unsafe.Pointer(&v)) == 1
}

// newPlainEncodedColumn 创建 Plain 编码的 EncodedColumn
func newPlainEncodedColumn(typ common.DataType, rowCount uint32, data []byte, nulls *common.Bitmap) *EncodedColumn {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     typ,
		RowCount: rowCount,
		Data:     data,
	}
	if nulls != nil && !nulls.IsEmpty() {
		enc.Nulls = nulls.ToBytes()
	}
	return enc
}

func encodePlainStrings(strs []string, rowCount uint32, nulls *common.Bitmap) (*EncodedColumn, error) {
	offsets := make([]uint32, rowCount+1)
	// 预计算总字节数，一次性分配 dataBuf
	totalBytes := 0
	for i := uint32(0); i < rowCount; i++ {
		if nulls == nil || !nulls.Get(i) {
			totalBytes += len(strs[i])
		}
	}
	dataBuf := make([]byte, 0, totalBytes)
	for i := uint32(0); i < rowCount; i++ {
		offsets[i] = uint32(len(dataBuf))
		if nulls == nil || !nulls.Get(i) {
			dataBuf = append(dataBuf, strs[i]...)
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

func encodeRLE(typ common.DataType, data any, rowCount uint32, nulls *common.Bitmap) (*EncodedColumn, error) {
	if !typ.IsIntFamily() {
		return nil, fmt.Errorf("rle encode: only int family supported, got %v", typ)
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
	case common.TypeInt64, common.TypeInt8, common.TypeInt16,
		common.TypeInt32, common.TypeUint64, common.TypeDate:
		return decodePlainInt64(enc.Data), nulls, nil
	case common.TypeFloat64:
		return decodePlainFloat64(enc.Data), nulls, nil
	case common.TypeTimestamp:
		return decodePlainTimestamp(enc.Data), nulls, nil
	case common.TypeString:
		// 校验 Offsets 长度，防止损坏的列块触发越界 panic。
		if uint32(len(enc.Offsets)) < enc.RowCount+1 {
			return nil, nil, fmt.Errorf("plain decode: offsets length %d < rowcount+1 %d", len(enc.Offsets), enc.RowCount+1)
		}
		strs := make([]string, enc.RowCount)
		dataLen := uint32(len(enc.Data))
		for i := uint32(0); i < enc.RowCount; i++ {
			start := enc.Offsets[i]
			end := enc.Offsets[i+1]
			if start > end || end > dataLen {
				return nil, nil, fmt.Errorf("plain decode: invalid string offset [%d:%d] for data len %d", start, end, dataLen)
			}
			strs[i] = string(enc.Data[start:end])
		}
		return strs, nulls, nil
	default:
		return nil, nil, fmt.Errorf("plain decode: unsupported type %v", enc.Type)
	}
}

// decodeLE64Batch 从小端字节序列解码为 8 字节定长切片。
// 在小端架构上使用 unsafe 零拷贝转换。
// 调用方需保证 T 为 8 字节定长类型（int64/float64），否则小端快路径的内存视图长度不正确。
func decodeLE64Batch[T any](data []byte, fromBits func(uint64) T) []T {
	count := len(data) / 8
	vals := make([]T, count)
	if count > 0 && isLittleEndian() {
		dst := unsafe.Slice((*byte)(unsafe.Pointer(&vals[0])), count*8)
		copy(dst, data)
	} else {
		for i := 0; i < count; i++ {
			vals[i] = fromBits(binary.LittleEndian.Uint64(data[i*8:]))
		}
	}
	return vals
}

// decodePlainInt64 从小端字节序列解码 int64 切片。
func decodePlainInt64(data []byte) []int64 {
	return decodeLE64Batch(data, func(v uint64) int64 { return int64(v) })
}

// decodePlainFloat64 从小端字节序列解码 float64 切片。
func decodePlainFloat64(data []byte) []float64 {
	return decodeLE64Batch(data, math.Float64frombits)
}

// decodePlainTimestamp 从小端字节序列解码 timestamp（int64）切片。
func decodePlainTimestamp(data []byte) []int64 {
	return decodeLE64Batch(data, func(v uint64) int64 { return int64(v) })
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
