package storage

import (
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"
)

var (
	encoderPool sync.Pool
	decoderPool sync.Pool
)

// getEncoder 从池中获取或创建 ZSTD 编码器。
func getEncoder() (*zstd.Encoder, error) {
	if v := encoderPool.Get(); v != nil {
		return v.(*zstd.Encoder), nil
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return nil, fmt.Errorf("zstd encoder init: %w", err)
	}
	return enc, nil
}

// putEncoder 将 ZSTD 编码器归还到池中。
func putEncoder(enc *zstd.Encoder) {
	encoderPool.Put(enc)
}

// getDecoder 从池中获取或创建 ZSTD 解码器。
func getDecoder() (*zstd.Decoder, error) {
	if v := decoderPool.Get(); v != nil {
		return v.(*zstd.Decoder), nil
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("zstd decoder init: %w", err)
	}
	return dec, nil
}

// putDecoder 将 ZSTD 解码器归还到池中。
func putDecoder(dec *zstd.Decoder) {
	decoderPool.Put(dec)
}

// Compress 使用 ZSTD 压缩数据，返回压缩后的字节切片。
// 预分配输出缓冲区以减少内存分配，ZSTD 压缩后大小通常不超过输入大小。
func Compress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	enc, err := getEncoder()
	if err != nil {
		return nil, err
	}
	defer putEncoder(enc)
	// 预分配输出缓冲区：ZSTD 压缩后通常小于输入，但最坏情况略大
	// 使用 EncodeAll 的 dst 参数预分配，避免内部多次扩容
	dst := make([]byte, 0, len(data)+len(data)/4)
	return enc.EncodeAll(data, dst), nil
}

// Decompress 解压 ZSTD 压缩的数据，返回原始字节切片。
func Decompress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	dec, err := getDecoder()
	if err != nil {
		return nil, err
	}
	defer putDecoder(dec)
	result, err := dec.DecodeAll(data, nil)
	if err != nil {
		return nil, fmt.Errorf("zstd decompress: %w", err)
	}
	return result, nil
}

// CompressColumn 压缩 EncodedColumn 中的编码数据。
func CompressColumn(enc *EncodedColumn) error {
	if enc == nil {
		return fmt.Errorf("compress column: nil EncodedColumn")
	}
	compressed, err := Compress(enc.Data)
	if err != nil {
		return fmt.Errorf("compress column: %w", err)
	}
	enc.Data = compressed
	return nil
}

// DecompressColumn 解压 EncodedColumn 中的压缩数据。
func DecompressColumn(enc *EncodedColumn) error {
	if enc == nil {
		return fmt.Errorf("decompress column: nil EncodedColumn")
	}
	data, err := Decompress(enc.Data)
	if err != nil {
		return err
	}
	enc.Data = data
	return nil
}
