package query

import (
	"strings"
	"testing"
)

// TestParseExplainSelect 验证 EXPLAIN SELECT 解析为 ExplainStatement，
// 且内部语句被正确解析为 SelectStatement。
func TestParseExplainSelect(t *testing.T) {
	cases := []struct {
		sql   string
		inner string
	}{
		{"explain select * from test2", "SELECT * FROM test2"},
		{"EXPLAIN SELECT * FROM test2", "SELECT * FROM test2"},
		{"  explain   select * from t  ", "SELECT * FROM t"},
		{"explain select id, name from t where id > 10", "SELECT id, name FROM t WHERE (id > 10)"},
		{"explain select * from t limit 5;", "SELECT * FROM t LIMIT 5"},
	}
	for _, c := range cases {
		p := NewParser()
		stmt, err := p.Parse(c.sql)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.sql, err)
		}
		exp, ok := stmt.(*ExplainStatement)
		if !ok {
			t.Fatalf("Parse(%q): expected *ExplainStatement, got %T", c.sql, stmt)
		}
		sel, ok := exp.Inner.(*SelectStatement)
		if !ok {
			t.Fatalf("Parse(%q): expected inner *SelectStatement, got %T", c.sql, exp.Inner)
		}
		if sel.String() != c.inner {
			t.Errorf("Parse(%q): inner = %q, want %q", c.sql, sel.String(), c.inner)
		}
		wantStr := "EXPLAIN " + c.inner
		if exp.String() != wantStr {
			t.Errorf("Parse(%q): String() = %q, want %q", c.sql, exp.String(), wantStr)
		}
	}
}

// TestParseExplainErrors 验证 EXPLAIN 的各类错误场景返回清晰的错误信息。
func TestParseExplainErrors(t *testing.T) {
	cases := []struct {
		sql       string
		errSubstr string
	}{
		{"EXPLAIN", "缺少待解释的语句"},
		{"explain   ", "缺少待解释的语句"},
		{"explain insert into t values(1)", "仅支持 SELECT"},
		{"explain update t set a=1", "仅支持 SELECT"},
		{"explain delete from t", "仅支持 SELECT"},
		{"explain create table t (a int)", "仅支持 SELECT"},
		{"explain drop table t", "仅支持 SELECT"},
		{"explain show tables", "仅支持 SELECT"},
		{"explain select from t", "内部语句"},
		{"explain select * from", "内部语句"},
	}
	for _, c := range cases {
		p := NewParser()
		_, err := p.Parse(c.sql)
		if err == nil {
			t.Errorf("Parse(%q): 期望错误但成功", c.sql)
			continue
		}
		if !strings.Contains(err.Error(), c.errSubstr) {
			t.Errorf("Parse(%q): 错误 %q 未包含 %q", c.sql, err.Error(), c.errSubstr)
		}
	}
}

// TestParseExplainDoesNotAffectSelect 验证 EXPLAIN 关键字拦截不影响普通 SELECT。
func TestParseExplainDoesNotAffectSelect(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("select * from t")
	if err != nil {
		t.Fatalf("Parse select: %v", err)
	}
	if _, ok := stmt.(*SelectStatement); !ok {
		t.Fatalf("expected *SelectStatement, got %T", stmt)
	}
}

// TestParseExplainColumnsLikeExplain 验证列名以 explain 开头的查询不被误判为 EXPLAIN 语句。
// 例如 "select explain from t" 中 explain 是列名而非关键字。
func TestParseExplainAsColumnName(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("select explain from t")
	// explain 作为列名时，sqlparser 可能解析成功或失败，这里不强制要求成功，
	// 但若成功则不应是 ExplainStatement。
	if err != nil {
		return
	}
	if _, ok := stmt.(*ExplainStatement); ok {
		t.Error("select explain from t 不应被解析为 ExplainStatement")
	}
}
