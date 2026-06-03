package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
)

// Engine 是存储引擎的核心结构。
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

// EngineConfig 是 Engine 的配置参数。
type EngineConfig struct {
	DataDir         string
	MaxMemTableSize int64
}

// walWriteRecord 是 Write 操作的 JSON 序列化格式。
type walWriteRecord struct {
	Key     string                    `json:"key"`
	Version uint64                    `json:"version"`
	Columns map[string]walValueRecord `json:"columns"`
}

// walValueRecord 是 common.Value 的 JSON 序列化格式。
type walValueRecord struct {
	Typ     int     `json:"typ"`
	Valid   bool    `json:"valid"`
	Int64   int64   `json:"int64"`
	Float64 float64 `json:"float64"`
	Str     string  `json:"str"`
	Time    string  `json:"time"`
}

// walCheckpointRecord 是 Checkpoint 记录的 JSON 序列化格式。
type walCheckpointRecord struct {
	LastFlushedVersion uint64       `json:"last_flushed_version"`
	ColumnMeta         []walColMeta `json:"column_meta,omitempty"`
}

// walColMeta 是 ColumnMeta 的 JSON 序列化格式。
type walColMeta struct {
	ID   uint32 `json:"id"`
	Name string `json:"name"`
	Typ  int    `json:"typ"`
}

func serializeWriteRecord(key string, version uint64, columns map[string]common.Value) ([]byte, error) {
	rec := walWriteRecord{
		Key:     key,
		Version: version,
		Columns: make(map[string]walValueRecord, len(columns)),
	}
	for colName, v := range columns {
		rec.Columns[colName] = walValueRecord{
			Typ:     int(v.Typ),
			Valid:   v.Valid,
			Int64:   v.Int64,
			Float64: v.Float64,
			Str:     v.Str,
			Time:    v.Time.Format(time.RFC3339Nano),
		}
	}
	return json.Marshal(rec)
}

func deserializeWriteRecord(data []byte) (string, uint64, map[string]common.Value, error) {
	var rec walWriteRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return "", 0, nil, fmt.Errorf("engine: deserialize write record: %w", err)
	}
	columns := make(map[string]common.Value, len(rec.Columns))
	for colName, v := range rec.Columns {
		val := common.Value{
			Typ:     common.DataType(v.Typ),
			Valid:   v.Valid,
			Int64:   v.Int64,
			Float64: v.Float64,
			Str:     v.Str,
		}
		if v.Time != "" {
			t, err := time.Parse(time.RFC3339Nano, v.Time)
			if err != nil {
				return "", 0, nil, fmt.Errorf("engine: parse time %q: %w", v.Time, err)
			}
			val.Time = t
		}
		columns[colName] = val
	}
	return rec.Key, rec.Version, columns, nil
}

func serializeCheckpointRecord(lastFlushedVersion uint64, colMeta []ColumnMeta) ([]byte, error) {
	rec := walCheckpointRecord{
		LastFlushedVersion: lastFlushedVersion,
	}
	for _, cm := range colMeta {
		rec.ColumnMeta = append(rec.ColumnMeta, walColMeta{
			ID:   cm.ID,
			Name: cm.Name,
			Typ:  int(cm.Type),
		})
	}
	return json.Marshal(rec)
}

func deserializeCheckpointRecord(data []byte) (uint64, []ColumnMeta, error) {
	var rec walCheckpointRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return 0, nil, fmt.Errorf("engine: deserialize checkpoint record: %w", err)
	}
	var colMeta []ColumnMeta
	for _, cm := range rec.ColumnMeta {
		colMeta = append(colMeta, ColumnMeta{
			ID:   cm.ID,
			Name: cm.Name,
			Type: common.DataType(cm.Typ),
		})
	}
	return rec.LastFlushedVersion, colMeta, nil
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

	eng := &Engine{
		activeMem:    NewMemTableWithSize(maxSize),
		flusher:      NewFlusher(cfg.DataDir),
		compactor:    NewCompactor(cfg.DataDir),
		nextVersion:  1,
		primaryIndex: index.NewPrimaryIndex(),
		bloomIndex:   index.NewBloomIndex(),
		sparseIndex:  index.NewSparseIndex(),
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
			_ = wal.Close()
			return nil, fmt.Errorf("engine: replay wal: %w", err)
		}
	}
	eng.wal = wal

	return eng, nil
}

// replayWALRecords 将 WAL 回放记录应用到 MemTable。
func (e *Engine) replayWALRecords(records []RawRecord) error {
	var lastFlushedVersion uint64
	var lastColumnMeta []ColumnMeta

	// First pass: find the last checkpoint to determine lastFlushedVersion
	for _, rec := range records {
		if rec.Type == walTypeCheckpoint {
			version, colMeta, err := deserializeCheckpointRecord(rec.Payload)
			if err != nil {
				continue
			}
			if version > lastFlushedVersion {
				lastFlushedVersion = version
				lastColumnMeta = colMeta
			}
		}
	}

	// Restore column meta from checkpoint if not already set
	if len(lastColumnMeta) > 0 && len(e.columnMeta) == 0 {
		e.columnMeta = make([]ColumnMeta, len(lastColumnMeta))
		copy(e.columnMeta, lastColumnMeta)
	}

	// Second pass: apply write records with version > lastFlushedVersion
	var maxVersion uint64
	for _, rec := range records {
		if rec.Type == walTypeWrite {
			key, version, columns, err := deserializeWriteRecord(rec.Payload)
			if err != nil {
				continue
			}
			if version <= lastFlushedVersion {
				continue
			}
			row := Row{
				Version: version,
				Columns: columns,
			}
			_, _, _ = e.activeMem.Put(key, row)
			if version > maxVersion {
				maxVersion = version
			}
		}
	}

	// Update nextVersion to be greater than any version seen
	if maxVersion >= e.nextVersion {
		e.nextVersion = maxVersion + 1
	}

	return nil
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
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "segment_") || !strings.HasSuffix(name, ".widb") {
			continue
		}

		filePath := filepath.Join(e.flusher.dataDir, name)
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		seg, err := DeserializeSegment(data)
		if err != nil {
			continue
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

		e.segments = append(e.segments, seg)
		e.segmentLevels = append(e.segmentLevels, 0)

		if seg.ID > maxSegID {
			maxSegID = seg.ID
		}
	}

	// Register indexes for loaded segments
	for i, seg := range e.segments {
		e.registerSegmentIndexes(seg, e.segmentLevels[i])
	}

	// Update flusher and compactor nextID to avoid ID collisions
	if maxSegID > 0 {
		e.flusher.nextID = maxSegID
		e.compactor.nextID = maxSegID
	}

	return nil
}

// Write 向引擎写入一行数据。
func (e *Engine) Write(key string, values map[string]common.Value) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	version := e.nextVersion

	// Write to WAL first (write-ahead logging)
	payload, err := serializeWriteRecord(key, version, values)
	if err != nil {
		return fmt.Errorf("engine write: serialize wal: %w", err)
	}
	if err := e.wal.AppendWrite(payload); err != nil {
		return fmt.Errorf("engine write: wal append: %w", err)
	}
	if err := e.wal.Sync(); err != nil {
		return fmt.Errorf("engine write: wal sync: %w", err)
	}

	e.nextVersion++

	row := Row{
		Version: version,
		Columns: values,
	}

	if e.activeMem.ShouldFlush() {
		if err := e.rotateMemTable(); err != nil {
			return fmt.Errorf("engine write: rotate memtable: %w", err)
		}
	}

	_, _, err = e.activeMem.Put(key, row)
	if err != nil {
		return fmt.Errorf("engine write: %w", err)
	}

	return nil
}

// Get 根据主键查询一行数据，查询路径：MemTable → Immutable → PrimaryIndex → BloomFilter → Segment。
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

// Scan 扫描指定键范围内的所有行。
// 使用 MergeIterator 合并所有数据源（MemTable、Immutable、Segment），
// 结果按键排序，重复键取最新版本。
func (e *Engine) Scan(start, end string) []struct {
	Key   string
	Value Row
} {
	e.mu.RLock()
	defer e.mu.RUnlock()

	entries := e.ScanRange(start, end)
	results := make([]struct {
		Key   string
		Value Row
	}, len(entries))
	for i, entry := range entries {
		results[i].Key = entry.Key
		results[i].Value = entry.Value
	}
	return results
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

	e.mu.Unlock()

	if len(immutable) == 0 {
		return nil
	}

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

	// Write checkpoint after successful flush
	e.mu.Lock()
	colMeta := e.columnMeta
	e.mu.Unlock()

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

// Compact 执行 Tiered Compaction，将 L0 合并到 L1。
func (e *Engine) Compact(cols []ColumnMeta) error {
	e.mu.Lock()

	// Sync compactor nextID with flusher to avoid segment ID conflicts
	if e.flusher.nextID > e.compactor.nextID {
		e.compactor.nextID = e.flusher.nextID
	}

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

// ShouldCompact 判断是否需要执行 Compaction。
func (e *Engine) ShouldCompact() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.l0Count() >= defaultL0CompactionThreshold
}

// MemTableSize 返回当前活跃 MemTable 的大小。
func (e *Engine) MemTableSize() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.activeMem.Size()
}

// Close 关闭引擎，同步并关闭 WAL。
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
