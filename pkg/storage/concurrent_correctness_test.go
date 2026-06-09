package storage

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func ccIntVal(v int64) map[string]common.Value {
	return map[string]common.Value{colVal: common.NewInt64(v)}
}

func ccVerifyKeys(t *testing.T, eng *Engine, groups, perGroup int, prefix, suffix string, valFn func(g, j int) int64) {
	t.Helper()
	for g := 0; g < groups; g++ {
		for j := 0; j < perGroup; j++ {
			key := fmt.Sprintf("%s%d%s%04d", prefix, g, suffix, j)
			expected := valFn(g, j)
			row, ok := eng.Get(key)
			if !ok {
				t.Errorf("key %s not found", key)
				continue
			}
			if row.Columns[colVal].Int64 != expected {
				t.Errorf("key %s: expected %d, got %d", key, expected, row.Columns[colVal].Int64)
			}
		}
	}
}

func ccCheckScanSorted(results []struct {
	Key   string
	Value Row
}, start, end string) bool {
	for i := 1; i < len(results); i++ {
		if results[i].Key <= results[i-1].Key {
			return false
		}
	}
	for _, r := range results {
		if r.Key < start || r.Key > end {
			return false
		}
	}
	return true
}

// ccStartWriters launches goroutines that write keys with prefix "zzz_{prefix}{gid}_{i:04d}" until done.
func ccStartWriters(eng *Engine, prefix string, count int, done <-chan struct{}, wg *sync.WaitGroup) {
	for g := 0; g < count; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-done:
					return
				default:
					_ = eng.Write(fmt.Sprintf("zzz_%s%d_%04d", prefix, gid, i), ccIntVal(int64(i)))
				}
			}
		}(g)
	}
}

// ccStartFlushCompact runs Flush+Compact in a loop until done.
func ccStartFlushCompact(eng *Engine, cols []ColumnMeta, done <-chan struct{}, wg *sync.WaitGroup, ops *atomic.Int64) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				_ = eng.Flush(cols)
				if eng.ShouldCompact() {
					_ = eng.Compact(cols)
				}
				if ops != nil {
					ops.Add(1)
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
	}()
}

// ccCheckNoDuplicates returns true if there are duplicate keys in results.
func ccCheckNoDuplicates(results []struct {
	Key   string
	Value Row
}) bool {
	seen := make(map[string]struct{}, len(results))
	for _, r := range results {
		if _, dup := seen[r.Key]; dup {
			return true
		}
		seen[r.Key] = struct{}{}
	}
	return false
}

func TestConcurrentCorrectness_Serializability(t *testing.T) {
	defer suppressLog()()
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	const writers, perWriter = 8, 100
	var wg sync.WaitGroup
	var writeErr atomic.Int32
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				if err := eng.Write(fmt.Sprintf("ser_g%d_%04d", gid, j), ccIntVal(int64(gid*10000+j))); err != nil {
					writeErr.Add(1)
				}
			}
		}(g)
	}
	wg.Wait()
	if writeErr.Load() > 0 {
		t.Fatalf("write errors: %d", writeErr.Load())
	}
	ccVerifyKeys(t, eng, writers, perWriter, "ser_g", "_", func(g, j int) int64 {
		return int64(g*10000 + j)
	})
}

func TestConcurrentCorrectness_OverwriteSerialEquivalence(t *testing.T) {
	defer suppressLog()()
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	const overwriters = 10
	var wg sync.WaitGroup
	for g := 0; g < overwriters; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = eng.Write("shared_key", ccIntVal(int64(gid)))
			}
		}(g)
	}
	wg.Wait()

	row, ok := eng.Get("shared_key")
	if !ok {
		t.Fatal("shared_key not found")
	}
	if val := row.Columns[colVal].Int64; val < 0 || val >= overwriters {
		t.Errorf("unexpected value %d, expected one of [0, %d]", val, overwriters-1)
	}
	for g := 0; g < overwriters; g++ {
		_ = eng.Write("seq_key", ccIntVal(int64(g)))
	}
	row2, ok := eng.Get("seq_key")
	if !ok {
		t.Fatal("seq_key not found")
	}
	if row2.Columns[colVal].Int64 != overwriters-1 {
		t.Errorf("seq_key: expected %d, got %d", overwriters-1, row2.Columns[colVal].Int64)
	}
}

func TestConcurrentCorrectness_CompactAndScanInvariant(t *testing.T) {
	defer suppressLog()()
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()
	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	for i := 0; i < 50; i++ {
		_ = eng.Write(fmt.Sprintf("scan_%04d", i), ccIntVal(int64(i)))
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("initial flush: %v", err)
	}

	done := make(chan struct{})
	time.AfterFunc(200*time.Millisecond, func() { close(done) })
	var wg sync.WaitGroup
	var scanErr atomic.Int32

	ccStartWriters(eng, "out", 4, done, &wg)
	ccStartFlushCompact(eng, cols, done, &wg, nil)

	for s := 0; s < 4; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					results := eng.Scan("scan_0000", "scan_0049")
					if !ccCheckScanSorted(results, "scan_0000", "scan_0049") {
						scanErr.Add(1)
					}
					if ccCheckNoDuplicates(results) {
						scanErr.Add(1)
					}
				}
			}
		}()
	}
	wg.Wait()
	if scanErr.Load() > 0 {
		t.Errorf("scan invariant violations: %d", scanErr.Load())
	}
}

func TestConcurrentCorrectness_WriteBatchAtomicity(t *testing.T) {
	defer suppressLog()()
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	const batches, keysPerBatch = 10, 5
	var wg sync.WaitGroup
	var batchErr atomic.Int32
	for b := 0; b < batches; b++ {
		wg.Add(1)
		go func(bid int) {
			defer wg.Done()
			rows := make([]WriteRow, keysPerBatch)
			for k := 0; k < keysPerBatch; k++ {
				rows[k] = WriteRow{
					Key:    fmt.Sprintf("bat%d_k%d", bid, k),
					Values: ccIntVal(int64(bid*100 + k)),
				}
			}
			if err := eng.WriteBatch(rows); err != nil {
				batchErr.Add(1)
			}
		}(b)
	}
	wg.Wait()
	if batchErr.Load() > 0 {
		t.Fatalf("batch write errors: %d", batchErr.Load())
	}
	for b := 0; b < batches; b++ {
		for k := 0; k < keysPerBatch; k++ {
			key := fmt.Sprintf("bat%d_k%d", b, k)
			row, ok := eng.Get(key)
			if !ok {
				t.Errorf("batch %d key %s not found", b, key)
			} else if row.Columns[colVal].Int64 != int64(b*100+k) {
				t.Errorf("key %s: expected %d, got %d", key, b*100+k, row.Columns[colVal].Int64)
			}
		}
	}
}

func TestConcurrentCorrectness_RecoveryAfterConcurrentWrite(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}
	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	const writers, perWriter = 4, 50
	var wg sync.WaitGroup
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				_ = eng.Write(fmt.Sprintf("rcv_g%d_%04d", gid, j), ccIntVal(int64(gid*10000+j)))
			}
		}(g)
	}
	wg.Wait()
	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}
	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()
	ccVerifyKeys(t, eng2, writers, perWriter, "rcv_g", "_", func(g, j int) int64 {
		return int64(g*10000 + j)
	})
}

func TestConcurrentCorrectness_LongRunningStress(t *testing.T) {
	defer suppressLog()()
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()
	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	for i := 0; i < 50; i++ {
		_ = eng.Write(fmt.Sprintf("pre_%04d", i), ccIntVal(int64(i)))
	}
	duration := 2 * time.Second
	if testing.Short() {
		duration = 300 * time.Millisecond
	}
	done := make(chan struct{})
	time.AfterFunc(duration, func() { close(done) })
	var wg sync.WaitGroup
	var totalOps atomic.Int64
	var errors atomic.Int32

	ccStartWriters(eng, "str", 4, done, &wg)
	ccStartFlushCompact(eng, cols, done, &wg, &totalOps)

	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					_, _ = eng.Get("pre_0025")
					totalOps.Add(1)
				}
			}
		}()
	}
	for s := 0; s < 2; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					results := eng.Scan("pre_0000", "pre_0049")
					if !ccCheckScanSorted(results, "pre_0000", "pre_0049") {
						errors.Add(1)
					}
					totalOps.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	t.Logf("Stress: %d ops, %d errors in %v", totalOps.Load(), errors.Load(), duration)
	if errors.Load() > 0 {
		t.Errorf("stress test errors: %d", errors.Load())
	}
}

func TestConcurrentCorrectness_ScanRangeConsistency(t *testing.T) {
	defer suppressLog()()
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()
	for i := 0; i < 50; i++ {
		_ = eng.Write(fmt.Sprintf("range_%04d", i), ccIntVal(int64(i)))
	}
	done := make(chan struct{})
	time.AfterFunc(200*time.Millisecond, func() { close(done) })
	var wg sync.WaitGroup
	var scanErr atomic.Int32

	ccStartWriters(eng, "out", 4, done, &wg)

	for s := 0; s < 4; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					results := eng.Scan("range_0000", "range_0049")
					if len(results) != 50 {
						scanErr.Add(1)
						continue
					}
					for i, r := range results {
						if r.Key != fmt.Sprintf("range_%04d", i) || r.Value.Columns[colVal].Int64 != int64(i) {
							scanErr.Add(1)
							break
						}
					}
				}
			}
		}()
	}
	wg.Wait()
	if scanErr.Load() > 0 {
		t.Errorf("scan range consistency violations: %d", scanErr.Load())
	}
}

func TestConcurrentCorrectness_CompactDataIntegrity(t *testing.T) {
	defer suppressLog()()
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()
	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	for i := 0; i < 50; i++ {
		_ = eng.Write(fmt.Sprintf("data_%04d", i), ccIntVal(int64(i)))
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("initial flush: %v", err)
	}
	const writers, perWriter = 4, 50
	var wg sync.WaitGroup
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				_ = eng.Write(fmt.Sprintf("cw%d_%04d", gid, j), ccIntVal(int64(gid*10000+j)))
			}
		}(g)
	}
	var bgWg sync.WaitGroup
	bgWg.Add(1)
	go func() {
		defer bgWg.Done()
		for i := 0; i < 5; i++ {
			time.Sleep(20 * time.Millisecond)
			_ = eng.Flush(cols)
		}
	}()
	bgWg.Add(1)
	go func() {
		defer bgWg.Done()
		time.Sleep(50 * time.Millisecond)
		if eng.ShouldCompact() {
			_ = eng.Compact(cols)
		}
	}()
	wg.Wait()
	bgWg.Wait()
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("final flush: %v", err)
	}
	if eng.ShouldCompact() {
		if err := eng.Compact(cols); err != nil {
			t.Fatalf("final compact: %v", err)
		}
	}
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("data_%04d", i)
		row, ok := eng.Get(key)
		if !ok {
			t.Errorf("initial key %s not found", key)
		} else if row.Columns[colVal].Int64 != int64(i) {
			t.Errorf("key %s: expected %d, got %d", key, i, row.Columns[colVal].Int64)
		}
	}
	ccVerifyKeys(t, eng, writers, perWriter, "cw", "_", func(g, j int) int64 {
		return int64(g*10000 + j)
	})
}
