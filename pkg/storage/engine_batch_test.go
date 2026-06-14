package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestEngineWriteBatch(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	rows := []WriteRow{
		{Key: "bkey1", Values: map[string]common.Value{colVal: common.NewInt64(10)}},
		{Key: "bkey2", Values: map[string]common.Value{colVal: common.NewInt64(20)}},
		{Key: "bkey3", Values: map[string]common.Value{colVal: common.NewInt64(30)}},
	}

	if err := eng.WriteBatch(rows); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	for i, row := range rows {
		got, ok := eng.Get(row.Key)
		if !ok {
			t.Errorf("key %s not found", row.Key)
			continue
		}
		expected := int64((i + 1) * 10)
		if got.Columns[colVal].Int64 != expected {
			t.Errorf("key %s: expected %d, got %d", row.Key, expected, got.Columns[colVal].Int64)
		}
	}
}

func TestEngineWriteBatchEmpty(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 空批量写入应该是 no-op
	if err := eng.WriteBatch(nil); err != nil {
		t.Fatalf("WriteBatch(nil): %v", err)
	}
	if err := eng.WriteBatch([]WriteRow{}); err != nil {
		t.Fatalf("WriteBatch(empty): %v", err)
	}
}

func TestEngineWriteBatchSingle(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	rows := []WriteRow{
		{Key: "single_row", Values: map[string]common.Value{colVal: common.NewInt64(42)}},
	}

	if err := eng.WriteBatch(rows); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	got, ok := eng.Get("single_row")
	if !ok {
		t.Fatal("single_row not found")
	}
	if got.Columns[colVal].Int64 != 42 {
		t.Errorf("expected 42, got %d", got.Columns[colVal].Int64)
	}
	if got.Version != 1 {
		t.Errorf("expected version 1, got %d", got.Version)
	}
}

func TestEngineWriteBatchWALRecovery(t *testing.T) {
	dir := t.TempDir()

	// 创建引擎，批量写入数据，然后关闭
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("first NewEngine: %v", err)
	}

	rows := []WriteRow{
		{Key: "rkey1", Values: map[string]common.Value{colVal: common.NewInt64(100)}},
		{Key: "rkey2", Values: map[string]common.Value{colVal: common.NewInt64(200)}},
		{Key: "rkey3", Values: map[string]common.Value{colVal: common.NewInt64(300)}},
	}
	if err := eng.WriteBatch(rows); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("close first engine: %v", err)
	}

	// 重新打开引擎，验证数据从 WAL 恢复
	eng2, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("second NewEngine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	for _, r := range rows {
		got, ok := eng2.Get(r.Key)
		if !ok {
			t.Errorf("key %s not found after recovery", r.Key)
			continue
		}
		if got.Columns[colVal].Int64 != r.Values[colVal].Int64 {
			t.Errorf("key %s: expected %d, got %d",
				r.Key, r.Values[colVal].Int64, got.Columns[colVal].Int64)
		}
	}
}

func TestEngineWriteBatchAllTypes(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	rows := []WriteRow{
		{
			Key: "all_types_row",
			Values: map[string]common.Value{
				"bool_col": common.NewBool(true),
				"int":      common.NewInt64(-42),
				"float":    common.NewFloat64(3.14),
				"str":      common.NewString(testStrHello),
				"time":     common.NewTimestamp(ts),
			},
		},
	}

	if err := eng.WriteBatch(rows); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	got, ok := eng.Get("all_types_row")
	if !ok {
		t.Fatal("all_types_row not found")
	}
	if v := got.Columns["bool_col"]; !v.Valid || v.Int64 != 1 {
		t.Errorf("bool: expected true, got %v", v)
	}
	if v := got.Columns["int"]; v.Int64 != -42 {
		t.Errorf("int: expected -42, got %d", v.Int64)
	}
	if v := got.Columns["float"]; v.Float64 != 3.14 {
		t.Errorf("float: expected 3.14, got %f", v.Float64)
	}
	if v := got.Columns["str"]; v.Str != testStrHello {
		t.Errorf("str: expected hello, got %s", v.Str)
	}
	if v := got.Columns["time"]; !v.Time.Equal(ts) {
		t.Errorf("time: expected %v, got %v", ts, v.Time)
	}
}

func TestEngineWriteBatchAllTypesRecovery(t *testing.T) {
	dir := t.TempDir()

	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("first NewEngine: %v", err)
	}

	ts := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	rows := []WriteRow{
		{
			Key: "types_recovery_row",
			Values: map[string]common.Value{
				"b": common.NewBool(false),
				"i": common.NewInt64(99),
				"f": common.NewFloat64(2.718),
				"s": common.NewString("world"),
				"t": common.NewTimestamp(ts),
			},
		},
	}
	if err := eng.WriteBatch(rows); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	eng2, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("second NewEngine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	got, ok := eng2.Get("types_recovery_row")
	if !ok {
		t.Fatal("types_recovery_row not found after recovery")
	}
	if v := got.Columns["b"]; v.Int64 != 0 {
		t.Errorf("bool: expected false, got Int64=%d", v.Int64)
	}
	if v := got.Columns["i"]; v.Int64 != 99 {
		t.Errorf("int: expected 99, got %d", v.Int64)
	}
	if v := got.Columns["f"]; v.Float64 != 2.718 {
		t.Errorf("float: expected 2.718, got %f", v.Float64)
	}
	if v := got.Columns["s"]; v.Str != testStrWorld {
		t.Errorf("str: expected world, got %s", v.Str)
	}
	if v := got.Columns["t"]; !v.Time.Equal(ts) {
		t.Errorf("time: expected %v, got %v", ts, v.Time)
	}
}

func TestEngineWriteBatchWithNull(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	rows := []WriteRow{
		{
			Key: "null_row",
			Values: map[string]common.Value{
				colVal: common.NewInt64(1),
				"null": common.NewNull(),
			},
		},
	}

	if err := eng.WriteBatch(rows); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	got, ok := eng.Get("null_row")
	if !ok {
		t.Fatal("null_row not found")
	}
	if v := got.Columns["val"]; v.Int64 != 1 {
		t.Errorf("val: expected 1, got %d", v.Int64)
	}
	if v := got.Columns["null"]; v.Valid {
		t.Errorf("null: expected invalid, got valid=%v", v.Valid)
	}
}

func TestBatchWriteRecordBinaryRoundTrip(t *testing.T) {
	ts := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{"a": common.NewBool(true), "b": common.NewInt64(100)}},
		{Key: "k2", Values: map[string]common.Value{"c": common.NewFloat64(1.5), "d": common.NewString("test")}},
		{Key: "k3", Values: map[string]common.Value{"e": common.NewTimestamp(ts)}},
	}

	data, err := serializeBatchWriteRecord(rows, 10)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}

	result, err := deserializeBatchWriteRecord(data)
	if err != nil {
		t.Fatalf("deserialize: %v", err)
	}

	if len(result) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(result))
	}
	if result[0].Key != "k1" || result[0].Version != 10 {
		t.Errorf("row 0: key=%s version=%d", result[0].Key, result[0].Version)
	}
	if result[1].Key != "k2" || result[1].Version != 11 {
		t.Errorf("row 1: key=%s version=%d", result[1].Key, result[1].Version)
	}
	if result[2].Key != "k3" || result[2].Version != 12 {
		t.Errorf("row 2: key=%s version=%d", result[2].Key, result[2].Version)
	}
	if v := result[0].Values["a"]; v.Int64 != 1 {
		t.Errorf("row0.a: expected Int64=1, got %d", v.Int64)
	}
	if v := result[0].Values["b"]; v.Int64 != 100 {
		t.Errorf("row0.b: expected 100, got %d", v.Int64)
	}
	if v := result[1].Values["c"]; v.Float64 != 1.5 {
		t.Errorf("row1.c: expected 1.5, got %f", v.Float64)
	}
	if v := result[1].Values["d"]; v.Str != "test" {
		t.Errorf("row1.d: expected test, got %s", v.Str)
	}
	if v := result[2].Values["e"]; !v.Time.Equal(ts) {
		t.Errorf("row2.e: expected %v, got %v", ts, v.Time)
	}
}

// --- Merged from engine_batch_extended_test.go ---

func TestDeserializeBatchWriteRecordTruncated(t *testing.T) {
	// 空数据
	if _, err := deserializeBatchWriteRecord(nil); err == nil {
		t.Error("expected error for nil data")
	}
	// 只有行数头部，没有行数据
	if _, err := deserializeBatchWriteRecord([]byte{1, 0}); err == nil {
		t.Error("expected error for truncated data")
	}
	// 完全空
	if _, err := deserializeBatchWriteRecord([]byte{}); err == nil {
		t.Error("expected error for empty data")
	}
}

func TestEngineWriteBatchWALClosed(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	// Close the WAL to trigger errors on WriteBatch
	if err := eng.wal.Close(); err != nil {
		t.Fatalf("close WAL: %v", err)
	}

	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
	}
	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("expected error when WAL is closed, got nil")
	}
}

func TestEngineWriteBatchTriggersRotation(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir:         t.TempDir(),
		MaxMemTableSize: 1, // Set very small to trigger ShouldFlush quickly
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// Write enough rows to trigger memtable rotation
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("rot_key_%03d", i)
		rows := []WriteRow{
			{Key: key, Values: map[string]common.Value{colVal: common.NewInt64(int64(i))}},
		}
		if err := eng.WriteBatch(rows); err != nil {
			t.Fatalf("WriteBatch %d: %v", i, err)
		}
	}

	// Verify data is still readable after rotation
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("rot_key_%03d", i)
		got, ok := eng.Get(key)
		if !ok {
			t.Errorf("key %s not found after rotation", key)
			continue
		}
		if got.Columns[colVal].Int64 != int64(i) {
			t.Errorf("key %s: expected %d, got %d", key, i, got.Columns[colVal].Int64)
		}
	}
}

func TestEngineWriteBatchWALSyncError(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	// Close the WAL file to trigger sync error
	if err := eng.wal.Close(); err != nil {
		t.Fatalf("close WAL: %v", err)
	}

	rows := []WriteRow{
		{Key: "sync_key", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
	}
	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("expected error when WAL sync fails, got nil")
	}
}

func TestApplyBatchWriteRecordCorruptPayload(t *testing.T) {
	mem := NewMemTable()

	// Call applyBatchWriteRecord with invalid/corrupt payload bytes
	maxVer, ok := applyBatchWriteRecord([]byte{0xFF, 0xFF, 0xFF, 0xFF}, 0, mem)
	if ok {
		t.Error("expected ok=false for corrupt payload, got true")
	}
	if maxVer != 0 {
		t.Errorf("expected maxVer=0 for corrupt payload, got %d", maxVer)
	}
}

func TestApplyBatchWriteRecordSkipsOldVersions(t *testing.T) {
	mem := NewMemTable()

	// Serialize a batch write record with version starting at 1
	rows := []WriteRow{
		{Key: "old1", Values: map[string]common.Value{colVal: common.NewInt64(10)}},
		{Key: "old2", Values: map[string]common.Value{colVal: common.NewInt64(20)}},
	}
	data, err := serializeBatchWriteRecord(rows, 1)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}

	// Call applyBatchWriteRecord with lastFlushedVersion=100 (higher than all record versions)
	maxVer, ok := applyBatchWriteRecord(data, 100, mem)
	if !ok {
		t.Error("expected ok=true for valid payload, got false")
	}
	if maxVer != 0 {
		t.Errorf("expected maxVer=0 when all rows skipped, got %d", maxVer)
	}

	// Verify no rows were inserted into the memtable
	if _, found := mem.Get("old1"); found {
		t.Error("old1 should have been skipped")
	}
	if _, found := mem.Get("old2"); found {
		t.Error("old2 should have been skipped")
	}
}

func TestReadTypedValueTruncatedBool(t *testing.T) {
	_, n, err := readTypedValue([]byte{}, common.TypeBool)
	if err == nil {
		t.Error("expected error for truncated bool, got nil")
	}
	if n != 0 {
		t.Errorf("expected n=0, got %d", n)
	}
}

func TestReadTypedValueTruncatedInt64(t *testing.T) {
	_, n, err := readTypedValue([]byte{1, 2, 3, 4}, common.TypeInt64)
	if err == nil {
		t.Error("expected error for truncated int64, got nil")
	}
	if n != 0 {
		t.Errorf("expected n=0, got %d", n)
	}
}

func TestReadTypedValueTruncatedFloat64(t *testing.T) {
	_, n, err := readTypedValue([]byte{1, 2, 3, 4}, common.TypeFloat64)
	if err == nil {
		t.Error("expected error for truncated float64, got nil")
	}
	if n != 0 {
		t.Errorf("expected n=0, got %d", n)
	}
}

func TestReadTypedValueTruncatedStringLen(t *testing.T) {
	_, n, err := readTypedValue([]byte{1}, common.TypeString)
	if err == nil {
		t.Error("expected error for truncated string len, got nil")
	}
	if n != 0 {
		t.Errorf("expected n=0, got %d", n)
	}
}

func TestReadTypedValueTruncatedStringValue(t *testing.T) {
	// len=5 but only 3 bytes after the length field
	_, n, err := readTypedValue([]byte{5, 0, 'a', 'b', 'c'}, common.TypeString)
	if err == nil {
		t.Error("expected error for truncated string value, got nil")
	}
	if n != 0 {
		t.Errorf("expected n=0, got %d", n)
	}
}

func TestReadTypedValueTruncatedTimestamp(t *testing.T) {
	_, n, err := readTypedValue([]byte{1, 2, 3, 4}, common.TypeTimestamp)
	if err == nil {
		t.Error("expected error for truncated timestamp, got nil")
	}
	if n != 0 {
		t.Errorf("expected n=0, got %d", n)
	}
}

func TestReadValueBinaryTruncatedColNameLen(t *testing.T) {
	_, _, n, err := readValueBinary([]byte{})
	if err == nil {
		t.Error("expected error for empty data, got nil")
	}
	if n != 0 {
		t.Errorf("expected n=0, got %d", n)
	}
}

func TestReadValueBinaryTruncatedColName(t *testing.T) {
	// col name len = 5 but no name data
	_, _, n, err := readValueBinary([]byte{5, 0})
	if err == nil {
		t.Error("expected error for truncated col name, got nil")
	}
	if n != 0 {
		t.Errorf("expected n=0, got %d", n)
	}
}

func TestReadValueBinaryTruncatedTypeValid(t *testing.T) {
	// col name present but no type/valid bytes
	_, _, n, err := readValueBinary([]byte{1, 0, 'a'})
	if err == nil {
		t.Error("expected error for truncated type/valid, got nil")
	}
	if n != 0 {
		t.Errorf("expected n=0, got %d", n)
	}
}

func TestEngineRegisterSegmentIndexesNoBloomFilter(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// Write some data
	rows := []WriteRow{
		{Key: "bloom_key1", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
		{Key: "bloom_key2", Values: map[string]common.Value{colVal: common.NewInt64(2)}},
	}
	if err := eng.WriteBatch(rows); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	// Flush to create a segment
	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Manually remove bloom filter data from the segment to test the no-bloom path
	eng.mu.Lock()
	for _, seg := range eng.segments {
		seg.Footer.BloomFilter = nil
	}
	eng.mu.Unlock()

	// Re-register indexes without bloom filter - should not error
	eng.mu.Lock()
	for i, seg := range eng.segments {
		if err := eng.registerSegmentIndexes(seg, eng.segmentLevels[i]); err != nil {
			eng.mu.Unlock()
			t.Fatalf("registerSegmentIndexes without bloom: %v", err)
		}
	}
	eng.mu.Unlock()

	// Verify the engine still works (Get falls through to segment scan)
	got, ok := eng.Get("bloom_key1")
	if !ok {
		t.Error("bloom_key1 not found after re-registering indexes without bloom")
	}
	if got.Columns[colVal].Int64 != 1 {
		t.Errorf("bloom_key1: expected 1, got %d", got.Columns[colVal].Int64)
	}
}
