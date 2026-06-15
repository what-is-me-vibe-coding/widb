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
