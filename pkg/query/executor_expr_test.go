package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestExecutorArithmeticExpressions(t *testing.T) {
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
			&BinaryExpr{Op: OpAdd, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(5)}},
		},
		Aliases: []string{"age_plus_5"},
		schema: []ColumnDef{
			{Name: "age_plus_5", Type: common.TypeInt64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(project)
	if err != nil {
		t.Fatalf("execute arithmetic: %v", err)
	}

	if len(chunks) > 0 && chunks[0].RowCount() > 0 {
		val := func() common.Value { c, _ := chunks[0].GetColumn(0); return c.GetValue(0) }()
		if val.Int64 != 35 {
			t.Errorf("expected age+5 = 35, got %d", val.Int64)
		}
	}
}

func TestExecutorArithmeticSub(t *testing.T) {
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
			&BinaryExpr{Op: OpSub, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(10)}},
		},
		Aliases: []string{"age_minus_10"},
		schema: []ColumnDef{
			{Name: "age_minus_10", Type: common.TypeInt64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(project)
	if err != nil {
		t.Fatalf("execute sub: %v", err)
	}

	if len(chunks) > 0 && chunks[0].RowCount() > 0 {
		col, _ := chunks[0].GetColumn(0)
		val := col.GetValue(0)
		if val.Int64 != 20 {
			t.Errorf("expected 30-10=20, got %d", val.Int64)
		}
	}
}

func TestExecutorArithmeticMul(t *testing.T) {
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
			&BinaryExpr{Op: OpMul, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(2)}},
		},
		Aliases: []string{"age_times_2"},
		schema: []ColumnDef{
			{Name: "age_times_2", Type: common.TypeInt64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(project)
	if err != nil {
		t.Fatalf("execute mul: %v", err)
	}

	if len(chunks) > 0 && chunks[0].RowCount() > 0 {
		col, _ := chunks[0].GetColumn(0)
		val := col.GetValue(0)
		if val.Int64 != 60 {
			t.Errorf("expected 30*2=60, got %d", val.Int64)
		}
	}
}

func TestExecutorArithmeticDiv(t *testing.T) {
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
			&BinaryExpr{Op: OpDiv, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(3)}},
		},
		Aliases: []string{"age_div_3"},
		schema: []ColumnDef{
			{Name: "age_div_3", Type: common.TypeInt64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(project)
	if err != nil {
		t.Fatalf("execute div: %v", err)
	}

	if len(chunks) > 0 && chunks[0].RowCount() > 0 {
		col, _ := chunks[0].GetColumn(0)
		val := col.GetValue(0)
		if val.Int64 != 10 {
			t.Errorf("expected 30/3=10, got %d", val.Int64)
		}
	}
}

func TestExecutorArithmeticFloatAdd(t *testing.T) {
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
			&BinaryExpr{Op: OpAdd, Left: &ResolvedColumnExpr{Name: testColScore, Idx: 3, typ: common.TypeFloat64}, Right: &LiteralExpr{Value: common.NewFloat64(4.5)}},
		},
		Aliases: []string{"score_plus"},
		schema: []ColumnDef{
			{Name: "score_plus", Type: common.TypeFloat64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(project)
	if err != nil {
		t.Fatalf("execute float add: %v", err)
	}

	if len(chunks) > 0 && chunks[0].RowCount() > 0 {
		col, _ := chunks[0].GetColumn(0)
		val := col.GetValue(0)
		if val.Float64 != 100.0 {
			t.Errorf("expected 95.5+4.5=100.0, got %g", val.Float64)
		}
	}
}

func TestExecutorArithmeticFloatSub(t *testing.T) {
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
			&BinaryExpr{Op: OpSub, Left: &ResolvedColumnExpr{Name: testColScore, Idx: 3, typ: common.TypeFloat64}, Right: &LiteralExpr{Value: common.NewFloat64(5.5)}},
		},
		Aliases: []string{"score_minus"},
		schema: []ColumnDef{
			{Name: "score_minus", Type: common.TypeFloat64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(project)
	if err != nil {
		t.Fatalf("execute float sub: %v", err)
	}

	if len(chunks) > 0 && chunks[0].RowCount() > 0 {
		col, _ := chunks[0].GetColumn(0)
		val := col.GetValue(0)
		if val.Float64 != 90.0 {
			t.Errorf("expected 95.5-5.5=90.0, got %g", val.Float64)
		}
	}
}

func TestExecutorArithmeticFloatMul(t *testing.T) {
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
			&BinaryExpr{Op: OpMul, Left: &ResolvedColumnExpr{Name: testColScore, Idx: 3, typ: common.TypeFloat64}, Right: &LiteralExpr{Value: common.NewFloat64(2.0)}},
		},
		Aliases: []string{"double_score"},
		schema: []ColumnDef{
			{Name: "double_score", Type: common.TypeFloat64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(project)
	if err != nil {
		t.Fatalf("execute float mul: %v", err)
	}

	if len(chunks) > 0 && chunks[0].RowCount() > 0 {
		col, _ := chunks[0].GetColumn(0)
		val := col.GetValue(0)
		if val.Float64 != 191.0 {
			t.Errorf("expected 95.5*2=191.0, got %g", val.Float64)
		}
	}
}

func TestExecutorArithmeticFloatDiv(t *testing.T) {
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
			&BinaryExpr{Op: OpDiv, Left: &ResolvedColumnExpr{Name: testColScore, Idx: 3, typ: common.TypeFloat64}, Right: &LiteralExpr{Value: common.NewFloat64(2.0)}},
		},
		Aliases: []string{"half_score"},
		schema: []ColumnDef{
			{Name: "half_score", Type: common.TypeFloat64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(project)
	if err != nil {
		t.Fatalf("execute float div: %v", err)
	}

	if len(chunks) > 0 && chunks[0].RowCount() > 0 {
		col, _ := chunks[0].GetColumn(0)
		val := col.GetValue(0)
		if val.Float64 != 47.75 {
			t.Errorf("expected 95.5/2=47.75, got %g", val.Float64)
		}
	}
}

func TestExecutorArithmeticFloatDivByZero(t *testing.T) {
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
			&BinaryExpr{Op: OpDiv, Left: &ResolvedColumnExpr{Name: testColScore, Idx: 3, typ: common.TypeFloat64}, Right: &LiteralExpr{Value: common.NewFloat64(0.0)}},
		},
		Aliases: []string{testAliasDivZero},
		schema: []ColumnDef{
			{Name: testAliasDivZero, Type: common.TypeFloat64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(project)
	if err != nil {
		t.Fatalf("execute float div by zero: %v", err)
	}

	if len(chunks) > 0 && chunks[0].RowCount() > 0 {
		col, _ := chunks[0].GetColumn(0)
		val := col.GetValue(0)
		if val.Valid {
			t.Errorf("expected NULL for float division by zero, got %v", val)
		}
	}
}

func TestExecutorUnaryNeg(t *testing.T) {
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
			&UnaryExpr{Op: OpNeg, Expr: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}},
		},
		Aliases: []string{testAliasNegAge},
		schema: []ColumnDef{
			{Name: testAliasNegAge, Type: common.TypeInt64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(project)
	if err != nil {
		t.Fatalf("execute unary neg: %v", err)
	}

	if len(chunks) > 0 && chunks[0].RowCount() > 0 {
		col, _ := chunks[0].GetColumn(0)
		val := col.GetValue(0)
		if val.Int64 != -30 {
			t.Errorf("expected -30, got %d", val.Int64)
		}
	}
}

func TestExecutorUnaryNegFloat(t *testing.T) {
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
			&UnaryExpr{Op: OpNeg, Expr: &ResolvedColumnExpr{Name: testColScore, Idx: 3, typ: common.TypeFloat64}},
		},
		Aliases: []string{"neg_score"},
		schema: []ColumnDef{
			{Name: "neg_score", Type: common.TypeFloat64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(project)
	if err != nil {
		t.Fatalf("execute unary neg float: %v", err)
	}

	if len(chunks) > 0 && chunks[0].RowCount() > 0 {
		col, _ := chunks[0].GetColumn(0)
		val := col.GetValue(0)
		if val.Float64 != -95.5 {
			t.Errorf("expected -95.5, got %g", val.Float64)
		}
	}
}

func TestExecutorUnaryNegNull(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewNull(), testColScore: common.NewFloat64(95.5),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	project := &ProjectNode{
		Child: scan,
		Expressions: []Expression{
			&UnaryExpr{Op: OpNeg, Expr: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}},
		},
		Aliases: []string{testAliasNegAge},
		schema: []ColumnDef{
			{Name: testAliasNegAge, Type: common.TypeInt64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(project)
	if err != nil {
		t.Fatalf("execute unary neg null: %v", err)
	}

	if len(chunks) > 0 && chunks[0].RowCount() > 0 {
		col, _ := chunks[0].GetColumn(0)
		val := col.GetValue(0)
		if val.Valid {
			t.Errorf("expected NULL for negation of NULL, got %v", val)
		}
	}
}

// TestToFloat64 测试 toFloat64 辅助函数。
func TestToFloat64(t *testing.T) {
	tests := []struct {
		name string
		val  common.Value
		want float64
	}{
		{"float64值直接返回", common.NewFloat64(3.14), 3.14},
		{"int64值转换为float64", common.NewInt64(42), 42.0},
		{"其他类型返回0", common.NewString("hello"), 0},
		{"null类型返回0", common.NewNull(), 0},
		{"bool类型返回0", common.NewBool(true), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toFloat64(tt.val); got != tt.want {
				t.Errorf("toFloat64(%v) = %v, want %v", tt.val, got, tt.want)
			}
		})
	}
}
