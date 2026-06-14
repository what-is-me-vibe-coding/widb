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
// 优化：直接让编码器分配输出缓冲区，避免池化缓冲区的额外拷贝开销。
// 对于大块数据（如列 Block），省去一次 memcpy 和额外分配可显著提升写入吞吐。
func Compress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	enc, err := getEncoder()
	if err != nil {
		return nil, err
	}
	defer putEncoder(enc)

	// 直接传入 nil，让编码器自行分配输出缓冲区并返回，
	// 避免池化缓冲区 → 拷贝到新切片的双重分配开销。
	result := enc.EncodeAll(data, nil)
	return result, nil
}

// Decompress 解压 ZSTD 压缩的数据，返回原始字节切片。
// 优化：直接让解码器分配输出缓冲区，避免池化缓冲区的额外拷贝开销。
// 解压后的列 Block 通常较大，省去 memcpy 可显著降低读取路径延迟。
func Decompress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	dec, err := getDecoder()
	if err != nil {
		return nil, err
	}
	defer putDecoder(dec)

	// 直接传入 nil，让解码器自行分配输出缓冲区并返回，
	// 避免池化缓冲区 → 拷贝到新切片的双重分配开销。
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
