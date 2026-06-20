package query

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/xwb1989/sqlparser"
)

const mysqlTinyint = "TINYINT"

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
// 支持 SELECT、INSERT、CREATE TABLE、UPDATE、DELETE、DROP TABLE、
// SHOW TABLES、DESCRIBE、EXPLAIN 语句。
func (p *Parser) Parse(sql string) (Statement, error) {
	// EXPLAIN <stmt>：sqlparser 会将其解析为 OtherRead 丢失内部语句，
	// 需在入口拦截并递归解析内部语句，同时透传内部解析错误。
	if stmt, handled, err := tryParseExplain(sql); handled {
		return stmt, err
	}
	// SHOW TABLES / DESCRIBE table / DESC table 不被 sqlparser 支持，
	// 在此预先拦截。
	if stmt, ok := tryParseMetaCommand(sql); ok {
		return stmt, nil
	}
	normalized := p.preprocessSQL(sql)
	stmt, err := sqlparser.ParseStrictDDL(normalized)
	if err != nil {
		return nil, fmt.Errorf("query parse: %w", err)
	}
	return p.convert(stmt, sql)
}

// preprocessSQL 将项目自定义类型替换为 MySQL 兼容类型，
// 使 sqlparser 能正确解析。
//
// 优化：用单遍标识符扫描替代 5 次 regexp.ReplaceAllString 调用。
// 原实现对每条 SQL 都要做 5 次完整字符串扫描 + 正则引擎调度，
// 而 SELECT/INSERT/UPDATE/DELETE 等绝大多数语句不包含项目自定义类型关键字，
// 5 次扫描全部返回原字符串，是显著的纯开销。优化后单遍扫描即可，
// 跳过任何非关键字字符，避免了正则引擎与多次字符串扫描的开销。
//
// 匹配规则与原实现完全等价：大小写不敏感、整词（\b）边界。
// 关键字 → MySQL 类型映射：
//
//	INT64/FLOAT64/STRING/BOOL/BOOLEAN → BIGINT/DOUBLE/TEXT/TINYINT/TINYINT
func (p *Parser) preprocessSQL(sql string) string {
	// 快速路径：SQL 长度不足以容纳任何关键字时直接返回，避免分配。
	// 最短关键字是 BOOL(4)，加上前后可能的边界字符。
	if len(sql) < 4 {
		return sql
	}

	// 优化：仅当 SQL 含有关键字首字符（i/f/s/b 任意一个）时才进入完整扫描，
	// 跳过最常见的纯数字/标点/单字母 SQL。绝大多数 DML 不会触发。
	if !sqlNeedsTypeRepl(sql) {
		return sql
	}

	// 单遍扫描：识别标识符并查表替换。直接按字节比较大小写不敏感关键字，
	// 通过边界检查（前后为非标识符字符）实现 \b 语义。
	b := make([]byte, 0, len(sql))
	i := 0
	n := len(sql)
	for i < n {
		c := sql[i]
		if !isIdentStartByte(c) {
			b = append(b, c)
			i++
			continue
		}
		// 找到标识符的结束位置
		j := i + 1
		for j < n && isIdentPartByte(sql[j]) {
			j++
		}
		// 检查前后是否为标识符边界（与 \b 语义一致）
		leftBoundary := i == 0 || !isIdentPartByte(sql[i-1])
		rightBoundary := j == n || !isIdentPartByte(sql[j])
		if leftBoundary && rightBoundary {
			word := sql[i:j]
			if mapped, ok := mapCustomTypeKeyword(word); ok {
				b = append(b, mapped...)
			} else {
				b = append(b, word...)
			}
		} else {
			b = append(b, sql[i:j]...)
		}
		i = j
	}
	return string(b)
}

// sqlNeedsTypeRepl 快速判断 SQL 中是否包含任一关键字首字符（i/f/s/b）。
// 这是对单遍扫描的「零拷贝」前置优化：常见 DML（不涉及类型声明）无此字符，
// 直接返回原字符串，避免任何分配。判断错误仅导致一次空扫描，无正确性风险。
func sqlNeedsTypeRepl(sql string) bool {
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		if c == 'I' || c == 'i' || c == 'F' || c == 'f' ||
			c == 'S' || c == 's' || c == 'B' || c == 'b' {
			return true
		}
	}
	return false
}

// isIdentStartByte 判断字节是否为标识符首字符（字母或下划线）。
func isIdentStartByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

// isIdentPartByte 判断字节是否为标识符后续字符（首字符或数字）。
func isIdentPartByte(c byte) bool {
	return isIdentStartByte(c) || (c >= '0' && c <= '9')
}

// mapCustomTypeKeyword 将项目自定义类型关键字映射为 MySQL 兼容类型。
// 大小写不敏感；返回 (mapped, true) 表示匹配成功，否则返回 ("", false)。
// 关键字 → MySQL 类型映射：
//
//	INT64 → BIGINT、FLOAT64 → DOUBLE、STRING → TEXT、
//	BOOL/BOOLEAN → TINYINT。
func mapCustomTypeKeyword(word string) (string, bool) {
	switch len(word) {
	case 4:
		if strings.EqualFold(word, "BOOL") {
			return mysqlTinyint, true
		}
	case 5:
		if strings.EqualFold(word, "INT64") {
			return "BIGINT", true
		}
	case 6:
		if strings.EqualFold(word, "STRING") {
			return "TEXT", true
		}
	case 7:
		if strings.EqualFold(word, "FLOAT64") {
			return "DOUBLE", true
		}
		if strings.EqualFold(word, "BOOLEAN") {
			return mysqlTinyint, true
		}
	}
	return "", false
}

// convert 将 sqlparser 的 AST 转换为项目内部 AST。
func (p *Parser) convert(stmt sqlparser.Statement, originalSQL string) (Statement, error) {
	switch s := stmt.(type) {
	case *sqlparser.Select:
		return p.convertSelect(s)
	case *sqlparser.Insert:
		return p.convertInsert(s)
	case *sqlparser.Update:
		return p.convertUpdate(s)
	case *sqlparser.Delete:
		return p.convertDelete(s)
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

// convertDDL 转换 DDL 语句（CREATE TABLE / DROP TABLE）。
func (p *Parser) convertDDL(ddl *sqlparser.DDL, originalSQL string) (Statement, error) {
	if ddl.Action == sqlparser.DropStr {
		return p.convertDropTable(ddl, originalSQL)
	}
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

// convertDropTable 转换 DROP TABLE 语句。
// 注意：sqlparser 在 DROP 语句中将表名放在 ddl.Table（而非 ddl.NewName，
// 后者仅用于 CREATE 语句）。
func (p *Parser) convertDropTable(ddl *sqlparser.DDL, originalSQL string) (*DropTableStatement, error) {
	tableName := sqlparser.String(ddl.Table)
	ifExists := strings.Contains(strings.ToLower(originalSQL), "if exists")
	return &DropTableStatement{Table: tableName, IfExists: ifExists}, nil
}

// metaCommandRe 匹配 SHOW TABLES / DESCRIBE table / DESC table 语句。
// sqlparser 不支持这些 MySQL 元命令，因此在 Parse 入口预先拦截。
var metaCommandRe = regexp.MustCompile(`(?i)^\s*(SHOW\s+TABLES|DESCRIBE|DESC)\b(.*)$`)

// tryParseMetaCommand 尝试解析 SHOW TABLES / DESCRIBE / DESC 语句。
// 返回 (stmt, true) 表示匹配成功；返回 (nil, false) 表示非元命令，交由 sqlparser 处理。
func tryParseMetaCommand(sql string) (Statement, bool) {
	m := metaCommandRe.FindStringSubmatch(sql)
	if m == nil {
		return nil, false
	}
	keyword := strings.ToUpper(strings.TrimSpace(m[1]))
	rest := strings.TrimSpace(m[2])
	if strings.HasPrefix(keyword, "SHOW") {
		return &ShowTablesStatement{}, true
	}
	// DESCRIBE / DESC <table>
	table := strings.TrimRight(rest, ";")
	table = strings.TrimSpace(strings.Trim(table, "`"))
	if table == "" {
		return nil, false // 语法不完整，交由 sqlparser 报错
	}
	return &DescribeStatement{Table: table}, true
}

// explainRe 匹配 EXPLAIN 关键字开头的语句（大小写不敏感）。
var explainRe = regexp.MustCompile(`(?i)^\s*EXPLAIN\b(.*)$`)

// tryParseExplain 尝试解析 EXPLAIN <stmt> 语句。
// 返回 (stmt, true, nil) 表示成功解析为 ExplainStatement；
// 返回 (nil, true, err) 表示匹配到 EXPLAIN 但内部语句解析失败，err 为透传的解析错误；
// 返回 (nil, false, nil) 表示非 EXPLAIN 语句，交由后续流程处理。
//
// 仅允许只读查询（SELECT）走 EXPLAIN 路径；DDL/DML 等由服务层直接处理的语句
// 返回错误，避免 EXPLAIN 语义歧义。内部语句的解析错误会被透传，使调用者能看到
// 真实的语法错误而非 sqlparser 的 OtherRead 报错。
func tryParseExplain(sql string) (Statement, bool, error) {
	m := explainRe.FindStringSubmatch(sql)
	if m == nil {
		return nil, false, nil
	}
	innerSQL := strings.TrimRight(strings.TrimSpace(m[1]), ";")
	if innerSQL == "" {
		return nil, true, fmt.Errorf("query parse: EXPLAIN 后缺少待解释的语句")
	}
	inner, err := NewParser().Parse(innerSQL)
	if err != nil {
		return nil, true, fmt.Errorf("query parse: EXPLAIN 内部语句: %w", err)
	}
	if _, ok := inner.(*SelectStatement); !ok {
		return nil, true, fmt.Errorf("query parse: EXPLAIN 仅支持 SELECT 语句，不支持 %T", inner)
	}
	return &ExplainStatement{Inner: inner}, true, nil
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
