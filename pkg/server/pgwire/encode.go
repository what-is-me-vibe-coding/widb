package pgwire

import (
	"fmt"
	"strconv"

	"github.com/jackc/pgproto3/v2"
)

// encodeValue 将 Go 值编码为 PG 文本格式的字节切片。
// 返回 nil 表示 NULL 值（PG 协议中 NULL 以 -1 长度标识）。
func encodeValue(val any) []byte {
	if val == nil {
		return nil
	}
	switch v := val.(type) {
	case bool:
		if v {
			return []byte("t")
		}
		return []byte("f")
	case int64:
		return []byte(strconv.FormatInt(v, 10))
	case float64:
		return []byte(strconv.FormatFloat(v, 'g', -1, 64))
	case string:
		return []byte(v)
	default:
		return []byte(fmt.Sprintf("%v", v))
	}
}

// buildRowDescription 从列名和类型构建 RowDescription 消息。
func buildRowDescription(columns []string, types []pgType) *pgproto3.RowDescription {
	fields := make([]pgproto3.FieldDescription, len(columns))
	for i, col := range columns {
		fields[i] = pgproto3.FieldDescription{
			Name:         []byte(col),
			DataTypeOID:  types[i].OID,
			DataTypeSize: types[i].Size,
			Format:       pgproto3.TextFormat,
		}
	}
	return &pgproto3.RowDescription{Fields: fields}
}

// buildDataRow 从行数据和列顺序构建 DataRow 消息。
func buildDataRow(row map[string]any, columns []string) *pgproto3.DataRow {
	values := make([][]byte, len(columns))
	for i, col := range columns {
		values[i] = encodeValue(row[col])
	}
	return &pgproto3.DataRow{Values: values}
}
