package query

import (
	"fmt"

	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
)

// resolveExpr 解析表达式中的列引用，将其绑定到 table 的 schema。
// 当 table 为 nil 时表示无表上下文（用于 CHECK 约束等独立表达式），
// 此时遇到 *ColumnExpr 或 *StarExpr 将返回错误。
func (a *Analyzer) resolveExpr(expr Expression, table *catalog.Table) (Expression, error) {
	switch e := expr.(type) {
	case *ColumnExpr:
		if table == nil {
			return nil, fmt.Errorf("analyzer: column reference %q without table context", e.Name)
		}
		return a.resolveColumnRef(e, table)
	case *LiteralExpr:
		return e, nil
	case *StarExpr:
		if table == nil {
			return nil, fmt.Errorf("analyzer: unsupported expression type %T", expr)
		}
		return e, nil
	case *BinaryExpr:
		return a.resolveBinaryExpr(e, table)
	case *UnaryExpr:
		return a.resolveUnaryExpr(e, table)
	case *FuncExpr:
		return a.resolveFuncExpr(e, table)
	default:
		return nil, fmt.Errorf("analyzer: unsupported expression type %T", expr)
	}
}

// resolveExprNoTable 在无表上下文中解析表达式（CHECK 约束等）。
// 等价于 resolveExpr(expr, nil)，保留独立函数名以兼容既有调用点。
func (a *Analyzer) resolveExprNoTable(expr Expression) (Expression, error) {
	return a.resolveExpr(expr, nil)
}

func (a *Analyzer) resolveColumnRef(col *ColumnExpr, table *catalog.Table) (*ResolvedColumnExpr, error) {
	idx, err := table.ColumnIndex(col.Name)
	if err != nil {
		return nil, fmt.Errorf("analyzer: column %q does not exist in table %q", col.Name, table.Name)
	}
	tc := table.Columns[idx]
	return &ResolvedColumnExpr{
		Name: col.Name,
		Idx:  idx,
		typ:  tc.Type,
	}, nil
}

// resolveBinaryExpr 解析二元表达式的左右子表达式。table 可为 nil（无表上下文）。
func (a *Analyzer) resolveBinaryExpr(e *BinaryExpr, table *catalog.Table) (*BinaryExpr, error) {
	left, err := a.resolveExpr(e.Left, table)
	if err != nil {
		return nil, err
	}
	right, err := a.resolveExpr(e.Right, table)
	if err != nil {
		return nil, err
	}
	return &BinaryExpr{Op: e.Op, Left: left, Right: right}, nil
}

// resolveBinaryExprNoTable 在无表上下文中解析二元表达式。
// 等价于 resolveBinaryExpr(e, nil)，保留独立函数名以兼容既有调用点。
func (a *Analyzer) resolveBinaryExprNoTable(e *BinaryExpr) (*BinaryExpr, error) {
	return a.resolveBinaryExpr(e, nil)
}

// resolveUnaryExpr 解析一元表达式的内部表达式。table 可为 nil（无表上下文）。
func (a *Analyzer) resolveUnaryExpr(e *UnaryExpr, table *catalog.Table) (*UnaryExpr, error) {
	inner, err := a.resolveExpr(e.Expr, table)
	if err != nil {
		return nil, err
	}
	return &UnaryExpr{Op: e.Op, Expr: inner}, nil
}

// resolveFuncExpr 解析函数表达式的参数。table 可为 nil（无表上下文）。
func (a *Analyzer) resolveFuncExpr(e *FuncExpr, table *catalog.Table) (*FuncExpr, error) {
	args := make([]Expression, len(e.Args))
	for i, arg := range e.Args {
		resolved, err := a.resolveExpr(arg, table)
		if err != nil {
			return nil, err
		}
		args[i] = resolved
	}
	return &FuncExpr{Name: e.Name, Args: args}, nil
}

// resolveFuncExprNoTable 在无表上下文中解析函数表达式。
// 等价于 resolveFuncExpr(e, nil)，保留独立函数名以兼容既有调用点。
func (a *Analyzer) resolveFuncExprNoTable(e *FuncExpr) (*FuncExpr, error) {
	return a.resolveFuncExpr(e, nil)
}
