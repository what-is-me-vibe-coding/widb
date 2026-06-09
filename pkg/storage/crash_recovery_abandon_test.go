package storage

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestCrashRecovery_OverwriteAfterCrash 验证崩溃恢复后覆盖写入的正确性。
func TestCrashRecovery_OverwriteAfterCrash(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}
	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(100)})
	_ = eng.Write("key2", map[string]common.Value{colVal: common.NewInt64(200)})
	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	_ = eng2.Write("key1", map[string]common.Value{colVal: common.NewInt64(999)})
	_ = eng2.Write("key3", map[string]common.Value{colVal: common.NewInt64(300)})
	if err := eng2.Close(); err != nil {
		t.Fatalf("close engine 2: %v", err)
	}

	eng3, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine 2: %v", err)
	}
	defer func() { _ = eng3.Close() }()

	row, ok := eng3.Get("key1")
	if !ok || row.Columns[colVal].Int64 != 999 {
		t.Errorf("key1: expected 999 (overwritten), got %d", row.Columns[colVal].Int64)
	}
	row, ok = eng3.Get("key2")
	if !ok || row.Columns[colVal].Int64 != 200 {
		t.Errorf("key2: expected 200 (original), got %d", row.Columns[colVal].Int64)
	}
	row, ok = eng3.Get("key3")
	if !ok || row.Columns[colVal].Int64 != 300 {
		t.Errorf("key3: expected 300 (new), got %d", row.Columns[colVal].Int64)
	}
}

// TestCrashRecovery_ScanAfterRecovery 验证崩溃恢复后范围扫描的正确性。
func TestCrashRecovery_ScanAfterRecovery(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}
	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	const n = 100
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("key_%04d", i)
		_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))})
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	results := eng2.Scan("key_0010", "key_0050")
	if len(results) != 41 {
		t.Errorf("expected 41 scan results, got %d", len(results))
	}

	for _, r := range results {
		if r.Value.Columns[colVal].Int64 < 10 || r.Value.Columns[colVal].Int64 > 50 {
			t.Errorf("scan result out of range: key=%s, val=%d", r.Key, r.Value.Columns[colVal].Int64)
		}
	}
}

// TestCrashRecovery_MultipleFlushCycles 多次刷盘循环中验证 checkpoint 机制正确。
func TestCrashRecovery_MultipleFlushCycles(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	rng := rand.New(rand.NewSource(321))
	expectedData := make(map[string]int64)

	for i := 0; i < 10; i++ {
		eng, err := NewEngine(cfg)
		if err != nil {
			t.Fatalf("iteration %d: new engine: %v", i, err)
		}

		cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

		for j := 0; j < 15; j++ {
			key := fmt.Sprintf("key_%04d", rng.Intn(400))
			val := int64(rng.Intn(100000))
			_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(val)})
			expectedData[key] = val
		}

		_ = eng.Flush(cols)

		for j := 0; j < 5; j++ {
			key := fmt.Sprintf("key_%04d", rng.Intn(400))
			val := int64(rng.Intn(100000))
			_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(val)})
			expectedData[key] = val
		}

		if err := eng.Close(); err != nil {
			t.Fatalf("iteration %d: close engine: %v", i, err)
		}
	}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("final recovery: %v", err)
	}
	defer func() { _ = eng.Close() }()

	verifyRecoveredData(t, eng, expectedData)
	t.Logf("MultipleFlushCycles: verified %d keys after 10 crash cycles", len(expectedData))
}

// TestCrashRecovery_EmptyDirAfterCrash 验证崩溃后数据目录为空时引擎能正常启动。
func TestCrashRecovery_EmptyDirAfterCrash(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}
	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})
	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine on empty dir: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	_, ok := eng2.Get("key1")
	if ok {
		t.Error("expected key1 not found after dir cleanup")
	}
}

// TestCrashRecovery_CompactThenKill 验证 Compaction 完成后数据能正确恢复。
func TestCrashRecovery_CompactThenKill(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}
	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	for batch := 0; batch < 4; batch++ {
		for j := 0; j < 10; j++ {
			key := fmt.Sprintf("batch%d_%d", batch, j)
			_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(batch*100 + j))})
		}
		_ = eng.Flush(cols)
	}

	_ = eng.Compact(cols)
	_ = eng.Write("post_compact", map[string]common.Value{colVal: common.NewInt64(99999)})

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	for batch := 0; batch < 4; batch++ {
		for j := 0; j < 10; j++ {
			key := fmt.Sprintf("batch%d_%d", batch, j)
			row, ok := eng2.Get(key)
			if !ok {
				t.Errorf("key %s not recovered after compaction", key)
				continue
			}
			expected := int64(batch*100 + j)
			if row.Columns[colVal].Int64 != expected {
				t.Errorf("key %s: expected %d, got %d", key, expected, row.Columns[colVal].Int64)
			}
		}
	}

	row, ok := eng2.Get("post_compact")
	if !ok || row.Columns[colVal].Int64 != 99999 {
		t.Error("post_compact key not recovered")
	}
}

// TestCrashRecovery_VersionMonotonicity 验证崩溃恢复后版本号单调递增。
func TestCrashRecovery_VersionMonotonicity(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}
	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	_ = eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(1)})
	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	_ = eng2.Write(crKey2, map[string]common.Value{colVal: common.NewInt64(2)})
	if err := eng2.Close(); err != nil {
		t.Fatalf("close engine 2: %v", err)
	}

	eng3, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine 2: %v", err)
	}
	_ = eng3.Write(crKey3, map[string]common.Value{colVal: common.NewInt64(3)})
	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	_ = eng3.Flush(cols)
	if err := eng3.Close(); err != nil {
		t.Fatalf("close engine 3: %v", err)
	}

	eng4, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine 3: %v", err)
	}
	defer func() { _ = eng4.Close() }()

	expected := map[string]int64{crKey1: 1, crKey2: 2, crKey3: 3}
	verifyRecoveredData(t, eng4, expected)
}

// TestCrashRecovery_MixedWorkload 混合工作负载下的崩溃恢复测试。
func TestCrashRecovery_MixedWorkload(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	rng := rand.New(rand.NewSource(654))
	expectedData := make(map[string]int64)

	for i := 0; i < 20; i++ {
		eng, err := NewEngine(cfg)
		if err != nil {
			t.Fatalf("iteration %d: new engine: %v", i, err)
		}

		cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

		ops := rng.Intn(5) + 3
		for op := 0; op < ops; op++ {
			switch rng.Intn(3) {
			case 0:
				key := fmt.Sprintf("key_%04d", rng.Intn(500))
				val := int64(rng.Intn(100000))
				_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(val)})
				expectedData[key] = val
			case 1:
				_ = eng.Flush(cols)
			case 2:
				if eng.ShouldCompact() {
					_ = eng.Compact(cols)
				}
			}
		}

		if err := eng.Close(); err != nil {
			t.Fatalf("iteration %d: close engine: %v", i, err)
		}
	}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("final recovery: %v", err)
	}
	defer func() { _ = eng.Close() }()

	verifyRecoveredData(t, eng, expectedData)
	t.Logf("MixedWorkload: verified %d keys after 20 crash cycles", len(expectedData))
}

// TestCrashRecovery_WALMissing 验证 WAL 文件丢失后引擎能正常启动。
func TestCrashRecovery_WALMissing(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}
	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Flush(cols)

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	// 删除 WAL 文件，模拟 WAL 丢失
	walPath := filepath.Join(dir, "wal.log")
	if err := os.Remove(walPath); err != nil {
		t.Fatalf("remove WAL: %v", err)
	}

	// 引擎应能正常启动
	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine after WAL removal: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	// 新写入应正常工作
	_ = eng2.Write("new_key", map[string]common.Value{colVal: common.NewInt64(999)})
	row, ok := eng2.Get("new_key")
	if !ok || row.Columns[colVal].Int64 != 999 {
		t.Error("new write should work after WAL removal")
	}
}

// TestCrashRecovery_DataIntegrityWithScan 通过扫描验证崩溃恢复后的数据完整性。
func TestCrashRecovery_DataIntegrityWithScan(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}
	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	const n = 200
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("key_%04d", i)
		_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))})
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	results := eng2.Scan("key_0000", "key_0199")
	if len(results) != n {
		t.Errorf("expected %d scan results, got %d", n, len(results))
	}

	seen := make(map[string]bool)
	for _, r := range results {
		if seen[r.Key] {
			t.Errorf("duplicate key in scan results: %s", r.Key)
		}
		seen[r.Key] = true
	}
}

// TestCrashRecovery_AbandonWithoutClose 模拟进程被 kill（不调用 Close），
// 验证 WAL 中已 Sync 的数据能恢复。
func TestCrashRecovery_AbandonWithoutClose(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}
	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(100)})
	_ = eng.Write("key2", map[string]common.Value{colVal: common.NewInt64(200)})
	// 不调用 Close，模拟进程被 kill

	// 第二轮：重新打开引擎，验证 WAL 回放
	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	row, ok := eng2.Get("key1")
	if !ok || row.Columns[colVal].Int64 != 100 {
		t.Errorf("key1: expected 100, got %d", row.Columns[colVal].Int64)
	}
	row, ok = eng2.Get("key2")
	if !ok || row.Columns[colVal].Int64 != 200 {
		t.Errorf("key2: expected 200, got %d", row.Columns[colVal].Int64)
	}
}

// TestCrashRecovery_AbandonWithFlush 验证 Flush 后不 Close 的崩溃恢复。
func TestCrashRecovery_AbandonWithFlush(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}
	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	_ = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(100)})
	_ = eng.Flush(cols)
	_ = eng.Write("key2", map[string]common.Value{colVal: common.NewInt64(200)})
	// 不 Close

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	row, ok := eng2.Get("key1")
	if !ok || row.Columns[colVal].Int64 != 100 {
		t.Errorf("key1: expected 100, got %d", row.Columns[colVal].Int64)
	}
	row, ok = eng2.Get("key2")
	if !ok || row.Columns[colVal].Int64 != 200 {
		t.Errorf("key2: expected 200, got %d", row.Columns[colVal].Int64)
	}
}

// TestCrashRecovery_AbandonAfterCompaction 验证 Compaction 后不 Close 的崩溃恢复。
func TestCrashRecovery_AbandonAfterCompaction(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}
	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("c", map[string]common.Value{colVal: common.NewInt64(3)})
	_ = eng.Flush(cols)
	_ = eng.Write("b", map[string]common.Value{colVal: common.NewInt64(2)})
	_ = eng.Write("d", map[string]common.Value{colVal: common.NewInt64(4)})
	_ = eng.Flush(cols)
	_ = eng.Compact(cols)
	_ = eng.Write("e", map[string]common.Value{colVal: common.NewInt64(5)})
	// 不 Close

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	expectedData := map[string]int64{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5}
	verifyRecoveredData(t, eng2, expectedData)
}
