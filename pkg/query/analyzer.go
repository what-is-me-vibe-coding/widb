package query

import (
	"fmt"

	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// CatalogProvider provides table metadata for the analyzer.
type CatalogProvider interface {
	GetTable(name string) (*catalog.Table, error)
}

// Analyzer performs semantic analysis on SQL statements.
type Analyzer struct {
	catalog CatalogProvider
}

// NewAnalyzer creates a new Analyzer with the given catalog provider.
func NewAnalyzer(catalog CatalogProvider) *Analyzer {
	return &Analyzer{catalog: catalog}
}

// Analyze resolves and validates a Statement, returning a PlanNode or an error.
func (a *Analyzer) Analyze(stmt Statement) (PlanNode, error) {
	switch s := stmt.(type) {
	case *SelectStatement:
		return a.analyzeSelect(s)
	case *InsertStatement:
		return a.analyzeInsert(s)
	case *CreateTableStatement:
		return a.analyzeCreateTable(s)
	default:
		return nil, fmt.Errorf("analyzer: unsupported statement type %T", stmt)
	}
}

func (a *Analyzer) analyzeSelect(sel *SelectStatement) (PlanNode, error) {
	if sel.From == nil {
		return a.analyzeSelectNoFrom(sel)
	}

	table, err := a.catalog.GetTable(sel.From.Name)
	if err != nil {
		return nil, fmt.Errorf("analyzer: %w", err)
	}

	resolvedCols, err := a.resolveSelectColumns(sel.Columns, table)
	if err != nil {
		return nil, err
	}

	scan, predicate, err := a.buildScanNode(sel, table)
	if err != nil {
		return nil, err
	}

	current, err := a.buildSelectPipeline(sel, table, scan, predicate, resolvedCols)
	if err != nil {
		return nil, err
	}

	return current, nil
}

// buildScanNode 构建扫描节点并解析 WHERE 谓词。
func (a *Analyzer) buildScanNode(sel *SelectStatement, table *catalog.Table) (*ScanNode, Expression, error) {
	var predicate Expression
	if sel.Where != nil {
		var err error
		predicate, err = a.resolveExpr(sel.Where, table)
		if err != nil {
			return nil, nil, fmt.Errorf("analyzer: resolve where: %w", err)
		}
	}

	scanCols := a.collectRequiredColumns(sel, table)
	scanSchema := buildScanSchema(scanCols, table)
	scan := &ScanNode{
		Table:     table.Name,
		Columns:   scanCols,
		Predicate: predicate,
		schema:    scanSchema,
	}
	return scan, predicate, nil
}

// buildSelectPipeline 在扫描节点之上构建 Filter → Aggregate → Project → Limit 流水线。
func (a *Analyzer) buildSelectPipeline(sel *SelectStatement, table *catalog.Table, scan *ScanNode, predicate Expression, resolvedCols []resolvedColumn) (PlanNode, error) {
	var current PlanNode = scan

	if predicate != nil {
		current = &FilterNode{
			Child:     current,
			Condition: predicate,
		}
	}

	var aggNode *AggregateNode
	if len(sel.GroupBy) > 0 || a.hasAggregateFuncs(sel.Columns) {
		var err error
		aggNode, err = a.buildAggregateNode(sel, table, current)
		if err != nil {
			return nil, err
		}
		current = aggNode
	}

	projectExprs, projectAliases, projectSchema, err := a.buildProjectOutput(sel.Columns, resolvedCols, table)
	if err != nil {
		return nil, err
	}

	// 当存在 AggregateNode 时，将投影中的聚合函数表达式替换为对聚合输出列的引用，
	// 避免 ProjectNode 重复求值聚合函数。
	if aggNode != nil {
		projectExprs = remapAggregateRefs(projectExprs, aggNode)
	}

	if a.needsProjection(sel, table) {
		current = &ProjectNode{
			Child:       current,
			Expressions: projectExprs,
			Aliases:     projectAliases,
			schema:      projectSchema,
		}
	}

	if sel.Limit != nil {
		current = &LimitNode{
			Child:  current,
			Offset: sel.Limit.Offset,
			Count:  sel.Limit.Count,
		}
	}

	return current, nil
}

func (a *Analyzer) analyzeSelectNoFrom(sel *SelectStatement) (PlanNode, error) {
	exprs := make([]Expression, len(sel.Columns))
	aliases := make([]string, len(sel.Columns))
	schema := make([]ColumnDef, len(sel.Columns))

	for i, col := range sel.Columns {
		resolved, err := a.resolveExprNoTable(col.Expr)
		if err != nil {
			return nil, err
		}
		exprs[i] = resolved
		aliases[i] = col.Alias

		colName := col.Alias
		if colName == "" {
			colName = col.Expr.String()
		}
		schema[i] = ColumnDef{
			Name:     colName,
			Type:     exprReturnType(resolved),
			Nullable: true,
		}
	}

	return &ProjectNode{
		Child:       nil,
		Expressions: exprs,
		Aliases:     aliases,
		schema:      schema,
	}, nil
}

func (a *Analyzer) analyzeInsert(ins *InsertStatement) (PlanNode, error) {
	table, err := a.catalog.GetTable(ins.Table)
	if err != nil {
		return nil, fmt.Errorf("analyzer: %w", err)
	}

	if len(ins.Columns) > 0 {
		for _, colName := range ins.Columns {
			if !table.HasColumn(colName) {
				return nil, fmt.Errorf("analyzer: column %q does not exist in table %q", colName, ins.Table)
			}
		}
	}

	scanSchema := make([]ColumnDef, len(table.Columns))
	for i, col := range table.Columns {
		scanSchema[i] = ColumnDef{
			Name:     col.Name,
			Type:     col.Type,
			Nullable: col.Nullable,
		}
	}

	return &ScanNode{
		Table:   table.Name,
		Columns: nil,
		schema:  scanSchema,
	}, nil
}

func (a *Analyzer) analyzeCreateTable(ct *CreateTableStatement) (PlanNode, error) {
	scanSchema := make([]ColumnDef, len(ct.Columns))
	copy(scanSchema, ct.Columns)

	return &ScanNode{
		Table:   ct.Table,
		Columns: nil,
		schema:  scanSchema,
	}, nil
}

type resolvedColumn struct {
	name  string
	idx   int
	typ   common.DataType
	alias string
	expr  Expression
}

func (a *Analyzer) resolveSelectColumns(cols []SelectColumn, table *catalog.Table) ([]resolvedColumn, error) {
	result := make([]resolvedColumn, 0, len(cols))

	for _, col := range cols {
		if _, ok := col.Expr.(*StarExpr); ok {
			for i, tc := range table.Columns {
				result = append(result, resolvedColumn{
					name: tc.Name,
					idx:  i,
					typ:  tc.Type,
				})
			}
			continue
		}

		resolved, err := a.resolveExpr(col.Expr, table)
		if err != nil {
			return nil, err
		}

		rc := resolvedColumn{
			alias: col.Alias,
			expr:  resolved,
		}

		if ce, ok := resolved.(*ResolvedColumnExpr); ok {
			rc.name = ce.Name
			rc.idx = ce.Idx
			rc.typ = ce.typ
		} else {
			rc.name = col.Expr.String()
			rc.typ = exprReturnType(resolved)
		}

		result = append(result, rc)
	}

	return result, nil
}

func (a *Analyzer) collectRequiredColumns(sel *SelectStatement, table *catalog.Table) []string {
	colSet := make(map[string]bool)

	a.collectExprColumns(sel.Where, colSet)

	for _, col := range sel.Columns {
		// SELECT * 需要所有列，确保带 WHERE 的全表查询能返回完整行
		if _, ok := col.Expr.(*StarExpr); ok {
			for _, tc := range table.Columns {
				colSet[tc.Name] = true
			}
			continue
		}
		a.collectExprColumns(col.Expr, colSet)
	}

	for _, gb := range sel.GroupBy {
		a.collectExprColumns(gb, colSet)
	}

	cols := make([]string, 0, len(colSet))
	for _, tc := range table.Columns {
		if colSet[tc.Name] {
			cols = append(cols, tc.Name)
		}
	}

	if len(cols) == 0 {
		for _, tc := range table.Columns {
			cols = append(cols, tc.Name)
		}
	}

	return cols
}

func (a *Analyzer) collectExprColumns(expr Expression, colSet map[string]bool) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ColumnExpr:
		colSet[e.Name] = true
	case *BinaryExpr:
		a.collectExprColumns(e.Left, colSet)
		a.collectExprColumns(e.Right, colSet)
	case *UnaryExpr:
		a.collectExprColumns(e.Expr, colSet)
	case *FuncExpr:
		for _, arg := range e.Args {
			a.collectExprColumns(arg, colSet)
		}
	}
}

func (a *Analyzer) buildProjectOutput(_ []SelectColumn, resolved []resolvedColumn, _ *catalog.Table) ([]Expression, []string, []ColumnDef, error) {
	exprs := make([]Expression, len(resolved))
	aliases := make([]string, len(resolved))
	schema := make([]ColumnDef, len(resolved))

	for i, rc := range resolved {
		if rc.expr != nil {
			exprs[i] = rc.expr
		} else {
			exprs[i] = &ResolvedColumnExpr{Name: rc.name, Idx: rc.idx, typ: rc.typ}
		}
		aliases[i] = rc.alias

		colName := rc.alias
		if colName == "" {
			colName = rc.name
		}
		schema[i] = ColumnDef{
			Name:     colName,
			Type:     rc.typ,
			Nullable: true,
		}
	}

	return exprs, aliases, schema, nil
}

func (a *Analyzer) needsProjection(sel *SelectStatement, _ *catalog.Table) bool {
	if sel.From == nil {
		return true
	}

	if len(sel.Columns) == 1 {
		if _, ok := sel.Columns[0].Expr.(*StarExpr); ok {
			return false
		}
	}

	for _, col := range sel.Columns {
		if col.Alias != "" {
			return true
		}
		if _, ok := col.Expr.(*FuncExpr); ok {
			return true
		}
		if _, ok := col.Expr.(*BinaryExpr); ok {
			return true
		}
		if _, ok := col.Expr.(*UnaryExpr); ok {
			return true
		}
	}

	return false
}

func buildScanSchema(colNames []string, table *catalog.Table) []ColumnDef {
	schema := make([]ColumnDef, len(colNames))
	for i, name := range colNames {
		col, err := table.GetColumn(name)
		if err != nil {
			schema[i] = ColumnDef{Name: name, Type: common.TypeNull, Nullable: true}
			continue
		}
		schema[i] = ColumnDef{
			Name:     col.Name,
			Type:     col.Type,
			Nullable: col.Nullable,
		}
	}
	return schema
}
