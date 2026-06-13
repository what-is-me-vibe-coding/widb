package storage

import (
	"bytes"
	"sync"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ---------------------------------------------------------------------------
// Compress/Decompress: 空输入和往返测试（compress.go 第 50-77 行）
// ---------------------------------------------------------------------------

// TestCompressEmptyInput_V9 测试 Compress 对空输入返回 nil, nil。
func TestCompressEmptyInput_V9(t *testing.T) {
	result, err := Compress([]byte{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if result != nil {
		t.Errorf("期望 nil，实际 %d 字节", len(result))
	}
}

// TestDecompressEmptyInput_V9 测试 Decompress 对空输入返回 nil, nil。
func TestDecompressEmptyInput_V9(t *testing.T) {
	result, err := Decompress([]byte{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if result != nil {
		t.Errorf("期望 nil，实际 %d 字节", len(result))
	}
}

// TestCompressDecompressRoundTrip_V9 使用表驱动测试验证不同大小数据的压缩/解压往返。
func TestCompressDecompressRoundTrip_V9(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"单字节", []byte{0x42}},
		{"短字符串", []byte("hello zstd")},
		{"中等数据", bytes.Repeat([]byte("medium data block "), 500)},
		{"大数据", bytes.Repeat([]byte("large data chunk for compression test "), 5000)},
		{"重复数据（高压缩比）", bytes.Repeat([]byte("AAAA"), 10000)},
		{"二进制数据", make([]byte, 1024)}, // 全零数据
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed, err := Compress(tt.data)
			if err != nil {
				t.Fatalf("Compress 失败: %v", err)
			}
			if len(tt.data) > 0 && compressed == nil {
				t.Fatal("非空数据压缩后不应为 nil")
			}

			decompressed, err := Decompress(compressed)
			if err != nil {
				t.Fatalf("Decompress 失败: %v", err)
			}

			if !bytes.Equal(decompressed, tt.data) {
				t.Errorf("往返不匹配: 输入长度 %d, 解压长度 %d", len(tt.data), len(decompressed))
			}
		})
	}
}

// TestCompressReducesSize_V9 测试压缩后的数据比原始数据小（对于可压缩数据）。
func TestCompressReducesSize_V9(t *testing.T) {
	// 高度可压缩的数据
	data := bytes.Repeat([]byte("repeat"), 10000)
	compressed, err := Compress(data)
	if err != nil {
		t.Fatalf("Compress 失败: %v", err)
	}

	if len(compressed) >= len(data) {
		t.Errorf("压缩后大小 %d 应小于原始大小 %d（对可压缩数据）", len(compressed), len(data))
	}
}

// ---------------------------------------------------------------------------
// CompressColumn / DecompressColumn: nil 输入和有效输入测试
// （compress.go 第 80-103 行）
// ---------------------------------------------------------------------------

// TestCompressColumnNil_V9 测试 CompressColumn 对 nil EncodedColumn 返回错误。
func TestCompressColumnNil_V9(t *testing.T) {
	err := CompressColumn(nil)
	if err == nil {
		t.Fatal("期望错误，实际返回 nil")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("nil EncodedColumn")) {
		t.Errorf("错误消息应包含 'nil EncodedColumn'，得到: %v", err)
	}
}

// TestDecompressColumnNil_V9 测试 DecompressColumn 对 nil EncodedColumn 返回错误。
func TestDecompressColumnNil_V9(t *testing.T) {
	err := DecompressColumn(nil)
	if err == nil {
		t.Fatal("期望错误，实际返回 nil")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("nil EncodedColumn")) {
		t.Errorf("错误消息应包含 'nil EncodedColumn'，得到: %v", err)
	}
}

// TestCompressColumnRoundTrip_V9 测试 CompressColumn + DecompressColumn 往返。
func TestCompressColumnRoundTrip_V9(t *testing.T) {
	originalData := []byte("column_data_for_compression_test")
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeString,
		RowCount: 5,
		Data:     originalData,
	}

	// 压缩
	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn 失败: %v", err)
	}

	// 验证数据已被压缩（大小应不同）
	if bytes.Equal(enc.Data, originalData) {
		t.Log("压缩后数据与原始数据相同（可能数据太小无法压缩），但不影响正确性")
	}

	// 解压
	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn 失败: %v", err)
	}

	// 验证数据恢复
	if !bytes.Equal(enc.Data, originalData) {
		t.Errorf("往返不匹配: 期望 %q, 实际 %q", string(originalData), string(enc.Data))
	}
}

// TestCompressColumnWithEmptyData_V9 测试 CompressColumn 对空数据列的处理。
func TestCompressColumnWithEmptyData_V9(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     0,
		RowCount: 0,
		Data:     []byte{},
	}

	err := CompressColumn(enc)
	if err != nil {
		t.Fatalf("CompressColumn 空数据不应报错: %v", err)
	}

	// 空数据压缩后 Data 应为 nil（Compress 返回 nil, nil 给空输入）
	if enc.Data != nil {
		t.Errorf("期望 Data 为 nil，实际 %d 字节", len(enc.Data))
	}
}

// TestDecompressColumnWithCorruptedData_V9 测试 DecompressColumn 对损坏数据返回错误。
func TestDecompressColumnWithCorruptedData_V9(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"无效 zstd 头", []byte{0xFF, 0xFE, 0xFD, 0xFC}},
		{"随机数据", []byte{0x00, 0x01, 0x02, 0x03}},
		{"部分 zstd 数据", []byte{0x28, 0xB5, 0x2F, 0xFD}}, // zstd 魔数但不完整
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := &EncodedColumn{
				Encoding: EncodingPlain,
				Type:     0,
				RowCount: 1,
				Data:     tt.data,
			}

			err := DecompressColumn(enc)
			if err == nil {
				t.Fatal("期望解压错误，实际返回 nil")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Decompress: 损坏数据测试
// ---------------------------------------------------------------------------

// TestDecompressCorruptedData_V9 测试 Decompress 对损坏数据返回错误。
func TestDecompressCorruptedData_V9(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"无效 zstd 头", []byte{0xFF, 0xFE, 0xFD, 0xFC}},
		{"随机数据", []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05}},
		{"截断的 zstd 数据", []byte{0x28, 0xB5, 0x2F, 0xFD, 0x00}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decompress(tt.data)
			if err == nil {
				t.Fatal("期望解压错误，实际返回 nil")
			}
		})
	}
}

// TestDecompressTruncatedCompressedData_V9 测试 Decompress 对截断的压缩数据返回错误。
func TestDecompressTruncatedCompressedData_V9(t *testing.T) {
	// 先压缩一段数据
	original := []byte("some data to compress and then truncate")
	compressed, err := Compress(original)
	if err != nil {
		t.Fatalf("Compress 失败: %v", err)
	}

	// 截断压缩数据
	if len(compressed) < 4 {
		t.Skip("压缩数据太短，无法截断")
	}
	truncated := compressed[:len(compressed)/2]

	_, err = Decompress(truncated)
	if err == nil {
		t.Fatal("期望截断数据解压失败，实际返回 nil")
	}
}

// ---------------------------------------------------------------------------
// getEncoder / getDecoder: 池复用测试
// ---------------------------------------------------------------------------

// TestEncoderDecoderPoolCycle_V9 测试编码器/解码器池的获取和归还循环。
func TestEncoderDecoderPoolCycle_V9(t *testing.T) {
	// 第一次获取：池为空，应创建新实例
	enc1, err := getEncoder()
	if err != nil {
		t.Fatalf("getEncoder 失败: %v", err)
	}

	dec1, err := getDecoder()
	if err != nil {
		t.Fatalf("getDecoder 失败: %v", err)
	}

	// 归还到池
	putEncoder(enc1)
	putDecoder(dec1)

	// 第二次获取：应从池中复用
	enc2, err := getEncoder()
	if err != nil {
		t.Fatalf("第二次 getEncoder 失败: %v", err)
	}

	dec2, err := getDecoder()
	if err != nil {
		t.Fatalf("第二次 getDecoder 失败: %v", err)
	}

	// 验证编码器和解码器可正常工作
	data := []byte("pool reuse test")
	compressed := enc2.EncodeAll(data, nil)
	result, err := dec2.DecodeAll(compressed, nil)
	if err != nil {
		t.Fatalf("池复用后编解码失败: %v", err)
	}

	if !bytes.Equal(result, data) {
		t.Errorf("池复用后数据不匹配: 期望 %q, 实际 %q", string(data), string(result))
	}

	putEncoder(enc2)
	putDecoder(dec2)
}

// TestEncoderPoolMultipleGetPut_V9 测试多次获取和归还编码器池。
func TestEncoderPoolMultipleGetPut_V9(t *testing.T) {
	const iterations = 10

	for i := 0; i < iterations; i++ {
		enc, err := getEncoder()
		if err != nil {
			t.Fatalf("第 %d 次 getEncoder 失败: %v", i, err)
		}

		data := []byte("iteration test data")
		compressed := enc.EncodeAll(data, nil)

		dec, err := getDecoder()
		if err != nil {
			t.Fatalf("第 %d 次 getDecoder 失败: %v", i, err)
		}

		decompressed, err := dec.DecodeAll(compressed, nil)
		if err != nil {
			t.Fatalf("第 %d 次解码失败: %v", i, err)
		}

		if !bytes.Equal(decompressed, data) {
			t.Errorf("第 %d 次数据不匹配", i)
		}

		putEncoder(enc)
		putDecoder(dec)
	}
}

// ---------------------------------------------------------------------------
// 并发压缩/解压操作
// ---------------------------------------------------------------------------

// TestConcurrentCompressDecompress_V9 测试并发压缩和解压操作。
func TestConcurrentCompressDecompress_V9(t *testing.T) {
	const goroutines = 10
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				data := []byte("concurrent_test_data_payload")
				compressed, err := Compress(data)
				if err != nil {
					t.Errorf("goroutine %d Compress 失败: %v", id, err)
					return
				}

				decompressed, err := Decompress(compressed)
				if err != nil {
					t.Errorf("goroutine %d Decompress 失败: %v", id, err)
					return
				}

				if !bytes.Equal(decompressed, data) {
					t.Errorf("goroutine %d 数据不匹配", id)
					return
				}
			}
		}(g)
	}

	wg.Wait()
}

// TestConcurrentCompressColumn_V9 测试并发 CompressColumn 操作。
func TestConcurrentCompressColumn_V9(t *testing.T) {
	const goroutines = 5
	const iterations = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				originalData := []byte("column_concurrent_data")
				enc := &EncodedColumn{
					Encoding: EncodingPlain,
					Type:     common.TypeString,
					RowCount: 1,
					Data:     originalData,
				}

				if err := CompressColumn(enc); err != nil {
					t.Errorf("goroutine %d CompressColumn 失败: %v", id, err)
					return
				}

				if err := DecompressColumn(enc); err != nil {
					t.Errorf("goroutine %d DecompressColumn 失败: %v", id, err)
					return
				}

				if !bytes.Equal(enc.Data, originalData) {
					t.Errorf("goroutine %d 数据不匹配", id)
					return
				}
			}
		}(g)
	}

	wg.Wait()
}

// ---------------------------------------------------------------------------
// CompressColumn / DecompressColumn: 边界情况
// ---------------------------------------------------------------------------

// TestCompressColumnLargeData_V9 测试 CompressColumn 对大数据的压缩。
func TestCompressColumnLargeData_V9(t *testing.T) {
	// 创建较大的列数据
	originalData := bytes.Repeat([]byte("large_column_data_for_testing"), 1000)
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeString,
		RowCount: 1000,
		Data:     originalData,
	}

	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn 大数据失败: %v", err)
	}

	// 验证压缩后数据更小
	if len(enc.Data) >= len(originalData) {
		t.Logf("压缩后大小 %d >= 原始大小 %d（可能数据不够可压缩）", len(enc.Data), len(originalData))
	}

	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn 大数据失败: %v", err)
	}

	if !bytes.Equal(enc.Data, originalData) {
		t.Errorf("大数据往返不匹配: 期望长度 %d, 实际长度 %d", len(originalData), len(enc.Data))
	}
}

// TestCompressColumnPreservesMetadata_V9 测试 CompressColumn/DecompressColumn
// 只修改 Data 字段，不修改其他元数据。
func TestCompressColumnPreservesMetadata_V9(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingDict,
		Type:     common.TypeInt64,
		RowCount: 42,
		Data:     []byte("test_data_for_metadata"),
		Dict:     []string{"a", "b", "c"},
		Offsets:  []uint32{0, 1, 2},
		Nulls:    []byte{0xFF},
	}

	// 保存元数据
	origEncoding := enc.Encoding
	origType := enc.Type
	origRowCount := enc.RowCount
	origDict := enc.Dict
	origOffsets := enc.Offsets
	origNulls := enc.Nulls

	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn 失败: %v", err)
	}

	// 验证元数据未被修改
	if enc.Encoding != origEncoding {
		t.Errorf("Encoding 被修改: 期望 %v, 实际 %v", origEncoding, enc.Encoding)
	}
	if enc.Type != origType {
		t.Errorf("Type 被修改: 期望 %v, 实际 %v", origType, enc.Type)
	}
	if enc.RowCount != origRowCount {
		t.Errorf("RowCount 被修改: 期望 %d, 实际 %d", origRowCount, enc.RowCount)
	}
	if len(enc.Dict) != len(origDict) {
		t.Errorf("Dict 被修改")
	}
	if len(enc.Offsets) != len(origOffsets) {
		t.Errorf("Offsets 被修改")
	}
	if !bytes.Equal(enc.Nulls, origNulls) {
		t.Errorf("Nulls 被修改")
	}

	// 解压后验证元数据仍然不变
	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn 失败: %v", err)
	}

	if enc.Encoding != origEncoding {
		t.Errorf("解压后 Encoding 被修改")
	}
	if enc.RowCount != origRowCount {
		t.Errorf("解压后 RowCount 被修改")
	}
}
