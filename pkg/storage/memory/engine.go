// Package memory 提供基于内存的存储引擎实现，适用于临时表、维度表、
// 元数据表等高频小表查询场景。
//
// 与 pkg/storage 中的 LSM 引擎不同，MemoryEngine 将全部数据保存在内存中，
// 不落盘、无 WAL、无 Compaction，因此不具备崩溃恢复能力，重启后数据丢失。
// 优势是零 I/O 延迟、无刷盘开销，适合可重建的临时数据。
//
// 数据按主键排序存储，支持范围扫描与点查。MemoryEngine 满足 server.TableEngine
// 接口，可与 LSM 引擎在同一 Server 中按表共存（通过 CREATE TABLE ... ENGINE=memory 选择）。
package memory

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// rowEntry 是内存引擎中的一行，按 key 排序存储。
type rowEntry struct {
	key string
	row storage.Row
}

// Engine 是基于内存的存储引擎，按主键排序保存全部数据。
//
// 并发安全：所有读写方法通过 sync.RWMutex 保护。读操作（Get/ScanRange）持读锁，
// 写操作（Write/WriteBatch）持写锁。rows 切片始终保持按 key 升序排列，
// 使得范围扫描可用二分查找定位起点，复杂度 O(log n + k)。
type Engine struct {
	mu         sync.RWMutex
	rows       []rowEntry // 按 key 升序排列
	columnMeta []storage.ColumnMeta
	nextVer    atomic.Uint64
	closed     bool
}

// New 创建一个内存引擎实例。
func New() *Engine {
	e := &Engine{}
	e.nextVer.Store(0)
	return e
}

// Write 写入一行数据。若 key 已存在则覆盖（含版本号更新）。
// 空 key 不被允许，与 LSM 引擎保持一致。
func (e *Engine) Write(key string, values map[string]common.Value) error {
	if key == "" {
		return fmt.Errorf("memory engine write: empty key is not allowed")
	}
	version := e.nextVer.Add(1)
	row := storage.Row{Version: version, Columns: values}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errEngineClosed
	}
	e.upsertLocked(key, row)
	return nil
}

// upsertLocked 在已持写锁的前提下插入或更新一行。调用者必须持有 e.mu。
// 使用二分查找定位插入/更新位置，保持 rows 按 key 升序。
func (e *Engine) upsertLocked(key string, row storage.Row) {
	idx := sort.Search(len(e.rows), func(i int) bool {
		return e.rows[i].key >= key
	})
	if idx < len(e.rows) && e.rows[idx].key == key {
		e.rows[idx].row = row
		return
	}
	e.rows = append(e.rows, rowEntry{})
	copy(e.rows[idx+1:], e.rows[idx:])
	e.rows[idx] = rowEntry{key: key, row: row}
}

// WriteBatch 批量写入多行数据，所有行共享一次锁获取，提升批量写入吞吐。
// 空 key 不被允许。批量写入内若某行 key 重复，后写入的行覆盖先写入的行。
//
// 优化：批量写入时先追加全部新行再统一排序去重，避免逐行 upsert 的 O(n) 写放大，
// 复杂度从 O(n*m) 降为 O((n+m) log (n+m))，对大批量写入显著降低耗时。
func (e *Engine) WriteBatch(rows []storage.WriteRow) error {
	if len(rows) == 0 {
		return nil
	}
	for i, r := range rows {
		if r.Key == "" {
			return fmt.Errorf("memory engine write batch: empty key at row %d is not allowed", i)
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errEngineClosed
	}
	base := e.nextVer.Add(uint64(len(rows)))
	// 先追加全部批量行（版本号随 i 递增），再统一排序去重。
	for i, r := range rows {
		version := base - uint64(len(rows)) + uint64(i) + 1
		e.rows = append(e.rows, rowEntry{key: r.Key, row: storage.Row{Version: version, Columns: r.Values}})
	}
	e.sortAndDedupLocked()
	return nil
}

// Delete 删除指定 key 的行。若 key 不存在则静默返回 nil（幂等）。
// 使用二分查找定位，复杂度 O(log n + n)（n 为删除后的切片移动）。
func (e *Engine) Delete(key string) error {
	if key == "" {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errEngineClosed
	}
	idx := sort.Search(len(e.rows), func(i int) bool {
		return e.rows[i].key >= key
	})
	if idx >= len(e.rows) || e.rows[idx].key != key {
		return nil // key 不存在，幂等返回
	}
	e.rows = append(e.rows[:idx], e.rows[idx+1:]...)
	return nil
}

// sortAndDedupLocked 将 e.rows 按 key 升序排序并对同 key 的条目去重（保留最后一个）。
// 调用者必须持有 e.mu。使用稳定排序保证同 key 时后追加的条目（版本号更大）胜出，
// 从而与逐行 upsert 的 last-wins 语义保持一致。
func (e *Engine) sortAndDedupLocked() {
	sort.SliceStable(e.rows, func(i, j int) bool {
		return e.rows[i].key < e.rows[j].key
	})
	if len(e.rows) <= 1 {
		return
	}
	w := 0
	for i := 1; i < len(e.rows); i++ {
		if e.rows[i].key == e.rows[w].key {
			e.rows[w] = e.rows[i] // 后者覆盖前者（last-wins）
		} else {
			w++
			e.rows[w] = e.rows[i]
		}
	}
	e.rows = e.rows[:w+1]
}

// Get 根据主键查询一行数据。未找到返回 (Row{}, false)。
// 使用二分查找，复杂度 O(log n)。
func (e *Engine) Get(key string) (storage.Row, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return storage.Row{}, false
	}
	idx := sort.Search(len(e.rows), func(i int) bool {
		return e.rows[i].key >= key
	})
	if idx < len(e.rows) && e.rows[idx].key == key {
		return e.rows[idx].row, true
	}
	return storage.Row{}, false
}

// ScanRange 扫描键范围 [start, end] 内的所有行，返回按 key 升序排列的结果。
// start 为空表示从最小 key 开始，end 为空表示到最大 key 结束。
// 使用二分查找定位起点，复杂度 O(log n + k)，k 为命中行数。
func (e *Engine) ScanRange(start, end string) []storage.ScanEntry {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return nil
	}
	return e.scanLocked(start, end)
}

// scanLocked 在已持读锁的前提下执行范围扫描。调用者必须持有 e.mu.RLock。
func (e *Engine) scanLocked(start, end string) []storage.ScanEntry {
	startIdx := 0
	if start != "" {
		startIdx = sort.Search(len(e.rows), func(i int) bool {
			return e.rows[i].key >= start
		})
	}

	entries := make([]storage.ScanEntry, 0, 8)
	for i := startIdx; i < len(e.rows); i++ {
		if end != "" && e.rows[i].key > end {
			break
		}
		entries = append(entries, storage.ScanEntry{
			Key:   e.rows[i].key,
			Value: e.rows[i].row,
		})
	}
	return entries
}

// ScanRangeWithPruning 扫描键范围并利用列谓词进行段裁剪。
//
// 内存引擎无 Segment 概念，不存在稀疏索引统计信息，因此不做段裁剪，
// 直接返回范围内全部行。列谓词过滤由查询执行器的 filterEntriesByPredicate 完成，
// 这与 LSM 引擎在 MemTable 数据上的处理方式一致。
func (e *Engine) ScanRangeWithPruning(start, end string, _ []storage.ColumnPredicate) []storage.ScanEntry {
	return e.ScanRange(start, end)
}

// ColumnMeta 返回列元数据的副本。
func (e *Engine) ColumnMeta() []storage.ColumnMeta {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.columnMeta) == 0 {
		return nil
	}
	result := make([]storage.ColumnMeta, len(e.columnMeta))
	copy(result, e.columnMeta)
	return result
}

// SetColumnMeta 设置列元数据，供查询执行器获取表结构信息。
func (e *Engine) SetColumnMeta(cols []storage.ColumnMeta) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.columnMeta = make([]storage.ColumnMeta, len(cols))
	copy(e.columnMeta, cols)
}

// Flush 对内存引擎是空操作（数据已在内存中，无需刷盘）。
// 接受列元数据参数以与 LSM 引擎的 Flush 签名保持一致，便于统一路由。
func (e *Engine) Flush(cols []storage.ColumnMeta) error {
	e.SetColumnMeta(cols)
	return nil
}

// PrimaryIndex 返回 nil。内存引擎使用内置排序切片直接支持点查与范围扫描，
// 不依赖外部主键索引。
func (e *Engine) PrimaryIndex() *index.PrimaryIndex { return nil }

// SparseIndex 返回 nil。内存引擎无 Segment 级稀疏索引统计，
// ScanRangeWithPruning 不做段裁剪。
func (e *Engine) SparseIndex() *index.SparseIndex { return nil }

// StartScheduler 是空操作。内存引擎无后台任务（无 Compaction、无 WAL 清理）。
func (e *Engine) StartScheduler(_ storage.SchedulerConfig) {}

// Close 关闭引擎，释放内存数据。多次调用是安全的。
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	e.rows = nil
	e.columnMeta = nil
	return nil
}

// RowCount 返回当前引擎中的行数，主要用于测试与监控。
func (e *Engine) RowCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.rows)
}

// EngineStats 汇总内存引擎的运行时状态。
// 与 pkg/storage.EngineStats 字段不一一对应，因为内存引擎无 Segment / MemTable 概念。
type EngineStats struct {
	RowCount    int64
	ColumnCount int
}

// Stats 返回内存引擎的运行时状态快照。
// 字段意义：
//   - RowCount：当前内存表中的存活行数。
//   - ColumnCount：列元数据中已声明的列数。
func (e *Engine) Stats() EngineStats {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return EngineStats{
		RowCount:    int64(len(e.rows)),
		ColumnCount: len(e.columnMeta),
	}
}

// errEngineClosed 表示在引擎已关闭后执行操作。
var errEngineClosed = fmt.Errorf("memory engine: already closed")
