package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// testingTime 是测试用的时间常量。
var testingTime = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// fmtErrorf 封装 fmt.Errorf 用于测试。
func fmtErrorf(format string, args ...interface{}) error {
	return fmt.Errorf(format, args...)
}

// --- 协议编解码测试 ---

// verifyPacket 验证解码后的 Packet 与原始 Packet 一致。
func verifyPacket(t *testing.T, got, want *Packet) {
	t.Helper()
	if got.Magic != want.Magic {
		t.Errorf("Magic = 0x%08x, 期望 0x%08x", got.Magic, want.Magic)
	}
	if got.Version != want.Version {
		t.Errorf("Version = %d, 期望 %d", got.Version, want.Version)
	}
	if got.Type != want.Type {
		t.Errorf("Type = %d, 期望 %d", got.Type, want.Type)
	}
	if got.Length != want.Length {
		t.Errorf("Length = %d, 期望 %d", got.Length, want.Length)
	}
	if !bytes.Equal(got.Payload, want.Payload) {
		t.Errorf("Payload 不匹配")
	}
}

func TestPacketEncodeDecode(t *testing.T) {
	tests := []struct {
		name    string
		pkt     *Packet
		wantErr bool
	}{
		{"查询包", NewPacket(PacketQuery, []byte(`{"sql":"SELECT 1"}`)), false},
		{"写入包", NewPacket(PacketWrite, []byte(`{"table":"t","rows":[{"id":1}]}`)), false},
		{"心跳包", NewPacket(PacketPing, nil), false},
		{"空Payload", NewPacket(PacketResponse, []byte{}), false},
		{"大Payload", NewPacket(PacketQuery, bytes.Repeat([]byte("x"), 4096)), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := tt.pkt.Encode()
			expectedLen := HeaderSize + len(tt.pkt.Payload)
			if len(encoded) != expectedLen {
				t.Fatalf("编码长度 = %d, 期望 %d", len(encoded), expectedLen)
			}

			decoded, err := DecodePacket(bytes.NewReader(encoded))
			if (err != nil) != tt.wantErr {
				t.Fatalf("DecodePacket() 错误 = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			verifyPacket(t, decoded, tt.pkt)
		})
	}
}

func TestDecodePacketInvalidMagic(t *testing.T) {
	pkt := &Packet{
		Magic:   0xDEADBEEF,
		Version: ProtocolVersion,
		Type:    PacketQuery,
		Length:  0,
	}
	encoded := pkt.Encode()

	_, err := DecodePacket(bytes.NewReader(encoded))
	if err == nil {
		t.Fatal("期望返回魔数错误，但成功解码")
	}
	if !strings.Contains(err.Error(), "invalid magic") {
		t.Errorf("错误信息应包含 'invalid magic'，实际: %v", err)
	}
}

func TestDecodePacketTruncatedHeader(t *testing.T) {
	_, err := DecodePacket(bytes.NewReader([]byte{0x57, 0x49}))
	if err == nil {
		t.Fatal("期望返回读取错误，但成功解码")
	}
}

func TestDecodePacketTruncatedPayload(t *testing.T) {
	pkt := NewPacket(PacketQuery, []byte("hello"))
	encoded := pkt.Encode()

	truncated := encoded[:HeaderSize+2]
	_, err := DecodePacket(bytes.NewReader(truncated))
	if err == nil {
		t.Fatal("期望返回读取错误，但成功解码")
	}
}

func TestNewPacket(t *testing.T) {
	payload := []byte(`{"sql":"SELECT 1"}`)
	pkt := NewPacket(PacketQuery, payload)

	if pkt.Magic != Magic {
		t.Errorf("Magic = 0x%08x, 期望 0x%08x", pkt.Magic, Magic)
	}
	if pkt.Version != ProtocolVersion {
		t.Errorf("Version = %d, 期望 %d", pkt.Version, ProtocolVersion)
	}
	if pkt.Type != PacketQuery {
		t.Errorf("Type = %d, 期望 %d", pkt.Type, PacketQuery)
	}
	if pkt.Length != uint32(len(payload)) {
		t.Errorf("Length = %d, 期望 %d", pkt.Length, len(payload))
	}
}

// --- JSON 结构测试 ---

func TestQueryRequestJSON(t *testing.T) {
	original := QueryRequest{SQL: testSelectAll}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	var decoded QueryRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}

	if decoded.SQL != original.SQL {
		t.Errorf("SQL = %q, 期望 %q", decoded.SQL, original.SQL)
	}
}

func TestWriteRequestJSON(t *testing.T) {
	original := WriteRequest{
		Table: testTable,
		Rows: []map[string]interface{}{
			{"id": float64(1), testColName: testName},
		},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	var decoded WriteRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}

	if decoded.Table != original.Table {
		t.Errorf("Table = %q, 期望 %q", decoded.Table, original.Table)
	}
}

func TestResponseJSON(t *testing.T) {
	resp := &Response{Code: 0, Data: []int{1, 2, 3}, Rows: 3}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}

	if decoded.Code != 0 {
		t.Errorf("Code = %d, 期望 0", decoded.Code)
	}
	if decoded.Rows != 3 {
		t.Errorf("Rows = %d, 期望 3", decoded.Rows)
	}
}

// --- interfaceToValue 测试 ---

func TestInterfaceToValue(t *testing.T) {
	tests := []struct {
		name    string
		raw     interface{}
		typ     common.DataType
		wantErr bool
	}{
		{"nil值", nil, common.TypeNull, false},
		{"bool真", true, common.TypeBool, false},
		{"bool假", false, common.TypeBool, false},
		{"int64_从float64", float64(42), common.TypeInt64, false},
		{"int64_从int", 42, common.TypeInt64, false},
		{"float64值", 3.14, common.TypeFloat64, false},
		{"float64_从int", 42, common.TypeFloat64, false},
		{"string值", testStrHello, common.TypeString, false},
		{"timestamp值", "2024-01-01T00:00:00Z", common.TypeTimestamp, false},
		{"bool类型错误", "true", common.TypeBool, true},
		{"int64类型错误", "42", common.TypeInt64, true},
		{"string类型错误", 42, common.TypeString, true},
		{"timestamp格式错误", "invalid", common.TypeTimestamp, true},
		{"timestamp类型错误", 42, common.TypeTimestamp, true},
		{"不支持的数据类型", "x", common.DataType(99), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := interfaceToValue(tt.raw, tt.typ)
			if (err != nil) != tt.wantErr {
				t.Errorf("interfaceToValue() 错误 = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}
			if tt.raw == nil {
				if !val.IsNull() {
					t.Error("期望 NULL 值")
				}
			}
		})
	}
}

// --- valueToInterface 测试 ---

func TestValueToInterface(t *testing.T) {
	ts := testingTime

	tests := []struct {
		name string
		val  common.Value
		want interface{}
	}{
		{"null", common.NewNull(), nil},
		{"bool_true", common.NewBool(true), true},
		{"bool_false", common.NewBool(false), false},
		{"int64", common.NewInt64(42), int64(42)},
		{"float64", common.NewFloat64(3.14), 3.14},
		{"string", common.NewString("hello"), "hello"},
		{"timestamp", common.NewTimestamp(ts), ts.Format("2006-01-02T15:04:05.999999999Z07:00")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := valueToInterface(tt.val)
			if got != tt.want {
				t.Errorf("valueToInterface() = %v (%T), 期望 %v (%T)", got, got, tt.want, tt.want)
			}
		})
	}
}

// --- 辅助函数测试 ---

func TestIsClosedConnErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"EOF", io.EOF, true},
		{"nil", nil, false},
		{"其他错误", fmtErrorf("some error"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isClosedConnErr(tt.err); got != tt.want {
				t.Errorf("isClosedConnErr() = %v, 期望 %v", got, tt.want)
			}
		})
	}
}

func TestCountRows(t *testing.T) {
	result := countRows(nil)
	if result != 0 {
		t.Errorf("countRows(nil) = %d, 期望 0", result)
	}

	result = countRows([]*storage.Chunk{nil})
	if result != 0 {
		t.Errorf("countRows([nil]) = %d, 期望 0", result)
	}

	chunk := storage.NewChunk(4)
	col := storage.NewColumnVector(0, common.TypeInt64, 4)
	_ = col.Append(common.NewInt64(1))
	_ = col.Append(common.NewInt64(2))
	_ = chunk.AddColumn(col)

	result = countRows([]*storage.Chunk{chunk})
	if result != 2 {
		t.Errorf("countRows = %d, 期望 2", result)
	}
}

func TestChunksToRows(t *testing.T) {
	result := chunksToRows(nil)
	if result != nil {
		t.Errorf("chunksToRows(nil) 应返回 nil, 实际: %v", result)
	}

	result = chunksToRows([]*storage.Chunk{nil})
	if result != nil {
		t.Errorf("chunksToRows([nil]) 应返回 nil, 实际: %v", result)
	}

	chunk := storage.NewChunk(4)
	col := storage.NewColumnVector(0, common.TypeInt64, 4)
	_ = col.Append(common.NewInt64(42))
	_ = chunk.AddColumn(col)

	result = chunksToRows([]*storage.Chunk{chunk})
	if len(result) != 1 {
		t.Fatalf("结果行数 = %d, 期望 1", len(result))
	}
	if result[0]["col_0"] != int64(42) {
		t.Errorf("col_0 = %v, 期望 42", result[0]["col_0"])
	}
}

func TestToFloat64ValueErrors(t *testing.T) {
	_, err := toFloat64Value("not a number")
	if err == nil {
		t.Error("期望返回类型不匹配错误")
	}
}

func TestToInt64ValueErrors(t *testing.T) {
	_, err := toInt64Value("not a number")
	if err == nil {
		t.Error("期望返回类型不匹配错误")
	}
}

// TestDecodePacketWrongVersion 验证 DecodePacket 拒绝错误版本号的包。
// 修复前可能未检查版本号，导致不兼容的客户端请求被错误接受。
func TestDecodePacketWrongVersion(t *testing.T) {
	tests := []struct {
		name    string
		version uint16
	}{
		{"版本0", 0},
		{"版本2", 2},
		{"版本255", 255},
		{"版本999", 999},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := &Packet{
				Magic:   Magic,
				Version: tt.version,
				Type:    PacketQuery,
				Length:  0,
			}
			encoded := pkt.Encode()

			_, err := DecodePacket(bytes.NewReader(encoded))
			if err == nil {
				t.Fatalf("期望返回版本错误，但成功解码（version=%d）", tt.version)
			}
			if !strings.Contains(err.Error(), "unsupported version") {
				t.Errorf("错误信息应包含 'unsupported version'，实际: %v", err)
			}
		})
	}
}

// TestDecodePacketCorrectVersion 验证 DecodePacket 接受正确版本号的包。
func TestDecodePacketCorrectVersion(t *testing.T) {
	pkt := NewPacket(PacketPing, nil)
	encoded := pkt.Encode()

	decoded, err := DecodePacket(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("期望成功解码，但返回错误: %v", err)
	}
	if decoded.Version != ProtocolVersion {
		t.Errorf("Version = %d, 期望 %d", decoded.Version, ProtocolVersion)
	}
}
