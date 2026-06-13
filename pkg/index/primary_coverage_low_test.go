package index

import (
	"testing"
)

// TestLookupSecondLoop_V7 测试 Lookup 中第二个循环路径（从 idx 向后扫描）。
// 当多个 segment 拥有相同 MinKey 时，sort.Search 返回第一个满足 MinKey > key 的位置，
// 此时 segments[idx].MinKey 可能等于 key，触发第二个循环。
func TestLookupSecondLoop_V7(t *testing.T) {
	pi := NewPrimaryIndex()
	// 注册多个 MinKey 相同的 L0 segment
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "b", MaxKey: "d", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "b", MaxKey: "f", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 3, MinKey: "b", MaxKey: "h", Level: 0})

	ids := pi.Lookup("b")
	if len(ids) == 0 {
		t.Fatal("期望找到包含 key 'b' 的 segment，但结果为空")
	}
	// 三个 segment 的 MinKey 都是 "b"，都应被找到
	if len(ids) != 3 {
		t.Errorf("期望 3 个 segment，实际得到 %d 个: %v", len(ids), ids)
	}
}

// TestLookupExactMinKeyMatch_V7 测试 Lookup 精确匹配 MinKey 的场景。
func TestLookupExactMinKeyMatch_V7(t *testing.T) {
	pi := NewPrimaryIndex()
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "c", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "d", MaxKey: "f", Level: 0})

	tests := []struct {
		key     string
		wantLen int
		wantID  uint64
	}{
		{"a", 1, 1}, // 精确匹配第一个 segment 的 MinKey
		{"d", 1, 2}, // 精确匹配第二个 segment 的 MinKey
	}
	for _, tt := range tests {
		ids := pi.Lookup(tt.key)
		if len(ids) != tt.wantLen {
			t.Errorf("Lookup(%q): 期望 %d 个结果，实际 %d 个", tt.key, tt.wantLen, len(ids))
		}
		if len(ids) > 0 && ids[0] != tt.wantID {
			t.Errorf("Lookup(%q): 期望 ID %d，实际 %d", tt.key, tt.wantID, ids[0])
		}
	}
}

// TestLookupEmptyIndex_V7 测试空索引的 Lookup 返回 nil。
func TestLookupEmptyIndex_V7(t *testing.T) {
	pi := NewPrimaryIndex()
	ids := pi.Lookup("any")
	if ids != nil {
		t.Errorf("期望 nil，实际 %v", ids)
	}
}

// TestLookupSameMinKeyDifferentMaxKey_V7 测试相同 MinKey 但不同 MaxKey 的 segment。
func TestLookupSameMinKeyDifferentMaxKey_V7(t *testing.T) {
	pi := NewPrimaryIndex()
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "c", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "a", MaxKey: "z", Level: 0})

	// 查找 "e"：seg1 MaxKey="c" 不包含，seg2 MaxKey="z" 包含
	ids := pi.Lookup("e")
	if len(ids) != 1 {
		t.Fatalf("期望 1 个 segment，实际 %d 个", len(ids))
	}
	if ids[0] != 2 {
		t.Errorf("期望 ID 2，实际 %d", ids[0])
	}
}

// TestLookupKeyBetweenSegments_V7 测试 key 落在两个 segment 之间的间隙。
func TestLookupKeyBetweenSegments_V7(t *testing.T) {
	pi := NewPrimaryIndex()
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "c", Level: 1})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "e", MaxKey: "g", Level: 1})

	ids := pi.Lookup("d")
	if len(ids) != 0 {
		t.Errorf("期望 0 个结果（key 在间隙中），实际 %d 个: %v", len(ids), ids)
	}
}

// TestLookupMultipleSameMinKeyForwardScan_V7 测试第二个循环的向前扫描路径。
// 当 sort.Search 返回 idx 使得 segments[idx].MinKey == key 时，第二个循环会执行。
func TestLookupMultipleSameMinKeyForwardScan_V7(t *testing.T) {
	pi := NewPrimaryIndex()
	// 三个 segment MinKey 都是 "c"，但 MaxKey 不同
	_ = pi.RegisterSegment(SegmentMeta{ID: 10, MinKey: "c", MaxKey: "c", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 20, MinKey: "c", MaxKey: "e", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 30, MinKey: "c", MaxKey: "z", Level: 0})

	ids := pi.Lookup("c")
	if len(ids) != 3 {
		t.Errorf("期望 3 个 segment 包含 key 'c'，实际 %d 个: %v", len(ids), ids)
	}
}

// TestLookupKeyBeforeAllMinKeys_V7 测试 key 小于所有 segment 的 MinKey。
func TestLookupKeyBeforeAllMinKeys_V7(t *testing.T) {
	pi := NewPrimaryIndex()
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "m", MaxKey: "z", Level: 0})

	ids := pi.Lookup("a")
	if len(ids) != 0 {
		t.Errorf("期望 0 个结果，实际 %d 个: %v", len(ids), ids)
	}
}

// TestLookupKeyAfterAllMaxKeys_V7 测试 key 大于所有 segment 的 MaxKey。
func TestLookupKeyAfterAllMaxKeys_V7(t *testing.T) {
	pi := NewPrimaryIndex()
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "m", Level: 0})

	ids := pi.Lookup("z")
	if len(ids) != 0 {
		t.Errorf("期望 0 个结果，实际 %d 个: %v", len(ids), ids)
	}
}
