package pgwire

// PostgreSQL 内置数据类型 OID。
// 参见 https://www.postgresql.org/docs/current/datatype.html
const (
	// OIDBool 是 boolean 类型的 OID。
	OIDBool uint32 = 16
	// OIDInt8 是 int8（64 位整数）类型的 OID。
	OIDInt8 uint32 = 20
	// OIDText 是 text 类型的 OID（变长字符串）。
	OIDText uint32 = 25
	// OIDFloat8 是 float8（64 位浮点数）类型的 OID。
	OIDFloat8 uint32 = 701
	// OIDTimestamp 是 timestamp 类型的 OID。
	OIDTimestamp uint32 = 1114
)

// pgType 持有 PG 列类型的元数据。
type pgType struct {
	OID  uint32 // PostgreSQL 类型 OID
	Size int16  // 类型固定大小（字节），-1 表示变长
}

// defaultType 是未推断出类型时的默认类型（TEXT）。
var defaultType = pgType{OID: OIDText, Size: -1}

// inferTypeFromValue 从 Go 值推断 PG 类型。
func inferTypeFromValue(val any) pgType {
	switch val.(type) {
	case bool:
		return pgType{OID: OIDBool, Size: 1}
	case int64:
		return pgType{OID: OIDInt8, Size: 8}
	case float64:
		return pgType{OID: OIDFloat8, Size: 8}
	case string:
		return pgType{OID: OIDText, Size: -1}
	default:
		return defaultType
	}
}

// inferColumnTypes 从结果行数据推断每列的 PG 类型。
// 遍历所有行，取每列第一个非 nil 值的类型；全为 nil 时默认 TEXT。
func inferColumnTypes(columns []string, rows []map[string]any) []pgType {
	types := make([]pgType, len(columns))
	for i, col := range columns {
		types[i] = defaultType
		for _, row := range rows {
			if val, ok := row[col]; ok && val != nil {
				types[i] = inferTypeFromValue(val)
				break
			}
		}
	}
	return types
}
