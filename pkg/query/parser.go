package query

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/xwb1989/sqlparser"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const mysqlTinyint = "TINYINT"

// customTypeReplacements 将项目自定义类型映射为 MySQL 兼容类型，
// 以便 sqlparser 能正确解析 CREATE TABLE 语句。
var customTypeReplacements = []struct {
	pattern *regexp.Regexp
	mysql   string
}{
	{regexp.MustCompile(`(?i)\bINT64\b`), "BIGINT"},
	{regexp.MustCompile(`(?i)\bFLOAT64\b`), "DOUBLE"},
	{regexp.MustCompile(`(?i)\bSTRING\b`), "TEXT"},
	{regexp.MustCompile(`(?i)\bBOOL\b`), mysqlTinyint},
	{regexp.MustCompile(`(?i)\bBOOLEAN\b`), mysqlTinyint},
}

// Parser 将 SQL 语句解析为项目内部的 AST（Statement）。
type Parser struct{}

// NewParser 创建一个新的 Parser 实例。
func NewParser() *Parser {
	return &Parser{}
}

// Parse 将 SQL 字符串解析为 Statement。
// 支持 SELECT、INSERT、CREATE TABLE 三种语句。
func (p *Parser) Parse(sql string) (Statement, error) {
	normalized := p.preprocessSQL(sql)
	stmt, err := sqlparser.ParseStrictDDL(normalized)
	if err != nil {
		return nil, fmt.Errorf("query parse: %w", err)
	}
	return p.convert(stmt, sql)
}

// preprocessSQL 将项目自定义类型替换为 MySQL 兼容类型，
// 使 sqlparser 能正确解析。
func (p *Parser) preprocessSQL(sql string) string {
	result := sql
	for _, r := range customTypeReplacements {
		result = r.pattern.ReplaceAllString(result, r.mysql)
	}
	return result
}

// convert 将 sqlparser 的 AST 转换为项目内部 AST。
func (p *Parser) convert(stmt sqlparser.Statement, originalSQL string) (Statement, error) {
	switch s := stmt.(type) {
	case *sqlparser.Select:
		return p.convertSelect(s)
	case *sqlparser.Insert:
		return p.convertInsert(s)
	case *sqlparser.DDL:
		return p.convertDDL(s, originalSQL)
	default:
		return nil, fmt.Errorf("query parse: unsupported statement type %T", stmt)
	}
}

// convertSelect 转换 SELECT 语句。
func (p *Parser) convertSelect(sel *sqlparser.Select) (*SelectStatement, error) {
	columns, err := p.convertSelectExprs(sel.SelectExprs)
	if err != nil {
		return nil, err
	}

	var from *TableRef
	if len(sel.From) > 0 {
		from, err = p.convertTableExprs(sel.From)
		if err != nil {
			return nil, err
		}
		if from.Name == "dual" {
			from = nil
		}
	}

	var where Expression
	if sel.Where != nil {
		where, err = p.convertExpr(sel.Where.Expr)
		if err != nil {
			return nil, err
		}
	}

	groupBy, err := p.convertGroupBy(sel.GroupBy)
	if err != nil {
		return nil, err
	}

	var limit *LimitClause
	if sel.Limit != nil {
		limit, err = p.convertLimit(sel.Limit)
		if err != nil {
			return nil, err
		}
	}

	return &SelectStatement{
		Columns: columns,
		From:    from,
		Where:   where,
		GroupBy: groupBy,
		Limit:   limit,
	}, nil
}

// convertInsert 转换 INSERT 语句。
func (p *Parser) convertInsert(ins *sqlparser.Insert) (*InsertStatement, error) {
	tableName := sqlparser.String(ins.Table)

	columns := make([]string, len(ins.Columns))
	for i, col := range ins.Columns {
		columns[i] = sqlparser.String(col)
	}

	rows, err := p.convertValues(ins.Rows.(sqlparser.Values))
	if err != nil {
		return nil, err
	}

	return &InsertStatement{
		Table:   tableName,
		Columns: columns,
		Rows:    rows,
	}, nil
}

// convertDDL 转换 DDL 语句（仅支持 CREATE TABLE）。
func (p *Parser) convertDDL(ddl *sqlparser.DDL, originalSQL string) (*CreateTableStatement, error) {
	if ddl.Action != sqlparser.CreateStr {
		return nil, fmt.Errorf("query parse: unsupported DDL action %q", ddl.Action)
	}

	tableName := sqlparser.String(ddl.NewName)

	ifNotExists := strings.Contains(
		strings.ToLower(originalSQL),
		"if not exists",
	)

	var colDefs []ColumnDef
	var primaryKeys []string

	if ddl.TableSpec != nil {
		colDefs, primaryKeys, err := p.convertTableSpec(ddl.TableSpec)
		if err != nil {
			return nil, err
		}
		return &CreateTableStatement{
			Table:       tableName,
			Columns:     colDefs,
			PrimaryKey:  primaryKeys,
			IfNotExists: ifNotExists,
		}, nil
	}

	return &CreateTableStatement{
		Table:       tableName,
		Columns:     colDefs,
		PrimaryKey:  primaryKeys,
		IfNotExists: ifNotExists,
	}, nil
}

// convertSelectExprs 转换 SELECT 列表。
func (p *Parser) convertSelectExprs(exprs sqlparser.SelectExprs) ([]SelectColumn, error) {
	result := make([]SelectColumn, len(exprs))
	for i, expr := range exprs {
		switch e := expr.(type) {
		case *sqlparser.StarExpr:
			result[i] = SelectColumn{Expr: &StarExpr{}}
		case *sqlparser.AliasedExpr:
			converted, err := p.convertExpr(e.Expr)
			if err != nil {
				return nil, err
			}
			alias := ""
			if !e.As.IsEmpty() {
				alias = e.As.String()
			}
			result[i] = SelectColumn{Expr: converted, Alias: alias}
		default:
			return nil, fmt.Errorf("query parse: unsupported select expr type %T", expr)
		}
	}
	return result, nil
}

// convertTableExprs 转换 FROM 子句。
func (p *Parser) convertTableExprs(tableExprs sqlparser.TableExprs) (*TableRef, error) {
	if len(tableExprs) != 1 {
		return nil, fmt.Errorf("query parse: only single table FROM is supported, got %d", len(tableExprs))
	}

	aliasedTable, ok := tableExprs[0].(*sqlparser.AliasedTableExpr)
	if !ok {
		return nil, fmt.Errorf("query parse: unsupported table expr type %T", tableExprs[0])
	}

	tableName, ok := aliasedTable.Expr.(sqlparser.TableName)
	if !ok {
		return nil, fmt.Errorf("query parse: unsupported table source type %T", aliasedTable.Expr)
	}

	alias := ""
	if !aliasedTable.As.IsEmpty() {
		alias = aliasedTable.As.String()
	}

	return &TableRef{
		Name:  sqlparser.String(tableName),
		Alias: alias,
	}, nil
}

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
func (p *Parser) convertColumnType(ct *sqlparser.ColumnType) (common.DataType, error) {
	typ := strings.ToUpper(ct.Type)

	switch typ {
	case "BIGINT", "INT":
		return common.TypeInt64, nil
	case "DOUBLE", "FLOAT":
		return common.TypeFloat64, nil
	case "TEXT", "VARCHAR", "CHAR":
		return common.TypeString, nil
	case "BOOLEAN", "TINYINT":
		return common.TypeBool, nil
	case "TIMESTAMP", "DATETIME":
		return common.TypeTimestamp, nil
	default:
		return common.TypeNull, fmt.Errorf("query parse: unsupported column type %q", ct.Type)
	}
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
