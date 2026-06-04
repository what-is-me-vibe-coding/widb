package storage

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestCrashRecovery_ConcurrentWriteRecovery 验证并发写入后崩溃恢复的数据一致性。
func TestCrashRecovery_ConcurrentWriteRecovery(t *testing.T) {
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	const goroutines = 10
	const writesPerRoutine = 50
	var wg sync.WaitGroup

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < writesPerRoutine; j++ {
				key := fmt.Sprintf("g%d_key_%03d", gid, j)
				_ = eng.Write(key, map[string]common.Value{
					colVal: common.NewInt64(int64(gid*1000 + j)),
				})
			}
		}(g)
	}
	wg.Wait()

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	// Verify all writes are recovered
	for g := 0; g < goroutines; g++ {
		for j := 0; j < writesPerRoutine; j++ {
			key := fmt.Sprintf("g%d_key_%03d", g, j)
			row, ok := eng2.Get(key)
			if !ok {
				t.Errorf("key %s not recovered", key)
				continue
			}
			expected := int64(g*1000 + j)
			if row.Columns[colVal].Int64 != expected {
				t.Errorf("key %s: expected %d, got %d", key, expected, row.Columns[colVal].Int64)
			}
		}
	}
}

// TestCrashRecovery_AfterCompaction 验证Flush和Compact后写入更多数据再崩溃，所有数据都能恢复。
func TestCrashRecovery_AfterCompaction(t *testing.T) {
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// Write and flush first batch
	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("c", map[string]common.Value{colVal: common.NewInt64(3)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush 1: %v", err)
	}

	// Write and flush second batch
	_ = eng.Write("b", map[string]common.Value{colVal: common.NewInt64(2)})
	_ = eng.Write("d", map[string]common.Value{colVal: common.NewInt64(4)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush 2: %v", err)
	}

	// Compact
	if err := eng.Compact(cols); err != nil {
		t.Fatalf("compact: %v", err)
	}

	// Write more data after compaction
	_ = eng.Write("e", map[string]common.Value{colVal: common.NewInt64(5)})
	_ = eng.Write("f", map[string]common.Value{colVal: common.NewInt64(6)})

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	// Reopen and verify all data
	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	expectedData := map[string]int64{
		"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "f": 6,
	}
	for key, expected := range expectedData {
		row, ok := eng2.Get(key)
		if !ok {
			t.Errorf("key %s not recovered", key)
			continue
		}
		if row.Columns[colVal].Int64 != expected {
			t.Errorf("key %s: expected %d, got %d", key, expected, row.Columns[colVal].Int64)
		}
	}
}

// TestCrashRecovery_MultipleDataTypes 验证不同数据类型的崩溃恢复。
func TestCrashRecovery_MultipleDataTypes(t *testing.T) {
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	// Write with different data types
	_ = eng.Write("int_key", map[string]common.Value{colVal: common.NewInt64(42)})
	_ = eng.Write("float_key", map[string]common.Value{colVal: common.NewFloat64(3.14)})
	_ = eng.Write("string_key", map[string]common.Value{colVal: common.NewString(testStrHello)})
	_ = eng.Write("bool_key", map[string]common.Value{colVal: common.NewBool(true)})

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	row, ok := eng2.Get("int_key")
	if !ok || row.Columns[colVal].Int64 != 42 {
		t.Errorf("int_key not recovered correctly")
	}

	row, ok = eng2.Get("float_key")
	if !ok || row.Columns[colVal].Float64 != 3.14 {
		t.Errorf("float_key not recovered correctly")
	}

	row, ok = eng2.Get("string_key")
	if !ok || row.Columns[colVal].Str != testStrHello {
		t.Errorf("string_key not recovered correctly")
	}

	row, ok = eng2.Get("bool_key")
	if !ok || row.Columns[colVal].Int64 != 1 {
		t.Errorf("bool_key not recovered correctly")
	}
}

// TestCrashRecovery_WALTruncateAfterCheckpoint 验证WAL在Checkpoint后截断不影响恢复。
func TestCrashRecovery_WALTruncateAfterCheckpoint(t *testing.T) {
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	_ = eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write(crKey2, map[string]common.Value{colVal: common.NewInt64(2)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush: %v", err)
	}

	// Truncate WAL after checkpoint - this removes the checkpoint record
	// so on recovery, columnMeta won't be restored from WAL.
	// However, segment data is still on disk.
	if err := eng.wal.Truncate(); err != nil {
		t.Fatalf("truncate wal: %v", err)
	}

	// Write more data after truncation - this data will be in the new WAL
	_ = eng.Write(crKey3, map[string]common.Value{colVal: common.NewInt64(3)})

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	// Reopen and verify
	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	// key3 should be in WAL (written after truncation)
	row, ok := eng2.Get(crKey3)
	if !ok || row.Columns[colVal].Int64 != 3 {
		t.Errorf("key3 not recovered from WAL after truncation")
	}
}

// corruptSecondWALRecord corrupts the second WAL record's JSON payload
// and recalculates its CRC so the corruption passes CRC check but fails JSON deserialization.
func corruptSecondWALRecord(t *testing.T, walPath string) {
	t.Helper()
	data, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatalf("read wal: %v", err)
	}
	if len(data) < walMetaSize {
		t.Fatalf("WAL file too small: %d bytes", len(data))
	}

	firstTotalLen := int(binary.LittleEndian.Uint32(data[0:4]))
	firstRecordEnd := walHeaderSize + firstTotalLen
	if firstRecordEnd+walHeaderSize > len(data) {
		t.Fatalf("WAL file too small to contain second record")
	}

	secondTotalLen := int(binary.LittleEndian.Uint32(data[firstRecordEnd : firstRecordEnd+4]))
	payloadOffset := firstRecordEnd + walHeaderSize + walTypeSize
	if payloadOffset >= len(data) {
		t.Fatalf("WAL second record payload offset out of bounds")
	}

	data[payloadOffset] ^= 0xFF
	secondPayloadLen := secondTotalLen - walTypeSize - walCRCSize
	crcData := data[firstRecordEnd+walHeaderSize : firstRecordEnd+walHeaderSize+walTypeSize+secondPayloadLen]
	newCRC := crc32.Checksum(crcData, crcTable)
	binary.LittleEndian.PutUint32(data[firstRecordEnd+walHeaderSize+walTypeSize+secondPayloadLen:], newCRC)

	if err := os.WriteFile(walPath, data, 0644); err != nil {
		t.Fatalf("write corrupted wal: %v", err)
	}
}

// TestCrashRecovery_DeserializationErrors 验证WAL记录反序列化错误的容错性。
// 构造一个包含多条记录的 WAL，损坏其中一条的 JSON payload（CRC 仍有效），
// 验证引擎能正常启动、损坏记录被跳过、其余记录正常恢复。
func TestCrashRecovery_DeserializationErrors(t *testing.T) {
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	_ = eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write(crKey2, map[string]common.Value{colVal: common.NewInt64(2)})
	_ = eng.Write(crKey3, map[string]common.Value{colVal: common.NewInt64(3)})

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	corruptSecondWALRecord(t, filepath.Join(dir, "wal.log"))

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine with corrupted WAL: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	row, ok := eng2.Get(crKey1)
	if !ok {
		t.Error("key1 should be recovered (not corrupted)")
	} else if row.Columns[colVal].Int64 != 1 {
		t.Errorf("key1: expected 1, got %d", row.Columns[colVal].Int64)
	}

	_, ok = eng2.Get(crKey2)
	if ok {
		t.Error("key2 should not be recovered (corrupted record)")
	}

	row, ok = eng2.Get(crKey3)
	if !ok {
		t.Error("key3 should be recovered (not corrupted)")
	} else if row.Columns[colVal].Int64 != 3 {
		t.Errorf("key3: expected 3, got %d", row.Columns[colVal].Int64)
	}
}

// TestCrashRecovery_SegmentLoading 验证从磁盘加载段文件的正确性。
func TestCrashRecovery_SegmentLoading(t *testing.T) {
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// Write and flush multiple batches
	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("c", map[string]common.Value{colVal: common.NewInt64(3)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush 1: %v", err)
	}

	_ = eng.Write("b", map[string]common.Value{colVal: common.NewInt64(2)})
	_ = eng.Write("d", map[string]common.Value{colVal: common.NewInt64(4)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush 2: %v", err)
	}

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	// Reopen and verify segments are loaded
	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	// Verify segment count
	if eng2.SegmentCount() != 2 {
		t.Errorf("expected 2 segments, got %d", eng2.SegmentCount())
	}

	// Verify all data from segments
	expectedData := map[string]int64{"a": 1, "b": 2, "c": 3, "d": 4}
	for key, expected := range expectedData {
		row, ok := eng2.Get(key)
		if !ok {
			t.Errorf("key %s not recovered from segments", key)
			continue
		}
		if row.Columns[colVal].Int64 != expected {
			t.Errorf("key %s: expected %d, got %d", key, expected, row.Columns[colVal].Int64)
		}
	}
}

// TestCrashRecovery_L1SegmentRecovery 验证压缩后L1段文件的恢复。
func TestCrashRecovery_L1SegmentRecovery(t *testing.T) {
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// Write and flush to create L0 segments
	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("c", map[string]common.Value{colVal: common.NewInt64(3)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush 1: %v", err)
	}

	_ = eng.Write("b", map[string]common.Value{colVal: common.NewInt64(2)})
	_ = eng.Write("d", map[string]common.Value{colVal: common.NewInt64(4)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("flush 2: %v", err)
	}

	// Compact to create L1 segment
	if err := eng.Compact(cols); err != nil {
		t.Fatalf("compact: %v", err)
	}

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	// Reopen and verify L1 segment data
	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	// All data should be accessible from the compacted L1 segment
	expectedData := map[string]int64{"a": 1, "b": 2, "c": 3, "d": 4}
	for key, expected := range expectedData {
		row, ok := eng2.Get(key)
		if !ok {
			t.Errorf("key %s not recovered from L1 segment", key)
			continue
		}
		if row.Columns[colVal].Int64 != expected {
			t.Errorf("key %s: expected %d, got %d", key, expected, row.Columns[colVal].Int64)
		}
	}
}

// TestCrashRecovery_ImmutableMemTableRecovery 验证不可变MemTable数据的恢复。
func TestCrashRecovery_ImmutableMemTableRecovery(t *testing.T) {
	dir := t.TempDir()
	// Use a small memtable to trigger rotation
	cfg := EngineConfig{DataDir: dir, MaxMemTableSize: 256}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	// Write data that stays in MemTable
	_ = eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write(crKey2, map[string]common.Value{colVal: common.NewInt64(2)})

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	// Reopen - data should be recovered from WAL into activeMem
	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}

	// Write more data to trigger memtable rotation
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("rot_key_%03d", i)
		_ = eng2.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))})
	}

	// key1 should still be accessible (from immutable or active memtable)
	row, ok := eng2.Get(crKey1)
	if !ok {
		t.Error("key1 not accessible after memtable rotation")
	} else if row.Columns[colVal].Int64 != 1 {
		t.Errorf("key1: expected 1, got %d", row.Columns[colVal].Int64)
	}

	_ = eng2.Close()
}
