package server

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/query"
	"github.com/what-is-me-vibe-coding/test-db/pkg/server/pgwire"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

const msgPong = "pong"

const defaultMaxMemTableSize = 64 * 1024 * 1024 // 64MB

// Config 是服务器配置参数。
type Config struct {
	TCPAddr         string
	HTTPAddr        string
	PGAddr          string // PostgreSQL wire 协议监听地址，空表示不启用
	DataDir         string
	MaxMemTableSize int64
	MaxConnections  int                     // 最大并发 TCP 连接数，0 表示不限制
	EnableScheduler bool                    // 是否启用后台调度器
	SchedulerConfig storage.SchedulerConfig // 调度器配置
	// SlowQueryThreshold 是慢查询判定阈值（duration 形式）。
	// 转换层（pkg/cmdutil）已把 YAML 的毫秒值换算成 time.Duration。
	// <= 0 时禁用慢查询日志。
	SlowQueryThreshold time.Duration
	// SlowQueryCapacity 是慢查询环形缓冲容量；<= 0 时 NewSlowQueryLog 内部回退到 100。
	SlowQueryCapacity int
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
	adapter   *routingAdapter // 表路由适配器，按表名选择 LSM 或内存引擎
	// slowQueries 是慢查询日志；可能为 nil（禁用时 NewServer 内部判空）。
	slowQueries *SlowQueryLog

	tcpListener  net.Listener
	httpServer   *http.Server
	httpListener net.Listener
	pgServer     *pgwire.Server

	connCount int64 // 当前活跃 TCP 连接数
	conns     map[net.Conn]struct{}
	connMu    sync.Mutex
	wg        sync.WaitGroup
	done      chan struct{}
	stopOnce  sync.Once
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

	adapter := newRoutingAdapter(eng)
	exec := query.NewExecutor(adapter)

	s := &Server{
		cfg:         cfg,
		storage:     eng,
		catalog:     cat,
		parser:      query.NewParser(),
		analyzer:    query.NewAnalyzer(cat),
		optimizer:   query.NewOptimizer(),
		executor:    exec,
		adapter:     adapter,
		slowQueries: NewSlowQueryLog(cfg.SlowQueryThreshold, cfg.SlowQueryCapacity),
		done:        make(chan struct{}),
		conns:       make(map[net.Conn]struct{}),
	}

	// 恢复每张 LSM 表的独立引擎（位于 dataDir/tables/<name>/），
	// 实现表间数据隔离。仅恢复存在独立数据目录的表，无目录的表回退到默认引擎。
	if err := s.recoverLSMEngines(); err != nil {
		if closeErr := eng.Close(); closeErr != nil {
			log.Printf("server: close engine after lsm recovery failure: %v", closeErr)
		}
		return nil, fmt.Errorf("server: recover lsm engines: %w", err)
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

	// 启动 PostgreSQL wire 协议服务（若配置了地址）
	if s.cfg.PGAddr != "" {
		adapter := &pgwireAdapter{server: s}
		s.pgServer = pgwire.NewServer(s.cfg.PGAddr, adapter)
		if err := s.pgServer.Start(); err != nil {
			// PG 监听失败不阻断启动，仅记录日志
			log.Printf("server: start pgwire: %v", err)
			s.pgServer = nil
		}
	}

	// 启动后台调度器
	if s.cfg.EnableScheduler {
		s.storage.StartScheduler(s.cfg.SchedulerConfig)
		s.adapter.forEachLSMEngine(func(eng TableEngine) {
			if lsmEng, ok := eng.(*storage.Engine); ok {
				lsmEng.StartScheduler(s.cfg.SchedulerConfig)
			}
		})
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

// PGAddr 返回 PostgreSQL wire 协议监听地址，未启动时返回空字符串。
func (s *Server) PGAddr() string {
	if s.pgServer != nil {
		return s.pgServer.Addr()
	}
	return ""
}

// Catalog 返回服务器的 Catalog 实例。
func (s *Server) Catalog() *catalog.Catalog {
	return s.catalog
}

// SlowQueryLog 返回慢查询日志实例；可能为 nil（已禁用时 NewServer 返回零值 SlowQueryLog，
// 上层应通过 Enabled() 判定而非 nil 判定，避免漏掉「未设置阈值」的常见误用）。
func (s *Server) SlowQueryLog() *SlowQueryLog {
	return s.slowQueries
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

		// 先关闭所有内存引擎表与 LSM 表引擎，再关闭默认 LSM 引擎。
		if s.adapter != nil {
			s.adapter.closeAll()
		}

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

// tableDataDir 返回指定 LSM 表的独立数据目录路径：dataDir/tables/<escaped-name>/。
// 使用 url.PathEscape 对表名转义，避免表名包含路径分隔符等特殊字符。
func (s *Server) tableDataDir(table string) string {
	return filepath.Join(s.cfg.DataDir, "tables", url.PathEscape(table))
}

// recoverLSMEngines 在服务器启动时为 catalog 中每张 LSM 表恢复独立引擎。
// 仅恢复存在独立数据目录（dataDir/tables/<name>/）的表；无目录的表回退到默认引擎，
// 兼容历史数据（建表于本隔离机制引入之前）。
func (s *Server) recoverLSMEngines() error {
	snap := s.catalog.Snapshot()
	for name, tbl := range snap.Tables {
		if tbl.Engine != catalog.EngineLSM {
			continue
		}
		tableDir := s.tableDataDir(name)
		if _, err := os.Stat(tableDir); err != nil {
			continue // 无独立目录，回退到默认引擎
		}
		eng, err := storage.NewEngine(storage.EngineConfig{
			DataDir:         tableDir,
			MaxMemTableSize: s.cfg.MaxMemTableSize,
		})
		if err != nil {
			return fmt.Errorf("recover lsm engine for table %q: %w", name, err)
		}
		eng.SetColumnMeta(buildColumnMeta(tbl.Columns))
		if err := s.adapter.registerLSMEngine(name, eng); err != nil {
			_ = eng.Close()
			return fmt.Errorf("register lsm engine for table %q: %w", name, err)
		}
	}
	return nil
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
	if s.pgServer != nil {
		s.pgServer.Stop()
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
