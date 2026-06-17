package query

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/xwb1989/sqlparser"
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

// engineOptionRe 从 CREATE TABLE 语句中提取 ENGINE=<name> 选项。
// 大小写不敏感，允许等号两侧有空白。捕获组 1 为引擎名。
var engineOptionRe = regexp.MustCompile(`(?i)\bENGINE\s*=\s*([A-Za-z_][A-Za-z0-9_]*)`)

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
		var err error
		colDefs, primaryKeys, err = p.convertTableSpec(ddl.TableSpec)
		if err != nil {
			return nil, err
		}
	}

	return &CreateTableStatement{
		Table:       tableName,
		Columns:     colDefs,
		PrimaryKey:  primaryKeys,
		IfNotExists: ifNotExists,
		Engine:      extractEngine(originalSQL),
	}, nil
}

// extractEngine 从原始 SQL 中提取 ENGINE=<name> 选项，返回小写的引擎名。
// 未指定 ENGINE 时返回空字符串，由 catalog 层规范化为默认的 LSM 引擎。
func extractEngine(originalSQL string) string {
	m := engineOptionRe.FindStringSubmatch(originalSQL)
	if len(m) < 2 {
		return ""
	}
	return strings.ToLower(m[1])
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
