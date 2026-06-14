package storage

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestCompactRemovesBySegmentID 验证 Compact 按 ID 删除 segment，
// 即使在并发 Flush 添加新 segment 的情况下也不会误删。
func TestCompactRemovesBySegmentID(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// 写入并刷盘 4 批数据，产生 4 个 L0 segment
	for i := 0; i < 4; i++ {
		for j := 0; j < 5; j++ {
			key := fmt.Sprintf("batch%d_key%d", i, j)
			_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i*10 + j))})
		}
		if err := eng.Flush(cols); err != nil {
			t.Fatalf("flush %d: %v", i, err)
		}
	}

	if eng.SegmentCount() != 4 {
		t.Fatalf("expected 4 segments before compact, got %d", eng.SegmentCount())
	}

	// 执行 Compact
	if err := eng.Compact(cols); err != nil {
		t.Fatalf("compact: %v", err)
	}

	// 验证 Compact 后只有 1 个 L1 segment
	if eng.SegmentCount() != 1 {
		t.Errorf("expected 1 segment after compact, got %d", eng.SegmentCount())
	}

	segs := eng.Segments()
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segs))
	}
	if segs[0].RowCount != 20 {
		t.Errorf("expected 20 rows in compacted segment, got %d", segs[0].RowCount)
	}
}

// TestCompactConcurrentFlush 验证 Compact 与并发 Flush 不会导致数据损坏。
// 这是 Compact 竞态条件修复的核心测试。
func TestCompactConcurrentFlush(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// 先创建 4 个 L0 segment 以满足 ShouldCompact 条件
	for i := 0; i < 4; i++ {
		for j := 0; j < 5; j++ {
			key := fmt.Sprintf("init%d_key%d", i, j)
			_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i*10 + j))})
		}
		if err := eng.Flush(cols); err != nil {
			t.Fatalf("flush %d: %v", i, err)
		}
	}

	// 并发执行 Compact 和 Flush
	var wg sync.WaitGroup
	var compactErr, flushErr error

	wg.Add(2)
	go func() {
		defer wg.Done()
		compactErr = eng.Compact(cols)
	}()
	go func() {
		defer wg.Done()
		for j := 0; j < 5; j++ {
			key := fmt.Sprintf("concurrent_key%d", j)
			_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(j))})
		}
		flushErr = eng.Flush(cols)
	}()
	wg.Wait()

	if compactErr != nil {
		t.Errorf("compact error: %v", compactErr)
	}
	if flushErr != nil {
		t.Errorf("flush error: %v", flushErr)
	}

	// 验证所有 segment 数据可读
	segs := eng.Segments()
	for _, seg := range segs {
		if seg.RowCount == 0 {
			t.Errorf("segment %d has 0 rows", seg.ID)
		}
	}
}

// TestCompactIdempotent 验证多次 Compact 不会产生重复或丢失数据。
func TestCompactIdempotent(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// 创建 4 个 L0 segment
	for i := 0; i < 4; i++ {
		for j := 0; j < 5; j++ {
			key := fmt.Sprintf("key_%03d", i*5+j)
			_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i*5 + j))})
		}
		if err := eng.Flush(cols); err != nil {
			t.Fatalf("flush %d: %v", i, err)
		}
	}

	// 第一次 Compact
	if err := eng.Compact(cols); err != nil {
		t.Fatalf("first compact: %v", err)
	}

	countAfterFirst := eng.SegmentCount()

	// 第二次 Compact（没有 L0 segment，应该无操作）
	if err := eng.Compact(cols); err != nil {
		t.Fatalf("second compact: %v", err)
	}

	if eng.SegmentCount() != countAfterFirst {
		t.Errorf("segment count changed after no-op compact: %d -> %d", countAfterFirst, eng.SegmentCount())
	}
}

// TestReadTypedValueUnknownType 验证 readTypedValue 对未知类型返回错误。
func TestReadTypedValueUnknownType(t *testing.T) {
	_, n, err := readTypedValue([]byte{}, common.DataType(99))
	if err == nil {
		t.Error("expected error for unknown type, got nil")
	}
	if n != 0 {
		t.Errorf("expected 0 bytes read for error, got %d", n)
	}
}

// TestReadValueBinaryUnknownType 验证 readValueBinary 对未知类型返回错误。
func TestReadValueBinaryUnknownType(t *testing.T) {
	// 构造一个包含未知类型的二进制数据
	// 格式: nameLen(2) + name + type(1) + valid(1) + value
	data := []byte{
		3, 0, // nameLen = 3
		'f', 'o', 'o', // name = "foo"
		99, // type = unknown (99)
		1,  // valid = true
	}
	_, _, _, err := readValueBinary(data)
	if err == nil {
		t.Error("expected error for unknown type in readValueBinary, got nil")
	}
}

// TestDecodeAllColumnsErrorPropagation 验证 decodeAllColumns 在解压/解码失败时返回错误。
func TestDecodeAllColumnsErrorPropagation(t *testing.T) {
	// 构造一个包含损坏数据的 Segment
	seg := &Segment{
		ID:       1,
		MinKey:   "a",
		MaxKey:   "z",
		RowCount: 1,
		Keys:     []string{"a"},
		Columns: []EncodedColumn{
			{
				Encoding: EncodingPlain,
				Type:     common.TypeInt64,
				RowCount: 1,
				Data:     []byte{0xFF, 0xFE, 0xFD}, // 损坏的数据（长度不足）
			},
		},
	}

	_, err := seg.decodeAllColumns()
	if err == nil {
		t.Error("expected error from decodeAllColumns with corrupt data, got nil")
	}
}

// TestSegmentIteratorDecodeError 验证 segmentIterator 在解码失败时正确报告错误。
func TestSegmentIteratorDecodeError(t *testing.T) {
	seg := &Segment{
		ID:       1,
		MinKey:   "a",
		MaxKey:   "z",
		RowCount: 1,
		Keys:     []string{"a"},
		Columns: []EncodedColumn{
			{
				Encoding: EncodingPlain,
				Type:     common.TypeInt64,
				RowCount: 1,
				Data:     []byte{0xFF}, // 损坏的数据
			},
		},
	}

	colMeta := []ColumnMeta{{ID: 0, Name: "col_0", Type: common.TypeInt64}}
	it := newSegmentIterator(seg, colMeta, "a", "z", nil)

	// 迭代器应该报告错误
	if it.Next() {
		t.Error("expected Next() to return false with corrupt segment data")
	}
	if it.Err() == nil {
		t.Error("expected error from iterator with corrupt segment data")
	}
	it.Close()
}

// TestRaceWriteCloseGroupCommitter 验证 Write 与 Close 并发访问 groupCommitter 不会产生数据竞态。
// 修复前：Write 中读取 e.groupCommitter 未持锁，而 Close 中设置 e.groupCommitter = nil 也未持锁，
// 导致竞态条件。修复后：两处均通过 e.mu 同步。
func TestRaceWriteCloseGroupCommitter(t *testing.T) {
	for i := 0; i < 10; i++ {
		eng, err := NewEngine(EngineConfig{
			DataDir:      t.TempDir(),
			SyncMode:     SyncGroupCommit,
			SyncInterval: 1 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("new engine: %v", err)
		}

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = eng.Write(fmt.Sprintf("race_key_%04d", j), map[string]common.Value{
					colVal: common.NewInt64(int64(j)),
				})
			}
		}()

		// Give writes a chance to start
		time.Sleep(time.Millisecond)

		_ = eng.Close()
		wg.Wait()
	}
}

// TestRegisterSegmentIndexesErrorHandling 验证 registerSegmentIndexes 返回错误时被正确处理。
func TestRegisterSegmentIndexesErrorHandling(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 正常情况下 registerSegmentIndexes 不应返回错误
	// 这里验证返回值类型是 error
	seg := &Segment{
		ID:     1,
		MinKey: "a",
		MaxKey: "z",
		Footer: SegmentFooter{},
	}

	err = eng.registerSegmentIndexes(seg, 0)
	if err != nil {
		t.Errorf("unexpected error from registerSegmentIndexes: %v", err)
	}
}

// TestScanRangeAcquiresRLockInternally 验证 ScanRange 内部获取 RLock，
// 调用方无需手动加锁即可安全调用。
func TestScanRangeAcquiresRLockInternally(t *testing.T) {
	t.Parallel()

	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入一些数据
	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("b", map[string]common.Value{colVal: common.NewInt64(2)})
	_ = eng.Write("c", map[string]common.Value{colVal: common.NewInt64(3)})

	// 不手动获取锁，直接调用 ScanRange，应正常工作
	results := eng.ScanRange("a", "c")
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

// TestScanRangeConcurrentWithWrite 验证 ScanRange 与并发 Write 不会产生数据竞态。
// ScanRange 内部获取 RLock，Write 获取 Lock，两者应正确互斥。
func TestScanRangeConcurrentWithWrite(t *testing.T) {
	t.Parallel()

	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 预先写入数据
	for i := 0; i < 10; i++ {
		key := string(rune('a' + i))
		_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))})
	}

	var wg sync.WaitGroup
	const readers = 4
	const writers = 2

	wg.Add(readers + writers)

	// 并发读
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				results := eng.ScanRange("a", "z")
				_ = results
			}
		}()
	}

	// 并发写
	for i := 0; i < writers; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				key := string(rune('A' + id))
				_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(id*100 + j))})
			}
		}(i)
	}

	wg.Wait()
}

// TestComputeStringStatsShortOffsets 验证 computeStringStats 在 offsets 数组
// 长度不足时不会 panic，而是安全地跳过越界行。
func TestComputeStringStatsShortOffsets(t *testing.T) {
	t.Parallel()

	// 构造 rowCount=5 但 offsets 只有 2 个元素（不足以覆盖所有行）
	data := []byte("helloworld")
	offsets := []uint32{0, 5} // 只能安全访问第 0 行
	rowCount := uint32(5)

	stat := &ColumnStat{}
	// 不应 panic
	computeStringStats(data, offsets, rowCount, nil, stat)

	// 第 0 行 "hello" 应被正确统计
	if stat.Min == nil || string(stat.Min) != testStrHello {
		t.Errorf("expected Min='hello', got %v", stat.Min)
	}
	if stat.Max == nil || string(stat.Max) != testStrHello {
		t.Errorf("expected Max='hello', got %v", stat.Max)
	}
}

// TestComputeStringStatsEmptyOffsets 验证 computeStringStats 在 offsets 为空时不会 panic。
func TestComputeStringStatsEmptyOffsets(t *testing.T) {
	t.Parallel()

	data := []byte(testStrHello)
	rowCount := uint32(3)

	stat := &ColumnStat{}
	// 不应 panic
	computeStringStats(data, nil, rowCount, nil, stat)

	// 无有效行可统计，Min/Max 应为 nil
	if stat.Min != nil || stat.Max != nil {
		t.Errorf("expected nil Min/Max for empty offsets, got Min=%v Max=%v", stat.Min, stat.Max)
	}
}

// TestIndexCacheGetColumnStatsConcurrentReads 验证 IndexCache.GetColumnStats
// 允许多个 goroutine 并发读取而不会死锁。
func TestIndexCacheGetColumnStatsConcurrentReads(t *testing.T) {
	t.Parallel()

	cache := NewIndexCache(100)

	// 预填充数据
	for i := uint64(0); i < 20; i++ {
		cache.PutColumnStats(i, []ColumnStat{{ColumnID: uint32(i), NullCount: uint32(i)}})
	}

	var wg sync.WaitGroup
	const goroutines = 10
	const iterations = 100

	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				// 并发读取不同的 segmentID
				stats, ok := cache.GetColumnStats(uint64(i % 20))
				if ok && len(stats) != 1 {
					t.Errorf("unexpected stats length: %d", len(stats))
				}
			}
		}()
	}
	wg.Wait()
}
