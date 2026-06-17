package storage

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
)

const (
	defaultBlockCacheSize     = 256 * 1024 * 1024 // 256MB
	defaultBlockCacheMaxEntry = 1024 * 1024       // 1MB
	defaultIndexCacheEntries  = 1000              // 1000 条目
)

// segmentIDGen 是 Segment ID 的集中式生成器，确保 Flusher 和 Compactor 共享同一个 ID 源，
// 消除手动同步 nextID 的需要。
type segmentIDGen struct {
	nextID atomic.Uint64
}

// newSegmentIDGen 创建一个 Segment ID 生成器。
func newSegmentIDGen() *segmentIDGen {
	return &segmentIDGen{}
}

// Next 原子地分配并返回下一个 Segment ID。
func (g *segmentIDGen) Next() uint64 {
	return g.nextID.Add(1)
}

// Current 返回当前已分配的最大 ID（无锁读取）。
func (g *segmentIDGen) Current() uint64 {
	return g.nextID.Load()
}

// InitIfLarger 当 id 大于当前值时更新，用于从磁盘恢复时初始化。
func (g *segmentIDGen) InitIfLarger(id uint64) {
	setNextIDAtomic(&g.nextID, id)
}

// Engine 是存储引擎的核心结构。
type Engine struct {
	mu                     sync.RWMutex
	activeMem              *MemTable
	immutable              []*MemTable
	wal                    *WAL
	flusher                *Flusher
	compactor              *Compactor
	segIDGen               *segmentIDGen
	segments               []*Segment
	segmentMap             map[uint64]*Segment
	segmentLevels          []int
	l0SegmentCount         int           // 缓存 L0 Segment 数量，避免每次线性扫描
	nextVersion            atomic.Uint64 // 原子化版本号，避免写路径锁竞争
	primaryIndex           *index.PrimaryIndex
	bloomIndex             *index.BloomIndex
	sparseIndex            *index.SparseIndex
	columnMeta             []ColumnMeta
	blockCache             *BlockCache
	indexCache             *IndexCache
	scheduler              *Scheduler
	schedulerOnce          sync.Once
	groupCommitter         *GroupCommitter
	syncMode               SyncMode
	blockCacheMaxEntrySize int64 // 单个缓存条目的最大允许大小，超过此值不缓存
}

// EngineConfig 是 Engine 的配置参数。
type EngineConfig struct {
	DataDir                string
	MaxMemTableSize        int64         // MemTable 最大容量（字节），默认 4MB
	BlockCacheSize         int64         // BlockCache 容量（字节），默认 256MB，<=0 表示不缓存
	BlockCacheMaxEntrySize int64         // 单个缓存条目的最大允许大小（字节），默认 1MB，超过此值不缓存，防止冷数据污染
	IndexCacheSize         int           // IndexCache 容量（条目数），默认 1000，<=0 表示不缓存
	SyncMode               SyncMode      // WAL 同步模式，默认 SyncEveryWrite
	SyncInterval           time.Duration // GroupCommit 模式下的同步间隔，默认 1ms
}

// NewEngine 创建一个新的存储引擎实例。
func NewEngine(cfg EngineConfig) (*Engine, error) {
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("engine: create data dir: %w", err)
	}

	idGen := newSegmentIDGen()
	eng := &Engine{
		activeMem:              NewMemTableWithSize(resolveMemTableSize(cfg.MaxMemTableSize)),
		segIDGen:               idGen,
		flusher:                NewFlusher(cfg.DataDir, idGen),
		compactor:              NewCompactor(cfg.DataDir, idGen),
		segmentMap:             make(map[uint64]*Segment),
		primaryIndex:           index.NewPrimaryIndex(),
		bloomIndex:             index.NewBloomIndex(),
		sparseIndex:            index.NewSparseIndex(),
		blockCache:             NewBlockCache(resolveBlockCacheSize(cfg.BlockCacheSize)),
		indexCache:             NewIndexCache(resolveIndexCacheSize(cfg.IndexCacheSize)),
		syncMode:               cfg.SyncMode,
		blockCacheMaxEntrySize: resolveBlockCacheMaxEntry(cfg.BlockCacheMaxEntrySize),
	}

	// Load existing segments from disk
	if err := eng.loadSegments(); err != nil {
		return nil, fmt.Errorf("engine: load segments: %w", err)
	}

	// Open or create WAL and replay records
	wal, err := eng.initWAL(cfg)
	if err != nil {
		return nil, err
	}
	eng.wal = wal

	// 启动 GroupCommitter（如果配置了 GroupCommit 模式）
	if cfg.SyncMode == SyncGroupCommit {
		eng.groupCommitter = NewGroupCommitter(wal, cfg.SyncInterval)
	}

	return eng, nil
}

// resolveMemTableSize 解析 MemTable 大小配置，未设置则使用默认值。
func resolveMemTableSize(maxSize int64) int64 {
	if maxSize <= 0 {
		return memTableDefaultSize
	}
	return maxSize
}

// resolveBlockCacheSize 解析 BlockCache 大小配置，未设置则使用默认值。
func resolveBlockCacheSize(size int64) int64 {
	if size == 0 {
		return defaultBlockCacheSize
	}
	return size
}

// resolveIndexCacheSize 解析 IndexCache 条目数配置，未设置则使用默认值。
func resolveIndexCacheSize(size int) int {
	if size == 0 {
		return defaultIndexCacheEntries
	}
	return size
}

// resolveBlockCacheMaxEntry 解析 BlockCache 单条目最大大小配置，未设置则使用默认值。
func resolveBlockCacheMaxEntry(size int64) int64 {
	if size == 0 {
		return defaultBlockCacheMaxEntry
	}
	return size
}

// initWAL 打开或创建 WAL 并回放记录，返回初始化好的 WAL 实例。
func (e *Engine) initWAL(cfg EngineConfig) (*WAL, error) {
	walPath := filepath.Join(cfg.DataDir, "wal.log")
	wal, records, err := OpenWAL(walPath)
	if err != nil {
		wal, err = CreateWAL(walPath)
		if err != nil {
			return nil, fmt.Errorf("engine: create wal: %w", err)
		}
		return wal, nil
	}
	// Replay WAL records to recover data
	if err := e.replayWALRecords(records); err != nil {
		if closeErr := wal.Close(); closeErr != nil {
			log.Printf("engine: close wal after replay failure: %v", closeErr)
		}
		return nil, fmt.Errorf("engine: replay wal: %w", err)
	}
	return wal, nil
}

// Write 向引擎写入一行数据。
func (e *Engine) Write(key string, values map[string]common.Value) error {
	if key == "" {
		return fmt.Errorf("engine write: empty key is not allowed")
	}

	// Step 1: 原子分配版本号（无锁，减少写路径竞争）
	version := e.nextVersion.Add(1)

	// Step 2: Serialize WAL record (no lock needed, CPU-bound)
	payload, err := serializeWriteRecord(key, version, values)
	if err != nil {
		return fmt.Errorf("engine write: serialize wal: %w", err)
	}

	// Step 3: WAL append + sync (I/O-bound, no engine lock needed)
	// WAL has its own internal serialization for concurrent appends.
	if err := e.wal.AppendWrite(payload); err != nil {
		return fmt.Errorf("engine write: wal append: %w", err)
	}

	var syncCh <-chan struct{}
	e.mu.RLock()
	gc := e.groupCommitter
	e.mu.RUnlock()
	if gc != nil {
		syncCh = gc.Submit()
	} else if err := e.wal.Sync(); err != nil {
		return fmt.Errorf("engine write: wal sync: %w", err)
	}

	// Step 4: Put to memtable under lock (brief hold)
	e.mu.Lock()
	if e.activeMem.ShouldFlush() {
		if err := e.rotateMemTable(); err != nil {
			e.mu.Unlock()
			return fmt.Errorf("engine write: rotate memtable: %w", err)
		}
	}

	_, _, err = e.activeMem.Put(key, Row{Version: version, Columns: values})
	e.mu.Unlock()

	if err != nil {
		return fmt.Errorf("engine write: %w", err)
	}

	// Step 5: Wait for WAL sync completion (outside engine lock)
	if syncCh != nil {
		<-syncCh
	}

	return nil
}

// freezeActiveMemTable 将活跃 MemTable 冻结并移入 immutable 队列。
// 调用者必须持有 e.mu 锁。
func (e *Engine) freezeActiveMemTable() {
	if e.activeMem.Len() > 0 {
		e.activeMem.Freeze()
		e.immutable = append(e.immutable, e.activeMem)
		e.activeMem = NewMemTableWithSize(e.activeMem.maxSize)
	}
}

// drainImmutable 冻结活跃 MemTable 并排空 immutable 队列，返回待刷写的 memtable 列表。
// 调用者必须持有 e.mu 锁，返回后 immutable 队列已被清空。
func (e *Engine) drainImmutable() []*MemTable {
	e.freezeActiveMemTable()
	immutable := e.immutable
	e.immutable = nil
	return immutable
}

// Flush 将内存表中的数据刷写到磁盘。
func (e *Engine) Flush(cols []ColumnMeta) error {
	e.mu.Lock()

	var flushVersion uint64
	if v := e.nextVersion.Load(); v > 0 {
		flushVersion = v - 1
	}

	immutable := e.drainImmutable()

	if len(e.columnMeta) == 0 && len(cols) > 0 {
		e.columnMeta = make([]ColumnMeta, len(cols))
		copy(e.columnMeta, cols)
	}

	if len(immutable) == 0 {
		e.mu.Unlock()
		return nil
	}

	e.mu.Unlock()

	if err := e.flushImmutable(immutable, cols); err != nil {
		return err
	}

	return e.writeCheckpoint(flushVersion)
}

// flushImmutable 逐个刷写 immutable memtable 到磁盘。
// 注意：调用时 e.mu 未持有。失败回写时将未刷写的 memtable 前置到 e.immutable，
// 确保它们排在并发 Write 新增的 memtable 之前，维持正确的刷写顺序。
func (e *Engine) flushImmutable(immutable []*MemTable, cols []ColumnMeta) error {
	var flushedIdx int
	for i, mem := range immutable {
		seg, err := e.flusher.Flush(mem, cols)
		if err != nil {
			e.mu.Lock()
			// 前置未刷写的 memtable，保证它们在并发 Write 新增的 memtable 之前
			remaining := immutable[flushedIdx:]
			e.immutable = append(remaining, e.immutable...)
			e.mu.Unlock()
			return fmt.Errorf("engine flush: %w", err)
		}

		e.mu.Lock()
		if err := e.addSegment(seg, 0); err != nil {
			e.mu.Unlock()
			// 清理已刷盘但注册失败的段文件，避免磁盘资源泄漏
			cleanupSegmentFile(seg)
			remaining := immutable[flushedIdx+1:]
			if len(remaining) > 0 {
				e.mu.Lock()
				e.immutable = append(remaining, e.immutable...)
				e.mu.Unlock()
			}
			return fmt.Errorf("engine flush: %w", err)
		}
		e.mu.Unlock()
		flushedIdx = i + 1
	}
	return nil
}

// writeCheckpoint 在成功刷写后写入 WAL checkpoint 记录。
func (e *Engine) writeCheckpoint(flushVersion uint64) error {
	// 合并两次加锁为一次，减少锁竞争
	e.mu.RLock()
	colMeta := e.columnMeta
	gc := e.groupCommitter
	e.mu.RUnlock()

	if gc != nil {
		gc.SyncNow()
	}

	checkpointPayload, err := serializeCheckpointRecord(flushVersion, colMeta)
	if err != nil {
		return fmt.Errorf("engine flush: serialize checkpoint: %w", err)
	}
	if err := e.wal.AppendCheckpoint(checkpointPayload); err != nil {
		return fmt.Errorf("engine flush: write checkpoint: %w", err)
	}
	if err := e.wal.Sync(); err != nil {
		return fmt.Errorf("engine flush: sync checkpoint: %w", err)
	}

	return nil
}

// Close 关闭引擎，停止后台调度器，刷写剩余内存数据，同步并关闭 WAL。
func (e *Engine) Close() error {
	// 先停止后台调度器和 GroupCommitter，避免在关闭过程中触发操作
	// 在锁内读取并置空，确保与 Write/StartScheduler 等方法的同步
	e.mu.Lock()
	sched := e.scheduler
	e.scheduler = nil
	gc := e.groupCommitter
	e.groupCommitter = nil
	e.mu.Unlock()

	if sched != nil {
		sched.Stop()
	}
	if gc != nil {
		gc.Close()
	}

	e.mu.Lock()

	// 将 activeMem 中未刷写的数据移入 immutable，确保 Close 后数据不丢失
	immutable := e.drainImmutable()
	cols := e.columnMeta
	e.mu.Unlock()

	// 尝试刷写所有 immutable memtable，确保数据持久化
	// 刷写失败不阻止关闭流程，因为数据仍在 WAL 中，重启后可恢复
	for _, mem := range immutable {
		seg, err := e.flusher.Flush(mem, cols)
		if err != nil {
			log.Printf("engine close: flush memtable failed (data recoverable from WAL): %v", err)
			continue
		}
		e.mu.Lock()
		if err := e.addSegment(seg, 0); err != nil {
			log.Printf("engine close: register segment %d: %v", seg.ID, err)
		}
		e.mu.Unlock()
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.wal.Close(); err != nil {
		return fmt.Errorf("engine close: close wal: %w", err)
	}

	return nil
}

func (e *Engine) collectSegmentsByLevel(level int) ([]*Segment, []int) {
	var segments []*Segment
	var indices []int
	for i, lvl := range e.segmentLevels {
		if lvl == level {
			segments = append(segments, e.segments[i])
			indices = append(indices, i)
		}
	}
	return segments, indices
}

// --- Engine Accessors ---

// Segments 返回所有 Segment 的副本。
func (e *Engine) Segments() []*Segment {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]*Segment, len(e.segments))
	copy(result, e.segments)
	return result
}

// SegmentCount 返回 Segment 的数量。
func (e *Engine) SegmentCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.segments)
}

// L0SegmentCount 返回 L0 层 Segment 的数量。
func (e *Engine) L0SegmentCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.l0SegmentCount
}

// MemTableSize 返回当前活跃 MemTable 的大小。
func (e *Engine) MemTableSize() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.activeMem.Size()
}

// PrimaryIndex 返回主键索引实例。
func (e *Engine) PrimaryIndex() *index.PrimaryIndex {
	return e.primaryIndex
}

// BloomIndex 返回布隆过滤器索引实例。
func (e *Engine) BloomIndex() *index.BloomIndex {
	return e.bloomIndex
}

// SparseIndex 返回稀疏索引实例。
func (e *Engine) SparseIndex() *index.SparseIndex {
	return e.sparseIndex
}

// ColumnMeta 返回列元数据的副本。
func (e *Engine) ColumnMeta() []ColumnMeta {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]ColumnMeta, len(e.columnMeta))
	copy(result, e.columnMeta)
	return result
}

// SetColumnMeta 设置列元数据，用于在 CREATE TABLE 时告知引擎列定义，
// 使后台调度器的自动刷盘能正确编码列数据。
func (e *Engine) SetColumnMeta(cols []ColumnMeta) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.columnMeta = make([]ColumnMeta, len(cols))
	copy(e.columnMeta, cols)
}

// SchedulerStats 返回后台调度器的运行统计信息。
// 如果调度器未启动，ok 为 false。
func (e *Engine) SchedulerStats() (stats SchedulerStats, ok bool) {
	e.mu.RLock()
	sched := e.scheduler
	e.mu.RUnlock()

	if sched == nil {
		return SchedulerStats{}, false
	}
	return sched.Stats(), true
}

// StartScheduler 启动后台任务调度器，定时执行刷盘、Compaction 和 WAL 清理。
// 如果调度器已在运行，则不做任何操作。使用 sync.Once 保证只启动一次。
func (e *Engine) StartScheduler(cfg SchedulerConfig) {
	e.schedulerOnce.Do(func() {
		sched := NewScheduler(e, cfg)
		sched.Start()

		e.mu.Lock()
		e.scheduler = sched
		e.mu.Unlock()
	})
}
