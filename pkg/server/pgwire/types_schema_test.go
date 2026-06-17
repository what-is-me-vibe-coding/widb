package pgwire

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestDataTypeToPGType 验证 widb DataType 到 PG 类型的映射。
// 修复前 DATE/TIMESTAMP 经值推断会被错误映射为 TEXT，此处确保按位宽与语义精确映射。
func TestDataTypeToPGType(t *testing.T) {
	tests := []struct {
		name string
		dt   int
		want pgType
	}{
		{"bool", widbTypeBool, pgType{OID: OIDBool, Size: 1}},
		{"int8", widbTypeInt8, pgType{OID: OIDInt2, Size: 2}},
		{"int16", widbTypeInt16, pgType{OID: OIDInt2, Size: 2}},
		{"int32", widbTypeInt32, pgType{OID: OIDInt4, Size: 4}},
		{"int64", widbTypeInt64, pgType{OID: OIDInt8, Size: 8}},
		{"uint64", widbTypeUint64, pgType{OID: OIDInt8, Size: 8}},
		{"date", widbTypeDate, pgType{OID: OIDDate, Size: 4}},
		{"float64", widbTypeFloat64, pgType{OID: OIDFloat8, Size: 8}},
		{"string", widbTypeString, pgType{OID: OIDText, Size: -1}},
		{"timestamp", widbTypeTimestamp, pgType{OID: OIDTimestamp, Size: 8}},
		{"null falls to default", widbTypeNull, defaultType},
		{"unknown falls to default", 999, defaultType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dataTypeToPGType(tt.dt)
			if got != tt.want {
				t.Errorf("dataTypeToPGType(%d) = %+v, want %+v", tt.dt, got, tt.want)
			}
		})
	}
}

// TestColumnTypesFromSchema 验证根据列类型列表构建 PG 类型元数据。
func TestColumnTypesFromSchema(t *testing.T) {
	t.Run("matched length", func(t *testing.T) {
		cols := []string{"id", "name", "ts", "d"}
		colTypes := []int{widbTypeInt64, widbTypeString, widbTypeTimestamp, widbTypeDate}
		got := columnTypesFromSchema(cols, colTypes)
		if got == nil {
			t.Fatal("期望非 nil")
		}
		wantOIDs := []uint32{OIDInt8, OIDText, OIDTimestamp, OIDDate}
		for i, want := range wantOIDs {
			if got[i].OID != want {
				t.Errorf("列 %s 期望 OID %d, got %d", cols[i], want, got[i].OID)
			}
		}
	})

	t.Run("mismatched length returns nil", func(t *testing.T) {
		cols := []string{"a", "b"}
		colTypes := []int{widbTypeInt64}
		if got := columnTypesFromSchema(cols, colTypes); got != nil {
			t.Errorf("长度不匹配应返回 nil, got %v", got)
		}
	})

	t.Run("empty columns", func(t *testing.T) {
		got := columnTypesFromSchema(nil, nil)
		if got == nil || len(got) != 0 {
			t.Errorf("空列应返回空切片, got %v", got)
		}
	})

	t.Run("nil colTypes returns nil", func(t *testing.T) {
		cols := []string{"a"}
		if got := columnTypesFromSchema(cols, nil); got != nil {
			t.Errorf("nil colTypes 应返回 nil, got %v", got)
		}
	})
}

// parseRowDescriptionOIDs 从 RowDescription 消息体中解析每列的 DataTypeOID。
// 消息体格式: fieldCount(2) + [name\0 + tableOID(4) + colAttr(2) + typeOID(4) + typeSize(2) + typeMod(4) + format(2)]*
func parseRowDescriptionOIDs(body []byte) []uint32 {
	if len(body) < 2 {
		return nil
	}
	fieldCount := int(binary.BigEndian.Uint16(body[0:2]))
	oids := make([]uint32, 0, fieldCount)
	pos := 2
	for i := 0; i < fieldCount; i++ {
		// 跳过 name（以 \0 结尾）
		end := bytes.IndexByte(body[pos:], 0)
		if end < 0 {
			return oids
		}
		pos += end + 1
		// tableOID(4) + colAttr(2) = 6 字节
		pos += 6
		// typeOID(4)
		oid := binary.BigEndian.Uint32(body[pos : pos+4])
		oids = append(oids, oid)
		// typeSize(2) + typeMod(4) + format(2) = 8 字节
		pos += 4 + 8
	}
	return oids
}

// TestConnQuerySchemaTypes 验证设置了 ColumnTypes 的查询结果，
// RowDescription 中的 OID 与 Schema 类型一致（修复 DATE/TIMESTAMP 被误报为 TEXT）。
func TestConnQuerySchemaTypes(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns: []string{"id", "name", "score", "active", "ts", "d", "small"},
		ColumnTypes: []int{
			widbTypeInt64, widbTypeString, widbTypeFloat64,
			widbTypeBool, widbTypeTimestamp, widbTypeDate, widbTypeInt16,
		},
		Rows: []map[string]any{
			{
				"id": int64(1), "name": "alice", "score": float64(9.5),
				"active": true, "ts": "2024-01-02T03:04:05Z", "d": "2024-01-02",
				"small": int64(7),
			},
		},
		IsQuery: true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendQuery("SELECT * FROM t"); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	// 读取 RowDescription('T')
	mt, body, err := client.readMessage()
	if err != nil {
		t.Fatalf("读取 RowDescription 失败: %v", err)
	}
	if mt != 'T' {
		t.Fatalf("期望 RowDescription('T'), got %c", mt)
	}
	gotOIDs := parseRowDescriptionOIDs(body)
	wantOIDs := []uint32{OIDInt8, OIDText, OIDFloat8, OIDBool, OIDTimestamp, OIDDate, OIDInt2}
	if len(gotOIDs) != len(wantOIDs) {
		t.Fatalf("期望 %d 列, got %d (oids=%v)", len(wantOIDs), len(gotOIDs), gotOIDs)
	}
	for i, want := range wantOIDs {
		if gotOIDs[i] != want {
			t.Errorf("列 %d 期望 OID %d, got %d", i, want, gotOIDs[i])
		}
	}
	// 读取剩余消息直到 ReadyForQuery
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("读取剩余响应失败: %v", err)
	}
}

// TestConnQuerySchemaTypesAllNil 验证全 NULL 行时 Schema 类型仍能正确上报。
// 修复前值推断会将全 NULL 列报为 TEXT；Schema 类型修复后应报为实际类型。
func TestConnQuerySchemaTypesAllNil(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns:     []string{"d", "ts"},
		ColumnTypes: []int{widbTypeDate, widbTypeTimestamp},
		Rows:        []map[string]any{{"d": nil, "ts": nil}},
		IsQuery:     true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendQuery("SELECT * FROM t"); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	mt, body, err := client.readMessage()
	if err != nil {
		t.Fatalf("读取 RowDescription 失败: %v", err)
	}
	if mt != 'T' {
		t.Fatalf("期望 RowDescription('T'), got %c", mt)
	}
	gotOIDs := parseRowDescriptionOIDs(body)
	wantOIDs := []uint32{OIDDate, OIDTimestamp}
	if len(gotOIDs) != len(wantOIDs) {
		t.Fatalf("期望 %d 列, got %d (oids=%v)", len(wantOIDs), len(gotOIDs), gotOIDs)
	}
	for i, want := range wantOIDs {
		if gotOIDs[i] != want {
			t.Errorf("列 %d 期望 OID %d, got %d", i, want, gotOIDs[i])
		}
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("读取剩余响应失败: %v", err)
	}
}

// TestConnQueryFallbackInfer 验证未设置 ColumnTypes 时回退到值推断。
func TestConnQueryFallbackInfer(t *testing.T) {
	exec := &mockExecutor{result: &SQLResult{
		Columns: []string{"id", "name"},
		// 不设置 ColumnTypes，应回退到值推断
		Rows: []map[string]any{
			{"id": int64(1), "name": "alice"},
		},
		IsQuery: true,
	}}
	srv := startTestServer(t, exec)
	defer srv.Stop()

	client := newPGClient(t, srv.Addr())
	defer client.close()
	if err := client.sendStartupMessage(); err != nil {
		t.Fatalf("sendStartupMessage 失败: %v", err)
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := client.sendQuery("SELECT * FROM t"); err != nil {
		t.Fatalf("sendQuery 失败: %v", err)
	}
	mt, body, err := client.readMessage()
	if err != nil {
		t.Fatalf("读取 RowDescription 失败: %v", err)
	}
	if mt != 'T' {
		t.Fatalf("期望 RowDescription('T'), got %c", mt)
	}
	gotOIDs := parseRowDescriptionOIDs(body)
	// 值推断: int64 -> OIDInt8, string -> OIDText
	wantOIDs := []uint32{OIDInt8, OIDText}
	if len(gotOIDs) != len(wantOIDs) {
		t.Fatalf("期望 %d 列, got %d (oids=%v)", len(wantOIDs), len(gotOIDs), gotOIDs)
	}
	for i, want := range wantOIDs {
		if gotOIDs[i] != want {
			t.Errorf("列 %d 期望 OID %d, got %d", i, want, gotOIDs[i])
		}
	}
	if _, err := client.readUntilReadyForQuery(); err != nil {
		t.Fatalf("读取剩余响应失败: %v", err)
	}
}

// TestNewOIDConstants 验证新增的 OID 常量值符合 PostgreSQL 标准。
func TestNewOIDConstants(t *testing.T) {
	if OIDInt2 != 21 {
		t.Errorf("OIDInt2 期望 21, got %d", OIDInt2)
	}
	if OIDInt4 != 23 {
		t.Errorf("OIDInt4 期望 23, got %d", OIDInt4)
	}
	if OIDDate != 1082 {
		t.Errorf("OIDDate 期望 1082, got %d", OIDDate)
	}
}
