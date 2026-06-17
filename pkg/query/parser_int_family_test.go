package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestParseCreateTableIntFamilyTypes 验证整数族与 DATE 类型的 SQL 解析。
func TestParseCreateTableIntFamilyTypes(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want common.DataType
	}{
		{"bigint_unsigned_to_uint64", "CREATE TABLE t (c BIGINT UNSIGNED NOT NULL)", common.TypeUint64},
		{"tinyint_unsigned_to_int8", "CREATE TABLE t (c TINYINT UNSIGNED NOT NULL)", common.TypeInt8},
		{"smallint_to_int16", "CREATE TABLE t (c SMALLINT NOT NULL)", common.TypeInt16},
		{"mediumint_to_int32", "CREATE TABLE t (c MEDIUMINT NOT NULL)", common.TypeInt32},
		{"int_to_int64", "CREATE TABLE t (c INT NOT NULL)", common.TypeInt64},
		{"date_type", "CREATE TABLE t (c DATE NOT NULL)", common.TypeDate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewParser()
			stmt, err := p.Parse(tt.sql)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.sql, err)
			}
			ct, ok := stmt.(*CreateTableStatement)
			if !ok {
				t.Fatalf("expected *CreateTableStatement, got %T", stmt)
			}
			if len(ct.Columns) == 0 {
				t.Fatalf("expected at least 1 column")
			}
			if ct.Columns[0].Type != tt.want {
				t.Errorf("column type = %s, want %s", ct.Columns[0].Type, tt.want)
			}
		})
	}
}

// TestCoerceValueIntFamily 验证整数族类型间的 coerceValue 转换。
func TestCoerceValueIntFamily(t *testing.T) {
	tests := []struct {
		name    string
		input   common.Value
		target  common.DataType
		wantTyp common.DataType
		wantVal int64
	}{
		{"INT64 -> INT8", common.NewInt64(42), common.TypeInt8, common.TypeInt8, 42},
		{"INT64 -> INT16", common.NewInt64(100), common.TypeInt16, common.TypeInt16, 100},
		{"INT64 -> INT32", common.NewInt64(1000), common.TypeInt32, common.TypeInt32, 1000},
		{"INT64 -> UINT64", common.NewInt64(99), common.TypeUint64, common.TypeUint64, 99},
		{"INT64 -> DATE", common.NewInt64(19723), common.TypeDate, common.TypeDate, 19723},
		{"INT8 -> INT64", common.NewInt8(-5), common.TypeInt64, common.TypeInt64, -5},
		{"DATE -> INT32", common.NewDate(100), common.TypeInt32, common.TypeInt32, 100},
		{"UINT64 -> INT8", common.NewUint64(7), common.TypeInt8, common.TypeInt8, 7},
		{"BOOL -> INT64", common.NewBool(true), common.TypeInt64, common.TypeInt64, 1},
		{"FLOAT64 -> INT8", common.NewFloat64(3.7), common.TypeInt8, common.TypeInt8, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := coerceValue(tt.input, tt.target)
			if got.Typ != tt.wantTyp {
				t.Errorf("type = %s, want %s", got.Typ, tt.wantTyp)
			}
			if got.Int64 != tt.wantVal {
				t.Errorf("value = %d, want %d", got.Int64, tt.wantVal)
			}
		})
	}
}

// TestCoerceValueIntFamilyUnsupported 验证整数族目标拒绝字符串源值（返回原值）。
func TestCoerceValueIntFamilyUnsupported(t *testing.T) {
	orig := common.NewString("abc")
	got := coerceValue(orig, common.TypeInt8)
	if got.Typ != common.TypeString {
		t.Errorf("unsupported STRING->INT8: type = %s, want STRING (原值)", got.Typ)
	}
	if got.Str != "abc" {
		t.Errorf("unsupported STRING->INT8: value = %q, want %q", got.Str, "abc")
	}
}
