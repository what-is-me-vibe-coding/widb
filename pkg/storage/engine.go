package storage

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
)

// Engine 是存储引擎的核心结构。
type Engine struct {
	mu                     sync.RWMutex
	activeMem              *MemTable
	immutable              []*MemTable
	wal                    *WAL
	flusher                *Flusher
	compactor              *Compactor
	segments               []*Segment
	segmentMap             map[uint64]*Segment
	segmentLevels          []int
	nextVersion            uint64
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

	maxSize := cfg.MaxMemTableSize
	if maxSize <= 0 {
		maxSize = memTableDefaultSize
	}

	blockCacheSize := cfg.BlockCacheSize
	if blockCacheSize == 0 {
		blockCacheSize = 256 * 1024 * 1024 // 默认 256MB
	}

	blockCacheMaxEntrySize := cfg.BlockCacheMaxEntrySize
	if blockCacheMaxEntrySize == 0 {
		blockCacheMaxEntrySize = 1024 * 1024 // 默认 1MB
	}

	indexCacheSize := cfg.IndexCacheSize
	if indexCacheSize == 0 {
		indexCacheSize = 1000 // 默认 1000 条目
	}

	eng := &Engine{
		activeMem:              NewMemTableWithSize(maxSize),
		flusher:                NewFlusher(cfg.DataDir),
		compactor:              NewCompactor(cfg.DataDir),
		segmentMap:             make(map[uint64]*Segment),
		nextVersion:            1,
		primaryIndex:           index.NewPrimaryIndex(),
		bloomIndex:             index.NewBloomIndex(),
		sparseIndex:            index.NewSparseIndex(),
		blockCache:             NewBlockCache(blockCacheSize),
		indexCache:             NewIndexCache(indexCacheSize),
		syncMode:               cfg.SyncMode,
		blockCacheMaxEntrySize: blockCacheMaxEntrySize,
	}

	// Load existing segments from disk
	if err := eng.loadSegments(); err != nil {
		return nil, fmt.Errorf("engine: load segments: %w", err)
	}

	// Open or create WAL
	walPath := filepath.Join(cfg.DataDir, "wal.log")
	wal, records, err := OpenWAL(walPath)
	if err != nil {
		wal, err = CreateWAL(walPath)
		if err != nil {
			return nil, fmt.Errorf("engine: create wal: %w", err)
		}
	} else {
		// Replay WAL records to recover data
		if err := eng.replayWALRecords(records); err != nil {
			if closeErr := wal.Close(); closeErr != nil {
				log.Printf("engine: close wal after replay failure: %v", closeErr)
			}
			return nil, fmt.Errorf("engine: replay wal: %w", err)
		}
	}
	eng.wal = wal

	// 启动 GroupCommitter（如果配置了 GroupCommit 模式）
	if cfg.SyncMode == SyncGroupCommit {
		eng.groupCommitter = NewGroupCommitter(wal, cfg.SyncInterval)
	}

	return eng, nil
}

// Write 向引擎写入一行数据。
func (e *Engine) Write(key string, values map[string]common.Value) error {
	// Step 1: Allocate version under lock (brief hold)
	e.mu.Lock()
	version := e.nextVersion
	e.nextVersion++
	e.mu.Unlock()

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

// Flush 将内存表中的数据刷写到磁盘。
func (e *Engine) Flush(cols []ColumnMeta) error {
	e.mu.Lock()

	var flushVersion uint64
	if e.nextVersion > 0 {
		flushVersion = e.nextVersion - 1
	}

	if e.activeMem.Len() > 0 {
		e.activeMem.Freeze()
		e.immutable = append(e.immutable, e.activeMem)
		e.activeMem = NewMemTableWithSize(e.activeMem.maxSize)
	}

	immutable := e.immutable
	e.immutable = nil

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
func (e *Engine) flushImmutable(immutable []*MemTable, cols []ColumnMeta) error {
	var flushedIdx int
	for i, mem := range immutable {
		seg, err := e.flusher.Flush(mem, cols)
		if err != nil {
			e.mu.Lock()
			e.immutable = append(e.immutable, immutable[flushedIdx:]...)
			e.mu.Unlock()
			return fmt.Errorf("engine flush: %w", err)
		}

		e.mu.Lock()
		e.segments = append(e.segments, seg)
		e.segmentMap[seg.ID] = seg
		e.segmentLevels = append(e.segmentLevels, 0)
		if err := e.registerSegmentIndexes(seg, 0); err != nil {
			e.mu.Unlock()
			remaining := immutable[flushedIdx+1:]
			if len(remaining) > 0 {
				e.mu.Lock()
				e.immutable = append(e.immutable, remaining...)
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
	if e.activeMem.Len() > 0 {
		e.activeMem.Freeze()
		e.immutable = append(e.immutable, e.activeMem)
		e.activeMem = NewMemTableWithSize(e.activeMem.maxSize)
	}

	immutable := e.immutable
	e.immutable = nil
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
		e.segments = append(e.segments, seg)
		e.segmentMap[seg.ID] = seg
		e.segmentLevels = append(e.segmentLevels, 0)
		if err := e.registerSegmentIndexes(seg, 0); err != nil {
			log.Printf("engine close: register segment %d indexes: %v", seg.ID, err)
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
	count := 0
	for _, lvl := range e.segmentLevels {
		if lvl == 0 {
			count++
		}
	}
	return count
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
