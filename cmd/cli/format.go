// Package main 提供 widb-cli 的多格式结果渲染能力。
// 支持 pretty（ClickHouse 风格表格）、vertical（垂直行块）、json、csv 四种格式。
package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

// 输出格式常量。
const (
	formatPretty   = "pretty"
	formatVertical = "vertical"
	formatJSON     = "json"
	formatCSV      = "csv"
)

// supportedFormats 是所有支持的输出格式列表。
var supportedFormats = []string{formatPretty, formatVertical, formatJSON, formatCSV}

// isValidFormat 判断格式名称是否合法。
func isValidFormat(f string) bool {
	for _, s := range supportedFormats {
		if s == f {
			return true
		}
	}
	return false
}

// formatResponse 以 JSON 格式渲染响应，保留原行为用于向后兼容。
func formatResponse(resp *server.Response) string {
	return renderResponse(resp, formatJSON)
}

// renderResponse 根据指定格式渲染响应。
// 错误响应统一返回 "错误: <message>"；无结果集的响应返回标量信息；
// 有结果集的响应按 format 指定的方式渲染。
func renderResponse(resp *server.Response, format string) string {
	if resp.Code != 0 {
		return "错误: " + resp.Message
	}

	rows, hasRows := extractRows(resp.Data)
	if !hasRows {
		return renderScalar(resp)
	}

	cols := resultColumns(resp, rows)
	switch format {
	case formatPretty:
		return renderPretty(cols, rows)
	case formatVertical:
		return renderVertical(cols, rows)
	case formatCSV:
		return renderCSV(cols, rows)
	case formatJSON:
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
// Data 必须是 []interface{} 且每个元素是 map[string]interface{} 才视为结果集；
// 空切片或非切片类型均返回 ok=false，交由 renderScalar 处理。
func extractRows(data any) ([]map[string]any, bool) {
	if data == nil {
		return nil, false
	}
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
func formatCell(v any) any {
	if v == nil {
		return "NULL"
	}
	return v
}

// cellToString 将单元格值转为字符串，nil 显示为 "NULL"。
func cellToString(v any) string {
	if v == nil {
		return "NULL"
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
