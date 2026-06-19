package render

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/fatih/color"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"

	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

// 输出格式常量。
const (
	FormatPretty   = "pretty"
	FormatVertical = "vertical"
	FormatJSON     = "json"
	FormatCSV      = "csv"
)

// SupportedFormats 是所有支持的输出格式列表。
var SupportedFormats = []string{FormatPretty, FormatVertical, FormatJSON, FormatCSV}

// IsValidFormat 判断格式名称是否合法。
func IsValidFormat(f string) bool {
	for _, s := range SupportedFormats {
		if s == f {
			return true
		}
	}
	return false
}

// Response 根据指定格式渲染响应。
// 错误响应统一返回 "错误: <message>"；无结果集的响应返回标量信息；
// 有结果集的响应按 format 指定的方式渲染。
func Response(resp *server.Response, format string) string {
	if resp.Code != 0 {
		return "错误: " + resp.Message
	}

	rows, hasRows := extractRows(resp.Data)
	if !hasRows {
		return renderScalar(resp)
	}

	cols := resultColumns(resp, rows)
	switch format {
	case FormatPretty:
		return renderPretty(cols, rows)
	case FormatVertical:
		return renderVertical(cols, rows)
	case FormatCSV:
		return renderCSV(cols, rows)
	case FormatJSON:
		return renderJSONRows(rows)
	default:
		return renderJSONRows(rows)
	}
}

// renderScalar 渲染无结果集的响应（写入、DDL、心跳等）。
func renderScalar(resp *server.Response) string {
	if resp.Data != nil {
		b, _ := json.Marshal(resp.Data)
		return string(b)
	}
	if resp.Message != "" {
		return resp.Message
	}
	if resp.Rows > 0 {
		return fmt.Sprintf("成功，影响 %d 行", resp.Rows)
	}
	return "成功"
}

// extractRows 从 Data 中提取行列表。
// 支持两种来源：
//   - 进程内调用（cmd/widb）：Data 为 []map[string]any
//   - 网络反序列化（cmd/cli）：Data 为 []interface{}，每个元素是 map[string]interface{}
//
// 空切片或非切片类型均返回 ok=false，交由 renderScalar 处理。
func extractRows(data any) ([]map[string]any, bool) {
	if data == nil {
		return nil, false
	}
	// 进程内调用路径：直接返回 []map[string]any
	if rows, ok := data.([]map[string]any); ok && len(rows) > 0 {
		return rows, true
	}
	// 网络反序列化路径：[]interface{} 中每个元素为 map[string]interface{}
	slice, ok := data.([]interface{})
	if !ok || len(slice) == 0 {
		return nil, false
	}
	rows := make([]map[string]any, 0, len(slice))
	for _, item := range slice {
		m, ok := item.(map[string]interface{})
		if !ok {
			return nil, false
		}
		rows = append(rows, m)
	}
	return rows, true
}

// resultColumns 确定结果集的列顺序。
// 优先使用响应中携带的 Columns（Schema 顺序）；否则从行数据中按字典序推导。
func resultColumns(resp *server.Response, rows []map[string]any) []string {
	if len(resp.Columns) > 0 {
		return resp.Columns
	}
	seen := make(map[string]bool)
	cols := make([]string, 0, len(rows[0]))
	for _, row := range rows {
		for k := range row {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}
	sort.Strings(cols)
	return cols
}

// renderPretty 以 ClickHouse 风格的圆角表格渲染。
func renderPretty(cols []string, rows []map[string]any) string {
	t := table.NewWriter()
	t.SetStyle(table.StyleRounded)
	t.Style().Format.Header = text.FormatDefault // 保留列名原始大小写
	header := make(table.Row, 0, len(cols))
	for _, c := range cols {
		header = append(header, c)
	}
	t.AppendHeader(header)
	for _, row := range rows {
		r := make(table.Row, 0, len(cols))
		for _, c := range cols {
			r = append(r, formatCell(row[c]))
		}
		t.AppendRow(r)
	}
	return t.Render()
}

// renderVertical 以垂直（行块）格式渲染，每行记录单独一块，适合宽表。
func renderVertical(cols []string, rows []map[string]any) string {
	var sb strings.Builder
	for i, row := range rows {
		fmt.Fprintf(&sb, "──[ Row %d ]──\n", i+1)
		for _, c := range cols {
			fmt.Fprintf(&sb, "%s: %s\n", c, cellToString(row[c]))
		}
		sb.WriteString("\n")
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

// renderCSV 以 CSV 格式渲染，首行为列名，后续每行一条记录。
func renderCSV(cols []string, rows []map[string]any) string {
	var sb strings.Builder
	sb.WriteString(strings.Join(cols, ","))
	sb.WriteString("\n")
	for _, row := range rows {
		vals := make([]string, 0, len(cols))
		for _, c := range cols {
			vals = append(vals, csvField(cellToString(row[c])))
		}
		sb.WriteString(strings.Join(vals, ","))
		sb.WriteString("\n")
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

// csvField 对字段进行 CSV 转义：含逗号、引号或换行时用双引号包裹。
func csvField(s string) string {
	if strings.ContainsAny(s, ",\"\n") {
		return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
	}
	return s
}

// renderJSONRows 以 JSON 数组格式渲染结果集，并附上行数。
func renderJSONRows(rows []map[string]any) string {
	b, _ := json.Marshal(rows)
	return fmt.Sprintf("%s (%d 行)", string(b), len(rows))
}

// formatCell 将单元格值格式化为 go-pretty 可显示的值，nil 显示为 "NULL"。
// 当全局颜色开关启用时，NULL 单元格使用黄色高亮，便于在宽表输出中快速识别。
func formatCell(v any) any {
	if v == nil {
		return colorizeNull("NULL")
	}
	return v
}

// cellToString 将单元格值转为字符串，nil 显示为 "NULL"。
// 字符串场景（如 CSV/Vertical）使用与 formatCell 一致的颜色处理。
func cellToString(v any) string {
	if v == nil {
		return colorizeNull("NULL")
	}
	switch val := v.(type) {
	case string:
		return val
	case fmt.Stringer:
		return val.String()
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// colorizeNull 在颜色启用时返回黄色 "NULL"，否则原样返回。
// 内部使用 fatih/color 的全局 NoColor 开关，遵守 NO_COLOR 环境变量。
func colorizeNull(s string) string {
	if color.NoColor {
		return s
	}
	return color.YellowString(s)
}
