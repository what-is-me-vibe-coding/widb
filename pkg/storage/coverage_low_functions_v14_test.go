package storage

import (
	"encoding/binary"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// --- compress.go: getEncoder/getDecoder pool ---

func TestV14EncoderPoolRoundTrip(t *testing.T) {
	enc, err := getEncoder()
	if err != nil {
		t.Fatalf("getEncoder: %v", err)
	}
	putEncoder(enc)

	enc2, err := getEncoder()
	if err != nil {
		t.Fatalf("getEncoder second: %v", err)
	}
	putEncoder(enc2)
}

func TestV14DecoderPoolRoundTrip(t *testing.T) {
	dec, err := getDecoder()
	if err != nil {
		t.Fatalf("getDecoder: %v", err)
	}
	putDecoder(dec)

	dec2, err := getDecoder()
	if err != nil {
		t.Fatalf("getDecoder second: %v", err)
	}
	putDecoder(dec2)
}

func TestV14CompressColumnValid(t *testing.T) {
	ints := []int64{1, 2, 3, 4, 5}
	enc, err := EncodeColumn(common.TypeInt64, ints, 5, nil)
	if err != nil {
		t.Fatalf("EncodeColumn: %v", err)
	}
	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn: %v", err)
	}
	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn: %v", err)
	}
	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn: %v", err)
	}
	result := decoded.([]int64)
	for i, v := range ints {
		if result[i] != v {
			t.Errorf("row %d: got %d, want %d", i, result[i], v)
		}
	}
}

// --- encoding.go: EncodeColumn with various encodings ---

func TestV14EncodeColumnPlainInt64(t *testing.T) {
	data := []int64{10, 20, 30}
	enc, err := EncodeColumn(common.TypeInt64, data, 3, nil)
	if err != nil {
		t.Fatalf("EncodeColumn plain int64: %v", err)
	}
	if enc.Encoding != EncodingPlain {
		t.Errorf("expected Plain encoding, got %v", enc.Encoding)
	}
}

func TestV14EncodeColumnDictString(t *testing.T) {
	data := []string{"a", "b", "a", "c"}
	enc, err := EncodeColumn(common.TypeString, data, 4, nil)
	if err != nil {
		t.Fatalf("EncodeColumn dict string: %v", err)
	}
	if enc.Encoding != EncodingDict {
		t.Errorf("expected Dict encoding, got %v", enc.Encoding)
	}
}

func TestV14EncodeColumnRLEInt64(t *testing.T) {
	data := make([]int64, 200)
	for i := range data {
		data[i] = int64(i / 100)
	}
	enc, err := EncodeColumn(common.TypeInt64, data, 200, nil)
	if err != nil {
		t.Fatalf("EncodeColumn RLE int64: %v", err)
	}
	if enc.Encoding != EncodingRLE {
		t.Errorf("expected RLE encoding, got %v", enc.Encoding)
	}
}

func TestV14EncodeColumnBitmapBool(t *testing.T) {
	data := []uint64{1, 0, 1, 1, 0}
	enc, err := EncodeColumn(common.TypeBool, data, 5, nil)
	if err != nil {
		t.Fatalf("EncodeColumn bitmap bool: %v", err)
	}
	if enc.Encoding != EncodingBitmap {
		t.Errorf("expected Bitmap encoding, got %v", enc.Encoding)
	}
}

func TestV14EncodeColumnInvalidEncoding(t *testing.T) {
	enc := &EncodedColumn{Encoding: EncodingType(99), Type: common.TypeInt64, RowCount: 1, Data: make([]byte, 8)}
	_, _, err := DecodeColumn(enc)
	if err == nil {
		t.Error("expected error for invalid encoding type")
	}
}

// --- iterator.go: decodeSegmentColumn ---

func TestV14DecodeSegmentColumnBlockCacheHit(t *testing.T) {
	seg := buildTestSegment(t, []string{"a", "b", "c"}, []int64{1, 2, 3})
	colMeta := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	bc := NewBlockCache(1024 * 1024)

	it := newSegmentIterator(seg, colMeta, "a", "c", bc)
	if !it.Next() {
		t.Fatal("expected at least one entry")
	}
	if it.Err() != nil {
		t.Fatalf("unexpected error: %v", it.Err())
	}

	// Second iterator on same segment should hit cache
	it2 := newSegmentIterator(seg, colMeta, "a", "c", bc)
	if !it2.Next() {
		t.Fatal("expected at least one entry from cache")
	}
	if it2.Err() != nil {
		t.Fatalf("unexpected error from cache: %v", it2.Err())
	}

	stats := bc.Stats()
	if stats.Hits == 0 {
		t.Error("expected cache hits on second iterator")
	}
}

func TestV14DecodeSegmentColumnCorruptedData(t *testing.T) {
	seg := buildTestSegment(t, []string{"a", "b"}, []int64{1, 2})
	// Corrupt the compressed column data
	for i := range seg.Columns[0].Data {
		seg.Columns[0].Data[i] = 0xFF
	}
	colMeta := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	bc := NewBlockCache(1024 * 1024)

	it := newSegmentIterator(seg, colMeta, "a", "b", bc)
	if it.Next() {
		t.Error("expected no entries due to corrupted data")
	}
	if it.Err() == nil {
		t.Error("expected error from corrupted column data")
	}
}

func TestV14DecodeSegmentColumnInvalidEncoding(t *testing.T) {
	seg := buildTestSegment(t, []string{"a", "b"}, []int64{1, 2})
	// Set invalid encoding type after segment is built (compressed)
	seg.Columns[0].Encoding = EncodingType(99)
	colMeta := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	bc := NewBlockCache(1024 * 1024)

	it := newSegmentIterator(seg, colMeta, "a", "b", bc)
	if it.Next() {
		t.Error("expected no entries due to invalid encoding")
	}
	if it.Err() == nil {
		t.Error("expected error from invalid encoding type")
	}
}

// --- segment_serialize.go: DeserializeSegment ---

func TestV14DeserializeSegmentInvalidFooterOffset(t *testing.T) {
	seg := buildTestSegment(t, []string{"a", "b"}, []int64{1, 2})
	data, err := seg.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	// Set footer offset to point past the footer offset field but before the actual footer,
	// causing footer data to exceed footer offset.
	footerOffPos := len(data) - 8
	// Set footer offset to a small value within the data range but causing footerEnd > footerOffPos
	binary.LittleEndian.PutUint64(data[footerOffPos:], 8)

	_, err = DeserializeSegment(data)
	if err == nil {
		t.Error("expected error for invalid footer offset")
	}
}

func TestV14DeserializeSegmentCorruptedFooter(t *testing.T) {
	seg := buildTestSegment(t, []string{"a", "b"}, []int64{1, 2})
	data, err := seg.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	// Corrupt footer by setting column count to a value larger than what the footer data can hold
	footerOffPos := len(data) - 8
	footerOffset := binary.LittleEndian.Uint64(data[footerOffPos:])
	footerStart := int(footerOffset)
	// Set colCount to 1000, which will require reading far beyond available footer data
	if footerStart+4 <= len(data) {
		binary.LittleEndian.PutUint32(data[footerStart:], 1000)
	}

	_, err = DeserializeSegment(data)
	if err == nil {
		t.Error("expected error for corrupted footer")
	}
}

// --- flusher.go: writeSegment with invalid dir ---

func TestV14WriteSegmentInvalidDir(t *testing.T) {
	f := NewFlusher("/dev/null/invalid/path", newSegmentIDGen())
	seg := buildTestSegment(t, []string{"a"}, []int64{1})
	_, err := writeSegmentFile(f.dataDir, seg)
	if err == nil {
		t.Error("expected error for invalid data dir")
	}
}

// --- compaction.go: decodeSegmentColumn error ---

func TestV14CompactionDecodeSegmentColumnError(t *testing.T) {
	// Build a segment with corrupted column data
	seg := buildTestSegment(t, []string{"a", "b"}, []int64{1, 2})
	for i := range seg.Columns[0].Data {
		seg.Columns[0].Data[i] = 0xFF
	}

	_, err := decodeSegmentColumn(&seg.Columns[0], 0)
	if err == nil {
		t.Error("expected error from corrupted column in decodeSegmentColumn")
	}
}

// --- engine.go: Write error paths ---

func TestV14WriteRotateMemTableError(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir:         t.TempDir(),
		MaxMemTableSize: 1,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// Close WAL to cause rotateMemTable's flush to fail
	_ = eng.wal.Close()

	err = eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)})
	if err == nil {
		t.Error("expected error when Write triggers rotate with closed WAL")
	}
}

// --- engine_batch.go: WriteBatch error paths ---

func TestV14WriteBatchEmpty(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	err = eng.WriteBatch(nil)
	if err != nil {
		t.Errorf("WriteBatch(nil) should return nil, got: %v", err)
	}
}

func TestV14WriteBatchAppendError(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	_ = eng.wal.Close()

	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
	}
	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("expected error when WriteBatch with closed WAL (append)")
	}
}

func TestV14WriteBatchSyncError(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
	}

	// Close WAL after append but before sync by corrupting the file
	_ = eng.wal.Close()

	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("expected error when WriteBatch with closed WAL (sync)")
	}
}

func TestV14WriteBatchRotateError(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir:         t.TempDir(),
		MaxMemTableSize: 1,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	// Close WAL so rotate will fail
	_ = eng.wal.Close()

	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
	}
	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("expected error when WriteBatch triggers rotate with closed WAL")
	}
}

// --- encoding.go: EncodeColumn with plain types ---

func TestV14EncodeColumnPlainFloat64(t *testing.T) {
	data := []float64{1.1, 2.2, 3.3}
	enc, err := EncodeColumn(common.TypeFloat64, data, 3, nil)
	if err != nil {
		t.Fatalf("EncodeColumn plain float64: %v", err)
	}
	if enc.Encoding != EncodingPlain {
		t.Errorf("expected Plain, got %v", enc.Encoding)
	}
}

func TestV14EncodeColumnPlainTimestamp(t *testing.T) {
	data := []int64{1000000, 2000000, 3000000}
	enc, err := EncodeColumn(common.TypeTimestamp, data, 3, nil)
	if err != nil {
		t.Fatalf("EncodeColumn plain timestamp: %v", err)
	}
	if enc.Encoding != EncodingPlain {
		t.Errorf("expected Plain, got %v", enc.Encoding)
	}
}

func TestV14EncodeColumnWithNulls(t *testing.T) {
	ints := []int64{10, 20, 30}
	nulls := common.NewBitmap(3)
	nulls.Set(1)
	enc, err := EncodeColumn(common.TypeInt64, ints, 3, nulls)
	if err != nil {
		t.Fatalf("EncodeColumn with nulls: %v", err)
	}
	if len(enc.Nulls) == 0 {
		t.Error("expected nulls data in encoded column")
	}
}

// --- flusher.go: buildEncodedColumn with various types ---

func TestV14BuildEncodedColumnFloat64(t *testing.T) {
	f := NewFlusher(t.TempDir(), newSegmentIDGen())
	rows := []KeyValue{
		{Key: "a", Value: Row{Columns: map[string]common.Value{colVal: common.NewFloat64(1.5)}}},
		{Key: "b", Value: Row{Columns: map[string]common.Value{colVal: common.NewFloat64(2.5)}}},
	}
	colMeta := ColumnMeta{ID: 0, Name: colVal, Type: common.TypeFloat64}
	enc, err := f.buildEncodedColumn(colMeta, rows, 2)
	if err != nil {
		t.Fatalf("buildEncodedColumn float64: %v", err)
	}
	if enc.Type != common.TypeFloat64 {
		t.Errorf("expected Float64 type, got %v", enc.Type)
	}
}

func TestV14BuildEncodedColumnBool(t *testing.T) {
	f := NewFlusher(t.TempDir(), newSegmentIDGen())
	rows := []KeyValue{
		{Key: "a", Value: Row{Columns: map[string]common.Value{colVal: common.NewBool(true)}}},
		{Key: "b", Value: Row{Columns: map[string]common.Value{colVal: common.NewBool(false)}}},
	}
	colMeta := ColumnMeta{ID: 0, Name: colVal, Type: common.TypeBool}
	enc, err := f.buildEncodedColumn(colMeta, rows, 2)
	if err != nil {
		t.Fatalf("buildEncodedColumn bool: %v", err)
	}
	if enc.Type != common.TypeBool {
		t.Errorf("expected Bool type, got %v", enc.Type)
	}
}

func TestV14BuildEncodedColumnString(t *testing.T) {
	f := NewFlusher(t.TempDir(), newSegmentIDGen())
	rows := []KeyValue{
		{Key: "a", Value: Row{Columns: map[string]common.Value{colVal: common.NewString("hello")}}},
		{Key: "b", Value: Row{Columns: map[string]common.Value{colVal: common.NewString("world")}}},
	}
	colMeta := ColumnMeta{ID: 0, Name: colVal, Type: common.TypeString}
	enc, err := f.buildEncodedColumn(colMeta, rows, 2)
	if err != nil {
		t.Fatalf("buildEncodedColumn string: %v", err)
	}
	if enc.Type != common.TypeString {
		t.Errorf("expected String type, got %v", enc.Type)
	}
}

func TestV14BuildEncodedColumnWithNull(t *testing.T) {
	f := NewFlusher(t.TempDir(), newSegmentIDGen())
	rows := []KeyValue{
		{Key: "a", Value: Row{Columns: map[string]common.Value{colVal: common.NewInt64(1)}}},
		{Key: "b", Value: Row{Columns: map[string]common.Value{}}}, // missing col -> null
	}
	colMeta := ColumnMeta{ID: 0, Name: colVal, Type: common.TypeInt64}
	_, err := f.buildEncodedColumn(colMeta, rows, 2)
	if err != nil {
		t.Fatalf("buildEncodedColumn with null: %v", err)
	}
}

// --- compaction.go: Compact with segments ---

func TestV14CompactDecodeError(t *testing.T) {
	dir := t.TempDir()
	compactor := NewCompactor(dir, newSegmentIDGen())

	seg := buildTestSegment(t, []string{"a", "b"}, []int64{1, 2})
	// Corrupt column data
	for i := range seg.Columns[0].Data {
		seg.Columns[0].Data[i] = 0xFF
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	_, err := compactor.Compact([]*Segment{seg}, cols)
	if err == nil {
		t.Error("expected error from Compact with corrupted segment")
	}
}

// --- engine.go: NewEngine error paths ---

func TestV14NewEngineInvalidDataDir(t *testing.T) {
	_, err := NewEngine(EngineConfig{DataDir: "/dev/null/impossible"})
	if err == nil {
		t.Error("expected error for invalid data dir")
	}
}
