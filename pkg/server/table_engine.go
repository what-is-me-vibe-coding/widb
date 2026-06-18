package server

import (
	"fmt"
	"sync"

	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
	"github.com/what-is-me-vibe-coding/test-db/pkg/query"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage/memory"
)

// TableEngine 抽象表级存储引擎，LSM 引擎（*storage.Engine）与内存引擎
// （*memory.Engine）均实现此接口，使 Server 可按表路由读写请求。
type TableEngine interface {
	Write(key string, values map[string]common.Value) error
	WriteBatch(rows []storage.WriteRow) error
	Delete(key string) error
	ScanRange(start, end string) []storage.ScanEntry
	ScanRangeWithPruning(start, end string, preds []storage.ColumnPredicate) []storage.ScanEntry
	ColumnMeta() []storage.ColumnMeta
	PrimaryIndex() *index.PrimaryIndex
	SparseIndex() *index.SparseIndex
	Close() error
}

// engineAdapter 将单个 TableEngine 适配为 query.StorageProvider。
// 用于在 executor 的 ForTable 路由中返回特定表的存储视图。
type engineAdapter struct {
	engine TableEngine
}

func (a *engineAdapter) ScanRange(start, end string) []storage.ScanEntry {
	return a.engine.ScanRange(start, end)
}

func (a *engineAdapter) ScanRangeWithPruning(start, end string, preds []storage.ColumnPredicate) []storage.ScanEntry {
	return a.engine.ScanRangeWithPruning(start, end, preds)
}

func (a *engineAdapter) ColumnMeta() []storage.ColumnMeta { return a.engine.ColumnMeta() }

func (a *engineAdapter) PrimaryIndex() *index.PrimaryIndex { return a.engine.PrimaryIndex() }

func (a *engineAdapter) SparseIndex() *index.SparseIndex { return a.engine.SparseIndex() }

// routingAdapter 是表感知的 StorageProvider，按表名路由到不同的 TableEngine。
// 每张 LSM 表拥有独立的 *storage.Engine（位于 dataDir/tables/<name>/），
// 实现表间键空间、列元数据与 WAL 的完全隔离；内存引擎表通过 ForTable 返回专属适配器。
// defaultEng 仅作为未注册引擎的回退（兼容历史数据），不再承载新表数据。
// 同时实现 query.TableStorageProvider 接口，使 executor 能按 ScanNode.Table 路由。
type routingAdapter struct {
	defaultEng TableEngine
	lsmEngines map[string]TableEngine // 每张 LSM 表的独立引擎
	memEngines map[string]TableEngine
	mu         sync.RWMutex
}

// newRoutingAdapter 创建表路由适配器。
func newRoutingAdapter(defaultEng TableEngine) *routingAdapter {
	return &routingAdapter{
		defaultEng: defaultEng,
		lsmEngines: make(map[string]TableEngine),
		memEngines: make(map[string]TableEngine),
	}
}

// registerMemoryEngine 注册一张内存引擎表。重复注册同一表名会返回错误。
func (r *routingAdapter) registerMemoryEngine(table string, eng TableEngine) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.memEngines[table]; ok {
		return fmt.Errorf("memory engine for table %q already registered", table)
	}
	r.memEngines[table] = eng
	return nil
}

// registerLSMEngine 注册一张 LSM 表的独立引擎。重复注册同一表名会返回错误。
func (r *routingAdapter) registerLSMEngine(table string, eng TableEngine) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.lsmEngines[table]; ok {
		return fmt.Errorf("lsm engine for table %q already registered", table)
	}
	r.lsmEngines[table] = eng
	return nil
}

// unregisterMemoryEngine 注销一张内存引擎表并关闭其引擎，释放占用的内存。
// 用于 CREATE TABLE 失败时的回滚，以及未来 DROP TABLE 的支持。
// 若该表未注册内存引擎，返回错误。
func (r *routingAdapter) unregisterMemoryEngine(table string) error {
	r.mu.Lock()
	eng, ok := r.memEngines[table]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("memory engine for table %q not registered", table)
	}
	delete(r.memEngines, table)
	r.mu.Unlock()
	_ = eng.Close()
	return nil
}

// unregisterLSMEngine 注销一张 LSM 表的独立引擎并关闭之，释放数据目录资源。
// 用于 DROP TABLE 与 CREATE TABLE 失败回滚。若该表未注册独立引擎，返回错误。
func (r *routingAdapter) unregisterLSMEngine(table string) error {
	r.mu.Lock()
	eng, ok := r.lsmEngines[table]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("lsm engine for table %q not registered", table)
	}
	delete(r.lsmEngines, table)
	r.mu.Unlock()
	_ = eng.Close()
	return nil
}

// engineForTable 返回指定表的引擎。优先返回该表的独立 LSM 引擎，其次内存引擎，
// 最后回退到默认引擎（兼容历史数据）。
func (r *routingAdapter) engineForTable(table string) TableEngine {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if eng, ok := r.lsmEngines[table]; ok {
		return eng
	}
	if eng, ok := r.memEngines[table]; ok {
		return eng
	}
	return r.defaultEng
}

// forEachLSMEngine 遍历所有已注册的 LSM 表引擎，用于批量启动调度器等操作。
// 遍历期间持读锁，调用方不应在 fn 中修改路由表。
func (r *routingAdapter) forEachLSMEngine(fn func(eng TableEngine)) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, eng := range r.lsmEngines {
		fn(eng)
	}
}

// ForTable 返回指定表的 StorageProvider，实现 query.TableStorageProvider。
func (r *routingAdapter) ForTable(table string) query.StorageProvider {
	return &engineAdapter{engine: r.engineForTable(table)}
}

// closeAll 关闭所有内存引擎与 LSM 表引擎。默认 LSM 引擎由 Server.Stop 单独关闭。
func (r *routingAdapter) closeAll() {
	r.mu.Lock()
	lsmEngines := r.lsmEngines
	r.lsmEngines = make(map[string]TableEngine)
	memEngines := r.memEngines
	r.memEngines = make(map[string]TableEngine)
	r.mu.Unlock()
	for _, eng := range lsmEngines {
		_ = eng.Close()
	}
	for _, eng := range memEngines {
		_ = eng.Close()
	}
}

// 以下方法将无表上下文的调用委托给默认 LSM 引擎，保持 query.StorageProvider 兼容性。
// executor 在 ScanNode.Table 为空或未实现 ForTable 时回退到这些方法。
func (r *routingAdapter) ScanRange(start, end string) []storage.ScanEntry {
	return r.defaultEng.ScanRange(start, end)
}

func (r *routingAdapter) ScanRangeWithPruning(start, end string, preds []storage.ColumnPredicate) []storage.ScanEntry {
	return r.defaultEng.ScanRangeWithPruning(start, end, preds)
}

func (r *routingAdapter) ColumnMeta() []storage.ColumnMeta { return r.defaultEng.ColumnMeta() }

func (r *routingAdapter) PrimaryIndex() *index.PrimaryIndex { return r.defaultEng.PrimaryIndex() }

func (r *routingAdapter) SparseIndex() *index.SparseIndex { return r.defaultEng.SparseIndex() }

// buildColumnMeta 从 catalog 列定义构建 storage.ColumnMeta。
// 用于在创建内存引擎表时初始化列元数据。
func buildColumnMeta(cols []catalog.ColumnDef) []storage.ColumnMeta {
	meta := make([]storage.ColumnMeta, len(cols))
	for i, c := range cols {
		meta[i] = storage.ColumnMeta{ID: uint32(i), Name: c.Name, Type: c.Type}
	}
	return meta
}

// createMemoryEngine 创建一个内存引擎并设置列元数据。
func createMemoryEngine(cols []catalog.ColumnDef) *memory.Engine {
	eng := memory.New()
	eng.SetColumnMeta(buildColumnMeta(cols))
	return eng
}
