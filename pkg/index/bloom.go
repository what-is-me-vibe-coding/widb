package index

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/bits-and-blooms/bloom/v3"
)

// DefaultBloomFPRate 是布隆过滤器的默认误判率。
const DefaultBloomFPRate = 0.01

// SegmentBloom 存储一个 Segment 的布隆过滤器。
type SegmentBloom struct {
	SegID  uint64
	Filter *bloom.BloomFilter
}

// BloomIndex 管理所有 Segment 的布隆过滤器，用于主键存在性快速判断。
type BloomIndex struct {
	mu      sync.RWMutex
	blooms  map[uint64]*bloom.BloomFilter
	hitCnt  uint64
	missCnt uint64
}

// NewBloomIndex 创建一个 BloomIndex。
func NewBloomIndex() *BloomIndex {
	return &BloomIndex{
		blooms: make(map[uint64]*bloom.BloomFilter),
	}
}

// Register 注册一个 Segment 的布隆过滤器。
func (bi *BloomIndex) Register(segID uint64, filter *bloom.BloomFilter) error {
	if filter == nil {
		return fmt.Errorf("bloom index: nil filter for segment %d", segID)
	}
	bi.mu.Lock()
	defer bi.mu.Unlock()
	bi.blooms[segID] = filter
	return nil
}

// RegisterFromBytes 从序列化字节注册布隆过滤器。
func (bi *BloomIndex) RegisterFromBytes(segID uint64, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	filter := &bloom.BloomFilter{}
	if err := filter.UnmarshalBinary(data); err != nil {
		return fmt.Errorf("bloom index: unmarshal segment %d: %w", segID, err)
	}
	bi.mu.Lock()
	defer bi.mu.Unlock()
	bi.blooms[segID] = filter
	return nil
}

// Unregister 从索引中移除一个 Segment 的布隆过滤器。
func (bi *BloomIndex) Unregister(segID uint64) {
	bi.mu.Lock()
	defer bi.mu.Unlock()
	delete(bi.blooms, segID)
}

// MayContain 检查 key 是否可能存在于指定 Segment 中。
// 返回 false 表示一定不存在，可跳过该 Segment。
func (bi *BloomIndex) MayContain(segID uint64, key []byte) bool {
	bi.mu.RLock()
	filter, ok := bi.blooms[segID]
	bi.mu.RUnlock()

	if !ok {
		return true
	}

	result := filter.Test(key)
	if result {
		atomic.AddUint64(&bi.hitCnt, 1)
	} else {
		atomic.AddUint64(&bi.missCnt, 1)
	}

	return result
}

// Stats 返回布隆过滤器的命中/未命中统计。
func (bi *BloomIndex) Stats() (hit, miss uint64) {
	return atomic.LoadUint64(&bi.hitCnt), atomic.LoadUint64(&bi.missCnt)
}

// Len 返回已注册的布隆过滤器数量。
func (bi *BloomIndex) Len() int {
	bi.mu.RLock()
	defer bi.mu.RUnlock()
	return len(bi.blooms)
}

// Clear 清空所有布隆过滤器。
func (bi *BloomIndex) Clear() {
	bi.mu.Lock()
	defer bi.mu.Unlock()
	bi.blooms = make(map[uint64]*bloom.BloomFilter)
	atomic.StoreUint64(&bi.hitCnt, 0)
	atomic.StoreUint64(&bi.missCnt, 0)
}

// BuildFromKeys 根据主键集合构建布隆过滤器的序列化字节。
// n 是预期元素数，fpRate 是目标误判率。
func BuildFromKeys(keys []string, fpRate float64) ([]byte, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	if fpRate <= 0 || fpRate >= 1 {
		fpRate = DefaultBloomFPRate
	}

	filter := bloom.NewWithEstimates(uint(len(keys)), fpRate)
	for _, k := range keys {
		filter.Add([]byte(k))
	}

	data, err := filter.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("bloom index: marshal: %w", err)
	}
	return data, nil
}

// BuildAndRegister 构建布隆过滤器并注册到索引。
func (bi *BloomIndex) BuildAndRegister(segID uint64, keys []string, fpRate float64) error {
	data, err := BuildFromKeys(keys, fpRate)
	if err != nil {
		return err
	}
	if data == nil {
		return nil
	}
	return bi.RegisterFromBytes(segID, data)
}
