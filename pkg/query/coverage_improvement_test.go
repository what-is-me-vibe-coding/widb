package query

import (
	"testing"

	"github.com/xwb1989/sqlparser"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// --- needsProjection 覆盖率提升测试 ---

// TestNeedsProjection_NoFrom 测试 sel.From == nil 时返回 true。
func TestNeedsProjection_NoFrom(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	sel := &SelectStatement{
		Columns: []SelectColumn{
			{Expr: &LiteralExpr{Value: common.NewInt64(1)}},
		},
		From: nil,
	}
	// needsProjection 在 From == nil 时直接返回 true
	got := analyzer.needsProjection(sel, nil)
	if !got {
		t.Error("needsProjection(From=nil) = false, want true")
	}
}

// TestNeedsProjection_StarNoProjection 测试 SELECT * 不需要投影。
func TestNeedsProjection_StarNoProjection(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	table := testCatalog().Tables[testTableUsers]
	sel := &SelectStatement{
		Columns: []SelectColumn{
			{Expr: &StarExpr{}},
		},
		From: &TableRef{Name: testTableUsers},
	}
	got := analyzer.needsProjection(sel, table)
	if got {
		t.Error("needsProjection(SELECT *) = true, want false")
	}
}

// TestNeedsProjection_WithAlias 测试带别名的列需要投影。
func TestNeedsProjection_WithAlias(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	table := testCatalog().Tables[testTableUsers]
	sel := &SelectStatement{
		Columns: []SelectColumn{
			{Expr: &ColumnExpr{Name: testColName}, Alias: "n"},
			{Expr: &ColumnExpr{Name: testColAge}},
		},
		From: &TableRef{Name: testTableUsers},
	}
	got := analyzer.needsProjection(sel, table)
	if !got {
		t.Error("needsProjection(带别名) = false, want true")
	}
}

// TestNeedsProjection_WithFuncExpr 测试包含函数表达式的列需要投影。
func TestNeedsProjection_WithFuncExpr(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	table := testCatalog().Tables[testTableUsers]
	sel := &SelectStatement{
		Columns: []SelectColumn{
			{Expr: &FuncExpr{Name: testFuncCount, Args: []Expression{&StarExpr{}}}},
		},
		From: &TableRef{Name: testTableUsers},
	}
	got := analyzer.needsProjection(sel, table)
	if !got {
		t.Error("needsProjection(FuncExpr) = false, want true")
	}
}

// TestNeedsProjection_WithBinaryExpr 测试包含二元表达式的列需要投影。
func TestNeedsProjection_WithBinaryExpr(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	table := testCatalog().Tables[testTableUsers]
	sel := &SelectStatement{
		Columns: []SelectColumn{
			{Expr: &BinaryExpr{Op: OpAdd, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(1)}}},
		},
		From: &TableRef{Name: testTableUsers},
	}
	got := analyzer.needsProjection(sel, table)
	if !got {
		t.Error("needsProjection(BinaryExpr) = false, want true")
	}
}

// TestNeedsProjection_WithUnaryExpr 测试包含一元表达式的列需要投影。
func TestNeedsProjection_WithUnaryExpr(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	table := testCatalog().Tables[testTableUsers]
	sel := &SelectStatement{
		Columns: []SelectColumn{
			{Expr: &UnaryExpr{Op: OpNeg, Expr: &ColumnExpr{Name: testColAge}}},
		},
		From: &TableRef{Name: testTableUsers},
	}
	got := analyzer.needsProjection(sel, table)
	if !got {
		t.Error("needsProjection(UnaryExpr) = false, want true")
	}
}

// TestNeedsProjection_SimpleColumnsNoProjection 测试多列无别名/函数/二元/一元表达式时不需要投影。
func TestNeedsProjection_SimpleColumnsNoProjection(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	table := testCatalog().Tables[testTableUsers]
	sel := &SelectStatement{
		Columns: []SelectColumn{
			{Expr: &ColumnExpr{Name: testColName}},
			{Expr: &ColumnExpr{Name: testColAge}},
		},
		From: &TableRef{Name: testTableUsers},
	}
	got := analyzer.needsProjection(sel, table)
	if got {
		t.Error("needsProjection(简单多列) = true, want false")
	}
}

// --- buildChunksFromEntries 覆盖率提升测试 ---

// TestBuildChunksFromEntries_EmptyEntries 测试空 entries 返回 nil。
func TestBuildChunksFromEntries_EmptyEntries(t *testing.T) {
	schema := []ColumnDef{
		{Name: testColID, Type: common.TypeInt64, Nullable: false},
	}
	chunks := buildChunksFromEntries(nil, schema, 1024)
	if chunks != nil {
		t.Errorf("buildChunksFromEntries(空entries) = %v, want nil", chunks)
	}
}

// TestBuildChunksFromEntries_EmptySchema 测试空 schema 返回 nil。
func TestBuildChunksFromEntries_EmptySchema(t *testing.T) {
	entries := []storage.ScanEntry{
		{Key: "a", Value: storage.Row{Columns: map[string]common.Value{testColID: common.NewInt64(1)}}},
	}
	chunks := buildChunksFromEntries(entries, nil, 1024)
	if chunks != nil {
		t.Errorf("buildChunksFromEntries(空schema) = %v, want nil", chunks)
	}
}

// TestBuildChunksFromEntries_MissingColumn 测试 entries 中缺少 schema 定义的列时走 coerceValue 路径。
func TestBuildChunksFromEntries_MissingColumn(t *testing.T) {
	// schema 定义了 id 和 name 两列，但 entry 中只有 id
	schema := []ColumnDef{
		{Name: testColID, Type: common.TypeInt64, Nullable: false},
		{Name: testColName, Type: common.TypeString, Nullable: true},
	}
	entries := []storage.ScanEntry{
		{Key: "a", Value: storage.Row{Columns: map[string]common.Value{
			testColID: common.NewInt64(1),
			// name 列缺失，应填充 NULL
		}}},
	}
	chunks := buildChunksFromEntries(entries, schema, 1024)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].RowCount() != 1 {
		t.Errorf("expected 1 row, got %d", chunks[0].RowCount())
	}
}

// TestBuildChunksFromEntries_MultipleChunks 测试 chunkSize 小于 entries 数量时生成多个 chunk。
func TestBuildChunksFromEntries_MultipleChunks(t *testing.T) {
	schema := []ColumnDef{
		{Name: testColID, Type: common.TypeInt64, Nullable: false},
	}
	entries := make([]storage.ScanEntry, 5)
	for i := range entries {
		entries[i] = storage.ScanEntry{
			Key:   fmtKey(i),
			Value: storage.Row{Columns: map[string]common.Value{testColID: common.NewInt64(int64(i))}},
		}
	}
	// chunkSize=2，5 条 entries 应生成 3 个 chunk（2+2+1）
	chunks := buildChunksFromEntries(entries, schema, 2)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	totalRows := countRows(chunks)
	if totalRows != 5 {
		t.Errorf("expected 5 total rows, got %d", totalRows)
	}
}

// --- convertLimit 覆盖率提升测试 ---

// TestConvertLimit_InvalidOffset 测试无效的 LIMIT offset 值。
func TestConvertLimit_InvalidOffset(t *testing.T) {
	p := NewParser()
	// 使用 ColName 作为 offset，触发 parseUint64 的非 SQLVal 分支
	limit := &sqlparser.Limit{
		Offset:   &sqlparser.ColName{Name: sqlparser.NewColIdent("col")},
		Rowcount: &sqlparser.SQLVal{Type: sqlparser.IntVal, Val: []byte("10")},
	}
	_, err := p.convertLimit(limit)
	if err == nil {
		t.Error("convertLimit(无效offset) 应返回错误，got nil")
	}
}

// TestConvertLimit_InvalidCount 测试无效的 LIMIT count 值。
func TestConvertLimit_InvalidCount(t *testing.T) {
	p := NewParser()
	// 使用 ColName 作为 count，触发 parseUint64 的非 SQLVal 分支
	limit := &sqlparser.Limit{
		Rowcount: &sqlparser.ColName{Name: sqlparser.NewColIdent("col")},
	}
	_, err := p.convertLimit(limit)
	if err == nil {
		t.Error("convertLimit(无效count) 应返回错误，got nil")
	}
}

// TestBuildChunksFromEntries_CoerceValue 测试类型不匹配时走 coerceValue 路径。
func TestBuildChunksFromEntries_CoerceValue(t *testing.T) {
	// schema 定义 id 为 Int64，但 entry 中 id 存的是 Float64 值
	// Append 会因类型不匹配返回错误，然后走 coerceValue 路径
	schema := []ColumnDef{
		{Name: testColID, Type: common.TypeInt64, Nullable: false},
	}
	entries := []storage.ScanEntry{
		{Key: "a", Value: storage.Row{Columns: map[string]common.Value{
			testColID: common.NewFloat64(42.7),
		}}},
	}
	chunks := buildChunksFromEntries(entries, schema, 1024)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].RowCount() != 1 {
		t.Errorf("expected 1 row, got %d", chunks[0].RowCount())
	}
}

// TestNeedsProjection_WithAliasIndirect 测试通过 Analyze 间接验证带别名的 SELECT 需要投影。
func TestNeedsProjection_WithAliasIndirect(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("SELECT name AS n FROM users")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	proj := findProjectNode(plan)
	if proj == nil {
		t.Error("带别名的 SELECT 应产生 ProjectNode，got nil")
	}
}

// TestNeedsProjection_WithFuncExprIndirect 测试通过 Analyze 间接验证带函数的 SELECT 需要投影。
func TestNeedsProjection_WithFuncExprIndirect(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("SELECT COUNT(*) FROM users")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	proj := findProjectNode(plan)
	if proj == nil {
		t.Error("带函数的 SELECT 应产生 ProjectNode，got nil")
	}
}

// TestNeedsProjection_WithBinaryExprIndirect 测试通过 Analyze 间接验证带二元表达式的 SELECT 需要投影。
func TestNeedsProjection_WithBinaryExprIndirect(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())

	// 直接构造带 BinaryExpr 的 SelectStatement，因为解析器可能不支持 age+1 语法
	sel := &SelectStatement{
		Columns: []SelectColumn{
			{Expr: &BinaryExpr{Op: OpAdd, Left: &ColumnExpr{Name: testColAge}, Right: &LiteralExpr{Value: common.NewInt64(1)}}},
		},
		From: &TableRef{Name: testTableUsers},
	}

	plan, err := analyzer.Analyze(sel)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	proj := findProjectNode(plan)
	if proj == nil {
		t.Error("带 BinaryExpr 的 SELECT 应产生 ProjectNode，got nil")
	}
}

// TestNeedsProjection_WithUnaryExprIndirect 测试通过 Analyze 间接验证带一元表达式的 SELECT 需要投影。
func TestNeedsProjection_WithUnaryExprIndirect(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())

	sel := &SelectStatement{
		Columns: []SelectColumn{
			{Expr: &UnaryExpr{Op: OpNeg, Expr: &ColumnExpr{Name: testColAge}}},
		},
		From: &TableRef{Name: testTableUsers},
	}

	plan, err := analyzer.Analyze(sel)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	proj := findProjectNode(plan)
	if proj == nil {
		t.Error("带 UnaryExpr 的 SELECT 应产生 ProjectNode，got nil")
	}
}

// TestNeedsProjection_SimpleColumnsNoProjectionIndirect 测试通过 Analyze 间接验证简单列不需要投影。
func TestNeedsProjection_SimpleColumnsNoProjectionIndirect(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("SELECT name, age FROM users")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	proj := findProjectNode(plan)
	if proj != nil {
		t.Error("简单列 SELECT 不应产生 ProjectNode")
	}
}

// TestNeedsProjection_StarNoProjectionIndirect 测试通过 Analyze 间接验证 SELECT * 不需要投影。
func TestNeedsProjection_StarNoProjectionIndirect(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("SELECT * FROM users")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	proj := findProjectNode(plan)
	if proj != nil {
		t.Error("SELECT * 不应产生 ProjectNode")
	}
}

// TestNeedsProjection_NoFromIndirect 测试通过 Analyze 间接验证无 FROM 子句需要投影。
func TestNeedsProjection_NoFromIndirect(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("SELECT 1")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	proj, ok := plan.(*ProjectNode)
	if !ok {
		t.Fatalf("SELECT 1 应产生 ProjectNode，got %T", plan)
	}
	if len(proj.Expressions) != 1 {
		t.Errorf("expected 1 expression, got %d", len(proj.Expressions))
	}
}

// TestConvertLimit_InvalidOffsetValue 测试 LIMIT offset 为无效数字字符串。
func TestConvertLimit_InvalidOffsetValue(t *testing.T) {
	p := NewParser()
	// 使用 SQLVal 但包含非数字内容，触发 ParseUint 错误
	limit := &sqlparser.Limit{
		Offset:   &sqlparser.SQLVal{Type: sqlparser.IntVal, Val: []byte("-1")},
		Rowcount: &sqlparser.SQLVal{Type: sqlparser.IntVal, Val: []byte("10")},
	}
	_, err := p.convertLimit(limit)
	if err == nil {
		t.Error("convertLimit(负数offset) 应返回错误，got nil")
	}
}

// TestConvertLimit_InvalidCountValue 测试 LIMIT count 为无效数字字符串。
func TestConvertLimit_InvalidCountValue(t *testing.T) {
	p := NewParser()
	// 使用 SQLVal 但包含非数字内容，触发 ParseUint 错误
	limit := &sqlparser.Limit{
		Rowcount: &sqlparser.SQLVal{Type: sqlparser.IntVal, Val: []byte("-1")},
	}
	_, err := p.convertLimit(limit)
	if err == nil {
		t.Error("convertLimit(负数count) 应返回错误，got nil")
	}
}

// TestBuildChunksFromEntries_EmptyEntriesAndSchema 测试空 entries 和空 schema 都返回 nil。
func TestBuildChunksFromEntries_EmptyEntriesAndSchema(t *testing.T) {
	chunks := buildChunksFromEntries(nil, nil, 1024)
	if chunks != nil {
		t.Errorf("buildChunksFromEntries(空entries和schema) = %v, want nil", chunks)
	}

	// 空 entries 切片（非 nil）也应返回 nil
	chunks = buildChunksFromEntries([]storage.ScanEntry{}, []ColumnDef{{Name: testColID, Type: common.TypeInt64}}, 1024)
	if chunks != nil {
		t.Errorf("buildChunksFromEntries(空entries切片) = %v, want nil", chunks)
	}
}

// TestNeedsProjection_MultipleColumnsOneAlias 测试多列中一列有别名时需要投影。
func TestNeedsProjection_MultipleColumnsOneAlias(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	table := testCatalog().Tables[testTableUsers]
	sel := &SelectStatement{
		Columns: []SelectColumn{
			{Expr: &ColumnExpr{Name: testColName}, Alias: "n"},
			{Expr: &ColumnExpr{Name: testColAge}},
		},
		From: &TableRef{Name: testTableUsers},
	}
	got := analyzer.needsProjection(sel, table)
	if !got {
		t.Error("needsProjection(多列一列有别名) = false, want true")
	}
}

// TestBuildChunksFromEntries_MissingColumnWithCoerce 测试缺失列与类型不匹配组合场景。
func TestBuildChunksFromEntries_MissingColumnWithCoerce(t *testing.T) {
	schema := []ColumnDef{
		{Name: testColID, Type: common.TypeInt64, Nullable: false},
		{Name: testColScore, Type: common.TypeFloat64, Nullable: true},
	}
	entries := []storage.ScanEntry{
		{
			Key: "a",
			Value: storage.Row{Columns: map[string]common.Value{
				testColID: common.NewInt64(1),
				// score 列缺失，应填充 NULL
			}},
		},
		{
			Key: "b",
			Value: storage.Row{Columns: map[string]common.Value{
				testColID:    common.NewFloat64(2.0), // 类型不匹配，走 coerceValue
				testColScore: common.NewFloat64(88.5),
			}},
		},
	}
	chunks := buildChunksFromEntries(entries, schema, 1024)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	totalRows := countRows(chunks)
	if totalRows != 2 {
		t.Errorf("expected 2 rows, got %d", totalRows)
	}
}
