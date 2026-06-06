package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
	"github.com/what-is-me-vibe-coding/test-db/pkg/query"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// msgPong is the message returned by the ping handler.
const msgPong = "pong"

// Config 是服务器配置参数。
type Config struct {
	TCPAddr         string
	HTTPAddr        string
	DataDir         string
	MaxMemTableSize int64
	MaxConnections  int // 最大并发 TCP 连接数，0 表示不限制
}

// Server 是数据库服务器，同时提供 TCP 和 HTTP 接入。
type Server struct {
	cfg       Config
	storage   *storage.Engine
	catalog   *catalog.Catalog
	parser    *query.Parser
	analyzer  *query.Analyzer
	optimizer *query.Optimizer
	executor  *query.Executor
	metrics   *Metrics
	registry  prometheus.Registerer

	tcpListener  net.Listener
	httpServer   *http.Server
	httpListener net.Listener

	connCount int64 // 当前活跃 TCP 连接数
	wg        sync.WaitGroup
	done      chan struct{}
}

// storageAdapter 适配 storage.Engine 以实现 query.StorageProvider 接口。
type storageAdapter struct {
	engine *storage.Engine
}

// ScanRange 实现 StorageProvider 接口。
func (sa *storageAdapter) ScanRange(start, end string) []storage.ScanEntry {
	return sa.engine.ScanRange(start, end)
}

// ColumnMeta 实现 StorageProvider 接口。
func (sa *storageAdapter) ColumnMeta() []storage.ColumnMeta {
	return sa.engine.ColumnMeta()
}

// PrimaryIndex 实现 StorageProvider 接口。
func (sa *storageAdapter) PrimaryIndex() *index.PrimaryIndex {
	return sa.engine.PrimaryIndex()
}

// SparseIndex 实现 StorageProvider 接口。
func (sa *storageAdapter) SparseIndex() *index.SparseIndex {
	return sa.engine.SparseIndex()
}

// NewServer 创建一个新的服务器实例，初始化所有组件。
func NewServer(cfg Config, opts ...Option) (*Server, error) {
	if cfg.MaxMemTableSize <= 0 {
		cfg.MaxMemTableSize = 64 * 1024 * 1024
	}

	eng, err := storage.NewEngine(storage.EngineConfig{
		DataDir:         cfg.DataDir,
		MaxMemTableSize: cfg.MaxMemTableSize,
	})
	if err != nil {
		return nil, fmt.Errorf("server: create storage engine: %w", err)
	}

	cat := catalog.NewCatalog("")
	sp := &storageAdapter{engine: eng}
	exec := query.NewExecutor(sp)

	s := &Server{
		cfg:       cfg,
		storage:   eng,
		catalog:   cat,
		parser:    query.NewParser(),
		analyzer:  query.NewAnalyzer(cat),
		optimizer: query.NewOptimizer(),
		executor:  exec,
		done:      make(chan struct{}),
	}

	for _, opt := range opts {
		opt(s)
	}

	// 如果未通过选项设置 metrics，使用默认注册器
	if s.metrics == nil {
		s.metrics = NewMetrics(prometheus.DefaultRegisterer)
	}

	return s, nil
}

// Option 是服务器配置选项函数。
type Option func(*Server)

// WithMetricsRegistry 设置自定义 Prometheus 注册器（用于测试隔离）。
func WithMetricsRegistry(reg prometheus.Registerer) Option {
	return func(s *Server) {
		s.metrics = NewMetrics(reg)
		s.registry = reg
	}
}

// Start 启动 TCP 和 HTTP 监听。
func (s *Server) Start() error {
	tcpLn, err := net.Listen("tcp", s.cfg.TCPAddr)
	if err != nil {
		return fmt.Errorf("server: listen tcp %s: %w", s.cfg.TCPAddr, err)
	}
	s.tcpListener = tcpLn
	log.Printf("TCP 监听 %s", s.cfg.TCPAddr)

	s.wg.Add(1)
	go s.acceptTCP()

	mux := s.registerHTTPHandlers()
	s.httpServer = &http.Server{Handler: mux}

	httpLn, err := net.Listen("tcp", s.cfg.HTTPAddr)
	if err != nil {
		_ = s.tcpListener.Close()
		return fmt.Errorf("server: listen http %s: %w", s.cfg.HTTPAddr, err)
	}
	s.httpListener = httpLn

	s.wg.Add(1)
	go s.serveHTTP()

	log.Printf("HTTP 监听 %s", s.cfg.HTTPAddr)
	return nil
}

// TCPAddr 返回 TCP 监听地址，未启动时返回空字符串。
func (s *Server) TCPAddr() string {
	if s.tcpListener != nil {
		return s.tcpListener.Addr().String()
	}
	return ""
}

// HTTPAddr 返回 HTTP 监听地址，未启动时返回空字符串。
func (s *Server) HTTPAddr() string {
	if s.httpListener != nil {
		return s.httpListener.Addr().String()
	}
	return ""
}

// Catalog 返回服务器的 Catalog 实例。
func (s *Server) Catalog() *catalog.Catalog {
	return s.catalog
}

// Stop 优雅关闭服务器，等待所有活跃连接完成。
func (s *Server) Stop() error {
	close(s.done)

	if s.tcpListener != nil {
		_ = s.tcpListener.Close()
	}
	if s.httpListener != nil {
		_ = s.httpListener.Close()
	}
	if s.httpServer != nil {
		_ = s.httpServer.Close()
	}

	s.wg.Wait()

	if s.storage != nil {
		if err := s.storage.Close(); err != nil {
			return fmt.Errorf("server: close storage: %w", err)
		}
	}

	log.Println("服务器已关闭")
	return nil
}

// acceptTCP 接受 TCP 连接。
func (s *Server) acceptTCP() {
	defer s.wg.Done()

	for {
		conn, err := s.tcpListener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				// 瞬态错误（如资源耗尽）不应终止 accept 循环
				if isTransientAcceptErr(err) {
					log.Printf("TCP accept 瞬态错误（将继续重试）: %v", err)
					continue
				}
				log.Printf("TCP accept 错误: %v", err)
				return
			}
		}

		// 检查连接数限制
		if s.cfg.MaxConnections > 0 && s.connCount >= int64(s.cfg.MaxConnections) {
			log.Printf("TCP 连接数已达上限 %d，拒绝新连接", s.cfg.MaxConnections)
			_ = conn.Close()
			continue
		}

		s.wg.Add(1)
		atomic.AddInt64(&s.connCount, 1)
		go s.handleTCPConn(conn)
	}
}

// serveHTTP 启动 HTTP 服务。
func (s *Server) serveHTTP() {
	defer s.wg.Done()

	if err := s.httpServer.Serve(s.httpListener); err != nil {
		select {
		case <-s.done:
			return
		default:
			if err != http.ErrServerClosed {
				log.Printf("HTTP 服务错误: %v", err)
			}
		}
	}
}

// handleTCPConn 处理单个 TCP 连接。
func (s *Server) handleTCPConn(conn net.Conn) {
	defer s.wg.Done()
	defer atomic.AddInt64(&s.connCount, -1)
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)

	for {
		select {
		case <-s.done:
			return
		default:
		}

		if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
			return
		}

		pkt, err := DecodePacket(reader)
		if err != nil {
			if isClosedConnErr(err) {
				return
			}
			log.Printf("TCP 解码错误: %v", err)
			return
		}

		resp, err := s.handlePacket(pkt)
		if err != nil {
			errResp := &Response{Code: -1, Message: err.Error()}
			payload, _ := json.Marshal(errResp)
			resp = NewPacket(PacketResponse, payload)
		}

		if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return
		}
		if _, err := conn.Write(resp.Encode()); err != nil {
			return
		}
	}
}

// handlePacket 根据包类型路由到对应的处理器。
func (s *Server) handlePacket(pkt *Packet) (*Packet, error) {
	switch pkt.Type {
	case PacketQuery:
		return s.handleQueryPacket(pkt)
	case PacketWrite:
		return s.handleWritePacket(pkt)
	case PacketPing:
		return s.handlePing()
	default:
		return nil, fmt.Errorf("未知的包类型: %d", pkt.Type)
	}
}

// handleQueryPacket 处理查询请求包。
func (s *Server) handleQueryPacket(pkt *Packet) (*Packet, error) {
	var req QueryRequest
	if err := json.Unmarshal(pkt.Payload, &req); err != nil {
		return nil, fmt.Errorf("解析查询请求: %w", err)
	}

	resp, err := s.handleQuery(&req)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("序列化查询响应: %w", err)
	}

	return NewPacket(PacketResponse, payload), nil
}

// handleWritePacket 处理写入请求包。
func (s *Server) handleWritePacket(pkt *Packet) (*Packet, error) {
	var req WriteRequest
	if err := json.Unmarshal(pkt.Payload, &req); err != nil {
		return nil, fmt.Errorf("解析写入请求: %w", err)
	}

	resp, err := s.handleWrite(&req)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("序列化写入响应: %w", err)
	}

	return NewPacket(PacketResponse, payload), nil
}

// handlePing 处理心跳请求。
func (s *Server) handlePing() (*Packet, error) {
	resp := &Response{Code: 0, Message: msgPong}
	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("序列化心跳响应: %w", err)
	}
	return NewPacket(PacketResponse, payload), nil
}

// handleQuery 执行 SQL 查询。
func (s *Server) handleQuery(req *QueryRequest) (*Response, error) {
	start := time.Now()
	defer func() {
		s.metrics.QueryDuration.WithLabelValues("sql").Observe(time.Since(start).Seconds())
	}()

	stmt, err := s.parser.Parse(req.SQL)
	if err != nil {
		s.metrics.QueriesTotal.WithLabelValues("parse_error").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("SQL 解析错误: %v", err)}, nil
	}

	plan, err := s.analyzer.Analyze(stmt)
	if err != nil {
		s.metrics.QueriesTotal.WithLabelValues("analyze_error").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("SQL 分析错误: %v", err)}, nil
	}

	optimized := s.optimizer.Optimize(plan)

	chunks, err := s.executor.Execute(optimized)
	if err != nil {
		s.metrics.QueriesTotal.WithLabelValues("execute_error").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("SQL 执行错误: %v", err)}, nil
	}

	data := chunksToRows(chunks)
	totalRows := countRows(chunks)

	s.metrics.QueriesTotal.WithLabelValues("success").Inc()
	return &Response{Code: 0, Data: data, Rows: totalRows}, nil
}

// handleWrite 批量写入数据。
func (s *Server) handleWrite(req *WriteRequest) (*Response, error) {
	start := time.Now()

	tbl, err := s.catalog.GetTable(req.Table)
	if err != nil {
		s.metrics.WritesTotal.WithLabelValues("table_not_found").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("表不存在: %v", err)}, nil
	}

	writeRows := make([]storage.WriteRow, 0, len(req.Rows))
	for _, row := range req.Rows {
		key, values, convErr := s.convertWriteRow(tbl, row)
		if convErr != nil {
			s.metrics.WritesTotal.WithLabelValues("convert_error").Inc()
			return &Response{Code: -1, Message: fmt.Sprintf("行数据转换错误: %v", convErr)}, nil
		}
		writeRows = append(writeRows, storage.WriteRow{Key: key, Values: values})
	}

	if err := s.storage.WriteBatch(writeRows); err != nil {
		s.metrics.WritesTotal.WithLabelValues("write_error").Inc()
		return &Response{Code: -1, Message: fmt.Sprintf("写入错误: %v", err)}, nil
	}

	s.metrics.WritesTotal.WithLabelValues("success").Add(float64(len(writeRows)))
	s.metrics.WriteDuration.WithLabelValues("success").Observe(time.Since(start).Seconds())
	return &Response{Code: 0, Rows: len(writeRows)}, nil
}

// convertWriteRow 将 JSON 行数据转换为存储引擎需要的格式。
func (s *Server) convertWriteRow(
	tbl *catalog.Table, row map[string]interface{},
) (string, map[string]common.Value, error) {
	values := make(map[string]common.Value, len(row))

	colTypes := make(map[string]common.DataType, len(tbl.Columns))
	for _, col := range tbl.Columns {
		colTypes[col.Name] = col.Type
	}

	for colName, rawVal := range row {
		colType, ok := colTypes[colName]
		if !ok {
			continue
		}
		val, err := interfaceToValue(rawVal, colType)
		if err != nil {
			return "", nil, fmt.Errorf("列 %s: %w", colName, err)
		}
		values[colName] = val
	}

	key, err := s.buildPrimaryKey(tbl, row)
	if err != nil {
		return "", nil, err
	}

	return key, values, nil
}

// buildPrimaryKey 从行数据中提取主键值，拼接为存储 key。
func (s *Server) buildPrimaryKey(
	tbl *catalog.Table, row map[string]interface{},
) (string, error) {
	key := ""
	for i, pk := range tbl.PrimaryKey {
		rawVal, ok := row[pk]
		if !ok {
			return "", fmt.Errorf("主键列 %s 缺失", pk)
		}
		if i > 0 {
			key += "|"
		}
		key += fmt.Sprintf("%v", rawVal)
	}
	return key, nil
}

// isClosedConnErr 判断是否为连接关闭相关的错误。
func isClosedConnErr(err error) bool {
	if err == io.EOF {
		return true
	}
	if opErr, ok := err.(*net.OpError); ok {
		return opErr.Timeout()
	}
	return false
}

// isTransientAcceptErr 判断 TCP Accept 错误是否为可恢复的瞬态错误（如临时资源耗尽）。
func isTransientAcceptErr(err error) bool {
	if opErr, ok := err.(*net.OpError); ok {
		return opErr.Temporary() || opErr.Timeout()
	}
	return false
}
