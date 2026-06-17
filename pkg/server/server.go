package server

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
	"github.com/what-is-me-vibe-coding/test-db/pkg/query"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

const msgPong = "pong"

const defaultMaxMemTableSize = 64 * 1024 * 1024 // 64MB

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
	conns     map[net.Conn]struct{}
	connMu    sync.Mutex
	wg        sync.WaitGroup
	done      chan struct{}
	stopOnce  sync.Once
}

// storageAdapter 适配 storage.Engine 以实现 query.StorageProvider 接口。
type storageAdapter struct {
	engine *storage.Engine
}

// ScanRange 实现 StorageProvider 接口。
func (sa *storageAdapter) ScanRange(start, end string) []storage.ScanEntry {
	return sa.engine.ScanRange(start, end)
}

// ScanRangeWithPruning 实现 StorageProvider 接口，利用列谓词进行段裁剪。
func (sa *storageAdapter) ScanRangeWithPruning(start, end string, predicates []storage.ColumnPredicate) []storage.ScanEntry {
	return sa.engine.ScanRangeWithPruning(start, end, predicates)
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
		cfg.MaxMemTableSize = defaultMaxMemTableSize
	}

	eng, err := storage.NewEngine(storage.EngineConfig{
		DataDir:         cfg.DataDir,
		MaxMemTableSize: cfg.MaxMemTableSize,
	})
	if err != nil {
		return nil, fmt.Errorf("server: create storage engine: %w", err)
	}

	// 从数据目录加载 catalog，使表定义在重启后可恢复
	catalogPath := filepath.Join(cfg.DataDir, "catalog.json")
	cat, err := catalog.LoadCatalog(catalogPath)
	if err != nil {
		if closeErr := eng.Close(); closeErr != nil {
			log.Printf("server: close engine after catalog load failure: %v", closeErr)
		}
		return nil, fmt.Errorf("server: load catalog: %w", err)
	}

	// 恢复引擎列元数据，使后台调度器自动刷盘能正确编码列
	if cols := buildColumnMetaFromCatalog(cat); len(cols) > 0 {
		eng.SetColumnMeta(cols)
	}

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
		conns:     make(map[net.Conn]struct{}),
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
// 多次调用是安全的，仅第一次调用会执行关闭逻辑。
// 关闭监听器后，主动关闭所有已建立的 TCP 连接，避免空闲连接阻塞关闭流程。
func (s *Server) Stop() error {
	var stopErr error
	s.stopOnce.Do(func() {
		close(s.done)
		s.closeListeners()
		s.closeAllConns()
		s.wg.Wait()

		if s.storage != nil {
			if err := s.storage.Close(); err != nil {
				stopErr = fmt.Errorf("server: close storage: %w", err)
				return
			}
		}

		log.Println("服务器已关闭")
	})
	return stopErr
}

// closeListeners 关闭 TCP 监听器、HTTP 监听器和 HTTP 服务器。
func (s *Server) closeListeners() {
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
}

// trackConn 将连接加入跟踪集合，用于优雅关闭时主动断开。
func (s *Server) trackConn(conn net.Conn) {
	s.connMu.Lock()
	s.conns[conn] = struct{}{}
	s.connMu.Unlock()
}

// untrackConn 将连接从跟踪集合中移除。
func (s *Server) untrackConn(conn net.Conn) {
	s.connMu.Lock()
	delete(s.conns, conn)
	s.connMu.Unlock()
}

// closeAllConns 关闭所有已建立的 TCP 连接。
func (s *Server) closeAllConns() {
	s.connMu.Lock()
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.conns = make(map[net.Conn]struct{})
	s.connMu.Unlock()

	for _, c := range conns {
		_ = c.Close()
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
