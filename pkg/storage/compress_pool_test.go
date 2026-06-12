package storage

import (
	"bytes"
	"sync"
	"testing"
)

// TestEncoderDecoderPoolReuse 测试 Compress/Decompress 的编解码器池复用。
// 通过清空池再多次调用确保池命中和未命中路径都被覆盖。
func TestEncoderDecoderPoolReuse(t *testing.T) {
	// 先清空池，确保第一次调用走池未命中路径（创建新编码器/解码器）
	encoderPool = sync.Pool{}
	decoderPool = sync.Pool{}

	data := []byte("hello world, this is a test string for compression pool reuse")

	// 第一次调用：池未命中，需要创建新的编码器和解码器
	compressed, err := Compress(data)
	if err != nil {
		t.Fatalf("第一次 Compress 失败: %v", err)
	}
	if len(compressed) == 0 {
		t.Fatal("压缩结果不应为空")
	}

	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("第一次 Decompress 失败: %v", err)
	}
	if !bytes.Equal(decompressed, data) {
		t.Errorf("解压结果不匹配: 期望 %q, 实际 %q", data, decompressed)
	}

	// 第二次调用：池命中，复用已归还的编码器和解码器
	compressed2, err := Compress(data)
	if err != nil {
		t.Fatalf("第二次 Compress 失败: %v", err)
	}

	decompressed2, err := Decompress(compressed2)
	if err != nil {
		t.Fatalf("第二次 Decompress 失败: %v", err)
	}
	if !bytes.Equal(decompressed2, data) {
		t.Errorf("解压结果不匹配: 期望 %q, 实际 %q", data, decompressed2)
	}

	// 多次调用确保池复用稳定
	for i := 0; i < 10; i++ {
		compressed, err := Compress(data)
		if err != nil {
			t.Fatalf("第 %d 次 Compress 失败: %v", i, err)
		}
		decompressed, err := Decompress(compressed)
		if err != nil {
			t.Fatalf("第 %d 次 Decompress 失败: %v", i, err)
		}
		if !bytes.Equal(decompressed, data) {
			t.Fatalf("第 %d 次解压结果不匹配", i)
		}
	}
}

// TestCompressEmptyDataV2 测试空数据的压缩和解压。
func TestCompressEmptyDataV2(t *testing.T) {
	// 空数据应返回 nil
	result, err := Compress(nil)
	if err != nil {
		t.Fatalf("Compress(nil) 失败: %v", err)
	}
	if result != nil {
		t.Errorf("Compress(nil) 期望 nil，实际: %v", result)
	}

	result, err = Compress([]byte{})
	if err != nil {
		t.Fatalf("Compress([]byte{}) 失败: %v", err)
	}
	if result != nil {
		t.Errorf("Compress([]byte{}) 期望 nil，实际: %v", result)
	}

	result, err = Decompress(nil)
	if err != nil {
		t.Fatalf("Decompress(nil) 失败: %v", err)
	}
	if result != nil {
		t.Errorf("Decompress(nil) 期望 nil，实际: %v", result)
	}

	result, err = Decompress([]byte{})
	if err != nil {
		t.Fatalf("Decompress([]byte{}) 失败: %v", err)
	}
	if result != nil {
		t.Errorf("Decompress([]byte{}) 期望 nil，实际: %v", result)
	}
}

// TestCompressLargeDataV2 测试大数据的压缩和解压。
func TestCompressLargeDataV2(t *testing.T) {
	// 生成较大的可压缩数据
	data := make([]byte, 10000)
	for i := range data {
		data[i] = byte(i % 10) // 重复模式，压缩效果好
	}

	compressed, err := Compress(data)
	if err != nil {
		t.Fatalf("Compress 大数据失败: %v", err)
	}
	if len(compressed) >= len(data) {
		t.Logf("压缩效果不佳: 原始 %d 字节, 压缩后 %d 字节", len(data), len(compressed))
	}

	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress 大数据失败: %v", err)
	}
	if !bytes.Equal(decompressed, data) {
		t.Error("大数据解压结果不匹配")
	}
}

// TestDecompressInvalidZstdDataV2 测试解压无效数据时的错误。
func TestDecompressInvalidZstdDataV2(t *testing.T) {
	_, err := Decompress([]byte("this is not valid zstd data"))
	if err == nil {
		t.Error("期望 Decompress 返回错误，但返回 nil")
	}
}

// TestCompressColumnNilInputV3 测试 CompressColumn 传入 nil 时的错误。
func TestCompressColumnNilInputV3(t *testing.T) {
	err := CompressColumn(nil)
	if err == nil {
		t.Error("期望 CompressColumn(nil) 返回错误，但返回 nil")
	}
}

// TestDecompressColumnNilInputV3 测试 DecompressColumn 传入 nil 时的错误。
func TestDecompressColumnNilInputV3(t *testing.T) {
	err := DecompressColumn(nil)
	if err == nil {
		t.Error("期望 DecompressColumn(nil) 返回错误，但返回 nil")
	}
}

// TestCompressDecompressColumnRoundTripV2 测试列数据的压缩和解压往返。
func TestCompressDecompressColumnRoundTripV2(t *testing.T) {
	original := &EncodedColumn{
		Data: []byte("test column data for compression"),
	}

	// 压缩
	if err := CompressColumn(original); err != nil {
		t.Fatalf("CompressColumn 失败: %v", err)
	}

	// 解压
	if err := DecompressColumn(original); err != nil {
		t.Fatalf("DecompressColumn 失败: %v", err)
	}

	if string(original.Data) != "test column data for compression" {
		t.Errorf("往返结果不匹配: %q", original.Data)
	}
}
