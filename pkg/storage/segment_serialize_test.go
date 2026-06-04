package storage

import (
	"encoding/binary"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestSegmentColumnBlockRoundTrip(t *testing.T) {
	rowCount := uint32(10)
	ints := make([]int64, rowCount)
	for i := uint32(0); i < rowCount; i++ {
		ints[i] = int64(i)
	}

	enc, err := EncodeColumn(common.TypeInt64, ints, rowCount, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	serialized := SerializeColumnBlock(enc)
	restored, err := DeserializeColumnBlock(serialized)
	if err != nil {
		t.Fatalf("deserialize column block: %v", err)
	}

	if restored.Encoding != enc.Encoding {
		t.Errorf("encoding: got %v, want %v", restored.Encoding, enc.Encoding)
	}
	if restored.Type != enc.Type {
		t.Errorf("type: got %v, want %v", restored.Type, enc.Type)
	}
	if restored.RowCount != enc.RowCount {
		t.Errorf("rowCount: got %d, want %d", restored.RowCount, enc.RowCount)
	}

	decoded, _, err := DecodeColumn(restored)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	decodedInts, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("decoded type: got %T, want []int64", decoded)
	}
	for i := uint32(0); i < rowCount; i++ {
		if decodedInts[i] != ints[i] {
			t.Errorf("row %d: got %d, want %d", i, decodedInts[i], ints[i])
		}
	}
}

func TestDeserializeColumnBlockTooShort(t *testing.T) {
	_, err := DeserializeColumnBlock([]byte{})
	if err == nil {
		t.Error("expected error for empty data")
	}
}

func TestSegmentFooterRoundTrip(t *testing.T) {
	footer := &SegmentFooter{
		ColumnStats: []ColumnStat{
			{ColumnID: 0, Min: int64ToBytes(1), Max: int64ToBytes(100), NullCount: 5},
			{ColumnID: 1, Min: []byte("abc"), Max: []byte("xyz"), NullCount: 3},
		},
		BloomFilter: []byte{0x01, 0x02, 0x03},
		IndexOffset: 12345,
	}

	serialized := serializeFooter(footer)
	restored, err := deserializeFooter(serialized)
	if err != nil {
		t.Fatalf("deserialize footer: %v", err)
	}

	if len(restored.ColumnStats) != 2 {
		t.Fatalf("ColumnStats count: got %d, want 2", len(restored.ColumnStats))
	}
	if restored.ColumnStats[0].ColumnID != 0 {
		t.Errorf("ColumnStats[0].ColumnID: got %d, want 0", restored.ColumnStats[0].ColumnID)
	}
	if restored.ColumnStats[0].NullCount != 5 {
		t.Errorf("ColumnStats[0].NullCount: got %d, want 5", restored.ColumnStats[0].NullCount)
	}
	if restored.ColumnStats[1].ColumnID != 1 {
		t.Errorf("ColumnStats[1].ColumnID: got %d, want 1", restored.ColumnStats[1].ColumnID)
	}
	if string(restored.ColumnStats[1].Min) != "abc" {
		t.Errorf("ColumnStats[1].Min: got %q, want %q", string(restored.ColumnStats[1].Min), "abc")
	}
	if string(restored.ColumnStats[1].Max) != "xyz" {
		t.Errorf("ColumnStats[1].Max: got %q, want %q", string(restored.ColumnStats[1].Max), "xyz")
	}
	if len(restored.BloomFilter) != 3 {
		t.Errorf("BloomFilter length: got %d, want 3", len(restored.BloomFilter))
	}
	if restored.IndexOffset != 12345 {
		t.Errorf("IndexOffset: got %d, want 12345", restored.IndexOffset)
	}
}

func TestReadOffsetsTruncatedData(t *testing.T) {
	// Test readOffsets with data that's too short for the declared offsets count
	enc := &EncodedColumn{}

	// Create data with offsets length = 2 but not enough bytes for 2 uint32s
	// Need: 4 bytes for nullsLen + 4 bytes for dataLen + 4 bytes for offsetsLen = 12 bytes header
	// Then need offsetsLen * 4 = 8 bytes for offsets data
	// Total needed from pos=8: 4 bytes (offsetsLen) + 8 bytes (offsets data) = 12 bytes
	// So data needs at least 8 + 12 = 20 bytes, but we make it shorter
	data := make([]byte, 14) // Only 6 bytes after pos=8, not enough for offsetsLen(4) + 2 offsets(8)

	// Manually construct: nullsLen=0, dataLen=0, offsetsLen=2, but truncated
	binary.LittleEndian.PutUint32(data[0:], 0) // nullsLen
	binary.LittleEndian.PutUint32(data[4:], 0) // dataLen
	binary.LittleEndian.PutUint32(data[8:], 2) // offsetsLen = 2
	// Only 2 bytes left but need 8 for 2 offsets

	_, err := readOffsets(data, 8, enc)
	if err == nil {
		t.Error("expected error for truncated offsets data")
	}
}

func TestReadOffsetsWithValidData(t *testing.T) {
	enc := &EncodedColumn{}

	// Build valid data with offsets
	offsets := []uint32{0, 5, 10}
	data := make([]byte, 24) // enough space

	pos := 0
	binary.LittleEndian.PutUint32(data[pos:], 0) // nullsLen
	pos += 4
	binary.LittleEndian.PutUint32(data[pos:], 0) // dataLen
	pos += 4
	binary.LittleEndian.PutUint32(data[pos:], uint32(len(offsets))) // offsetsLen = 3
	pos += 4
	for _, off := range offsets {
		binary.LittleEndian.PutUint32(data[pos:], off)
		pos += 4
	}

	newPos, err := readOffsets(data, 8, enc)
	if err != nil {
		t.Fatalf("readOffsets: %v", err)
	}
	if len(enc.Offsets) != 3 {
		t.Fatalf("expected 3 offsets, got %d", len(enc.Offsets))
	}
	for i, off := range offsets {
		if enc.Offsets[i] != off {
			t.Errorf("offset[%d]: got %d, want %d", i, enc.Offsets[i], off)
		}
	}
	_ = newPos
}

func TestReadOffsetsLengthFieldExceedsBuffer(t *testing.T) {
	enc := &EncodedColumn{}
	// Data too short to even read the offsets length field
	data := make([]byte, 3)
	_, err := readOffsets(data, 0, enc)
	if err == nil {
		t.Error("expected error when offsets length field exceeds buffer")
	}
}

func TestReadColumnDataTruncated(t *testing.T) {
	enc := &EncodedColumn{}
	// Data too short to read data length field
	data := make([]byte, 3)
	_, err := readColumnData(data, 0, enc)
	if err == nil {
		t.Error("expected error when data length field exceeds buffer")
	}
}

func TestReadColumnDataPayloadExceedsBuffer(t *testing.T) {
	enc := &EncodedColumn{}
	data := make([]byte, 8)
	// Write dataLen = 100 but only 4 bytes available
	binary.LittleEndian.PutUint32(data[0:], 100) // dataLen
	_, err := readColumnData(data, 0, enc)
	if err == nil {
		t.Error("expected error when column data payload exceeds buffer")
	}
}

func TestReadNullsPayloadExceedsBuffer(t *testing.T) {
	enc := &EncodedColumn{}
	data := make([]byte, 8)
	binary.LittleEndian.PutUint32(data[0:], 100) // nullsLen
	_, err := readNulls(data, 0, enc)
	if err == nil {
		t.Error("expected error when nulls data exceeds buffer")
	}
}

func TestReadDictTruncated(t *testing.T) {
	enc := &EncodedColumn{}
	// Data too short to read dict length field
	data := make([]byte, 3)
	_, err := readDict(data, 0, enc)
	if err == nil {
		t.Error("expected error when dict length field exceeds buffer")
	}
}

func TestDeserializeFooterTooShort(t *testing.T) {
	data := make([]byte, 3)
	_, err := deserializeFooter(data)
	if err == nil {
		t.Error("expected error for too-short footer data")
	}
}

func TestDeserializeFooterTruncatedColumnStat(t *testing.T) {
	// Footer with 1 column stat but not enough data
	data := make([]byte, 8)
	binary.LittleEndian.PutUint32(data[0:], 1) // colCount = 1
	_, err := deserializeFooter(data)
	if err == nil {
		t.Error("expected error for truncated column stat in footer")
	}
}
