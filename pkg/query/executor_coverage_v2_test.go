package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// ============================================================================
// executeFilter tests
// ============================================================================

// TestExecuteFilter_EmptyInput tests executeFilter with empty input chunks (no rows).
func TestExecuteFilter_EmptyInput(t *testing.T) {
	ms := newMockStorage() // no entries

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(10)}},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("executeFilter empty input: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 0 {
		t.Errorf("expected 0 rows for empty input, got %d", totalRows)
	}
}

// TestExecuteFilter_SelectsAllRows tests executeFilter with a condition that selects all rows.
func TestExecuteFilter_SelectsAllRows(t *testing.T) {
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

	// age > 0 selects all rows
	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(0)}},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("executeFilter selects all: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 2 {
		t.Errorf("expected 2 rows (all pass filter), got %d", totalRows)
	}
}

// TestExecuteFilter_SelectsNoRows tests executeFilter with a condition that selects no rows.
func TestExecuteFilter_SelectsNoRows(t *testing.T) {
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

	// age > 1000 selects no rows
	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(1000)}},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("executeFilter selects none: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 0 {
		t.Errorf("expected 0 rows (none pass filter), got %d", totalRows)
	}
}

// TestExecuteFilter_ComplexAndOr tests executeFilter with complex AND/OR conditions.
func TestExecuteFilter_ComplexAndOr(t *testing.T) {
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
	ms.addEntry("d", map[string]common.Value{
		testColID: common.NewInt64(4), testColName: common.NewString(testNameDiana),
		testColAge: common.NewInt64(28), testColScore: common.NewFloat64(60.0),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	// (age > 25 AND score > 80) OR (age < 26)
	// alice: (30>25 AND 95.5>80) = true -> selected
	// bob: (25>25=false) OR (25<26=true) = true -> selected
	// charlie: (35>25 AND 72>80=false) OR (35<26=false) = false -> not selected
	// diana: (28>25 AND 60>80=false) OR (28<26=false) = false -> not selected
	filter := &FilterNode{
		Child: scan,
		Condition: &BinaryExpr{
			Op: OpOr,
			Left: &BinaryExpr{
				Op:    OpAnd,
				Left:  &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(25)}},
				Right: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColScore, Idx: 3, typ: common.TypeFloat64}, Right: &LiteralExpr{Value: common.NewFloat64(80)}},
			},
			Right: &BinaryExpr{Op: OpLt, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(26)}},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("executeFilter complex AND/OR: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 2 {
		t.Errorf("expected 2 rows (complex AND/OR), got %d", totalRows)
	}
}

// ============================================================================
// executeProject tests
// ============================================================================

// TestExecuteProject_EmptyInput tests executeProject with empty input chunks.
func TestExecuteProject_EmptyInput(t *testing.T) {
	ms := newMockStorage() // no entries

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	project := &ProjectNode{
		Child: scan,
		Expressions: []Expression{
			&ResolvedColumnExpr{Name: testColName, Idx: 1, typ: common.TypeString},
		},
		Aliases: []string{testColName},
		schema: []ColumnDef{
			{Name: testColName, Type: common.TypeString, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(project)
	if err != nil {
		t.Fatalf("executeProject empty input: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 0 {
		t.Errorf("expected 0 rows for empty input, got %d", totalRows)
	}
}

// TestExecuteProject_TypeCoercion tests executeProject with expression that requires type coercion.
func TestExecuteProject_TypeCoercion(t *testing.T) {
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

	// Project Int64 column into Float64 output schema (triggers coerceValue)
	project := &ProjectNode{
		Child: scan,
		Expressions: []Expression{
			&ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
		},
		Aliases: []string{"age_float"},
		schema: []ColumnDef{
			{Name: "age_float", Type: common.TypeFloat64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(project)
	if err != nil {
		t.Fatalf("executeProject type coercion: %v", err)
	}

	if len(chunks) == 0 || chunks[0].RowCount() == 0 {
		t.Fatal("expected at least 1 row")
	}

	col, _ := chunks[0].GetColumn(0)
	val := col.GetValue(0)
	if val.Float64 != 30.0 {
		t.Errorf("expected 30.0 (coerced int->float), got %g", val.Float64)
	}
}

// TestExecuteProject_MultipleExpressions tests executeProject with multiple expressions.
func TestExecuteProject_MultipleExpressions(t *testing.T) {
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

	project := &ProjectNode{
		Child: scan,
		Expressions: []Expression{
			&ResolvedColumnExpr{Name: testColName, Idx: 1, typ: common.TypeString},
			&ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
			&ResolvedColumnExpr{Name: testColScore, Idx: 3, typ: common.TypeFloat64},
		},
		Aliases: []string{testColName, testColAge, testColScore},
		schema: []ColumnDef{
			{Name: testColName, Type: common.TypeString, Nullable: true},
			{Name: testColAge, Type: common.TypeInt64, Nullable: true},
			{Name: testColScore, Type: common.TypeFloat64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(project)
	if err != nil {
		t.Fatalf("executeProject multiple expressions: %v", err)
	}

	if len(chunks) == 0 || chunks[0].RowCount() == 0 {
		t.Fatal("expected at least 1 row")
	}

	if chunks[0].ColumnCount() != 3 {
		t.Errorf("expected 3 columns, got %d", chunks[0].ColumnCount())
	}

	nameCol, _ := chunks[0].GetColumn(0)
	if nameCol.GetValue(0).Str != testNameAlice {
		t.Errorf("expected name='alice', got %q", nameCol.GetValue(0).Str)
	}

	ageCol, _ := chunks[0].GetColumn(1)
	if ageCol.GetValue(0).Int64 != 30 {
		t.Errorf("expected age=30, got %d", ageCol.GetValue(0).Int64)
	}

	scoreCol, _ := chunks[0].GetColumn(2)
	if scoreCol.GetValue(0).Float64 != 95.5 {
		t.Errorf("expected score=95.5, got %g", scoreCol.GetValue(0).Float64)
	}
}

// ============================================================================
// projectChunk tests
// ============================================================================

// TestProjectChunk_LiteralExpression tests projectChunk with a literal expression.
func TestProjectChunk_LiteralExpression(t *testing.T) {
	inputSchema := []ColumnDef{
		{Name: testColID, Type: common.TypeInt64, Nullable: false},
	}
	outputSchema := []ColumnDef{
		{Name: "constant", Type: common.TypeInt64, Nullable: false},
	}
	colIdxMap := buildColIdxMapFromSchema(inputSchema)

	chunk := storage.NewChunk(defaultChunkSize)
	col := storage.NewColumnVector(0, common.TypeInt64, 2)
	_ = col.Append(common.NewInt64(1))
	_ = col.Append(common.NewInt64(2))
	_ = chunk.AddColumn(col)

	// Project a constant literal value
	exprs := []Expression{&LiteralExpr{Value: common.NewInt64(42)}}
	output, err := projectChunk(chunk, exprs, inputSchema, outputSchema, colIdxMap)
	if err != nil {
		t.Fatalf("projectChunk literal expr: %v", err)
	}
	if output.RowCount() != 2 {
		t.Errorf("expected 2 rows, got %d", output.RowCount())
	}

	col0, _ := output.GetColumn(0)
	// Both rows should have the literal value 42
	if col0.GetValue(0).Int64 != 42 {
		t.Errorf("expected literal value 42 at row 0, got %d", col0.GetValue(0).Int64)
	}
	if col0.GetValue(1).Int64 != 42 {
		t.Errorf("expected literal value 42 at row 1, got %d", col0.GetValue(1).Int64)
	}
}

// TestProjectChunk_ColumnReferenceExpression tests projectChunk with a column reference expression.
func TestProjectChunk_ColumnReferenceExpression(t *testing.T) {
	inputSchema := []ColumnDef{
		{Name: testColID, Type: common.TypeInt64, Nullable: false},
		{Name: testColName, Type: common.TypeString, Nullable: true},
	}
	outputSchema := []ColumnDef{
		{Name: testColName, Type: common.TypeString, Nullable: true},
	}
	colIdxMap := buildColIdxMapFromSchema(inputSchema)

	chunk := storage.NewChunk(defaultChunkSize)
	idCol := storage.NewColumnVector(0, common.TypeInt64, 2)
	_ = idCol.Append(common.NewInt64(1))
	_ = idCol.Append(common.NewInt64(2))
	_ = chunk.AddColumn(idCol)

	nameCol := storage.NewColumnVector(1, common.TypeString, 2)
	_ = nameCol.Append(common.NewString(testNameAlice))
	_ = nameCol.Append(common.NewString(testNameBob))
	_ = chunk.AddColumn(nameCol)

	// Project the name column using ResolvedColumnExpr
	exprs := []Expression{&ResolvedColumnExpr{Name: testColName, Idx: 1, typ: common.TypeString}}
	output, err := projectChunk(chunk, exprs, inputSchema, outputSchema, colIdxMap)
	if err != nil {
		t.Fatalf("projectChunk column ref: %v", err)
	}
	if output.RowCount() != 2 {
		t.Errorf("expected 2 rows, got %d", output.RowCount())
	}

	col0, _ := output.GetColumn(0)
	if col0.GetValue(0).Str != testNameAlice {
		t.Errorf("expected 'alice' at row 0, got %q", col0.GetValue(0).Str)
	}
	if col0.GetValue(1).Str != testNameBob {
		t.Errorf("expected 'bob' at row 1, got %q", col0.GetValue(1).Str)
	}
}
