package storage

import (
	"bytes"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const (
	levelNameFastest           = "SpeedFastest"
	levelNameDefault           = "SpeedDefault"
	levelNameBetterCompression = "SpeedBetterCompression"
	levelNameBestCompression   = "SpeedBestCompression"
	testDataSmall              = "hello world"
)

// TestCompressWithDifferentLevels 测试不同压缩级别下的压缩和解压正确性
func TestCompressWithDifferentLevels(t *testing.T) {
	tests := []struct {
		name  string
		level zstd.EncoderLevel
		data  []byte
	}{
		{levelNameDefault + "压缩小数据", zstd.SpeedDefault, []byte(testDataSmall)},
		{levelNameFastest + "压缩小数据", zstd.SpeedFastest, []byte(testDataSmall)},
		{levelNameBetterCompression + "压缩小数据", zstd.SpeedBetterCompression, []byte(testDataSmall)},
		{levelNameBestCompression + "压缩小数据", zstd.SpeedBestCompression, []byte(testDataSmall)},
		{levelNameDefault + "压缩大数据", zstd.SpeedDefault, bytes.Repeat([]byte("abcdefgh"), 1000)},
		{levelNameFastest + "压缩大数据", zstd.SpeedFastest, bytes.Repeat([]byte("abcdefgh"), 1000)},
		{levelNameBetterCompression + "压缩大数据", zstd.SpeedBetterCompression, bytes.Repeat([]byte("abcdefgh"), 1000)},
		{levelNameBestCompression + "压缩大数据", zstd.SpeedBestCompression, bytes.Repeat([]byte("abcdefgh"), 1000)},
		{levelNameDefault + "压缩高可压缩数据", zstd.SpeedDefault, bytes.Repeat([]byte{0x00}, 10000)},
		{levelNameBestCompression + "压缩高可压缩数据", zstd.SpeedBestCompression, bytes.Repeat([]byte{0x00}, 10000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(tt.level))
			if err != nil {
				t.Fatalf("创建编码器失败 (level=%v): %v", tt.level, err)
			}

			compressed := enc.EncodeAll(tt.data, nil)
			if len(compressed) == 0 {
				t.Fatal("压缩返回空数据")
			}

			decompressed, err := Decompress(compressed)
			if err != nil {
				t.Fatalf("解压失败 (level=%v): %v", tt.level, err)
			}

			if !bytes.Equal(decompressed, tt.data) {
				t.Errorf("round-trip 不匹配: got %d bytes, want %d bytes", len(decompressed), len(tt.data))
			}
		})
	}
}

// TestCompressLevelSizeComparison 测试不同压缩级别的压缩率差异
func TestCompressLevelSizeComparison(t *testing.T) {
	data := bytes.Repeat([]byte("test data for compression level comparison"), 5000)

	levels := []struct {
		name  string
		level zstd.EncoderLevel
	}{
		{levelNameFastest, zstd.SpeedFastest},
		{levelNameDefault, zstd.SpeedDefault},
		{levelNameBetterCompression, zstd.SpeedBetterCompression},
		{levelNameBestCompression, zstd.SpeedBestCompression},
	}

	sizes := make([]int, 0, len(levels))
	for _, l := range levels {
		enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(l.level))
		if err != nil {
			t.Fatalf("创建编码器失败 (%s): %v", l.name, err)
		}
		compressed := enc.EncodeAll(data, nil)
		sizes = append(sizes, len(compressed))
		t.Logf("%s: original=%d, compressed=%d, ratio=%.2f%%", l.name, len(data), len(compressed), float64(len(compressed))*100/float64(len(data)))

		decompressed, err := Decompress(compressed)
		if err != nil {
			t.Fatalf("解压 %s 失败: %v", l.name, err)
		}
		if !bytes.Equal(decompressed, data) {
			t.Errorf("%s round-trip 不匹配", l.name)
		}
	}

	if sizes[0] == 0 || sizes[len(sizes)-1] == 0 {
		t.Error("压缩后数据大小不应为 0")
	}
}

// verifyCompressColumnRoundTrip 验证指定级别的列压缩解压往返正确性
func verifyCompressColumnRoundTrip(t *testing.T, level zstd.EncoderLevel, ints []int64) {
	t.Helper()
	enc, err := EncodeColumn(common.TypeInt64, ints, uint32(len(ints)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}

	zstdEnc, zstdErr := zstd.NewWriter(nil, zstd.WithEncoderLevel(level))
	if zstdErr != nil {
		t.Fatalf("创建编码器失败: %v", zstdErr)
	}
	originalData := enc.Data
	enc.Data = zstdEnc.EncodeAll(enc.Data, nil)

	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn 失败: %v", err)
	}

	if !bytes.Equal(enc.Data, originalData) {
		t.Error("解压后数据不匹配")
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}
	decodedInts, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("期望 []int64, 得到 %T", decoded)
	}
	for i := range ints {
		if decodedInts[i] != ints[i] {
			t.Errorf("row %d: got %d, want %d", i, decodedInts[i], ints[i])
		}
	}
}

// TestCompressColumnWithDifferentLevels 测试不同压缩级别下 CompressColumn/DecompressColumn 的正确性
func TestCompressColumnWithDifferentLevels(t *testing.T) {
	ints := make([]int64, 5000)
	for i := range ints {
		ints[i] = int64(i * 3)
	}

	levels := []struct {
		name  string
		level zstd.EncoderLevel
	}{
		{levelNameFastest, zstd.SpeedFastest},
		{levelNameDefault, zstd.SpeedDefault},
		{levelNameBetterCompression, zstd.SpeedBetterCompression},
		{levelNameBestCompression, zstd.SpeedBestCompression},
	}

	for _, l := range levels {
		t.Run(l.name, func(t *testing.T) {
			verifyCompressColumnRoundTrip(t, l.level, ints)
		})
	}
}

// TestInitEncoderDecoder 测试延迟初始化的全局编码器和解码器正常工作
func TestInitEncoderDecoder(t *testing.T) {
	// 触发延迟初始化
	data := []byte("test init encoder and decoder")
	compressed, err := Compress(data)
	if err != nil {
		t.Fatalf("Compress 失败: %v", err)
	}
	if len(compressed) == 0 {
		t.Fatal("Compress 返回空数据")
	}

	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress 失败: %v", err)
	}
	if !bytes.Equal(decompressed, data) {
		t.Errorf("round-trip 不匹配: got %q, want %q", decompressed, data)
	}
}

// verifyTypeCompressRoundTrip 验证指定类型和级别的压缩解压往返正确性
func verifyTypeCompressRoundTrip(t *testing.T, enc *zstd.Encoder, typ common.DataType, _ interface{}, encoded *EncodedColumn) {
	t.Helper()
	originalData := encoded.Data
	encoded.Data = enc.EncodeAll(encoded.Data, nil)

	if err := DecompressColumn(encoded); err != nil {
		t.Fatalf("DecompressColumn %v 失败: %v", typ, err)
	}
	if !bytes.Equal(encoded.Data, originalData) {
		t.Errorf("%v 解压后数据不匹配", typ)
	}
}

// TestCompressDecompressVariousTypesWithLevel 测试不同压缩级别下多种数据类型的压缩解压
func TestCompressDecompressVariousTypesWithLevel(t *testing.T) {
	levels := []struct {
		name  string
		level zstd.EncoderLevel
	}{
		{levelNameFastest, zstd.SpeedFastest},
		{levelNameDefault, zstd.SpeedDefault},
		{levelNameBetterCompression, zstd.SpeedBetterCompression},
		{levelNameBestCompression, zstd.SpeedBestCompression},
	}

	for _, tt := range levels {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(tt.level))
			if err != nil {
				t.Fatalf("创建编码器失败: %v", err)
			}

			// 测试字符串类型
			strs := []string{testStrHello, testStrWorld, testStrFoo, testStrApple, testStrBanana}
			encoded, encErr := EncodeColumn(common.TypeString, strs, uint32(len(strs)), nil)
			if encErr != nil {
				t.Fatalf("EncodeColumn string 失败: %v", encErr)
			}
			verifyTypeCompressRoundTrip(t, enc, common.TypeString, strs, encoded)

			// 测试 float64 类型
			floats := make([]float64, 1000)
			for i := range floats {
				floats[i] = float64(i) * 1.5
			}
			encodedF, encErrF := EncodeColumn(common.TypeFloat64, floats, uint32(len(floats)), nil)
			if encErrF != nil {
				t.Fatalf("EncodeColumn float64 失败: %v", encErrF)
			}
			verifyTypeCompressRoundTrip(t, enc, common.TypeFloat64, floats, encodedF)
		})
	}
}
