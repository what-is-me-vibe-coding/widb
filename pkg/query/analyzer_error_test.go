package query

import (
	"testing"
)

func TestAnalyzerTableNotExist(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("SELECT id FROM nonexistent")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	_, err = analyzer.Analyze(stmt)
	if err == nil {
		t.Fatal("expected error for nonexistent table")
	}
}

func TestAnalyzerColumnNotExist(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("SELECT nonexistent_col FROM users")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	_, err = analyzer.Analyze(stmt)
	if err == nil {
		t.Fatal("expected error for nonexistent column")
	}
}

func TestAnalyzerInsertInvalidColumn(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())
	parser := NewParser()

	stmt, err := parser.Parse("INSERT INTO users (id, nonexistent) VALUES (1, 'test')")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	_, err = analyzer.Analyze(stmt)
	if err == nil {
		t.Fatal("expected error for nonexistent column in INSERT")
	}
}

func TestAnalyzerSelectNoFromColumnRef(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())

	stmt := &SelectStatement{
		Columns: []SelectColumn{
			{Expr: &ColumnExpr{Name: "id"}},
		},
	}

	_, err := analyzer.Analyze(stmt)
	if err == nil {
		t.Fatal("expected error for column reference without table context")
	}
}

func TestAnalyzerSelectNoFromUnsupportedExpr(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())

	stmt := &SelectStatement{
		Columns: []SelectColumn{
			{Expr: &unsupportedExpr{}},
		},
	}

	_, err := analyzer.Analyze(stmt)
	if err == nil {
		t.Fatal("expected error for unsupported expression type")
	}
}

func TestAnalyzerUnsupportedStatement(t *testing.T) {
	analyzer := NewAnalyzer(testCatalog())

	_, err := analyzer.Analyze(&unsupportedStmt{})
	if err == nil {
		t.Fatal("expected error for unsupported statement type")
	}
}

type unsupportedStmt struct{}

func (s *unsupportedStmt) statementNode() {}
func (s *unsupportedStmt) String() string { return "UNSUPPORTED" }

type unsupportedExpr struct{}

func (e *unsupportedExpr) exprNode()      {}
func (e *unsupportedExpr) String() string { return "UNSUPPORTED" }
