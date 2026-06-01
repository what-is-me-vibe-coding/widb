package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// Engine 是存储引擎的顶层协调器，管理 WAL、MemTable 和 Segment。
type Engine struct {
	mu            sync.RWMutex
	activeMem     *MemTable
	immutable     []*MemTable
	wal           *WAL
	flusher       *Flusher
	compactor     *Compactor
	segments      []*Segment
	segmentLevels []int
	nextVersion   uint64
}

// EngineConfig 配置 Engine 的参数。
type EngineConfig struct {
	DataDir         string
	MaxMemTableSize int64
}

// NewEngine 创建一个新的存储引擎实例。
func NewEngine(cfg EngineConfig) (*Engine, error) {
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("engine: create data dir: %w", err)
	}

	walPath := filepath.Join(cfg.DataDir, "wal.log")
	wal, _, err := OpenWAL(walPath)
	if err != nil {
		wal, err = CreateWAL(walPath)
		if err != nil {
			return nil, fmt.Errorf("engine: create wal: %w", err)
		}
	}

	maxSize := cfg.MaxMemTableSize
	if maxSize <= 0 {
		maxSize = memTableDefaultSize
	}

	eng := &Engine{
		activeMem:   NewMemTableWithSize(maxSize),
		wal:         wal,
		flusher:     NewFlusher(cfg.DataDir),
		compactor:   NewCompactor(cfg.DataDir),
		nextVersion: 1,
	}

	return eng, nil
}

// Write 写入一行数据，先写 WAL 再写 MemTable。
func (e *Engine) Write(key string, values map[string]common.Value) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	row := Row{
		Version: e.nextVersion,
		Columns: values,
	}
	e.nextVersion++

	if e.activeMem.ShouldFlush() {
		if err := e.rotateMemTable(); err != nil {
			return fmt.Errorf("engine write: rotate memtable: %w", err)
		}
	}

	_, _, err := e.activeMem.Put(key, row)
	if err != nil {
		return fmt.Errorf("engine write: %w", err)
	}

	return nil
}

// Get 从 MemTable 和 Immutable 中查询键对应的值。
func (e *Engine) Get(key string) (Row, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if row, ok := e.activeMem.Get(key); ok {
		return row, true
	}

	for i := len(e.immutable) - 1; i >= 0; i-- {
		if row, ok := e.immutable[i].Get(key); ok {
			return row, true
		}
	}

	return Row{}, false
}

// Scan 在 [start, end] 范围内扫描。
func (e *Engine) Scan(start, end string) []struct {
	Key   string
	Value Row
} {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var results []struct {
		Key   string
		Value Row
	}

	results = append(results, e.activeMem.Scan(start, end)...)

	for i := len(e.immutable) - 1; i >= 0; i-- {
		results = append(results, e.immutable[i].Scan(start, end)...)
	}

	return results
}

// Flush 将所有 ImmutableMemTable 刷盘为 Segment。
func (e *Engine) Flush(cols []ColumnMeta) error {
	e.mu.Lock()

	if e.activeMem.Len() > 0 {
		e.activeMem.Freeze()
		e.immutable = append(e.immutable, e.activeMem)
		e.activeMem = NewMemTableWithSize(e.activeMem.maxSize)
	}

	immutable := e.immutable
	e.immutable = nil
	e.mu.Unlock()

	for _, mem := range immutable {
		seg, err := e.flusher.Flush(mem, cols)
		if err != nil {
			return fmt.Errorf("engine flush: %w", err)
		}

		e.mu.Lock()
		e.segments = append(e.segments, seg)
		e.segmentLevels = append(e.segmentLevels, 0)
		e.mu.Unlock()
	}

	return nil
}

// Segments 返回所有已刷盘的 Segment 列表。
func (e *Engine) Segments() []*Segment {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]*Segment, len(e.segments))
	copy(result, e.segments)
	return result
}

// SegmentCount 返回 Segment 总数。
func (e *Engine) SegmentCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.segments)
}

// L0SegmentCount 返回 L0 层 Segment 数量。
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

// Compact 执行一次 Compaction，将 L0 的 Segment 合并到 L1。
// 如果 L1 不存在则直接创建，如果 L1 已存在则合并 L0 和 L1 的 Segment。
func (e *Engine) Compact(cols []ColumnMeta) error {
	e.mu.Lock()

	l0Segments, l0Indices := e.collectL0Segments()
	if len(l0Segments) == 0 {
		e.mu.Unlock()
		return nil
	}

	l1Segments, l1Indices := e.collectL1Segments()

	allSegments := make([]*Segment, 0, len(l0Segments)+len(l1Segments))
	allSegments = append(allSegments, l0Segments...)
	allSegments = append(allSegments, l1Segments...)

	allIndices := make([]int, 0, len(l0Indices)+len(l1Indices))
	allIndices = append(allIndices, l0Indices...)
	allIndices = append(allIndices, l1Indices...)

	e.mu.Unlock()

	newSeg, err := e.compactor.Compact(allSegments, cols)
	if err != nil {
		return fmt.Errorf("engine compact: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	sort.Slice(allIndices, func(i, j int) bool {
		return allIndices[i] > allIndices[j]
	})
	for _, idx := range allIndices {
		e.segments = append(e.segments[:idx], e.segments[idx+1:]...)
		e.segmentLevels = append(e.segmentLevels[:idx], e.segmentLevels[idx+1:]...)
	}

	e.segments = append(e.segments, newSeg)
	e.segmentLevels = append(e.segmentLevels, 1)

	if err := e.compactor.CleanupSegments(allSegments); err != nil {
		return fmt.Errorf("engine compact: cleanup: %w", err)
	}

	return nil
}

// ShouldCompact 返回是否应该触发 Compaction。
func (e *Engine) ShouldCompact() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.l0Count() >= defaultL0CompactionThreshold
}

// MemTableSize 返回活跃 MemTable 的估算大小。
func (e *Engine) MemTableSize() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.activeMem.Size()
}

// Close 关闭引擎，清理资源。
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.wal.Sync(); err != nil {
		return fmt.Errorf("engine close: sync wal: %w", err)
	}
	if err := e.wal.Close(); err != nil {
		return fmt.Errorf("engine close: close wal: %w", err)
	}

	return nil
}

// rotateMemTable 冻结当前 MemTable 并创建新的。
func (e *Engine) rotateMemTable() error {
	if e.activeMem.Len() == 0 {
		return nil
	}

	e.activeMem.Freeze()
	e.immutable = append(e.immutable, e.activeMem)
	e.activeMem = NewMemTableWithSize(e.activeMem.maxSize)
	return nil
}

func (e *Engine) l0Count() int {
	count := 0
	for _, lvl := range e.segmentLevels {
		if lvl == 0 {
			count++
		}
	}
	return count
}

func (e *Engine) collectL0Segments() ([]*Segment, []int) {
	var segments []*Segment
	var indices []int
	for i, lvl := range e.segmentLevels {
		if lvl == 0 {
			segments = append(segments, e.segments[i])
			indices = append(indices, i)
		}
	}
	return segments, indices
}

func (e *Engine) collectL1Segments() ([]*Segment, []int) {
	var segments []*Segment
	var indices []int
	for i, lvl := range e.segmentLevels {
		if lvl == 1 {
			segments = append(segments, e.segments[i])
			indices = append(indices, i)
		}
	}
	return segments, indices
}
