package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ---------------------------------------------------------------------------
// OpenWAL 错误路径覆盖（wal.go:66）
// ---------------------------------------------------------------------------

// TestOpenWALNonExistentPathCov_V4 测试 OpenWAL 在路径不存在时返回错误
// 覆盖 wal.go:69-70 行的 os.IsNotExist 分支
func TestOpenWALNonExistentPathCov_V4(t *testing.T) {
	dir := t.TempDir()
	// 文件不存在
	_, _, err := OpenWAL(filepath.Join(dir, "no_such.wal"))
	if err == nil {
		t.Fatal("期望文件不存在时返回错误，得到 nil")
	}
}

// TestOpenWALNonExistentDirCov_V4 测试 OpenWAL 在目录不存在时返回错误
// 覆盖 wal.go:69-70 行的 os.IsNotExist 分支
func TestOpenWALNonExistentDirCov_V4(t *testing.T) {
	_, _, err := OpenWAL("/nonexistent_dir/sub/dir/test.wal")
	if err == nil {
		t.Fatal("期望目录不存在时返回错误，得到 nil")
	}
}

// TestOpenWALPermissionDeniedCov_V4 测试 OpenWAL 在权限不足时返回错误
// 覆盖 wal.go:72 行的非 NotExist 错误分支
func TestOpenWALPermissionDeniedCov_V4(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root 用户绕过文件权限检查")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "noperm.wal")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("创建文件失败: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("关闭文件失败: %v", err)
	}
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatalf("chmod 失败: %v", err)
	}
	defer func() { _ = os.Chmod(path, 0644) }()

	_, _, err = OpenWAL(path)
	if err == nil {
		t.Fatal("期望权限不足返回错误，得到 nil")
	}
}

// TestOpenWALValidRecoveryCov_V4 测试 OpenWAL 正常恢复路径
// 验证 replay + truncate + seek 正常路径的完整覆盖
func TestOpenWALValidRecoveryCov_V4(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "valid.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.AppendWrite([]byte("record_one"))
	_ = w.AppendWrite([]byte("record_two"))
	_ = w.Sync()
	_ = w.Close()

	opened, recs, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = opened.Close() }()

	if len(recs) != 2 {
		t.Fatalf("期望 2 条记录，得到 %d", len(recs))
	}
	// 验证恢复后可以继续追加
	if err := opened.AppendWrite([]byte("after_open")); err != nil {
		t.Fatalf("恢复后追加失败: %v", err)
	}
}

// ---------------------------------------------------------------------------
// getEncoder / getDecoder 池复用路径（compress.go:16, compress.go:33）
// ---------------------------------------------------------------------------

// TestGetEncoderPoolReuseCov_V4 测试 getEncoder 从池中复用编码器
// 覆盖 compress.go:17-18 行的池复用分支
func TestGetEncoderPoolReuseCov_V4(t *testing.T) {
	// 第一次获取编码器（池为空，创建新的）
	enc1, err := getEncoder()
	if err != nil {
		t.Fatalf("getEncoder 失败: %v", err)
	}
	// 归还到池中
	putEncoder(enc1)

	// 第二次获取编码器（应从池中复用）
	enc2, err := getEncoder()
	if err != nil {
		t.Fatalf("getEncoder 复用失败: %v", err)
	}
	// 验证编码器可用
	result := enc2.EncodeAll([]byte("pool reuse test"), nil)
	if len(result) == 0 {
		t.Error("编码器应产生非空输出")
	}
	putEncoder(enc2)
}

// TestGetDecoderPoolReuseCov_V4 测试 getDecoder 从池中复用解码器
// 覆盖 compress.go:34-35 行的池复用分支
func TestGetDecoderPoolReuseCov_V4(t *testing.T) {
	// 先压缩一些数据用于后续解码
	compressed, err := Compress([]byte("decoder pool reuse test"))
	if err != nil {
		t.Fatalf("Compress 失败: %v", err)
	}

	// 第一次获取解码器（池为空，创建新的）
	dec1, err := getDecoder()
	if err != nil {
		t.Fatalf("getDecoder 失败: %v", err)
	}
	// 归还到池中
	putDecoder(dec1)

	// 第二次获取解码器（应从池中复用）
	dec2, err := getDecoder()
	if err != nil {
		t.Fatalf("getDecoder 复用失败: %v", err)
	}
	// 验证解码器可用
	result, err := dec2.DecodeAll(compressed, nil)
	if err != nil {
		t.Fatalf("DecodeAll 失败: %v", err)
	}
	if string(result) != "decoder pool reuse test" {
		t.Errorf("解码结果不匹配: 期望 %q，得到 %q", "decoder pool reuse test", string(result))
	}
	putDecoder(dec2)
}

// TestCompressPoolReuseViaCompressCov_V4 测试通过 Compress/Decompress 间接复用编解码器池
// 连续多次调用 Compress/Decompress，验证池复用路径
func TestCompressPoolReuseViaCompressCov_V4(t *testing.T) {
	for i := 0; i < 5; i++ {
		original := []byte("pool test iteration data")
		compressed, err := Compress(original)
		if err != nil {
			t.Fatalf("Compress 第 %d 次失败: %v", i, err)
		}
		decompressed, err := Decompress(compressed)
		if err != nil {
			t.Fatalf("Decompress 第 %d 次失败: %v", i, err)
		}
		if string(decompressed) != string(original) {
			t.Errorf("第 %d 次往返不匹配", i)
		}
	}
}

// ---------------------------------------------------------------------------
// Compress 空数据路径（compress.go:50）
// ---------------------------------------------------------------------------

// TestCompressEmptyNilDataCov_V4 测试 Compress 对 nil 和空切片的处理
// 覆盖 compress.go:51-53 行的空数据分支（返回 nil, nil）
func TestCompressEmptyNilDataCov_V4(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"nil 切片", nil},
		{"空切片", []byte{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Compress(tt.data)
			if err != nil {
				t.Fatalf("Compress 失败: %v", err)
			}
			if result != nil {
				t.Errorf("期望 nil 结果，得到 %d 字节", len(result))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// CompressColumn nil 错误路径（compress.go:80）
// ---------------------------------------------------------------------------

// TestCompressColumnNilErrorCov_V4 测试 CompressColumn 传入 nil 返回错误
// 覆盖 compress.go:81-83 行的 nil 检查分支
func TestCompressColumnNilErrorCov_V4(t *testing.T) {
	err := CompressColumn(nil)
	if err == nil {
		t.Fatal("期望 nil EncodedColumn 返回错误，得到 nil")
	}
}

// TestDecompressColumnNilErrorCov_V4 测试 DecompressColumn 传入 nil 返回错误
// 覆盖 compress.go:94-96 行的 nil 检查分支
func TestDecompressColumnNilErrorCov_V4(t *testing.T) {
	err := DecompressColumn(nil)
	if err == nil {
		t.Fatal("期望 nil EncodedColumn 返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// EncodeColumn / DecodeColumn 未知编码类型路径（encoding.go:51）
// ---------------------------------------------------------------------------

// TestDecodeColumnUnknownEncodingCov_V4 测试 DecodeColumn 对未知编码类型返回错误
// 覆盖 encoding.go:79 行的 default 分支
// 注：EncodeColumn 的 default 分支（encoding.go:63）不可达，
// 因为 selectEncoding 总是返回已知编码类型
func TestDecodeColumnUnknownEncodingCov_V4(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingType(99), // 未知编码类型
		Type:     common.TypeInt64,
		RowCount: 1,
		Data:     make([]byte, 8),
	}
	_, _, err := DecodeColumn(enc)
	if err == nil {
		t.Fatal("期望未知编码类型返回错误，得到 nil")
	}
}

// TestEncodeColumnAllSupportedTypesCov_V4 测试 EncodeColumn 对所有支持的数据类型正常编码
// 确保 selectEncoding 的所有分支都被覆盖
func TestEncodeColumnAllSupportedTypesCov_V4(t *testing.T) {
	tests := []struct {
		name     string
		typ      common.DataType
		data     interface{}
		rowCount uint32
		wantEnc  EncodingType
	}{
		{"bool->Bitmap", common.TypeBool, []uint64{1, 0, 1}, 3, EncodingBitmap},
		{"string->Dict", common.TypeString, []string{"a", "b"}, 2, EncodingDict},
		{"int64 RLE", common.TypeInt64, repeatInt64(42, 100), 100, EncodingRLE},
		{"int64 Plain", common.TypeInt64, []int64{1, 2, 3}, 3, EncodingPlain},
		{"float64->Plain", common.TypeFloat64, []float64{1.1, 2.2}, 2, EncodingPlain},
		{"timestamp->Plain", common.TypeTimestamp, []int64{1000, 2000}, 2, EncodingPlain},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := EncodeColumn(tt.typ, tt.data, tt.rowCount, nil)
			if err != nil {
				t.Fatalf("EncodeColumn 失败: %v", err)
			}
			if enc.Encoding != tt.wantEnc {
				t.Errorf("编码类型 = %v，期望 %v", enc.Encoding, tt.wantEnc)
			}
		})
	}
}

// repeatInt64 生成重复的 int64 切片（用于触发 RLE 编码）
func repeatInt64(val int64, count int) []int64 {
	s := make([]int64, count)
	for i := range s {
		s[i] = val
	}
	return s
}
