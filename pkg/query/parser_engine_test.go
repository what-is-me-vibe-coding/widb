package query

import "testing"

// TestParseCreateTableEngine 验证 CREATE TABLE ... ENGINE=<name> 选项解析。
func TestParseCreateTableEngine(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		wantEng string
	}{
		{"memory引擎", "CREATE TABLE t (id BIGINT NOT NULL, PRIMARY KEY(id)) ENGINE=memory", "memory"},
		{"lsm引擎", "CREATE TABLE t (id BIGINT NOT NULL, PRIMARY KEY(id)) ENGINE=lsm", "lsm"},
		{"大写MEMORY", "CREATE TABLE t (id BIGINT NOT NULL, PRIMARY KEY(id)) ENGINE=MEMORY", "memory"},
		{"带空格", "CREATE TABLE t (id BIGINT NOT NULL, PRIMARY KEY(id)) ENGINE = memory", "memory"},
		{"未指定引擎", "CREATE TABLE t (id BIGINT NOT NULL, PRIMARY KEY(id))", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := NewParser().Parse(tt.sql)
			if err != nil {
				t.Fatalf("Parse 失败: %v", err)
			}
			ct, ok := stmt.(*CreateTableStatement)
			if !ok {
				t.Fatalf("期望 *CreateTableStatement，得到 %T", stmt)
			}
			if ct.Engine != tt.wantEng {
				t.Errorf("Engine 期望 %q，得到 %q", tt.wantEng, ct.Engine)
			}
		})
	}
}

// TestCreateTableStatementStringWithEngine 验证带 ENGINE 的 String() 输出。
func TestCreateTableStatementStringWithEngine(t *testing.T) {
	s := &CreateTableStatement{
		Table:      "t",
		Columns:    []ColumnDef{{Name: "id", Type: 2, Nullable: false}},
		PrimaryKey: []string{"id"},
		Engine:     "memory",
	}
	got := s.String()
	if want := "ENGINE=memory"; !contains(got, want) {
		t.Errorf("String() 期望包含 %q，得到 %q", want, got)
	}
}

// TestExtractEngine 验证 extractEngine 正则提取逻辑。
func TestExtractEngine(t *testing.T) {
	tests := []struct {
		sql  string
		want string
	}{
		{"CREATE TABLE t (id INT) ENGINE=memory", "memory"},
		{"CREATE TABLE t (id INT) ENGINE=lsm", "lsm"},
		{"CREATE TABLE t (id INT) ENGINE = memory", "memory"},
		{"CREATE TABLE t (id INT) engine=Memory", "memory"},
		{"CREATE TABLE t (id INT)", ""},
		{"CREATE TABLE t (id INT) DEFAULT CHARSET=utf8", ""},
	}
	for _, tt := range tests {
		if got := extractEngine(tt.sql); got != tt.want {
			t.Errorf("extractEngine(%q) = %q, want %q", tt.sql, got, tt.want)
		}
	}
}

// contains 是简单的子串包含判断，避免引入 strings 包。
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
