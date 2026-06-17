package pgwire

// SQLExecutor 执行 SQL 语句并返回结构化结果。
// 由服务层实现，pgwire 通过此接口执行客户端发来的查询。
type SQLExecutor interface {
	ExecuteSQL(sql string) (*SQLResult, error)
}

// SQLResult 是 SQL 执行的结果。
type SQLResult struct {
	// Columns 是结果集的列名列表（仅 SELECT 查询有值）。
	Columns []string
	// Rows 是结果集的行数据，每行为列名到值的映射。
	Rows []map[string]any
	// RowsAffected 是受影响的行数（INSERT 返回插入行数）。
	RowsAffected int
	// CommandTag 是 PG 协议的命令标签，如 "SELECT 3"、"INSERT 0 5"、"CREATE TABLE"。
	CommandTag string
	// IsQuery 标识是否为有结果集的查询（SELECT）。
	IsQuery bool
}
