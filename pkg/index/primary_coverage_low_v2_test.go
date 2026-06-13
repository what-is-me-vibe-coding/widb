package index

import (
	"testing"
)

// TestCoverageLowPrimaryV2_LookupBackwardScanOnly 测试 Lookup 中 key 仅在向后扫描范围内找到的情况。
// 当 key 小于某个 segment 的 MinKey 但在其 MaxKey 范围内时，
// 二分查找定位到该 segment 之前，只有向后扫描才能找到它。
func TestCoverageLowPrimaryV2_LookupBackwardScanOnly(t *testing.T) {
	pi := NewPrimaryIndex()

	// 注册 L0 层重叠段：
	// seg1: MinKey="c", MaxKey="z"  — key "b" 不在此段
	// seg2: MinKey="a", MaxKey="m"  — key "b" 在此段
	// 按 MinKey 排序后: seg2("a"-"m"), seg1("c"-"z")
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "c", MaxKey: "z", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "a", MaxKey: "m", Level: 0})

	// key "b" 的二分查找 idx=1（第一个 MinKey > "b" 的是 seg1）
	// 向后扫描: i=0 即 seg2("a"-"m")，"b" 在范围内 ✓
	// 向前扫描: i=0 即 seg2，无需向前（idx-1 < 0）
	ids := pi.Lookup("b")
	if len(ids) != 1 || ids[0] != 2 {
		t.Errorf("Lookup(\"b\") = %v, 期望 [2]", ids)
	}
}

// TestCoverageLowPrimaryV2_LookupBackwardScanL0Overlap 测试 L0 重叠场景中，
// key 仅通过向后扫描在更早的 segment 中找到（更早的段 MinKey 更小但 MaxKey 更大）。
func TestCoverageLowPrimaryV2_LookupBackwardScanL0Overlap(t *testing.T) {
	pi := NewPrimaryIndex()

	// L0 层段，允许重叠：
	// seg1: MinKey="a", MaxKey="z" — 范围很大
	// seg2: MinKey="e", MaxKey="h" — 范围较小
	// 排序后: seg1("a"-"z"), seg2("e"-"h")
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "z", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "e", MaxKey: "h", Level: 0})

	// key "f" 的二分查找 idx=1（第一个 MinKey > "f" 的是 seg2 的 MinKey="e" 不大于 "f"，
	// 所以 idx=2，超出范围）
	// 向后扫描: i=1(seg2), "f" 在 "e"-"h" 内 ✓; i=0(seg1), "f" 在 "a"-"z" 内 ✓
	ids := pi.Lookup("f")
	if len(ids) != 2 {
		t.Errorf("Lookup(\"f\") 期望 2 个段，得到 %d 个: %v", len(ids), ids)
	}
}

// TestCoverageLowPrimaryV2_LookupForwardScanMatch 测试 Lookup 中 key 通过向前扫描匹配到 segment。
// 当 key 等于某个 segment 的 MinKey 时，二分查找定位到该 segment 的位置，
// 向前扫描时 MinKey == key 的 segment 被匹配。
func TestCoverageLowPrimaryV2_LookupForwardScanMatch(t *testing.T) {
	pi := NewPrimaryIndex()

	// seg1: MinKey="a", MaxKey="c"
	// seg2: MinKey="d", MaxKey="f"
	// seg3: MinKey="g", MaxKey="i"
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "c", Level: 1})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "d", MaxKey: "f", Level: 1})
	_ = pi.RegisterSegment(SegmentMeta{ID: 3, MinKey: "g", MaxKey: "i", Level: 1})

	// key "d" 的二分查找 idx=1（第一个 MinKey > "d" 的是 seg3 的 MinKey="g"）
	// 等等，不对。排序后 seg1("a"), seg2("d"), seg3("g")
	// sort.Search 找第一个 MinKey > "d"，即 seg3(idx=2)
	// 向后扫描: i=1(seg2), "d" 在 "d"-"f" 内 ✓; i=0(seg1), "d" 不在 "a"-"c" 内
	// 向前扫描: i=2(seg3), MinKey="g" > "d"，break
	ids := pi.Lookup("d")
	if len(ids) != 1 || ids[0] != 2 {
		t.Errorf("Lookup(\"d\") = %v, 期望 [2]", ids)
	}
}

// TestCoverageLowPrimaryV2_LookupForwardScanExactMinKey 测试 key 等于某 segment 的 MinKey，
// 该 segment 在向前扫描范围内被找到。
func TestCoverageLowPrimaryV2_LookupForwardScanExactMinKey(t *testing.T) {
	pi := NewPrimaryIndex()

	// seg1: MinKey="a", MaxKey="c"
	// seg2: MinKey="d", MaxKey="f"
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "c", Level: 1})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "d", MaxKey: "f", Level: 1})

	// key "a" 的二分查找 idx=1（第一个 MinKey > "a" 是 seg2 的 MinKey="d"）
	// 向后扫描: i=0(seg1), "a" 在 "a"-"c" 内 ✓
	// 向前扫描: i=1(seg2), MinKey="d" > "a"，break
	ids := pi.Lookup("a")
	if len(ids) != 1 || ids[0] != 1 {
		t.Errorf("Lookup(\"a\") = %v, 期望 [1]", ids)
	}
}

// TestCoverageLowPrimaryV2_LookupEmptyResult 测试 Lookup 在所有 segment 都不包含 key 时返回空。
func TestCoverageLowPrimaryV2_LookupEmptyResult(t *testing.T) {
	pi := NewPrimaryIndex()

	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "c", Level: 1})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "d", MaxKey: "f", Level: 1})

	tests := []struct {
		name string
		key  string
	}{
		{"key在所有段之前", "0"},
		{"key在所有段之后", "z"},
		{"key在两段间隙中", "cd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids := pi.Lookup(tt.key)
			if len(ids) != 0 {
				t.Errorf("Lookup(%q) = %v, 期望空结果", tt.key, ids)
			}
		})
	}
}

// TestCoverageLowPrimaryV2_LookupEmptyIndex 测试 Lookup 在空索引上返回 nil。
func TestCoverageLowPrimaryV2_LookupEmptyIndex(t *testing.T) {
	pi := NewPrimaryIndex()
	ids := pi.Lookup("any")
	if ids != nil {
		t.Errorf("Lookup on empty index = %v, 期望 nil", ids)
	}
}

// TestCoverageLowPrimaryV2_LookupBackwardScanKeyBeforeAllSegments 测试 key 小于所有 segment 的 MinKey，
// 向后扫描范围为空，向前扫描也不匹配。
func TestCoverageLowPrimaryV2_LookupBackwardScanKeyBeforeAllSegments(t *testing.T) {
	pi := NewPrimaryIndex()

	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "m", MaxKey: "z", Level: 1})

	// key "a" 的二分查找 idx=0（第一个 MinKey > "a" 是 seg1 的 MinKey="m"）
	// 向后扫描: i=-1，跳过
	// 向前扫描: i=0(seg1), MinKey="m" > "a"，break
	ids := pi.Lookup("a")
	if len(ids) != 0 {
		t.Errorf("Lookup(\"a\") = %v, 期望空结果", ids)
	}
}

// TestCoverageLowPrimaryV2_LookupL0OverlapBackwardScan 测试 L0 层重叠段中，
// 向后扫描找到 MinKey 更小但 MaxKey 更大的段。
// 这覆盖了注释中描述的场景：更早的段可能拥有更大的 MaxKey。
func TestCoverageLowPrimaryV2_LookupL0OverlapBackwardScan(t *testing.T) {
	pi := NewPrimaryIndex()

	// L0 层段：
	// seg1: MinKey="a", MaxKey="z" — MinKey 小但 MaxKey 大
	// seg2: MinKey="c", MaxKey="e" — MinKey 大但 MaxKey 小
	// 排序后: seg1("a"-"z"), seg2("c"-"e")
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "z", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "c", MaxKey: "e", Level: 0})

	// key "f" 的二分查找 idx=2（没有 MinKey > "f"）
	// 向后扫描: i=1(seg2), "f" 不在 "c"-"e" 内; i=0(seg1), "f" 在 "a"-"z" 内 ✓
	// 向前扫描: 无（idx >= len）
	ids := pi.Lookup("f")
	if len(ids) != 1 || ids[0] != 1 {
		t.Errorf("Lookup(\"f\") = %v, 期望 [1]（仅 seg1 包含 f）", ids)
	}
}

// TestCoverageLowPrimaryV2_LookupForwardScanBreak 测试向前扫描中 MinKey > key 时立即 break。
func TestCoverageLowPrimaryV2_LookupForwardScanBreak(t *testing.T) {
	pi := NewPrimaryIndex()

	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "c", Level: 1})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "d", MaxKey: "f", Level: 1})
	_ = pi.RegisterSegment(SegmentMeta{ID: 3, MinKey: "g", MaxKey: "i", Level: 1})

	// key "e" 的二分查找 idx=2（第一个 MinKey > "e" 是 seg3 的 MinKey="g"）
	// 向后扫描: i=1(seg2), "e" 在 "d"-"f" 内 ✓; i=0(seg1), "e" 不在 "a"-"c" 内
	// 向前扫描: i=2(seg3), MinKey="g" > "e"，break
	ids := pi.Lookup("e")
	if len(ids) != 1 || ids[0] != 2 {
		t.Errorf("Lookup(\"e\") = %v, 期望 [2]", ids)
	}
}
