package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

const (
	benchTableName = "users"
	benchColID     = "id"
	benchColName   = "name"
	benchColScore  = "score"
)

var benchTableDef = []catalog.ColumnDef{
	{Name: benchColID, Type: common.TypeInt64, Nullable: false},
	{Name: benchColName, Type: common.TypeString, Nullable: true},
	{Name: benchColScore, Type: common.TypeFloat64, Nullable: true},
}

// --- SQL 解析基准测试 ---

func BenchmarkParserSelect(b *testing.B) {
	parser := NewParser()
	sql := "SELECT id, name, score FROM users WHERE score > 90.0"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parser.Parse(sql)
	}
	b.ReportAllocs()
}

func BenchmarkParserInsert(b *testing.B) {
	parser := NewParser()
	sql := "INSERT INTO users (id, name) VALUES (1, 'alice')"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parser.Parse(sql)
	}
	b.ReportAllocs()
}

func BenchmarkParserCreateTable(b *testing.B) {
	parser := NewParser()
	sql := "CREATE TABLE users (id INT64, name STRING, score FLOAT64, PRIMARY KEY (id))"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parser.Parse(sql)
	}
	b.ReportAllocs()
}

// BenchmarkPreprocessSQLNoMatch 度量无关键字场景（最常见 DML）的预处理开销。
// 用于验证单遍扫描 + 前置关键字首字符判断在「无匹配」场景下的零拷贝快速路径。
func BenchmarkPreprocessSQLNoMatch(b *testing.B) {
	parser := NewParser()
	sql := "SELECT id, name, score FROM users WHERE score > 90.0"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = parser.preprocessSQL(sql)
	}
	b.ReportAllocs()
}

// BenchmarkPreprocessSQLWithMatch 度量含关键字场景（CREATE TABLE）的预处理开销。
// 用于验证单遍扫描在「有匹配」场景下仍能高效完成类型替换。
func BenchmarkPreprocessSQLWithMatch(b *testing.B) {
	parser := NewParser()
	sql := "CREATE TABLE users (id INT64, name STRING, score FLOAT64, active BOOL, PRIMARY KEY (id))"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = parser.preprocessSQL(sql)
	}
	b.ReportAllocs()
}

// --- 分析器基准测试 ---

func BenchmarkAnalyzer(b *testing.B) {
	cat := catalog.NewCatalog("")
	_ = cat.CreateTable(benchTableName, benchTableDef, []string{benchColID}, catalog.TableOptions{})

	analyzer := NewAnalyzer(cat)
	parser := NewParser()
	stmt, _ := parser.Parse("SELECT id, name FROM users WHERE score > 90.0")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analyzer.Analyze(stmt)
	}
	b.ReportAllocs()
}

// --- 优化器基准测试 ---

func BenchmarkOptimizer(b *testing.B) {
	cat := catalog.NewCatalog("")
	_ = cat.CreateTable(benchTableName, benchTableDef, []string{benchColID}, catalog.TableOptions{})

	analyzer := NewAnalyzer(cat)
	parser := NewParser()
	optimizer := NewOptimizer()

	// 每次迭代创建新的 plan，因为 Optimizer 会原地修改 plan 节点，
	// 复用同一 plan 会导致后续迭代测量的是已优化计划的开销。
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stmt, _ := parser.Parse("SELECT id, name FROM users WHERE score > 90.0")
		plan, _ := analyzer.Analyze(stmt)
		_ = optimizer.Optimize(plan)
	}
	b.ReportAllocs()
}

// --- 执行器基准测试 ---

// benchStorageProvider 提供基准测试所需的存储访问。
type benchStorageProvider struct{}

func (sp *benchStorageProvider) ScanRange(_, _ string) []storage.ScanEntry {
	return nil
}

func (sp *benchStorageProvider) ScanRangeWithPruning(start, end string, _ []storage.ColumnPredicate) []storage.ScanEntry {
	return sp.ScanRange(start, end)
}

func (sp *benchStorageProvider) ColumnMeta() []storage.ColumnMeta {
	return []storage.ColumnMeta{
		{Name: benchColID, Type: common.TypeInt64},
		{Name: benchColName, Type: common.TypeString},
		{Name: benchColScore, Type: common.TypeFloat64},
	}
}

func (sp *benchStorageProvider) PrimaryIndex() *index.PrimaryIndex {
	return index.NewPrimaryIndex()
}

func (sp *benchStorageProvider) SparseIndex() *index.SparseIndex {
	return index.NewSparseIndex()
}

func BenchmarkExecutorScan(b *testing.B) {
	sp := &benchStorageProvider{}
	exec := NewExecutor(sp)

	plan := &ScanNode{Table: benchTableName}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = exec.Execute(plan)
	}
	b.ReportAllocs()
}

// --- 端到端查询基准测试 ---

func BenchmarkEndToEndSelect(b *testing.B) {
	cat := catalog.NewCatalog("")
	_ = cat.CreateTable(benchTableName, benchTableDef, []string{benchColID}, catalog.TableOptions{})

	sp := &benchStorageProvider{}
	exec := NewExecutor(sp)
	analyzer := NewAnalyzer(cat)
	optimizer := NewOptimizer()
	parser := NewParser()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stmt, _ := parser.Parse("SELECT id, name FROM users WHERE score > 90.0")
		plan, _ := analyzer.Analyze(stmt)
		optimized := optimizer.Optimize(plan)
		_, _ = exec.Execute(optimized)
	}
	b.ReportAllocs()
}

// --- 过滤快速路径基准测试 ---
//
// 以下基准直接对预构建的 Chunk 调用 filterChunk，隔离过滤算子开销，
// 用于度量类型特化快速路径（int-family/float64/string）的吞吐。
// 数据规模 8192 行，命中约一半行，覆盖典型 OLAP 范围扫描过滤场景。

const benchFilterRows = 8192

// buildBenchFilterChunk 构建一个包含 id(INT64)/score(FLOAT64)/name(STRING)
// 三列、benchFilterRows 行的 Chunk，用于过滤基准测试。
func buildBenchFilterChunk() *storage.Chunk {
	rowCount := uint32(benchFilterRows)
	idCol := storage.NewColumnVector(0, common.TypeInt64, rowCount)
	scoreCol := storage.NewColumnVector(1, common.TypeFloat64, rowCount)
	nameCol := storage.NewColumnVector(2, common.TypeString, rowCount)
	for i := uint32(0); i < rowCount; i++ {
		idCol.SetInt64(i, int64(i))
		scoreCol.SetFloat64(i, float64(i))
		nameCol.SetString(i, fmtBenchName(i))
	}
	idCol.SetLen(rowCount)
	scoreCol.SetLen(rowCount)
	nameCol.SetLen(rowCount)
	chunk := storage.NewChunk(rowCount)
	_ = chunk.AddColumn(idCol)
	_ = chunk.AddColumn(scoreCol)
	_ = chunk.AddColumn(nameCol)
	return chunk
}

// buildBenchFilterChunkWithNulls 构建与 buildBenchFilterChunk 同结构的 Chunk，
// 但每隔 8 行将 id/score/name 置为 NULL，用于度量含 NULL 列的过滤开销
// （走 fastFilterTyped 的 nulls.Get 逐行检查分支）。
func buildBenchFilterChunkWithNulls() *storage.Chunk {
	rowCount := uint32(benchFilterRows)
	idCol := storage.NewColumnVector(0, common.TypeInt64, rowCount)
	scoreCol := storage.NewColumnVector(1, common.TypeFloat64, rowCount)
	nameCol := storage.NewColumnVector(2, common.TypeString, rowCount)
	for i := uint32(0); i < rowCount; i++ {
		if i%8 == 0 {
			idCol.SetNull(i)
			scoreCol.SetNull(i)
			nameCol.SetNull(i)
			continue
		}
		idCol.SetInt64(i, int64(i))
		scoreCol.SetFloat64(i, float64(i))
		nameCol.SetString(i, fmtBenchName(i))
	}
	idCol.SetLen(rowCount)
	scoreCol.SetLen(rowCount)
	nameCol.SetLen(rowCount)
	chunk := storage.NewChunk(rowCount)
	_ = chunk.AddColumn(idCol)
	_ = chunk.AddColumn(scoreCol)
	_ = chunk.AddColumn(nameCol)
	return chunk
}

func fmtBenchName(i uint32) string {
	return "name-" + fmtIntKey(int(i))
}

func benchSchema() []ColumnDef {
	return []ColumnDef{
		{Name: benchColID, Type: common.TypeInt64, Nullable: false},
		{Name: benchColScore, Type: common.TypeFloat64, Nullable: true},
		{Name: benchColName, Type: common.TypeString, Nullable: true},
	}
}

func BenchmarkFilterInt64FastPath(b *testing.B) {
	chunk := buildBenchFilterChunk()
	schema := benchSchema()
	cond := &BinaryExpr{
		Op:    OpGt,
		Left:  &ResolvedColumnExpr{Name: benchColID, Idx: 0, typ: common.TypeInt64},
		Right: &LiteralExpr{Value: common.NewInt64(benchFilterRows / 2)},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := filterChunk(chunk, cond, schema, buildColIdxMapFromSchema(schema))
		if err != nil {
			b.Fatal(err)
		}
		if out.RowCount() == 0 {
			b.Fatal("expected non-empty result")
		}
	}
	b.ReportAllocs()
}

func BenchmarkFilterFloat64FastPath(b *testing.B) {
	chunk := buildBenchFilterChunk()
	schema := benchSchema()
	cond := &BinaryExpr{
		Op:    OpGe,
		Left:  &ResolvedColumnExpr{Name: benchColScore, Idx: 1, typ: common.TypeFloat64},
		Right: &LiteralExpr{Value: common.NewFloat64(float64(benchFilterRows) / 2)},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := filterChunk(chunk, cond, schema, buildColIdxMapFromSchema(schema))
		if err != nil {
			b.Fatal(err)
		}
		if out.RowCount() == 0 {
			b.Fatal("expected non-empty result")
		}
	}
	b.ReportAllocs()
}

func BenchmarkFilterStringFastPath(b *testing.B) {
	chunk := buildBenchFilterChunk()
	schema := benchSchema()
	cond := &BinaryExpr{
		Op:    OpEq,
		Left:  &ResolvedColumnExpr{Name: benchColName, Idx: 2, typ: common.TypeString},
		Right: &LiteralExpr{Value: common.NewString(fmtBenchName(benchFilterRows / 2))},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := filterChunk(chunk, cond, schema, buildColIdxMapFromSchema(schema))
		if err != nil {
			b.Fatal(err)
		}
		if out.RowCount() != 1 {
			b.Fatalf("expected 1 row, got %d", out.RowCount())
		}
	}
	b.ReportAllocs()
}

// 以下基准对含 NULL 的列执行过滤，走 fastFilterTyped 的 nulls.Get 逐行检查分支，
// 用于回归“无 NULL 快速路径”优化后，含 NULL 场景未发生退化。

func BenchmarkFilterInt64FastPathWithNulls(b *testing.B) {
	chunk := buildBenchFilterChunkWithNulls()
	schema := benchSchema()
	cond := &BinaryExpr{
		Op:    OpGt,
		Left:  &ResolvedColumnExpr{Name: benchColID, Idx: 0, typ: common.TypeInt64},
		Right: &LiteralExpr{Value: common.NewInt64(benchFilterRows / 2)},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := filterChunk(chunk, cond, schema, buildColIdxMapFromSchema(schema))
		if err != nil {
			b.Fatal(err)
		}
		if out.RowCount() == 0 {
			b.Fatal("expected non-empty result")
		}
	}
	b.ReportAllocs()
}

func BenchmarkFilterStringFastPathWithNulls(b *testing.B) {
	chunk := buildBenchFilterChunkWithNulls()
	schema := benchSchema()
	// benchFilterRows/2 为 8 的倍数，在 buildBenchFilterChunkWithNulls 中被置为 NULL，
	// 故 +1 选取非 NULL 行作为等值匹配目标。
	cond := &BinaryExpr{
		Op:    OpEq,
		Left:  &ResolvedColumnExpr{Name: benchColName, Idx: 2, typ: common.TypeString},
		Right: &LiteralExpr{Value: common.NewString(fmtBenchName(benchFilterRows/2 + 1))},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := filterChunk(chunk, cond, schema, buildColIdxMapFromSchema(schema))
		if err != nil {
			b.Fatal(err)
		}
		if out.RowCount() != 1 {
			b.Fatalf("expected 1 row, got %d", out.RowCount())
		}
	}
	b.ReportAllocs()
}

// --- 聚合基准测试 ---
//
// 以下基准直接对预构建的 Chunk 调用 aggregateRows，隔离聚合算子开销，
// 用于度量列引用快速路径（跳过逐行 map 构建与全列读取）的吞吐收益。
// 数据规模 8192 行，分组列循环 0..7 共 8 个分组，覆盖典型 GROUP BY + SUM 场景。

// buildBenchAggChunk 构建一个 numCols 列 INT64、benchFilterRows 行的 Chunk。
// 第 0 列为分组键（循环 0..7，共 8 个分组），其余列为行号（用于 SUM 求值）。
func buildBenchAggChunk(numCols uint32) *storage.Chunk {
	rowCount := uint32(benchFilterRows)
	chunk := storage.NewChunk(rowCount)
	for c := uint32(0); c < numCols; c++ {
		col := storage.NewColumnVector(c, common.TypeInt64, rowCount)
		for r := uint32(0); r < rowCount; r++ {
			if c == 0 {
				col.SetInt64(r, int64(r%8))
			} else {
				col.SetInt64(r, int64(r))
			}
		}
		col.SetLen(rowCount)
		_ = chunk.AddColumn(col)
	}
	return chunk
}

// benchAggSchema 构建与 buildBenchAggChunk 同结构的 schema，列名 c0..cN。
func benchAggSchema(numCols uint32) []ColumnDef {
	schema := make([]ColumnDef, numCols)
	for i := uint32(0); i < numCols; i++ {
		schema[i] = ColumnDef{Name: "c" + fmtIntKey(int(i)), Type: common.TypeInt64, Nullable: false}
	}
	return schema
}

// benchAggNode 构建一个 GROUP BY c0, SUM c1 的 AggregateNode，直接用于 aggregateRows。
func benchAggNode(schema []ColumnDef) *AggregateNode {
	return &AggregateNode{
		GroupBy: []Expression{&ResolvedColumnExpr{Name: schema[0].Name, Idx: 0, typ: common.TypeInt64}},
		Aggregates: []AggregateExpr{
			{Func: AggSum, Arg: &ResolvedColumnExpr{Name: schema[1].Name, Idx: 1, typ: common.TypeInt64}},
		},
	}
}

// BenchmarkAggregateGroupBySum 度量窄表（3 列）GROUP BY + SUM 聚合吞吐。
func BenchmarkAggregateGroupBySum(b *testing.B) {
	schema := benchAggSchema(3)
	chunk := buildBenchAggChunk(3)
	childResult := &execResult{chunks: []*storage.Chunk{chunk}, schema: schema}
	colIdxMap := buildColIdxMapFromSchema(schema)
	agg := benchAggNode(schema)
	exec := NewExecutor(&benchStorageProvider{})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, order := exec.aggregateRows(agg, childResult, schema, colIdxMap)
		if len(order) != 8 {
			b.Fatalf("expected 8 groups, got %d", len(order))
		}
	}
	b.ReportAllocs()
}

// BenchmarkAggregateWideGroupBySum 度量宽表（32 列）GROUP BY + SUM 聚合吞吐。
// 宽表场景下慢速路径需为每行构建包含全部 32 列的 map，而快速路径仅读取 2 个引用列，
// 用以验证列引用快速路径对宽表的显著收益。
func BenchmarkAggregateWideGroupBySum(b *testing.B) {
	schema := benchAggSchema(32)
	chunk := buildBenchAggChunk(32)
	childResult := &execResult{chunks: []*storage.Chunk{chunk}, schema: schema}
	colIdxMap := buildColIdxMapFromSchema(schema)
	agg := benchAggNode(schema)
	exec := NewExecutor(&benchStorageProvider{})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, order := exec.aggregateRows(agg, childResult, schema, colIdxMap)
		if len(order) != 8 {
			b.Fatalf("expected 8 groups, got %d", len(order))
		}
	}
	b.ReportAllocs()
}
