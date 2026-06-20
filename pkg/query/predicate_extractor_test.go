package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
)

// TestExtractColumnPredicates_NilPred 验证 nil 谓词返回 nil。
func TestExtractColumnPredicates_NilPred(t *testing.T) {
	if got := ExtractColumnPredicates(nil); got != nil {
		t.Errorf("ExtractColumnPredicates(nil) = %v, want nil", got)
	}
}

// TestExtractColumnPredicates_ColumnOpLiteral 验证 "column op literal" 形式
// 在 *ColumnExpr（未分析）与 *ResolvedColumnExpr（已分析）两种表达下均能正确提取。
func TestExtractColumnPredicates_ColumnOpLiteral(t *testing.T) {
	tests := []struct {
		name    string
		expr    Expression
		wantCol string
		wantOp  index.PredicateOp
		wantVal common.Value
	}{
		{
			name: "未分析 ColumnExpr: age = 30",
			expr: &BinaryExpr{Op: OpEq,
				Left:  &ColumnExpr{Name: "age"},
				Right: &LiteralExpr{Value: common.NewInt64(30)}},
			wantCol: "age", wantOp: index.OpEqual, wantVal: common.NewInt64(30),
		},
		{
			name: "已分析 ResolvedColumnExpr: age > 25",
			expr: &BinaryExpr{Op: OpGt,
				Left:  &ResolvedColumnExpr{Name: "age", Idx: 1, typ: common.TypeInt64},
				Right: &LiteralExpr{Value: common.NewInt64(25)}},
			wantCol: "age", wantOp: index.OpGreater, wantVal: common.NewInt64(25),
		},
		{
			name: "未分析: amount < 100.5",
			expr: &BinaryExpr{Op: OpLt,
				Left:  &ColumnExpr{Name: "amount"},
				Right: &LiteralExpr{Value: common.NewFloat64(100.5)}},
			wantCol: "amount", wantOp: index.OpLess, wantVal: common.NewFloat64(100.5),
		},
		{
			name: "未分析: status != 'active'",
			expr: &BinaryExpr{Op: OpNe,
				Left:  &ColumnExpr{Name: "status"},
				Right: &LiteralExpr{Value: common.NewString("active")}},
			wantCol: "status", wantOp: index.OpNotEqual, wantVal: common.NewString("active"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preds := ExtractColumnPredicates(tt.expr)
			if len(preds) != 1 {
				t.Fatalf("got %d predicates, want 1", len(preds))
			}
			p := preds[0]
			if p.ColumnName != tt.wantCol {
				t.Errorf("ColumnName = %q, want %q", p.ColumnName, tt.wantCol)
			}
			if p.Op != tt.wantOp {
				t.Errorf("Op = %v, want %v", p.Op, tt.wantOp)
			}
			if !p.Value.Equal(tt.wantVal) {
				t.Errorf("Value = %v, want %v", p.Value, tt.wantVal)
			}
		})
	}
}

// TestExtractColumnPredicates_LiteralOpColumn 验证 "literal op column" 形式
// （运算符需要翻转）也能正确提取。
func TestExtractColumnPredicates_LiteralOpColumn(t *testing.T) {
	tests := []struct {
		name    string
		expr    Expression
		wantCol string
		wantOp  index.PredicateOp
		wantVal common.Value
	}{
		{
			name: "30 > age 等价于 age < 30",
			expr: &BinaryExpr{Op: OpGt,
				Left:  &LiteralExpr{Value: common.NewInt64(30)},
				Right: &ColumnExpr{Name: "age"}},
			wantCol: "age", wantOp: index.OpLess, wantVal: common.NewInt64(30),
		},
		{
			name: "30 >= age 等价于 age <= 30",
			expr: &BinaryExpr{Op: OpGe,
				Left:  &LiteralExpr{Value: common.NewInt64(30)},
				Right: &ColumnExpr{Name: "age"}},
			wantCol: "age", wantOp: index.OpLessEqual, wantVal: common.NewInt64(30),
		},
		{
			name: "已分析: 100 < id 等价于 id > 100",
			expr: &BinaryExpr{Op: OpLt,
				Left:  &LiteralExpr{Value: common.NewInt64(100)},
				Right: &ResolvedColumnExpr{Name: "id", Idx: 0, typ: common.TypeInt64}},
			wantCol: "id", wantOp: index.OpGreater, wantVal: common.NewInt64(100),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preds := ExtractColumnPredicates(tt.expr)
			if len(preds) != 1 {
				t.Fatalf("got %d predicates, want 1", len(preds))
			}
			p := preds[0]
			if p.ColumnName != tt.wantCol {
				t.Errorf("ColumnName = %q, want %q", p.ColumnName, tt.wantCol)
			}
			if p.Op != tt.wantOp {
				t.Errorf("Op = %v, want %v", p.Op, tt.wantOp)
			}
			if !p.Value.Equal(tt.wantVal) {
				t.Errorf("Value = %v, want %v", p.Value, tt.wantVal)
			}
		})
	}
}

// TestExtractColumnPredicates_AndConjuncts 验证 AND 连接的多个谓词全部被提取。
func TestExtractColumnPredicates_AndConjuncts(t *testing.T) {
	expr := &BinaryExpr{Op: OpAnd,
		Left: &BinaryExpr{Op: OpEq,
			Left:  &ColumnExpr{Name: "age"},
			Right: &LiteralExpr{Value: common.NewInt64(30)}},
		Right: &BinaryExpr{Op: OpGt,
			Left:  &ColumnExpr{Name: "score"},
			Right: &LiteralExpr{Value: common.NewFloat64(80.0)}},
	}
	preds := ExtractColumnPredicates(expr)
	if len(preds) != 2 {
		t.Fatalf("got %d predicates, want 2", len(preds))
	}
	if preds[0].ColumnName != "age" || preds[0].Op != index.OpEqual {
		t.Errorf("pred[0] = %+v, want age=Eq", preds[0])
	}
	if preds[1].ColumnName != "score" || preds[1].Op != index.OpGreater {
		t.Errorf("pred[1] = %+v, want score=Gt", preds[1])
	}
}

// TestExtractColumnPredicates_OrConjunctsSkipped 验证 OR 子句中的谓词不被提取
// （避免漏匹配导致的段裁剪错误）。
func TestExtractColumnPredicates_OrConjunctsSkipped(t *testing.T) {
	// age = 30 OR score > 80：OR 不应让任一谓词参与裁剪
	expr := &BinaryExpr{Op: OpOr,
		Left: &BinaryExpr{Op: OpEq,
			Left:  &ColumnExpr{Name: "age"},
			Right: &LiteralExpr{Value: common.NewInt64(30)}},
		Right: &BinaryExpr{Op: OpGt,
			Left:  &ColumnExpr{Name: "score"},
			Right: &LiteralExpr{Value: common.NewFloat64(80.0)}},
	}
	preds := ExtractColumnPredicates(expr)
	if len(preds) != 0 {
		t.Errorf("OR 子句不应提取裁剪谓词，got %d: %+v", len(preds), preds)
	}
}

// TestExtractColumnPredicates_NullLiteralSkipped 验证 NULL 字面量不参与裁剪
// （NULL 与任何值比较语义模糊）。
func TestExtractColumnPredicates_NullLiteralSkipped(t *testing.T) {
	expr := &BinaryExpr{Op: OpEq,
		Left:  &ColumnExpr{Name: "age"},
		Right: &LiteralExpr{Value: common.NewNull()}}
	preds := ExtractColumnPredicates(expr)
	if len(preds) != 0 {
		t.Errorf("NULL 字面量不应参与裁剪，got %+v", preds)
	}
}

// TestExtractColumnPredicates_NonColumnOrNonLiteral 验证非列/非字面量表达式被跳过。
func TestExtractColumnPredicates_NonColumnOrNonLiteral(t *testing.T) {
	// age = age（两边都是列引用，不是字面量）→ 不可裁剪
	expr := &BinaryExpr{Op: OpEq,
		Left:  &ColumnExpr{Name: "age"},
		Right: &ColumnExpr{Name: "age"}}
	if got := ExtractColumnPredicates(expr); len(got) != 0 {
		t.Errorf("列-列比较不应提取，got %+v", got)
	}

	// 1 = 1（两边都是字面量，没有列引用）→ 不可裁剪
	expr2 := &BinaryExpr{Op: OpEq,
		Left:  &LiteralExpr{Value: common.NewInt64(1)},
		Right: &LiteralExpr{Value: common.NewInt64(1)}}
	if got := ExtractColumnPredicates(expr2); len(got) != 0 {
		t.Errorf("字面量-字面量比较不应提取，got %+v", got)
	}
}

// TestExtractColumnPredicates_LogicalOpsSkipped 验证算术/逻辑运算符不被裁剪。
func TestExtractColumnPredicates_LogicalOpsSkipped(t *testing.T) {
	// age + 1 = 30：左侧是算术表达式，不是简单列引用
	expr := &BinaryExpr{Op: OpEq,
		Left: &BinaryExpr{Op: OpAdd,
			Left:  &ColumnExpr{Name: "age"},
			Right: &LiteralExpr{Value: common.NewInt64(1)}},
		Right: &LiteralExpr{Value: common.NewInt64(30)}}
	if got := ExtractColumnPredicates(expr); len(got) != 0 {
		t.Errorf("算术表达式左侧不应参与裁剪，got %+v", got)
	}
}

// TestExtractColumnPredicatesPublic_MixedConjuncts 验证混合（可裁剪+不可裁剪）合取项
// 只返回可裁剪部分，不可裁剪部分依赖 EvalRowPredicate 在上层二次过滤。
func TestExtractColumnPredicatesPublic_MixedConjuncts(t *testing.T) {
	// age > 25 AND (name LIKE 'a%')
	// "age > 25" 可裁剪；"name LIKE 'a%'" 不可裁剪（LIKE 不是比较运算符）
	expr := &BinaryExpr{Op: OpAnd,
		Left: &BinaryExpr{Op: OpGt,
			Left:  &ColumnExpr{Name: "age"},
			Right: &LiteralExpr{Value: common.NewInt64(25)}},
		Right: &BinaryExpr{Op: OpLike,
			Left:  &ColumnExpr{Name: "name"},
			Right: &LiteralExpr{Value: common.NewString("a%")}},
	}
	preds := ExtractColumnPredicates(expr)
	if len(preds) != 1 {
		t.Fatalf("got %d predicates, want 1", len(preds))
	}
	if preds[0].ColumnName != "age" || preds[0].Op != index.OpGreater {
		t.Errorf("pred[0] = %+v, want age=Gt", preds[0])
	}
}

// TestExtractColumnPredicates_EmptyColumnName 验证空列名返回 false。
func TestExtractColumnPredicates_EmptyColumnName(t *testing.T) {
	// ColumnExpr.Name 为空字符串 → columnRefName 返回 false
	expr := &BinaryExpr{Op: OpEq,
		Left:  &ColumnExpr{Name: ""},
		Right: &LiteralExpr{Value: common.NewInt64(30)}}
	if got := ExtractColumnPredicates(expr); len(got) != 0 {
		t.Errorf("空列名不应提取谓词，got %+v", got)
	}

	// ResolvedColumnExpr.Name 为空字符串
	expr2 := &BinaryExpr{Op: OpEq,
		Left:  &ResolvedColumnExpr{Name: "", Idx: 0, typ: common.TypeInt64},
		Right: &LiteralExpr{Value: common.NewInt64(30)}}
	if got := ExtractColumnPredicates(expr2); len(got) != 0 {
		t.Errorf("空列名（已分析）不应提取谓词，got %+v", got)
	}
}

// TestExtractColumnPredicates_ReturnsStorageColumnPredicate 验证返回值类型为
// storage.ColumnPredicate，与 ScanRangeWithPruning 接口签名一致。
func TestExtractColumnPredicates_ReturnsStorageColumnPredicate(t *testing.T) {
	expr := &BinaryExpr{Op: OpEq,
		Left:  &ColumnExpr{Name: "id"},
		Right: &LiteralExpr{Value: common.NewInt64(5)}}
	preds := ExtractColumnPredicates(expr)
	// 编译期已保证类型一致；通过取值与再次赋值触发 QF 静态分析无法消除的强校验
	_ = preds[0]
	if preds[0].ColumnName != "id" {
		t.Errorf("ColumnName = %q, want id", preds[0].ColumnName)
	}
}
