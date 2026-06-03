package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const (
	benchColName  = "name"
	benchColScore = "score"
	benchValName  = "alice"
)

// --- MemTable 基准测试 ---

func BenchmarkMemTablePut(b *testing.B) {
	mt := NewMemTableWithSize(64 * 1024 * 1024)
	row := Row{
		Version: 1,
		Columns: map[string]common.Value{
			benchColName:  common.NewString(benchValName),
			benchColScore: common.NewFloat64(95.5),
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key_%016d", i)
		_, _, _ = mt.Put(key, row)
	}
	b.ReportAllocs()
}

func BenchmarkMemTableGet(b *testing.B) {
	mt := NewMemTableWithSize(64 * 1024 * 1024)
	row := Row{
		Version: 1,
		Columns: map[string]common.Value{
			benchColName:  common.NewString(benchValName),
			benchColScore: common.NewFloat64(95.5),
		},
	}

	const numKeys = 10000
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("key_%016d", i)
		_, _, _ = mt.Put(key, row)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key_%016d", i%numKeys)
		_, _ = mt.Get(key)
	}
	b.ReportAllocs()
}

func BenchmarkMemTableScan(b *testing.B) {
	mt := NewMemTableWithSize(64 * 1024 * 1024)
	row := Row{
		Version: 1,
		Columns: map[string]common.Value{
			benchColName:  common.NewString(benchValName),
			benchColScore: common.NewFloat64(95.5),
		},
	}

	const numKeys = 10000
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("key_%016d", i)
		_, _, _ = mt.Put(key, row)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := fmt.Sprintf("key_%016d", i%5000)
		end := fmt.Sprintf("key_%016d", (i%5000)+1000)
		_ = mt.Scan(start, end)
	}
	b.ReportAllocs()
}

// --- Engine 写入基准测试 ---

func BenchmarkEngineWrite(b *testing.B) {
	dir, err := os.MkdirTemp("", "bench-engine-*")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	eng, err := NewEngine(EngineConfig{
		DataDir:         dir,
		MaxMemTableSize: 64 * 1024 * 1024,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	vals := map[string]common.Value{
		benchColName:  common.NewString(benchValName),
		benchColScore: common.NewFloat64(95.5),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key_%016d", i)
		_ = eng.Write(key, vals)
	}
	b.ReportAllocs()
}

func BenchmarkEngineWriteParallel(b *testing.B) {
	dir, err := os.MkdirTemp("", "bench-engine-parallel-*")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	eng, err := NewEngine(EngineConfig{
		DataDir:         dir,
		MaxMemTableSize: 64 * 1024 * 1024,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	vals := map[string]common.Value{
		benchColName:  common.NewString(benchValName),
		benchColScore: common.NewFloat64(95.5),
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key_%016d", i)
			_ = eng.Write(key, vals)
			i++
		}
	})
	b.ReportAllocs()
}

// --- Engine 读取基准测试 ---

func BenchmarkEngineGet(b *testing.B) {
	dir, err := os.MkdirTemp("", "bench-engine-get-*")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	eng, err := NewEngine(EngineConfig{
		DataDir:         dir,
		MaxMemTableSize: 64 * 1024 * 1024,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	vals := map[string]common.Value{
		benchColName:  common.NewString(benchValName),
		benchColScore: common.NewFloat64(95.5),
	}

	const numKeys = 10000
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("key_%016d", i)
		_ = eng.Write(key, vals)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key_%016d", i%numKeys)
		_, _ = eng.Get(key)
	}
	b.ReportAllocs()
}

// --- Engine Scan 基准测试 ---

func BenchmarkEngineScanRange(b *testing.B) {
	dir, err := os.MkdirTemp("", "bench-engine-scan-*")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	eng, err := NewEngine(EngineConfig{
		DataDir:         dir,
		MaxMemTableSize: 64 * 1024 * 1024,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	vals := map[string]common.Value{
		benchColName:  common.NewString(benchValName),
		benchColScore: common.NewFloat64(95.5),
	}

	const numKeys = 10000
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("key_%016d", i)
		_ = eng.Write(key, vals)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := fmt.Sprintf("key_%016d", i%5000)
		end := fmt.Sprintf("key_%016d", (i%5000)+100)
		_ = eng.ScanRange(start, end)
	}
	b.ReportAllocs()
}

// --- WAL 基准测试 ---

func BenchmarkWALAppend(b *testing.B) {
	dir, err := os.MkdirTemp("", "bench-wal-*")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	wal, err := CreateWAL(filepath.Join(dir, "wal.log"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = wal.Close() }()

	data := []byte("benchmark test data for WAL append performance")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wal.AppendWrite(data)
	}
	b.ReportAllocs()
}

// --- 编码基准测试 ---

func BenchmarkEncodePlain(b *testing.B) {
	const size = 10000
	values := make([]int64, size)
	for i := range values {
		values[i] = int64(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = EncodeColumn(common.TypeInt64, values, size, nil)
	}
	b.ReportAllocs()
}

func BenchmarkDecodePlain(b *testing.B) {
	const size = 10000
	values := make([]int64, size)
	for i := range values {
		values[i] = int64(i)
	}
	encoded, _ := EncodeColumn(common.TypeInt64, values, size, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = DecodeColumn(encoded)
	}
	b.ReportAllocs()
}

func BenchmarkEncodeDict(b *testing.B) {
	const size = 10000
	values := make([]string, size)
	for i := range values {
		values[i] = fmt.Sprintf("value_%d", i%100)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = EncodeColumn(common.TypeString, values, size, nil)
	}
	b.ReportAllocs()
}

func BenchmarkDecodeDict(b *testing.B) {
	const size = 10000
	values := make([]string, size)
	for i := range values {
		values[i] = fmt.Sprintf("value_%d", i%100)
	}
	encoded, _ := EncodeColumn(common.TypeString, values, size, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = DecodeColumn(encoded)
	}
	b.ReportAllocs()
}

func BenchmarkEncodeRLE(b *testing.B) {
	const size = 10000
	values := make([]int64, size)
	for i := range values {
		values[i] = int64(i / 100)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = EncodeColumn(common.TypeInt64, values, size, nil)
	}
	b.ReportAllocs()
}

func BenchmarkDecodeRLE(b *testing.B) {
	const size = 10000
	values := make([]int64, size)
	for i := range values {
		values[i] = int64(i / 100)
	}
	encoded, _ := EncodeColumn(common.TypeInt64, values, size, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = DecodeColumn(encoded)
	}
	b.ReportAllocs()
}

// --- 压缩基准测试 ---

func BenchmarkCompress(b *testing.B) {
	data := make([]byte, 8192)
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Compress(data)
	}
	b.ReportAllocs()
}

func BenchmarkDecompress(b *testing.B) {
	data := make([]byte, 8192)
	for i := range data {
		data[i] = byte(i % 256)
	}
	compressed := Compress(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Decompress(compressed)
	}
	b.ReportAllocs()
}

// --- ColumnVector 基准测试 ---

func BenchmarkColumnVectorAppend(b *testing.B) {
	col := NewColumnVector(0, common.TypeInt64, uint32(b.N))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = col.Append(common.NewInt64(int64(i)))
	}
	b.ReportAllocs()
}

func BenchmarkColumnVectorGetValue(b *testing.B) {
	const size = 10000
	col := NewColumnVector(0, common.TypeInt64, size)
	for i := 0; i < size; i++ {
		_ = col.Append(common.NewInt64(int64(i)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = col.GetValue(uint32(i % size))
	}
	b.ReportAllocs()
}
