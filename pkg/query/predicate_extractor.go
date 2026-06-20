package query

import (
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// ExtractColumnPredicates 从 WHERE 谓词中提取所有形如 "column op literal" 的简单比较，
// 转换为可下推到存储引擎段裁剪的 ColumnPredicate 列表。
//
// 转换规则：
//   - 仅处理 AND 连接（splitConjuncts 拆分），OR 子句不参与（避免漏匹配）
//   - 接受 column op literal 与 literal op column 两种形式，后者翻转运算符后参与裁剪
//   - 接受的运算符：=、!=、<、<=、>、>=
//   - 列引用既支持已分析的 *ResolvedColumnExpr（SELECT 路径使用），
//     也支持未分析的 *ColumnExpr（DELETE/UPDATE 路径使用，绕过 analyzer）
//   - NULL 字面量不参与裁剪（语义模糊，避免误裁）
//   - 复杂表达式（OR、嵌套、函数调用等）自动跳过，保证裁剪安全性
//
// 该函数是公开 API，供 server 层 DELETE/UPDATE 处理器使用，对未分析的 WHERE
// 表达式也能正确提取段裁剪谓词。SELECT 路径已通过 *Executor.extractColumnPredicates
// 内部委托到本函数，避免重复实现。
func ExtractColumnPredicates(pred Expression) []storage.ColumnPredicate {
	if pred == nil {
		return nil
	}
	conjuncts := splitConjuncts(pred)
	if len(conjuncts) == 0 {
		return nil
	}
	preds := make([]storage.ColumnPredicate, 0, len(conjuncts))
	for _, c := range conjuncts {
		bin, ok := c.(*BinaryExpr)
		if !ok {
			continue
		}
		colPred, ok := columnBinaryToPredicate(bin)
		if !ok {
			continue
		}
		preds = append(preds, colPred)
	}
	return preds
}

// columnBinaryToPredicate 将形如 "column op literal" 或 "literal op column" 的
// 二元表达式转换为 ColumnPredicate。非比较运算符、非列引用、NULL 字面量均返回 false。
// 接受 *ResolvedColumnExpr 与 *ColumnExpr 两种列引用类型，使本函数既能在 SELECT
// 路径（已分析）使用，也能在 DELETE/UPDATE 路径（未分析）使用。
func columnBinaryToPredicate(bin *BinaryExpr) (storage.ColumnPredicate, bool) {
	if !isComparisonOp(bin.Op) {
		return storage.ColumnPredicate{}, false
	}

	// 形如 "column op literal"
	if colName, ok := columnRefName(bin.Left); ok {
		if lit, isLit := bin.Right.(*LiteralExpr); isLit && lit.Value.Valid {
			op, mapped := opToIndexOp[bin.Op]
			if !mapped {
				return storage.ColumnPredicate{}, false
			}
			return storage.ColumnPredicate{ColumnName: colName, Op: op, Value: lit.Value}, true
		}
	}

	// 形如 "literal op column"，需翻转运算符
	if colName, ok := columnRefName(bin.Right); ok {
		if lit, isLit := bin.Left.(*LiteralExpr); isLit && lit.Value.Valid {
			flipped, ok := flipComparisonOp(bin.Op)
			if !ok {
				return storage.ColumnPredicate{}, false
			}
			op, mapped := opToIndexOp[flipped]
			if !mapped {
				return storage.ColumnPredicate{}, false
			}
			return storage.ColumnPredicate{ColumnName: colName, Op: op, Value: lit.Value}, true
		}
	}

	return storage.ColumnPredicate{}, false
}

// columnRefName 从表达式中提取列名。接受 *ColumnExpr（未分析）和
// *ResolvedColumnExpr（已分析）两种类型；其他类型返回 ("", false)。
func columnRefName(expr Expression) (string, bool) {
	switch v := expr.(type) {
	case *ColumnExpr:
		if v.Name == "" {
			return "", false
		}
		return v.Name, true
	case *ResolvedColumnExpr:
		if v.Name == "" {
			return "", false
		}
		return v.Name, true
	}
	return "", false
}
