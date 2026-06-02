package storage

import (
	"fmt"
	"sync/atomic"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const (
	// defaultBlockCacheCapacity 是 BlockCache 的默认容量（字节）。
	defaultBlockCacheCapacity = 64 * 1024 * 1024 // 64MB
)

// CachedBlock 是缓存中的解码后列块数据。
type CachedBlock struct {
	Data     interface{} // 解码后的数据（具体类型取决于 DataType）
	Nulls    *common.Bitmap
	Type     common.DataType
	RowCount uint32
	Size     int // 估算内存占用
}

// BlockCache 缓存解压解码后的列块数据，避免重复解压解码开销。
// 键格式：segmentID:columnID
type BlockCache struct {
	cache   *common.LRUCache
	lookups uint64
	hits    uint64
}

// BlockCacheConfig 是 BlockCache 的配置参数。
type BlockCacheConfig struct {
	Capacity int // 缓存容量（字节），0 表示使用默认值
}

// NewBlockCache 创建一个 BlockCache 实例。
func NewBlockCache(cfg BlockCacheConfig) *BlockCache {
	capacity := cfg.Capacity
	if capacity <= 0 {
		capacity = defaultBlockCacheCapacity
	}
	return &BlockCache{
		cache: common.NewLRUCache(capacity),
	}
}

// blockCacheKey 生成缓存键。
func blockCacheKey(segID uint64, colIdx uint32) string {
	return fmt.Sprintf("%d:%d", segID, colIdx)
}

// Get 从缓存中获取解码后的列块数据。
func (bc *BlockCache) Get(segID uint64, colIdx uint32) (*CachedBlock, bool) {
	atomic.AddUint64(&bc.lookups, 1)
	val, ok := bc.cache.Get(blockCacheKey(segID, colIdx))
	if !ok {
		return nil, false
	}
	atomic.AddUint64(&bc.hits, 1)
	return val.(*CachedBlock), true
}

// Put 将解码后的列块数据放入缓存。
func (bc *BlockCache) Put(segID uint64, colIdx uint32, block *CachedBlock) {
	if block == nil {
		return
	}
	bc.cache.Put(blockCacheKey(segID, colIdx), block, block.Size)
}

// InvalidateSegment 使指定 Segment 的所有缓存条目失效。
func (bc *BlockCache) InvalidateSegment(segID uint64, columnCount int) {
	for i := 0; i < columnCount; i++ {
		bc.cache.Delete(blockCacheKey(segID, uint32(i)))
	}
}

// Invalidate 使指定列块的缓存失效。
func (bc *BlockCache) Invalidate(segID uint64, colIdx uint32) {
	bc.cache.Delete(blockCacheKey(segID, colIdx))
}

// Clear 清空所有缓存。
func (bc *BlockCache) Clear() {
	bc.cache.Clear()
}

// Stats 返回缓存统计信息。
func (bc *BlockCache) Stats() BlockCacheStats {
	hit, miss := bc.cache.Stats()
	return BlockCacheStats{
		HitCount:    hit,
		MissCount:   miss,
		LookupCount: atomic.LoadUint64(&bc.lookups),
		EntryCount:  bc.cache.Len(),
		TotalSize:   bc.cache.TotalSize(),
		HitRate:     bc.cache.HitRate(),
	}
}

// BlockCacheStats 是 BlockCache 的统计信息。
type BlockCacheStats struct {
	HitCount    uint64
	MissCount   uint64
	LookupCount uint64
	EntryCount  int
	TotalSize   int
	HitRate     float64
}

// estimateBlockSize 估算解码后列块的内存占用。
func estimateBlockSize(data interface{}, nulls *common.Bitmap, _ common.DataType, rowCount uint32) int {
	size := 0
	switch d := data.(type) {
	case []int64:
		size = len(d) * 8
	case []float64:
		size = len(d) * 8
	case []string:
		for _, s := range d {
			size += len(s) + 16 // 字符串头 + 数据
		}
	case []uint64:
		size = len(d) * 8
	case []bool:
		size = (len(d) + 7) / 8
	default:
		size = int(rowCount) * 8
	}
	if nulls != nil {
		size += int((rowCount + 7) / 8)
	}
	return size
}

// decodeColumnForCache 解码指定列并返回可缓存的 CachedBlock。
func decodeColumnForCache(seg *Segment, colIdx uint32) (*CachedBlock, error) {
	if int(colIdx) >= len(seg.Columns) {
		return nil, fmt.Errorf("column index %d out of range", colIdx)
	}
	src := &seg.Columns[colIdx]
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
		enc.Dict = make([]string, len(src.Dict))
		copy(enc.Dict, src.Dict)
	}
	if len(src.Nulls) > 0 {
		enc.Nulls = make([]byte, len(src.Nulls))
		copy(enc.Nulls, src.Nulls)
	}
	if err := DecompressColumn(enc); err != nil {
		return nil, fmt.Errorf("decompress column %d: %w", colIdx, err)
	}
	decoded, nulls, err := DecodeColumn(enc)
	if err != nil {
		return nil, fmt.Errorf("decode column %d: %w", colIdx, err)
	}
	block := &CachedBlock{
		Data:     decoded,
		Nulls:    nulls,
		Type:     enc.Type,
		RowCount: enc.RowCount,
	}
	block.Size = estimateBlockSize(decoded, nulls, enc.Type, enc.RowCount)
	return block, nil
}

// extractValueFromCachedBlock 从缓存的列块中提取指定行的值。
func extractValueFromCachedBlock(block *CachedBlock, rowIdx uint32) common.Value {
	if block == nil {
		return common.NewNull()
	}
	cd := columnData{data: block.Data, nulls: block.Nulls, typ: block.Type}
	return extractValue(cd, rowIdx)
}
