package storage

import (
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"
)

var (
	encoderPool     sync.Pool
	decoderPool     sync.Pool
	compressBufPool sync.Pool
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

// getCompressBuf 从池中获取压缩输出缓冲区。
func getCompressBuf(minCap int) *[]byte {
	if v := compressBufPool.Get(); v != nil {
		buf := v.(*[]byte)
		if cap(*buf) >= minCap {
			return buf
		}
		// 容量不足，分配新的
		newBuf := make([]byte, 0, minCap)
		return &newBuf
	}
	buf := make([]byte, 0, minCap)
	return &buf
}

// putCompressBuf 将压缩输出缓冲区归还到池中。
func putCompressBuf(buf *[]byte) {
	if cap(*buf) <= 1<<20 { // 只缓存不超过 1MB 的缓冲区，防止内存膨胀
		*buf = (*buf)[:0]
		compressBufPool.Put(buf)
	}
}

// Compress 使用 ZSTD 压缩数据，返回压缩后的字节切片。
func Compress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	enc, err := getEncoder()
	if err != nil {
		return nil, err
	}
	defer putEncoder(enc)

	// 使用池化缓冲区作为输出，减少堆分配
	dst := getCompressBuf(len(data))
	result := enc.EncodeAll(data, *dst)
	// result 可能引用了 dst 的底层数组，需要拷贝到独立切片
	out := make([]byte, len(result))
	copy(out, result)
	putCompressBuf(dst)
	return out, nil
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

	// 预估解压后大小约为压缩数据的 4 倍，使用池化缓冲区
	estimatedCap := len(data) * 4
	if estimatedCap < 64 {
		estimatedCap = 64
	}
	dst := getCompressBuf(estimatedCap)
	result, err := dec.DecodeAll(data, *dst)
	if err != nil {
		putCompressBuf(dst)
		return nil, fmt.Errorf("zstd decompress: %w", err)
	}
	// result 可能引用了 dst 的底层数组，需要拷贝到独立切片
	out := make([]byte, len(result))
	copy(out, result)
	putCompressBuf(dst)
	return out, nil
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
