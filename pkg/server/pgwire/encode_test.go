package pgwire

import (
	"bytes"
	"strconv"
	"testing"

	"github.com/jackc/pgproto3/v2"
)

// TestEncodeValue 验证 Go 值到 PG 文本格式的编码。
func TestEncodeValue(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want []byte
	}{
		{"nil returns nil", nil, nil},
		{"bool true", true, []byte("t")},
		{"bool false", false, []byte("f")},
		{"int64 positive", int64(42), []byte("42")},
		{"int64 negative", int64(-100), []byte("-100")},
		{"int64 zero", int64(0), []byte("0")},
		{"int64 max", int64(9223372036854775807), []byte("9223372036854775807")},
		{"float64", float64(3.14), []byte("3.14")},
		{"float64 zero", float64(0), []byte("0")},
		{"float64 negative", float64(-1.5), []byte("-1.5")},
		{"string", "hello", []byte("hello")},
		{"string empty", "", []byte("")},
		{"string unicode", "中文", []byte("中文")},
		{"unknown type uses fmt", 42, []byte("42")},
		{"slice uses fmt", []int{1, 2}, []byte("[1 2]")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := encodeValue(tt.val)
			if tt.want == nil {
				if got != nil {
					t.Errorf("encodeValue(%v) 期望 nil, got %v", tt.val, got)
				}
				return
			}
			if !bytes.Equal(got, tt.want) {
				t.Errorf("encodeValue(%v) = %q, want %q", tt.val, got, tt.want)
			}
		})
	}
}

// TestBuildRowDescription 验证 RowDescription 消息构建。
func TestBuildRowDescription(t *testing.T) {
	t.Run("empty columns", func(t *testing.T) {
		rd := buildRowDescription(nil, nil)
		if len(rd.Fields) != 0 {
			t.Errorf("期望 0 个字段, got %d", len(rd.Fields))
		}
	})

	t.Run("multiple columns with types", func(t *testing.T) {
		cols := []string{"id", "name", "score", "active"}
		types := []pgType{
			{OID: OIDInt8, Size: 8},
			{OID: OIDText, Size: -1},
			{OID: OIDFloat8, Size: 8},
			{OID: OIDBool, Size: 1},
		}
		rd := buildRowDescription(cols, types)
		if len(rd.Fields) != 4 {
			t.Fatalf("期望 4 个字段, got %d", len(rd.Fields))
		}
		for i, col := range cols {
			f := rd.Fields[i]
			if string(f.Name) != col {
				t.Errorf("字段 %d 名称期望 %s, got %s", i, col, f.Name)
			}
			if f.DataTypeOID != types[i].OID {
				t.Errorf("字段 %d OID 期望 %d, got %d", i, types[i].OID, f.DataTypeOID)
			}
			if f.DataTypeSize != types[i].Size {
				t.Errorf("字段 %d Size 期望 %d, got %d", i, types[i].Size, f.DataTypeSize)
			}
			if f.Format != pgproto3.TextFormat {
				t.Errorf("字段 %d Format 期望 TextFormat", i)
			}
		}
	})
}

// TestBuildDataRow 验证 DataRow 消息构建。
func TestBuildDataRow(t *testing.T) {
	t.Run("empty columns", func(t *testing.T) {
		dr := buildDataRow(map[string]any{}, nil)
		if len(dr.Values) != 0 {
			t.Errorf("期望 0 个值, got %d", len(dr.Values))
		}
	})

	t.Run("row with mixed types and nil", func(t *testing.T) {
		cols := []string{"id", "name", "score", "active", "note"}
		row := map[string]any{
			"id":     int64(1),
			"name":   "alice",
			"score":  float64(9.5),
			"active": true,
			"note":   nil,
		}
		dr := buildDataRow(row, cols)
		if len(dr.Values) != 5 {
			t.Fatalf("期望 5 个值, got %d", len(dr.Values))
		}
		if string(dr.Values[0]) != "1" {
			t.Errorf("id 期望 '1', got %q", dr.Values[0])
		}
		if string(dr.Values[1]) != "alice" {
			t.Errorf("name 期望 'alice', got %q", dr.Values[1])
		}
		if string(dr.Values[2]) != "9.5" {
			t.Errorf("score 期望 '9.5', got %q", dr.Values[2])
		}
		if string(dr.Values[3]) != "t" {
			t.Errorf("active 期望 't', got %q", dr.Values[3])
		}
		if dr.Values[4] != nil {
			t.Errorf("note 期望 nil, got %v", dr.Values[4])
		}
	})

	t.Run("missing column key encodes as nil", func(t *testing.T) {
		cols := []string{"missing"}
		row := map[string]any{"other": int64(1)}
		dr := buildDataRow(row, cols)
		if dr.Values[0] != nil {
			t.Errorf("缺失列应编码为 nil, got %v", dr.Values[0])
		}
	})
}

// TestEncodeValueFloatFormats 验证浮点数编码格式符合 PG 文本协议。
func TestEncodeValueFloatFormats(t *testing.T) {
	// PG float8 文本格式使用最短表示
	tests := []struct {
		val  float64
		want string
	}{
		{0.1, "0.1"},
		{1e10, "1e+10"},
		{1e-10, "1e-10"},
		{1.0, "1"},
	}
	for _, tt := range tests {
		got := encodeValue(tt.val)
		if string(got) != tt.want {
			t.Errorf("encodeValue(%v) = %q, want %q", tt.val, got, tt.want)
		}
	}
}

// TestBuildRowDescriptionLargeInt 验证大整数编码。
func TestBuildRowDescriptionLargeInt(t *testing.T) {
	big := int64(1) << 62
	got := encodeValue(big)
	want := []byte(strconv.FormatInt(big, 10))
	if !bytes.Equal(got, want) {
		t.Errorf("大整数编码错误: got %q, want %q", got, want)
	}
}
