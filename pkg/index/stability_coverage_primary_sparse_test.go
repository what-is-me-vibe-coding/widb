package index

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ==================== Lookup 覆盖率测试 ====================

// TestLookupEmptyIndex 测试在空索引上执行点查。
func TestLookupEmptyIndex(t *testing.T) {
	pi := NewPrimaryIndex()
	result := pi.Lookup("any-key")
	if result != nil {
		t.Errorf("expected nil for lookup on empty index, got %v", result)
	}
}

// TestLookupKeyBetweenSegments 测试查找落在两个 Segment 间隙中的 key。
func TestLookupKeyBetweenSegments(t *testing.T) {
	pi := NewPrimaryIndex()
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "c", Level: 1})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "f", MaxKey: "h", Level: 1})
	result := pi.Lookup("e")
	if len(result) != 0 {
		t.Errorf("expected no segments for key between ranges, got %v", result)
	}
}

// TestLookupKeyAtExactMinKeyBoundary 测试查找 key 恰好等于某个 Segment 的 MinKey。
func TestLookupKeyAtExactMinKeyBoundary(t *testing.T) {
	pi := NewPrimaryIndex()
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "c", MaxKey: "f", Level: 1})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "g", MaxKey: "m", Level: 1})
	result := pi.Lookup("c")
	if len(result) != 1 {
		t.Errorf("expected 1 segment for key=MinKey boundary, got %d", len(result))
	}
	if len(result) > 0 && result[0] != 1 {
		t.Errorf("expected segment ID 1, got %d", result[0])
	}
	result = pi.Lookup("g")
	if len(result) != 1 {
		t.Errorf("expected 1 segment for key=MinKey boundary, got %d", len(result))
	}
	if len(result) > 0 && result[0] != 2 {
		t.Errorf("expected segment ID 2, got %d", result[0])
	}
}

// TestLookupOverlappingL0SameKey 测试多个 L0 Segment 包含相同 key 的场景。
func TestLookupOverlappingL0SameKey(t *testing.T) {
	pi := NewPrimaryIndex()
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "e", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "c", MaxKey: "g", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 3, MinKey: "b", MaxKey: "f", Level: 0})
	result := pi.Lookup("d")
	if len(result) != 3 {
		t.Errorf("expected 3 overlapping L0 segments for key 'd', got %d", len(result))
	}
	found := make(map[uint64]bool)
	for _, id := range result {
		found[id] = true
	}
	if !found[1] || !found[2] || !found[3] {
		t.Errorf("expected all 3 segments in result, got %v", result)
	}
}

// TestLookupKeyAtMaxKeyBoundary 测试查找 key 恰好等于 Segment 的 MaxKey。
func TestLookupKeyAtMaxKeyBoundary(t *testing.T) {
	pi := NewPrimaryIndex()
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "f", Level: 1})
	result := pi.Lookup("f")
	if len(result) != 1 {
		t.Errorf("expected 1 segment for key=MaxKey, got %d", len(result))
	}
}

// TestLookupKeyBeforeAllSegments 测试查找 key 小于所有 Segment 的 MinKey。
func TestLookupKeyBeforeAllSegments(t *testing.T) {
	pi := NewPrimaryIndex()
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "d", MaxKey: "h", Level: 1})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "j", MaxKey: "m", Level: 1})
	result := pi.Lookup("a")
	if len(result) != 0 {
		t.Errorf("expected no segments for key before all ranges, got %v", result)
	}
}

// TestLookupKeyAfterAllSegments 测试查找 key 大于所有 Segment 的 MaxKey。
func TestLookupKeyAfterAllSegments(t *testing.T) {
	pi := NewPrimaryIndex()
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "c", Level: 1})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "e", MaxKey: "g", Level: 1})
	result := pi.Lookup("z")
	if len(result) != 0 {
		t.Errorf("expected no segments for key after all ranges, got %v", result)
	}
}

// ==================== CanSkip 覆盖率测试 ====================

// TestCanSkipNoRegisteredStats 测试 CanSkip 在没有注册任何统计信息时的行为。
func TestCanSkipNoRegisteredStats(t *testing.T) {
	si := NewSparseIndex()
	if si.CanSkip(999, 0, OpEqual, common.NewInt64(10)) {
		t.Error("should not skip when no stats registered")
	}
	if si.CanSkip(999, 0, OpLess, common.NewInt64(10)) {
		t.Error("should not skip when no stats registered")
	}
	if si.CanSkip(999, 0, OpGreater, common.NewInt64(10)) {
		t.Error("should not skip when no stats registered")
	}
}

// TestCanSkipOpNotEqual 测试 CanSkip 对 OpNotEqual 始终返回 false。
func TestCanSkipOpNotEqual(t *testing.T) {
	si := NewSparseIndex()
	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)
	if si.CanSkip(1, 0, OpNotEqual, common.NewInt64(50)) {
		t.Error("OpNotEqual should always return false (value in range)")
	}
	if si.CanSkip(1, 0, OpNotEqual, common.NewInt64(5)) {
		t.Error("OpNotEqual should always return false (value below min)")
	}
	if si.CanSkip(1, 0, OpNotEqual, common.NewInt64(200)) {
		t.Error("OpNotEqual should always return false (value above max)")
	}
}

// TestCanSkipOpLessEdgeCases 测试 CanSkip 对 OpLess 的边界情况。
func TestCanSkipOpLessEdgeCases(t *testing.T) {
	si := NewSparseIndex()
	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)
	if !si.CanSkip(1, 0, OpLess, common.NewInt64(5)) {
		t.Error("should skip OpLess(5) when min=10")
	}
	if !si.CanSkip(1, 0, OpLess, common.NewInt64(10)) {
		t.Error("should skip OpLess(10) when min=10")
	}
	if si.CanSkip(1, 0, OpLess, common.NewInt64(11)) {
		t.Error("should not skip OpLess(11) when min=10")
	}
	if si.CanSkip(1, 0, OpLess, common.NewInt64(100)) {
		t.Error("should not skip OpLess(100) when min=10")
	}
	if si.CanSkip(1, 0, OpLess, common.NewInt64(200)) {
		t.Error("should not skip OpLess(200) when min=10")
	}
}

// TestCanSkipOpGreaterEdgeCases 测试 CanSkip 对 OpGreater 的边界情况。
func TestCanSkipOpGreaterEdgeCases(t *testing.T) {
	si := NewSparseIndex()
	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)
	if !si.CanSkip(1, 0, OpGreater, common.NewInt64(200)) {
		t.Error("should skip OpGreater(200) when max=100")
	}
	if !si.CanSkip(1, 0, OpGreater, common.NewInt64(100)) {
		t.Error("should skip OpGreater(100) when max=100")
	}
	if si.CanSkip(1, 0, OpGreater, common.NewInt64(99)) {
		t.Error("should not skip OpGreater(99) when max=100")
	}
	if si.CanSkip(1, 0, OpGreater, common.NewInt64(50)) {
		t.Error("should not skip OpGreater(50) when max=100")
	}
	if si.CanSkip(1, 0, OpGreater, common.NewInt64(5)) {
		t.Error("should not skip OpGreater(5) when max=100")
	}
}

// TestCanSkipOpLessEqualEdgeCases 测试 CanSkip 对 OpLessEqual 的边界情况。
func TestCanSkipOpLessEqualEdgeCases(t *testing.T) {
	si := NewSparseIndex()
	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)
	if !si.CanSkip(1, 0, OpLessEqual, common.NewInt64(5)) {
		t.Error("should skip OpLessEqual(5) when min=10")
	}
	if !si.CanSkip(1, 0, OpLessEqual, common.NewInt64(9)) {
		t.Error("should skip OpLessEqual(9) when min=10")
	}
	if si.CanSkip(1, 0, OpLessEqual, common.NewInt64(10)) {
		t.Error("should not skip OpLessEqual(10) when min=10")
	}
	if si.CanSkip(1, 0, OpLessEqual, common.NewInt64(50)) {
		t.Error("should not skip OpLessEqual(50) when min=10")
	}
}

// TestCanSkipOpGreaterEqualEdgeCases 测试 CanSkip 对 OpGreaterEqual 的边界情况。
func TestCanSkipOpGreaterEqualEdgeCases(t *testing.T) {
	si := NewSparseIndex()
	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)
	if !si.CanSkip(1, 0, OpGreaterEqual, common.NewInt64(200)) {
		t.Error("should skip OpGreaterEqual(200) when max=100")
	}
	if !si.CanSkip(1, 0, OpGreaterEqual, common.NewInt64(101)) {
		t.Error("should skip OpGreaterEqual(101) when max=100")
	}
	if si.CanSkip(1, 0, OpGreaterEqual, common.NewInt64(100)) {
		t.Error("should not skip OpGreaterEqual(100) when max=100")
	}
	if si.CanSkip(1, 0, OpGreaterEqual, common.NewInt64(50)) {
		t.Error("should not skip OpGreaterEqual(50) when max=100")
	}
}

// TestCanSkipHasValuesFalse 测试 CanSkip 在 HasValues=false 时的行为。
func TestCanSkipHasValuesFalse(t *testing.T) {
	si := NewSparseIndex()
	si.RegisterColumnStat(1, 0, nil, nil, 5, common.TypeInt64)
	if si.CanSkip(1, 0, OpEqual, common.NewInt64(10)) {
		t.Error("should not skip when HasValues=false")
	}
	if si.CanSkip(1, 0, OpLess, common.NewInt64(10)) {
		t.Error("should not skip when HasValues=false")
	}
	if si.CanSkip(1, 0, OpGreater, common.NewInt64(10)) {
		t.Error("should not skip when HasValues=false")
	}
}

// TestCanSkipUnknownOp 测试 CanSkip 对未知操作符的行为。
func TestCanSkipUnknownOp(t *testing.T) {
	si := NewSparseIndex()
	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)
	if si.CanSkip(1, 0, PredicateOp(100), common.NewInt64(50)) {
		t.Error("should not skip for unknown predicate op")
	}
}

// TestCanSkipEqualEdgeCases 测试 CanSkip 对 OpEqual 的边界情况。
func TestCanSkipEqualEdgeCases(t *testing.T) {
	si := NewSparseIndex()
	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)
	if si.CanSkip(1, 0, OpEqual, common.NewInt64(10)) {
		t.Error("should not skip OpEqual(10) when min=10")
	}
	if si.CanSkip(1, 0, OpEqual, common.NewInt64(100)) {
		t.Error("should not skip OpEqual(100) when max=100")
	}
	if si.CanSkip(1, 0, OpEqual, common.NewInt64(50)) {
		t.Error("should not skip OpEqual(50) when value in range")
	}
	if !si.CanSkip(1, 0, OpEqual, common.NewInt64(9)) {
		t.Error("should skip OpEqual(9) when min=10")
	}
	if !si.CanSkip(1, 0, OpEqual, common.NewInt64(101)) {
		t.Error("should skip OpEqual(101) when max=100")
	}
}
