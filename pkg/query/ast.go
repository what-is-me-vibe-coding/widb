package query

import (
	"fmt"
	"strings"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const nullStr = "NULL"

// Statement 表示一条 SQL 语句的抽象语法树。
type Statement interface {
	statementNode()
	String() string
}

// SelectStatement 表示 SELECT 查询语句。
type SelectStatement struct {
	Columns []SelectColumn
	From    *TableRef
	Where   Expression
	GroupBy []Expression
	Limit   *LimitClause
}

func (s *SelectStatement) statementNode() {}
func (s *SelectStatement) String() string {
	var b strings.Builder
	b.WriteString("SELECT ")
	for i, col := range s.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(col.String())
	}
	if s.From != nil {
		b.WriteString(" FROM ")
		b.WriteString(s.From.String())
	}
	if s.Where != nil {
		b.WriteString(" WHERE ")
		b.WriteString(s.Where.String())
	}
	if len(s.GroupBy) > 0 {
		b.WriteString(" GROUP BY ")
		for i, expr := range s.GroupBy {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(expr.String())
		}
	}
	if s.Limit != nil {
		b.WriteString(" ")
		b.WriteString(s.Limit.String())
	}
	return b.String()
}

// SelectColumn 表示 SELECT 列表中的一项。
type SelectColumn struct {
	Expr  Expression
	Alias string
}

func (c SelectColumn) String() string {
	if c.Alias != "" {
		return fmt.Sprintf("%s AS %s", c.Expr.String(), c.Alias)
	}
	return c.Expr.String()
}

// TableRef 表示 FROM 子句中的表引用。
type TableRef struct {
	Name  string
	Alias string
}

func (t *TableRef) String() string {
	if t.Alias != "" {
		return fmt.Sprintf("%s AS %s", t.Name, t.Alias)
	}
	return t.Name
}

// LimitClause 表示 LIMIT 子句。
type LimitClause struct {
	Offset uint64
	Count  uint64
}

func (l *LimitClause) String() string {
	if l.Offset > 0 {
		return fmt.Sprintf("LIMIT %d, %d", l.Offset, l.Count)
	}
	return fmt.Sprintf("LIMIT %d", l.Count)
}

// InsertStatement 表示 INSERT 语句。
type InsertStatement struct {
	Table   string
	Columns []string
	Rows    [][]Expression
}

func (s *InsertStatement) statementNode() {}
func (s *InsertStatement) String() string {
	var b strings.Builder
	b.WriteString("INSERT INTO ")
	b.WriteString(s.Table)
	if len(s.Columns) > 0 {
		b.WriteString(" (")
		for i, col := range s.Columns {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(col)
		}
		b.WriteString(")")
	}
	b.WriteString(" VALUES ")
	for i, row := range s.Rows {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("(")
		for j, val := range row {
			if j > 0 {
				b.WriteString(", ")
			}
			b.WriteString(val.String())
		}
		b.WriteString(")")
	}
	return b.String()
}

// CreateTableStatement 表示 CREATE TABLE 语句。
type CreateTableStatement struct {
	Table       string
	Columns     []ColumnDef
	PrimaryKey  []string
	IfNotExists bool
}

func (s *CreateTableStatement) statementNode() {}
func (s *CreateTableStatement) String() string {
	var b strings.Builder
	b.WriteString("CREATE TABLE ")
	if s.IfNotExists {
		b.WriteString("IF NOT EXISTS ")
	}
	b.WriteString(s.Table)
	b.WriteString(" (")
	for i, col := range s.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(col.String())
	}
	if len(s.PrimaryKey) > 0 {
		b.WriteString(", PRIMARY KEY (")
		for i, pk := range s.PrimaryKey {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(pk)
		}
		b.WriteString(")")
	}
	b.WriteString(")")
	return b.String()
}

// ColumnDef 定义 CREATE TABLE 中的列。
type ColumnDef struct {
	Name     string
	Type     common.DataType
	Nullable bool
}

func (c ColumnDef) String() string {
	nullableStr := "NOT NULL"
	if c.Nullable {
		nullableStr = nullStr
	}
	return fmt.Sprintf("%s %s %s", c.Name, c.Type.String(), nullableStr)
}

// Expression 表示 SQL 表达式。
type Expression interface {
	exprNode()
	String() string
}

// ColumnExpr 表示列引用表达式。
type ColumnExpr struct {
	Name string
}

func (e *ColumnExpr) exprNode()      {}
func (e *ColumnExpr) String() string { return e.Name }

// LiteralExpr 表示字面量表达式。
type LiteralExpr struct {
	Value common.Value
}

func (e *LiteralExpr) exprNode() {}
func (e *LiteralExpr) String() string {
	if !e.Value.Valid {
		return nullStr
	}
	switch e.Value.Typ {
	case common.TypeString:
		return fmt.Sprintf("'%s'", e.Value.Str)
	case common.TypeFloat64:
		return fmt.Sprintf("%g", e.Value.Float64)
	default:
		return e.Value.String()
	}
}

// BinaryExpr 表示二元运算表达式。
type BinaryExpr struct {
	Op    BinaryOp
	Left  Expression
	Right Expression
}

func (e *BinaryExpr) exprNode() {}
func (e *BinaryExpr) String() string {
	return fmt.Sprintf("(%s %s %s)", e.Left.String(), e.Op.String(), e.Right.String())
}

// UnaryExpr 表示一元运算表达式。
type UnaryExpr struct {
	Op   UnaryOp
	Expr Expression
}

func (e *UnaryExpr) exprNode() {}
func (e *UnaryExpr) String() string {
	return fmt.Sprintf("%s%s", e.Op.String(), e.Expr.String())
}

// FuncExpr 表示函数调用表达式。
type FuncExpr struct {
	Name string
	Args []Expression
}

func (e *FuncExpr) exprNode() {}
func (e *FuncExpr) String() string {
	args := make([]string, len(e.Args))
	for i, a := range e.Args {
		args[i] = a.String()
	}
	return fmt.Sprintf("%s(%s)", e.Name, strings.Join(args, ", "))
}

// StarExpr 表示 * 通配符表达式。
type StarExpr struct{}

func (e *StarExpr) exprNode()      {}
func (e *StarExpr) String() string { return "*" }

// BinaryOp 表示二元运算符。
type BinaryOp int

// BinaryOp 表示二元运算符的类型。
const (
	OpEq BinaryOp = iota
	OpNe
	OpLt
	OpLe
	OpGt
	OpGe
	OpAnd
	OpOr
	OpAdd
	OpSub
	OpMul
	OpDiv
	OpLike
)

func (op BinaryOp) String() string {
	switch op {
	case OpEq:
		return "="
	case OpNe:
		return "!="
	case OpLt:
		return "<"
	case OpLe:
		return "<="
	case OpGt:
		return ">"
	case OpGe:
		return ">="
	case OpAnd:
		return "AND"
	case OpOr:
		return "OR"
	case OpAdd:
		return "+"
	case OpSub:
		return "-"
	case OpMul:
		return "*"
	case OpDiv:
		return "/"
	case OpLike:
		return "LIKE"
	default:
		return "?"
	}
}

// UnaryOp 表示一元运算符。
type UnaryOp int

// UnaryOp 常量定义。
const (
	OpNot UnaryOp = iota
	OpNeg
)

func (op UnaryOp) String() string {
	switch op {
	case OpNot:
		return "NOT "
	case OpNeg:
		return "-"
	default:
		return "?"
	}
}
