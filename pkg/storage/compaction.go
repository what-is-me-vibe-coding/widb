package storage

import (
	"container/heap"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const (
	defaultL0CompactionThreshold = 4
	defaultLevelSizeMultiplier   = 2
)

// Compactor 负责将多个 Segment 合并为更少的 Segment。
type Compactor struct {
	mu      sync.Mutex
	dataDir string
	nextID  atomic.Uint64 // 无锁读取 segment ID
}

// SetNextID updates the compactor's nextID if the given id is larger.
func (c *Compactor) SetNextID(id uint64) {
	setNextIDAtomic(&c.nextID, id)
}

// NextID 无锁读取 compactor 的当前 nextID。
func (c *Compactor) NextID() uint64 {
	return c.nextID.Load()
}

// NewCompactor 创建一个 Compactor 实例。
func NewCompactor(dataDir string) *Compactor {
	return &Compactor{dataDir: dataDir}
}

// Compact 将输入的 segments 合并为一个新的 Segment。
func (c *Compactor) Compact(segments []*Segment, cols []ColumnMeta) (*Segment, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(segments) == 0 {
		return nil, fmt.Errorf("compactor: no segments to compact")
	}

	rows, err := c.mergeSegments(segments, cols)
	if err != nil {
		return nil, fmt.Errorf("compactor: merge segments: %w", err)
	}

	if len(rows) == 0 {
		return nil, fmt.Errorf("compactor: merged result is empty")
	}

	seg, err := c.buildSegment(rows, cols)
	if err != nil {
		return nil, fmt.Errorf("compactor: build segment: %w", err)
	}

	return seg, nil
}

// CompactToLevel 将 L0 的 segments 合并到 L1，或将 Ln 合并到 Ln+1。
func (c *Compactor) CompactToLevel(segments []*Segment, _ int, cols []ColumnMeta) (*Segment, error) {
	seg, err := c.Compact(segments, cols)
	if err != nil {
		return nil, err
	}
	return seg, nil
}

// segReader 跟踪单个 Segment 在 k-way merge 中的读取位置。
type segReader struct {
	seg    *Segment
	rows   []memRow
	pos    int
	segIdx int // 在 sortedSegs 中的索引，用于去重优先级
}

// compactionEntry 是 k-way merge 堆中的条目。
type compactionEntry struct {
	key    string
	segIdx int
	reader *segReader
}

// compactionHeap 实现堆接口，按 key 升序，key 相同时 segIdx 降序（最新优先）。
type compactionHeap []*compactionEntry

func (h compactionHeap) Len() int { return len(h) }
func (h compactionHeap) Less(i, j int) bool {
	if h[i].key != h[j].key {
		return h[i].key < h[j].key
	}
	// key 相同时，segIdx 大的排在堆顶（优先处理）
	return h[i].segIdx > h[j].segIdx
}
func (h compactionHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *compactionHeap) Push(x any)   { *h = append(*h, x.(*compactionEntry)) }
func (h *compactionHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// sortSegsByID 按 Segment ID 升序排序（ID 越小越旧）。
func sortSegsByID(segs []*Segment) {
	sort.Slice(segs, func(i, j int) bool { return segs[i].ID < segs[j].ID })
}

func (c *Compactor) mergeSegments(segments []*Segment, cols []ColumnMeta) ([]memRow, error) {
	// 使用 k-way merge 替代全量排序：各 Segment 内行已按 key 有序，
	// 通过最小堆归并，复杂度 O(n log k) 优于 O(n log n) 的全量排序。
	// 同时在归并过程中去重，同一 key 保留最高 segment ID（最新版本）。

	// 先按 Segment ID 排序，确保 ID 更大的 segment 在堆中优先级更高
	sortedSegs := make([]*Segment, len(segments))
	copy(sortedSegs, segments)
	sortSegsByID(sortedSegs)

	readers := make([]*segReader, 0, len(sortedSegs))
	estimatedRows := 0
	for i, seg := range sortedSegs {
		rows, err := c.readSegmentRows(seg, cols)
		if err != nil {
			return nil, fmt.Errorf("compactor: read segment %d: %w", seg.ID, err)
		}
		if len(rows) > 0 {
			readers = append(readers, &segReader{
				seg:    seg,
				rows:   rows,
				pos:    0,
				segIdx: i,
			})
			estimatedRows += len(rows)
		}
	}

	if len(readers) == 0 {
		return nil, nil
	}

	// 最小堆：按 key 排序，key 相同时 segIdx 大的优先（最新数据）
	h := &compactionHeap{}
	heap.Init(h)
	for _, r := range readers {
		heap.Push(h, &compactionEntry{
			key:    r.rows[0].Key,
			segIdx: r.segIdx,
			reader: r,
		})
	}

	deduped := make([]memRow, 0, estimatedRows)
	var prevKey string

	for h.Len() > 0 {
		entry := (*h)[0]
		key := entry.key
		row := entry.reader.rows[entry.reader.pos]

		// 推进该 reader 的位置
		entry.reader.pos++
		if entry.reader.pos < len(entry.reader.rows) {
			entry.key = entry.reader.rows[entry.reader.pos].Key
			heap.Fix(h, 0)
		} else {
			heap.Pop(h)
		}

		// 去重：同一 key 只保留第一个遇到的（segIdx 最大，即最新版本）
		if key == prevKey {
			// 跳过旧版本
			continue
		}
		deduped = append(deduped, row)
		prevKey = key
	}

	return deduped, nil
}

func (c *Compactor) readSegmentRows(seg *Segment, _ []ColumnMeta) ([]memRow, error) {
	if seg.RowCount == 0 {
		return nil, nil
	}

	numCols := len(seg.Columns)

	decodedCols := make([]decodedColumn, numCols)
	for i := range seg.Columns {
		cd, err := decodeSegmentColumn(&seg.Columns[i], i)
		if err != nil {
			return nil, err
		}
		decodedCols[i] = cd
	}

	// 预分配连续的 values 缓冲区，每行从中切片，避免逐行分配 []common.Value
	// 减少 GC 压力，特别是大 Segment（百万行级别）时效果显著
	valuesBuf := make([]common.Value, int(seg.RowCount)*numCols)
	rows := make([]memRow, 0, seg.RowCount)
	for r := uint32(0); r < seg.RowCount; r++ {
		offset := int(r) * numCols
		values := valuesBuf[offset : offset+numCols]
		for i := range decodedCols {
			values[i] = extractValue(decodedCols[i], r)
		}
		var key string
		if int(r) < len(seg.Keys) {
			key = seg.Keys[r]
		} else {
			key = fmt.Sprintf("row_%d", seg.ID*1000000+uint64(len(rows)))
		}
		rows = append(rows, memRow{
			Key:    key,
			Values: values,
		})
	}

	return rows, nil
}

// decodeSegmentColumn 解码单个 Segment 列用于 Compaction。
// 使用共享的 decodeColumnFromEncoded 函数，避免重复的列解码逻辑。
func decodeSegmentColumn(src *EncodedColumn, colIdx int) (decodedColumn, error) {
	dc, err := decodeColumnFromEncoded(src, colIdx)
	if err != nil {
		return decodedColumn{}, fmt.Errorf("compactor: %w", err)
	}
	return dc, nil
}

func (c *Compactor) buildSegment(rows []memRow, cols []ColumnMeta) (*Segment, error) {
	rowCount := uint32(len(rows))
	minKey := rows[0].Key
	maxKey := rows[len(rows)-1].Key

	c.nextID.Add(1)
	builder := NewSegmentBuilder(c.nextID.Load(), minKey, maxKey)

	keys := make([]string, len(rows))
	for i, row := range rows {
		keys[i] = row.Key
	}
	builder.SetKeys(keys)

	for colIdx, colMeta := range cols {
		enc, err := buildColumnEncoded(rows, colIdx, colMeta, rowCount)
		if err != nil {
			return nil, err
		}
		builder.AddEncodedColumn(enc)
	}

	seg, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("compactor: build segment: %w", err)
	}

	fileName := filepath.Join(c.dataDir, fmt.Sprintf("segment_%d.widb", c.nextID.Load()))
	data, err := seg.Serialize()
	if err != nil {
		return nil, fmt.Errorf("compactor: serialize segment: %w", err)
	}

	if err := os.MkdirAll(c.dataDir, 0755); err != nil {
		return nil, fmt.Errorf("compactor: create data dir: %w", err)
	}

	if err := os.WriteFile(fileName, data, 0644); err != nil {
		return nil, fmt.Errorf("compactor: write segment file: %w", err)
	}

	seg.FilePath = fileName
	return seg, nil
}

// buildColumnEncoded 将行数据中指定列编码为 EncodedColumn。
func buildColumnEncoded(rows []memRow, colIdx int, colMeta ColumnMeta, rowCount uint32) (*EncodedColumn, error) {
	cv := NewColumnVector(colMeta.ID, colMeta.Type, rowCount)
	for _, row := range rows {
		if colIdx >= len(row.Values) {
			if err := cv.Append(common.NewNull()); err != nil {
				return nil, fmt.Errorf("compactor: column %s append null: %w", colMeta.Name, err)
			}
			continue
		}
		val := row.Values[colIdx]
		if err := cv.Append(val); err != nil {
			return nil, fmt.Errorf("compactor: column %s: %w", colMeta.Name, err)
		}
	}

	enc, err := encodeColumnVector(cv)
	if err != nil {
		return nil, fmt.Errorf("compactor: encode column %s: %w", colMeta.Name, err)
	}
	return enc, nil
}

// CleanupSegments 删除旧 Segment 文件。
func (c *Compactor) CleanupSegments(segments []*Segment) error {
	for _, seg := range segments {
		if seg.FilePath != "" {
			if err := os.Remove(seg.FilePath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("compactor: remove segment %s: %w", seg.FilePath, err)
			}
		}
	}
	return nil
}

type memRow struct {
	Key    string
	Values []common.Value
}

// Compact 执行 Tiered Compaction，将 L0 合并到 L1。
func (e *Engine) Compact(cols []ColumnMeta) error {
	e.mu.Lock()

	// Sync compactor nextID with flusher to avoid segment ID conflicts
	e.compactor.SetNextID(e.flusher.NextID())

	l0Segments, _ := e.collectSegmentsByLevel(0)
	if len(l0Segments) == 0 {
		e.mu.Unlock()
		return nil
	}

	l1Segments, _ := e.collectSegmentsByLevel(1)

	allSegments := make([]*Segment, 0, len(l0Segments)+len(l1Segments))
	allSegments = append(allSegments, l0Segments...)
	allSegments = append(allSegments, l1Segments...)

	// 记录待删除的 segment ID，而非索引，避免并发操作导致索引失效
	compactIDs := make(map[uint64]struct{}, len(allSegments))
	for _, seg := range allSegments {
		compactIDs[seg.ID] = struct{}{}
	}

	e.mu.Unlock()

	newSeg, err := e.compactor.Compact(allSegments, cols)
	if err != nil {
		return fmt.Errorf("engine compact: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// 先注册新 segment 的索引，再注销旧 segment 的索引，
	// 确保任何时刻索引中都有数据可用，避免部分失败导致数据丢失。
	e.segments = append(e.segments, newSeg)
	e.segmentMap[newSeg.ID] = newSeg
	e.segmentLevels = append(e.segmentLevels, 1)
	if err := e.registerSegmentIndexes(newSeg, 1); err != nil {
		// 注册新索引失败，回滚：移除刚添加的 segment
		e.segments = e.segments[:len(e.segments)-1]
		delete(e.segmentMap, newSeg.ID)
		e.segmentLevels = e.segmentLevels[:len(e.segmentLevels)-1]
		return fmt.Errorf("engine compact: %w", err)
	}

	// 新 segment 注册成功后，再注销旧 segment 的索引
	for _, seg := range allSegments {
		e.unregisterSegmentIndexes(seg.ID)
		delete(e.segmentMap, seg.ID)
	}

	// 按 ID 删除旧 segment
	remaining := make([]*Segment, 0, len(e.segments))
	remainingLevels := make([]int, 0, len(e.segmentLevels))
	for i, seg := range e.segments {
		if _, ok := compactIDs[seg.ID]; !ok {
			remaining = append(remaining, seg)
			remainingLevels = append(remainingLevels, e.segmentLevels[i])
		}
	}
	e.segments = remaining
	e.segmentLevels = remainingLevels

	if err := e.compactor.CleanupSegments(allSegments); err != nil {
		return fmt.Errorf("engine compact: cleanup: %w", err)
	}

	// 同步 flusher 的 nextID，避免后续 Flush 产生 segment ID 冲突
	e.flusher.SetNextID(e.compactor.NextID())

	return nil
}

// ShouldCompact 判断是否需要执行 Compaction。
func (e *Engine) ShouldCompact() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.l0Count() >= defaultL0CompactionThreshold
}
