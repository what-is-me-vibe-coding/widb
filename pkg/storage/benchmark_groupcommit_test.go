package storage

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// --- GroupCommit 写入基准测试 ---

func BenchmarkEngineWriteGroupCommit(b *testing.B) {
	dir, err := os.MkdirTemp("", "bench-engine-gc-*")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	eng, err := NewEngine(EngineConfig{
		DataDir:         dir,
		MaxMemTableSize: 64 * 1024 * 1024,
		SyncMode:        SyncGroupCommit,
		SyncInterval:    1 * time.Millisecond,
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

func BenchmarkEngineWriteGroupCommitParallel(b *testing.B) {
	dir, err := os.MkdirTemp("", "bench-engine-gc-parallel-*")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	eng, err := NewEngine(EngineConfig{
		DataDir:         dir,
		MaxMemTableSize: 64 * 1024 * 1024,
		SyncMode:        SyncGroupCommit,
		SyncInterval:    1 * time.Millisecond,
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

func BenchmarkEngineWriteBatchGroupCommit(b *testing.B) {
	dir, err := os.MkdirTemp("", "bench-engine-batch-gc-*")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	eng, err := NewEngine(EngineConfig{
		DataDir:         dir,
		MaxMemTableSize: 64 * 1024 * 1024,
		SyncMode:        SyncGroupCommit,
		SyncInterval:    1 * time.Millisecond,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	const batchSize = 100
	batch := make([]WriteRow, batchSize)
	for i := range batch {
		batch[i] = WriteRow{
			Key: fmt.Sprintf("key_%016d", i),
			Values: map[string]common.Value{
				benchColName:  common.NewString(benchValName),
				benchColScore: common.NewFloat64(95.5),
			},
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range batch {
			batch[j].Key = fmt.Sprintf("key_%016d_%d", i, j)
		}
		_ = eng.WriteBatch(batch)
	}
	b.ReportAllocs()
}
