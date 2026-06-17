package query

import "testing"

// TestParseShowTables 验证 SHOW TABLES 解析为 ShowTablesStatement。
func TestParseShowTables(t *testing.T) {
	cases := []string{"SHOW TABLES", "show tables", "  SHOW   TABLES  "}
	for _, sql := range cases {
		p := NewParser()
		stmt, err := p.Parse(sql)
		if err != nil {
			t.Fatalf("Parse(%q): %v", sql, err)
		}
		st, ok := stmt.(*ShowTablesStatement)
		if !ok {
			t.Fatalf("Parse(%q): expected *ShowTablesStatement, got %T", sql, stmt)
		}
		if st.String() != "SHOW TABLES" {
			t.Errorf("String() = %q, want %q", st.String(), "SHOW TABLES")
		}
	}
}

// TestParseDescribe 验证 DESCRIBE / DESC 解析为 DescribeStatement。
func TestParseDescribe(t *testing.T) {
	cases := []struct {
		sql   string
		table string
	}{
		{"DESCRIBE t", "t"},
		{"DESC t", "t"},
		{"describe `t`", "t"},
		{"DESC t;", "t"},
		{"  DESCRIBE  my_table  ", "my_table"},
	}
	for _, c := range cases {
		p := NewParser()
		stmt, err := p.Parse(c.sql)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.sql, err)
		}
		desc, ok := stmt.(*DescribeStatement)
		if !ok {
			t.Fatalf("Parse(%q): expected *DescribeStatement, got %T", c.sql, stmt)
		}
		if desc.Table != c.table {
			t.Errorf("Parse(%q): Table = %q, want %q", c.sql, desc.Table, c.table)
		}
	}
}

// TestParseDescribeEmpty 验证 DESCRIBE 后无表名时交由 sqlparser 报错。
func TestParseDescribeEmpty(t *testing.T) {
	p := NewParser()
	if _, err := p.Parse("DESCRIBE"); err == nil {
		t.Error("DESCRIBE 无表名应报错")
	}
}

// TestParseDeleteStatement 验证 DELETE 语句解析。
func TestParseDeleteStatement(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("DELETE FROM t WHERE id = 5")
	if err != nil {
		t.Fatalf("Parse DELETE: %v", err)
	}
	del, ok := stmt.(*DeleteStatement)
	if !ok {
		t.Fatalf("expected *DeleteStatement, got %T", stmt)
	}
	if del.Table != "t" {
		t.Errorf("Table = %q, want %q", del.Table, "t")
	}
	if del.Where == nil {
		t.Error("expected WHERE clause")
	}
}

// TestParseDeleteNoWhere 验证无 WHERE 的 DELETE 解析（全表删除）。
func TestParseDeleteNoWhere(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("DELETE FROM t")
	if err != nil {
		t.Fatalf("Parse DELETE: %v", err)
	}
	del, ok := stmt.(*DeleteStatement)
	if !ok {
		t.Fatalf("expected *DeleteStatement, got %T", stmt)
	}
	if del.Where != nil {
		t.Error("expected nil WHERE clause")
	}
}

// TestParseDropTableIfExists 验证 DROP TABLE IF EXISTS 解析。
func TestParseDropTableIfExists(t *testing.T) {
	p := NewParser()
	stmt, err := p.Parse("DROP TABLE IF EXISTS t")
	if err != nil {
		t.Fatalf("Parse DROP TABLE IF EXISTS: %v", err)
	}
	dt, ok := stmt.(*DropTableStatement)
	if !ok {
		t.Fatalf("expected *DropTableStatement, got %T", stmt)
	}
	if !dt.IfExists {
		t.Error("IfExists = false, want true")
	}
}
