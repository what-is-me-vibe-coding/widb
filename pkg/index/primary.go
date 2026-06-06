package index

import (
	"fmt"
	"sort"
	"sync"
)

// SegmentMeta 描述注册到索引的 Segment 的元数据。
type SegmentMeta struct {
	ID     uint64
	MinKey string
	MaxKey string
	Level  int
}

// PrimaryIndex 是主键索引，维护每个 Segment 的键范围到 SegmentID 的映射。
// L0 层 Segment 允许键范围重叠，L1+ 层不允许重叠。
type PrimaryIndex struct {
	mu       sync.RWMutex
	segments []SegmentMeta
}

// NewPrimaryIndex 创建一个 PrimaryIndex。
func NewPrimaryIndex() *PrimaryIndex {
	return &PrimaryIndex{}
}

// RegisterSegment 注册一个 Segment 到索引中。
func (pi *PrimaryIndex) RegisterSegment(seg SegmentMeta) error {
	pi.mu.Lock()
	defer pi.mu.Unlock()

	if seg.ID == 0 {
		return fmt.Errorf("primary index: invalid segment ID 0")
	}
	if seg.MinKey > seg.MaxKey && seg.MinKey != "" && seg.MaxKey != "" {
		return fmt.Errorf("primary index: min key %q > max key %q", seg.MinKey, seg.MaxKey)
	}

	pi.segments = append(pi.segments, seg)
	pi.sortSegments()
	return nil
}

// UnregisterSegment 从索引中移除一个 Segment。
func (pi *PrimaryIndex) UnregisterSegment(segID uint64) error {
	pi.mu.Lock()
	defer pi.mu.Unlock()

	for i, seg := range pi.segments {
		if seg.ID == segID {
			pi.segments = append(pi.segments[:i], pi.segments[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("primary index: segment %d not found", segID)
}

// Lookup 点查：返回包含 key 的所有 Segment ID。
// 利用 segments 按 MinKey 排序的特性，使用二分查找快速定位候选范围。
func (pi *PrimaryIndex) Lookup(key string) []uint64 {
	pi.mu.RLock()
	defer pi.mu.RUnlock()

	if len(pi.segments) == 0 {
		return nil
	}

	var result []uint64

	// 二分查找：找到第一个 MinKey <= key 的 segment
	// 由于 segments 按 MinKey 排序，key 可能落在 MinKey <= key 的连续范围内
	idx := sort.Search(len(pi.segments), func(i int) bool {
		return pi.segments[i].MinKey > key
	})

	// 从 idx-1 开始向前扫描，因为 key 可能落在 MinKey <= key 的多个 segment 中
	// L0 层允许重叠，所以需要检查所有可能包含 key 的 segment
	for i := idx - 1; i >= 0; i-- {
		seg := pi.segments[i]
		if keyInRange(key, seg.MinKey, seg.MaxKey) {
			result = append(result, seg.ID)
		}
		// 如果当前 segment 的 MaxKey < key，更早的 segment 也不可能包含 key
		if seg.MaxKey < key {
			break
		}
	}

	// 从 idx 开始向后扫描，检查 MinKey == key 或 MinKey > key 但 MaxKey >= key 的 segment
	for i := idx; i < len(pi.segments); i++ {
		seg := pi.segments[i]
		if seg.MinKey > key {
			break // 后续 segment 的 MinKey 更大，不可能包含 key
		}
		if keyInRange(key, seg.MinKey, seg.MaxKey) {
			result = append(result, seg.ID)
		}
	}

	return result
}

// Range 范围查询：返回与 [start, end] 有交集的所有 Segment ID。
func (pi *PrimaryIndex) Range(start, end string) []uint64 {
	pi.mu.RLock()
	defer pi.mu.RUnlock()

	var result []uint64
	for _, seg := range pi.segments {
		if rangeOverlap(start, end, seg.MinKey, seg.MaxKey) {
			result = append(result, seg.ID)
		}
	}
	return result
}

// SegmentCount 返回已注册的 Segment 数量。
func (pi *PrimaryIndex) SegmentCount() int {
	pi.mu.RLock()
	defer pi.mu.RUnlock()
	return len(pi.segments)
}

// GetSegment 获取指定 ID 的 Segment 元数据。
func (pi *PrimaryIndex) GetSegment(segID uint64) (SegmentMeta, bool) {
	pi.mu.RLock()
	defer pi.mu.RUnlock()

	for _, seg := range pi.segments {
		if seg.ID == segID {
			return seg, true
		}
	}
	return SegmentMeta{}, false
}

// Clear 清空所有索引。
func (pi *PrimaryIndex) Clear() {
	pi.mu.Lock()
	defer pi.mu.Unlock()
	pi.segments = nil
}

func (pi *PrimaryIndex) sortSegments() {
	sort.Slice(pi.segments, func(i, j int) bool {
		return pi.segments[i].MinKey < pi.segments[j].MinKey
	})
}

func keyInRange(key, minKey, maxKey string) bool {
	if minKey == "" && maxKey == "" {
		return false
	}
	return key >= minKey && key <= maxKey
}

func rangeOverlap(start, end, minKey, maxKey string) bool {
	if minKey == "" && maxKey == "" {
		return false
	}
	return start <= maxKey && end >= minKey
}
