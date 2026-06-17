package pgwire

// PostgreSQL 内置数据类型 OID。
// 参见 https://www.postgresql.org/docs/current/datatype.html
const (
	// OIDBool 是 boolean 类型的 OID。
	OIDBool uint32 = 16
	// OIDInt8 是 int8（64 位整数）类型的 OID。
	OIDInt8 uint32 = 20
	// OIDInt2 是 int2（16 位整数）类型的 OID。
	OIDInt2 uint32 = 21
	// OIDInt4 是 int4（32 位整数）类型的 OID。
	OIDInt4 uint32 = 23
	// OIDText 是 text 类型的 OID（变长字符串）。
	OIDText uint32 = 25
	// OIDFloat8 是 float8（64 位浮点数）类型的 OID。
	OIDFloat8 uint32 = 701
	// OIDTimestamp 是 timestamp 类型的 OID。
	OIDTimestamp uint32 = 1114
	// OIDDate 是 date 类型的 OID。
	OIDDate uint32 = 1082
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

// widbDataType 枚举值，与 pkg/common.DataType 保持同步。
// 此处以常量形式声明，避免 pgwire 包反向依赖 pkg/common。
const (
	widbTypeNull      = 0
	widbTypeBool      = 1
	widbTypeInt64     = 2
	widbTypeFloat64   = 3
	widbTypeString    = 4
	widbTypeTimestamp = 5
	widbTypeDate      = 6
	widbTypeInt8      = 7
	widbTypeInt16     = 8
	widbTypeInt32     = 9
	widbTypeUint64    = 10
)

// dataTypeToPGType 将 widb DataType 映射为 PostgreSQL 列类型元数据。
// 整数族按位宽映射到 PG int2/int4/int8；DATE/TIMESTAMP 映射到对应 PG 类型。
// 未知类型回退到 TEXT。
func dataTypeToPGType(dt int) pgType {
	switch dt {
	case widbTypeBool:
		return pgType{OID: OIDBool, Size: 1}
	case widbTypeInt8, widbTypeInt16:
		return pgType{OID: OIDInt2, Size: 2}
	case widbTypeInt32:
		return pgType{OID: OIDInt4, Size: 4}
	case widbTypeInt64, widbTypeUint64:
		return pgType{OID: OIDInt8, Size: 8}
	case widbTypeFloat64:
		return pgType{OID: OIDFloat8, Size: 8}
	case widbTypeString:
		return pgType{OID: OIDText, Size: -1}
	case widbTypeTimestamp:
		return pgType{OID: OIDTimestamp, Size: 8}
	case widbTypeDate:
		return pgType{OID: OIDDate, Size: 4}
	default:
		return defaultType
	}
}

// columnTypesFromSchema 根据列类型列表构建 PG 类型元数据。
// 当 columnTypes 长度与 columns 不匹配时返回 nil，调用方回退到值推断。
func columnTypesFromSchema(columns []string, columnTypes []int) []pgType {
	if len(columnTypes) != len(columns) {
		return nil
	}
	types := make([]pgType, len(columns))
	for i, dt := range columnTypes {
		types[i] = dataTypeToPGType(dt)
	}
	return types
}
