package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

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
