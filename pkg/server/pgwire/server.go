package pgwire

import (
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/jackc/pgproto3/v2"
)

// Server 是 PostgreSQL wire 协议服务端，监听 TCP 连接并为每个连接
// 创建独立的 connHandler 处理 Simple Query 协议。
type Server struct {
	addr     string
	executor SQLExecutor
	listener net.Listener
	wg       sync.WaitGroup
	done     chan struct{}
	stopOnce sync.Once
}

// NewServer 创建一个新的 pgwire 服务端实例。
// addr 为监听地址（如 "0.0.0.0:5432"），executor 用于执行 SQL。
func NewServer(addr string, executor SQLExecutor) *Server {
	return &Server{
		addr:     addr,
		executor: executor,
		done:     make(chan struct{}),
	}
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
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

// handleConn 处理单个 PG wire 连接的完整生命周期。
func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() { _ = conn.Close() }()

	backend := pgproto3.NewBackend(pgproto3.NewChunkReader(conn), conn)
	handler := newConnHandler(backend, s.executor)
	handler.serve()
}
