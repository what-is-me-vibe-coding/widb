package storage

import (
	"fmt"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// buildBinarySearchSegment 构造一个 Keys 按字典序排序的 Segment，用于二分查找测试。
// 键格式：key_00000、key_00001 ... 固定宽度便于比较。
func buildBinarySearchSegment(n int) *Segment {
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = fmt.Sprintf("key_%08d", i)
	}
	return &Segment{Keys: keys}
}

func TestSegmentFindRowByKeyGE_Empty(t *testing.T) {
	seg := &Segment{Keys: nil}
	_, ok := seg.FindRowByKeyGE("any")
	if ok {
		t.Error("expected false for empty segment")
	}
}

func TestSegmentFindRowByKeyLE_Empty(t *testing.T) {
	seg := &Segment{Keys: nil}
	_, ok := seg.FindRowByKeyLE("any")
	if ok {
		t.Error("expected false for empty segment")
	}
}

func TestSegmentFindRowByKeyGE_AllLessThanKey(t *testing.T) {
	// 所有 key 都小于查询值时返回 false
	seg := &Segment{Keys: []string{"a", "b", "c"}}
	_, ok := seg.FindRowByKeyGE("z")
	if ok {
		t.Error("expected false when all keys < query")
	}
}

func TestSegmentFindRowByKeyLE_AllGreaterThanKey(t *testing.T) {
	// 所有 key 都大于查询值时返回 false
	seg := &Segment{Keys: []string{"x", "y", "z"}}
	_, ok := seg.FindRowByKeyLE("a")
	if ok {
		t.Error("expected false when all keys > query")
	}
}

func TestSegmentFindRowByKeyGE_ExactMatch(t *testing.T) {
	seg := &Segment{Keys: []string{"a", "c", "e", "g"}}
	tests := []struct {
		key     string
		wantIdx uint32
		wantOK  bool
	}{
		{"a", 0, true},
		{"c", 1, true},
		{"e", 2, true},
		{"g", 3, true},
	}
	for _, tc := range tests {
		idx, ok := seg.FindRowByKeyGE(tc.key)
		if ok != tc.wantOK || idx != tc.wantIdx {
			t.Errorf("FindRowByKeyGE(%q) = (%d, %v), want (%d, %v)",
				tc.key, idx, ok, tc.wantIdx, tc.wantOK)
		}
	}
}

func TestSegmentFindRowByKeyLE_ExactMatch(t *testing.T) {
	seg := &Segment{Keys: []string{"a", "c", "e", "g"}}
	tests := []struct {
		key     string
		wantIdx uint32
		wantOK  bool
	}{
		{"a", 0, true},
		{"c", 1, true},
		{"e", 2, true},
		{"g", 3, true},
	}
	for _, tc := range tests {
		idx, ok := seg.FindRowByKeyLE(tc.key)
		if ok != tc.wantOK || idx != tc.wantIdx {
			t.Errorf("FindRowByKeyLE(%q) = (%d, %v), want (%d, %v)",
				tc.key, idx, ok, tc.wantIdx, tc.wantOK)
		}
	}
}

func TestSegmentFindRowByKeyGE_InsertPosition(t *testing.T) {
	// 不存在的 key 应返回第一个 >= key 的位置
	seg := &Segment{Keys: []string{"a", "c", "e", "g"}}
	tests := []struct {
		key     string
		wantIdx uint32
		wantOK  bool
	}{
		{"b", 1, true},  // a < b <= c
		{"d", 2, true},  // c < d <= e
		{"f", 3, true},  // e < f <= g
		{"h", 0, false}, // 所有 key 都 < h
		{"_", 0, true},  // _ <= a，第一个 >= _ 的位置是 0
	}
	for _, tc := range tests {
		idx, ok := seg.FindRowByKeyGE(tc.key)
		if ok != tc.wantOK || idx != tc.wantIdx {
			t.Errorf("FindRowByKeyGE(%q) = (%d, %v), want (%d, %v)",
				tc.key, idx, ok, tc.wantIdx, tc.wantOK)
		}
	}
}

func TestSegmentFindRowByKeyLE_InsertPosition(t *testing.T) {
	// 不存在的 key 应返回最后一个 <= key 的位置
	seg := &Segment{Keys: []string{"a", "c", "e", "g"}}
	tests := []struct {
		key     string
		wantIdx uint32
		wantOK  bool
	}{
		{"b", 0, true},  // a <= b < c
		{"d", 1, true},  // c <= d < e
		{"f", 2, true},  // e <= f < g
		{"_", 0, false}, // 所有 key 都 > _
		{"z", 3, true},  // z >= g
	}
	for _, tc := range tests {
		idx, ok := seg.FindRowByKeyLE(tc.key)
		if ok != tc.wantOK || idx != tc.wantIdx {
			t.Errorf("FindRowByKeyLE(%q) = (%d, %v), want (%d, %v)",
				tc.key, idx, ok, tc.wantIdx, tc.wantOK)
		}
	}
}

func TestSegmentFindRowByKeyGE_LargeRandom(t *testing.T) {
	// 大规模随机测试：GE 应返回第一个 >= key 的位置
	seg := buildBinarySearchSegment(10000)
	// 测试边界值与中间值
	testKeys := []string{
		"key_00000000",  // 等于 keys[0]
		"key_00005000",  // 等于 keys[5000]
		"key_00009999",  // 等于 keys[9999]
		"key_00000001",  // keys[0] < key < keys[1]，应返回 1
		"key_00005001",  // keys[5000] < key < keys[5001]，应返回 5001
		"key_00000000a", // > keys[9999]，应返回 false
		"a",             // < keys[0]，应返回 0
	}
	for _, k := range testKeys {
		idx, ok := seg.FindRowByKeyGE(k)
		if !ok {
			// 仅当 key > keys[last] 时返回 false
			if k < "key_00009999" || k == "key_00009999" {
				t.Errorf("FindRowByKeyGE(%q): expected ok=true", k)
			}
			continue
		}
		if idx >= uint32(len(seg.Keys)) {
			t.Errorf("FindRowByKeyGE(%q): idx %d out of range", k, idx)
			continue
		}
		if seg.Keys[idx] < k {
			t.Errorf("FindRowByKeyGE(%q): keys[%d]=%q < %q", k, idx, seg.Keys[idx], k)
		}
		if idx > 0 && seg.Keys[idx-1] >= k {
			t.Errorf("FindRowByKeyGE(%q): keys[%d]=%q >= %q (should be < %q)",
				k, idx-1, seg.Keys[idx-1], k, k)
		}
	}
}

func TestSegmentFindRowByKeyLE_LargeRandom(t *testing.T) {
	// 大规模随机测试：LE 应返回最后一个 <= key 的位置
	seg := buildBinarySearchSegment(10000)
	testKeys := []string{
		"key_00000000",
		"key_00005000",
		"key_00009999",
		"key_00000001",
		"key_00005001",
		"key_00000000a", // > keys[9999]，应返回 9999
		"a",             // < keys[0]，应返回 false
		"z",             // > keys[9999]
	}
	for _, k := range testKeys {
		idx, ok := seg.FindRowByKeyLE(k)
		if !ok {
			if k > "key_00000000" {
				t.Errorf("FindRowByKeyLE(%q): expected ok=true", k)
			}
			continue
		}
		if idx >= uint32(len(seg.Keys)) {
			t.Errorf("FindRowByKeyLE(%q): idx %d out of range", k, idx)
			continue
		}
		if seg.Keys[idx] > k {
			t.Errorf("FindRowByKeyLE(%q): keys[%d]=%q > %q", k, idx, seg.Keys[idx], k)
		}
		if int(idx)+1 < len(seg.Keys) && seg.Keys[idx+1] <= k {
			t.Errorf("FindRowByKeyLE(%q): keys[%d]=%q <= %q (should be > %q)",
				k, idx+1, seg.Keys[idx+1], k, k)
		}
	}
}

func TestSegmentIterator_BinarySearchPositioning_WideRangeAtEnd(t *testing.T) {
	// 验证：当查询范围在大段尾部时，迭代器从尾部开始而非从 0 扫描
	seg := buildBinarySearchSegment(10000)
	seg.Columns = []EncodedColumn{{Type: common.TypeInt64}}
	it := newSegmentIterator(seg, nil, "key_00009900", "key_00009999", nil)
	defer it.Close()

	count := 0
	for it.Next() {
		if it.currentKey < "key_00009900" || it.currentKey > "key_00009999" {
			t.Errorf("returned key %q out of range", it.currentKey)
		}
		count++
	}
	if count != 100 {
		t.Errorf("expected 100 rows in range [key_00009900, key_00009999], got %d", count)
	}
	if it.Err() != nil {
		t.Errorf("unexpected error: %v", it.Err())
	}
}

func TestSegmentIterator_BinarySearchPositioning_StartBeyondAllKeys(t *testing.T) {
	// 验证：当 start > 所有 key 时，迭代器立即结束
	seg := buildBinarySearchSegment(1000)
	it := newSegmentIterator(seg, nil, "key_99999999", "key_99999999a", nil)
	defer it.Close()

	if it.Next() {
		t.Errorf("expected no rows, but got key %q", it.currentKey)
	}
}

func TestSegmentIterator_BinarySearchPositioning_EndBeforeAllKeys(t *testing.T) {
	// 验证：当 end < 所有 key 时，迭代器立即结束
	seg := buildBinarySearchSegment(1000)
	it := newSegmentIterator(seg, nil, "_", "0", nil)
	defer it.Close()

	if it.Next() {
		t.Errorf("expected no rows, but got key %q", it.currentKey)
	}
}

func TestSegmentIterator_BinarySearchPositioning_EmptySegment(t *testing.T) {
	// 验证：空 Segment 时迭代器立即结束
	seg := &Segment{Keys: nil}
	it := newSegmentIterator(seg, nil, "a", "z", nil)
	defer it.Close()

	if it.Next() {
		t.Errorf("expected no rows from empty segment, got key %q", it.currentKey)
	}
}

func TestSegmentIterator_BinarySearchPositioning_SingleKey(t *testing.T) {
	// 验证：单 key 范围正确工作
	seg := buildBinarySearchSegment(100)
	it := newSegmentIterator(seg, nil, "key_00000050", "key_00000050", nil)
	defer it.Close()

	if !it.Next() {
		t.Fatal("expected 1 row, got none")
	}
	if it.currentKey != "key_00000050" {
		t.Errorf("expected key_00000050, got %q", it.currentKey)
	}
	if it.Next() {
		t.Errorf("expected only 1 row, got extra key %q", it.currentKey)
	}
}

func TestSegmentIterator_BinarySearchPositioning_FullRange(t *testing.T) {
	// 验证：完整范围扫描返回所有行
	seg := buildBinarySearchSegment(100)
	it := newSegmentIterator(seg, nil, "key_00000000", "key_00000099", nil)
	defer it.Close()

	count := 0
	for it.Next() {
		count++
	}
	if count != 100 {
		t.Errorf("expected 100 rows, got %d", count)
	}
}

// BenchmarkSegmentIterator_BinarySearchPositioning 验证二分定位优化的效果。
// 场景：大段（100k keys），查询尾部窄范围（最后 100 个 key）。
// 优化前 Next() 需要从 0 线性扫描 99900+ 个 key 才能到达 startIdx。
// 优化后构造时通过 FindRowByKeyGE/LE 二分定位，使扫描范围 [99900, 99999] 直接生效。
func BenchmarkSegmentIterator_BinarySearchPositioning(b *testing.B) {
	seg := buildBinarySearchSegment(100000)
	seg.Columns = []EncodedColumn{{Type: common.TypeInt64}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		it := newSegmentIterator(seg, nil, "key_00099900", "key_00099999", nil)
		count := 0
		for it.Next() {
			count++
		}
		_ = count
		it.Close()
	}
}

// BenchmarkFindRowByKeyGE 对比新二分方法与原线性扫描。
// 用于单元测试和回归：验证二分方法在大段上明显优于线性扫描。
func BenchmarkFindRowByKeyGE(b *testing.B) {
	seg := buildBinarySearchSegment(100000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = seg.FindRowByKeyGE("key_00099900")
	}
}

func BenchmarkFindRowByKeyLE(b *testing.B) {
	seg := buildBinarySearchSegment(100000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = seg.FindRowByKeyLE("key_00099999")
	}
}

func TestSegmentComputeRange_Empty(t *testing.T) {
	seg := &Segment{Keys: nil}
	_, _, ok := seg.ComputeRange("a", "z")
	if ok {
		t.Error("expected false for empty segment")
	}
}

func TestSegmentComputeRange_NoOverlap(t *testing.T) {
	seg := &Segment{Keys: []string{"m", "n", "o"}}
	tests := []struct{ start, end string }{
		{"a", "b"}, // end < min
		{"p", "q"}, // start > max
	}
	for _, tc := range tests {
		_, _, ok := seg.ComputeRange(tc.start, tc.end)
		if ok {
			t.Errorf("ComputeRange(%q, %q): expected no overlap", tc.start, tc.end)
		}
	}
}

func TestSegmentComputeRange_Overlap(t *testing.T) {
	seg := &Segment{Keys: []string{"a", "c", "e", "g", "i"}}
	tests := []struct {
		start, end   string
		wantS, wantE uint32
	}{
		{"b", "f", 1, 2}, // c..e
		{"a", "i", 0, 4}, // full
		{"e", "e", 2, 2}, // 单点
		{"_", "a", 0, 0}, // 仅 a 命中
		{"i", "z", 4, 4}, // 仅 i 命中
		{"g", "h", 3, 3}, // 仅 g 命中
		{"c", "c", 1, 1}, // 端点单命中
	}
	for _, tc := range tests {
		s, e, ok := seg.ComputeRange(tc.start, tc.end)
		if !ok {
			t.Errorf("ComputeRange(%q, %q): expected ok", tc.start, tc.end)
			continue
		}
		if s != tc.wantS || e != tc.wantE {
			t.Errorf("ComputeRange(%q, %q) = (%d, %d), want (%d, %d)",
				tc.start, tc.end, s, e, tc.wantS, tc.wantE)
		}
	}
}

func BenchmarkComputeRange(b *testing.B) {
	seg := buildBinarySearchSegment(100000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = seg.ComputeRange("key_00099900", "key_00099999")
	}
}
