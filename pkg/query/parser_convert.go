package query

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/xwb1989/sqlparser"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// convertExpr 转换表达式。
func (p *Parser) convertExpr(expr sqlparser.Expr) (Expression, error) {
	if expr == nil {
		return nil, nil
	}

	switch e := expr.(type) {
	case *sqlparser.ColName:
		return &ColumnExpr{Name: sqlparser.String(e)}, nil
	case *sqlparser.SQLVal:
		return p.convertSQLVal(e)
	case *sqlparser.NullVal:
		return &LiteralExpr{Value: common.NewNull()}, nil
	case *sqlparser.ComparisonExpr:
		return p.convertComparisonExpr(e)
	case *sqlparser.AndExpr:
		return p.convertBinaryExpr(e.Left, e.Right, OpAnd)
	case *sqlparser.OrExpr:
		return p.convertBinaryExpr(e.Left, e.Right, OpOr)
	case *sqlparser.NotExpr:
		inner, err := p.convertExpr(e.Expr)
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: OpNot, Expr: inner}, nil
	case *sqlparser.ParenExpr:
		return p.convertExpr(e.Expr)
	case *sqlparser.FuncExpr:
		return p.convertFuncExpr(e)
	default:
		return nil, fmt.Errorf("query parse: unsupported expr type %T", expr)
	}
}

// convertBinaryExpr 转换二元逻辑表达式（AND/OR）。
func (p *Parser) convertBinaryExpr(left, right sqlparser.Expr, op BinaryOp) (*BinaryExpr, error) {
	l, err := p.convertExpr(left)
	if err != nil {
		return nil, err
	}
	r, err := p.convertExpr(right)
	if err != nil {
		return nil, err
	}
	return &BinaryExpr{Op: op, Left: l, Right: r}, nil
}

// convertSQLVal 转换 SQL 字面量值。
func (p *Parser) convertSQLVal(val *sqlparser.SQLVal) (*LiteralExpr, error) {
	switch val.Type {
	case sqlparser.IntVal:
		n, err := strconv.ParseInt(string(val.Val), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("query parse: invalid int value %q: %w", string(val.Val), err)
		}
		return &LiteralExpr{Value: common.NewInt64(n)}, nil
	case sqlparser.FloatVal:
		f, err := strconv.ParseFloat(string(val.Val), 64)
		if err != nil {
			return nil, fmt.Errorf("query parse: invalid float value %q: %w", string(val.Val), err)
		}
		return &LiteralExpr{Value: common.NewFloat64(f)}, nil
	case sqlparser.StrVal:
		return &LiteralExpr{Value: common.NewString(string(val.Val))}, nil
	default:
		return nil, fmt.Errorf("query parse: unsupported SQLVal type %d", val.Type)
	}
}

// convertComparisonExpr 转换比较表达式。
func (p *Parser) convertComparisonExpr(expr *sqlparser.ComparisonExpr) (*BinaryExpr, error) {
	left, err := p.convertExpr(expr.Left)
	if err != nil {
		return nil, err
	}
	right, err := p.convertExpr(expr.Right)
	if err != nil {
		return nil, err
	}

	op, err := p.convertComparisonOp(expr.Operator)
	if err != nil {
		return nil, err
	}

	return &BinaryExpr{Op: op, Left: left, Right: right}, nil
}

// convertComparisonOp 转换比较运算符。
func (p *Parser) convertComparisonOp(op string) (BinaryOp, error) {
	switch op {
	case sqlparser.EqualStr:
		return OpEq, nil
	case sqlparser.NotEqualStr:
		return OpNe, nil
	case sqlparser.LessThanStr:
		return OpLt, nil
	case sqlparser.LessEqualStr:
		return OpLe, nil
	case sqlparser.GreaterThanStr:
		return OpGt, nil
	case sqlparser.GreaterEqualStr:
		return OpGe, nil
	case sqlparser.LikeStr:
		return OpLike, nil
	default:
		return 0, fmt.Errorf("query parse: unsupported comparison operator %q", op)
	}
}

// convertFuncExpr 转换函数调用表达式。
func (p *Parser) convertFuncExpr(fn *sqlparser.FuncExpr) (*FuncExpr, error) {
	name := strings.ToLower(fn.Name.String())

	args := make([]Expression, 0, len(fn.Exprs))
	for _, selExpr := range fn.Exprs {
		aliased, ok := selExpr.(*sqlparser.AliasedExpr)
		if !ok {
			if _, starOk := selExpr.(*sqlparser.StarExpr); starOk {
				args = append(args, &StarExpr{})
				continue
			}
			return nil, fmt.Errorf("query parse: unsupported func arg type %T", selExpr)
		}
		arg, err := p.convertExpr(aliased.Expr)
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
	}

	return &FuncExpr{Name: name, Args: args}, nil
}

// convertGroupBy 转换 GROUP BY 子句。
func (p *Parser) convertGroupBy(groupBy sqlparser.GroupBy) ([]Expression, error) {
	result := make([]Expression, len(groupBy))
	for i, expr := range groupBy {
		converted, err := p.convertExpr(expr)
		if err != nil {
			return nil, err
		}
		result[i] = converted
	}
	return result, nil
}

// convertLimit 转换 LIMIT 子句。
func (p *Parser) convertLimit(limit *sqlparser.Limit) (*LimitClause, error) {
	var offset, count uint64

	if limit.Offset != nil {
		o, err := p.parseUint64(limit.Offset)
		if err != nil {
			return nil, fmt.Errorf("query parse: limit offset: %w", err)
		}
		offset = o
	}

	c, err := p.parseUint64(limit.Rowcount)
	if err != nil {
		return nil, fmt.Errorf("query parse: limit count: %w", err)
	}
	count = c

	return &LimitClause{Offset: offset, Count: count}, nil
}

// parseUint64 从 SQL 表达式解析 uint64 值。
func (p *Parser) parseUint64(expr sqlparser.Expr) (uint64, error) {
	sqlVal, ok := expr.(*sqlparser.SQLVal)
	if !ok {
		return 0, fmt.Errorf("expected integer literal, got %T", expr)
	}
	n, err := strconv.ParseUint(string(sqlVal.Val), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid integer %q: %w", string(sqlVal.Val), err)
	}
	return n, nil
}

// convertTableSpec 转换 CREATE TABLE 的表定义。
func (p *Parser) convertTableSpec(spec *sqlparser.TableSpec) ([]ColumnDef, []string, error) {
	colDefs := make([]ColumnDef, 0, len(spec.Columns))
	var primaryKeys []string

	for _, col := range spec.Columns {
		dataType, err := p.convertColumnType(&col.Type)
		if err != nil {
			return nil, nil, err
		}

		nullable := !bool(col.Type.NotNull)

		colDefs = append(colDefs, ColumnDef{
			Name:     col.Name.String(),
			Type:     dataType,
			Nullable: nullable,
		})

		if col.Type.KeyOpt == colKeyPrimary {
			primaryKeys = append(primaryKeys, col.Name.String())
		}
	}

	for _, idx := range spec.Indexes {
		if idx.Info.Primary {
			for _, ic := range idx.Columns {
				primaryKeys = append(primaryKeys, ic.Column.String())
			}
		}
	}

	return colDefs, primaryKeys, nil
}

// convertColumnType 将 sqlparser 的列类型转换为 common.DataType。
// 支持整数族无符号变体（BIGINT UNSIGNED→UINT64、TINYINT UNSIGNED→INT8）。
func (p *Parser) convertColumnType(ct *sqlparser.ColumnType) (common.DataType, error) {
	typ := strings.ToUpper(ct.Type)
	if dt, ok := intLikeColumnType(typ, bool(ct.Unsigned)); ok {
		return dt, nil
	}
	switch typ {
	case "DOUBLE", "FLOAT":
		return common.TypeFloat64, nil
	case "TEXT", "VARCHAR", "CHAR":
		return common.TypeString, nil
	case "BOOLEAN":
		return common.TypeBool, nil
	case "TIMESTAMP", "DATETIME":
		return common.TypeTimestamp, nil
	case "DATE":
		return common.TypeDate, nil
	default:
		return common.TypeNull, fmt.Errorf("query parse: unsupported column type %q", ct.Type)
	}
}

// intLikeColumnType 映射整数类 SQL 类型（含 BOOL 与无符号变体）。
// 返回 (类型, 是否匹配)。TINYINT 无符号映射为 INT8，否则为 BOOL（MySQL 约定）。
func intLikeColumnType(typ string, unsigned bool) (common.DataType, bool) {
	switch typ {
	case "BIGINT":
		if unsigned {
			return common.TypeUint64, true
		}
		return common.TypeInt64, true
	case "INT":
		return common.TypeInt64, true
	case "TINYINT":
		if unsigned {
			return common.TypeInt8, true
		}
		return common.TypeBool, true
	case "SMALLINT":
		return common.TypeInt16, true
	case "MEDIUMINT":
		return common.TypeInt32, true
	}
	return common.TypeNull, false
}

const colKeyPrimary = 1

// convertValues 转换 INSERT VALUES 子句。
func (p *Parser) convertValues(values sqlparser.Values) ([][]Expression, error) {
	rows := make([][]Expression, len(values))
	for i, valTuple := range values {
		row := make([]Expression, len(valTuple))
		for j, expr := range valTuple {
			converted, err := p.convertExpr(expr)
			if err != nil {
				return nil, err
			}
			row[j] = converted
		}
		rows[i] = row
	}
	return rows, nil
}
