package server

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
	"github.com/what-is-me-vibe-coding/test-db/pkg/query"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

const msgPong = "pong"

// Config 是服务器配置参数。
type Config struct {
	TCPAddr         string
	HTTPAddr        string
	DataDir         string
	MaxMemTableSize int64
	MaxConnections  int                     // 最大并发 TCP 连接数，0 表示不限制
	EnableScheduler bool                    // 是否启用后台调度器
	SchedulerConfig storage.SchedulerConfig // 调度器配置
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
		// HTTP 监听失败，需优雅关闭已启动的 TCP goroutine
		close(s.done)
		if closeErr := s.tcpListener.Close(); closeErr != nil {
			log.Printf("server: close tcp listener after http listen failure: %v", closeErr)
		}
		s.wg.Wait()
		// 重置 done 通道，允许后续重试 Start
		s.done = make(chan struct{})
		return fmt.Errorf("server: listen http %s: %w", s.cfg.HTTPAddr, err)
	}
	s.httpListener = httpLn

	s.wg.Add(1)
	go s.serveHTTP()

	log.Printf("HTTP 监听 %s", s.cfg.HTTPAddr)

	// 启动后台调度器
	if s.cfg.EnableScheduler {
		s.storage.StartScheduler(s.cfg.SchedulerConfig)
		log.Println("后台调度器已启动")
	}

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
		if err := s.tcpListener.Close(); err != nil {
			log.Printf("server: close tcp listener: %v", err)
		}
	}
	if s.httpListener != nil {
		if err := s.httpListener.Close(); err != nil {
			log.Printf("server: close http listener: %v", err)
		}
	}
	if s.httpServer != nil {
		if err := s.httpServer.Close(); err != nil {
			log.Printf("server: close http server: %v", err)
		}
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

	// 从查询计划的 Schema 中提取列名，用于 JSON 响应的 key
	var colNames []string
	if schema := optimized.Schema(); len(schema) > 0 {
		colNames = make([]string, len(schema))
		for i, col := range schema {
			colNames[i] = col.Name
		}
	}
	data := chunksToRows(chunks, colNames)
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
	tbl *catalog.Table, row map[string]any,
) (string, map[string]common.Value, error) {
	values := make(map[string]common.Value, len(row))
	colTypes := tbl.ColTypeMap()

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
// 使用 \x00 作为分隔符，避免主键值包含分隔符时产生碰撞。
func (s *Server) buildPrimaryKey(
	tbl *catalog.Table, row map[string]any,
) (string, error) {
	var builder strings.Builder
	for i, pk := range tbl.PrimaryKey {
		rawVal, ok := row[pk]
		if !ok {
			return "", fmt.Errorf("主键列 %s 缺失", pk)
		}
		if i > 0 {
			builder.WriteByte(0)
		}
		fmt.Fprintf(&builder, "%v", rawVal)
	}
	return builder.String(), nil
}
