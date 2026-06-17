package server

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// 协议常量定义。
const (
	// Magic 是 TCP 协议的魔数，用于识别协议包。
	Magic uint32 = 0x57494442

	// ProtocolVersion 是当前协议版本号。
	ProtocolVersion uint16 = 1

	// HeaderSize 是固定包头长度（4+2+1+4=11 字节）。
	HeaderSize = 11

	// MaxPacketSize 是单个数据包的最大负载大小（16MB），防止 OOM 攻击。
	MaxPacketSize = 16 * 1024 * 1024

	// PacketQuery 表示查询请求包类型。
	PacketQuery uint8 = 1

	// PacketWrite 表示写入请求包类型。
	PacketWrite uint8 = 2

	// PacketPing 表示心跳请求包类型。
	PacketPing uint8 = 3

	// PacketResponse 表示响应包类型。
	PacketResponse uint8 = 10
)

// Packet 是 TCP 协议的数据包结构。
// 固定包头 11 字节，Payload 为 JSON 格式。
type Packet struct {
	Magic   uint32
	Version uint16
	Type    uint8
	Length  uint32
	Payload []byte
}

// Encode 将 Packet 编码为二进制字节流。
// 字节序使用大端序（Big-Endian）。
func (p *Packet) Encode() []byte {
	buf := make([]byte, HeaderSize+len(p.Payload))
	binary.BigEndian.PutUint32(buf[0:4], p.Magic)
	binary.BigEndian.PutUint16(buf[4:6], p.Version)
	buf[6] = p.Type
	binary.BigEndian.PutUint32(buf[7:11], p.Length)
	copy(buf[HeaderSize:], p.Payload)
	return buf
}

// DecodePacket 从 io.Reader 解码一个 Packet。
// 先读取固定 11 字节头部，再根据 Length 读取 Payload。
func DecodePacket(r io.Reader) (*Packet, error) {
	header := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, fmt.Errorf("decode packet header: %w", err)
	}

	pkt := &Packet{
		Magic:   binary.BigEndian.Uint32(header[0:4]),
		Version: binary.BigEndian.Uint16(header[4:6]),
		Type:    header[6],
		Length:  binary.BigEndian.Uint32(header[7:11]),
	}

	if pkt.Magic != Magic {
		return nil, fmt.Errorf("decode packet: invalid magic 0x%08x, expected 0x%08x",
			pkt.Magic, Magic)
	}

	if pkt.Version != ProtocolVersion {
		return nil, fmt.Errorf("decode packet: unsupported version %d, expected %d",
			pkt.Version, ProtocolVersion)
	}

	if pkt.Length > MaxPacketSize {
		return nil, fmt.Errorf("decode packet: payload size %d exceeds maximum %d",
			pkt.Length, MaxPacketSize)
	}

	if pkt.Length > 0 {
		pkt.Payload = make([]byte, pkt.Length)
		if _, err := io.ReadFull(r, pkt.Payload); err != nil {
			return nil, fmt.Errorf("decode packet payload: %w", err)
		}
	}

	return pkt, nil
}

// NewPacket 创建一个新的协议包，自动填充 Magic 和 Version。
func NewPacket(typ uint8, payload []byte) *Packet {
	return &Packet{
		Magic:   Magic,
		Version: ProtocolVersion,
		Type:    typ,
		Length:  uint32(len(payload)),
		Payload: payload,
	}
}

// QueryRequest 是查询请求的 JSON 结构。
type QueryRequest struct {
	SQL string `json:"sql"`
}

// WriteRequest 是写入请求的 JSON 结构。
type WriteRequest struct {
	Table string           `json:"table"`
	Rows  []map[string]any `json:"rows"`
}

// Response 是统一的响应 JSON 结构。
// Columns 携带查询结果集的列名（按 Schema 顺序），供客户端按原始列序渲染表格；
// 写入或无结果集的响应该字段为空（JSON 中因 omitempty 而省略）。
type Response struct {
	Code    int      `json:"code"`
	Message string   `json:"message,omitempty"`
	Data    any      `json:"data,omitempty"`
	Rows    int      `json:"rows,omitempty"`
	Columns []string `json:"columns,omitempty"`
	// ColumnTypes 携带查询结果集每列的 DataType，供进程内调用方（如 pgwire
	// 适配器）获取准确的列类型，以生成正确的 PG RowDescription。
	// 不参与 JSON 序列化，避免变更 TCP/HTTP 线协议。
	ColumnTypes []common.DataType `json:"-"`
}
