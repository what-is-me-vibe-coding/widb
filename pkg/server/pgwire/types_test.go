package pgwire

import (
	"testing"
)

// TestInferTypeFromValue 验证从 Go 值推断 PG 类型的逻辑。
func TestInferTypeFromValue(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want pgType
	}{
		{"bool true", true, pgType{OID: OIDBool, Size: 1}},
		{"bool false", false, pgType{OID: OIDBool, Size: 1}},
		{"int64", int64(42), pgType{OID: OIDInt8, Size: 8}},
		{"int64 negative", int64(-1), pgType{OID: OIDInt8, Size: 8}},
		{"float64", float64(3.14), pgType{OID: OIDFloat8, Size: 8}},
		{"float64 zero", float64(0), pgType{OID: OIDFloat8, Size: 8}},
		{"string", "hello", pgType{OID: OIDText, Size: -1}},
		{"string empty", "", pgType{OID: OIDText, Size: -1}},
		{"nil falls to default", nil, defaultType},
		{"unknown type falls to default", []byte{1, 2}, defaultType},
		{"int not int64 falls to default", 42, defaultType},
		{"float32 not float64 falls to default", float32(1.0), defaultType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferTypeFromValue(tt.val)
			if got != tt.want {
				t.Errorf("inferTypeFromValue(%v) = %+v, want %+v", tt.val, got, tt.want)
			}
		})
	}
}

// TestInferColumnTypes 验证从结果行推断每列类型。
func TestInferColumnTypes(t *testing.T) {
	t.Run("empty columns", func(t *testing.T) {
		got := inferColumnTypes(nil, nil)
		if len(got) != 0 {
			t.Errorf("期望空切片, got %v", got)
		}
	})

	t.Run("no rows uses default", func(t *testing.T) {
		cols := []string{"a", "b"}
		got := inferColumnTypes(cols, nil)
		if len(got) != 2 {
			t.Fatalf("期望 2 个类型, got %d", len(got))
		}
		for _, ty := range got {
			if ty != defaultType {
				t.Errorf("期望默认类型, got %+v", ty)
			}
		}
	})

	t.Run("infer from first non-nil value", func(t *testing.T) {
		cols := []string{"id", "name", "score", "flag"}
		rows := []map[string]any{
			{"id": int64(1), "name": nil, "score": float64(9.5), "flag": true},
			{"id": int64(2), "name": "alice", "score": nil, "flag": false},
		}
		got := inferColumnTypes(cols, rows)
		if got[0].OID != OIDInt8 {
			t.Errorf("列 id 期望 OIDInt8, got %d", got[0].OID)
		}
		if got[1].OID != OIDText {
			t.Errorf("列 name 期望 OIDText, got %d", got[1].OID)
		}
		if got[2].OID != OIDFloat8 {
			t.Errorf("列 score 期望 OIDFloat8, got %d", got[2].OID)
		}
		if got[3].OID != OIDBool {
			t.Errorf("列 flag 期望 OIDBool, got %d", got[3].OID)
		}
	})

	t.Run("all nil values uses default", func(t *testing.T) {
		cols := []string{"a"}
		rows := []map[string]any{
			{"a": nil},
			{"a": nil},
		}
		got := inferColumnTypes(cols, rows)
		if got[0] != defaultType {
			t.Errorf("期望默认类型, got %+v", got[0])
		}
	})

	t.Run("missing column key uses default", func(t *testing.T) {
		cols := []string{"missing"}
		rows := []map[string]any{
			{"other": int64(1)},
		}
		got := inferColumnTypes(cols, rows)
		if got[0] != defaultType {
			t.Errorf("缺失列应使用默认类型, got %+v", got[0])
		}
	})
}

// TestPGOIDConstants 验证 OID 常量值符合 PostgreSQL 标准。
func TestPGOIDConstants(t *testing.T) {
	if OIDBool != 16 {
		t.Errorf("OIDBool 期望 16, got %d", OIDBool)
	}
	if OIDInt8 != 20 {
		t.Errorf("OIDInt8 期望 20, got %d", OIDInt8)
	}
	if OIDText != 25 {
		t.Errorf("OIDText 期望 25, got %d", OIDText)
	}
	if OIDFloat8 != 701 {
		t.Errorf("OIDFloat8 期望 701, got %d", OIDFloat8)
	}
	if OIDTimestamp != 1114 {
		t.Errorf("OIDTimestamp 期望 1114, got %d", OIDTimestamp)
	}
}
