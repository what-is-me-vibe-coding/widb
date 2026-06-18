package render

import (
	"strings"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

// --- IsValidFormat ---

func TestIsValidFormat(t *testing.T) {
	for _, f := range SupportedFormats {
		if !IsValidFormat(f) {
			t.Errorf("IsValidFormat(%q) = false, want true", f)
		}
	}
	for _, f := range []string{"xml", "", "PRETTY", "Pretty"} {
		if IsValidFormat(f) {
			t.Errorf("IsValidFormat(%q) = true, want false", f)
		}
	}
}

// --- extractRows ---

func TestExtractRows(t *testing.T) {
	tests := []struct {
		name    string
		data    any
		wantOK  bool
		wantLen int
	}{
		{"nil", nil, false, 0},
		{"空切片", []interface{}{}, false, 0},
		{"非切片", map[string]interface{}{"k": "v"}, false, 0},
		{"元素非map", []interface{}{1, 2}, false, 0},
		{"单行", []interface{}{map[string]interface{}{"a": int64(1)}}, true, 1},
		{"多行", []interface{}{
			map[string]interface{}{"a": int64(1)},
			map[string]interface{}{"a": int64(2)},
		}, true, 2},
		{"进程内类型", []map[string]any{{"a": int64(1)}, {"a": int64(2)}}, true, 2},
		{"进程内空切片", []map[string]any{}, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, ok := extractRows(tt.data)
			if ok != tt.wantOK {
				t.Errorf("extractRows ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && len(rows) != tt.wantLen {
				t.Errorf("extractRows len = %d, want %d", len(rows), tt.wantLen)
			}
		})
	}
}

// --- resultColumns ---

func TestResultColumns(t *testing.T) {
	t.Run("使用响应Columns", func(t *testing.T) {
		resp := &server.Response{Columns: []string{"id", "name"}}
		rows := []map[string]any{{"name": "x", "id": int64(1)}}
		got := resultColumns(resp, rows)
		if len(got) != 2 || got[0] != "id" || got[1] != "name" {
			t.Errorf("resultColumns = %v, want [id name]", got)
		}
	})
	t.Run("从行推导按字典序", func(t *testing.T) {
		resp := &server.Response{}
		rows := []map[string]any{{"zebra": "z", "apple": "a", "mango": "m"}}
		got := resultColumns(resp, rows)
		want := []string{"apple", "mango", "zebra"}
		if len(got) != len(want) {
			t.Fatalf("len = %d, want %d", len(got), len(want))
		}
		for i, c := range want {
			if got[i] != c {
				t.Errorf("resultColumns[%d] = %q, want %q", i, got[i], c)
			}
		}
	})
}

// --- Response: 错误与标量 ---

func TestRenderResponseError(t *testing.T) {
	resp := &server.Response{Code: -1, Message: "表不存在"}
	for _, f := range SupportedFormats {
		if got := Response(resp, f); got != "错误: 表不存在" {
			t.Errorf("format %s: got %q, want %q", f, got, "错误: 表不存在")
		}
	}
}

func TestRenderResponseScalar(t *testing.T) {
	tests := []struct {
		name string
		resp *server.Response
		want string
	}{
		{"消息", &server.Response{Code: 0, Message: "pong"}, "pong"},
		{"行数", &server.Response{Code: 0, Rows: 5}, "成功，影响 5 行"},
		{"空", &server.Response{Code: 0}, "成功"},
		{"nil数据", &server.Response{Code: 0, Data: nil, Rows: 0}, "成功"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, f := range SupportedFormats {
				if got := Response(tt.resp, f); got != tt.want {
					t.Errorf("format %s: got %q, want %q", f, got, tt.want)
				}
			}
		})
	}
}

func TestRenderResponseMapData(t *testing.T) {
	resp := &server.Response{Code: 0, Data: map[string]interface{}{"key": "value"}, Rows: 0}
	for _, f := range SupportedFormats {
		got := Response(resp, f)
		if !strings.Contains(got, "key") {
			t.Errorf("format %s: 期望包含 'key', got %q", f, got)
		}
	}
}

// --- renderPretty ---

func TestRenderPretty(t *testing.T) {
	cols := []string{"id", "name"}
	rows := []map[string]any{
		{"id": int64(1), "name": "alice"},
		{"id": int64(2), "name": nil},
	}
	got := renderPretty(cols, rows)
	// 列名应保留原始大小写
	if !strings.Contains(got, "id") || !strings.Contains(got, "name") {
		t.Errorf("pretty 输出应包含列名: %q", got)
	}
	if !strings.Contains(got, "alice") {
		t.Errorf("pretty 输出应包含数据: %q", got)
	}
	if !strings.Contains(got, "NULL") {
		t.Errorf("pretty 输出应包含 NULL: %q", got)
	}
}

// --- renderVertical ---

func TestRenderVertical(t *testing.T) {
	cols := []string{"id", "name"}
	rows := []map[string]any{
		{"id": int64(1), "name": "alice"},
	}
	got := renderVertical(cols, rows)
	if !strings.Contains(got, "Row 1") {
		t.Errorf("vertical 输出应包含 'Row 1': %q", got)
	}
	if !strings.Contains(got, "id: 1") {
		t.Errorf("vertical 输出应包含 'id: 1': %q", got)
	}
	if !strings.Contains(got, "name: alice") {
		t.Errorf("vertical 输出应包含 'name: alice': %q", got)
	}
}

// --- renderCSV ---

func TestRenderCSV(t *testing.T) {
	cols := []string{"id", "name"}
	rows := []map[string]any{
		{"id": int64(1), "name": "alice"},
		{"id": int64(2), "name": "bob, jr"},
	}
	got := renderCSV(cols, rows)
	if !strings.HasPrefix(got, "id,name") {
		t.Errorf("csv 首行应为列名: %q", got)
	}
	if !strings.Contains(got, "1,alice") {
		t.Errorf("csv 应包含数据行: %q", got)
	}
	// 含逗号的字段应被转义
	if !strings.Contains(got, "\"bob, jr\"") {
		t.Errorf("csv 应转义含逗号字段: %q", got)
	}
}

func TestCSVField(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"simple", "simple"},
		{"a,b", "\"a,b\""},
		{"a\"b", "\"a\"\"b\""},
		{"line1\nline2", "\"line1\nline2\""},
	}
	for _, tt := range tests {
		if got := csvField(tt.in); got != tt.want {
			t.Errorf("csvField(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// --- renderJSONRows ---

func TestRenderJSONRows(t *testing.T) {
	rows := []map[string]any{
		{"id": int64(1), "name": "alice"},
	}
	got := renderJSONRows(rows)
	if !strings.Contains(got, "1 行") {
		t.Errorf("json 输出应包含行数: %q", got)
	}
	if !strings.Contains(got, "alice") {
		t.Errorf("json 输出应包含数据: %q", got)
	}
}

// --- cellToString / formatCell ---

func TestCellToString(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"nil", nil, "NULL"},
		{"字符串", "hello", "hello"},
		{"整数", int64(42), "42"},
		{"浮点", float64(3.14), "3.14"},
		{"布尔", true, "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cellToString(tt.v); got != tt.want {
				t.Errorf("cellToString(%v) = %q, want %q", tt.v, got, tt.want)
			}
		})
	}
}

func TestFormatCell(t *testing.T) {
	if got := formatCell(nil); got != "NULL" {
		t.Errorf("formatCell(nil) = %v, want NULL", got)
	}
	if got := formatCell(int64(1)); got != int64(1) {
		t.Errorf("formatCell(1) = %v, want 1", got)
	}
}
