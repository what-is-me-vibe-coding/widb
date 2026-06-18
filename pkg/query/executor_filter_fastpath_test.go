package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// buildSingleColChunk 构建一个仅含单列的 Chunk，用于精确控制列类型与 NULL 行。
// values 长度决定行数；其中 Valid==false 的值标记为 NULL。
func buildSingleColChunk(typ common.DataType, values []common.Value) (*storage.Chunk, []ColumnDef) {
	rowCount := uint32(len(values))
	col := storage.NewColumnVector(0, typ, rowCount)
	for i, v := range values {
		if !v.Valid {
			col.SetNull(uint32(i))
			continue
		}
		_ = col.SetValue(uint32(i), v)
	}
	col.SetLen(rowCount)
	chunk := storage.NewChunk(rowCount)
	_ = chunk.AddColumn(col)
	schema := []ColumnDef{{Name: "c0", Type: typ, Nullable: true}}
	return chunk, schema
}

// runFastFilter 对单列 Chunk 执行 column op literal 过滤，返回命中行数。
func runFastFilter(t *testing.T, chunk *storage.Chunk, schema []ColumnDef, op BinaryOp, lit common.Value) int {
	t.Helper()
	cond := &BinaryExpr{
		Op:    op,
		Left:  &ResolvedColumnExpr{Name: "c0", Idx: 0, typ: schema[0].Type},
		Right: &LiteralExpr{Value: lit},
	}
	out, err := filterChunk(chunk, cond, schema, buildColIdxMapFromSchema(schema))
	if err != nil {
		t.Fatalf("filterChunk %s: %v", op, err)
	}
	return int(out.RowCount())
}

// TestFastFilterInt64AllOps 覆盖整数族快速路径的全部比较运算符。
// 数据：[1, 2, NULL, 4, 5]，字面量 3，验证 NULL 被跳过且各 op 语义正确。
func TestFastFilterInt64AllOps(t *testing.T) {
	values := []common.Value{
		common.NewInt64(1), common.NewInt64(2), common.NewNull(),
		common.NewInt64(4), common.NewInt64(5),
	}
	chunk, schema := buildSingleColChunk(common.TypeInt64, values)
	lit := common.NewInt64(3)

	tests := []struct {
		op   BinaryOp
		want int
		desc string
	}{
		{OpEq, 0, "no row equals 3"},
		{OpNe, 4, "4 non-null rows != 3"},
		{OpLt, 2, "1,2 < 3"},
		{OpLe, 2, "1,2 <= 3"},
		{OpGt, 2, "4,5 > 3"},
		{OpGe, 2, "4,5 >= 3"},
	}
	for _, tt := range tests {
		if got := runFastFilter(t, chunk, schema, tt.op, lit); got != tt.want {
			t.Errorf("int64 %s: %s, got %d want %d", tt.op, tt.desc, got, tt.want)
		}
	}
}

// TestFastFilterFloat64AllOps 覆盖 FLOAT64 快速路径的全部比较运算符。
func TestFastFilterFloat64AllOps(t *testing.T) {
	values := []common.Value{
		common.NewFloat64(1.5), common.NewFloat64(2.5), common.NewNull(),
		common.NewFloat64(4.5), common.NewFloat64(5.5),
	}
	chunk, schema := buildSingleColChunk(common.TypeFloat64, values)
	lit := common.NewFloat64(3.0)

	tests := []struct {
		op   BinaryOp
		want int
	}{
		{OpEq, 0},
		{OpNe, 4},
		{OpLt, 2},
		{OpLe, 2},
		{OpGt, 2},
		{OpGe, 2},
	}
	for _, tt := range tests {
		if got := runFastFilter(t, chunk, schema, tt.op, lit); got != tt.want {
			t.Errorf("float64 %s: got %d want %d", tt.op, got, tt.want)
		}
	}
}

// TestFastFilterStringAllOps 覆盖 STRING 快速路径的全部比较运算符。
func TestFastFilterStringAllOps(t *testing.T) {
	values := []common.Value{
		common.NewString("apple"), common.NewString("banana"), common.NewNull(),
		common.NewString("cherry"), common.NewString("date"),
	}
	chunk, schema := buildSingleColChunk(common.TypeString, values)
	lit := common.NewString("cherry")

	tests := []struct {
		op   BinaryOp
		want int
	}{
		{OpEq, 1},
		{OpNe, 3},
		{OpLt, 2},
		{OpLe, 3},
		{OpGt, 1},
		{OpGe, 2},
	}
	for _, tt := range tests {
		if got := runFastFilter(t, chunk, schema, tt.op, lit); got != tt.want {
			t.Errorf("string %s: got %d want %d", tt.op, got, tt.want)
		}
	}
}

// TestFastFilterIntFamilyTypes 验证整数族各子类型（INT8/16/32/UINT64/DATE）
// 均命中 int64 快速路径，且跨类型按 Int64 字段比较。
func TestFastFilterIntFamilyTypes(t *testing.T) {
	intFamilyTypes := []common.DataType{
		common.TypeInt8, common.TypeInt16, common.TypeInt32, common.TypeUint64, common.TypeDate,
	}
	for _, typ := range intFamilyTypes {
		values := []common.Value{
			common.NewIntFamilyValue(typ, 10), common.NewIntFamilyValue(typ, 20),
			common.NewNull(), common.NewIntFamilyValue(typ, 30),
		}
		chunk, schema := buildSingleColChunk(typ, values)
		// 字面量使用 INT64 类型，验证跨整数族类型比较
		lit := common.NewInt64(20)
		if got := runFastFilter(t, chunk, schema, OpGe, lit); got != 2 {
			t.Errorf("int-family %s OpGe 20: got %d want 2", typ, got)
		}
		if got := runFastFilter(t, chunk, schema, OpEq, lit); got != 1 {
			t.Errorf("int-family %s OpEq 20: got %d want 1", typ, got)
		}
	}
}

// TestFastFilterTypeMismatchFallback 验证列与字面量类型不同构时
// 回退到通用逐行路径仍能得到正确结果。
func TestFastFilterTypeMismatchFallback(t *testing.T) {
	// FLOAT64 列与 INT64 字面量：类型不同构，回退到通用路径按 float64 跨类型比较
	values := []common.Value{
		common.NewFloat64(1.0), common.NewFloat64(2.0), common.NewFloat64(3.0),
	}
	chunk, schema := buildSingleColChunk(common.TypeFloat64, values)
	lit := common.NewInt64(2)
	if got := runFastFilter(t, chunk, schema, OpEq, lit); got != 1 {
		t.Errorf("float64 col vs int64 lit OpEq: got %d want 1 (cross-type numeric)", got)
	}

	// BOOL 列与 BOOL 字面量：不在特化路径中，走通用路径
	boolValues := []common.Value{
		common.NewBool(true), common.NewBool(false), common.NewBool(true),
	}
	boolChunk, boolSchema := buildSingleColChunk(common.TypeBool, boolValues)
	boolLit := common.NewBool(true)
	if got := runFastFilter(t, boolChunk, boolSchema, OpEq, boolLit); got != 2 {
		t.Errorf("bool OpEq true: got %d want 2 (generic fallback)", got)
	}
}

// TestFastFilterAllNulls 验证全 NULL 列过滤返回 0 行（快速路径 NULL 跳过）。
func TestFastFilterAllNulls(t *testing.T) {
	for _, typ := range []common.DataType{common.TypeInt64, common.TypeFloat64, common.TypeString} {
		values := []common.Value{common.NewNull(), common.NewNull(), common.NewNull()}
		chunk, schema := buildSingleColChunk(typ, values)
		lit := common.NewIntFamilyValue(typ, 0)
		switch typ {
		case common.TypeFloat64:
			lit = common.NewFloat64(0)
		case common.TypeString:
			lit = common.NewString("x")
		}
		if got := runFastFilter(t, chunk, schema, OpEq, lit); got != 0 {
			t.Errorf("all-null %s OpEq: got %d want 0", typ, got)
		}
	}
}

// TestCompareOrdered 直接验证 compareOrdered 在各类型上的语义，
// 确保与 compareValues 同类型非 NULL 场景一致。
func TestCompareOrdered(t *testing.T) {
	intCases := []struct {
		op    BinaryOp
		left  int64
		right int64
		want  bool
	}{
		{OpEq, 5, 5, true},
		{OpEq, 5, 6, false},
		{OpNe, 5, 6, true},
		{OpLt, 5, 6, true},
		{OpLe, 5, 5, true},
		{OpGt, 6, 5, true},
		{OpGe, 5, 5, true},
	}
	for _, tt := range intCases {
		if got := compareOrdered(tt.op, tt.left, tt.right); got != tt.want {
			t.Errorf("compareOrdered[int64] %s %d,%d: got %v want %v", tt.op, tt.left, tt.right, got, tt.want)
		}
	}
	if !compareOrdered(OpLt, 1.5, 2.5) {
		t.Error("compareOrdered[float64] Lt 1.5,2.5: got false want true")
	}
	if !compareOrdered(OpEq, "a", "a") {
		t.Error("compareOrdered[string] Eq a,a: got false want true")
	}
	if compareOrdered(OpEq, "a", "b") {
		t.Error("compareOrdered[string] Eq a,b: got true want false")
	}
	// 未支持的运算符返回 false
	if compareOrdered(OpAnd, 1, 1) {
		t.Error("compareOrdered unsupported op: got true want false")
	}
}
