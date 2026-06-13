package index

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ==================== Lookup 覆盖率测试 ====================

// TestLookupEmptyIndex 测试在空索引上执行点查。
// 空索引应返回 nil。
func TestLookupEmptyIndex(t *testing.T) {
	pi := NewPrimaryIndex()

	result := pi.Lookup("any-key")
	if result != nil {
		t.Errorf("expected nil for lookup on empty index, got %v", result)
	}
}

// TestLookupKeyBetweenSegments 测试查找落在两个 Segment 间隙中的 key。
// key 不在任何 Segment 范围内，应返回空。
func TestLookupKeyBetweenSegments(t *testing.T) {
	pi := NewPrimaryIndex()

	// 注册两个不连续的 Segment：[a, c] 和 [f, h]
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "c", Level: 1})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "f", MaxKey: "h", Level: 1})

	// 查找 "e"，落在 [a,c] 和 [f,h] 之间
	result := pi.Lookup("e")
	if len(result) != 0 {
		t.Errorf("expected no segments for key between ranges, got %v", result)
	}
}

// TestLookupKeyAtExactMinKeyBoundary 测试查找 key 恰好等于某个 Segment 的 MinKey。
// 验证边界条件正确处理。
func TestLookupKeyAtExactMinKeyBoundary(t *testing.T) {
	pi := NewPrimaryIndex()

	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "c", MaxKey: "f", Level: 1})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "g", MaxKey: "m", Level: 1})

	// 查找 "c"，恰好是 Segment 1 的 MinKey
	result := pi.Lookup("c")
	if len(result) != 1 {
		t.Errorf("expected 1 segment for key=MinKey boundary, got %d", len(result))
	}
	if len(result) > 0 && result[0] != 1 {
		t.Errorf("expected segment ID 1, got %d", result[0])
	}

	// 查找 "g"，恰好是 Segment 2 的 MinKey
	result = pi.Lookup("g")
	if len(result) != 1 {
		t.Errorf("expected 1 segment for key=MinKey boundary, got %d", len(result))
	}
	if len(result) > 0 && result[0] != 2 {
		t.Errorf("expected segment ID 2, got %d", result[0])
	}
}

// TestLookupOverlappingL0SameKey 测试多个 L0 Segment 包含相同 key 的场景。
// L0 层允许重叠，查找重叠区域内的 key 应返回所有匹配的 Segment。
func TestLookupOverlappingL0SameKey(t *testing.T) {
	pi := NewPrimaryIndex()

	// 注册三个 L0 重叠 Segment，都包含 key "d"
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "e", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "c", MaxKey: "g", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 3, MinKey: "b", MaxKey: "f", Level: 0})

	result := pi.Lookup("d")
	if len(result) != 3 {
		t.Errorf("expected 3 overlapping L0 segments for key 'd', got %d", len(result))
	}

	// 验证所有三个 Segment 都在结果中
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

	// 查找 "f"，恰好是 MaxKey
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

	// 查找 "a"，在所有 Segment 之前
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

	// 查找 "z"，在所有 Segment 之后
	result := pi.Lookup("z")
	if len(result) != 0 {
		t.Errorf("expected no segments for key after all ranges, got %v", result)
	}
}

// ==================== BuildAndRegister 覆盖率测试 ====================

// TestBuildAndRegisterEmptyKeysV13 测试 BuildAndRegister 传入空 keys 时返回 nil。
// 空 keys 不应注册任何过滤器。
func TestBuildAndRegisterEmptyKeysV13(t *testing.T) {
	bi := NewBloomIndex()

	err := bi.BuildAndRegister(1, []string{}, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister with empty keys: %v", err)
	}
	if bi.Len() != 0 {
		t.Errorf("expected 0 bloom filters after empty keys, got %d", bi.Len())
	}

	// nil keys 也应返回 nil
	err = bi.BuildAndRegister(2, nil, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister with nil keys: %v", err)
	}
	if bi.Len() != 0 {
		t.Errorf("expected 0 bloom filters after nil keys, got %d", bi.Len())
	}
}

// TestBuildAndRegisterValidKeysV13 测试 BuildAndRegister 正常路径。
// 验证注册后可以通过 MayContain 查到 key。
func TestBuildAndRegisterValidKeysV13(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{"key-a", "key-b", "key-c", "key-d", "key-e"}
	err := bi.BuildAndRegister(42, keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister valid keys: %v", err)
	}

	if bi.Len() != 1 {
		t.Errorf("expected 1 bloom filter, got %d", bi.Len())
	}

	// 验证所有 key 都能被查到
	for _, k := range keys {
		if !bi.MayContain(42, []byte(k)) {
			t.Errorf("expected MayContain(%q)=true after BuildAndRegister", k)
		}
	}
}

// TestBuildAndRegisterInvalidFPRateV13 测试 BuildAndRegister 使用无效 fpRate 时使用默认值。
// fpRate <= 0 或 >= 1 时应回退到 DefaultBloomFPRate。
func TestBuildAndRegisterInvalidFPRateV13(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{"fp-key-1", "fp-key-2", "fp-key-3"}

	tests := []struct {
		name   string
		fpRate float64
	}{
		{"零值fpRate", 0},
		{"负数fpRate", -0.5},
		{"大于1的fpRate", 2.0},
		{"等于1的fpRate", 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := bi.BuildAndRegister(1, keys, tt.fpRate)
			if err != nil {
				t.Fatalf("BuildAndRegister with fpRate=%v: %v", tt.fpRate, err)
			}

			if bi.Len() != 1 {
				t.Errorf("expected 1 bloom filter, got %d", bi.Len())
			}

			// 验证 key 仍然可以被查到
			for _, k := range keys {
				if !bi.MayContain(1, []byte(k)) {
					t.Errorf("expected MayContain(%q)=true with fpRate=%v", k, tt.fpRate)
				}
			}

			// 清理以便下一个子测试
			bi.Clear()
		})
	}
}

// ==================== CanSkip 覆盖率测试 ====================

// TestCanSkipNoRegisteredStats 测试 CanSkip 在没有注册任何统计信息时的行为。
// 无统计信息时不应跳过。
func TestCanSkipNoRegisteredStats(t *testing.T) {
	si := NewSparseIndex()

	// 没有注册任何统计，CanSkip 应返回 false
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
// 不等判断无法基于 Min/Max 统计跳过。
func TestCanSkipOpNotEqual(t *testing.T) {
	si := NewSparseIndex()

	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)

	// OpNotEqual 无论值在范围内还是范围外，都应返回 false
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
// OpLess: 跳过条件为 !minVal.Less(value)，即 minVal >= value 时可跳过。
func TestCanSkipOpLessEdgeCases(t *testing.T) {
	si := NewSparseIndex()

	// 范围 [10, 100]
	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)

	// value=5: min(10) 不小于 5，可跳过（没有值 < 5）
	if !si.CanSkip(1, 0, OpLess, common.NewInt64(5)) {
		t.Error("should skip OpLess(5) when min=10")
	}

	// value=10: min(10) 不小于 10，可跳过（没有值 < 10）
	if !si.CanSkip(1, 0, OpLess, common.NewInt64(10)) {
		t.Error("should skip OpLess(10) when min=10")
	}

	// value=11: min(10) < 11，不可跳过（可能有值 < 11）
	if si.CanSkip(1, 0, OpLess, common.NewInt64(11)) {
		t.Error("should not skip OpLess(11) when min=10")
	}

	// value=100: min(10) < 100，不可跳过
	if si.CanSkip(1, 0, OpLess, common.NewInt64(100)) {
		t.Error("should not skip OpLess(100) when min=10")
	}

	// value=200: min(10) < 200，不可跳过
	if si.CanSkip(1, 0, OpLess, common.NewInt64(200)) {
		t.Error("should not skip OpLess(200) when min=10")
	}
}

// TestCanSkipOpGreaterEdgeCases 测试 CanSkip 对 OpGreater 的边界情况。
// OpGreater: 跳过条件为 !value.Less(maxVal)，即 value >= maxVal 时可跳过。
func TestCanSkipOpGreaterEdgeCases(t *testing.T) {
	si := NewSparseIndex()

	// 范围 [10, 100]
	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)

	// value=200: 200 不小于 max(100)，可跳过（没有值 > 200）
	if !si.CanSkip(1, 0, OpGreater, common.NewInt64(200)) {
		t.Error("should skip OpGreater(200) when max=100")
	}

	// value=100: 100 不小于 max(100)，可跳过（没有值 > 100）
	if !si.CanSkip(1, 0, OpGreater, common.NewInt64(100)) {
		t.Error("should skip OpGreater(100) when max=100")
	}

	// value=99: 99 < max(100)，不可跳过（可能有值 > 99）
	if si.CanSkip(1, 0, OpGreater, common.NewInt64(99)) {
		t.Error("should not skip OpGreater(99) when max=100")
	}

	// value=50: 50 < max(100)，不可跳过
	if si.CanSkip(1, 0, OpGreater, common.NewInt64(50)) {
		t.Error("should not skip OpGreater(50) when max=100")
	}

	// value=5: 5 < max(100)，不可跳过
	if si.CanSkip(1, 0, OpGreater, common.NewInt64(5)) {
		t.Error("should not skip OpGreater(5) when max=100")
	}
}

// TestCanSkipOpLessEqualEdgeCases 测试 CanSkip 对 OpLessEqual 的边界情况。
// OpLessEqual: 跳过条件为 value.Less(minVal)，即 value < min 时可跳过。
func TestCanSkipOpLessEqualEdgeCases(t *testing.T) {
	si := NewSparseIndex()

	// 范围 [10, 100]
	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)

	// value=5: 5 < min(10)，可跳过（没有值 <= 5）
	if !si.CanSkip(1, 0, OpLessEqual, common.NewInt64(5)) {
		t.Error("should skip OpLessEqual(5) when min=10")
	}

	// value=9: 9 < min(10)，可跳过
	if !si.CanSkip(1, 0, OpLessEqual, common.NewInt64(9)) {
		t.Error("should skip OpLessEqual(9) when min=10")
	}

	// value=10: 10 不小于 min(10)，不可跳过（有值 <= 10）
	if si.CanSkip(1, 0, OpLessEqual, common.NewInt64(10)) {
		t.Error("should not skip OpLessEqual(10) when min=10")
	}

	// value=50: 50 不小于 min(10)，不可跳过
	if si.CanSkip(1, 0, OpLessEqual, common.NewInt64(50)) {
		t.Error("should not skip OpLessEqual(50) when min=10")
	}
}

// TestCanSkipOpGreaterEqualEdgeCases 测试 CanSkip 对 OpGreaterEqual 的边界情况。
// OpGreaterEqual: 跳过条件为 maxVal.Less(value)，即 max < value 时可跳过。
func TestCanSkipOpGreaterEqualEdgeCases(t *testing.T) {
	si := NewSparseIndex()

	// 范围 [10, 100]
	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)

	// value=200: max(100) < 200，可跳过（没有值 >= 200）
	if !si.CanSkip(1, 0, OpGreaterEqual, common.NewInt64(200)) {
		t.Error("should skip OpGreaterEqual(200) when max=100")
	}

	// value=101: max(100) < 101，可跳过
	if !si.CanSkip(1, 0, OpGreaterEqual, common.NewInt64(101)) {
		t.Error("should skip OpGreaterEqual(101) when max=100")
	}

	// value=100: max(100) 不小于 100，不可跳过（有值 >= 100）
	if si.CanSkip(1, 0, OpGreaterEqual, common.NewInt64(100)) {
		t.Error("should not skip OpGreaterEqual(100) when max=100")
	}

	// value=50: max(100) 不小于 50，不可跳过
	if si.CanSkip(1, 0, OpGreaterEqual, common.NewInt64(50)) {
		t.Error("should not skip OpGreaterEqual(50) when max=100")
	}
}

// TestCanSkipHasValuesFalse 测试 CanSkip 在 HasValues=false 时的行为。
// 没有有效值的统计信息不应导致跳过。
func TestCanSkipHasValuesFalse(t *testing.T) {
	si := NewSparseIndex()

	// 注册空 min/max 的统计（HasValues=false）
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
// 未知操作符应返回 false（不跳过）。
func TestCanSkipUnknownOp(t *testing.T) {
	si := NewSparseIndex()

	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)

	// 使用超出枚举范围的操作符
	if si.CanSkip(1, 0, PredicateOp(100), common.NewInt64(50)) {
		t.Error("should not skip for unknown predicate op")
	}
}

// TestCanSkipEqualEdgeCases 测试 CanSkip 对 OpEqual 的边界情况。
func TestCanSkipEqualEdgeCases(t *testing.T) {
	si := NewSparseIndex()

	// 范围 [10, 100]
	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)

	// value 等于 min：不应跳过
	if si.CanSkip(1, 0, OpEqual, common.NewInt64(10)) {
		t.Error("should not skip OpEqual(10) when min=10")
	}

	// value 等于 max：不应跳过
	if si.CanSkip(1, 0, OpEqual, common.NewInt64(100)) {
		t.Error("should not skip OpEqual(100) when max=100")
	}

	// value 在范围内：不应跳过
	if si.CanSkip(1, 0, OpEqual, common.NewInt64(50)) {
		t.Error("should not skip OpEqual(50) when value in range")
	}

	// value 小于 min：应跳过
	if !si.CanSkip(1, 0, OpEqual, common.NewInt64(9)) {
		t.Error("should skip OpEqual(9) when min=10")
	}

	// value 大于 max：应跳过
	if !si.CanSkip(1, 0, OpEqual, common.NewInt64(101)) {
		t.Error("should skip OpEqual(101) when max=100")
	}
}
