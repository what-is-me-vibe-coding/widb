package storage

import (
	"fmt"
	"log"
	"sort"
	"sync/atomic"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
)

// setNextIDAtomic 使用 CAS 将 id 设置到 target，仅当 id 大于当前值时更新。
func setNextIDAtomic(target *atomic.Uint64, id uint64) {
	for {
		current := target.Load()
		if id <= current {
			return
		}
		if target.CompareAndSwap(current, id) {
			return
		}
	}
}

// --- Engine Helpers ---

// addSegment 将 Segment 添加到引擎的内部数据结构并注册索引。
// 调用者必须持有 e.mu 写锁。如果索引注册失败，已添加的数据会被回滚。
func (e *Engine) addSegment(seg *Segment, level int) error {
	e.segments = append(e.segments, seg)
	e.segmentMap[seg.ID] = seg
	e.segmentLevels = append(e.segmentLevels, level)
	if level == 0 {
		e.l0SegmentCount++
	}

	if err := e.registerSegmentIndexes(seg, level); err != nil {
		// 回滚已添加的数据
		e.segments = e.segments[:len(e.segments)-1]
		delete(e.segmentMap, seg.ID)
		e.segmentLevels = e.segmentLevels[:len(e.segmentLevels)-1]
		if level == 0 {
			e.l0SegmentCount--
		}
		return err
	}
	return nil
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
	return e.l0SegmentCount
}

// --- Engine Batch ---

// WriteRow 是批量写入的单行数据。
type WriteRow struct {
	Key    string
	Values map[string]common.Value
}

// WriteBatch 批量写入多行数据，所有行共享一次 WAL sync，大幅提升批量写入吞吐。
// 优化：原子化版本号分配，释放引擎锁进行 WAL I/O，避免阻塞并发读写；支持 GroupCommitter。
func (e *Engine) WriteBatch(rows []WriteRow) error {
	if len(rows) == 0 {
		return nil
	}

	// 校验所有行的 key 不为空，避免空 key 导致后续存储层异常
	for i, row := range rows {
		if row.Key == "" {
			return fmt.Errorf("engine write batch: empty key at row %d is not allowed", i)
		}
	}

	// Step 1: 原子分配版本号（无锁，减少写路径竞争）
	baseVersion := e.nextVersion.Add(uint64(len(rows))) - uint64(len(rows)) + 1

	// Step 2: Serialize WAL record (no lock needed, CPU-bound)
	payload, err := serializeBatchWriteRecord(rows, baseVersion)
	if err != nil {
		return fmt.Errorf("engine write batch: serialize: %w", err)
	}

	// Step 3: WAL append + sync (I/O-bound, no engine lock needed)
	if err := e.wal.AppendBatch([]BatchRecord{{Type: walTypeBatchWrite, Payload: payload}}); err != nil {
		return fmt.Errorf("engine write batch: wal: %w", err)
	}

	// 根据同步模式选择同步策略，与 Engine.Write 保持一致
	var syncCh <-chan struct{}
	e.mu.RLock()
	gc := e.groupCommitter
	e.mu.RUnlock()
	if gc != nil {
		syncCh = gc.Submit()
	} else if err := e.wal.Sync(); err != nil {
		return fmt.Errorf("engine write batch: sync: %w", err)
	}

	// Step 4: Put all rows to memtable under lock (brief hold)
	e.mu.Lock()
	for i := range rows {
		if _, _, err := e.activeMem.Put(rows[i].Key, Row{Version: baseVersion + uint64(i), Columns: rows[i].Values}); err != nil {
			e.mu.Unlock()
			return fmt.Errorf("engine write batch: %w", err)
		}
	}
	if e.activeMem.ShouldFlush() {
		if err := e.rotateMemTable(); err != nil {
			e.mu.Unlock()
			return fmt.Errorf("engine write batch: rotate: %w", err)
		}
	}
	e.mu.Unlock()

	// Step 5: Wait for WAL sync completion (outside engine lock, GroupCommit 模式)
	if syncCh != nil {
		<-syncCh
	}

	return nil
}

// Delete 删除指定 key 的行。通过写入墓碑（tombstone）实现：
// 向 MemTable 写入一行空 Columns 的记录，其版本号最新，在 MergeIterator 中
// 覆盖旧版本数据，使该 key 在扫描与 Get 中不可见。
//
// 注意：墓碑仅对 MemTable 中未刷盘的数据完全生效。一旦墓碑被刷入 Segment，
// 由于 Segment 列式存储会按 ColumnMeta 重建行，墓碑会变为全 NULL 行而非被丢弃。
// 对于已刷盘数据，建议使用 ENGINE=memory 表或在 compaction 后再验证。
func (e *Engine) Delete(key string) error {
	if key == "" {
		return fmt.Errorf("engine delete: empty key is not allowed")
	}

	version := e.nextVersion.Add(1)

	// 墓碑记录：Columns 为 nil，序列化后列数为 0。
	payload, err := serializeWriteRecord(key, version, nil)
	if err != nil {
		return fmt.Errorf("engine delete: serialize: %w", err)
	}

	if err := e.wal.AppendWrite(payload); err != nil {
		return fmt.Errorf("engine delete: wal: %w", err)
	}

	var syncCh <-chan struct{}
	e.mu.RLock()
	gc := e.groupCommitter
	e.mu.RUnlock()
	if gc != nil {
		syncCh = gc.Submit()
	} else if err := e.wal.Sync(); err != nil {
		return fmt.Errorf("engine delete: sync: %w", err)
	}

	e.mu.Lock()
	if e.activeMem.ShouldFlush() {
		if err := e.rotateMemTable(); err != nil {
			e.mu.Unlock()
			return fmt.Errorf("engine delete: rotate: %w", err)
		}
	}
	_, _, err = e.activeMem.Put(key, Row{Version: version, Columns: nil, Tombstone: true})
	e.mu.Unlock()
	if err != nil {
		return fmt.Errorf("engine delete: %w", err)
	}

	if syncCh != nil {
		<-syncCh
	}
	return nil
}

// --- Engine Index ---

// registerSegmentIndexes 将 Segment 注册到所有索引（主键、布隆、稀疏），
// 并将列统计信息缓存到 IndexCache。
func (e *Engine) registerSegmentIndexes(seg *Segment, level int) error {
	segMeta := index.SegmentMeta{
		ID:     seg.ID,
		MinKey: seg.MinKey,
		MaxKey: seg.MaxKey,
		Level:  level,
	}
	if err := e.primaryIndex.RegisterSegment(segMeta); err != nil {
		return fmt.Errorf("engine: register primary index for segment %d: %w", seg.ID, err)
	}

	if len(seg.Footer.BloomFilter) > 0 {
		if err := e.bloomIndex.RegisterFromBytes(seg.ID, seg.Footer.BloomFilter); err != nil {
			return fmt.Errorf("engine: register bloom index for segment %d: %w", seg.ID, err)
		}
	}

	e.sparseIndex.LoadFromSegment(seg, seg.MinKey, seg.MaxKey, level)

	// 缓存列统计信息到 IndexCache
	if len(seg.Footer.ColumnStats) > 0 {
		stats := make([]ColumnStat, len(seg.Footer.ColumnStats))
		copy(stats, seg.Footer.ColumnStats)
		e.indexCache.PutColumnStats(seg.ID, stats)
	}

	return nil
}

// unregisterSegmentIndexes 从所有索引中注销 Segment，并清除相关缓存。
func (e *Engine) unregisterSegmentIndexes(segID uint64) {
	_ = e.primaryIndex.UnregisterSegment(segID) // 注销失败不影响后续清理
	e.bloomIndex.Unregister(segID)
	e.sparseIndex.UnregisterSegment(segID)
	e.blockCache.Invalidate(segID)
	e.indexCache.Invalidate(segID)
}

// --- Engine Read ---

// isTombstone 判断一行是否为墓碑标记（DELETE 写入的空行）。
// 墓碑行通过 Tombstone 标志位标记，与合法的空列行区分。
func isTombstone(row Row) bool {
	return row.Tombstone
}

// Get 根据主键查询一行数据，查询路径：MemTable → Immutable → PrimaryIndex → BloomFilter → Segment。
// 若查到的是墓碑（已删除），返回 (Row{}, false)。
func (e *Engine) Get(key string) (Row, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if row, ok := e.activeMem.Get(key); ok {
		return row, !isTombstone(row)
	}

	for i := len(e.immutable) - 1; i >= 0; i-- {
		if row, ok := e.immutable[i].Get(key); ok {
			return row, !isTombstone(row)
		}
	}

	row, ok := e.getFromSegments(key)
	return row, ok && !isTombstone(row)
}

func (e *Engine) getFromSegments(key string) (Row, bool) {
	segIDs := e.primaryIndex.Lookup(key)
	if len(segIDs) == 0 {
		return Row{}, false
	}

	// 仅在多个 segment 时排序，单 segment 跳过排序减少开销
	if len(segIDs) > 1 {
		sort.Slice(segIDs, func(i, j int) bool { return segIDs[i] < segIDs[j] })
	}

	for i := len(segIDs) - 1; i >= 0; i-- {
		segID := segIDs[i]
		if !e.bloomIndex.MayContainString(segID, key) {
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

		columns := e.fetchColumnsFromSegment(seg, segID, rowIdx)
		return Row{Version: seg.ID, Columns: columns}, true
	}

	return Row{}, false
}

// fetchColumnsFromSegment 从 Segment 中提取指定行所有列的值，优先从 BlockCache 读取。
// 解码后的列数据在大小允许时写入 BlockCache，防止大体积冷数据驱逐热数据。
// 优化：预分配 map 容量与列数匹配，减少 rehash 开销。
func (e *Engine) fetchColumnsFromSegment(seg *Segment, segID uint64, rowIdx uint32) map[string]common.Value {
	columns := make(map[string]common.Value, len(e.columnMeta))
	for colIdx, col := range e.columnMeta {
		cacheKey := CacheKey{SegmentID: segID, ColumnIdx: uint32(colIdx)}
		if dc, ok := e.blockCache.get(cacheKey); ok {
			columns[col.Name] = extractValue(dc, rowIdx)
			continue
		}
		val, err := seg.GetColumnValue(uint32(colIdx), rowIdx)
		if err != nil {
			continue
		}
		columns[col.Name] = val
		if dc, ok := seg.getColCache(uint32(colIdx)); ok {
			if estimateDecodedSize(dc) <= e.blockCacheMaxEntrySize {
				e.blockCache.put(cacheKey, dc)
			}
		}
	}
	return columns
}

func (e *Engine) findSegmentByID(segID uint64) *Segment {
	return e.segmentMap[segID]
}

// ColumnPredicate 表示一个列级谓词，用于段裁剪优化。
// 通过稀疏索引的列统计信息（Min/Max），在扫描前跳过不可能包含匹配数据的段，
// 减少不必要的解码和过滤开销，对宽表选择性查询效果尤为显著。
type ColumnPredicate struct {
	ColumnName string
	Op         index.PredicateOp
	Value      common.Value
}

// ScanRangeWithPruning 扫描指定键范围内的所有行，同时利用列谓词进行段裁剪。
// 对于每个满足键范围的段，检查其列统计信息是否可以排除该段，
// 仅扫描可能包含匹配数据的段，减少 I/O 和 CPU 开销。
// MemTable 数据不受段裁剪影响（无列统计），始终参与扫描。
func (e *Engine) ScanRangeWithPruning(start, end string, predicates []ColumnPredicate) []ScanEntry {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.scanRangeWithPruningUnlocked(start, end, predicates)
}

// scanRangeWithPruningUnlocked 在不持锁的情况下执行带段裁剪的扫描。
// 调用者必须持有 e.mu.RLock。
func (e *Engine) scanRangeWithPruningUnlocked(start, end string, predicates []ColumnPredicate) []ScanEntry {
	iters := e.buildScanIteratorsWithPruning(start, end, predicates)
	if len(iters) == 0 {
		return nil
	}

	// 优先使用 MemTable 迭代器的精确行数预分配，无 MemTable 数据时回退到估算值。
	estimatedSize := sumIterCounts(iters)
	if estimatedSize == 0 {
		estimatedSize = capScanPrealloc(e.estimateScanSize(start, end))
	}

	mi := NewMergeIterator(iters...)
	defer mi.Close()

	results := make([]ScanEntry, 0, estimatedSize)
	for mi.Next() {
		entry := mi.Entry()
		if isTombstone(entry.Value) {
			continue // 跳过墓碑（已删除的行）
		}
		results = append(results, entry)
	}

	if err := mi.Err(); err != nil {
		log.Printf("scan range with pruning [%q,%q]: %v", start, end, err)
	}

	return results
}

// buildScanIteratorsWithPruning 构建带段裁剪的扫描迭代器。
// 对每个段，检查列谓词是否可以排除该段；MemTable 始终参与扫描。
func (e *Engine) buildScanIteratorsWithPruning(start, end string, predicates []ColumnPredicate) []ScanIterator {
	capacity := len(e.segments) + len(e.immutable) + 1
	iters := make([]ScanIterator, 0, capacity)

	// 构建列名到列 ID 的映射，用于查找稀疏索引
	colNameToID := make(map[string]uint32, len(e.columnMeta))
	for _, col := range e.columnMeta {
		colNameToID[col.Name] = col.ID
	}

	for _, seg := range e.segments {
		if seg.MinKey > end || seg.MaxKey < start {
			continue
		}
		// 使用列谓词进行段裁剪：任一谓词可以排除该段则跳过
		if e.canSkipSegment(seg.ID, predicates, colNameToID) {
			continue
		}
		iters = append(iters, newSegmentIterator(seg, e.columnMeta, start, end, e.blockCache))
	}

	for i := 0; i < len(e.immutable); i++ {
		iters = append(iters, newMemTableIterator(e.immutable[i], start, end))
	}

	iters = append(iters, newMemTableIterator(e.activeMem, start, end))

	return iters
}

// canSkipSegment 检查是否可以根据列谓词跳过指定段。
// 如果任一谓词的列统计信息表明该段不可能包含匹配数据，则返回 true。
func (e *Engine) canSkipSegment(segID uint64, predicates []ColumnPredicate, colNameToID map[string]uint32) bool {
	for _, pred := range predicates {
		colID, ok := colNameToID[pred.ColumnName]
		if !ok {
			continue
		}
		if e.sparseIndex.CanSkip(segID, colID, pred.Op, pred.Value) {
			return true
		}
	}
	return false
}

// maxScanResultPrealloc 限制扫描结果切片的预分配上限。
// estimateScanSize 对 MemTable 使用全量行数估算（无法廉价获知范围内精确行数），
// 选择性范围扫描（如点查附近小区间）实际命中行数远小于估算值，
// 不加限制会导致 4MB~40MB 级别的无效预分配与 GC 压力。
// 16384 条（约 512KB）足以覆盖中小型扫描避免扩容，
// 大型全表扫描则按 2 倍增长摊销，额外拷贝开销可忽略。
const maxScanResultPrealloc = 1 << 14 // 16384

// estimateScanSize 估算扫描结果大小，用于预分配结果切片。
func (e *Engine) estimateScanSize(start, end string) int {
	estimatedSize := e.activeMem.Len()
	for _, imm := range e.immutable {
		estimatedSize += imm.Len()
	}
	for _, seg := range e.segments {
		if seg.MinKey <= end && seg.MaxKey >= start {
			estimatedSize += int(seg.RowCount)
		}
	}
	if estimatedSize > 1<<20 {
		estimatedSize = 1 << 20
	}
	return estimatedSize
}

// capScanPrealloc 返回用于结果切片预分配的容量，上限为 maxScanResultPrealloc。
// 估算值用于预分配以减少 append 扩容，但对选择性扫描会严重高估，
// 因此封顶以平衡“减少扩容”与“避免无效内存占用”。
func capScanPrealloc(estimated int) int {
	if estimated > maxScanResultPrealloc {
		return maxScanResultPrealloc
	}
	return estimated
}

// Scan 扫描指定键范围内的所有行，直接返回 ScanEntry 切片，避免额外结构体复制。
// 注意：此方法静默丢弃迭代错误。如需获取错误信息，请使用 ScanWithError。
func (e *Engine) Scan(start, end string) []ScanEntry {
	entries, err := e.ScanWithError(start, end)
	if err != nil {
		log.Printf("engine scan [%q,%q]: %v", start, end, err)
	}
	return entries
}

// ScanWithError 扫描指定键范围内的所有行，同时返回迭代过程中遇到的错误。
func (e *Engine) ScanWithError(start, end string) ([]ScanEntry, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.scanRangeUnlocked(start, end)
}
