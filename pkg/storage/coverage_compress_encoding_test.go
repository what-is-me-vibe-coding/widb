package storage

import (
	"bytes"
	"sync"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestGetEncoderPoolMiss 测试 getEncoder 的池未命中路径（创建新编码器）。
// 通过清空编码器池来确保下一次调用必须创建新实例。
func TestGetEncoderPoolMiss(t *testing.T) {
	// 先通过 getEncoder/putEncoder 填充池
	for i := 0; i < 5; i++ {
		enc, err := getEncoder()
		if err != nil {
			t.Fatalf("getEncoder() 第 %d 次调用失败: %v", i, err)
		}
		putEncoder(enc)
	}

	// 清空池：取出所有编码器但不归还
	var held []any
	for {
		v := encoderPool.Get()
		if v == nil {
			break
		}
		held = append(held, v)
	}

	// 现在池应该为空，下一次 getEncoder 必须走创建新实例路径（第 20-23 行）
	enc, err := getEncoder()
	if err != nil {
		t.Fatalf("getEncoder() 池未命中路径失败: %v", err)
	}
	if enc == nil {
		t.Fatal("getEncoder() 返回 nil 编码器")
	}

	// 验证新创建的编码器可以正常工作
	data := []byte("pool miss test")
	compressed := enc.EncodeAll(data, nil)
	if len(compressed) == 0 {
		t.Fatal("编码器压缩后数据为空")
	}
	putEncoder(enc)

	// 归还之前持有的编码器
	for _, e := range held {
		encoderPool.Put(e)
	}
}

// TestGetDecoderPoolMiss 测试 getDecoder 的池未命中路径（创建新解码器）。
func TestGetDecoderPoolMiss(t *testing.T) {
	// 先填充池
	for i := 0; i < 5; i++ {
		dec, err := getDecoder()
		if err != nil {
			t.Fatalf("getDecoder() 第 %d 次调用失败: %v", i, err)
		}
		putDecoder(dec)
	}

	// 清空池
	var held []any
	for {
		v := decoderPool.Get()
		if v == nil {
			break
		}
		held = append(held, v)
	}

	// 池为空，下一次 getDecoder 必须走创建新实例路径（第 37-40 行）
	dec, err := getDecoder()
	if err != nil {
		t.Fatalf("getDecoder() 池未命中路径失败: %v", err)
	}
	if dec == nil {
		t.Fatal("getDecoder() 返回 nil 解码器")
	}

	// 验证新创建的解码器可以正常工作
	enc, err := getEncoder()
	if err != nil {
		t.Fatalf("getEncoder() 失败: %v", err)
	}
	original := []byte("decoder pool miss test")
	compressed := enc.EncodeAll(original, nil)
	putEncoder(enc)

	result, err := dec.DecodeAll(compressed, nil)
	if err != nil {
		t.Fatalf("解码器解压失败: %v", err)
	}
	if !bytes.Equal(result, original) {
		t.Errorf("解码结果不匹配: got %d bytes, want %d bytes", len(result), len(original))
	}
	putDecoder(dec)

	// 归还之前持有的解码器
	for _, d := range held {
		decoderPool.Put(d)
	}
}

// TestCompressPoolRoundTrip 测试编码器池的往返使用（获取、使用、归还、再获取）。
// 多次压缩/解压循环确保池的归还和复用路径被覆盖。
func TestCompressPoolRoundTrip(t *testing.T) {
	data := []byte("pool round trip test data for encoder reuse")

	for i := 0; i < 5; i++ {
		compressed, err := Compress(data)
		if err != nil {
			t.Fatalf("第 %d 次压缩失败: %v", i, err)
		}
		decompressed, err := Decompress(compressed)
		if err != nil {
			t.Fatalf("第 %d 次解压失败: %v", i, err)
		}
		if !bytes.Equal(decompressed, data) {
			t.Errorf("第 %d 次循环: 数据不匹配", i)
		}
	}
}

// TestCompressColumnSingleRow 测试 CompressColumn 对只有 1 行数据的处理。
func TestCompressColumnSingleRow(t *testing.T) {
	ints := []int64{42}
	enc, err := EncodeColumn(common.TypeInt64, ints, 1, nil)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}

	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn 失败: %v", err)
	}

	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn 失败: %v", err)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}

	decodedInts, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("期望 []int64，实际 %T", decoded)
	}
	if decodedInts[0] != 42 {
		t.Errorf("got %d, want 42", decodedInts[0])
	}
}

// TestCompressColumnAllNulls 测试 CompressColumn 对全部为 NULL 的数据的处理。
func TestCompressColumnAllNulls(t *testing.T) {
	ints := make([]int64, 10) // 全零值
	nulls := common.NewBitmap(10)
	for i := uint32(0); i < 10; i++ {
		nulls.Set(i)
	}

	enc, err := EncodeColumn(common.TypeInt64, ints, 10, nulls)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}

	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn 失败: %v", err)
	}

	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn 失败: %v", err)
	}

	decoded, decodedNulls, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}

	decodedInts, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("期望 []int64，实际 %T", decoded)
	}
	if len(decodedInts) != 10 {
		t.Errorf("行数 = %d, want 10", len(decodedInts))
	}
	// 验证所有行都是 NULL
	for i := uint32(0); i < 10; i++ {
		if decodedNulls == nil || !decodedNulls.Get(i) {
			t.Errorf("行 %d: 期望 NULL", i)
		}
	}
}

// TestDecodeColumnUnknownEncodingCov 测试 DecodeColumn 对未知编码类型返回错误。
// 这覆盖了 encoding.go 中 DecodeColumn 的 default 分支（第 86-87 行）。
func TestDecodeColumnUnknownEncodingCov(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingType(99),
		Type:     common.TypeInt64,
		RowCount: 1,
		Data:     make([]byte, 8),
	}
	_, _, err := DecodeColumn(enc)
	if err == nil {
		t.Fatal("期望未知编码类型返回错误，实际返回 nil")
	}
}

// TestEncodeColumnUnknownEncodingCov 测试 EncodeColumn 中 default 分支。
// 由于 selectEncoding 对所有已知类型只返回 0-3，default 分支无法通过正常路径触发。
// 这里通过验证 TypeNull 类型来间接测试：TypeNull 走 EncodingPlain，
// 但 encodePlain 对 TypeNull 在 default 中报错。
func TestEncodeColumnUnknownEncodingCov(t *testing.T) {
	// TypeNull 不匹配任何已知编码策略，selectEncoding 返回 EncodingPlain，
	// 但 encodePlain 对 TypeNull 返回错误
	_, err := EncodeColumn(common.TypeNull, nil, 1, nil)
	if err == nil {
		t.Fatal("期望 TypeNull 编码返回错误，实际返回 nil")
	}
}

// TestConcurrentCompressDecompress 测试并发压缩/解压，覆盖池竞争路径。
func TestConcurrentCompressDecompress(t *testing.T) {
	data := []byte("concurrent pool contention test data")
	const goroutines = 20
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				compressed, err := Compress(data)
				if err != nil {
					t.Errorf("goroutine %d: 压缩失败: %v", id, err)
					return
				}
				decompressed, err := Decompress(compressed)
				if err != nil {
					t.Errorf("goroutine %d: 解压失败: %v", id, err)
					return
				}
				if !bytes.Equal(decompressed, data) {
					t.Errorf("goroutine %d: 数据不匹配", id)
					return
				}
			}
		}(g)
	}

	wg.Wait()
}

// compressColumnRoundTrip 执行一次列编码-压缩-解压-解码的完整流程。
func compressColumnRoundTrip(t *testing.T, id, val int) {
	t.Helper()
	ints := []int64{int64(val)}
	enc, err := EncodeColumn(common.TypeInt64, ints, 1, nil)
	if err != nil {
		t.Errorf("goroutine %d: EncodeColumn 失败: %v", id, err)
		return
	}
	if err := CompressColumn(enc); err != nil {
		t.Errorf("goroutine %d: CompressColumn 失败: %v", id, err)
		return
	}
	if err := DecompressColumn(enc); err != nil {
		t.Errorf("goroutine %d: DecompressColumn 失败: %v", id, err)
		return
	}
	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Errorf("goroutine %d: DecodeColumn 失败: %v", id, err)
		return
	}
	decodedInts, ok := decoded.([]int64)
	if !ok || len(decodedInts) != 1 || decodedInts[0] != ints[0] {
		t.Errorf("goroutine %d: 解码结果不匹配", id)
	}
}

// TestConcurrentCompressColumn 测试并发列压缩/解压，覆盖池竞争路径。
func TestConcurrentCompressColumn(t *testing.T) {
	const goroutines = 10
	const iterations = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				compressColumnRoundTrip(t, id, id*100+i)
			}
		}(g)
	}

	wg.Wait()
}

// TestCompressDecompressMultiplePoolCycles 测试多次池循环（获取-使用-归还）。
// 确保编码器和解码器在多次归还后仍能正常工作。
func TestCompressDecompressMultiplePoolCycles(t *testing.T) {
	datasets := [][]byte{
		[]byte("first dataset"),
		bytes.Repeat([]byte("second dataset with more data"), 100),
		{0x00, 0x01, 0x02, 0x03},
		bytes.Repeat([]byte{0xFF}, 500),
	}

	for cycle, data := range datasets {
		compressed, err := Compress(data)
		if err != nil {
			t.Fatalf("第 %d 次循环: 压缩失败: %v", cycle, err)
		}
		decompressed, err := Decompress(compressed)
		if err != nil {
			t.Fatalf("第 %d 次循环: 解压失败: %v", cycle, err)
		}
		if !bytes.Equal(decompressed, data) {
			t.Errorf("第 %d 次循环: 数据不匹配", cycle)
		}
	}
}

// TestEncodeColumnSingleRowString 测试只有 1 行字符串数据的编码。
func TestEncodeColumnSingleRowString(t *testing.T) {
	strs := []string{testStrHello}
	enc, err := EncodeColumn(common.TypeString, strs, 1, nil)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}

	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn 失败: %v", err)
	}

	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn 失败: %v", err)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}

	decodedStrs, ok := decoded.([]string)
	if !ok {
		t.Fatalf("期望 []string，实际 %T", decoded)
	}
	if decodedStrs[0] != testStrHello {
		t.Errorf("got %q, want %q", decodedStrs[0], testStrHello)
	}
}

// TestEncodeColumnSingleRowBool 测试只有 1 行布尔数据的编码。
func TestEncodeColumnSingleRowBool(t *testing.T) {
	bools := []uint64{1}
	enc, err := EncodeColumn(common.TypeBool, bools, 1, nil)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}

	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn 失败: %v", err)
	}

	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn 失败: %v", err)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}

	decodedBools, ok := decoded.([]uint64)
	if !ok {
		t.Fatalf("期望 []uint64，实际 %T", decoded)
	}
	if decodedBools[0] != 1 {
		t.Errorf("got %d, want 1", decodedBools[0])
	}
}

// TestEncodeColumnAllNullsString 测试全部为 NULL 的字符串列编码。
func TestEncodeColumnAllNullsString(t *testing.T) {
	strs := make([]string, 5)
	nulls := common.NewBitmap(5)
	for i := uint32(0); i < 5; i++ {
		nulls.Set(i)
	}

	enc, err := EncodeColumn(common.TypeString, strs, 5, nulls)
	if err != nil {
		t.Fatalf("EncodeColumn 失败: %v", err)
	}

	if err := CompressColumn(enc); err != nil {
		t.Fatalf("CompressColumn 失败: %v", err)
	}

	if err := DecompressColumn(enc); err != nil {
		t.Fatalf("DecompressColumn 失败: %v", err)
	}

	decoded, decodedNulls, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn 失败: %v", err)
	}

	decodedStrs, ok := decoded.([]string)
	if !ok {
		t.Fatalf("期望 []string，实际 %T", decoded)
	}
	if len(decodedStrs) != 5 {
		t.Errorf("行数 = %d, want 5", len(decodedStrs))
	}
	for i := uint32(0); i < 5; i++ {
		if decodedNulls == nil || !decodedNulls.Get(i) {
			t.Errorf("行 %d: 期望 NULL", i)
		}
	}
}
