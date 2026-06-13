package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// --- projectChunk: type coercion error paths (82.4%) ---

// TestProjectChunkCoerceValueFallback tests that projectChunk falls back to
// coerceValue when Append fails due to type mismatch.
func TestProjectChunkCoerceValueFallback(t *testing.T) {
	inputSchema := []ColumnDef{
		{Name: testColID, Type: common.TypeInt64, Nullable: false},
	}
	outputSchema := []ColumnDef{
		{Name: testColID, Type: common.TypeFloat64, Nullable: true},
	}
	colIdxMap := buildColIdxMapFromSchema(inputSchema)

	chunk := storage.NewChunk(defaultChunkSize)
	col := storage.NewColumnVector(0, common.TypeInt64, 1)
	_ = col.Append(common.NewInt64(42))
	_ = chunk.AddColumn(col)

	// The expression returns Int64 but outputSchema expects Float64,
	// so Append will fail and coerceValue will be called.
	exprs := []Expression{&ResolvedColumnExpr{Name: testColID, Idx: 0, typ: common.TypeInt64}}
	output, err := projectChunk(chunk, exprs, inputSchema, outputSchema, colIdxMap)
	if err != nil {
		t.Fatalf("projectChunk coercion: %v", err)
	}
	if output.RowCount() != 1 {
		t.Errorf("expected 1 row, got %d", output.RowCount())
	}
}

// TestProjectChunkCoerceValueStringToInt64 tests that projecting a String value
// into an Int64 column returns an error (unsupported coercion).
func TestProjectChunkCoerceValueStringToInt64(t *testing.T) {
	inputSchema := []ColumnDef{
		{Name: testColName, Type: common.TypeString, Nullable: true},
	}
	outputSchema := []ColumnDef{
		{Name: testColName, Type: common.TypeInt64, Nullable: true},
	}
	colIdxMap := buildColIdxMapFromSchema(inputSchema)

	chunk := storage.NewChunk(defaultChunkSize)
	col := storage.NewColumnVector(0, common.TypeString, 1)
	_ = col.Append(common.NewString("hello"))
	_ = chunk.AddColumn(col)

	// String value projected into Int64 column: both Append and coerceValue fail,
	// resulting in an error.
	exprs := []Expression{&ResolvedColumnExpr{Name: testColName, Idx: 0, typ: common.TypeString}}
	_, err := projectChunk(chunk, exprs, inputSchema, outputSchema, colIdxMap)
	if err == nil {
		t.Error("expected error for string->int64 coercion, got nil")
	}
}

// --- executeFilter: evalExpr error paths (84.6%) ---

// TestFilterChunkEvalExprError tests that filterChunk continues when evalExpr
// returns an error (unsupported expression type), skipping that row.
func TestFilterChunkEvalExprError(t *testing.T) {
	schema := []ColumnDef{
		{Name: testColID, Type: common.TypeInt64, Nullable: false},
	}
	colIdxMap := buildColIdxMapFromSchema(schema)

	chunk := storage.NewChunk(defaultChunkSize)
	col := storage.NewColumnVector(0, common.TypeInt64, 2)
	_ = col.Append(common.NewInt64(1))
	_ = col.Append(common.NewInt64(2))
	_ = chunk.AddColumn(col)

	// Use an unsupported expression type that will cause evalExpr to return error
	cond := &unsupportedExpr{}
	output, err := filterChunk(chunk, cond, schema, colIdxMap)
	if err != nil {
		t.Fatalf("filterChunk with unsupported expr: %v", err)
	}
	// All rows should be skipped since evalExpr errors are continued past
	if output.RowCount() != 0 {
		t.Errorf("expected 0 rows (all skipped due to eval error), got %d", output.RowCount())
	}
}

// --- filterChunk: null handling (87.0%) ---

// TestFilterChunkNullConditionResult tests filterChunk when the condition
// evaluates to NULL (not truthy), so the row is excluded.
func TestFilterChunkNullConditionResult(t *testing.T) {
	schema := []ColumnDef{
		{Name: testColID, Type: common.TypeInt64, Nullable: false},
	}
	colIdxMap := buildColIdxMapFromSchema(schema)

	chunk := storage.NewChunk(defaultChunkSize)
	col := storage.NewColumnVector(0, common.TypeInt64, 1)
	_ = col.Append(common.NewInt64(1))
	_ = chunk.AddColumn(col)

	// LiteralExpr with NULL value: isTruthyValue returns false
	cond := &LiteralExpr{Value: common.NewNull()}
	output, err := filterChunk(chunk, cond, schema, colIdxMap)
	if err != nil {
		t.Fatalf("filterChunk null condition: %v", err)
	}
	if output.RowCount() != 0 {
		t.Errorf("expected 0 rows (NULL condition is falsy), got %d", output.RowCount())
	}
}

// --- executeProject: column not found error (85.7%) ---

// TestProjectChunkColumnNotFound tests projectChunk when a column referenced
// in the expression is not found in the row values, resulting in NULL.
func TestProjectChunkColumnNotFound(t *testing.T) {
	inputSchema := []ColumnDef{
		{Name: testColID, Type: common.TypeInt64, Nullable: false},
	}
	outputSchema := []ColumnDef{
		{Name: "missing_col", Type: common.TypeInt64, Nullable: true},
	}
	colIdxMap := buildColIdxMapFromSchema(inputSchema)

	chunk := storage.NewChunk(defaultChunkSize)
	col := storage.NewColumnVector(0, common.TypeInt64, 1)
	_ = col.Append(common.NewInt64(1))
	_ = chunk.AddColumn(col)

	// ColumnExpr referencing a column not in the schema -> evalExpr returns NULL
	exprs := []Expression{&ColumnExpr{Name: "missing_col"}}
	output, err := projectChunk(chunk, exprs, inputSchema, outputSchema, colIdxMap)
	if err != nil {
		t.Fatalf("projectChunk missing column: %v", err)
	}
	if output.RowCount() != 1 {
		t.Errorf("expected 1 row, got %d", output.RowCount())
	}
}

// --- filterEntriesByPredicate: expression evaluation error (88.9%) ---

// TestFilterEntriesByPredicateEvalError tests that filterEntriesByPredicate
// skips entries when evalExpr returns an error.
func TestFilterEntriesByPredicateEvalError(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	entries := []storage.ScanEntry{
		{Key: "a", Value: storage.Row{Columns: map[string]common.Value{testColID: common.NewInt64(1)}}},
	}
	// Use an unsupported expression type that causes evalExpr to return error
	pred := &unsupportedExpr{}
	result := exec.filterEntriesByPredicate(entries, pred, []string{testColID})
	if len(result) != 0 {
		t.Errorf("expected 0 entries (eval error skips), got %d", len(result))
	}
}

// --- evalArithmeticOp: unsupported binary op ---

// TestEvalArithmeticOpUnsupported tests that evalArithmeticOp returns an error
// for unsupported binary operations.
func TestEvalArithmeticOpUnsupported(t *testing.T) {
	_, err := evalArithmeticOp(BinaryOp(99), common.NewInt64(1), common.NewInt64(2))
	if err == nil {
		t.Error("expected error for unsupported binary op, got nil")
	}
}

// --- evalFuncExpr: scalar function not supported ---

// TestEvalFuncExprUnsupported tests that evalFuncExpr returns an error
// for any function call in row evaluation context.
func TestEvalFuncExprUnsupported(t *testing.T) {
	row := map[string]common.Value{testColID: common.NewInt64(1)}
	colIdxMap := map[string]int{testColID: 0}

	_, err := evalFuncExpr(&FuncExpr{Name: testFuncUnknownFunc, Args: nil}, row, colIdxMap)
	if err == nil {
		t.Error("expected error for scalar function in row eval, got nil")
	}
}

// --- coerceValue: additional type coercion paths ---

// TestCoerceValueBoolToInt64 tests coercing bool to int64.
func TestCoerceValueBoolToInt64(t *testing.T) {
	result := coerceValue(common.NewBool(true), common.TypeInt64)
	if result.Int64 != 1 {
		t.Errorf("expected 1 for bool->int64, got %d", result.Int64)
	}
}

// TestCoerceValueBoolToFloat64 tests coercing bool to float64.
func TestCoerceValueBoolToFloat64(t *testing.T) {
	result := coerceValue(common.NewBool(true), common.TypeFloat64)
	if result.Float64 != 1.0 {
		t.Errorf("expected 1.0 for bool->float64, got %g", result.Float64)
	}
}

// TestCoerceValueNullToAny tests that NULL values are preserved as NULL.
func TestCoerceValueNullToAny(t *testing.T) {
	result := coerceValue(common.NewNull(), common.TypeInt64)
	if result.Valid {
		t.Errorf("expected NULL for null->int64, got valid value")
	}
}

// --- evalLogicalOp: AND/OR with evalExpr error on right side ---

// TestEvalLogicalOpAndRightError tests that AND returns an error when the right
// side evaluation fails.
func TestEvalLogicalOpAndRightError(t *testing.T) {
	row := map[string]common.Value{testColID: common.NewInt64(1)}
	colIdxMap := map[string]int{testColID: 0}

	// AND: left is truthy, right eval fails
	e := &BinaryExpr{
		Op:    OpAnd,
		Left:  &LiteralExpr{Value: common.NewBool(true)},
		Right: &unsupportedExpr{},
	}
	_, err := evalBinaryExpr(e, row, colIdxMap)
	if err == nil {
		t.Error("expected error for AND with error on right side, got nil")
	}
}

// TestEvalLogicalOpOrRightError tests that OR returns an error when the right
// side evaluation fails.
func TestEvalLogicalOpOrRightError(t *testing.T) {
	row := map[string]common.Value{testColID: common.NewInt64(1)}
	colIdxMap := map[string]int{testColID: 0}

	// OR: left is falsy, right eval fails
	e := &BinaryExpr{
		Op:    OpOr,
		Left:  &LiteralExpr{Value: common.NewBool(false)},
		Right: &unsupportedExpr{},
	}
	_, err := evalBinaryExpr(e, row, colIdxMap)
	if err == nil {
		t.Error("expected error for OR with error on right side, got nil")
	}
}

// --- executeNode: unsupported node type ---

// TestExecuteNodeUnsupportedType tests that executeNode returns an error
// for unsupported plan node types.
func TestExecuteNodeUnsupportedType(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	_, err := exec.executeNode(nil)
	if err == nil {
		t.Error("expected error for nil plan node, got nil")
	}
}

// --- evalUnaryExpr: OpNeg with unsupported type ---

// TestEvalUnaryExprNegUnsupportedType tests that OpNeg with a string value
// returns an error.
func TestEvalUnaryExprNegUnsupportedType(t *testing.T) {
	row := map[string]common.Value{testColName: common.NewString("hello")}
	colIdxMap := map[string]int{testColName: 0}

	_, err := evalUnaryExpr(&UnaryExpr{Op: OpNeg, Expr: &LiteralExpr{Value: common.NewString("hello")}}, row, colIdxMap)
	if err == nil {
		t.Error("expected error for NEG on string type, got nil")
	}
}

// --- filterChunk: empty input chunk ---

// TestFilterChunkEmptyInput tests that filterChunk with an empty input
// returns an empty chunk.
func TestFilterChunkEmptyInput(t *testing.T) {
	schema := []ColumnDef{{Name: testColID, Type: common.TypeInt64}}
	colIdxMap := buildColIdxMapFromSchema(schema)
	chunk := storage.NewChunk(defaultChunkSize)

	output, err := filterChunk(chunk, &LiteralExpr{Value: common.NewBool(true)}, schema, colIdxMap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.RowCount() != 0 {
		t.Errorf("expected 0 rows for empty input, got %d", output.RowCount())
	}
}
