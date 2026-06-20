package query

import "testing"

// TestPreprocessSQLEdgeCases 覆盖单遍扫描实现的边界条件：
//   - 大小写不敏感（\b 仍由边界检查保证）
//   - 整词边界：标识符子串（如 INT64_FOO）不应被替换
//   - 同 SQL 出现多个关键字
//   - BOOLEAN 是 BOOL 的别名
//   - 空 SQL / 短 SQL 不触发任何替换
//   - 数字字面量不触发关键字
//   - 关键字作为表/列名（非类型场景）也不应被错误替换
func TestPreprocessSQLEdgeCases(t *testing.T) {
	p := NewParser()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"short", "id", "id"},
		{"lower_int64", "CREATE TABLE t (c int64)", "CREATE TABLE t (c BIGINT)"},
		{"mixed_case", "CREATE TABLE t (c Int64)", "CREATE TABLE t (c BIGINT)"},
		{"boolean_alias", "CREATE TABLE t (c BOOLEAN)", "CREATE TABLE t (c TINYINT)"},
		{"boolean_lower", "create table t (c boolean)", "create table t (c TINYINT)"},
		// 整词边界：前后为字母/数字/下划线时不应替换
		{"int64_in_identifier", "SELECT int64_col FROM t", "SELECT int64_col FROM t"},
		{"int64_in_table", "SELECT * FROM int64table", "SELECT * FROM int64table"},
		// 注：字符串字面量内含关键字（'int64'）的替换语义与原 regex 实现一致：
		// 仅按词边界判断，不感知 SQL 语法，因此同样会被替换。
		{"int64_in_string_literal", "SELECT 'int64' AS x", "SELECT 'BIGINT' AS x"},
		{"bool_in_identifier", "SELECT bool_flag FROM t", "SELECT bool_flag FROM t"},
		{"string_in_identifier", "SELECT string_col FROM t", "SELECT string_col FROM t"},
		// 多个关键字共存
		{"multi_types", "CREATE TABLE t (a INT64, b FLOAT64, c STRING, d BOOL)", "CREATE TABLE t (a BIGINT, b DOUBLE, c TEXT, d TINYINT)"},
		// 数字字面量中含关键字
		{"number_literal", "SELECT 12345 FROM t", "SELECT 12345 FROM t"},
		// 单字符 SQL（无任何关键字首字符）
		{"single_char", "1", "1"},
		// 含首字符 b/i/f/s 但非关键字
		{"b_word", "SELECT * FROM blog", "SELECT * FROM blog"},
		{"i_word", "SELECT i FROM t", "SELECT i FROM t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.preprocessSQL(tt.input)
			if got != tt.want {
				t.Errorf("preprocessSQL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestSqlNeedsTypeRepl 验证前置快速判断的正确性：含 i/f/s/b 任意字节返回 true，
// 否则 false。空字符串与纯数字/标点 SQL 全部返回 false。
func TestSqlNeedsTypeRepl(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"empty", "", false},
		{"digits_only", "123456", false},
		{"punct_only", "(),.;", false},
		{"contains_i", "id", true},
		{"contains_I", "ID", true},
		{"contains_f", "from", true},
		{"contains_s", "SELECT", true},
		{"contains_b", "bool", true},
		{"contains_B", "BIGINT", true},
		{"no_keyword_letter", "1234 56.78", false},
		{"with_keyword", "create table x (a int64)", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sqlNeedsTypeRepl(tt.input); got != tt.want {
				t.Errorf("sqlNeedsTypeRepl(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestMapCustomTypeKeyword 验证关键字映射函数的字面量返回值，
// 避免后续维护时映射错误但单元测试侥幸通过。
func TestMapCustomTypeKeyword(t *testing.T) {
	tests := []struct {
		word string
		want string
		ok   bool
	}{
		{"INT64", "BIGINT", true},
		{"int64", "BIGINT", true},
		{"Int64", "BIGINT", true},
		{"FLOAT64", "DOUBLE", true},
		{"STRING", "TEXT", true},
		{"BOOL", "TINYINT", true},
		{"BOOLEAN", "TINYINT", true},
		{"bigint", "", false}, // 已是 MySQL 关键字，不映射
		{"int", "", false},    // 短词
		{"INT640", "", false}, // 长度不匹配
		{"int", "", false},    // 短词
		{"", "", false},       // 空串
		{"INT64X", "", false}, // 长度 6 但不是 STRING
	}
	for _, tt := range tests {
		t.Run(tt.word, func(t *testing.T) {
			got, ok := mapCustomTypeKeyword(tt.word)
			if ok != tt.ok {
				t.Errorf("mapCustomTypeKeyword(%q) ok = %v, want %v", tt.word, ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("mapCustomTypeKeyword(%q) = %q, want %q", tt.word, got, tt.want)
			}
		})
	}
}
