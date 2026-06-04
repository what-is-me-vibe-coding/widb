package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// mockStorage 实现 StorageProvider 接口，用于测试。
type mockStorage struct {
	entries    []storage.ScanEntry
	columnMeta []storage.ColumnMeta
	pkIndex    *index.PrimaryIndex
	spIndex    *index.SparseIndex
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		pkIndex: index.NewPrimaryIndex(),
		spIndex: index.NewSparseIndex(),
	}
}

func (m *mockStorage) ScanRange(start, end string) []storage.ScanEntry {
	var result []storage.ScanEntry
	for _, e := range m.entries {
		if e.Key >= start && e.Key <= end {
			result = append(result, e)
		}
	}
	return result
}

func (m *mockStorage) ColumnMeta() []storage.ColumnMeta {
	return m.columnMeta
}

func (m *mockStorage) PrimaryIndex() *index.PrimaryIndex {
	return m.pkIndex
}

func (m *mockStorage) SparseIndex() *index.SparseIndex {
	return m.spIndex
}

func (m *mockStorage) addEntry(key string, cols map[string]common.Value) {
	m.entries = append(m.entries, storage.ScanEntry{
		Key:   key,
		Value: storage.Row{Columns: cols},
	})
}

// buildTestSchema 构建测试用 schema 。
func buildTestSchema() []ColumnDef {
	return []ColumnDef{
		{Name: testColID, Type: common.TypeInt64, Nullable: false},
		{Name: testColName, Type: common.TypeString, Nullable: true},
		{Name: testColAge, Type: common.TypeInt64, Nullable: true},
		{Name: testColScore, Type: common.TypeFloat64, Nullable: true},
	}
}

// countRows 统计所有 Chunk 的总行数。
func countRows(chunks []*storage.Chunk) int {
	total := 0
	for _, c := range chunks {
		total += int(c.RowCount())
	}
	return total
}

// fmtKey 生成测试用 key。
func fmtKey(i int) string {
	return fmtIntKey(i)
}

func fmtIntKey(i int) string {
	const digits = "0123456789abcdef"
	if i < 16 {
		return string(digits[i])
	}
	return fmtIntKey(i/16) + string(digits[i%16])
}

func TestExecutorScanBasic(t *testing.T) {
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

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(scan)
	if err != nil {
		t.Fatalf("execute scan: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 2 {
		t.Errorf("expected 2 rows, got %d", totalRows)
	}
}

func TestExecutorScanWithPredicate(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})
	ms.addEntry("b", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
		testColAge: common.NewInt64(25), testColScore: common.NewFloat64(88.0),
	})
	ms.addEntry("c", map[string]common.Value{
		testColID: common.NewInt64(3), testColName: common.NewString(testNameCharlie),
		testColAge: common.NewInt64(35), testColScore: common.NewFloat64(72.0),
	})

	scan := &ScanNode{
		Table:     testTableUsers,
		Columns:   []string{testColID, testColName, testColAge, testColScore},
		Predicate: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(28)}},
		schema:    buildTestSchema(),
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(scan)
	if err != nil {
		t.Fatalf("execute scan with predicate: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 2 {
		t.Errorf("expected 2 rows (age > 28), got %d", totalRows)
	}
}

func TestExecutorScanEmpty(t *testing.T) {
	ms := newMockStorage()

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(scan)
	if err != nil {
		t.Fatalf("execute scan empty: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 0 {
		t.Errorf("expected 0 rows, got %d", totalRows)
	}
}

func TestExecutorScanWithKeyRange(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})
	ms.addEntry("b", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
		testColAge: common.NewInt64(25), testColScore: common.NewFloat64(88.0),
	})
	ms.addEntry("c", map[string]common.Value{
		testColID: common.NewInt64(3), testColName: common.NewString(testNameCharlie),
		testColAge: common.NewInt64(35), testColScore: common.NewFloat64(72.0),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		Predicate: &BinaryExpr{
			Op:    OpGe,
			Left:  &ResolvedColumnExpr{Name: testColID, Idx: 0, typ: common.TypeInt64},
			Right: &LiteralExpr{Value: common.NewString("b")},
		},
		schema: buildTestSchema(),
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(scan)
	if err != nil {
		t.Fatalf("execute scan with key range: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows < 1 {
		t.Errorf("expected at least 1 row with key range, got %d", totalRows)
	}
}

func TestExecutorScanWithEqKeyRange(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})
	ms.addEntry("b", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
		testColAge: common.NewInt64(25), testColScore: common.NewFloat64(88.0),
	})
	ms.addEntry("c", map[string]common.Value{
		testColID: common.NewInt64(3), testColName: common.NewString(testNameCharlie),
		testColAge: common.NewInt64(35), testColScore: common.NewFloat64(72.0),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		Predicate: &BinaryExpr{
			Op:    OpEq,
			Left:  &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
			Right: &LiteralExpr{Value: common.NewInt64(25)},
		},
		schema: buildTestSchema(),
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(scan)
	if err != nil {
		t.Fatalf("execute scan eq: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 1 {
		t.Errorf("expected 1 row (age=25), got %d", totalRows)
	}
}

func TestExecutorScanWithLtKeyRange(t *testing.T) {
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
		Predicate: &BinaryExpr{
			Op:    OpLt,
			Left:  &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
			Right: &LiteralExpr{Value: common.NewInt64(30)},
		},
		schema: buildTestSchema(),
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(scan)
	if err != nil {
		t.Fatalf("execute scan lt: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 1 {
		t.Errorf("expected 1 row (age<30), got %d", totalRows)
	}
}

func TestExecutorScanWithLeKeyRange(t *testing.T) {
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
		Predicate: &BinaryExpr{
			Op:    OpLe,
			Left:  &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
			Right: &LiteralExpr{Value: common.NewInt64(30)},
		},
		schema: buildTestSchema(),
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(scan)
	if err != nil {
		t.Fatalf("execute scan le key range: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows < 1 {
		t.Errorf("expected at least 1 row, got %d", totalRows)
	}
}

func TestExecutorScanWithGtKeyRange(t *testing.T) {
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
		Predicate: &BinaryExpr{
			Op:    OpGt,
			Left:  &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
			Right: &LiteralExpr{Value: common.NewInt64(25)},
		},
		schema: buildTestSchema(),
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(scan)
	if err != nil {
		t.Fatalf("execute scan gt key range: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows < 1 {
		t.Errorf("expected at least 1 row, got %d", totalRows)
	}
}

func TestExecutorUnsupportedNode(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	_, err := exec.Execute(nil)
	if err == nil {
		t.Error("expected error for nil plan node")
	}
}

func TestExecutorLargeDataset(t *testing.T) {
	ms := newMockStorage()
	for i := 0; i < 2000; i++ {
		key := fmtKey(i)
		ms.addEntry(key, map[string]common.Value{
			testColID:    common.NewInt64(int64(i)),
			testColName:  common.NewString(key),
			testColAge:   common.NewInt64(int64(20 + i%50)),
			testColScore: common.NewFloat64(float64(50 + i%50)),
		})
	}

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(scan)
	if err != nil {
		t.Fatalf("execute large dataset: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 2000 {
		t.Errorf("expected 2000 rows, got %d", totalRows)
	}
}

func TestExecutorComparisonOperators(t *testing.T) {
	tests := []struct {
		name string
		op   BinaryOp
		want int
	}{
		{"eq", OpEq, 1},
		{"ne", OpNe, 2},
		{"lt", OpLt, 1},
		{"le", OpLe, 2},
		{"gt", OpGt, 1},
		{"ge", OpGe, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := newMockStorage()
			ms.addEntry("a", map[string]common.Value{
				testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
				testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
			})
			ms.addEntry("b", map[string]common.Value{
				testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
				testColAge: common.NewInt64(25), testColScore: common.NewFloat64(88.0),
			})
			ms.addEntry("c", map[string]common.Value{
				testColID: common.NewInt64(3), testColName: common.NewString(testNameCharlie),
				testColAge: common.NewInt64(35), testColScore: common.NewFloat64(72.0),
			})

			scan := &ScanNode{
				Table:   testTableUsers,
				Columns: []string{testColID, testColName, testColAge, testColScore},
				schema:  buildTestSchema(),
			}

			filter := &FilterNode{
				Child:     scan,
				Condition: &BinaryExpr{Op: tt.op, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(30)}},
			}

			exec := NewExecutor(ms)
			chunks, err := exec.Execute(filter)
			if err != nil {
				t.Fatalf("execute %s: %v", tt.name, err)
			}

			totalRows := countRows(chunks)
			if totalRows != tt.want {
				t.Errorf("op %s: expected %d rows, got %d", tt.op, tt.want, totalRows)
			}
		})
	}
}

func TestExecutorFilterWithNullLiteral(t *testing.T) {
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

	// Comparison with NULL literal should result in NULL (falsy)
	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpEq, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewNull()}},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("execute filter null literal: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 0 {
		t.Errorf("expected 0 rows (comparison with NULL), got %d", totalRows)
	}
}

// TestMaxStr 测试 maxStr 辅助函数。
func TestMaxStr(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want string
	}{
		{"b大于a返回b", "b", "a", "b"},
		{"a小于b返回b", "a", "b", "b"},
		{"相等返回自身", "a", "a", "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maxStr(tt.a, tt.b); got != tt.want {
				t.Errorf("maxStr(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// TestMinStr 测试 minStr 辅助函数。
func TestMinStr(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want string
	}{
		{"a小于b返回a", "a", "b", "a"},
		{"b大于a返回a", "b", "a", "a"},
		{"相等返回自身", "a", "a", "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := minStr(tt.a, tt.b); got != tt.want {
				t.Errorf("minStr(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
