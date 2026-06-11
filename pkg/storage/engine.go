// Package storage 实现存储引擎核心，包括 WAL、MemTable、Segment、Compaction。
// 可依赖 pkg/common 与 pkg/catalog。
package storage

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
)

// Engine 是存储引擎的核心结构。
type Engine struct {
	mu             sync.RWMutex
	activeMem      *MemTable
	immutable      []*MemTable
	wal            *WAL
	flusher        *Flusher
	compactor      *Compactor
	segments       []*Segment
	segmentMap     map[uint64]*Segment
	segmentLevels  []int
	nextVersion    uint64
	primaryIndex   *index.PrimaryIndex
	bloomIndex     *index.BloomIndex
	sparseIndex    *index.SparseIndex
	columnMeta     []ColumnMeta
	blockCache     *BlockCache
	indexCache     *IndexCache
	scheduler      *Scheduler
	groupCommitter *GroupCommitter
	syncMode       SyncMode
}

// EngineConfig 是 Engine 的配置参数。
type EngineConfig struct {
	DataDir         string
	MaxMemTableSize int64
	BlockCacheSize  int64         // BlockCache 容量（字节），默认 256MB，<=0 表示不缓存
	IndexCacheSize  int           // IndexCache 容量（条目数），默认 1000，<=0 表示不缓存
	SyncMode        SyncMode      // WAL 同步模式，默认 SyncEveryWrite
	SyncInterval    time.Duration // GroupCommit 模式下的同步间隔，默认 1ms
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

	indexCacheSize := cfg.IndexCacheSize
	if indexCacheSize == 0 {
		indexCacheSize = 1000 // 默认 1000 条目
	}

	eng := &Engine{
		activeMem:    NewMemTableWithSize(maxSize),
		flusher:      NewFlusher(cfg.DataDir),
		compactor:    NewCompactor(cfg.DataDir),
		segmentMap:   make(map[uint64]*Segment),
		nextVersion:  1,
		primaryIndex: index.NewPrimaryIndex(),
		bloomIndex:   index.NewBloomIndex(),
		sparseIndex:  index.NewSparseIndex(),
		blockCache:   NewBlockCache(blockCacheSize),
		indexCache:   NewIndexCache(indexCacheSize),
		syncMode:     cfg.SyncMode,
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
	e.mu.Lock()
	colMeta := e.columnMeta
	e.mu.Unlock()

	e.mu.RLock()
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

// parseSegmentEntry parses a directory entry into a Segment, returning the segment,
// its ID, whether the entry was a segment file, and an error if it was a segment file
// that failed to load.
func (e *Engine) parseSegmentEntry(entry os.DirEntry) (*Segment, uint64, bool, error) {
	if entry.IsDir() {
		return nil, 0, false, nil
	}
	name := entry.Name()
	if !strings.HasPrefix(name, "segment_") || !strings.HasSuffix(name, ".widb") {
		return nil, 0, false, nil
	}

	filePath := filepath.Join(e.flusher.dataDir, name)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, 0, true, fmt.Errorf("failed to read segment file %s: %w", name, err)
	}

	seg, err := DeserializeSegment(data)
	if err != nil {
		return nil, 0, true, fmt.Errorf("failed to deserialize segment file %s: %w", name, err)
	}
	seg.FilePath = filePath

	// Extract segment ID from filename: segment_<id>.widb
	idStr := name[len("segment_") : len(name)-len(".widb")]
	var segID uint64
	if _, err := fmt.Sscanf(idStr, "%d", &segID); err == nil {
		seg.ID = segID
	}

	// Derive MinKey/MaxKey from sorted keys
	if len(seg.Keys) > 0 {
		seg.MinKey = seg.Keys[0]
		seg.MaxKey = seg.Keys[len(seg.Keys)-1]
	}

	return seg, segID, true, nil
}

// loadSegments 从磁盘加载已有的 Segment 文件。
func (e *Engine) loadSegments() error {
	entries, err := os.ReadDir(e.flusher.dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("engine: read data dir: %w", err)
	}

	var maxSegID uint64
	var segFileCount int
	var failedCount int
	for _, entry := range entries {
		seg, segID, isSegFile, parseErr := e.parseSegmentEntry(entry)
		if !isSegFile {
			continue
		}
		segFileCount++
		if parseErr != nil {
			log.Printf("engine: %v", parseErr)
			failedCount++
			continue
		}
		e.segments = append(e.segments, seg)
		e.segmentMap[seg.ID] = seg
		e.segmentLevels = append(e.segmentLevels, 0)
		if segID > maxSegID {
			maxSegID = segID
		}
	}

	if failedCount > 0 {
		log.Printf("engine: warning: %d of %d segment files failed to load during recovery", failedCount, segFileCount)
		if failedCount == segFileCount {
			return fmt.Errorf("engine: all %d segment files failed to load during recovery", segFileCount)
		}
	}

	// Register indexes for loaded segments
	for i, seg := range e.segments {
		if err := e.registerSegmentIndexes(seg, e.segmentLevels[i]); err != nil {
			return fmt.Errorf("engine: register segment %d indexes: %w", seg.ID, err)
		}
	}

	// Update flusher and compactor nextID to avoid ID collisions
	if maxSegID > 0 {
		e.flusher.nextID = maxSegID
		e.compactor.nextID = maxSegID
	}

	return nil
}
