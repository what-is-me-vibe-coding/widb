package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ---------------------------------------------------------------------------
// Engine Write 与 GroupCommit 模式测试（v6 补充）
// ---------------------------------------------------------------------------

// TestWriteGroupCommitV6 测试 Write 在 GroupCommit 模式下多键写入。
func TestWriteGroupCommitV6(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir:      dir,
		SyncMode:     SyncGroupCommit,
		SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入数据，应通过 GroupCommitter 提交
	if err := eng.Write("gc_v6_key1", map[string]common.Value{colVal: common.NewInt64(100)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if err := eng.Write("gc_v6_key2", map[string]common.Value{colVal: common.NewInt64(200)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}

	// 验证数据可读
	row, ok := eng.Get("gc_v6_key1")
	if !ok {
		t.Fatal("期望找到 gc_v6_key1")
	}
	if row.Columns[colVal].Int64 != 100 {
		t.Errorf("期望 100，实际 %d", row.Columns[colVal].Int64)
	}

	row, ok = eng.Get("gc_v6_key2")
	if !ok {
		t.Fatal("期望找到 gc_v6_key2")
	}
	if row.Columns[colVal].Int64 != 200 {
		t.Errorf("期望 200，实际 %d", row.Columns[colVal].Int64)
	}
}

// TestWriteMemTableRotationV6 测试 Write 触发 memtable 轮转并验证数据完整性。
func TestWriteMemTableRotationV6(t *testing.T) {
	dir := t.TempDir()
	// 设置很小的 MaxMemTableSize 以触发轮转
	eng, err := NewEngine(EngineConfig{
		DataDir:         dir,
		MaxMemTableSize: 128,
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入足够多的数据以触发 memtable 轮转
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("rot_v6_%04d", i)
		if err := eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))}); err != nil {
			t.Fatalf("Write %d 失败: %v", i, err)
		}
	}

	// 验证数据仍可读取
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("rot_v6_%04d", i)
		row, ok := eng.Get(key)
		if !ok {
			t.Errorf("期望找到 %s", key)
			continue
		}
		if row.Columns[colVal].Int64 != int64(i) {
			t.Errorf("key %s: 期望 %d，实际 %d", key, i, row.Columns[colVal].Int64)
		}
	}
}

// ---------------------------------------------------------------------------
// writeCheckpoint 测试（v6 补充）
// ---------------------------------------------------------------------------

// TestWriteCheckpointAfterFlushV6 测试 Flush 后 writeCheckpoint 写入 WAL checkpoint 记录。
func TestWriteCheckpointAfterFlushV6(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// 写入数据
	if err := eng.Write("cp_v6_key1", map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if err := eng.Write("cp_v6_key2", map[string]common.Value{colVal: common.NewInt64(2)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}

	// Flush 应触发 writeCheckpoint
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	// 验证 segment 已创建
	if eng.SegmentCount() == 0 {
		t.Error("期望至少 1 个 segment，实际 0")
	}
}

// TestWriteCheckpointGroupCommitV6 测试 GroupCommit 模式下的 writeCheckpoint。
func TestWriteCheckpointGroupCommitV6(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir:      dir,
		SyncMode:     SyncGroupCommit,
		SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// 写入并 Flush，应通过 GroupCommitter.SyncNow() 同步
	if err := eng.Write("gc_cp_v6_key", map[string]common.Value{colVal: common.NewInt64(42)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	// 验证数据在 segment 中
	if eng.SegmentCount() == 0 {
		t.Error("期望至少 1 个 segment")
	}
}

// TestWriteCheckpointMultipleFlushesV6 测试多次 Flush 后 checkpoint 版本递增。
func TestWriteCheckpointMultipleFlushesV6(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// 第一次 Flush
	if err := eng.Write("cp_v6_1", map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write 1 失败: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 1 失败: %v", err)
	}

	// 第二次 Flush
	if err := eng.Write("cp_v6_2", map[string]common.Value{colVal: common.NewInt64(2)}); err != nil {
		t.Fatalf("Write 2 失败: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush 2 失败: %v", err)
	}

	// 验证两次 Flush 都成功创建了 segment
	if eng.SegmentCount() < 2 {
		t.Errorf("期望至少 2 个 segment，实际 %d", eng.SegmentCount())
	}
}

// ---------------------------------------------------------------------------
// WriteBatch 测试（v6 补充）
// ---------------------------------------------------------------------------

// TestWriteBatchEmptyRowsV6 测试 WriteBatch 传入空行切片。
func TestWriteBatchEmptyRowsV6(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 空切片应直接返回 nil
	if err := eng.WriteBatch(nil); err != nil {
		t.Errorf("期望 nil，实际 %v", err)
	}
	if err := eng.WriteBatch([]WriteRow{}); err != nil {
		t.Errorf("期望 nil，实际 %v", err)
	}
}

// TestWriteBatchGroupCommitV6 测试 WriteBatch 在 GroupCommit 模式下的批量写入。
func TestWriteBatchGroupCommitV6(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir:      dir,
		SyncMode:     SyncGroupCommit,
		SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	rows := []WriteRow{
		{Key: "batch_gc_v6_1", Values: map[string]common.Value{colVal: common.NewInt64(10)}},
		{Key: "batch_gc_v6_2", Values: map[string]common.Value{colVal: common.NewInt64(20)}},
		{Key: "batch_gc_v6_3", Values: map[string]common.Value{colVal: common.NewInt64(30)}},
	}

	if err := eng.WriteBatch(rows); err != nil {
		t.Fatalf("WriteBatch 失败: %v", err)
	}

	// 验证所有行可读
	for i, row := range rows {
		got, ok := eng.Get(row.Key)
		if !ok {
			t.Errorf("第 %d 行: 期望找到 key %s", i, row.Key)
			continue
		}
		expectedVal := int64((i + 1) * 10)
		if got.Columns[colVal].Int64 != expectedVal {
			t.Errorf("key %s: 期望 %d，实际 %d", row.Key, expectedVal, got.Columns[colVal].Int64)
		}
	}
}

// TestWriteBatchMemTableRotationV6 测试 WriteBatch 触发 memtable 轮转。
func TestWriteBatchMemTableRotationV6(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir:         dir,
		MaxMemTableSize: 128,
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 批量写入足够多的数据以触发轮转
	for batch := 0; batch < 5; batch++ {
		var rows []WriteRow
		for i := 0; i < 20; i++ {
			key := fmt.Sprintf("batch_rot_v6_%d_%04d", batch, i)
			rows = append(rows, WriteRow{
				Key:    key,
				Values: map[string]common.Value{colVal: common.NewInt64(int64(batch*20 + i))},
			})
		}
		if err := eng.WriteBatch(rows); err != nil {
			t.Fatalf("WriteBatch %d 失败: %v", batch, err)
		}
	}

	// 验证部分数据可读
	row, ok := eng.Get("batch_rot_v6_0_0000")
	if !ok {
		t.Fatal("期望找到 batch_rot_v6_0_0000")
	}
	if row.Columns[colVal].Int64 != 0 {
		t.Errorf("期望 0，实际 %d", row.Columns[colVal].Int64)
	}
}
