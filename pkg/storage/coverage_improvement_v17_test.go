package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/index"
)

// TestEngineWriteGroupCommitSyncWaitV17 验证 GroupCommit 模式下 Write 正确等待 sync channel。
func TestEngineWriteGroupCommitSyncWaitV17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir:      t.TempDir(),
		SyncMode:     SyncGroupCommit,
		SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入多条数据，每条都会走 GroupCommit sync channel 等待路径
	for i := 0; i < 10; i++ {
		err := eng.Write("key", map[string]common.Value{colVal: common.NewInt64(int64(i))})
		if err != nil {
			t.Fatalf("write key%d: %v", i, err)
		}
	}

	// 验证数据正确
	row, ok := eng.Get("key")
	if !ok {
		t.Fatal("key not found")
	}
	if row.Columns[colVal].Int64 != 9 {
		t.Errorf("expected val=9, got %d", row.Columns[colVal].Int64)
	}
}

// TestEngineWriteGroupCommitSyncWaitConcurrentV17 验证并发写入时 GroupCommit sync channel 等待路径。
func TestEngineWriteGroupCommitSyncWaitConcurrentV17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir:      t.TempDir(),
		SyncMode:     SyncGroupCommit,
		SyncInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	const n = 20
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			err := eng.Write("key", map[string]common.Value{colVal: common.NewInt64(int64(idx))})
			errCh <- err
		}(i)
	}

	for i := 0; i < n; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("concurrent write %d: %v", i, err)
		}
	}
}

// TestEngineWriteWALFileClosedV17 验证 Write 在 WAL 文件描述符关闭后返回错误（SyncEveryWrite 模式）。
func TestEngineWriteWALFileClosedV17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	// 先正常写入一条
	_ = eng.Write("key0", map[string]common.Value{colVal: common.NewInt64(0)})

	// 关闭 WAL 文件描述符以触发 Sync 错误
	_ = eng.wal.file.Close()

	err = eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(1)})
	if err == nil {
		t.Error("expected error when writing with closed WAL file (SyncEveryWrite mode)")
	}
}

// TestEngineWriteBatchWALFileClosedV17 验证 WriteBatch 在 WAL 文件描述符关闭后返回错误。
func TestEngineWriteBatchWALFileClosedV17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	// 先正常写入
	_ = eng.Write("key0", map[string]common.Value{colVal: common.NewInt64(0)})

	// 关闭 WAL 文件描述符
	_ = eng.wal.file.Close()

	err = eng.WriteBatch([]WriteRow{
		{Key: crKey1, Values: map[string]common.Value{colVal: common.NewInt64(1)}},
	})
	if err == nil {
		t.Error("expected error when WriteBatch with closed WAL file")
	}
}

// TestEngineWriteBatchWALClosedV17 验证 WriteBatch 在 WAL Close 后返回错误。
func TestEngineWriteBatchWALClosedV17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	// 关闭 WAL 以触发 AppendBatch 错误
	_ = eng.wal.Close()

	err = eng.WriteBatch([]WriteRow{
		{Key: crKey1, Values: map[string]common.Value{colVal: common.NewInt64(1)}},
	})
	if err == nil {
		t.Error("expected error when WriteBatch with closed WAL")
	}
}

// TestWriteCheckpointSyncErrorV17 验证 writeCheckpoint 在 WAL Sync 失败时返回错误。
func TestWriteCheckpointSyncErrorV17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	_ = eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(1)})

	// 关闭 WAL 文件描述符以触发 Sync 错误
	_ = eng.wal.file.Close()

	err = eng.writeCheckpoint(0)
	if err == nil {
		t.Error("expected error when writeCheckpoint with closed WAL file")
	}
}

// TestWriteCheckpointGroupCommitSyncNowV17 验证 writeCheckpoint 在 GroupCommit 模式下调用 SyncNow。
func TestWriteCheckpointGroupCommitSyncNowV17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir:      t.TempDir(),
		SyncMode:     SyncGroupCommit,
		SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_ = eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(1)})

	err = eng.writeCheckpoint(0)
	if err != nil {
		t.Fatalf("writeCheckpoint with GroupCommit: %v", err)
	}
}

// TestWriteCheckpointAppendErrorV17 验证 writeCheckpoint 在 WAL AppendCheckpoint 失败时返回错误。
func TestWriteCheckpointAppendErrorV17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	_ = eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(1)})

	// 关闭 WAL 以触发 AppendCheckpoint 错误
	_ = eng.wal.Close()

	err = eng.writeCheckpoint(0)
	if err == nil {
		t.Error("expected error when writeCheckpoint with closed WAL")
	}
}

// TestNewGroupCommitterDefaultIntervalV17 验证 NewGroupCommitter 在 syncInterval <= 0 时使用默认值。
func TestNewGroupCommitterDefaultIntervalV17(t *testing.T) {
	wal, err := CreateWAL(filepath.Join(t.TempDir(), "wal.log"))
	if err != nil {
		t.Fatalf("create wal: %v", err)
	}
	defer func() { _ = wal.Close() }()

	// 传入 0，应使用默认间隔
	gc0 := NewGroupCommitter(wal, 0)
	if gc0 == nil {
		t.Fatal("expected non-nil GroupCommitter with 0 interval")
	}
	if gc0.syncInterval != defaultSyncInterval {
		t.Errorf("expected default sync interval %v, got %v", defaultSyncInterval, gc0.syncInterval)
	}
	gc0.Close()

	// 传入负值，应使用默认间隔
	gcNeg := NewGroupCommitter(wal, -1*time.Second)
	if gcNeg == nil {
		t.Fatal("expected non-nil GroupCommitter with negative interval")
	}
	if gcNeg.syncInterval != defaultSyncInterval {
		t.Errorf("expected default sync interval %v, got %v", defaultSyncInterval, gcNeg.syncInterval)
	}
	gcNeg.Close()
}

// TestCompressColumnEmptyDataV17 验证 CompressColumn 对空数据列的处理。
func TestCompressColumnEmptyDataV17(t *testing.T) {
	enc := &EncodedColumn{Data: []byte{}}
	err := CompressColumn(enc)
	if err != nil {
		t.Fatalf("CompressColumn with empty data: %v", err)
	}
	// 空数据应返回 nil
	if enc.Data != nil {
		t.Errorf("expected nil data after compressing empty, got %v", enc.Data)
	}
}

// TestBuildAndRegisterEmptyKeysV17 验证 BuildAndRegister 对空 keys 的处理。
func TestBuildAndRegisterEmptyKeysV17(t *testing.T) {
	bi := index.NewBloomIndex()
	err := bi.BuildAndRegister(1, []string{}, 0.01)
	// 空 keys 应返回 nil data，BuildAndRegister 应返回 nil
	if err != nil {
		t.Fatalf("BuildAndRegister with empty keys: %v", err)
	}
}

// TestEngineWriteGroupCommitWithFileClosedV17 验证 GroupCommit 模式下 WAL 文件关闭后 Write 的行为。
func TestEngineWriteGroupCommitWithFileClosedV17(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir:      t.TempDir(),
		SyncMode:     SyncGroupCommit,
		SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	// 先正常写入
	_ = eng.Write("key0", map[string]common.Value{colVal: common.NewInt64(0)})

	// 关闭 WAL 文件描述符以触发 sync 错误
	_ = eng.wal.file.Close()

	// GroupCommit 模式下，Write 应该在 AppendWrite 阶段就失败
	err = eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(1)})
	if err == nil {
		t.Error("expected error when writing with closed WAL file in GroupCommit mode")
	}
}

// TestEngineWriteGroupCommitSyncWaitAndCloseV17 验证 GroupCommit 模式下多次写入后正常关闭。
func TestEngineWriteGroupCommitSyncWaitAndCloseV17(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{
		DataDir:      dir,
		SyncMode:     SyncGroupCommit,
		SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	// 写入多条数据，确保走 sync channel 等待路径
	for i := 0; i < 20; i++ {
		err := eng.Write("key", map[string]common.Value{colVal: common.NewInt64(int64(i))})
		if err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	// 正常关闭，确保 GroupCommitter 的 Close 路径被覆盖
	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}
}
