package storage

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"sync"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
)

const segmentMagic uint32 = 0x57494442

const segmentVersion uint16 = 1

// ColumnStat 存储单个列的统计信息。
type ColumnStat struct {
	ColumnID  uint32
	Min       []byte
	Max       []byte
	NullCount uint32
}

// SegmentFooter 是 Segment 文件的尾部元数据。
type SegmentFooter struct {
	ColumnStats []ColumnStat
	BloomFilter []byte
	RawKeys     []byte
	IndexOffset int64
}

// Segment 表示一个不可变的列式存储段。
type Segment struct {
	ID       uint64
	MinKey   string
	MaxKey   string
	RowCount uint32
	Columns  []EncodedColumn
	Footer   SegmentFooter
	FilePath string
	Keys     []string

	// 逐列延迟解码缓存：首次访问某列时解码并缓存，避免点查时解码所有列
	colCache       []decodedColumn
	colDecodeState []colDecodeState
	cacheInit      sync.Once
}

// colDecodeState 跟踪逐列解码状态，支持解码失败时重试。
// 替代 sync.Once 以解决解码失败后不可重试的问题。
type colDecodeState struct {
	mu      sync.Mutex
	decoded bool
}

// SegmentID 返回 Segment 的唯一标识。
func (s *Segment) SegmentID() uint64 {
	return s.ID
}

// ForEachColumnStat 遍历所有列的统计信息。
func (s *Segment) ForEachColumnStat(fn func(colID uint32, colType common.DataType, minVal, maxVal []byte, nullCount uint32)) {
	for _, stat := range s.Footer.ColumnStats {
		var dt common.DataType
		if int(stat.ColumnID) < len(s.Columns) {
			dt = s.Columns[stat.ColumnID].Type
		}
		fn(stat.ColumnID, dt, stat.Min, stat.Max, stat.NullCount)
	}
}

// SegmentBuilder 从 Chunk 构建 Segment。
type SegmentBuilder struct {
	id      uint64
	minKey  string
	maxKey  string
	keys    []string
	fpRate  float64
	columns []EncodedColumn
}

// NewSegmentBuilder 创建 SegmentBuilder。
func NewSegmentBuilder(id uint64, minKey, maxKey string) *SegmentBuilder {
	return &SegmentBuilder{
		id:     id,
		minKey: minKey,
		maxKey: maxKey,
		fpRate: 0.01,
	}
}

// SetKeys 设置主键数据，用于构建布隆过滤器。
// 直接赋值 keys 切片引用，避免不必要的拷贝。
// Build 时会对 seg.Keys 做深拷贝，因此 builder 后续修改不影响已构建的 Segment。
func (b *SegmentBuilder) SetKeys(keys []string) {
	b.keys = keys
}

// SetBloomFPRate 设置布隆过滤器目标误判率。
func (b *SegmentBuilder) SetBloomFPRate(fpRate float64) {
	b.fpRate = fpRate
}

// AddEncodedColumn 添加一个已编码的列。
// 转移 EncodedColumn 所有权，避免深拷贝开销。
// 调用后 enc 被清零（*enc = EncodedColumn{}），防止调用方误用已转移的数据。
// SegmentBuilder 在 Build 时会对列数据进行压缩（原地修改），
// 因此所有权转移是安全的——builder 是数据的唯一消费者。
func (b *SegmentBuilder) AddEncodedColumn(enc *EncodedColumn) {
	if enc == nil {
		return
	}
	b.columns = append(b.columns, *enc)
	*enc = EncodedColumn{}
}

// computeColumnStat 计算单列的统计信息。
func computeColumnStat(enc *EncodedColumn) ColumnStat {
	stat := ColumnStat{}

	var nulls *common.Bitmap
	if len(enc.Nulls) > 0 {
		nulls = common.NewBitmapFromBytes(enc.Nulls)
	}

	for i := uint32(0); i < enc.RowCount; i++ {
		if nulls != nil && nulls.Get(i) {
			stat.NullCount++
		}
	}

	computeMinMax(enc, nulls, &stat)

	return stat
}

func computeMinMax(enc *EncodedColumn, nulls *common.Bitmap, stat *ColumnStat) {
	switch enc.Encoding {
	case EncodingPlain:
		computePlainMinMax(enc, nulls, stat)
	case EncodingDict:
		computeDictMinMax(enc, stat)
	case EncodingRLE:
		computeRLEMinMax(enc, stat)
	case EncodingBitmap:
		computeBitmapMinMax(enc, stat)
	}
}

func computePlainMinMax(enc *EncodedColumn, nulls *common.Bitmap, stat *ColumnStat) {
	switch enc.Type {
	case common.TypeInt64, common.TypeTimestamp, common.TypeInt8, common.TypeInt16,
		common.TypeInt32, common.TypeUint64, common.TypeDate:
		computeIntStats(enc.Data, enc.RowCount, nulls, stat)
	case common.TypeFloat64:
		computeFloatStats(enc.Data, enc.RowCount, nulls, stat)
	case common.TypeString:
		computeStringStats(enc.Data, enc.Offsets, enc.RowCount, nulls, stat)
	}
}

func computeDictMinMax(enc *EncodedColumn, stat *ColumnStat) {
	if len(enc.Dict) > 0 {
		stat.Min = []byte(enc.Dict[0])
		stat.Max = []byte(enc.Dict[len(enc.Dict)-1])
	}
}

func computeRLEMinMax(enc *EncodedColumn, stat *ColumnStat) {
	runCount := len(enc.Data) / 16
	if runCount == 0 {
		if enc.RowCount > 0 {
			stat.Min = appendInt64Bytes(nil, 0)
			stat.Max = appendInt64Bytes(nil, 0)
		}
		return
	}
	first := true
	var minVal, maxVal int64
	for r := 0; r < runCount; r++ {
		off := r * 16
		if off+16 > len(enc.Data) {
			break
		}
		val := int64(binary.LittleEndian.Uint64(enc.Data[off:]))
		isNull := enc.Data[off+12] == 1
		if isNull {
			continue
		}
		if first {
			minVal, maxVal = val, val
			first = false
		} else {
			if val < minVal {
				minVal = val
			}
			if val > maxVal {
				maxVal = val
			}
		}
	}
	if !first {
		stat.Min = appendInt64Bytes(nil, minVal)
		stat.Max = appendInt64Bytes(nil, maxVal)
	}
}

func computeBitmapMinMax(enc *EncodedColumn, stat *ColumnStat) {
	if stat.NullCount < enc.RowCount {
		stat.Min = []byte{0}
		stat.Max = []byte{1}
	}
}

func computeIntStats(data []byte, rowCount uint32, nulls *common.Bitmap, stat *ColumnStat) {
	var minVal, maxVal int64
	first := true
	for i := uint32(0); i < rowCount && int(i)*8+8 <= len(data); i++ {
		if nulls != nil && nulls.Get(i) {
			continue
		}
		v := int64(binary.LittleEndian.Uint64(data[i*8:]))
		if first {
			minVal, maxVal = v, v
			first = false
		} else {
			if v < minVal {
				minVal = v
			}
			if v > maxVal {
				maxVal = v
			}
		}
	}
	if !first {
		stat.Min = appendInt64Bytes(nil, minVal)
		stat.Max = appendInt64Bytes(nil, maxVal)
	}
}

func computeFloatStats(data []byte, rowCount uint32, nulls *common.Bitmap, stat *ColumnStat) {
	var minVal, maxVal float64
	first := true
	for i := uint32(0); i < rowCount && int(i)*8+8 <= len(data); i++ {
		if nulls != nil && nulls.Get(i) {
			continue
		}
		v := math.Float64frombits(binary.LittleEndian.Uint64(data[i*8:]))
		if first {
			minVal, maxVal = v, v
			first = false
		} else {
			if v < minVal {
				minVal = v
			}
			if v > maxVal {
				maxVal = v
			}
		}
	}
	if !first {
		stat.Min = appendFloat64Bytes(nil, minVal)
		stat.Max = appendFloat64Bytes(nil, maxVal)
	}
}

func computeStringStats(data []byte, offsets []uint32, rowCount uint32, nulls *common.Bitmap, stat *ColumnStat) {
	var minBytes, maxBytes []byte
	first := true
	for i := uint32(0); i < rowCount; i++ {
		if nulls != nil && nulls.Get(i) {
			continue
		}
		if int(i)+1 >= len(offsets) {
			break
		}
		start := offsets[i]
		end := offsets[i+1]
		if int(end) > len(data) || int(start) > len(data) {
			break
		}
		s := data[start:end]
		if first {
			minBytes = s
			maxBytes = s
			first = false
		} else {
			if bytes.Compare(s, minBytes) < 0 {
				minBytes = s
			}
			if bytes.Compare(s, maxBytes) > 0 {
				maxBytes = s
			}
		}
	}
	if !first {
		stat.Min = make([]byte, len(minBytes))
		copy(stat.Min, minBytes)
		stat.Max = make([]byte, len(maxBytes))
		copy(stat.Max, maxBytes)
	}
}

// appendInt64Bytes 将 int64 值以小端字节序追加到 buf，避免中间堆分配。
func appendInt64Bytes(buf []byte, v int64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(v))
	return append(buf, b[:]...)
}

// appendFloat64Bytes 将 float64 值以小端字节序追加到 buf，避免中间堆分配。
func appendFloat64Bytes(buf []byte, v float64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], math.Float64bits(v))
	return append(buf, b[:]...)
}

// int64ToBytes 将 int64 值转换为小端字节切片。
// 保留此函数供测试代码使用，内部使用 appendInt64Bytes 避免堆分配。
func int64ToBytes(v int64) []byte {
	return appendInt64Bytes(nil, v)
}

// Build 构建 Segment，返回序列化后的字节流。
func (b *SegmentBuilder) Build() (*Segment, error) {
	if len(b.columns) == 0 {
		return nil, fmt.Errorf("segment builder: no columns added")
	}

	stats := make([]ColumnStat, len(b.columns))
	for i := range b.columns {
		stats[i] = computeColumnStat(&b.columns[i])
		stats[i].ColumnID = uint32(i)
	}

	for i := range b.columns {
		if err := CompressColumn(&b.columns[i]); err != nil {
			return nil, fmt.Errorf("segment builder: compress column %d: %w", i, err)
		}
	}

	seg := &Segment{
		ID:       b.id,
		MinKey:   b.minKey,
		MaxKey:   b.maxKey,
		RowCount: b.columns[0].RowCount,
		Columns:  b.columns,
		Footer: SegmentFooter{
			ColumnStats: stats,
			IndexOffset: 0,
		},
	}

	if len(b.keys) > 0 {
		data, err := index.BuildFromKeys(b.keys, b.fpRate)
		if err != nil {
			return nil, fmt.Errorf("segment builder: build bloom filter: %w", err)
		}
		seg.Footer.BloomFilter = data
		// 深拷贝 keys 到 Segment，确保 Segment 与 builder 生命周期独立
		seg.Keys = make([]string, len(b.keys))
		copy(seg.Keys, b.keys)
		seg.Footer.RawKeys = serializeKeys(b.keys)
	}

	return seg, nil
}

func serializeKeys(keys []string) []byte {
	if len(keys) == 0 {
		return nil
	}
	// 预计算总大小：4(count) + 每个key的 4(len) + len(key)
	totalSize := 4
	for _, k := range keys {
		totalSize += 4 + len(k)
	}
	buf := make([]byte, 0, totalSize)
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], uint32(len(keys)))
	buf = append(buf, tmp[:]...)
	for _, k := range keys {
		binary.LittleEndian.PutUint32(tmp[:], uint32(len(k)))
		buf = append(buf, tmp[:]...)
		buf = append(buf, k...)
	}
	return buf
}

func deserializeKeys(data []byte) []string {
	if len(data) < 4 {
		return nil
	}
	count := binary.LittleEndian.Uint32(data[0:])
	pos := 4
	keys := make([]string, 0, count)
	for i := uint32(0); i < count; i++ {
		if pos+4 > len(data) {
			break
		}
		kLen := binary.LittleEndian.Uint32(data[pos:])
		pos += 4
		if pos+int(kLen) > len(data) {
			break
		}
		keys = append(keys, string(data[pos:pos+int(kLen)]))
		pos += int(kLen)
	}
	return keys
}

// FindRowByKey 使用二分查找定位指定主键的行索引。
func (s *Segment) FindRowByKey(key string) (uint32, bool) {
	if len(s.Keys) == 0 {
		return 0, false
	}
	lo, hi := 0, len(s.Keys)-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		if s.Keys[mid] == key {
			return uint32(mid), true
		}
		if s.Keys[mid] < key {
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return 0, false
}

// FindRowByKeyGE 使用二分查找返回第一个 keys[i] >= key 的行索引。
// 当所有 keys 都小于 key 时返回 (0, false)。
func (s *Segment) FindRowByKeyGE(key string) (uint32, bool) {
	n := len(s.Keys)
	if n == 0 {
		return 0, false
	}
	lo, hi := 0, n
	for lo < hi {
		mid := int(uint(lo+hi) >> 1) // 防止 lo+hi 溢出
		if s.Keys[mid] < key {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo >= n {
		return 0, false
	}
	return uint32(lo), true
}

// FindRowByKeyLE 使用二分查找返回最后一个 keys[i] <= key 的行索引。
// 当所有 keys 都大于 key 时返回 (0, false)。
func (s *Segment) FindRowByKeyLE(key string) (uint32, bool) {
	n := len(s.Keys)
	if n == 0 {
		return 0, false
	}
	lo, hi := 0, n
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if s.Keys[mid] <= key {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return 0, false
	}
	return uint32(lo - 1), true
}

// ComputeRange 通过二分查找计算 [start, end] 在 Keys 数组中的下标范围。
// 不相交（含空段、start>max、end<min）时返回 (0, 0, false)。
// 用于 segmentIterator 构造时一次性确定有效行范围。
func (s *Segment) ComputeRange(start, end string) (uint32, uint32, bool) {
	n := len(s.Keys)
	if n == 0 {
		return 0, 0, false
	}
	startIdx, startOK := s.FindRowByKeyGE(start)
	if !startOK {
		return 0, 0, false
	}
	endIdx, endOK := s.FindRowByKeyLE(end)
	if !endOK || startIdx > endIdx {
		return 0, 0, false
	}
	return startIdx, endIdx, true
}
