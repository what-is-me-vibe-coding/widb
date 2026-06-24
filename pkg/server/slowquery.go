// Package server 慢查询日志：环形缓冲记录执行耗时超过阈值的 SQL，供运维排障。
//
// 设计要点：
//   - 线程安全：通过 RWMutex 保护环形缓冲，多 goroutine 并发记录/读取均安全。
//   - 固定容量：超过容量后覆盖最旧记录，避免长时间运行内存无界增长。
//   - 顺序输出：Snapshot 始终按"从新到旧"返回，便于运维直接定位最新慢查询。
//   - 入口过滤：执行耗时小于阈值的记录直接跳过，避免无谓的复制与锁竞争。
package server

import (
	"sort"
	"sync"
	"time"
)

// SlowQuerySource 标识慢查询的来源协议，便于在混合部署中区分瓶颈点。
type SlowQuerySource string

const (
	// SlowQuerySourceHTTP 表示来自 HTTP /query 与 /write 端点。
	SlowQuerySourceHTTP SlowQuerySource = "http"
	// SlowQuerySourceTCP 表示来自自定义 TCP 协议。
	SlowQuerySourceTCP SlowQuerySource = "tcp"
	// SlowQuerySourcePGWire 表示来自 PostgreSQL wire 协议。
	SlowQuerySourcePGWire SlowQuerySource = "pgwire"
	// SlowQuerySourceInProc 表示来自进程内 ExecuteQuery/ExecuteWrite。
	SlowQuerySourceInProc SlowQuerySource = "inproc"
)

// slowQueryMaxSQLLength 是慢查询日志中保存的 SQL 文本最大字节数。
// 超过该长度的 SQL 会被截断并追加 "... (truncated)" 标记，避免单条记录占用过多内存。
const slowQueryMaxSQLLength = 4096

// SlowQueryRecord 是单条慢查询记录。
type SlowQueryRecord struct {
	// Timestamp 是查询开始执行的时间，便于按窗口统计与排序。
	Timestamp time.Time `json:"timestamp"`
	// Duration 是查询耗时，从执行开始到结束的间隔。
	Duration time.Duration `json:"duration_ns"`
	// Source 是查询来源协议标签（http/tcp/pgwire/inproc）。
	Source SlowQuerySource `json:"source"`
	// SQL 是被执行的 SQL 文本；过长会被截断。
	SQL string `json:"sql"`
	// Error 记录执行错误信息；空字符串表示执行成功。
	Error string `json:"error,omitempty"`
}

// SlowQueryLog 是线程安全的慢查询环形日志。
// 零值不可用，请使用 NewSlowQueryLog 构造。
type SlowQueryLog struct {
	// threshold 是慢查询判定阈值。Duration < threshold 的执行不会被记录。
	threshold time.Duration
	// capacity 是环形缓冲容量；capacity <= 0 时使用 100 的默认值。
	capacity int
	// mu 保护以下状态。读多写少，选用 RWMutex 让 Snapshot 走读锁。
	mu      sync.RWMutex
	buf     []SlowQueryRecord
	head    int // 下一个写入位置
	size    int // 已写入的有效记录数（<= cap(buf)）
	enabled bool
}

// NewSlowQueryLog 构造指定阈值与容量的慢查询日志。
// threshold <= 0 时日志自动禁用，所有 Record 调用均为 no-op，避免在低阈值场景误捕获正常查询。
// capacity <= 0 时回退到默认值 100，保持历史行为。
func NewSlowQueryLog(threshold time.Duration, capacity int) *SlowQueryLog {
	return &SlowQueryLog{
		threshold: threshold,
		capacity:  normalizeCapacity(capacity),
		buf:       make([]SlowQueryRecord, normalizeCapacity(capacity)),
		enabled:   threshold > 0,
	}
}

// normalizeCapacity 把负数/零容量归一为默认值 100。
func normalizeCapacity(capacity int) int {
	if capacity <= 0 {
		return 100
	}
	return capacity
}

// Enabled 返回慢查询日志是否启用（threshold > 0）。
// Record 调用在禁用时为 no-op；上层可在调用 Record 前用 Enabled 短路避免构造 SlowQueryRecord。
func (l *SlowQueryLog) Enabled() bool {
	if l == nil {
		return false
	}
	return l.enabled
}

// Threshold 返回当前阈值，便于 /admin/slow-queries 响应附带配置回显。
func (l *SlowQueryLog) Threshold() time.Duration {
	if l == nil {
		return 0
	}
	return l.threshold
}

// Capacity 返回环形缓冲容量，便于 /admin/slow-queries 响应附带配置回显。
func (l *SlowQueryLog) Capacity() int {
	if l == nil {
		return 0
	}
	return l.capacity
}

// Record 记录一条慢查询。duration < threshold 或日志禁用时直接返回。
// source 与 sql 会被复制保存，避免调用方继续修改底层字符串。
// errMsg 非空时记录到记录的 Error 字段，便于运维区分"慢但成功"与"慢且失败"。
func (l *SlowQueryLog) Record(duration time.Duration, source SlowQuerySource, sql string, errMsg string) {
	if l == nil || !l.enabled {
		return
	}
	if duration < l.threshold {
		return
	}
	rec := SlowQueryRecord{
		Timestamp: time.Now(),
		Duration:  duration,
		Source:    source,
		SQL:       truncateSQL(sql, slowQueryMaxSQLLength),
		Error:     errMsg,
	}
	l.mu.Lock()
	l.buf[l.head] = rec
	l.head = (l.head + 1) % l.capacity
	if l.size < l.capacity {
		l.size++
	}
	l.mu.Unlock()
}

// Snapshot 返回当前所有慢查询记录，从最新到最旧排序。
// 返回的切片是新分配的，调用方可以安全地修改而不会影响底层缓冲。
// 日志为空时返回长度为 0 的切片而非 nil，便于调用方直接 range。
func (l *SlowQueryLog) Snapshot() []SlowQueryRecord {
	if l == nil {
		return []SlowQueryRecord{}
	}
	l.mu.RLock()
	n := l.size
	if n == 0 {
		l.mu.RUnlock()
		return []SlowQueryRecord{}
	}
	out := make([]SlowQueryRecord, 0, n)
	// 从 head-1 倒序遍历环形缓冲，确保最新记录在结果首位。
	for i := 0; i < n; i++ {
		idx := (l.head - 1 - i + l.capacity) % l.capacity
		out = append(out, l.buf[idx])
	}
	l.mu.RUnlock()
	// 二次稳定排序：极端并发场景下环形缓冲内顺序可能不再严格单调，
	// 按时间戳再排一次保证外部观察的一致性。
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Timestamp.After(out[j].Timestamp)
	})
	return out
}

// Reset 清空所有记录。测试场景使用，调用方需自行保证无并发 Record。
func (l *SlowQueryLog) Reset() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.buf = make([]SlowQueryRecord, l.capacity)
	l.head = 0
	l.size = 0
	l.mu.Unlock()
}

// truncateSQL 把过长 SQL 截断到 max 字节并追加 "... (truncated)" 标记。
// max <= 0 时返回原字符串，避免误用造成空结果。
func truncateSQL(sql string, max int) string {
	if max <= 0 || len(sql) <= max {
		return sql
	}
	const suffix = "... (truncated)"
	// 预留 suffix 长度，确保最终字符串总长度不超过 max。
	keep := max - len(suffix)
	if keep < 0 {
		keep = 0
	}
	return sql[:keep] + suffix
}
