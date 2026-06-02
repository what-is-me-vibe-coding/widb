package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
)

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
	primaryIndex  *index.PrimaryIndex
	bloomIndex    *index.BloomIndex
	sparseIndex   *index.SparseIndex
	columnMeta    []ColumnMeta
}

type EngineConfig struct {
	DataDir         string
	MaxMemTableSize int64
}

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
		activeMem:    NewMemTableWithSize(maxSize),
		wal:          wal,
		flusher:      NewFlusher(cfg.DataDir),
		compactor:    NewCompactor(cfg.DataDir),
		nextVersion:  1,
		primaryIndex: index.NewPrimaryIndex(),
		bloomIndex:   index.NewBloomIndex(),
		sparseIndex:  index.NewSparseIndex(),
	}

	return eng, nil
}

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

	return e.getFromSegments(key)
}

func (e *Engine) getFromSegments(key string) (Row, bool) {
	segIDs := e.primaryIndex.Lookup(key)
	if len(segIDs) == 0 {
		return Row{}, false
	}

	sortedIDs := make([]uint64, len(segIDs))
	copy(sortedIDs, segIDs)
	sort.Slice(sortedIDs, func(i, j int) bool {
		return sortedIDs[i] > sortedIDs[j]
	})

	for _, segID := range sortedIDs {
		if !e.bloomIndex.MayContain(segID, []byte(key)) {
			continue
		}

		seg := e.findSegmentByID(segID)
		if seg == nil {
			continue
		}

		rowIdx, found := seg.FindRowByKey(key)
		if !found {
			continue
		}

		row := Row{Version: seg.ID}
		if row.Columns == nil {
			row.Columns = make(map[string]common.Value)
		}
		for colIdx, col := range e.columnMeta {
			val, err := seg.GetColumnValue(uint32(colIdx), rowIdx)
			if err != nil {
				continue
			}
			row.Columns[col.Name] = val
		}
		return row, true
	}

	return Row{}, false
}

func (e *Engine) findSegmentByID(segID uint64) *Segment {
	for _, seg := range e.segments {
		if seg.ID == segID {
			return seg
		}
	}
	return nil
}

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

func (e *Engine) Flush(cols []ColumnMeta) error {
	e.mu.Lock()

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

	e.mu.Unlock()

	for _, mem := range immutable {
		seg, err := e.flusher.Flush(mem, cols)
		if err != nil {
			return fmt.Errorf("engine flush: %w", err)
		}

		e.mu.Lock()
		e.segments = append(e.segments, seg)
		e.segmentLevels = append(e.segmentLevels, 0)
		e.registerSegmentIndexes(seg, 0)
		e.mu.Unlock()
	}

	return nil
}

func (e *Engine) registerSegmentIndexes(seg *Segment, level int) {
	segMeta := index.SegmentMeta{
		ID:     seg.ID,
		MinKey: seg.MinKey,
		MaxKey: seg.MaxKey,
		Level:  level,
	}
	_ = e.primaryIndex.RegisterSegment(segMeta)

	if len(seg.Footer.BloomFilter) > 0 {
		_ = e.bloomIndex.RegisterFromBytes(seg.ID, seg.Footer.BloomFilter)
	}

	e.sparseIndex.LoadFromSegment(seg, seg.MinKey, seg.MaxKey, level)
}

func (e *Engine) unregisterSegmentIndexes(segID uint64) {
	_ = e.primaryIndex.UnregisterSegment(segID)
	e.bloomIndex.Unregister(segID)
	e.sparseIndex.UnregisterSegment(segID)
}

func (e *Engine) Segments() []*Segment {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]*Segment, len(e.segments))
	copy(result, e.segments)
	return result
}

func (e *Engine) SegmentCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.segments)
}

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

	for _, seg := range allSegments {
		e.unregisterSegmentIndexes(seg.ID)
	}

	sort.Slice(allIndices, func(i, j int) bool {
		return allIndices[i] > allIndices[j]
	})
	for _, idx := range allIndices {
		e.segments = append(e.segments[:idx], e.segments[idx+1:]...)
		e.segmentLevels = append(e.segmentLevels[:idx], e.segmentLevels[idx+1:]...)
	}

	e.segments = append(e.segments, newSeg)
	e.segmentLevels = append(e.segmentLevels, 1)
	e.registerSegmentIndexes(newSeg, 1)

	if err := e.compactor.CleanupSegments(allSegments); err != nil {
		return fmt.Errorf("engine compact: cleanup: %w", err)
	}

	return nil
}

func (e *Engine) ShouldCompact() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.l0Count() >= defaultL0CompactionThreshold
}

func (e *Engine) MemTableSize() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.activeMem.Size()
}

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

func (e *Engine) PrimaryIndex() *index.PrimaryIndex {
	return e.primaryIndex
}

func (e *Engine) BloomIndex() *index.BloomIndex {
	return e.bloomIndex
}

func (e *Engine) SparseIndex() *index.SparseIndex {
	return e.sparseIndex
}

func (e *Engine) ColumnMeta() []ColumnMeta {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]ColumnMeta, len(e.columnMeta))
	copy(result, e.columnMeta)
	return result
}

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
