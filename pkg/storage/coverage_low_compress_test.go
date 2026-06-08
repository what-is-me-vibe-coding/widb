package storage

import (
	"encoding/binary"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const crCol1 = "col1"
const crCol0 = "col0"
const crCol = "col"

// ---------------------------------------------------------------------------
// Compress / Decompress edge cases
// ---------------------------------------------------------------------------

// TestCompressEmptyDataLowCov verifies that Compress returns nil,nil for empty input.
func TestCompressEmptyDataLowCov(t *testing.T) {
	result, err := Compress([]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for empty data, got %d bytes", len(result))
	}
}

// TestDecompressEmptyDataLowCov verifies that Decompress returns nil,nil for empty input.
func TestDecompressEmptyDataLowCov(t *testing.T) {
	result, err := Decompress([]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for empty data, got %d bytes", len(result))
	}
}

// TestCompressColumnNilLowCov verifies CompressColumn returns error for nil EncodedColumn.
func TestCompressColumnNilLowCov(t *testing.T) {
	err := CompressColumn(nil)
	if err == nil {
		t.Fatal("expected error for nil EncodedColumn, got nil")
	}
}

// TestDecompressColumnNilLowCov verifies DecompressColumn returns error for nil EncodedColumn.
func TestDecompressColumnNilLowCov(t *testing.T) {
	err := DecompressColumn(nil)
	if err == nil {
		t.Fatal("expected error for nil EncodedColumn, got nil")
	}
}

// TestCompressDecompressRoundTripLowCov verifies compress/decompress round-trip.
func TestCompressDecompressRoundTripLowCov(t *testing.T) {
	original := []byte("hello world, this is a test of zstd compression")
	compressed, err := Compress(original)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}
	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if string(decompressed) != string(original) {
		t.Errorf("round-trip mismatch: got %q, want %q", string(decompressed), string(original))
	}
}

// ---------------------------------------------------------------------------
// ColumnVector edge cases
// ---------------------------------------------------------------------------

// TestColumnVectorAppendNullLowCov tests appending a null value to a column vector.
func TestColumnVectorAppendNullLowCov(t *testing.T) {
	cv := NewColumnVector(0, common.TypeInt64, 4)
	if err := cv.Append(common.NewNull()); err != nil {
		t.Fatalf("Append null failed: %v", err)
	}
	if cv.Len() != 1 {
		t.Errorf("expected len 1, got %d", cv.Len())
	}
	if !cv.IsNull(0) {
		t.Error("expected row 0 to be null")
	}
}

// TestColumnVectorGrowLowCov tests that ColumnVector grows correctly.
func TestColumnVectorGrowLowCov(t *testing.T) {
	cv := NewColumnVector(0, common.TypeInt64, 2)
	originalCap := cv.Capacity()
	for i := uint32(0); i < originalCap+1; i++ {
		if err := cv.Append(common.NewInt64(int64(i))); err != nil {
			t.Fatalf("Append %d failed: %v", i, err)
		}
	}
	if cv.Capacity() <= originalCap {
		t.Errorf("expected capacity to grow beyond %d, got %d", originalCap, cv.Capacity())
	}
}

// ---------------------------------------------------------------------------
// readColumnData error path
// ---------------------------------------------------------------------------

// TestReadColumnDataOverflowLowCov tests readColumnData when data exceeds buffer.
func TestReadColumnDataOverflowLowCov(t *testing.T) {
	data := make([]byte, 8)
	binary.LittleEndian.PutUint32(data, 1000) // data length = 1000
	_, err := readColumnData(data, 0, &EncodedColumn{})
	if err == nil {
		t.Error("expected error for data exceeding buffer, got nil")
	}
}

// ---------------------------------------------------------------------------
// readOffsets error path
// ---------------------------------------------------------------------------

// TestReadOffsetsOverflowLowCov tests readOffsets when offsets data exceeds buffer.
func TestReadOffsetsOverflowLowCov(t *testing.T) {
	data := make([]byte, 8)
	binary.LittleEndian.PutUint32(data, 1000) // offsets count = 1000
	_, err := readOffsets(data, 0, &EncodedColumn{})
	if err == nil {
		t.Error("expected error for offsets exceeding buffer, got nil")
	}
}

// ---------------------------------------------------------------------------
// readDict error paths
// ---------------------------------------------------------------------------

// TestReadDictStringLenOverflowLowCov tests readDict when string length exceeds buffer.
func TestReadDictStringLenOverflowLowCov(t *testing.T) {
	data := make([]byte, 12)
	binary.LittleEndian.PutUint32(data, 1)       // 1 dict entry
	binary.LittleEndian.PutUint32(data[4:], 100) // string length = 100
	_, err := readDict(data, 0, &EncodedColumn{})
	if err == nil {
		t.Error("expected error for dict string length exceeding buffer, got nil")
	}
}

// TestReadDictEmptyStringLowCov tests readDict with zero-length string.
func TestReadDictEmptyStringLowCov(t *testing.T) {
	data := make([]byte, 12)
	binary.LittleEndian.PutUint32(data, 1)     // 1 dict entry
	binary.LittleEndian.PutUint32(data[4:], 0) // string length = 0
	enc := &EncodedColumn{}
	_, err := readDict(data, 0, enc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(enc.Dict) != 1 {
		t.Errorf("expected 1 dict entry, got %d", len(enc.Dict))
	}
	if enc.Dict[0] != "" {
		t.Errorf("expected empty string, got %q", enc.Dict[0])
	}
}
