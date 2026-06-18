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
