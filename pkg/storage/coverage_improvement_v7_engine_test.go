package storage

import (
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestEngineFlushEmptyImmutable 测试 Flush 在没有 immutable memtable 时的行为
func TestEngineFlushEmptyImmutable(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 没有写入任何数据，Flush 应返回 nil
	cols := []ColumnMeta{{ID: 0, Name: "id", Type: common.TypeInt64}}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush with no data: %v", err)
	}
}

// TestEngineFlushWithColumnMeta 测试 Flush 在首次设置 columnMeta 时的行为
func TestEngineFlushWithColumnMeta(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{
		{ID: 0, Name: "id", Type: common.TypeInt64},
		{ID: 1, Name: colName, Type: common.TypeString},
	}

	// 写入数据
	if err := eng.Write("key1", map[string]common.Value{
		"id":         common.NewInt64(1),
		benchColName: common.NewString("alice"),
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// 首次 Flush，设置 columnMeta
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// 验证 columnMeta 已设置
	meta := eng.ColumnMeta()
	if len(meta) != 2 {
		t.Fatalf("expected 2 column meta, got %d", len(meta))
	}
	if meta[0].Name != "id" || meta[1].Name != "name" {
		t.Fatalf("unexpected column meta: %v", meta)
	}
}

// TestDecodeColumnUnknownEncoding 测试 DecodeColumn 对未知编码的处理
func TestDecodeColumnUnknownEncoding(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingType(99),
		Type:     common.TypeInt64,
		RowCount: 1,
		Data:     []byte{0, 0, 0, 0, 0, 0, 0, 0},
	}
	_, _, err := DecodeColumn(enc)
	if err == nil {
		t.Fatal("expected error for unknown encoding")
	}
}

// TestSchedulerTryFlushWithData 测试 tryFlush 在有 immutable memtable 时的行为
func TestSchedulerTryFlushWithData(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{})

	// 没有数据时 tryFlush 应正常返回
	if err := sched.tryFlush(); err != nil {
		t.Fatalf("tryFlush with no data: %v", err)
	}

	// 写入数据
	for i := 0; i < 5; i++ {
		if err := eng.Write(string(rune('a'+i)), map[string]common.Value{
			"id": common.NewInt64(int64(i)),
		}); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	// tryFlush 应正常执行（可能触发 Flush 或仅检查状态）
	if err := sched.tryFlush(); err != nil {
		t.Fatalf("tryFlush with data: %v", err)
	}
}

// TestSchedulerTryCompactNoCompactionNeeded 测试 tryCompact 在不需要 Compaction 时的行为
func TestSchedulerTryCompactNoCompactionNeeded(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{})

	// 没有 L0 segment 时 tryCompact 应正常返回
	if err := sched.tryCompact(); err != nil {
		t.Fatalf("tryCompact with no L0 segments: %v", err)
	}

	stats := sched.Stats()
	if stats.CompactCount != 0 {
		t.Errorf("expected CompactCount=0, got %d", stats.CompactCount)
	}
}

// TestEngineCloseSyncError 测试 Engine.Close 在 WAL Sync 失败时的行为
func TestEngineCloseSyncError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// 先关闭 WAL 文件，使 Sync 失败
	_ = eng.wal.file.Close()

	err = eng.Close()
	if err == nil {
		t.Log("Close succeeded despite closed WAL file")
	}
}

// TestBlockCacheClearNilCache 测试 BlockCache.Clear() 在 nil 缓存上的行为
func TestBlockCacheClearNilCache(t *testing.T) {
	var cache *BlockCache
	// 不应 panic
	cache.Clear()
	_ = t // 仅验证不 panic
}

// TestIndexCacheDisabledCapacity 测试 IndexCache 在容量 <= 0 时的行为
func TestIndexCacheDisabledCapacity(t *testing.T) {
	cache := NewIndexCache(0)

	_, ok := cache.GetColumnStats(1)
	if ok {
		t.Fatal("expected miss on disabled IndexCache")
	}

	cache.PutColumnStats(1, []ColumnStat{{ColumnID: 0}})
	_, ok = cache.GetColumnStats(1)
	if ok {
		t.Fatal("expected miss on disabled IndexCache after put")
	}
}

// TestBlockCachePutOversizedEntry 测试 BlockCache 放入超过容量的条目
func TestBlockCachePutOversizedEntry(t *testing.T) {
	cache := NewBlockCache(100) // 很小的容量

	// 放入一个超过容量的条目
	dc := decodedColumn{data: make([]int64, 1000), typ: common.TypeInt64}
	cache.put(CacheKey{SegmentID: 1, ColumnIdx: 0}, dc)

	// 条目应该被放入（即使超过容量），因为 LRU 淘汰后仍可能不够
	stats := cache.Stats()
	if stats.Entries == 0 {
		t.Fatal("expected at least one entry even if oversized")
	}
}

// TestEngineSegmentsAccessors 测试 Engine 的 Segment 相关访问器
func TestEngineSegmentsAccessors(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 初始状态
	if eng.SegmentCount() != 0 {
		t.Fatalf("expected 0 segments, got %d", eng.SegmentCount())
	}
	if eng.L0SegmentCount() != 0 {
		t.Fatalf("expected 0 L0 segments, got %d", eng.L0SegmentCount())
	}
	if len(eng.Segments()) != 0 {
		t.Fatalf("expected empty segments slice, got %d", len(eng.Segments()))
	}

	// 写入并刷盘
	cols := []ColumnMeta{{ID: 0, Name: "id", Type: common.TypeInt64}}
	for i := 0; i < 5; i++ {
		if err := eng.Write(string(rune('a'+i)), map[string]common.Value{
			"id": common.NewInt64(int64(i)),
		}); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if eng.SegmentCount() == 0 {
		t.Fatal("expected segments after flush")
	}
	if eng.L0SegmentCount() == 0 {
		t.Fatal("expected L0 segments after flush")
	}
}

// TestEngineMemTableSizeAccessor 测试 Engine.MemTableSize() 访问器
func TestEngineMemTableSizeAccessor(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	initialSize := eng.MemTableSize()
	if initialSize != 0 {
		t.Fatalf("expected initial MemTable size 0, got %d", initialSize)
	}

	if err := eng.Write("key1", map[string]common.Value{
		"id": common.NewInt64(1),
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	afterSize := eng.MemTableSize()
	if afterSize <= 0 {
		t.Fatalf("expected MemTable size > 0 after write, got %d", afterSize)
	}
}

// TestEnginePrimaryIndexAccessor 测试 Engine 的索引访问器
func TestEnginePrimaryIndexAccessor(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	if eng.PrimaryIndex() == nil {
		t.Fatal("expected non-nil PrimaryIndex")
	}
	if eng.BloomIndex() == nil {
		t.Fatal("expected non-nil BloomIndex")
	}
	if eng.SparseIndex() == nil {
		t.Fatal("expected non-nil SparseIndex")
	}
}

// TestSchedulerStartAndStop 测试 Scheduler 的启动和停止
func TestSchedulerStartAndStop(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{
		FlushInterval:    10 * time.Millisecond,
		CompactInterval:  10 * time.Millisecond,
		WALCleanInterval: 10 * time.Millisecond,
	})

	// 启动调度器
	sched.Start()

	// 重复启动应无效果
	sched.Start()

	// 停止调度器
	sched.Stop()

	// 重复停止应无效果
	sched.Stop()
}

// TestEngineWriteBatchNil 测试 WriteBatch 对 nil 的处理
func TestEngineWriteBatchNil(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// nil 行列表应直接返回 nil
	if err := eng.WriteBatch(nil); err != nil {
		t.Fatalf("WriteBatch nil: %v", err)
	}
	if err := eng.WriteBatch([]WriteRow{}); err != nil {
		t.Fatalf("WriteBatch empty: %v", err)
	}
}
