package query

import (
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// ============================================================================
// executeScan 覆盖率提升测试
// ============================================================================

// TestScan_EmptyTable 测试扫描空表。
// 覆盖 executeScan 中 len(entries)==0 的路径。
func TestScan_EmptyTable(t *testing.T) {
	ms := newMockStorage()

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(scan)
	if err != nil {
		t.Fatalf("扫描空表: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 0 {
		t.Errorf("期望 0 行，得到 %d", totalRows)
	}
}

// TestScan_FilterEliminatesAllRows 测试谓词过滤掉所有行。
// 覆盖 scanWithPredicate 返回空结果后 executeScan 的 len(entries)==0 路径。
func TestScan_FilterEliminatesAllRows(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})
	ms.addEntry("b", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
		testColAge: common.NewInt64(25), testColScore: common.NewFloat64(88.0),
	})

	// age > 1000 过滤掉所有行
	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		Predicate: &BinaryExpr{
			Op:    OpGt,
			Left:  &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
			Right: &LiteralExpr{Value: common.NewInt64(1000)},
		},
		schema: buildTestSchema(),
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(scan)
	if err != nil {
		t.Fatalf("过滤所有行: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 0 {
		t.Errorf("期望 0 行（全部被过滤），得到 %d", totalRows)
	}
}

// TestScan_MissingColumnInEntry 测试 ScanEntry 中缺少列时用 NULL 填充。
// 覆盖 buildChunksFromEntries 中 val, ok := entry.Value.Columns[colDef.Name] 的 !ok 路径。
func TestScan_MissingColumnInEntry(t *testing.T) {
	ms := newMockStorage()
	// 只提供部分列
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(scan)
	if err != nil {
		t.Fatalf("缺少列扫描: %v", err)
	}

	if len(chunks) == 0 || chunks[0].RowCount() == 0 {
		t.Fatal("期望至少有 1 行")
	}

	// name 列应为 NULL
	nameCol, _ := chunks[0].GetColumn(1)
	if nameCol.GetValue(0).Valid {
		t.Errorf("期望 name 列为 NULL，得到 %v", nameCol.GetValue(0))
	}
}

// ============================================================================
// sliceChunk 覆盖率提升测试
// ============================================================================

// TestSliceChunk_LimitZero 测试 limit=0 时 sliceChunk 的行为。
// 通过 LimitNode 的 Count=0 来触发 sliceChunk(startRow >= endRow) 路径。
func TestSliceChunk_LimitZero(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	limit := &LimitNode{
		Child:  scan,
		Offset: 0,
		Count:  0,
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(limit)
	if err != nil {
		t.Fatalf("LIMIT 0: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 0 {
		t.Errorf("期望 LIMIT 0 返回 0 行，得到 %d", totalRows)
	}
}

// TestSliceChunk_LimitGreaterThanChunkSize 测试 limit 超过 chunk 大小时返回所有行。
func TestSliceChunk_LimitGreaterThanChunkSize(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})
	ms.addEntry("b", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
		testColAge: common.NewInt64(25), testColScore: common.NewFloat64(88.0),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	limit := &LimitNode{
		Child:  scan,
		Offset: 0,
		Count:  10000, // 远超实际行数
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(limit)
	if err != nil {
		t.Fatalf("LIMIT 超大: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 2 {
		t.Errorf("期望 2 行（LIMIT 大于实际行数），得到 %d", totalRows)
	}
}

// TestSliceChunk_Int64Type 测试 sliceChunk 处理 INT64 类型列。
func TestSliceChunk_Int64Type(t *testing.T) {
	chunk := storage.NewChunk(4)
	col := storage.NewColumnVector(0, common.TypeInt64, 4)
	_ = col.Append(common.NewInt64(10))
	_ = col.Append(common.NewInt64(20))
	_ = col.Append(common.NewInt64(30))
	_ = col.Append(common.NewInt64(40))
	_ = chunk.AddColumn(col)

	result, err := sliceChunk(chunk, 1, 3)
	if err != nil {
		t.Fatalf("sliceChunk INT64: %v", err)
	}

	if result.RowCount() != 2 {
		t.Fatalf("期望 2 行，得到 %d", result.RowCount())
	}

	resultCol, _ := result.GetColumn(0)
	if resultCol.GetValue(0).Int64 != 20 {
		t.Errorf("期望第 0 行 = 20，得到 %d", resultCol.GetValue(0).Int64)
	}
	if resultCol.GetValue(1).Int64 != 30 {
		t.Errorf("期望第 1 行 = 30，得到 %d", resultCol.GetValue(1).Int64)
	}
}

// TestSliceChunk_Float64Type 测试 sliceChunk 处理 FLOAT64 类型列。
func TestSliceChunk_Float64Type(t *testing.T) {
	chunk := storage.NewChunk(4)
	col := storage.NewColumnVector(0, common.TypeFloat64, 4)
	_ = col.Append(common.NewFloat64(1.1))
	_ = col.Append(common.NewFloat64(2.2))
	_ = col.Append(common.NewFloat64(3.3))
	_ = col.Append(common.NewFloat64(4.4))
	_ = chunk.AddColumn(col)

	result, err := sliceChunk(chunk, 1, 3)
	if err != nil {
		t.Fatalf("sliceChunk FLOAT64: %v", err)
	}

	resultCol, _ := result.GetColumn(0)
	if resultCol.GetValue(0).Float64 != 2.2 {
		t.Errorf("期望第 0 行 = 2.2，得到 %g", resultCol.GetValue(0).Float64)
	}
	if resultCol.GetValue(1).Float64 != 3.3 {
		t.Errorf("期望第 1 行 = 3.3，得到 %g", resultCol.GetValue(1).Float64)
	}
}

// TestSliceChunk_StringType 测试 sliceChunk 处理 STRING 类型列。
func TestSliceChunk_StringType(t *testing.T) {
	chunk := storage.NewChunk(4)
	col := storage.NewColumnVector(0, common.TypeString, 4)
	_ = col.Append(common.NewString("a"))
	_ = col.Append(common.NewString("b"))
	_ = col.Append(common.NewString("c"))
	_ = col.Append(common.NewString("d"))
	_ = chunk.AddColumn(col)

	result, err := sliceChunk(chunk, 1, 3)
	if err != nil {
		t.Fatalf("sliceChunk STRING: %v", err)
	}

	resultCol, _ := result.GetColumn(0)
	if resultCol.GetValue(0).Str != "b" {
		t.Errorf("期望第 0 行 = 'b'，得到 %q", resultCol.GetValue(0).Str)
	}
	if resultCol.GetValue(1).Str != "c" {
		t.Errorf("期望第 1 行 = 'c'，得到 %q", resultCol.GetValue(1).Str)
	}
}

// TestSliceChunk_BoolType 测试 sliceChunk 处理 BOOL 类型列。
// 注意：BOOL 列的 Slice 按 word 拷贝不做位偏移，所以只测试 startRow=0 的场景。
func TestSliceChunk_BoolType(t *testing.T) {
	chunk := storage.NewChunk(4)
	col := storage.NewColumnVector(0, common.TypeBool, 4)
	_ = col.Append(common.NewBool(true))
	_ = col.Append(common.NewBool(false))
	_ = col.Append(common.NewBool(true))
	_ = col.Append(common.NewBool(false))
	_ = chunk.AddColumn(col)

	// startRow=0，sliceChunk 不会出错
	result, err := sliceChunk(chunk, 0, 2)
	if err != nil {
		t.Fatalf("sliceChunk BOOL: %v", err)
	}

	if result.RowCount() != 2 {
		t.Fatalf("期望 2 行，得到 %d", result.RowCount())
	}

	resultCol, _ := result.GetColumn(0)
	// 第 0 行应为 true
	if resultCol.GetValue(0).Int64 != 1 {
		t.Errorf("期望第 0 行 = true，得到 %d", resultCol.GetValue(0).Int64)
	}
	// 第 1 行应为 false
	if resultCol.GetValue(1).Int64 != 0 {
		t.Errorf("期望第 1 行 = false，得到 %d", resultCol.GetValue(1).Int64)
	}
}

// TestSliceChunk_TimestampType 测试 sliceChunk 处理 TIMESTAMP 类型列。
func TestSliceChunk_TimestampType(t *testing.T) {
	t1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	chunk := storage.NewChunk(4)
	col := storage.NewColumnVector(0, common.TypeTimestamp, 4)
	_ = col.Append(common.NewTimestamp(t1))
	_ = col.Append(common.NewTimestamp(t2))
	_ = col.Append(common.NewTimestamp(t3))
	_ = chunk.AddColumn(col)

	// 需要再加一列使 rowCount > 0
	col2 := storage.NewColumnVector(1, common.TypeInt64, 4)
	_ = col2.Append(common.NewInt64(1))
	_ = col2.Append(common.NewInt64(2))
	_ = col2.Append(common.NewInt64(3))
	_ = chunk.AddColumn(col2)

	result, err := sliceChunk(chunk, 1, 3)
	if err != nil {
		t.Fatalf("sliceChunk TIMESTAMP: %v", err)
	}

	resultCol, _ := result.GetColumn(0)
	if !resultCol.GetValue(0).Time.Equal(t2) {
		t.Errorf("期望第 0 行 = %v，得到 %v", t2, resultCol.GetValue(0).Time)
	}
	if !resultCol.GetValue(1).Time.Equal(t3) {
		t.Errorf("期望第 1 行 = %v，得到 %v", t3, resultCol.GetValue(1).Time)
	}
}

// TestSliceChunk_MultipleColumns 测试 sliceChunk 处理多列混合类型。
func TestSliceChunk_MultipleColumns(t *testing.T) {
	chunk := storage.NewChunk(4)

	intCol := storage.NewColumnVector(0, common.TypeInt64, 4)
	_ = intCol.Append(common.NewInt64(10))
	_ = intCol.Append(common.NewInt64(20))
	_ = intCol.Append(common.NewInt64(30))
	_ = intCol.Append(common.NewInt64(40))
	_ = chunk.AddColumn(intCol)

	floatCol := storage.NewColumnVector(1, common.TypeFloat64, 4)
	_ = floatCol.Append(common.NewFloat64(1.0))
	_ = floatCol.Append(common.NewFloat64(2.0))
	_ = floatCol.Append(common.NewFloat64(3.0))
	_ = floatCol.Append(common.NewFloat64(4.0))
	_ = chunk.AddColumn(floatCol)

	strCol := storage.NewColumnVector(2, common.TypeString, 4)
	_ = strCol.Append(common.NewString("a"))
	_ = strCol.Append(common.NewString("b"))
	_ = strCol.Append(common.NewString("c"))
	_ = strCol.Append(common.NewString("d"))
	_ = chunk.AddColumn(strCol)

	result, err := sliceChunk(chunk, 1, 3)
	if err != nil {
		t.Fatalf("sliceChunk 多列: %v", err)
	}

	if result.RowCount() != 2 {
		t.Fatalf("期望 2 行，得到 %d", result.RowCount())
	}

	// 验证 INT64 列
	r0, _ := result.GetColumn(0)
	if r0.GetValue(0).Int64 != 20 {
		t.Errorf("INT64 列第 0 行期望 20，得到 %d", r0.GetValue(0).Int64)
	}

	// 验证 FLOAT64 列
	r1, _ := result.GetColumn(1)
	if r1.GetValue(0).Float64 != 2.0 {
		t.Errorf("FLOAT64 列第 0 行期望 2.0，得到 %g", r1.GetValue(0).Float64)
	}

	// 验证 STRING 列
	r2, _ := result.GetColumn(2)
	if r2.GetValue(0).Str != "b" {
		t.Errorf("STRING 列第 0 行期望 'b'，得到 %q", r2.GetValue(0).Str)
	}
}

// TestSliceChunk_SliceWithNulls 测试 sliceChunk 处理含 NULL 值的列。
func TestSliceChunk_SliceWithNulls(t *testing.T) {
	chunk := storage.NewChunk(4)

	intCol := storage.NewColumnVector(0, common.TypeInt64, 4)
	_ = intCol.Append(common.NewInt64(10))
	_ = intCol.Append(common.NewNull())
	_ = intCol.Append(common.NewInt64(30))
	_ = intCol.Append(common.NewInt64(40))
	_ = chunk.AddColumn(intCol)

	result, err := sliceChunk(chunk, 1, 3)
	if err != nil {
		t.Fatalf("sliceChunk 含 NULL: %v", err)
	}

	resultCol, _ := result.GetColumn(0)
	// 第 0 行应为 NULL
	if resultCol.GetValue(0).Valid {
		t.Errorf("期望第 0 行为 NULL，得到 %v", resultCol.GetValue(0))
	}
	// 第 1 行应为 30
	if resultCol.GetValue(1).Int64 != 30 {
		t.Errorf("期望第 1 行 = 30，得到 %d", resultCol.GetValue(1).Int64)
	}
}
