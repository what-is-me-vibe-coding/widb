package query

import (
	"fmt"

	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
)

func (a *Analyzer) resolveExpr(expr Expression, table *catalog.Table) (Expression, error) {
	switch e := expr.(type) {
	case *ColumnExpr:
		return a.resolveColumnRef(e, table)
	case *LiteralExpr:
		return e, nil
	case *StarExpr:
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

func (a *Analyzer) resolveExprNoTable(expr Expression) (Expression, error) {
	switch e := expr.(type) {
	case *ColumnExpr:
		return nil, fmt.Errorf("analyzer: column reference %q without table context", e.Name)
	case *LiteralExpr:
		return e, nil
	case *BinaryExpr:
		return a.resolveBinaryExprNoTable(e)
	case *UnaryExpr:
		inner, err := a.resolveExprNoTable(e.Expr)
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: e.Op, Expr: inner}, nil
	case *FuncExpr:
		return a.resolveFuncExprNoTable(e)
	default:
		return nil, fmt.Errorf("analyzer: unsupported expression type %T", expr)
	}
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

func (a *Analyzer) resolveBinaryExprNoTable(e *BinaryExpr) (*BinaryExpr, error) {
	left, err := a.resolveExprNoTable(e.Left)
	if err != nil {
		return nil, err
	}
	right, err := a.resolveExprNoTable(e.Right)
	if err != nil {
		return nil, err
	}
	return &BinaryExpr{Op: e.Op, Left: left, Right: right}, nil
}

func (a *Analyzer) resolveUnaryExpr(e *UnaryExpr, table *catalog.Table) (*UnaryExpr, error) {
	inner, err := a.resolveExpr(e.Expr, table)
	if err != nil {
		return nil, err
	}
	return &UnaryExpr{Op: e.Op, Expr: inner}, nil
}

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

func (a *Analyzer) resolveFuncExprNoTable(e *FuncExpr) (*FuncExpr, error) {
	args := make([]Expression, len(e.Args))
	for i, arg := range e.Args {
		resolved, err := a.resolveExprNoTable(arg)
		if err != nil {
			return nil, err
		}
		args[i] = resolved
	}
	return &FuncExpr{Name: e.Name, Args: args}, nil
}
