package render

import (
	"strings"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

// 本文件补充 render.Response 顶层分发函数的覆盖率：
// 现有测试覆盖了错误/标量路径与各 render* 子函数，但未通过 Response
// 直接驱动带结果集的各格式分支（pretty/vertical/csv/json/default）。

// sampleRowData 构造进程内调用的结果集数据（[]map[string]any）。
func sampleRowData() []map[string]any {
	return []map[string]any{
		{"id": int64(1), "name": "alice"},
		{"id": int64(2), "name": nil},
	}
}

func TestResponsePrettyFormat(t *testing.T) {
	resp := &server.Response{Code: 0, Columns: []string{"id", "name"}, Data: sampleRowData(), Rows: 2}
	got := Response(resp, FormatPretty)
	for _, want := range []string{"id", "name", "alice", "NULL"} {
		if !strings.Contains(got, want) {
			t.Errorf("pretty 输出应包含 %q, 得到: %s", want, got)
		}
	}
}

func TestResponseVerticalFormat(t *testing.T) {
	resp := &server.Response{Code: 0, Columns: []string{"id", "name"}, Data: sampleRowData(), Rows: 2}
	got := Response(resp, FormatVertical)
	for _, want := range []string{"Row 1", "Row 2", "id: 1", "name: alice", "name: NULL"} {
		if !strings.Contains(got, want) {
			t.Errorf("vertical 输出应包含 %q, 得到: %s", want, got)
		}
	}
}

func TestResponseCSVFormat(t *testing.T) {
	resp := &server.Response{Code: 0, Columns: []string{"id", "name"}, Data: sampleRowData(), Rows: 2}
	got := Response(resp, FormatCSV)
	if !strings.HasPrefix(got, "id,name") {
		t.Errorf("csv 首行应为列名, 得到: %s", got)
	}
	if !strings.Contains(got, "1,alice") {
		t.Errorf("csv 应包含数据行 1,alice, 得到: %s", got)
	}
}

func TestResponseJSONFormat(t *testing.T) {
	resp := &server.Response{Code: 0, Columns: []string{"id", "name"}, Data: sampleRowData(), Rows: 2}
	got := Response(resp, FormatJSON)
	if !strings.Contains(got, "2 行") {
		t.Errorf("json 输出应包含行数, 得到: %s", got)
	}
	if !strings.Contains(got, "alice") {
		t.Errorf("json 输出应包含数据, 得到: %s", got)
	}
}

// TestResponseUnknownFormatFallsBackToJSON 验证未知格式回退到 JSON 渲染（default 分支）。
func TestResponseUnknownFormatFallsBackToJSON(t *testing.T) {
	resp := &server.Response{Code: 0, Columns: []string{"id", "name"}, Data: sampleRowData(), Rows: 2}
	got := Response(resp, "xml")
	if !strings.Contains(got, "alice") {
		t.Errorf("未知格式应回退到 JSON 渲染并包含数据, 得到: %s", got)
	}
}

// TestResponseNetworkDeserializedRows 验证网络反序列化路径（[]interface{}）的结果集渲染。
func TestResponseNetworkDeserializedRows(t *testing.T) {
	data := []interface{}{
		map[string]interface{}{"id": int64(1), "name": "bob"},
	}
	resp := &server.Response{Code: 0, Columns: []string{"id", "name"}, Data: data, Rows: 1}
	for _, f := range SupportedFormats {
		got := Response(resp, f)
		if !strings.Contains(got, "bob") {
			t.Errorf("format %s: 应包含数据 bob, 得到: %s", f, got)
		}
	}
}

// TestResponseColumnsOrderPreserved 验证 Response 使用响应携带的 Columns 顺序渲染，
// 而非按字典序推导。
func TestResponseColumnsOrderPreserved(t *testing.T) {
	resp := &server.Response{
		Code:    0,
		Columns: []string{"name", "id"},
		Data:    []map[string]any{{"id": int64(1), "name": "alice"}},
		Rows:    1,
	}
	got := Response(resp, FormatCSV)
	// 列名行应保持 name,id 顺序
	if !strings.HasPrefix(got, "name,id") {
		t.Errorf("应按 Columns 顺序渲染列名, 得到: %s", got)
	}
}
