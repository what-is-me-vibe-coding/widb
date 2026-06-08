package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// errNode is a PlanNode that always returns an error on execution.
type errNode struct{}

func (errNode) planNode()            {}
func (errNode) Schema() []ColumnDef  { return nil }
func (errNode) Children() []PlanNode { return nil }
func (errNode) String() string       { return "ErrorNode" }

const (
	v14ColCnt       = "cnt"
	v14ColAge       = "age"
	v14TypeBigint   = "BIGINT"
	v14TypeDouble   = "DOUBLE"
	v14TypeText     = "TEXT"
	v14FuncCount    = "count"
	v14FuncSumUpper = "SUM"
	v14FuncMinUpper = "MIN"
	v14FuncMaxUpper = "MAX"
	v14ValHello     = "hello"
	v14TypeString   = "string"
	v14TblT         = "t"
	v14ColID        = "id"
)

// --- 1. executor_ops.go: executeFilter / filterChunk ---

func TestV14FilterChunkBasic(t *testing.T) {
	schema := []ColumnDef{{Name: "a", Type: common.TypeInt64}}
	colIdxMap := buildColIdxMapFromSchema(schema)
	input := storage.NewChunk(8)
	col := storage.NewColumnVector(0, common.TypeInt64, 2)
	_ = col.Append(common.NewInt64(1))
	_ = col.Append(common.NewInt64(2))
	_ = input.AddColumn(col)
	cond := &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: "a", Idx: 0, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(0)}}
	out, err := filterChunk(input, cond, schema, colIdxMap)
	if err != nil {
		t.Fatalf("filterChunk: %v", err)
	}
	if out.RowCount() != 2 {
		t.Errorf("expected 2 rows, got %d", out.RowCount())
	}
}

func TestV14FilterChunkPartialMatch(t *testing.T) {
	schema := []ColumnDef{{Name: "x", Type: common.TypeInt64}}
	colIdxMap := buildColIdxMapFromSchema(schema)
	input := storage.NewChunk(8)
	col := storage.NewColumnVector(0, common.TypeInt64, 2)
	_ = col.Append(common.NewInt64(5))
	_ = col.Append(common.NewInt64(10))
	_ = input.AddColumn(col)
	cond := &BinaryExpr{Op: OpEq, Left: &ResolvedColumnExpr{Name: "x", Idx: 0, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(5)}}
	out, err := filterChunk(input, cond, schema, colIdxMap)
	if err != nil {
		t.Fatalf("filterChunk: %v", err)
	}
	if out.RowCount() != 1 {
		t.Errorf("expected 1 row, got %d", out.RowCount())
	}
}

func TestV14ExecuteFilterChildError(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	filter := &FilterNode{Child: errNode{}, Condition: &LiteralExpr{Value: common.NewBool(true)}}
	_, err := exec.Execute(filter)
	if err == nil {
		t.Error("expected error from child node")
	}
}

// --- 2. executor_ops.go: executeProject / projectChunk ---

func TestV14ExecuteProjectChildError(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	proj := &ProjectNode{Child: errNode{}, Expressions: []Expression{&LiteralExpr{Value: common.NewInt64(1)}}, Aliases: []string{""}, schema: []ColumnDef{{Name: "c", Type: common.TypeInt64}}}
	_, err := exec.Execute(proj)
	if err == nil {
		t.Error("expected error from child node")
	}
}

func TestV14ProjectChunkExprError(t *testing.T) {
	inSchema := []ColumnDef{{Name: "a", Type: common.TypeInt64}}
	outSchema := []ColumnDef{{Name: "b", Type: common.TypeInt64}}
	colIdxMap := buildColIdxMapFromSchema(inSchema)
	input := storage.NewChunk(8)
	col := storage.NewColumnVector(0, common.TypeInt64, 1)
	_ = col.Append(common.NewInt64(1))
	_ = input.AddColumn(col)
	exprs := []Expression{&FuncExpr{Name: "unknown_func", Args: []Expression{&ResolvedColumnExpr{Name: "a", Idx: 0, typ: common.TypeInt64}}}}
	_, err := projectChunk(input, exprs, inSchema, outSchema, colIdxMap)
	if err == nil {
		t.Error("expected error from expression eval")
	}
}

func TestV14AppendProjectValueTypeCoercion(t *testing.T) {
	colDef := ColumnDef{Name: "c", Type: common.TypeFloat64}
	col := storage.NewColumnVector(0, common.TypeFloat64, 1)
	err := appendProjectValue(col, common.NewInt64(42), colDef, 0)
	if err != nil {
		t.Fatalf("appendProjectValue with coercion: %v", err)
	}
	val := col.GetValue(0)
	if val.Typ != common.TypeFloat64 || val.Float64 != 42.0 {
		t.Errorf("expected float64(42.0), got %v", val)
	}
}

// --- 3. executor_aggregate.go: executeAggregate / accumulator.result ---

func TestV14ExecuteAggregateChildError(t *testing.T) {
	exec := NewExecutor(newMockStorage())
	agg := &AggregateNode{Child: errNode{}, Aggregates: []AggregateExpr{{Func: AggCount}}, schema: []ColumnDef{{Name: v14ColCnt, Type: common.TypeInt64}}}
	_, err := exec.Execute(agg)
	if err == nil {
		t.Error("expected error from child node")
	}
}

func TestV14AccumulatorResultAllTypes(t *testing.T) {
	tests := []struct {
		name string
		acc  accumulator
		want common.Value
	}{
		{"Count", accumulator{funcType: AggCount, count: 5}, common.NewInt64(5)},
		{"Sum_val", accumulator{funcType: AggSum, count: 3, sum: 12.5}, common.NewFloat64(12.5)},
		{"Sum_empty", accumulator{funcType: AggSum, count: 0}, common.NewNull()},
		{"Min_val", accumulator{funcType: AggMin, hasValue: true, minVal: common.NewInt64(3)}, common.NewInt64(3)},
		{"Min_empty", accumulator{funcType: AggMin, hasValue: false}, common.NewNull()},
		{"Max_val", accumulator{funcType: AggMax, hasValue: true, maxVal: common.NewInt64(9)}, common.NewInt64(9)},
		{"Max_empty", accumulator{funcType: AggMax, hasValue: false}, common.NewNull()},
		{"Avg_val", accumulator{funcType: AggAvg, count: 2, sum: 10.0}, common.NewFloat64(5.0)},
		{"Avg_empty", accumulator{funcType: AggAvg, count: 0}, common.NewNull()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.acc.result()
			if !got.Equal(tt.want) {
				t.Errorf("result() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestV14AggregateWithGroupByMultipleGroups(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice), testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5)})
	ms.addEntry("b", map[string]common.Value{testColID: common.NewInt64(2), testColName: common.NewString(testNameBob), testColAge: common.NewInt64(25), testColScore: common.NewFloat64(88.0)})
	ms.addEntry("c", map[string]common.Value{testColID: common.NewInt64(3), testColName: common.NewString(testNameCharlie), testColAge: common.NewInt64(30), testColScore: common.NewFloat64(72.0)})
	scan := &ScanNode{Table: testTableUsers, Columns: []string{testColID, testColName, testColAge, testColScore}, schema: buildTestSchema()}
	agg := &AggregateNode{
		Child:      scan,
		GroupBy:    []Expression{&ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}},
		Aggregates: []AggregateExpr{{Func: AggCount}},
		schema:     []ColumnDef{{Name: testColAge, Type: common.TypeInt64}, {Name: v14ColCnt, Type: common.TypeInt64}},
	}
	exec := NewExecutor(ms)
	chunks, err := exec.Execute(agg)
	if err != nil {
		t.Fatalf("execute aggregate: %v", err)
	}
	if countRows(chunks) != 2 {
		t.Errorf("expected 2 groups, got %d", countRows(chunks))
	}
}

// --- 4. analyzer.go: analyzeSelect ---

func TestV14AnalyzeSelectStarTableNotFound(t *testing.T) {
	analyzer := NewAnalyzer(catalog.NewDatabase())
	parser := NewParser()
	stmt, err := parser.Parse("SELECT * FROM nonexistent")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	_, err = analyzer.Analyze(stmt)
	if err == nil {
		t.Error("expected error for table not found")
	}
}

func TestV14AnalyzeSelectAggregateNoGroupBy(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()
	stmt, err := parser.Parse("SELECT SUM(score) FROM users")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}
	agg := findAggregateNode(plan)
	if agg == nil {
		t.Fatal("expected AggregateNode for SUM without GROUP BY")
	}
	if len(agg.GroupBy) != 0 {
		t.Errorf("expected 0 group by columns, got %d", len(agg.GroupBy))
	}
}

// --- 5. analyzer.go: analyzeInsert ---

func TestV14AnalyzeInsertTableNotFound(t *testing.T) {
	analyzer := NewAnalyzer(catalog.NewDatabase())
	parser := NewParser()
	stmt, err := parser.Parse("INSERT INTO nonexistent (id) VALUES (1)")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	_, err = analyzer.Analyze(stmt)
	if err == nil {
		t.Error("expected error for table not found")
	}
}

func TestV14AnalyzeInsertColumnNotExist(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()
	stmt, err := parser.Parse("INSERT INTO users (id, nonexistent_col) VALUES (1, 'x')")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	_, err = analyzer.Analyze(stmt)
	if err == nil {
		t.Error("expected error for nonexistent column")
	}
}

// --- 6. analyzer.go: buildAggregateNode ---

func TestV14BuildAggregateNodeMixedColumns(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()
	stmt, err := parser.Parse("SELECT age, COUNT(*), SUM(score) FROM users GROUP BY age")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	plan, err := analyzer.Analyze(stmt)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}
	agg := findAggregateNode(plan)
	if agg == nil {
		t.Fatal("expected AggregateNode")
	}
	if len(agg.GroupBy) != 1 {
		t.Errorf("expected 1 group by, got %d", len(agg.GroupBy))
	}
	if len(agg.Aggregates) != 2 {
		t.Errorf("expected 2 aggregates, got %d", len(agg.Aggregates))
	}
	if agg.Aggregates[0].Func != AggCount {
		t.Errorf("expected COUNT, got %v", agg.Aggregates[0].Func)
	}
	if agg.Aggregates[1].Func != AggSum {
		t.Errorf("expected SUM, got %v", agg.Aggregates[1].Func)
	}
}

// --- 7. optimizer.go: pushFilterDown ---

func TestV14PushFilterPastProject(t *testing.T) {
	rule := &PredicatePushdownRule{}
	scan := &ScanNode{Table: v14TblT, Columns: []string{v14ColID, v14ColAge}, schema: []ColumnDef{{Name: v14ColID, Type: common.TypeInt64}, {Name: v14ColAge, Type: common.TypeInt64}}}
	proj := &ProjectNode{Child: scan, Expressions: []Expression{&ResolvedColumnExpr{Name: v14ColID, Idx: 0, typ: common.TypeInt64}}, Aliases: []string{""}, schema: []ColumnDef{{Name: v14ColID, Type: common.TypeInt64}}}
	filter := &FilterNode{Child: proj, Condition: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: v14ColID, Idx: 0, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(5)}}}
	result := rule.Apply(filter)
	projResult, ok := result.(*ProjectNode)
	if !ok {
		t.Fatalf("expected ProjectNode on top, got %T", result)
	}
	if _, ok := projResult.Child.(*FilterNode); !ok {
		t.Fatalf("expected FilterNode pushed below project, got %T", projResult.Child)
	}
}

func TestV14PushFilterPastAggregate(t *testing.T) {
	rule := &PredicatePushdownRule{}
	scan := &ScanNode{Table: v14TblT, Columns: []string{v14ColAge}, schema: []ColumnDef{{Name: v14ColAge, Type: common.TypeInt64}}}
	agg := &AggregateNode{Child: scan, GroupBy: []Expression{&ResolvedColumnExpr{Name: v14ColAge, Idx: 0, typ: common.TypeInt64}}, Aggregates: []AggregateExpr{{Func: AggCount}}, schema: []ColumnDef{{Name: v14ColAge, Type: common.TypeInt64}, {Name: v14ColCnt, Type: common.TypeInt64}}}
	filter := &FilterNode{Child: agg, Condition: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: v14ColAge, Idx: 0, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(20)}}}
	result := rule.Apply(filter)
	if _, ok := result.(*FilterNode); !ok {
		t.Fatalf("expected FilterNode to remain above aggregate, got %T", result)
	}
}

func TestV14PushFilterPastLimit(t *testing.T) {
	rule := &PredicatePushdownRule{}
	scan := &ScanNode{Table: v14TblT, Columns: []string{v14ColID}, schema: []ColumnDef{{Name: v14ColID, Type: common.TypeInt64}}}
	limit := &LimitNode{Child: scan, Count: 10}
	filter := &FilterNode{Child: limit, Condition: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: v14ColID, Idx: 0, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(0)}}}
	result := rule.Apply(filter)
	f, ok := result.(*FilterNode)
	if !ok {
		t.Fatalf("expected FilterNode, got %T", result)
	}
	if _, ok := f.Child.(*LimitNode); !ok {
		t.Fatalf("expected LimitNode below filter, got %T", f.Child)
	}
}

// --- 8. parser.go: convertDDL ---

func TestV14ParserCreateTableColumnTypes(t *testing.T) {
	parser := NewParser()
	tests := []struct {
		name, sql, colName string
		colType            common.DataType
	}{
		{v14TypeBigint, "CREATE TABLE t (id BIGINT PRIMARY KEY)", v14ColID, common.TypeInt64},
		{"INT", "CREATE TABLE t (id INT PRIMARY KEY)", v14ColID, common.TypeInt64},
		{v14TypeDouble, "CREATE TABLE t (id INT, val DOUBLE)", "val", common.TypeFloat64},
		{v14TypeText, "CREATE TABLE t (id INT, name TEXT)", "name", common.TypeString},
		{"VARCHAR", "CREATE TABLE t (id INT, name VARCHAR(100))", "name", common.TypeString},
		{"BOOLEAN", "CREATE TABLE t (id INT, active BOOLEAN)", "active", common.TypeBool},
		{"TINYINT", "CREATE TABLE t (id INT, flag TINYINT)", "flag", common.TypeBool},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v14CheckColumnType(t, parser, tt.sql, tt.colName, tt.colType)
		})
	}
}

func v14CheckColumnType(t *testing.T, parser *Parser, sql, colName string, wantType common.DataType) {
	t.Helper()
	stmt, err := parser.Parse(sql)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	ct, ok := stmt.(*CreateTableStatement)
	if !ok {
		t.Fatalf("expected CreateTableStatement, got %T", stmt)
	}
	for _, col := range ct.Columns {
		if col.Name == colName {
			if col.Type != wantType {
				t.Errorf("column %q: expected %v, got %v", colName, wantType, col.Type)
			}
			return
		}
	}
	t.Errorf("column %q not found", colName)
}

func TestV14ParserDropTable(t *testing.T) {
	_, err := NewParser().Parse("DROP TABLE t")
	if err == nil {
		t.Error("expected error for unsupported DDL action DROP")
	}
}

// --- 9. parser.go: convertFuncExpr ---

func TestV14ParserConvertFuncExpr(t *testing.T) {
	parser := NewParser()
	tests := []struct {
		name, sql, funcName string
		argCount            int
	}{
		{testAggCountStar, "SELECT COUNT(*) FROM users", v14FuncCount, 1},
		{v14FuncSumUpper, "SELECT SUM(age) FROM users", "sum", 1},
		{"AVG", "SELECT AVG(score) FROM users", "avg", 1},
		{v14FuncMinUpper, "SELECT MIN(age) FROM users", "min", 1},
		{v14FuncMaxUpper, "SELECT MAX(age) FROM users", "max", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := parser.Parse(tt.sql)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			sel, ok := stmt.(*SelectStatement)
			if !ok {
				t.Fatalf("expected SelectStatement, got %T", stmt)
			}
			fn, ok := sel.Columns[0].Expr.(*FuncExpr)
			if !ok {
				t.Fatalf("expected FuncExpr, got %T", sel.Columns[0].Expr)
			}
			if fn.Name != tt.funcName {
				t.Errorf("expected %q, got %q", tt.funcName, fn.Name)
			}
			if len(fn.Args) != tt.argCount {
				t.Errorf("expected %d args, got %d", tt.argCount, len(fn.Args))
			}
		})
	}
}

// --- 10. parser.go: convertSQLVal ---

func TestV14ParserConvertSQLVal(t *testing.T) {
	parser := NewParser()
	tests := []struct {
		name, sql string
		check     func(*LiteralExpr, *testing.T)
	}{
		{"integer", "SELECT 42", func(l *LiteralExpr, t *testing.T) {
			if l.Value.Typ != common.TypeInt64 || l.Value.Int64 != 42 {
				t.Errorf("expected int64(42), got %v", l.Value)
			}
		}},
		{"float", "SELECT 3.14", func(l *LiteralExpr, t *testing.T) {
			if l.Value.Typ != common.TypeFloat64 {
				t.Errorf("expected Float64, got %v", l.Value.Typ)
			}
		}},
		{v14TypeString, "SELECT 'hello'", func(l *LiteralExpr, t *testing.T) {
			if l.Value.Typ != common.TypeString || l.Value.Str != v14ValHello {
				t.Errorf("expected string %q, got %v", v14ValHello, l.Value)
			}
		}},
		{"null", "SELECT NULL", func(l *LiteralExpr, t *testing.T) {
			if l.Value.Valid {
				t.Errorf("expected NULL, got %v", l.Value)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := parser.Parse(tt.sql)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			sel, ok := stmt.(*SelectStatement)
			if !ok {
				t.Fatalf("expected SelectStatement, got %T", stmt)
			}
			lit, ok := sel.Columns[0].Expr.(*LiteralExpr)
			if !ok {
				t.Fatalf("expected LiteralExpr, got %T", sel.Columns[0].Expr)
			}
			tt.check(lit, t)
		})
	}
}

// --- CoerceValue coverage ---

func TestV14CoerceValueConversions(t *testing.T) {
	tests := []struct {
		name        string
		val         common.Value
		target      common.DataType
		wantTyp     common.DataType
		wantFloat64 float64
		wantNull    bool
	}{
		{"null_to_any", common.NewNull(), common.TypeInt64, common.TypeNull, 0, true},
		{"same_type", common.NewInt64(7), common.TypeInt64, common.TypeInt64, 0, false},
		{"float64_to_int64", common.NewFloat64(3.7), common.TypeInt64, common.TypeInt64, 0, false},
		{"int64_to_float64", common.NewInt64(10), common.TypeFloat64, common.TypeFloat64, 10.0, false},
		{"bool_to_int64", common.NewBool(true), common.TypeInt64, common.TypeInt64, 0, false},
		{"bool_to_float64", common.NewBool(true), common.TypeFloat64, common.TypeFloat64, 1.0, false},
		{"int_to_bool", common.NewInt64(5), common.TypeBool, common.TypeBool, 0, false},
		{"string_passthrough", common.NewString("abc"), common.TypeInt64, common.TypeString, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := coerceValue(tt.val, tt.target)
			if tt.wantNull && got.Valid {
				t.Error("expected null result")
			}
			if got.Typ != tt.wantTyp {
				t.Errorf("expected type %v, got %v", tt.wantTyp, got.Typ)
			}
			if tt.wantTyp == common.TypeFloat64 && tt.wantFloat64 != 0 && got.Float64 != tt.wantFloat64 {
				t.Errorf("expected float64 %v, got %v", tt.wantFloat64, got.Float64)
			}
		})
	}
}
