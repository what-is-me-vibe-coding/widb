package index

import (
	"testing"
)

// TestLookupBinarySearchEmptyIndex 测试 Lookup 在空索引上的行为。
func TestLookupBinarySearchEmptyIndex(t *testing.T) {
	pi := NewPrimaryIndex()
	ids := pi.Lookup("any")
	if ids != nil {
		t.Errorf("expected nil for empty index, got %v", ids)
	}
}

// TestLookupBinarySearchForwardScan 测试 Lookup 二分查找后向前扫描路径。
// 注册多个 segment，其中 key 落在排序后中间的 segment 中，
// 验证向前扫描能正确找到所有包含 key 的 segment。
func TestLookupBinarySearchForwardScan(t *testing.T) {
	pi := NewPrimaryIndex()

	// 注册 3 个不重叠的 segment
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "e"})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "f", MaxKey: "j"})
	_ = pi.RegisterSegment(SegmentMeta{ID: 3, MinKey: "k", MaxKey: "o"})

	// 查找 "h"，应该只落在 segment 2 中
	ids := pi.Lookup("h")
	if len(ids) != 1 || ids[0] != 2 {
		t.Errorf("Lookup('h') = %v, want [2]", ids)
	}
}

// TestLookupBinarySearchBackwardBreak 测试 Lookup 向前扫描的提前终止。
// 当向前扫描遇到 MaxKey < key 时应提前终止。
func TestLookupBinarySearchBackwardBreak(t *testing.T) {
	pi := NewPrimaryIndex()

	// 注册多个 segment，key "m" 只在 segment 3 中
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "e"})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "f", MaxKey: "j"})
	_ = pi.RegisterSegment(SegmentMeta{ID: 3, MinKey: "k", MaxKey: "o"})

	ids := pi.Lookup("m")
	if len(ids) != 1 || ids[0] != 3 {
		t.Errorf("Lookup('m') = %v, want [3]", ids)
	}
}

// TestLookupBinarySearchExactMinKey 测试 Lookup 查找恰好等于 MinKey 的 key。
func TestLookupBinarySearchExactMinKey(t *testing.T) {
	pi := NewPrimaryIndex()

	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "e"})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "f", MaxKey: "j"})

	// "f" 恰好是 segment 2 的 MinKey
	ids := pi.Lookup("f")
	if len(ids) != 1 || ids[0] != 2 {
		t.Errorf("Lookup('f') = %v, want [2]", ids)
	}
}

// TestLookupBinarySearchKeyBeforeAll 测试 Lookup 查找在所有 segment 之前的 key。
func TestLookupBinarySearchKeyBeforeAll(t *testing.T) {
	pi := NewPrimaryIndex()

	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "d", MaxKey: "h"})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "i", MaxKey: "m"})

	ids := pi.Lookup("a")
	if len(ids) != 0 {
		t.Errorf("Lookup('a') = %v, want nil", ids)
	}
}

// TestLookupBinarySearchKeyAfterAll 测试 Lookup 查找在所有 segment 之后的 key。
func TestLookupBinarySearchKeyAfterAll(t *testing.T) {
	pi := NewPrimaryIndex()

	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "e"})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "f", MaxKey: "j"})

	ids := pi.Lookup("z")
	if len(ids) != 0 {
		t.Errorf("Lookup('z') = %v, want nil", ids)
	}
}

// TestRangeBinarySearchEmptyIndex 测试 Range 在空索引上的行为。
func TestRangeBinarySearchEmptyIndex(t *testing.T) {
	pi := NewPrimaryIndex()
	ids := pi.Range("a", "z")
	if ids != nil {
		t.Errorf("expected nil for empty index, got %v", ids)
	}
}

// TestRangeBinarySearchManySegments 测试 Range 在大量 segment 上的二分查找效率。
// 注册 100 个 segment，验证 Range 只扫描相关 segment。
func TestRangeBinarySearchManySegments(t *testing.T) {
	pi := NewPrimaryIndex()

	// 注册 100 个不重叠的 segment: seg_0=[a0-a9], seg_1=[b0-b9], ...
	for i := 0; i < 100; i++ {
		minKey := string(rune('a'+i%26)) + string(rune('0'+i/26))
		maxKey := string(rune('a'+i%26)) + "9"
		_ = pi.RegisterSegment(SegmentMeta{
			ID:     uint64(i + 1),
			MinKey: minKey,
			MaxKey: maxKey,
		})
	}

	// 查询范围 [c0, f9]，应该只匹配 c、d、e、f 开头的 segment
	ids := pi.Range("c0", "f9")
	if len(ids) == 0 {
		t.Error("expected some segments to match range [c0, f9]")
	}

	// 验证所有返回的 segment 确实与查询范围有交集
	for _, id := range ids {
		seg, ok := pi.GetSegment(id)
		if !ok {
			t.Errorf("segment %d not found", id)
		}
		if !rangeOverlap("c0", "f9", seg.MinKey, seg.MaxKey) {
			t.Errorf("segment %d [%q,%q] should not overlap [c0,f9]", id, seg.MinKey, seg.MaxKey)
		}
	}
}

// TestRangeBinarySearchNoOverlap 测试 Range 查询范围不与任何 segment 重叠。
func TestRangeBinarySearchNoOverlap(t *testing.T) {
	pi := NewPrimaryIndex()

	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "e"})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "f", MaxKey: "j"})

	ids := pi.Range("l", "z")
	if len(ids) != 0 {
		t.Errorf("Range('l', 'z') = %v, want nil", ids)
	}
}

// TestLookupOverlappingL0Multiple 测试 Lookup 在多个 L0 重叠 segment 上的行为。
func TestLookupOverlappingL0Multiple(t *testing.T) {
	pi := NewPrimaryIndex()

	// 3 个 L0 segment 都覆盖 "e"-"g" 范围
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "h", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "e", MaxKey: "m", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 3, MinKey: "c", MaxKey: "j", Level: 0})

	ids := pi.Lookup("f")
	if len(ids) != 3 {
		t.Errorf("Lookup('f') with 3 overlapping L0 segments: got %d, want 3", len(ids))
	}
}

// TestRangeOverlappingL0 测试 Range 在 L0 重叠 segment 上的行为。
func TestRangeOverlappingL0(t *testing.T) {
	pi := NewPrimaryIndex()

	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "h", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "e", MaxKey: "m", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 3, MinKey: "n", MaxKey: "z", Level: 0})

	ids := pi.Range("f", "j")
	if len(ids) != 2 {
		t.Errorf("Range('f','j') with overlapping L0: got %d segments, want 2", len(ids))
	}
}

// TestLookupSingleSegment 测试 Lookup 在只有一个 segment 时的行为。
func TestLookupSingleSegment(t *testing.T) {
	pi := NewPrimaryIndex()

	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "z"})

	ids := pi.Lookup("m")
	if len(ids) != 1 || ids[0] != 1 {
		t.Errorf("Lookup('m') = %v, want [1]", ids)
	}

	ids = pi.Lookup("0")
	if len(ids) != 0 {
		t.Errorf("Lookup('0') = %v, want nil", ids)
	}
}
