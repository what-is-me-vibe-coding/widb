package storage

import (
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

	// 解码缓存：首次访问时延迟解码并缓存，避免点查时重复解压整列
	decodedCache []decodedColumn
	decodeOnce   sync.Once
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
func (b *SegmentBuilder) SetKeys(keys []string) {
	b.keys = make([]string, len(keys))
	copy(b.keys, keys)
}

// SetBloomFPRate 设置布隆过滤器目标误判率。
func (b *SegmentBuilder) SetBloomFPRate(fpRate float64) {
	b.fpRate = fpRate
}

// AddEncodedColumn 添加一个已编码的列。
func (b *SegmentBuilder) AddEncodedColumn(enc *EncodedColumn) {
	if enc == nil {
		return
	}
	clone := EncodedColumn{
		Encoding: enc.Encoding,
		Type:     enc.Type,
		RowCount: enc.RowCount,
	}
	if len(enc.Data) > 0 {
		clone.Data = make([]byte, len(enc.Data))
		copy(clone.Data, enc.Data)
	}
	if len(enc.Offsets) > 0 {
		clone.Offsets = make([]uint32, len(enc.Offsets))
		copy(clone.Offsets, enc.Offsets)
	}
	if len(enc.Dict) > 0 {
		clone.Dict = make([]string, len(enc.Dict))
		copy(clone.Dict, enc.Dict)
	}
	if len(enc.Nulls) > 0 {
		clone.Nulls = make([]byte, len(enc.Nulls))
		copy(clone.Nulls, enc.Nulls)
	}
	b.columns = append(b.columns, clone)
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
	case common.TypeInt64, common.TypeTimestamp:
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
	// 直接从 RLE runs 计算 min/max，避免完整解码
	runCount := len(enc.Data) / 16
	if runCount == 0 {
		// 无有效 run 数据，但 RowCount>0 时，旧行为是全零数组
		if enc.RowCount > 0 {
			stat.Min = int64ToBytes(0)
			stat.Max = int64ToBytes(0)
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
		stat.Min = int64ToBytes(minVal)
		stat.Max = int64ToBytes(maxVal)
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
		stat.Min = int64ToBytes(minVal)
		stat.Max = int64ToBytes(maxVal)
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
		stat.Min = float64ToBytes(minVal)
		stat.Max = float64ToBytes(maxVal)
	}
}

func computeStringStats(data []byte, offsets []uint32, rowCount uint32, nulls *common.Bitmap, stat *ColumnStat) {
	var minStr, maxStr string
	first := true
	for i := uint32(0); i < rowCount; i++ {
		if nulls != nil && nulls.Get(i) {
			continue
		}
		if int(i+1) >= len(offsets) {
			break
		}
		start := offsets[i]
		end := offsets[i+1]
		if int(end) > len(data) || int(start) > len(data) {
			break
		}
		s := string(data[start:end])
		if first {
			minStr, maxStr = s, s
			first = false
		} else {
			if s < minStr {
				minStr = s
			}
			if s > maxStr {
				maxStr = s
			}
		}
	}
	if !first {
		stat.Min = []byte(minStr)
		stat.Max = []byte(maxStr)
	}
}

func int64ToBytes(v int64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, uint64(v))
	return b
}

func float64ToBytes(v float64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, math.Float64bits(v))
	return b
}

// Build 构建 Segment，返回序列化后的字节流。
// 优化：先计算统计信息（需要未压缩数据），再原地压缩 b.columns，
// 避免额外的深拷贝。Build 后 builder 不可复用。
func (b *SegmentBuilder) Build() (*Segment, error) {
	if len(b.columns) == 0 {
		return nil, fmt.Errorf("segment builder: no columns added")
	}

	// 先计算统计信息（需要未压缩的原始数据）
	stats := make([]ColumnStat, len(b.columns))
	for i := range b.columns {
		stats[i] = computeColumnStat(&b.columns[i])
		stats[i].ColumnID = uint32(i)
	}

	// 原地压缩 b.columns，无需额外拷贝
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
	var buf []byte
	count := make([]byte, 4)
	binary.LittleEndian.PutUint32(count, uint32(len(keys)))
	buf = append(buf, count...)
	for _, k := range keys {
		kb := []byte(k)
		kl := make([]byte, 4)
		binary.LittleEndian.PutUint32(kl, uint32(len(kb)))
		buf = append(buf, kl...)
		buf = append(buf, kb...)
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
