package storage

import (
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
				"str":      common.NewString("hello"),
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
	if v := got.Columns["str"]; v.Str != "hello" {
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
	if v := got.Columns["s"]; v.Str != "world" {
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
				"val":  common.NewInt64(1),
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
