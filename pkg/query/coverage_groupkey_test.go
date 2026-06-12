package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestBuildGroupKeySeparatorNoCollision 验证 buildGroupKey 使用 '\x00' 分隔符时，
// 列值中包含 '|' 字符不会导致分组键碰撞。
// 修复前使用 '|' 作为分隔符，"a|b"|"c" 和 "a"|"b|c" 会产生相同的键。
func TestBuildGroupKeySeparatorNoCollision(t *testing.T) {
	t.Parallel()

	colIdxMap := map[string]int{testStrCol1: 0, testStrCol2: 1}

	// 情况 1: col1="a|b", col2="c"
	row1 := map[string]common.Value{
		testStrCol1: common.NewString("a|b"),
		testStrCol2: common.NewString("c"),
	}
	groupBy1 := []Expression{
		&ResolvedColumnExpr{Name: testStrCol1, Idx: 0, typ: common.TypeString},
		&ResolvedColumnExpr{Name: testStrCol2, Idx: 1, typ: common.TypeString},
	}
	key1 := buildGroupKey(groupBy1, row1, colIdxMap)

	// 情况 2: col1="a", col2="b|c"
	row2 := map[string]common.Value{
		testStrCol1: common.NewString("a"),
		testStrCol2: common.NewString("b|c"),
	}
	key2 := buildGroupKey(groupBy1, row2, colIdxMap)

	if key1 == key2 {
		t.Errorf("分组键碰撞: key1=%q, key2=%q，不同列值应产生不同分组键", key1, key2)
	}
}

// TestBuildGroupKeySeparatorWithNullChar 验证使用 '\x00' 分隔符时，
// 即使列值包含 '\x00' 也不会产生碰撞（因为 '\x00' 在正常文本中极少出现）。
func TestBuildGroupKeySeparatorWithNullChar(t *testing.T) {
	t.Parallel()

	colIdxMap := map[string]int{testStrCol1: 0, testStrCol2: 1}

	groupBy := []Expression{
		&ResolvedColumnExpr{Name: testStrCol1, Idx: 0, typ: common.TypeString},
		&ResolvedColumnExpr{Name: testStrCol2, Idx: 1, typ: common.TypeString},
	}

	// 情况 1: col1="a", col2="b"
	row1 := map[string]common.Value{
		testStrCol1: common.NewString("a"),
		testStrCol2: common.NewString("b"),
	}
	key1 := buildGroupKey(groupBy, row1, colIdxMap)

	// 情况 2: col1="a\x00b", col2="" — 不同的值组合
	row2 := map[string]common.Value{
		testStrCol1: common.NewString("a\x00b"),
		testStrCol2: common.NewString(""),
	}
	key2 := buildGroupKey(groupBy, row2, colIdxMap)

	if key1 == key2 {
		t.Errorf("分组键碰撞: key1=%q, key2=%q", key1, key2)
	}
}

// TestBuildGroupKeyEmptyGroupBy 验证空 GROUP BY 返回空字符串。
func TestBuildGroupKeyEmptyGroupBy(t *testing.T) {
	t.Parallel()

	key := buildGroupKey(nil, nil, nil)
	if key != "" {
		t.Errorf("expected empty key for empty GROUP BY, got %q", key)
	}
}
