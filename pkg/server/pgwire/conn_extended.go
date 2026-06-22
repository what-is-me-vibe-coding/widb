package pgwire

import (
	"fmt"
	"log"
	"strings"

	"github.com/jackc/pgproto3/v2"
)

// extendedStmt 保存一个已 Parse 的 prepared statement。
// 简化实现：仅缓存 SQL 文本。Parse 时若原 statement 同名已存在则替换。
// 内部使用空串 "" 表示 "unnamed statement"，对应客户端常用的
// Parse("", query, ...) 调用模式。
type extendedStmt struct {
	sql string
}

// extendedPortal 保存一个已 Bind 的 portal。
// 简化实现：缓存关联的 SQL 文本（自 prepared statement 复制）。
// 忽略 Bind 携带的参数值，因为我们底层的 SQL 解析器不支持占位符；
// 实际查询时直接使用 prepared statement 的 SQL 文本执行。
// 内部使用空串 "" 表示 "unnamed portal"。
type extendedPortal struct {
	sql string
}

// consumeExtError 检查并返回当前是否处于 extended query 错误状态。
// 当 extErr 非 nil 时返回 true，表示应丢弃后续消息；并在首次被错误 Sync
// 之外的客户端读取时保留错误供可能的 ErrorResponse 报告。
// 实现逻辑：若 extErr 非 nil，返回 true；否则返回 false。
func (h *connHandler) consumeExtError() bool {
	h.extMu.Lock()
	defer h.extMu.Unlock()
	return h.extErr != nil
}

// setExtError 记录 extended query 错误状态。
// 错误状态会在下一次 Sync 时被清除（PG 协议要求）。
func (h *connHandler) setExtError(err error) {
	h.extMu.Lock()
	h.extErr = err
	h.extMu.Unlock()
}

// handleParse 处理 Parse 消息：缓存 SQL 文本到 prepared statement 映射。
// 空 SQL 也被允许（PG 规范），但会替换同名已存在的 statement。
func (h *connHandler) handleParse(m *pgproto3.Parse) {
	h.extMu.Lock()
	h.extStmts[m.Name] = &extendedStmt{sql: m.Query}
	h.extMu.Unlock()
	if err := h.send(&pgproto3.ParseComplete{}); err != nil {
		log.Printf("pgwire: send parse complete: %v", err)
	}
}

// handleBind 处理 Bind 消息：将 prepared statement 的 SQL 复制到 portal。
// 简化实现：忽略参数值（pgwire 内 SQL 解析器不支持占位符）。
// 若引用的 prepared statement 不存在，则进入错误状态。
func (h *connHandler) handleBind(m *pgproto3.Bind) {
	h.extMu.Lock()
	stmt, ok := h.extStmts[m.PreparedStatement]
	if !ok {
		h.extMu.Unlock()
		err := fmt.Errorf("prepared statement %q 不存在", m.PreparedStatement)
		h.sendError(err)
		h.setExtError(err)
		return
	}
	h.extPortals[m.DestinationPortal] = &extendedPortal{sql: stmt.sql}
	h.extMu.Unlock()
	if err := h.send(&pgproto3.BindComplete{}); err != nil {
		log.Printf("pgwire: send bind complete: %v", err)
	}
}

// handleDescribe 处理 Describe 消息：返回 parameter description 与 RowDescription/NoData。
// 简化实现：因我们不解析参数类型，ParameterDescription 始终返回空列表；
// RowDescription/NoData 在 Execute 时才确定（此时仅返回 NoData）。
func (h *connHandler) handleDescribe(m *pgproto3.Describe) {
	switch m.ObjectType {
	case 'S':
		// prepared statement: ParameterDescription + (RowDescription | NoData)
		h.extMu.Lock()
		stmt, ok := h.extStmts[m.Name]
		var paramOIDs []uint32
		if ok {
			// 若 SQL 中包含 $1/$2/... 占位符（简化：仅检查 $N 出现次数），返回对应 OID 列表。
			// 真实协议下应解析参数类型；此处无法静态分析，使用 unspecified OID=0。
			paramOIDs = extractParamOIDs(stmt.sql)
		}
		h.extMu.Unlock()
		if !ok {
			err := fmt.Errorf("prepared statement %q 不存在", m.Name)
			h.sendError(err)
			h.setExtError(err)
			return
		}
		if err := h.send(&pgproto3.ParameterDescription{ParameterOIDs: paramOIDs}); err != nil {
			log.Printf("pgwire: send parameter description: %v", err)
			return
		}
		// 是否返回行的 SQL 类别只能通过执行判断；简化实现：始终返回 NoData，
		// 在 Execute 时若实际为 SELECT/EXPLAIN/SHOW TABLES，则追加 RowDescription。
		if err := h.send(&pgproto3.NoData{}); err != nil {
			log.Printf("pgwire: send no data: %v", err)
		}
	case 'P':
		// portal: 仅返回 (RowDescription | NoData)
		h.extMu.Lock()
		_, ok := h.extPortals[m.Name]
		h.extMu.Unlock()
		if !ok {
			err := fmt.Errorf("portal %q 不存在", m.Name)
			h.sendError(err)
			h.setExtError(err)
			return
		}
		// 同上：返回 NoData，RowDescription 在 Execute 时按需补发。
		if err := h.send(&pgproto3.NoData{}); err != nil {
			log.Printf("pgwire: send no data: %v", err)
		}
	default:
		err := fmt.Errorf("describe 类型 %c 不支持", m.ObjectType)
		h.sendError(err)
		h.setExtError(err)
	}
}

// handleClose 处理 Close 消息：从对应映射中删除 prepared statement 或 portal，
// 并发送 CloseComplete。Close 始终成功响应，即使对象不存在（按 PG 规范）。
func (h *connHandler) handleClose(m *pgproto3.Close) {
	h.extMu.Lock()
	switch m.ObjectType {
	case 'S':
		delete(h.extStmts, m.Name)
	case 'P':
		delete(h.extPortals, m.Name)
	}
	h.extMu.Unlock()
	if err := h.send(&pgproto3.CloseComplete{}); err != nil {
		log.Printf("pgwire: send close complete: %v", err)
	}
}

// handleExecute 处理 Execute 消息：从 portal 取出 SQL，由 executor 执行。
// 对于返回结果集的查询，在 DataRow 之前先补发 RowDescription。
// maxRows>0 时限制返回行数；portal 不存在则进入错误状态。
func (h *connHandler) handleExecute(m *pgproto3.Execute) {
	h.extMu.Lock()
	portal, ok := h.extPortals[m.Portal]
	if !ok {
		h.extMu.Unlock()
		err := fmt.Errorf("portal %q 不存在", m.Portal)
		h.sendError(err)
		h.setExtError(err)
		return
	}
	sql := portal.sql
	h.extMu.Unlock()

	result, err := h.executor.ExecuteSQL(sql)
	if err != nil {
		// 执行错误不发 ErrorResponse 之前的描述消息（PG 规范）。
		h.sendError(err)
		// 按 PG 规范，Execute 错误不会污染 extended query 错误状态，
		// 仅在 Parse/Bind/Describe 失败时才进入错误状态。
		return
	}
	h.sendExtendedResult(result, int(m.MaxRows))
}

// sendExtendedResult 与 Simple Query 路径相同：补发 RowDescription（如有），
// 然后发送 DataRow* + CommandComplete。
// maxRows 限制返回的最大行数（0 表示无限制）。
func (h *connHandler) sendExtendedResult(result *SQLResult, maxRows int) {
	if result.IsQuery {
		types := columnTypesFromSchema(result.Columns, result.ColumnTypes)
		if types == nil {
			types = inferColumnTypes(result.Columns, result.Rows)
		}
		if err := h.send(buildRowDescription(result.Columns, types)); err != nil {
			log.Printf("pgwire: send row description: %v", err)
			return
		}
		rowLimit := len(result.Rows)
		if maxRows > 0 && maxRows < rowLimit {
			rowLimit = maxRows
		}
		for i := 0; i < rowLimit; i++ {
			row := result.Rows[i]
			if err := h.send(buildDataRow(row, result.Columns)); err != nil {
				log.Printf("pgwire: send data row: %v", err)
				return
			}
		}
	}
	tag := result.CommandTag
	if tag == "" {
		if result.IsQuery {
			tag = fmt.Sprintf("SELECT %d", len(result.Rows))
		} else {
			tag = "OK"
		}
	}
	if err := h.send(&pgproto3.CommandComplete{CommandTag: []byte(tag)}); err != nil {
		log.Printf("pgwire: send command complete: %v", err)
	}
}

// extractParamOIDs 从 SQL 文本中提取 $N 占位符数量（最大 N）并返回对应数量的
// OID=0（unspecified）。返回 nil 表示无占位符。
// 简化实现：widb 的 SQL 解析器不支持占位符，因此参数总是被忽略；
// 此函数仅用于 ParameterDescription 满足 PG 客户端对 Describe(S) 的期望。
func extractParamOIDs(sql string) []uint32 {
	if !strings.Contains(sql, "$") {
		return nil
	}
	maxN := 0
	for i := 0; i < len(sql)-1; i++ {
		if sql[i] != '$' {
			continue
		}
		n := 0
		hasDigit := false
		for j := i + 1; j < len(sql) && sql[j] >= '0' && sql[j] <= '9'; j++ {
			n = n*10 + int(sql[j]-'0')
			hasDigit = true
		}
		if hasDigit && n > maxN {
			maxN = n
		}
	}
	if maxN == 0 {
		return nil
	}
	oIDs := make([]uint32, maxN)
	return oIDs
}
