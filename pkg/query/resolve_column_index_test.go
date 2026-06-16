package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestResolveColumnIndexResolvedValid tests resolveColumnIndex with a
// ResolvedColumnExpr that has a valid index within the schema.
func TestResolveColumnIndexResolvedValid(t *testing.T) {
	schema := []ColumnDef{
		{Name: "id", Type: common.TypeInt64},
		{Name: "name", Type: common.TypeString},
		{Name: "age", Type: common.TypeInt64},
	}

	idx, name := resolveColumnIndex(&ResolvedColumnExpr{Name: "age", Idx: 2}, schema)
	if idx != 2 {
		t.Errorf("idx = %d, want 2", idx)
	}
	if name != "age" {
		t.Errorf("name = %q, want %q", name, "age")
	}
}

// TestResolveColumnIndexResolvedOutOfBoundsNegative tests resolveColumnIndex
// with a ResolvedColumnExpr that has a negative index.
func TestResolveColumnIndexResolvedOutOfBoundsNegative(t *testing.T) {
	schema := []ColumnDef{
		{Name: "id", Type: common.TypeInt64},
		{Name: "name", Type: common.TypeString},
	}

	idx, name := resolveColumnIndex(&ResolvedColumnExpr{Name: "bad", Idx: -1}, schema)
	if idx != -1 {
		t.Errorf("idx = %d, want -1", idx)
	}
	if name != "" {
		t.Errorf("name = %q, want empty", name)
	}
}

// TestResolveColumnIndexResolvedOutOfBoundsTooLarge tests resolveColumnIndex
// with a ResolvedColumnExpr that has an index beyond the schema length.
func TestResolveColumnIndexResolvedOutOfBoundsTooLarge(t *testing.T) {
	schema := []ColumnDef{
		{Name: "id", Type: common.TypeInt64},
	}

	idx, name := resolveColumnIndex(&ResolvedColumnExpr{Name: "bad", Idx: 99}, schema)
	if idx != -1 {
		t.Errorf("idx = %d, want -1", idx)
	}
	if name != "" {
		t.Errorf("name = %q, want empty", name)
	}
}

// TestResolveColumnIndexColumnExprMatch tests resolveColumnIndex with a
// ColumnExpr that matches a column in the schema.
func TestResolveColumnIndexColumnExprMatch(t *testing.T) {
	schema := []ColumnDef{
		{Name: "id", Type: common.TypeInt64},
		{Name: "score", Type: common.TypeFloat64},
		{Name: "age", Type: common.TypeInt64},
	}

	idx, name := resolveColumnIndex(&ColumnExpr{Name: "score"}, schema)
	if idx != 1 {
		t.Errorf("idx = %d, want 1", idx)
	}
	if name != "score" {
		t.Errorf("name = %q, want %q", name, "score")
	}
}

// TestResolveColumnIndexColumnExprNoMatch tests resolveColumnIndex with a
// ColumnExpr that doesn't match any column in the schema.
func TestResolveColumnIndexColumnExprNoMatch(t *testing.T) {
	schema := []ColumnDef{
		{Name: "id", Type: common.TypeInt64},
		{Name: "name", Type: common.TypeString},
	}

	idx, name := resolveColumnIndex(&ColumnExpr{Name: "nonexistent"}, schema)
	if idx != -1 {
		t.Errorf("idx = %d, want -1", idx)
	}
	if name != "" {
		t.Errorf("name = %q, want empty", name)
	}
}

// TestResolveColumnIndexUnsupportedExpr tests resolveColumnIndex with an
// unsupported expression type (LiteralExpr), which should return -1, "".
func TestResolveColumnIndexUnsupportedExpr(t *testing.T) {
	schema := []ColumnDef{
		{Name: "id", Type: common.TypeInt64},
	}

	idx, name := resolveColumnIndex(&LiteralExpr{Value: common.NewInt64(42)}, schema)
	if idx != -1 {
		t.Errorf("idx = %d, want -1", idx)
	}
	if name != "" {
		t.Errorf("name = %q, want empty", name)
	}
}
