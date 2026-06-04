package storage

import (
	"bytes"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestCompressDecompressEmpty(t *testing.T) {
	compressed := Compress(nil)
	if compressed != nil {
		t.Errorf("Compress(nil) = %v, want nil", compressed)
	}

	result, err := Decompress(nil)
	if err != nil {
		t.Fatalf("Decompress(nil) failed: %v", err)
	}
	if result != nil {
		t.Errorf("Decompress(nil) = %v, want nil", result)
	}

	result, err = Decompress([]byte{})
	if err != nil {
		t.Fatalf("Decompress([]byte{}) failed: %v", err)
	}
	if result != nil {
		t.Errorf("Decompress([]byte{}) = %v, want nil", result)
	}
}

func TestCompressDecompressRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"small", []byte("hello world")},
		{"medium", bytes.Repeat([]byte("abcdefgh"), 100)},
		{"large", bytes.Repeat([]byte("test data for compression"), 1000)},
		{"highly_compressible", bytes.Repeat([]byte{0x00}, 10000)},
		{"randomish", []byte("the quick brown fox jumps over the lazy dog 1234567890")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed := Compress(tt.data)
			if len(compressed) == 0 {
				t.Fatal("Compress returned empty data")
			}

			decompressed, err := Decompress(compressed)
			if err != nil {
				t.Fatalf("Decompress failed: %v", err)
			}

			if !bytes.Equal(decompressed, tt.data) {
				t.Errorf("round-trip mismatch: got %d bytes, want %d bytes", len(decompressed), len(tt.data))
			}
		})
	}
}

func TestCompressDecompressInt64(t *testing.T) {
	ints := make([]int64, 10000)
	for i := range ints {
		ints[i] = int64(i * 7)
	}

	enc, err := EncodeColumn(common.TypeInt64, ints, uint32(len(ints)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}

	originalSize := len(enc.Data)
	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn failed: %v", err)
	}

	if len(enc.Data) >= originalSize {
		t.Logf("compressed size %d >= original size %d (expected for small data)", len(enc.Data), originalSize)
	}

	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn failed: %v", err)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}

	decodedInts, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("expected []int64, got %T", decoded)
	}

	for i := range ints {
		if decodedInts[i] != ints[i] {
			t.Errorf("row %d: got %d, want %d", i, decodedInts[i], ints[i])
		}
	}
}

func TestCompressDecompressString(t *testing.T) {
	strs := []string{testStrHello, testStrWorld, testStrTest, testStrFoo, testStrBanana, testStrApple, testStrCherry, "date", "elderberry", "fig"}
	enc, err := EncodeColumn(common.TypeString, strs, uint32(len(strs)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}

	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn failed: %v", err)
	}

	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn failed: %v", err)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}

	decodedStrs, ok := decoded.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", decoded)
	}

	for i := range strs {
		if decodedStrs[i] != strs[i] {
			t.Errorf("row %d: got %q, want %q", i, decodedStrs[i], strs[i])
		}
	}
}

func TestCompressDecompressFloat64(t *testing.T) {
	floats := make([]float64, 5000)
	for i := range floats {
		floats[i] = float64(i) * 1.5
	}

	enc, err := EncodeColumn(common.TypeFloat64, floats, uint32(len(floats)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}

	originalSize := len(enc.Data)
	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn failed: %v", err)
	}
	t.Logf("float64: original=%d, compressed=%d, ratio=%.2f", originalSize, len(enc.Data), float64(len(enc.Data))/float64(originalSize))

	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn failed: %v", err)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}

	decodedFloats, ok := decoded.([]float64)
	if !ok {
		t.Fatalf("expected []float64, got %T", decoded)
	}

	for i := range floats {
		if decodedFloats[i] != floats[i] {
			t.Errorf("row %d: got %f, want %f", i, decodedFloats[i], floats[i])
		}
	}
}

func TestCompressDecompressBool(t *testing.T) {
	bools := make([]uint64, 10000)
	for i := range bools {
		if i%2 == 0 {
			bools[i] = 1
		}
	}

	enc, err := EncodeColumn(common.TypeBool, bools, uint32(len(bools)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}

	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn failed: %v", err)
	}

	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn failed: %v", err)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}

	decodedBools, ok := decoded.([]uint64)
	if !ok {
		t.Fatalf("expected []uint64, got %T", decoded)
	}

	for i := range bools {
		if decodedBools[i] != bools[i] {
			t.Errorf("row %d: got %d, want %d", i, decodedBools[i], bools[i])
		}
	}
}

func TestCompressDecompressTimestamp(t *testing.T) {
	now := time.Now()
	times := make([]int64, 5000)
	for i := range times {
		times[i] = now.Add(time.Duration(i) * time.Second).UnixNano()
	}

	enc, err := EncodeColumn(common.TypeTimestamp, times, uint32(len(times)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}

	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn failed: %v", err)
	}

	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn failed: %v", err)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}

	decodedTimes, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("expected []int64, got %T", decoded)
	}

	for i := range times {
		if decodedTimes[i] != times[i] {
			t.Errorf("row %d: got %d, want %d", i, decodedTimes[i], times[i])
		}
	}
}

func TestCompressDecompressRLE(t *testing.T) {
	ints := make([]int64, 10000)
	for i := range ints {
		ints[i] = int64(i / 100)
	}

	enc, err := EncodeColumn(common.TypeInt64, ints, uint32(len(ints)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}

	if enc.Encoding != EncodingRLE {
		t.Fatalf("expected RLE encoding, got %v", enc.Encoding)
	}

	originalSize := len(enc.Data)
	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn failed: %v", err)
	}
	t.Logf("RLE: original=%d, compressed=%d, ratio=%.2f", originalSize, len(enc.Data), float64(len(enc.Data))/float64(originalSize))

	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn failed: %v", err)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}

	decodedInts, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("expected []int64, got %T", decoded)
	}

	for i := range ints {
		if decodedInts[i] != ints[i] {
			t.Errorf("row %d: got %d, want %d", i, decodedInts[i], ints[i])
		}
	}
}

func TestCompressDecompressDict(t *testing.T) {
	strs := make([]string, 10000)
	for i := range strs {
		strs[i] = testStrHello
		if i%3 == 0 {
			strs[i] = testStrWorld
		}
		if i%7 == 0 {
			strs[i] = testStrFoo
		}
	}

	enc, err := EncodeColumn(common.TypeString, strs, uint32(len(strs)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}

	if enc.Encoding != EncodingDict {
		t.Fatalf("expected Dict encoding, got %v", enc.Encoding)
	}

	originalSize := len(enc.Data)
	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn failed: %v", err)
	}
	t.Logf("Dict: original=%d, compressed=%d, ratio=%.2f", originalSize, len(enc.Data), float64(len(enc.Data))/float64(originalSize))

	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn failed: %v", err)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}

	decodedStrs, ok := decoded.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", decoded)
	}

	for i := range strs {
		if decodedStrs[i] != strs[i] {
			t.Errorf("row %d: got %q, want %q", i, decodedStrs[i], strs[i])
		}
	}
}

func TestCompressColumnWithNulls(t *testing.T) {
	ints := make([]int64, 1000)
	for i := range ints {
		ints[i] = int64(i)
	}

	nulls := common.NewBitmap(1000)
	nulls.Set(0)
	nulls.Set(500)
	nulls.Set(999)

	enc, err := EncodeColumn(common.TypeInt64, ints, 1000, nulls)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}

	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn failed: %v", err)
	}

	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn failed: %v", err)
	}

	decoded, decodedNulls, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}

	decodedInts, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("expected []int64, got %T", decoded)
	}

	for i := range ints {
		if decodedNulls != nil && decodedNulls.Get(uint32(i)) {
			if !nulls.Get(uint32(i)) {
				t.Errorf("row %d: unexpected NULL", i)
			}
		} else {
			if nulls.Get(uint32(i)) {
				t.Errorf("row %d: expected NULL", i)
			}
			if decodedInts[i] != ints[i] {
				t.Errorf("row %d: got %d, want %d", i, decodedInts[i], ints[i])
			}
		}
	}
}

func TestCompressColumnNil(t *testing.T) {
	err := CompressColumn(nil)
	if err == nil {
		t.Fatal("expected error for nil EncodedColumn")
	}
}

func TestDecompressColumnNil(t *testing.T) {
	err := DecompressColumn(nil)
	if err == nil {
		t.Fatal("expected error for nil EncodedColumn")
	}
}

func TestDecompressCorruptedData(t *testing.T) {
	_, err := Decompress([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	if err == nil {
		t.Fatal("expected error for corrupted data")
	}
}

func TestCompressReduceSize(t *testing.T) {
	data := bytes.Repeat([]byte{0x00, 0x01, 0x02, 0x03}, 10000)
	compressed := Compress(data)
	if len(compressed) == 0 {
		t.Fatal("Compress returned empty data")
	}
	if len(compressed) >= len(data) {
		t.Errorf("compressed size %d >= original size %d, expected reduction", len(compressed), len(data))
	}
	t.Logf("compression ratio: %d/%d = %.2f%%", len(compressed), len(data), float64(len(compressed))*100/float64(len(data)))
}

// TestCompressEmptyData 测试压缩空数据
func TestCompressEmptyData(t *testing.T) {
	compressed := Compress([]byte{})
	if compressed != nil {
		t.Errorf("Compress([]byte{}) = %v, want nil", compressed)
	}
}

// TestDecompressEmptyData 测试解压空数据
func TestDecompressEmptyData(t *testing.T) {
	result, err := Decompress(nil)
	if err != nil {
		t.Fatalf("Decompress(nil) failed: %v", err)
	}
	if result != nil {
		t.Errorf("Decompress(nil) = %v, want nil", result)
	}

	result, err = Decompress([]byte{})
	if err != nil {
		t.Fatalf("Decompress([]byte{}) failed: %v", err)
	}
	if result != nil {
		t.Errorf("Decompress([]byte{}) = %v, want nil", result)
	}
}

// TestCompressLargeData 测试压缩和解压大数据
func TestCompressLargeData(t *testing.T) {
	// 创建 1MB 的重复数据
	data := bytes.Repeat([]byte("large data block for compression test "), 30000)
	compressed := Compress(data)
	if len(compressed) == 0 {
		t.Fatal("Compress returned empty data for large input")
	}

	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress large data failed: %v", err)
	}

	if !bytes.Equal(decompressed, data) {
		t.Errorf("round-trip mismatch for large data: got %d bytes, want %d bytes", len(decompressed), len(data))
	}

	t.Logf("large data: original=%d, compressed=%d, ratio=%.2f%%", len(data), len(compressed), float64(len(compressed))*100/float64(len(data)))
}

// TestDecompressInvalidData 测试解压无效数据（应返回错误）
func TestDecompressInvalidData(t *testing.T) {
	_, err := Decompress([]byte{0xFF, 0xFE, 0xFD, 0xFC, 0xFB})
	if err == nil {
		t.Fatal("expected error for invalid compressed data")
	}
}
