package server

import (
	"fmt"
	"strings"

	"github.com/what-is-me-vibe-coding/test-db/pkg/server/pgwire"
)

// pgwireAdapter 适配 Server 以实现 pgwire.SQLExecutor 接口。
type pgwireAdapter struct {
	server *Server
}

// ExecuteSQL 实现 pgwire.SQLExecutor 接口，通过进程内调用执行 SQL。
// 慢查询日志中的 source 标记为 SlowQuerySourcePGWire，便于与 HTTP/TCP 区分。
func (a *pgwireAdapter) ExecuteSQL(sql string) (*pgwire.SQLResult, error) {
	resp, err := a.server.handleQuerySource(SlowQuerySourcePGWire, &QueryRequest{SQL: sql})
	if err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("%s", resp.Message)
	}

	result := &pgwire.SQLResult{
		Columns:      resp.Columns,
		RowsAffected: resp.Rows,
		CommandTag:   buildCommandTag(sql, resp),
	}

	// 透传列类型，使 pgwire 能生成准确的 RowDescription（DATE/TIMESTAMP/INT 等）。
	if len(resp.ColumnTypes) > 0 {
		result.ColumnTypes = make([]int, len(resp.ColumnTypes))
		for i, t := range resp.ColumnTypes {
			result.ColumnTypes[i] = int(t)
		}
	}

	if resp.Data != nil {
		if rows, ok := resp.Data.([]map[string]any); ok {
			result.Rows = rows
			result.IsQuery = len(resp.Columns) > 0
		}
	}
	return result, nil
}

// buildCommandTag 根据 SQL 语句和响应推断 PG 协议命令标签。
func buildCommandTag(sql string, resp *Response) string {
	upper := strings.ToUpper(strings.TrimSpace(sql))
	switch {
	case strings.HasPrefix(upper, "SELECT"):
		return fmt.Sprintf("SELECT %d", resp.Rows)
	case strings.HasPrefix(upper, "EXPLAIN"):
		return fmt.Sprintf("EXPLAIN %d", resp.Rows)
	case strings.HasPrefix(upper, "INSERT"):
		return fmt.Sprintf("INSERT 0 %d", resp.Rows)
	case strings.HasPrefix(upper, "CREATE TABLE"):
		return "CREATE TABLE"
	case strings.HasPrefix(upper, "CREATE"):
		return "CREATE"
	case strings.HasPrefix(upper, "DROP"):
		return "DROP"
	case strings.HasPrefix(upper, "DELETE"):
		return fmt.Sprintf("DELETE %d", resp.Rows)
	case strings.HasPrefix(upper, "UPDATE"):
		return fmt.Sprintf("UPDATE %d", resp.Rows)
	default:
		return "OK"
	}
}
