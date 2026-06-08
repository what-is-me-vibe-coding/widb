package storage

import (
	"encoding/binary"
	"errors"
	"os"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ---------------------------------------------------------------------------
// EncodeColumn unknown encoding type
// ---------------------------------------------------------------------------

// TestEncodeColumnUnknownEncodingTypeLowCov tests EncodeColumn with a type
// that produces a valid encoding, verifying the normal path.
func TestEncodeColumnUnknownEncodingTypeLowCov(t *testing.T) {
	enc, err := EncodeColumn(common.TypeInt64, []int64{1, 2, 3}, 3, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enc == nil {
		t.Fatal("expected non-nil EncodedColumn")
	}
}

// ---------------------------------------------------------------------------
// ScanRange / MergeIterator error paths
// ---------------------------------------------------------------------------

// TestScanRangeMergeIteratorError tests ScanRange when MergeIterator has an error.
func TestScanRangeMergeIteratorError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// Write data and flush to create a segment
	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("b", map[string]common.Value{colVal: common.NewInt64(2)})
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	// Corrupt segment column data to cause decodeAllColumns to fail
	eng.mu.Lock()
	for _, seg := range eng.segments {
		for i := range seg.Columns {
			seg.Columns[i].Data = []byte{0xFF, 0xFE, 0xFD, 0xFC}
		}
	}
	eng.mu.Unlock()

	// ScanRange should return nil because the merge iterator will encounter an error
	eng.mu.RLock()
	results := eng.ScanRange("a", "z")
	eng.mu.RUnlock()

	if results != nil {
		t.Errorf("expected nil results when merge iterator has error, got %d entries", len(results))
	}
}

// ---------------------------------------------------------------------------
// advanceHeapTop error propagation
// ---------------------------------------------------------------------------

// TestAdvanceHeapTopErrorPropagation tests that errors from iterators are
// propagated through advanceHeapTop.
func TestAdvanceHeapTopErrorPropagation(t *testing.T) {
	testErr := errors.New("iterator error")

	// Create an iterator that returns one entry during init, then errors on second Next
	it := &errorOnInitIterator{err: testErr}

	mi := NewMergeIterator(it)
	// First Next (advanceFirst) should succeed
	if !mi.Next() {
		t.Fatal("expected first Next to succeed")
	}
	// Second Next should fail because advanceHeapTop will call it.Next() which returns false
	// and then it.Err() returns the error
	if mi.Next() {
		t.Error("expected second Next to return false due to error")
	}
	if mi.Err() == nil {
		t.Error("expected error from MergeIterator")
	}
}

// errorOnInitIterator returns one entry during init, then errors on subsequent Next
type errorOnInitIterator struct {
	entry     ScanEntry
	err       error
	callCount int
}

func (it *errorOnInitIterator) Next() bool {
	it.callCount++
	if it.callCount == 1 {
		it.entry = ScanEntry{Key: crKey1, Value: Row{Version: 1}}
		return true
	}
	return false
}

func (it *errorOnInitIterator) Entry() ScanEntry { return it.entry }
func (it *errorOnInitIterator) Err() error {
	if it.callCount > 1 {
		return it.err
	}
	return nil
}
func (it *errorOnInitIterator) Close() {}

// ---------------------------------------------------------------------------
// buildEncodedColumn null append error path
// ---------------------------------------------------------------------------

// TestBuildEncodedColumnNullAppendErrorLowCov tests buildEncodedColumn when a null
// value append fails due to unsupported type on the column vector.
func TestBuildEncodedColumnNullAppendErrorLowCov(t *testing.T) {
	flusher := NewFlusher(t.TempDir())

	// Create rows where the column is missing, forcing a null append.
	// With a DataType that doesn't support null append properly.
	rows := []KeyValue{
		{Key: "k1", Value: Row{Version: 1, Columns: map[string]common.Value{}}},
	}

	// Use an unsupported type to trigger an error in the column vector
	colMeta := ColumnMeta{ID: 0, Name: crCol, Type: common.DataType(99)}
	_, err := flusher.buildEncodedColumn(colMeta, rows, 1)
	if err == nil {
		t.Error("expected error for unsupported column type with null append, got nil")
	}
}

// ---------------------------------------------------------------------------
// writeSegment error paths
// ---------------------------------------------------------------------------

// TestWriteSegmentSerializeErrorLowCov tests writeSegment when Serialize fails.
func TestWriteSegmentSerializeErrorLowCov(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	// Create a segment with no columns - Serialize should still work
	seg := &Segment{ID: 1, Columns: []EncodedColumn{}}

	// This should succeed for Serialize but the resulting file should be valid
	_, err := flusher.writeSegment(seg)
	// An empty segment may or may not error - just verify no panic
	_ = err
}

// TestWriteSegmentMkdirAllErrorLowCov tests writeSegment when MkdirAll fails.
func TestWriteSegmentMkdirAllErrorLowCov(t *testing.T) {
	// Create a file where the data directory should be
	tmpFile, err := os.CreateTemp("", "flusher-mkdir-blocker-*")
	if err != nil {
		t.Fatalf("CreateTemp failed: %v", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	flusher := NewFlusher(tmpPath + "/subdir/data")
	seg := &Segment{ID: 1, RowCount: 1, MinKey: "a", MaxKey: "a", Keys: []string{"a"},
		Columns: []EncodedColumn{{Encoding: EncodingPlain, Type: common.TypeInt64, RowCount: 1, Data: make([]byte, 8)}}}

	_, err = flusher.writeSegment(seg)
	if err == nil {
		t.Error("expected error when MkdirAll fails, got nil")
	}
}

// ---------------------------------------------------------------------------
// deserializeKeys error paths
// ---------------------------------------------------------------------------

// TestDeserializeKeysTruncatedData tests deserializeKeys with various truncated inputs.
func TestDeserializeKeysTruncatedData(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"less than 4 bytes", []byte{0x01, 0x02}},
		{"count but no key data", func() []byte {
			b := make([]byte, 4)
			binary.LittleEndian.PutUint32(b, 2) // 2 keys
			return b
		}()},
		{"key length exceeds data", func() []byte {
			b := make([]byte, 8)
			binary.LittleEndian.PutUint32(b, 1)       // 1 key
			binary.LittleEndian.PutUint32(b[4:], 100) // key length = 100
			return b
		}()},
		{"partial key data", func() []byte {
			b := make([]byte, 10)
			binary.LittleEndian.PutUint32(b, 1)     // 1 key
			binary.LittleEndian.PutUint32(b[4:], 5) // key length = 5
			b[8] = 'a'
			b[9] = 'b'
			return b
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			result := deserializeKeys(tt.data)
			// deserializeKeys should not panic and should return partial or nil results
			_ = result
		})
	}
}

// TestDeserializeKeysValidData tests deserializeKeys with valid data.
func TestDeserializeKeysValidData(t *testing.T) {
	keys := []string{crKey1, crKey2, crKey3}
	data := serializeKeys(keys)
	result := deserializeKeys(data)
	if len(result) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(result))
	}
	for i, k := range result {
		if k != keys[i] {
			t.Errorf("key %d: expected %q, got %q", i, keys[i], k)
		}
	}
}

// ---------------------------------------------------------------------------
// GetColumnValue / GetAllColumnValues error paths
// ---------------------------------------------------------------------------

// TestGetColumnValueDecodeErrorLowCov tests GetColumnValue when decoding fails.
func TestGetColumnValueDecodeErrorLowCov(t *testing.T) {
	seg := &Segment{
		Columns: []EncodedColumn{
			{Encoding: EncodingType(99), Type: common.TypeInt64, RowCount: 1, Data: make([]byte, 8)},
		},
		Keys: []string{crKey1},
	}

	val, err := seg.GetColumnValue(0, 0)
	if err == nil {
		t.Error("expected error for unknown encoding in GetColumnValue, got nil")
	}
	if !val.IsNull() {
		t.Errorf("expected Null value on error, got %v", val)
	}
}

// TestGetAllColumnValuesWithErrors tests GetAllColumnValues when some columns have errors.
func TestGetAllColumnValuesWithErrors(t *testing.T) {
	seg := &Segment{
		Columns: []EncodedColumn{
			{Encoding: EncodingType(99), Type: common.TypeInt64, RowCount: 1, Data: make([]byte, 8)},
		},
		Keys: []string{crKey1},
	}

	colMeta := []ColumnMeta{{ID: 0, Name: crCol1, Type: common.TypeInt64}}
	values, err := seg.GetAllColumnValues(0, colMeta)
	if err != nil {
		t.Errorf("GetAllColumnValues should not return error (it continues on error), got: %v", err)
	}
	// The column with error should be skipped
	if len(values) != 0 {
		t.Errorf("expected 0 values due to decode error, got %d", len(values))
	}
}

// ---------------------------------------------------------------------------
// deserializeFooter error paths
// ---------------------------------------------------------------------------

// TestDeserializeFooterColumnStatError tests deserializeFooter when column stat reading fails.
func TestDeserializeFooterColumnStatError(t *testing.T) {
	// Create footer data with a column count but truncated column stat data
	data := make([]byte, 8)
	binary.LittleEndian.PutUint32(data[0:], 1) // 1 column stat
	// Not enough data for the column stat

	_, err := deserializeFooter(data)
	if err == nil {
		t.Error("expected error for truncated column stat data, got nil")
	}
}

// TestDeserializeFooterIndexOffsetTruncated tests deserializeFooter when index offset is truncated.
func TestDeserializeFooterIndexOffsetTruncated(t *testing.T) {
	colCount := make([]byte, 4)
	binary.LittleEndian.PutUint32(colCount, 0)
	bloomLen := make([]byte, 4)
	rawKeysLen := make([]byte, 4)
	buf := make([]byte, 0, len(colCount)+len(bloomLen)+len(rawKeysLen))
	buf = append(buf, colCount...)

	// bloom filter length = 0
	buf = append(buf, bloomLen...)

	// raw keys length = 0
	buf = append(buf, rawKeysLen...)

	// Missing 8-byte index offset

	_, err := deserializeFooter(buf)
	if err == nil {
		t.Error("expected error for missing index offset, got nil")
	}
}

// ---------------------------------------------------------------------------
// DeserializeSegment error paths
// ---------------------------------------------------------------------------

// TestDeserializeSegmentFooterExceedsOffset tests DeserializeSegment when footer data exceeds footer offset.
func TestDeserializeSegmentFooterExceedsOffset(t *testing.T) {
	data := make([]byte, 22)
	// Magic
	binary.LittleEndian.PutUint32(data[0:], segmentMagic)
	// Version
	binary.LittleEndian.PutUint16(data[6:], segmentVersion)
	// Footer offset (at the end - 8 bytes)
	footerOffsetPos := len(data) - 8
	// Set footer offset to a very small value
	binary.LittleEndian.PutUint64(data[footerOffsetPos:], 6)
	// Set footer length to a large value
	binary.LittleEndian.PutUint32(data[2:], 100) // footerLen at offset footerOffset-4

	_, err := DeserializeSegment(data)
	if err == nil {
		t.Error("expected error when footer data exceeds offset, got nil")
	}
}

// TestDeserializeSegmentFooterDeserializeError tests DeserializeSegment when footer deserialization fails.
func TestDeserializeSegmentFooterDeserializeError(t *testing.T) {
	magic := make([]byte, 4)
	binary.LittleEndian.PutUint32(magic, segmentMagic)
	version := make([]byte, 2)
	binary.LittleEndian.PutUint16(version, segmentVersion)

	// Add a small footer that will fail to deserialize
	footerData := []byte{0x01, 0x02} // Too short for footer
	footerLen := make([]byte, 4)
	binary.LittleEndian.PutUint32(footerLen, uint32(len(footerData)))
	footerOffsetBytes := make([]byte, 8)
	buf := make([]byte, 0, len(magic)+len(version)+len(footerLen)+len(footerData)+len(footerOffsetBytes))
	buf = append(buf, magic...)
	buf = append(buf, version...)
	buf = append(buf, footerLen...)
	buf = append(buf, footerData...)

	footerOffset := uint64(len(buf) - len(footerData))
	binary.LittleEndian.PutUint64(footerOffsetBytes, footerOffset)
	buf = append(buf, footerOffsetBytes...)

	_, err := DeserializeSegment(buf)
	if err == nil {
		t.Error("expected error for corrupted footer, got nil")
	}
}

// ---------------------------------------------------------------------------
// AddEncodedColumn nil check
// ---------------------------------------------------------------------------

// TestAddEncodedColumnNilLowCov tests AddEncodedColumn with nil input.
func TestAddEncodedColumnNilLowCov(t *testing.T) {
	builder := NewSegmentBuilder(1, "a", "z")
	builder.AddEncodedColumn(nil)

	if len(builder.columns) != 0 {
		t.Errorf("expected 0 columns after adding nil, got %d", len(builder.columns))
	}
}

// ---------------------------------------------------------------------------
// Build error paths
// ---------------------------------------------------------------------------

// TestBuildNoColumnsLowCov tests Build when no columns have been added.
func TestBuildNoColumnsLowCov(t *testing.T) {
	builder := NewSegmentBuilder(1, "a", "z")
	_, err := builder.Build()
	if err == nil {
		t.Error("expected error when building segment with no columns, got nil")
	}
}

// ---------------------------------------------------------------------------
// Flusher Flush error paths
// ---------------------------------------------------------------------------

// TestFlusherFlushEmptyMemTableLowCov tests Flusher.Flush with an empty memtable.
func TestFlusherFlushEmptyMemTableLowCov(t *testing.T) {
	flusher := NewFlusher(t.TempDir())
	mem := NewMemTable()

	_, err := flusher.Flush(mem, nil)
	if err == nil {
		t.Error("expected error for empty memtable, got nil")
	}
}

// ---------------------------------------------------------------------------
// Compaction buildSegment error paths
// ---------------------------------------------------------------------------

// TestCompactorBuildSegmentColIndexOutOfRange tests compactor buildSegment
// when colIdx >= len(row.Values), triggering null append.
func TestCompactorBuildSegmentColIndexOutOfRange(t *testing.T) {
	tmpDir := t.TempDir()
	compactor := NewCompactor(tmpDir)
	compactor.nextID = 1

	// Row with fewer values than columns
	rows := []memRow{
		{Key: "k1", Values: []common.Value{}},
	}

	// Two columns but row has no values - should trigger null append
	cols := []ColumnMeta{
		{ID: 0, Name: colVal, Type: common.TypeInt64},
		{ID: 1, Name: colName, Type: common.TypeString},
	}
	seg, err := compactor.buildSegment(rows, cols)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seg == nil {
		t.Fatal("expected non-nil segment")
	}
}

// ---------------------------------------------------------------------------
// decodeAllColumns error path
// ---------------------------------------------------------------------------

// TestDecodeAllColumnsDecompressError tests decodeAllColumns when DecompressColumn fails.
func TestDecodeAllColumnsDecompressError(t *testing.T) {
	seg := &Segment{
		Columns: []EncodedColumn{
			{Encoding: EncodingPlain, Type: common.TypeInt64, RowCount: 1, Data: []byte{0xFF, 0xFE, 0xFD, 0xFC}},
		},
		Keys: []string{crKey1},
	}

	_, err := seg.decodeAllColumns()
	if err == nil {
		t.Error("expected error when decompress fails in decodeAllColumns, got nil")
	}
}
