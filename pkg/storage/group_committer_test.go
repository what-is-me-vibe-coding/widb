package storage

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const (
	gcColName   = "name"
	gcColScore  = "score"
	gcColValue  = "value"
	gcColSeq    = "seq"
	gcColWriter = "writer"
	gcValAlice  = "alice"
)

// TestGroupCommitterBasic 验证 GroupCommitter 基本功能：提交后能收到同步完成通知。
func TestGroupCommitterBasic(t *testing.T) {
	wal, err := CreateWAL(t.TempDir() + "/wal.log")
	if err != nil {
		t.Fatalf("create wal: %v", err)
	}
	defer func() { _ = wal.Close() }()

	gc := NewGroupCommitter(wal, 1*time.Millisecond)
	defer gc.Close()

	ch := gc.Submit()
	select {
	case <-ch:
		// 同步完成
	case <-time.After(100 * time.Millisecond):
		t.Fatal("group commit sync timed out")
	}
}

// TestGroupCommitterMultipleWriters 验证多个写入者共享同一次 sync。
func TestGroupCommitterMultipleWriters(t *testing.T) {
	wal, err := CreateWAL(t.TempDir() + "/wal.log")
	if err != nil {
		t.Fatalf("create wal: %v", err)
	}
	defer func() { _ = wal.Close() }()

	gc := NewGroupCommitter(wal, 5*time.Millisecond)
	defer gc.Close()

	// 多个写入者同时提交
	const numWriters = 10
	chs := make([]<-chan struct{}, numWriters)
	for i := range chs {
		chs[i] = gc.Submit()
	}

	// 所有写入者应在同一次 sync 后被通知
	for i, ch := range chs {
		select {
		case <-ch:
			// OK
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("writer %d sync timed out", i)
		}
	}
}

// TestGroupCommitterClose 验证 Close 时执行最终同步。
func TestGroupCommitterClose(t *testing.T) {
	wal, err := CreateWAL(t.TempDir() + "/wal.log")
	if err != nil {
		t.Fatalf("create wal: %v", err)
	}

	gc := NewGroupCommitter(wal, 10*time.Millisecond)

	// 提交后立即关闭
	ch := gc.Submit()
	gc.Close()

	// 关闭后应收到同步通知
	select {
	case <-ch:
		// OK
	case <-time.After(100 * time.Millisecond):
		t.Fatal("final sync after close timed out")
	}
}

// TestGroupCommitterSyncNow 验证 SyncNow 立即触发同步。
func TestGroupCommitterSyncNow(t *testing.T) {
	wal, err := CreateWAL(t.TempDir() + "/wal.log")
	if err != nil {
		t.Fatalf("create wal: %v", err)
	}
	defer func() { _ = wal.Close() }()

	gc := NewGroupCommitter(wal, 1*time.Second) // 长间隔
	defer gc.Close()

	ch := gc.Submit()

	// SyncNow 应立即触发同步，不等定时器
	gc.SyncNow()

	select {
	case <-ch:
		// OK
	case <-time.After(100 * time.Millisecond):
		t.Fatal("SyncNow did not trigger sync")
	}
}

// TestEngineGroupCommitWrite 验证 GroupCommit 模式下的写入正确性。
func TestEngineGroupCommitWrite(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir:      t.TempDir(),
		SyncMode:     SyncGroupCommit,
		SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	vals := map[string]common.Value{
		gcColName:  common.NewString(gcValAlice),
		gcColScore: common.NewInt64(95),
	}

	if err := eng.Write("key1", vals); err != nil {
		t.Fatalf("write: %v", err)
	}

	row, ok := eng.Get("key1")
	if !ok {
		t.Fatal("key1 not found")
	}
	if v, exists := row.Columns[gcColName]; !exists || v.Str != gcValAlice {
		t.Errorf("expected name=%s, got %v", gcValAlice, v)
	}
}

// TestEngineGroupCommitBatchWrite 验证 GroupCommit 模式下的批量写入正确性。
func TestEngineGroupCommitBatchWrite(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir:      t.TempDir(),
		SyncMode:     SyncGroupCommit,
		SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	rows := make([]WriteRow, 10)
	for i := range rows {
		rows[i] = WriteRow{
			Key: fmt.Sprintf("key_%d", i),
			Values: map[string]common.Value{
				gcColValue: common.NewInt64(int64(i)),
			},
		}
	}

	if err := eng.WriteBatch(rows); err != nil {
		t.Fatalf("write batch: %v", err)
	}

	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key_%d", i)
		row, ok := eng.Get(key)
		if !ok {
			t.Fatalf("key %s not found", key)
		}
		if v, exists := row.Columns[gcColValue]; !exists || v.Int64 != int64(i) {
			t.Errorf("key %s: expected value=%d, got %v", key, i, v)
		}
	}
}

// TestEngineGroupCommitRecovery 验证 GroupCommit 模式下的崩溃恢复。
func TestEngineGroupCommitRecovery(t *testing.T) {
	dir := t.TempDir()

	// 创建引擎并写入数据
	eng, err := NewEngine(EngineConfig{
		DataDir:      dir,
		SyncMode:     SyncGroupCommit,
		SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	vals := map[string]common.Value{
		gcColName: common.NewString("recovery_test"),
	}

	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key_%d", i)
		if err := eng.Write(key, vals); err != nil {
			t.Fatalf("write %s: %v", key, err)
		}
	}

	// 等待 group commit 同步完成
	time.Sleep(10 * time.Millisecond)

	// 关闭引擎（模拟正常关闭）
	if err := eng.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 重新打开引擎，验证数据恢复
	eng2, err := NewEngine(EngineConfig{
		DataDir:      dir,
		SyncMode:     SyncGroupCommit,
		SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key_%d", i)
		row, ok := eng2.Get(key)
		if !ok {
			t.Fatalf("key %s not found after recovery", key)
		}
		if v, exists := row.Columns[gcColName]; !exists || v.Str != "recovery_test" {
			t.Errorf("key %s: expected name=recovery_test, got %v", key, v)
		}
	}
}

// TestEngineGroupCommitConcurrentWrites 验证 GroupCommit 模式下的并发写入正确性。
func TestEngineGroupCommitConcurrentWrites(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir:      t.TempDir(),
		SyncMode:     SyncGroupCommit,
		SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	const numWriters = 10
	const numWrites = 50

	var wg sync.WaitGroup
	wg.Add(numWriters)

	for w := 0; w < numWriters; w++ {
		go func(writerID int) {
			defer wg.Done()
			for i := 0; i < numWrites; i++ {
				key := fmt.Sprintf("w%d_key_%d", writerID, i)
				vals := map[string]common.Value{
					gcColWriter: common.NewInt64(int64(writerID)),
					gcColSeq:    common.NewInt64(int64(i)),
				}
				if err := eng.Write(key, vals); err != nil {
					t.Errorf("writer %d write %d: %v", writerID, i, err)
					return
				}
			}
		}(w)
	}

	wg.Wait()

	// 验证所有写入的数据
	for w := 0; w < numWriters; w++ {
		for i := 0; i < numWrites; i++ {
			key := fmt.Sprintf("w%d_key_%d", w, i)
			row, ok := eng.Get(key)
			if !ok {
				t.Errorf("key %s not found", key)
				continue
			}
			if v, exists := row.Columns[gcColWriter]; !exists || v.Int64 != int64(w) {
				t.Errorf("key %s: expected writer=%d, got %v", key, w, v)
			}
		}
	}
}

// TestEngineSyncEveryWriteDefault 验证默认 SyncEveryWrite 模式不受影响。
func TestEngineSyncEveryWriteDefault(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
		// SyncMode 默认为 SyncEveryWrite
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	if eng.syncMode != SyncEveryWrite {
		t.Errorf("expected SyncEveryWrite, got %d", eng.syncMode)
	}
	if eng.groupCommitter != nil {
		t.Error("groupCommitter should be nil in SyncEveryWrite mode")
	}

	vals := map[string]common.Value{
		gcColName: common.NewString("default_mode"),
	}
	if err := eng.Write("key1", vals); err != nil {
		t.Fatalf("write: %v", err)
	}

	row, ok := eng.Get("key1")
	if !ok {
		t.Fatal("key1 not found")
	}
	if v, exists := row.Columns[gcColName]; !exists || v.Str != "default_mode" {
		t.Errorf("expected name=default_mode, got %v", v)
	}
}
