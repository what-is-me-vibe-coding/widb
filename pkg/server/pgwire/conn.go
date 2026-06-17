package pgwire

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgproto3/v2"
)

// serverVersion 是报告给客户端的 PostgreSQL 版本字符串。
const serverVersion = "15.0 (widb)"

// processIDCounter 生成单调递增的进程 ID（用于 BackendKeyData）。
var processIDCounter uint32

// sslNegotiationResponse 是对 SSLRequest 的单字节 'N' 响应，表示不支持 SSL。
type sslNegotiationResponse struct{}

func (sslNegotiationResponse) Backend() {}

// Encode 将 'N' 字节追加到 dst。
func (sslNegotiationResponse) Encode(dst []byte) ([]byte, error) {
	return append(dst, 'N'), nil
}

// Decode 是 BackendMessage 接口的空实现（此消息仅由服务端发送，无需解码）。
func (sslNegotiationResponse) Decode([]byte) error { return nil }

// connHandler 处理单个 PG wire 连接的完整生命周期。
type connHandler struct {
	backend      *pgproto3.Backend
	executor     SQLExecutor
	conn         net.Conn
	idleTimeout  time.Duration
	writeTimeout time.Duration
}

// newConnHandler 创建一个新的连接处理器。
// conn 用于设置读写截止时间，idleTimeout 为单次读取空闲超时，writeTimeout 为单次写入超时。
func newConnHandler(backend *pgproto3.Backend, executor SQLExecutor, conn net.Conn, idleTimeout, writeTimeout time.Duration) *connHandler {
	return &connHandler{
		backend:      backend,
		executor:     executor,
		conn:         conn,
		idleTimeout:  idleTimeout,
		writeTimeout: writeTimeout,
	}
}

// serve 运行连接生命周期：启动握手 → 查询循环。
func (h *connHandler) serve() {
	if err := h.handleStartup(); err != nil {
		log.Printf("pgwire: startup failed: %v", err)
		return
	}
	h.queryLoop()
}

// setReadDeadline 在配置了空闲超时时设置下次读取的截止时间。
func (h *connHandler) setReadDeadline() {
	if h.idleTimeout > 0 {
		_ = h.conn.SetReadDeadline(time.Now().Add(h.idleTimeout))
	}
}

// send 发送一条后端消息，并在配置了写超时时设置写截止时间。
// 返回发送错误，便于调用方感知连接断开（修复 review #4：不再静默丢弃发送错误）。
func (h *connHandler) send(msg pgproto3.BackendMessage) error {
	if h.writeTimeout > 0 {
		_ = h.conn.SetWriteDeadline(time.Now().Add(h.writeTimeout))
	}
	return h.backend.Send(msg)
}

// handleStartup 处理启动握手，包括 SSL 协商和认证。
func (h *connHandler) handleStartup() error {
	h.setReadDeadline()
	msg, err := h.backend.ReceiveStartupMessage()
	if err != nil {
		return fmt.Errorf("receive startup: %w", err)
	}
	switch m := msg.(type) {
	case *pgproto3.StartupMessage:
		_ = m
		return h.sendStartupResponse()
	case *pgproto3.SSLRequest:
		return h.handleSSLNegotiation()
	case *pgproto3.GSSEncRequest:
		return h.handleSSLNegotiation()
	default:
		return fmt.Errorf("unexpected startup message: %T", msg)
	}
}

// handleSSLNegotiation 拒绝 SSL/GSS 加密，然后接收真正的 StartupMessage。
func (h *connHandler) handleSSLNegotiation() error {
	if err := h.send(sslNegotiationResponse{}); err != nil {
		return fmt.Errorf("send ssl response: %w", err)
	}
	h.setReadDeadline()
	msg, err := h.backend.ReceiveStartupMessage()
	if err != nil {
		return fmt.Errorf("receive startup after ssl: %w", err)
	}
	if _, ok := msg.(*pgproto3.StartupMessage); !ok {
		return fmt.Errorf("expected startup message, got %T", msg)
	}
	return h.sendStartupResponse()
}

// sendStartupResponse 发送认证成功后的初始消息序列。
func (h *connHandler) sendStartupResponse() error {
	if err := h.send(&pgproto3.AuthenticationOk{}); err != nil {
		return fmt.Errorf("send auth ok: %w", err)
	}
	if err := h.sendParameterStatuses(); err != nil {
		return err
	}
	pid := atomic.AddUint32(&processIDCounter, 1)
	if err := h.send(&pgproto3.BackendKeyData{
		ProcessID: pid,
		SecretKey: pid,
	}); err != nil {
		return fmt.Errorf("send backend key data: %w", err)
	}
	return h.sendReadyForQuery()
}

// sendParameterStatuses 发送客户端期望的参数状态。
func (h *connHandler) sendParameterStatuses() error {
	params := []struct{ name, value string }{
		{"server_version", serverVersion},
		{"client_encoding", "UTF8"},
		{"server_encoding", "UTF8"},
		{"DateStyle", "ISO, MDY"},
		{"TimeZone", "UTC"},
		{"standard_conforming_strings", "on"},
		{"integer_datetimes", "on"},
	}
	for _, p := range params {
		if err := h.send(&pgproto3.ParameterStatus{
			Name: p.name, Value: p.value,
		}); err != nil {
			return fmt.Errorf("send parameter %s: %w", p.name, err)
		}
	}
	return nil
}

// queryLoop 接收并处理客户端消息，直到连接关闭或 Terminate。
// 每次接收前重置读截止时间，空闲超时后自动关闭连接（修复 review #2）。
func (h *connHandler) queryLoop() {
	for {
		h.setReadDeadline()
		msg, err := h.backend.Receive()
		if err != nil {
			return
		}
		if !h.dispatchMessage(msg) {
			return
		}
	}
}

// dispatchMessage 分发消息到对应处理器，返回 false 表示应终止连接。
func (h *connHandler) dispatchMessage(msg pgproto3.FrontendMessage) bool {
	switch m := msg.(type) {
	case *pgproto3.Query:
		h.handleQuery(m.String)
		return true
	case *pgproto3.Terminate:
		return false
	case *pgproto3.Sync:
		_ = h.sendReadyForQuery()
		return true
	case *pgproto3.Flush:
		return true
	default:
		return true
	}
}

// handleQuery 执行 SQL 查询并发送结果。
func (h *connHandler) handleQuery(sql string) {
	sql = strings.TrimSpace(sql)
	if sql == "" {
		if err := h.send(&pgproto3.EmptyQueryResponse{}); err != nil {
			log.Printf("pgwire: send empty query response: %v", err)
			return
		}
		_ = h.sendReadyForQuery()
		return
	}
	result, err := h.executor.ExecuteSQL(sql)
	if err != nil {
		h.sendError(err)
		_ = h.sendReadyForQuery()
		return
	}
	if result.IsQuery {
		h.sendQueryResult(result)
	} else {
		if err := h.send(&pgproto3.CommandComplete{
			CommandTag: []byte(result.CommandTag),
		}); err != nil {
			log.Printf("pgwire: send command complete: %v", err)
			return
		}
	}
	_ = h.sendReadyForQuery()
}

// sendQueryResult 发送结果集（RowDescription + DataRow* + CommandComplete）。
// 使用 result.CommandTag 作为命令标签，避免对 INSERT/UPDATE...RETURNING 等带结果集
// 的写操作错误地返回 "SELECT N" 标签（修复 review #1）。
func (h *connHandler) sendQueryResult(result *SQLResult) {
	// 优先使用 Schema 列类型生成准确的 RowDescription（修复 DATE/TIMESTAMP/INT
	// 被错误推断为 TEXT 的问题）；Schema 类型缺失时回退到按行值推断。
	types := columnTypesFromSchema(result.Columns, result.ColumnTypes)
	if types == nil {
		types = inferColumnTypes(result.Columns, result.Rows)
	}
	if err := h.send(buildRowDescription(result.Columns, types)); err != nil {
		log.Printf("pgwire: send row description: %v", err)
		return
	}
	for _, row := range result.Rows {
		if err := h.send(buildDataRow(row, result.Columns)); err != nil {
			log.Printf("pgwire: send data row: %v", err)
			return
		}
	}
	tag := result.CommandTag
	if tag == "" {
		tag = fmt.Sprintf("SELECT %d", len(result.Rows))
	}
	if err := h.send(&pgproto3.CommandComplete{CommandTag: []byte(tag)}); err != nil {
		log.Printf("pgwire: send command complete: %v", err)
	}
}

// sendError 发送错误响应。
func (h *connHandler) sendError(err error) {
	if err := h.send(&pgproto3.ErrorResponse{
		Severity: "ERROR",
		Code:     "XX000",
		Message:  err.Error(),
	}); err != nil {
		log.Printf("pgwire: send error response: %v", err)
	}
}

// sendReadyForQuery 发送 ReadyForQuery 消息（空闲状态）。
func (h *connHandler) sendReadyForQuery() error {
	return h.send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
}
