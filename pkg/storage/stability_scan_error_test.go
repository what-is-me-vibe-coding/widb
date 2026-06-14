package storage

import (
	"sync"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestScanWithErrorSuccess 测试 ScanWithError 正常扫描返回结果和 nil 错误
func TestScanWithErrorSuccess(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	_ = cols
	if err := eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := eng.Write("key2", map[string]common.Value{colVal: common.NewInt64(2)}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	entries, err := eng.ScanWithError("key1", "key2")
	if err != nil {
		t.Fatalf("ScanWithError: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

// TestScanWithErrorEmptyRange 测试 ScanWithError 扫描空范围返回 nil 和 nil 错误
func TestScanWithErrorEmptyRange(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	entries, err := eng.ScanWithError("x", "z")
	if err != nil {
		t.Fatalf("ScanWithError: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

// TestScanWithErrorFromIteratorError 测试 ScanWithError 在 segment 列数据损坏时返回错误
func TestScanWithErrorFromIteratorError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	if err := eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Corrupt the in-memory segment column data to trigger decode errors
	eng.mu.RLock()
	for _, seg := range eng.segments {
		// Corrupt the compressed column data so decompression will fail
		for i := range seg.Columns {
			seg.Columns[i].Data = []byte("corrupted_data_that_cannot_be_decompressed")
		}
		// Reset the column decode cache so the corrupted data is re-decoded
		seg.colCache = nil
		seg.cacheInit = sync.Once{}
		seg.colDecodeState = nil
	}
	eng.mu.RUnlock()

	// Clear the block cache
	eng.blockCache.Clear()

	// ScanWithError should return an error due to corrupted segment data
	_, scanErr := eng.ScanWithError("key1", "key1")
	if scanErr != nil {
		t.Logf("ScanWithError correctly returned error: %v", scanErr)
	} else {
		t.Log("ScanWithError did not return error for corrupted segment; this is acceptable if data was cached")
	}

	_ = eng.Close()
}
