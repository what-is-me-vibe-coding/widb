package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestExecuteAggregate_CountOnData tests COUNT aggregate with data.
func TestExecuteAggregate_CountOnData(t *testing.T) {
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

	agg := &AggregateNode{
		Child:   scan,
		GroupBy: nil,
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
		},
		schema: []ColumnDef{
			{Name: testAggCountStar, Type: common.TypeInt64, Nullable: false},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(agg)
	if err != nil {
		t.Fatalf("executeAggregate COUNT: %v", err)
	}

	if len(chunks) == 0 || chunks[0].RowCount() == 0 {
		t.Fatal("expected at least 1 row")
	}

	col, _ := chunks[0].GetColumn(0)
	val := col.GetValue(0)
	if val.Int64 != 3 {
		t.Errorf("expected COUNT(*) = 3, got %d", val.Int64)
	}
}

// TestExecuteAggregate_SumOnData tests SUM aggregate with data.
func TestExecuteAggregate_SumOnData(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(90.0),
	})
	ms.addEntry("b", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
		testColAge: common.NewInt64(20), testColScore: common.NewFloat64(80.0),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	agg := &AggregateNode{
		Child:   scan,
		GroupBy: nil,
		Aggregates: []AggregateExpr{
			{Func: AggSum, Arg: &ResolvedColumnExpr{Name: testColScore, Idx: 3, typ: common.TypeFloat64}},
		},
		schema: []ColumnDef{
			{Name: testAggSumScore, Type: common.TypeFloat64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(agg)
	if err != nil {
		t.Fatalf("executeAggregate SUM: %v", err)
	}

	if len(chunks) == 0 || chunks[0].RowCount() == 0 {
		t.Fatal("expected at least 1 row")
	}

	col, _ := chunks[0].GetColumn(0)
	val := col.GetValue(0)
	if val.Float64 != 170.0 {
		t.Errorf("expected SUM(score) = 170.0, got %g", val.Float64)
	}
}

// TestExecuteAggregate_AvgOnData tests AVG aggregate with data.
func TestExecuteAggregate_AvgOnData(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(90.0),
	})
	ms.addEntry("b", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewString(testNameBob),
		testColAge: common.NewInt64(20), testColScore: common.NewFloat64(70.0),
	})
	ms.addEntry("c", map[string]common.Value{
		testColID: common.NewInt64(3), testColName: common.NewString(testNameCharlie),
		testColAge: common.NewInt64(25), testColScore: common.NewFloat64(80.0),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	agg := &AggregateNode{
		Child:   scan,
		GroupBy: nil,
		Aggregates: []AggregateExpr{
			{Func: AggAvg, Arg: &ResolvedColumnExpr{Name: testColScore, Idx: 3, typ: common.TypeFloat64}},
		},
		schema: []ColumnDef{
			{Name: testAggAvgScore, Type: common.TypeFloat64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(agg)
	if err != nil {
		t.Fatalf("executeAggregate AVG: %v", err)
	}

	if len(chunks) == 0 || chunks[0].RowCount() == 0 {
		t.Fatal("expected at least 1 row")
	}

	col, _ := chunks[0].GetColumn(0)
	val := col.GetValue(0)
	expected := (90.0 + 70.0 + 80.0) / 3.0
	if val.Float64 != expected {
		t.Errorf("expected AVG(score) = %g, got %g", expected, val.Float64)
	}
}

// TestExecuteAggregate_GroupByMultipleGroups tests GROUP BY with multiple groups.
func TestExecuteAggregate_GroupByMultipleGroups(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})
	ms.addEntry("d", map[string]common.Value{
		testColID: common.NewInt64(4), testColName: common.NewString(testNameDiana),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(85.0),
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

	agg := &AggregateNode{
		Child: scan,
		GroupBy: []Expression{
			&ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64},
		},
		Aggregates: []AggregateExpr{
			{Func: AggCount, Arg: &StarExpr{}},
			{Func: AggAvg, Arg: &ResolvedColumnExpr{Name: testColScore, Idx: 3, typ: common.TypeFloat64}},
		},
		schema: []ColumnDef{
			{Name: testColAge, Type: common.TypeInt64, Nullable: true},
			{Name: testAggCountStar, Type: common.TypeInt64, Nullable: false},
			{Name: testAggAvgScore, Type: common.TypeFloat64, Nullable: true},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(agg)
	if err != nil {
		t.Fatalf("executeAggregate GROUP BY multiple groups: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 3 {
		t.Errorf("expected 3 groups, got %d", totalRows)
	}

	// Find the group with age=30 (should have count=2)
	foundGroup30 := false
	for row := uint32(0); row < chunks[0].RowCount(); row++ {
		ageCol, _ := chunks[0].GetColumn(0)
		countCol, _ := chunks[0].GetColumn(1)
		ageVal := ageCol.GetValue(row)
		countVal := countCol.GetValue(row)
		if ageVal.Int64 == 30 {
			foundGroup30 = true
			if countVal.Int64 != 2 {
				t.Errorf("group age=30: expected COUNT=2, got %d", countVal.Int64)
			}
		}
	}
	if !foundGroup30 {
		t.Error("expected group with age=30")
	}
}
