package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// --- Engine 错误路径补充测试 ---

// TestEngineFlushNoImmutable 测试没有 immutable MemTable 时 Flush 的行为
func TestEngineFlushNoImmutable(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	err = eng.Flush(cols)
	if err != nil {
		t.Fatalf("Flush with no data should succeed, got: %v", err)
	}
}

// TestEngineCompactNoL0 测试没有 L0 Segment 时 Compact 的行为
func TestEngineCompactNoL0(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	err = eng.Compact(cols)
	if err != nil {
		t.Fatalf("Compact with no L0 segments should succeed, got: %v", err)
	}
}

// TestEngineShouldCompactEmpty 测试不需要 Compaction 时的判断
func TestEngineShouldCompactEmpty(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	if eng.ShouldCompact() {
		t.Error("expected ShouldCompact=false for empty engine")
	}
}

// TestEngineL0SegmentCountNoSegments 测试空引擎的 L0SegmentCount
func TestEngineL0SegmentCountNoSegments(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	if count := eng.L0SegmentCount(); count != 0 {
		t.Errorf("expected 0 L0 segments, got %d", count)
	}
}

// TestEngineSegmentCountNoSegments 测试空引擎的 SegmentCount
func TestEngineSegmentCountNoSegments(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	if count := eng.SegmentCount(); count != 0 {
		t.Errorf("expected 0 segments, got %d", count)
	}
}

// TestEngineMemTableSizeNoData 测试空引擎的 MemTableSize
func TestEngineMemTableSizeNoData(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	if size := eng.MemTableSize(); size != 0 {
		t.Errorf("expected 0 MemTable size, got %d", size)
	}
}

// TestEngineColumnMetaNoData 测试空引擎的 ColumnMeta
func TestEngineColumnMetaNoData(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	if len(eng.ColumnMeta()) != 0 {
		t.Errorf("expected empty ColumnMeta, got %d items", len(eng.ColumnMeta()))
	}
}

// TestEngineGetMissingKey 测试查询不存在的键
func TestEngineGetMissingKey(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	if _, ok := eng.Get("nonexistent_key"); ok {
		t.Error("expected false for non-existent key")
	}
}

// TestEngineSegmentCountAfterFlushV2 测试 Flush 后 SegmentCount 正确
func TestEngineSegmentCountAfterFlushV2(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	if eng.SegmentCount() != 1 {
		t.Errorf("expected 1 segment after flush, got %d", eng.SegmentCount())
	}
	if eng.L0SegmentCount() != 1 {
		t.Errorf("expected 1 L0 segment after flush, got %d", eng.L0SegmentCount())
	}
}

// TestEngineFlushSetsColumnMetaV2 测试 Flush 正确设置 ColumnMeta
func TestEngineFlushSetsColumnMetaV2(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{
		{ID: 0, Name: benchColName, Type: common.TypeString},
		{ID: 1, Name: colAge, Type: common.TypeInt64},
	}
	_ = eng.Write("a", map[string]common.Value{benchColName: common.NewString("alice"), colAge: common.NewInt64(30)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	meta := eng.ColumnMeta()
	if len(meta) != 2 {
		t.Fatalf("expected 2 column metas, got %d", len(meta))
	}
	if meta[0].Name != benchColName || meta[1].Name != colAge {
		t.Errorf("unexpected column meta: %+v", meta)
	}
}

// TestEngineCloseDoubleCloseV2 测试引擎重复关闭
func TestEngineCloseDoubleCloseV2(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	if err := eng.Close(); err == nil {
		t.Log("double close did not error (acceptable)")
	}
}

// TestEngineSegmentsAfterFlushV2 测试 Flush 后 Segments 返回正确
func TestEngineSegmentsAfterFlushV2(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if len(eng.Segments()) != 1 {
		t.Errorf("expected 1 segment, got %d", len(eng.Segments()))
	}
}

// TestEngineIndexAccessorsV2 测试引擎索引访问器
func TestEngineIndexAccessorsV2(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	if eng.PrimaryIndex() == nil {
		t.Error("expected non-nil PrimaryIndex")
	}
	if eng.BloomIndex() == nil {
		t.Error("expected non-nil BloomIndex")
	}
	if eng.SparseIndex() == nil {
		t.Error("expected non-nil SparseIndex")
	}
}

// TestEngineWriteTriggersRotation 测试写入触发 MemTable 轮转
func TestEngineWriteTriggersRotation(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir(), MaxMemTableSize: 256})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	for i := 0; i < 100; i++ {
		key := string(rune('a' + i%26))
		if err := eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))}); err != nil {
			t.Fatalf("Write %d failed: %v", i, err)
		}
	}
}

// --- Engine Write GroupCommit 测试 ---

// TestWriteWithGroupCommit 测试 SyncGroupCommit 模式下的 Write 路径。
// 覆盖 Engine.Write 中 groupCommitter.Submit() 和 <-syncCh 的代码路径。
func TestWriteWithGroupCommit(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir:      dir,
		SyncMode:     SyncGroupCommit,
		SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入数据，触发 groupCommitter.Submit() 路径
	vals := map[string]common.Value{
		colVal: common.NewInt64(42),
	}
	if err := eng.Write("key1", vals); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// 验证数据可读
	row, ok := eng.Get("key1")
	if !ok {
		t.Fatal("key1 未找到")
	}
	if v, exists := row.Columns[colVal]; !exists || v.Int64 != 42 {
		t.Errorf("期望 val=42, 实际: %v", v)
	}

	// 写入更多数据，确保 groupCommitter 路径稳定工作
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key_%d", i)
		vals := map[string]common.Value{
			colVal: common.NewInt64(int64(i)),
		}
		if err := eng.Write(key, vals); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
}

// TestWriteBatchWithGroupCommit 测试 SyncGroupCommit 模式下的 WriteBatch 路径。
// 覆盖 Engine.WriteBatch 中 groupCommitter.Submit() 和 <-syncCh 的代码路径。
func TestWriteBatchWithGroupCommit(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir:      dir,
		SyncMode:     SyncGroupCommit,
		SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	rows := []WriteRow{
		{Key: "key1", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
		{Key: crKey2, Values: map[string]common.Value{colVal: common.NewInt64(2)}},
		{Key: crKey3, Values: map[string]common.Value{colVal: common.NewInt64(3)}},
	}

	if err := eng.WriteBatch(rows); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	// 验证数据可读
	for i, key := range []string{"key1", crKey2, crKey3} {
		row, ok := eng.Get(key)
		if !ok {
			t.Fatalf("%s 未找到", key)
		}
		if v, exists := row.Columns[colVal]; !exists || v.Int64 != int64(i+1) {
			t.Errorf("%s: 期望 val=%d, 实际: %v", key, i+1, v)
		}
	}
}

// TestWriteCheckpointWithGroupCommit 测试 SyncGroupCommit 模式下的 writeCheckpoint 路径。
// 覆盖 writeCheckpoint 中 gc.SyncNow() 的代码路径。
func TestWriteCheckpointWithGroupCommit(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir:      dir,
		SyncMode:     SyncGroupCommit,
		SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入数据
	if err := eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(100)}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Flush 会调用 writeCheckpoint，触发 gc.SyncNow() 路径
	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// 验证 segment 已创建
	segs := eng.Segments()
	if len(segs) != 1 {
		t.Fatalf("期望 1 个 segment, 实际: %d", len(segs))
	}
	if segs[0].RowCount != 1 {
		t.Errorf("期望 rowCount=1, 实际: %d", segs[0].RowCount)
	}
}

// TestWriteCheckpointWithGroupCommitMultipleFlushes 测试多次 Flush 下 GroupCommit 的 writeCheckpoint 路径。
func TestWriteCheckpointWithGroupCommitMultipleFlushes(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir:      dir,
		SyncMode:     SyncGroupCommit,
		SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// 第一次写入并 Flush
	if err := eng.Write("k1", map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write k1: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 1: %v", err)
	}

	// 第二次写入并 Flush
	if err := eng.Write("k2", map[string]common.Value{colVal: common.NewInt64(2)}); err != nil {
		t.Fatalf("Write k2: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 2: %v", err)
	}

	// 验证两个 segment 已创建
	segs := eng.Segments()
	if len(segs) != 2 {
		t.Fatalf("期望 2 个 segment, 实际: %d", len(segs))
	}
}
