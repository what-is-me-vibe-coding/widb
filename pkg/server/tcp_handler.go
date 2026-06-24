package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync/atomic"
	"time"
)

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
		if s.cfg.MaxConnections > 0 && atomic.LoadInt64(&s.connCount) >= int64(s.cfg.MaxConnections) {
			log.Printf("TCP 连接数已达上限 %d，拒绝新连接", s.cfg.MaxConnections)
			_ = conn.Close() // 拒绝连接，忽略关闭错误
			continue
		}

		s.wg.Add(1)
		atomic.AddInt64(&s.connCount, 1)
		go s.handleTCPConn(conn)
	}
}

// handleTCPConn 处理单个 TCP 连接。
func (s *Server) handleTCPConn(conn net.Conn) {
	defer s.wg.Done()
	defer atomic.AddInt64(&s.connCount, -1)
	defer s.untrackConn(conn)
	defer func() { _ = conn.Close() }() // 连接处理结束，关闭错误不影响主流程

	s.trackConn(conn)

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
			resp = newErrorResponse(err)
		}

		if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return
		}
		if _, err := conn.Write(resp.Encode()); err != nil {
			return
		}
	}
}

// newErrorResponse 将错误构造为 Response 类型的 Packet。
func newErrorResponse(err error) *Packet {
	errResp := &Response{Code: -1, Message: err.Error()}
	payload, _ := json.Marshal(errResp)
	return NewPacket(PacketResponse, payload)
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
	return s.handleTypedPacket(pkt, func(payload []byte) (*Response, error) {
		var req QueryRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("解析查询请求: %w", err)
		}
		return s.handleQuerySource(SlowQuerySourceTCP, &req)
	})
}

// handleWritePacket 处理写入请求包。
func (s *Server) handleWritePacket(pkt *Packet) (*Packet, error) {
	return s.handleTypedPacket(pkt, func(payload []byte) (*Response, error) {
		var req WriteRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("解析写入请求: %w", err)
		}
		return s.handleWriteSource(SlowQuerySourceTCP, &req)
	})
}

// handleTypedPacket 是通用的 TCP 包处理器，封装业务处理和编码响应的逻辑。
func (s *Server) handleTypedPacket(pkt *Packet, handler func([]byte) (*Response, error)) (*Packet, error) {
	resp, err := handler(pkt.Payload)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("序列化响应: %w", err)
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

// isClosedConnErr 判断是否为连接关闭相关的错误。
func isClosedConnErr(err error) bool {
	if err == io.EOF {
		return true
	}
	// 使用 errors.Is 检查 net.ErrClosed，兼容 Go 1.16+ 的错误包装
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	opErr, ok := err.(*net.OpError)
	return ok && opErr.Timeout()
}

// isTransientAcceptErr 判断 TCP Accept 错误是否为可恢复的瞬态错误。
func isTransientAcceptErr(err error) bool {
	opErr, ok := err.(*net.OpError)
	if !ok || opErr.Timeout() {
		return ok
	}
	msg := opErr.Error()
	return strings.Contains(msg, "resource temporarily unavailable") ||
		strings.Contains(msg, "too many open files")
}
