package pgwire

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/jackc/pgproto3/v2"
)

// 默认连接保护参数，防止恶意客户端通过大量连接或长空闲连接耗尽 goroutine。
const (
	defaultMaxConns     = 256
	defaultIdleTimeout  = 5 * time.Minute
	defaultWriteTimeout = 30 * time.Second
)

// Server 是 PostgreSQL wire 协议服务端，监听 TCP 连接并为每个连接
// 创建独立的 connHandler 处理 Simple Query 与 Extended Query 协议。
type Server struct {
	addr         string
	executor     SQLExecutor
	listener     net.Listener
	wg           sync.WaitGroup
	done         chan struct{}
	stopOnce     sync.Once
	maxConns     int           // 最大并发连接数，<=0 表示不限制
	sem          chan struct{} // 连接数信号量，限制最大并发连接数
	idleTimeout  time.Duration // 单次读取的空闲超时，超时后关闭连接
	writeTimeout time.Duration // 单次写入超时，超时后关闭连接
}

// Option 配置 pgwire 服务端参数。
type Option func(*Server)

// WithMaxConns 设置最大并发连接数。n<=0 表示不限制。
func WithMaxConns(n int) Option {
	return func(s *Server) { s.maxConns = n }
}

// WithIdleTimeout 设置单次读取的空闲超时。
func WithIdleTimeout(d time.Duration) Option {
	return func(s *Server) { s.idleTimeout = d }
}

// WithWriteTimeout 设置单次写入超时。
func WithWriteTimeout(d time.Duration) Option {
	return func(s *Server) { s.writeTimeout = d }
}

// NewServer 创建一个新的 pgwire 服务端实例。
// addr 为监听地址（如 "0.0.0.0:5432"），executor 用于执行 SQL。
// 默认启用最大连接数与读写超时保护，可通过 Option 覆盖。
func NewServer(addr string, executor SQLExecutor, opts ...Option) *Server {
	s := &Server{
		addr:         addr,
		executor:     executor,
		done:         make(chan struct{}),
		maxConns:     defaultMaxConns,
		idleTimeout:  defaultIdleTimeout,
		writeTimeout: defaultWriteTimeout,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.maxConns > 0 {
		s.sem = make(chan struct{}, s.maxConns)
	}
	return s
}

// Start 开始监听并接受 PG wire 连接。
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("pgwire: listen %s: %w", s.addr, err)
	}
	s.listener = ln
	s.wg.Add(1)
	go s.acceptLoop()
	log.Printf("PG wire 监听 %s", s.addr)
	return nil
}

// Addr 返回实际监听地址，未启动时返回空字符串。
func (s *Server) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return ""
}

// Stop 优雅关闭服务端，等待所有连接处理完成。
// 多次调用是安全的。
func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		close(s.done)
		if s.listener != nil {
			_ = s.listener.Close()
		}
		s.wg.Wait()
	})
}

// acceptLoop 接受新连接，为每个连接启动独立 goroutine 处理。
// 达到最大连接数时拒绝新连接，防止 goroutine 耗尽（修复 review #2）。
func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				log.Printf("pgwire: accept error: %v", err)
			}
			continue
		}
		if !s.acquireConnSlot() {
			log.Printf("pgwire: max connections reached, rejecting %s", conn.RemoteAddr())
			_ = conn.Close()
			continue
		}
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

// acquireConnSlot 尝试占用一个连接槽，达到上限时返回 false。
func (s *Server) acquireConnSlot() bool {
	if s.sem == nil {
		return true
	}
	select {
	case s.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

// handleConn 处理单个 PG wire 连接的完整生命周期。
func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	if s.sem != nil {
		defer func() { <-s.sem }()
	}
	defer func() { _ = conn.Close() }()

	backend := pgproto3.NewBackend(pgproto3.NewChunkReader(conn), conn)
	handler := newConnHandler(backend, s.executor, conn, s.idleTimeout, s.writeTimeout)
	handler.serve()
}
