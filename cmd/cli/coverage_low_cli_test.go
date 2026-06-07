package main

import (
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
)

// errorBody 是一个在 Read 时返回错误的 io.ReadCloser，用于模拟 HTTP 响应体读取失败。
type errorBody struct{}

func (e *errorBody) Read(_ []byte) (n int, err error) {
	return 0, errors.New("模拟读取错误")
}

func (e *errorBody) Close() error { return nil }

// errorTransport 是一个自定义 http.RoundTripper，返回一个会在 Read 时出错的响应体。
type errorTransport struct{}

func (t *errorTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       &errorBody{},
	}, nil
}

// --- executeHTTP 低覆盖率测试 ---

// 测试 executeHTTP 在读取响应体失败时的错误处理
func TestCLIExecuteHTTPBodyReadError(t *testing.T) {
	c := newCLI("127.0.0.1:1", "127.0.0.1:1", testModeHTTP)
	defer c.close()
	// 使用自定义 Transport 模拟响应体读取失败
	c.httpCli.Transport = &errorTransport{}

	_, err := c.executeHTTP("SELECT 1")
	if err == nil {
		t.Error("期望读取响应失败错误")
	}
	if !strings.Contains(err.Error(), "读取响应失败") {
		t.Errorf("错误应包含 '读取响应失败': %v", err)
	}
}

// 测试 executeHTTP 在响应体非 JSON 时的错误处理
func TestCLIExecuteHTTPJSONUnmarshalError(t *testing.T) {
	// 启动一个返回非 JSON 内容的 HTTP 测试服务器
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("this is not json"))
	}))
	defer ts.Close()

	httpAddr := strings.TrimPrefix(ts.URL, "http://")
	c := newCLI("127.0.0.1:1", httpAddr, testModeHTTP)
	defer c.close()

	_, err := c.executeHTTP("SELECT 1")
	if err == nil {
		t.Error("期望解析响应失败错误")
	}
	if !strings.Contains(err.Error(), "解析响应失败") {
		t.Errorf("错误应包含 '解析响应失败': %v", err)
	}
}

// --- executeTCP 低覆盖率测试 ---

// 测试 executeTCP 在读取响应失败时的错误处理（服务端不回复直接关闭连接）
func TestCLIExecuteTCPReadResponseError(t *testing.T) {
	// 启动一个模拟 TCP 服务器，接收请求后关闭连接，导致客户端读取响应失败
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen 失败: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		// 读取客户端请求包后不回复，直接关闭连接
		header := make([]byte, server.HeaderSize)
		if _, err := conn.Read(header); err != nil {
			return
		}
		length := binary.BigEndian.Uint32(header[7:11])
		if length > 0 {
			payload := make([]byte, length)
			if _, err := conn.Read(payload); err != nil {
				return
			}
		}
		// 不发送响应，关闭连接触发客户端 DecodePacket 失败
	}()

	c := newCLI(ln.Addr().String(), "127.0.0.1:1", testModeTCP)
	defer c.close()

	_, err = c.execute("SELECT 1")
	if err == nil {
		t.Error("期望读取响应失败错误")
	}
	if !strings.Contains(err.Error(), "读取响应失败") {
		t.Errorf("错误应包含 '读取响应失败': %v", err)
	}
}

// --- pingTCP 低覆盖率测试 ---

// 测试 pingTCP 在读取响应失败时的错误处理（服务端不回复直接关闭连接）
func TestCLIPingTCPReadResponseError(t *testing.T) {
	// 启动一个模拟 TCP 服务器，接收心跳后关闭连接，导致客户端读取响应失败
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen 失败: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		// 读取客户端心跳包后不回复，直接关闭连接
		header := make([]byte, server.HeaderSize)
		if _, err := conn.Read(header); err != nil {
			return
		}
		length := binary.BigEndian.Uint32(header[7:11])
		if length > 0 {
			payload := make([]byte, length)
			if _, err := conn.Read(payload); err != nil {
				return
			}
		}
		// 不发送响应，关闭连接触发客户端 DecodePacket 失败
	}()

	c := newCLI(ln.Addr().String(), "127.0.0.1:1", testModeTCP)
	defer c.close()

	_, err = c.pingTCP()
	if err == nil {
		t.Error("期望读取心跳响应失败错误")
	}
	if !strings.Contains(err.Error(), "读取心跳响应失败") {
		t.Errorf("错误应包含 '读取心跳响应失败': %v", err)
	}
}

// 测试 pingTCP 在响应载荷非 JSON 时的错误处理
func TestCLIPingTCPResponseUnmarshalFail(t *testing.T) {
	// 启动一个模拟 TCP 服务器，发送有效数据包但载荷为非 JSON 内容
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen 失败: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		// 读取客户端心跳包
		header := make([]byte, server.HeaderSize)
		if _, err := conn.Read(header); err != nil {
			return
		}
		length := binary.BigEndian.Uint32(header[7:11])
		if length > 0 {
			payload := make([]byte, length)
			if _, err := conn.Read(payload); err != nil {
				return
			}
		}

		// 发送有效数据包但载荷为非 JSON 内容
		corruptedPayload := []byte("not json!!!")
		respPkt := server.NewPacket(server.PacketResponse, corruptedPayload)
		_, _ = conn.Write(respPkt.Encode())
	}()

	c := newCLI(ln.Addr().String(), "127.0.0.1:1", testModeTCP)
	defer c.close()

	_, err = c.pingTCP()
	if err == nil {
		t.Error("期望解析心跳响应失败错误")
	}
	if !strings.Contains(err.Error(), "解析心跳响应失败") {
		t.Errorf("错误应包含 '解析心跳响应失败': %v", err)
	}
}
