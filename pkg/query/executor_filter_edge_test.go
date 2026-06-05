package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestFilterAllPass 测试所有行都通过过滤条件
func TestFilterAllPass(t *testing.T) {
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

	// age > 0，所有行都通过
	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(0)}},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("execute filter: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 3 {
		t.Errorf("期望 3 行（全部通过），得到 %d", totalRows)
	}
}

// TestFilterAllFail 测试所有行都不通过过滤条件
func TestFilterAllFail(t *testing.T) {
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

	// age > 1000，所有行都不通过
	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(1000)}},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("execute filter: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 0 {
		t.Errorf("期望 0 行（全部不通过），得到 %d", totalRows)
	}
}

// TestFilterNullColumn 测试过滤 NULL 列值
func TestFilterNullColumn(t *testing.T) {
	ms := newMockStorage()
	// 所有列都是 NULL
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewNull(), testColName: common.NewNull(),
		testColAge: common.NewNull(), testColScore: common.NewNull(),
	})
	// 部分列是 NULL
	ms.addEntry("b", map[string]common.Value{
		testColID: common.NewInt64(2), testColName: common.NewNull(),
		testColAge: common.NewInt64(25), testColScore: common.NewNull(),
	})
	// 没有 NULL
	ms.addEntry("c", map[string]common.Value{
		testColID: common.NewInt64(3), testColName: common.NewString(testNameCharlie),
		testColAge: common.NewInt64(35), testColScore: common.NewFloat64(72.0),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	// age > 20，NULL age 不匹配
	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("execute filter null: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 2 {
		t.Errorf("期望 2 行（NULL age 不匹配），得到 %d", totalRows)
	}
}

// TestFilterNullInAndCondition 测试 AND 条件中 NULL 值的处理
func TestFilterNullInAndCondition(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewNull(),
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

	// age > 20 AND name = 'bob'，NULL name 导致 AND 左侧为 true 但右侧 NULL 比较为 NULL
	filter := &FilterNode{
		Child: scan,
		Condition: &BinaryExpr{
			Op:    OpAnd,
			Left:  &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
			Right: &BinaryExpr{Op: OpEq, Left: &ResolvedColumnExpr{Name: testColName, Idx: 1, typ: common.TypeString}, Right: &LiteralExpr{Value: common.NewString(testNameBob)}},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("execute filter AND null: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 1 {
		t.Errorf("期望 1 行（只有 bob 匹配），得到 %d", totalRows)
	}
}

// TestFilterNullInOrCondition 测试 OR 条件中 NULL 值的处理
func TestFilterNullInOrCondition(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewNull(),
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

	// name = 'alice' OR age > 20，NULL name 比较为 NULL，但 age > 20 为 true
	filter := &FilterNode{
		Child: scan,
		Condition: &BinaryExpr{
			Op:    OpOr,
			Left:  &BinaryExpr{Op: OpEq, Left: &ResolvedColumnExpr{Name: testColName, Idx: 1, typ: common.TypeString}, Right: &LiteralExpr{Value: common.NewString(testNameAlice)}},
			Right: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(20)}},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("execute filter OR null: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 2 {
		t.Errorf("期望 2 行（age > 20 都通过 OR），得到 %d", totalRows)
	}
}

// TestFilterMultipleAndConditions 测试多个 AND 条件
func TestFilterMultipleAndConditions(t *testing.T) {
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
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(72.0),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	// age = 30 AND score > 80，只有 alice 通过
	filter := &FilterNode{
		Child: scan,
		Condition: &BinaryExpr{
			Op:    OpAnd,
			Left:  &BinaryExpr{Op: OpEq, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(30)}},
			Right: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColScore, Idx: 3, typ: common.TypeFloat64}, Right: &LiteralExpr{Value: common.NewFloat64(80.0)}},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("execute filter multiple AND: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 1 {
		t.Errorf("期望 1 行（age=30 AND score>80），得到 %d", totalRows)
	}
}

// TestFilterMultipleOrConditions 测试多个 OR 条件
func TestFilterMultipleOrConditions(t *testing.T) {
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

	// age = 25 OR age = 35，bob 和 charlie 通过
	filter := &FilterNode{
		Child: scan,
		Condition: &BinaryExpr{
			Op:    OpOr,
			Left:  &BinaryExpr{Op: OpEq, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(25)}},
			Right: &BinaryExpr{Op: OpEq, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(35)}},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("execute filter multiple OR: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 2 {
		t.Errorf("期望 2 行（age=25 OR age=35），得到 %d", totalRows)
	}
}

// TestFilterNotWithNull 测试 NOT 条件与 NULL 值的组合
func TestFilterNotWithNull(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewNull(),
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

	// NOT (name = 'bob')，NULL name 比较为 NULL（falsy），NOT NULL = true
	filter := &FilterNode{
		Child: scan,
		Condition: &UnaryExpr{
			Op:   OpNot,
			Expr: &BinaryExpr{Op: OpEq, Left: &ResolvedColumnExpr{Name: testColName, Idx: 1, typ: common.TypeString}, Right: &LiteralExpr{Value: common.NewString(testNameBob)}},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("execute filter NOT null: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 1 {
		t.Errorf("期望 1 行（NOT (name='bob')），得到 %d", totalRows)
	}
}

// TestFilterWithFloatComparison 测试浮点数比较过滤
func TestFilterWithFloatComparison(t *testing.T) {
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

	// score > 80.0
	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColScore, Idx: 3, typ: common.TypeFloat64}, Right: &LiteralExpr{Value: common.NewFloat64(80.0)}},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("execute filter float: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 2 {
		t.Errorf("期望 2 行（score > 80.0），得到 %d", totalRows)
	}
}

// TestFilterWithLessThanOrEqual 测试小于等于比较
func TestFilterWithLessThanOrEqual(t *testing.T) {
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

	// age <= 25
	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpLe, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(25)}},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("execute filter LE: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 1 {
		t.Errorf("期望 1 行（age <= 25），得到 %d", totalRows)
	}
}

// TestFilterWithGreaterThanOrEqual 测试大于等于比较
func TestFilterWithGreaterThanOrEqual(t *testing.T) {
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

	// age >= 30
	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGe, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(30)}},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("execute filter GE: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 1 {
		t.Errorf("期望 1 行（age >= 30），得到 %d", totalRows)
	}
}

// TestFilterWithLessThan 测试小于比较
func TestFilterWithLessThan(t *testing.T) {
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

	// age < 30
	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpLt, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(30)}},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("execute filter LT: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 1 {
		t.Errorf("期望 1 行（age < 30），得到 %d", totalRows)
	}
}

// TestFilterWithNestedAndOr 测试嵌套 AND/OR 条件
func TestFilterWithNestedAndOr(t *testing.T) {
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
		testColAge: common.NewInt64(28), testColScore: common.NewFloat64(91.0),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	// (age > 25 AND score > 90) OR (age < 26)
	// alice: age=30>25 AND score=95.5>90 -> true
	// bob: age=25 NOT >25, age=25 NOT <26 -> false (age < 26 means age < 26, 25 < 26 is true)
	// charlie: age=35>25 AND score=72 NOT >90 -> false
	// diana: age=28>25 AND score=91>90 -> true
	filter := &FilterNode{
		Child: scan,
		Condition: &BinaryExpr{
			Op: OpOr,
			Left: &BinaryExpr{
				Op:    OpAnd,
				Left:  &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(25)}},
				Right: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColScore, Idx: 3, typ: common.TypeFloat64}, Right: &LiteralExpr{Value: common.NewFloat64(90.0)}},
			},
			Right: &BinaryExpr{Op: OpLt, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(26)}},
		},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("execute filter nested AND/OR: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 3 {
		t.Errorf("期望 3 行（alice, bob, diana），得到 %d", totalRows)
	}
}

// TestFilterEmptyInput 测试过滤空输入
func TestFilterEmptyInput(t *testing.T) {
	ms := newMockStorage()
	// 不添加任何数据

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpGt, Left: &ResolvedColumnExpr{Name: testColAge, Idx: 2, typ: common.TypeInt64}, Right: &LiteralExpr{Value: common.NewInt64(0)}},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("execute filter empty: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 0 {
		t.Errorf("期望 0 行（空输入），得到 %d", totalRows)
	}
}

// TestFilterStringEquality 测试字符串相等过滤
func TestFilterStringEquality(t *testing.T) {
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

	// name = 'alice'
	filter := &FilterNode{
		Child:     scan,
		Condition: &BinaryExpr{Op: OpEq, Left: &ResolvedColumnExpr{Name: testColName, Idx: 1, typ: common.TypeString}, Right: &LiteralExpr{Value: common.NewString(testNameAlice)}},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("execute filter string eq: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 1 {
		t.Errorf("期望 1 行（name='alice'），得到 %d", totalRows)
	}
}

// TestFilterBoolColumn 测试布尔列作为过滤条件
func TestFilterBoolColumn(t *testing.T) {
	ms := newMockStorage()
	ms.addEntry("a", map[string]common.Value{
		testColID: common.NewInt64(1), testColName: common.NewString(testNameAlice),
		testColAge: common.NewInt64(30), testColScore: common.NewFloat64(95.5),
	})
	ms.addEntry("b", map[string]common.Value{
		testColID: common.NewInt64(0), testColName: common.NewString("zero"),
		testColAge: common.NewInt64(0), testColScore: common.NewFloat64(0),
	})

	scan := &ScanNode{
		Table:   testTableUsers,
		Columns: []string{testColID, testColName, testColAge, testColScore},
		schema:  buildTestSchema(),
	}

	// 使用 id 列作为布尔条件（非零为 true）
	filter := &FilterNode{
		Child:     scan,
		Condition: &ResolvedColumnExpr{Name: testColID, Idx: 0, typ: common.TypeInt64},
	}

	exec := NewExecutor(ms)
	chunks, err := exec.Execute(filter)
	if err != nil {
		t.Fatalf("execute filter bool column: %v", err)
	}

	totalRows := countRows(chunks)
	if totalRows != 1 {
		t.Errorf("期望 1 行（id != 0），得到 %d", totalRows)
	}
}
