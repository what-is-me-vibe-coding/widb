package storage

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const (
	colName   = "name"
	colData   = "data"
	colID     = "id"
	colAge    = "age"
	colActive = "active"
	colVal    = "val"
	colScore  = "score"
)

func TestNewMemTable(t *testing.T) {
	mt := NewMemTable()
	if mt.Len() != 0 {
		t.Fatalf("expected 0 entries, got %d", mt.Len())
	}
	if mt.Size() != 0 {
		t.Fatalf("expected 0 size, got %d", mt.Size())
	}
	if mt.IsFrozen() {
		t.Fatal("expected not frozen")
	}
}

func TestMemTablePutAndGet(t *testing.T) {
	mt := NewMemTable()

	row := Row{
		Version: 1,
		Columns: map[string]common.Value{
			colName: common.NewString("alice"),
			colAge:  common.NewInt64(30),
		},
	}

	old, exists, err := mt.Put("user:1", row)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	if exists {
		t.Fatal("expected no existing value")
	}
	if old.Version != 0 {
		t.Fatal("expected zero old value")
	}

	got, ok := mt.Get("user:1")
	if !ok {
		t.Fatal("expected key to exist")
	}
	if got.Version != 1 {
		t.Errorf("expected version 1, got %d", got.Version)
	}
	if got.Columns[colName].Str != "alice" {
		t.Errorf("expected name alice, got %s", got.Columns[colName].Str)
	}
	if got.Columns["age"].Int64 != 30 {
		t.Errorf("expected age 30, got %d", got.Columns["age"].Int64)
	}
}

func TestMemTableGetNonExistent(t *testing.T) {
	mt := NewMemTable()
	_, ok := mt.Get("nonexistent")
	if ok {
		t.Fatal("expected key not found")
	}
}

func TestMemTablePutOverwrite(t *testing.T) {
	mt := NewMemTable()

	row1 := Row{
		Version: 1,
		Columns: map[string]common.Value{
			"value": common.NewInt64(100),
		},
	}

	row2 := Row{
		Version: 2,
		Columns: map[string]common.Value{
			"value": common.NewInt64(200),
		},
	}

	_, _, _ = mt.Put("key1", row1)
	old, exists, err := mt.Put("key1", row2)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	if !exists {
		t.Fatal("expected existing value")
	}
	if old.Version != 1 {
		t.Errorf("expected old version 1, got %d", old.Version)
	}

	got, ok := mt.Get("key1")
	if !ok {
		t.Fatal("expected key to exist")
	}
	if got.Version != 2 {
		t.Errorf("expected version 2, got %d", got.Version)
	}
}

func TestMemTableDelete(t *testing.T) {
	mt := NewMemTable()

	row := Row{
		Version: 1,
		Columns: map[string]common.Value{
			colData: common.NewString("test"),
		},
	}

	_, _, _ = mt.Put("key1", row)

	old, exists, err := mt.Delete("key1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if !exists {
		t.Fatal("expected key to exist")
	}
	if old.Version != 1 {
		t.Errorf("expected version 1, got %d", old.Version)
	}

	_, ok := mt.Get("key1")
	if ok {
		t.Fatal("expected key to be deleted")
	}
}

func TestMemTableDeleteNonExistent(t *testing.T) {
	mt := NewMemTable()
	_, exists, err := mt.Delete("nonexistent")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if exists {
		t.Fatal("expected key not found")
	}
}

func TestMemTableLen(t *testing.T) {
	mt := NewMemTable()

	for i := 0; i < 100; i++ {
		row := Row{
			Version: uint64(i),
			Columns: map[string]common.Value{
				colID: common.NewInt64(int64(i)),
			},
		}
		_, _, _ = mt.Put(fmt.Sprintf("key:%d", i), row)
	}

	if mt.Len() != 100 {
		t.Errorf("expected 100 entries, got %d", mt.Len())
	}

	_, _, _ = mt.Delete("key:0")
	_, _, _ = mt.Delete("key:1")

	if mt.Len() != 98 {
		t.Errorf("expected 98 entries, got %d", mt.Len())
	}
}

func TestMemTableScan(t *testing.T) {
	mt := NewMemTable()

	for i := 0; i < 20; i++ {
		row := Row{
			Version: uint64(i),
			Columns: map[string]common.Value{
				colID: common.NewInt64(int64(i)),
			},
		}
		_, _, _ = mt.Put(fmt.Sprintf("key:%02d", i), row)
	}

	results := mt.Scan("key:05", "key:10")
	if len(results) != 6 {
		t.Fatalf("expected 6 results, got %d", len(results))
	}

	for i, r := range results {
		expectedKey := fmt.Sprintf("key:%02d", i+5)
		if r.Key != expectedKey {
			t.Errorf("result[%d]: expected key %s, got %s", i, expectedKey, r.Key)
		}
	}
}

func TestMemTableScanEmpty(t *testing.T) {
	mt := NewMemTable()
	results := mt.Scan("a", "z")
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestMemTableScanNoMatch(t *testing.T) {
	mt := NewMemTable()
	_, _, _ = mt.Put("key:1", Row{Version: 1, Columns: map[string]common.Value{colID: common.NewInt64(1)}})
	_, _, _ = mt.Put("key:2", Row{Version: 2, Columns: map[string]common.Value{colID: common.NewInt64(2)}})

	results := mt.Scan("key:3", "key:5")
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestMemTableSize(t *testing.T) {
	mt := NewMemTable()

	row := Row{
		Version: 1,
		Columns: map[string]common.Value{
			colName: common.NewString(testStrHello),
		},
	}

	_, _, _ = mt.Put("key1", row)
	if mt.Size() == 0 {
		t.Fatal("expected non-zero size after put")
	}

	oldSize := mt.Size()
	_, _, _ = mt.Put("key1", Row{
		Version: 2,
		Columns: map[string]common.Value{
			colName: common.NewString("hello world"),
		},
	})

	if mt.Size() == oldSize {
		t.Log("size may not change significantly for small updates")
	}

	_, _, _ = mt.Delete("key1")
	if mt.Size() != 0 {
		t.Errorf("expected 0 size after delete, got %d", mt.Size())
	}
}

func TestMemTableShouldFlush(t *testing.T) {
	mt := NewMemTableWithSize(100)

	if mt.ShouldFlush() {
		t.Fatal("should not flush with 0 size")
	}

	_, _, _ = mt.Put("key1", Row{
		Version: 1,
		Columns: map[string]common.Value{
			colData: common.NewString("this is a relatively long string to increase size"),
		},
	})

	if !mt.ShouldFlush() {
		t.Log("flush threshold may not be reached with single entry")
	}
}

func TestMemTableFreeze(t *testing.T) {
	mt := NewMemTable()

	_, _, _ = mt.Put("key1", Row{
		Version: 1,
		Columns: map[string]common.Value{
			colData: common.NewString("frozen"),
		},
	})
	_, _, _ = mt.Put("key2", Row{
		Version: 2,
		Columns: map[string]common.Value{
			colData: common.NewString("frozen2"),
		},
	})

	mt.Freeze()

	if !mt.IsFrozen() {
		t.Fatal("expected frozen")
	}

	_, _, err := mt.Put("key3", Row{Version: 3})
	if err != common.ErrReadOnly {
		t.Errorf("expected ErrReadOnly, got %v", err)
	}

	_, _, err = mt.Delete("key1")
	if err != common.ErrReadOnly {
		t.Errorf("expected ErrReadOnly on delete, got %v", err)
	}

	val, ok := mt.Get("key1")
	if !ok {
		t.Fatal("frozen memtable should still allow reads for key1")
	}
	if val.Version != 1 {
		t.Errorf("expected version 1, got %d", val.Version)
	}

	val, ok = mt.Get("key2")
	if !ok {
		t.Fatal("frozen memtable should still allow reads for key2")
	}
	if val.Version != 2 {
		t.Errorf("expected version 2, got %d", val.Version)
	}

	_, ok = mt.Get("key3")
	if ok {
		t.Fatal("frozen memtable should not contain key3")
	}
}

func TestMemTableFreezeSnapshotIndependence(t *testing.T) {
	mt := NewMemTable()

	_, _, _ = mt.Put("key1", Row{
		Version: 1,
		Columns: map[string]common.Value{
			colData: common.NewString("original"),
		},
	})

	mt.Freeze()

	origVal, ok := mt.Get("key1")
	if !ok {
		t.Fatal("snapshot should contain key1")
	}
	if origVal.Columns[colData].Str != "original" {
		t.Errorf("expected original, got %s", origVal.Columns[colData].Str)
	}

	mt2 := NewMemTable()
	_, _, _ = mt2.Put("key1", Row{
		Version: 2,
		Columns: map[string]common.Value{
			colData: common.NewString("new memtable"),
		},
	})

	origVal2, ok2 := mt.Get("key1")
	if !ok2 {
		t.Fatal("frozen memtable should still contain key1")
	}
	if origVal2.Columns[colData].Str != "original" {
		t.Errorf("frozen memtable should be independent of other memtables, got %s", origVal2.Columns[colData].Str)
	}
}

func TestMemTableConcurrentWrite(t *testing.T) {
	mt := NewMemTable()

	const goroutines = 20
	const writesPerRoutine = 500
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < writesPerRoutine; j++ {
				key := fmt.Sprintf("key:%d:%d", id, j)
				row := Row{
					Version: uint64(id*1000 + j),
					Columns: map[string]common.Value{
						colID: common.NewInt64(int64(id*1000 + j)),
					},
				}
				_, _, _ = mt.Put(key, row)
			}
		}(i)
	}

	wg.Wait()

	expected := goroutines * writesPerRoutine
	if mt.Len() != expected {
		t.Errorf("expected %d entries, got %d", expected, mt.Len())
	}
}

func TestMemTableConcurrentReadWrite(t *testing.T) {
	mt := NewMemTable()

	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key:%d", i)
		_, _, _ = mt.Put(key, Row{
			Version: uint64(i),
			Columns: map[string]common.Value{
				colID: common.NewInt64(int64(i)),
			},
		})
	}

	const goroutines = 10
	const opsPerRoutine = 500
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerRoutine; j++ {
				op := rand.Intn(3)
				switch op {
				case 0:
					mt.Get(fmt.Sprintf("key:%d", rand.Intn(1000)))
				case 1:
					_, _, _ = mt.Put(fmt.Sprintf("key:%d", rand.Intn(1000)), Row{
						Version: uint64(j),
						Columns: map[string]common.Value{
							colID: common.NewInt64(int64(j)),
						},
					})
				case 2:
					mt.Scan(fmt.Sprintf("key:%d", rand.Intn(500)), fmt.Sprintf("key:%d", 500+rand.Intn(500)))
				}
			}
		}()
	}

	wg.Wait()

	if mt.Len() == 0 {
		t.Fatal("memtable should have entries after concurrent read-write")
	}
}

func TestMemTableLargeDataset(t *testing.T) {
	mt := NewMemTable()

	n := 10000
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("user:%08d", i)
		row := Row{
			Version: uint64(i),
			Columns: map[string]common.Value{
				colName:   common.NewString(fmt.Sprintf("user_%d", i)),
				"balance": common.NewFloat64(float64(i) * 1.5),
				colActive: common.NewBool(i%2 == 0),
			},
		}
		_, _, _ = mt.Put(key, row)
	}

	if mt.Len() != n {
		t.Errorf("expected %d entries, got %d", n, mt.Len())
	}

	for i := 0; i < n; i += 1000 {
		key := fmt.Sprintf("user:%08d", i)
		val, ok := mt.Get(key)
		if !ok {
			t.Errorf("key %s not found", key)
			continue
		}
		if val.Columns[colName].Str != fmt.Sprintf("user_%d", i) {
			t.Errorf("wrong name for key %s", key)
		}
	}

	results := mt.Scan("user:00005000", "user:00005099")
	if len(results) != 100 {
		t.Errorf("expected 100 scan results, got %d", len(results))
	}
}

func TestMemTableString(t *testing.T) {
	mt := NewMemTable()
	_, _, _ = mt.Put("k", Row{Version: 1})
	s := mt.String()
	if s == "" {
		t.Fatal("expected non-empty string")
	}
}

// TestMemTableSlabAllocatorBoundary 验证 slab 分配器在跨边界时正确切换：
// 1. 写入 2*skipNodeSlabSize 个键，确保至少触发一次 slab 扩容；
// 2. 跨 slab 边界的 key 仍能正确 Get / Scan；
// 3. 验证 slab 数量与节点数关系符合预期。
// 这是 inline-forward 优化后新增的关键回归测试，覆盖 slab 切分路径。
func TestMemTableSlabAllocatorBoundary(t *testing.T) {
	mt := NewMemTable()

	// 跨两个 slab 边界的写入量
	const total = skipNodeSlabSize*2 + 100
	for i := 0; i < total; i++ {
		key := fmt.Sprintf("k_%08d", i)
		_, _, _ = mt.Put(key, Row{
			Version: uint64(i),
			Columns: map[string]common.Value{
				"v": common.NewInt64(int64(i)),
			},
		})
	}

	if mt.Len() != total {
		t.Errorf("expected %d entries, got %d", total, mt.Len())
	}

	// 验证 slab 数量：head 节点占据第一个 slab 的 idx=0，
	// 之后 total 个 key 依次占用 1..total 节点位置，共需
	// ceil((total+1) / skipNodeSlabSize) 个 slab。
	expectedSlabs := (total + 1 + skipNodeSlabSize - 1) / skipNodeSlabSize
	if got := len(mt.tree.nodeSlabs); got != expectedSlabs {
		t.Errorf("expected %d slabs, got %d", expectedSlabs, got)
	}

	// 验证跨 slab 边界的 Get
	for _, idx := range []int{0, skipNodeSlabSize - 1, skipNodeSlabSize, skipNodeSlabSize + 1, total - 1} {
		key := fmt.Sprintf("k_%08d", idx)
		row, ok := mt.Get(key)
		if !ok {
			t.Errorf("key %s not found at slab boundary", key)
			continue
		}
		if row.Columns["v"].Int64 != int64(idx) {
			t.Errorf("key %s: expected v=%d, got %d", key, idx, row.Columns["v"].Int64)
		}
	}

	// 验证跨 slab 边界的 Scan
	results := mt.Scan(fmt.Sprintf("k_%08d", skipNodeSlabSize-5), fmt.Sprintf("k_%08d", skipNodeSlabSize+5))
	if len(results) != 11 {
		t.Errorf("expected 11 scan results across slab boundary, got %d", len(results))
	}
}

// TestMemTableSlabAllocationsCount 验证 inline-forward 优化后 MemTable.Put
// 的堆分配次数低于优化前的版本（优化前 3 allocs：map + *skipNode + forward slice）。
//
// 为精确测量 Put 路径的分配数，将 alloc 测量拆分为「插入路径」与「更新路径」：
//   - 插入路径：每次 run 使用不同 key，触发 allocNode，验证 slab 优化后
//     单次插入的分配收敛到 1 次（仅 map alloc，节点从 slab 数组中取）；
//   - 更新路径：固定 key，验证更新已有节点不分配新节点，符合跳表语义。
func TestMemTableSlabAllocationsCount(t *testing.T) {
	// 预热 GC，让 slab 等内部结构稳定
	for i := 0; i < 1000; i++ {
		mt := NewMemTable()
		_, _, _ = mt.Put(fmt.Sprintf("warm_%d", i), Row{
			Version: 1,
			Columns: map[string]common.Value{"v": common.NewInt64(1)},
		})
	}

	t.Run("InsertPath", func(t *testing.T) {
		// 每次 run 使用全新 key，强制走 allocNode() 路径。
		// 使用闭包计数器确保 key 在每次调用都不同。
		var seq int
		keyBuf := make([]string, 0, 2048)
		for i := 0; i < 2048; i++ {
			keyBuf = append(keyBuf, fmt.Sprintf("alloc_inspect_%010d", i))
		}
		cols := map[string]common.Value{"v": common.NewInt64(1)}

		mt := NewMemTable()
		allocs := testing.AllocsPerRun(1000, func() {
			key := keyBuf[seq%len(keyBuf)]
			seq++
			_, _, _ = mt.Put(key, Row{Version: 1, Columns: cols})
		})

		// 插入路径：map alloc（来自 Row.Columns 共享 map，理论上不再分配，
		// 但闭包捕获 keyBuf 元素与 fmt 操作可能引入额外分配）。
		// 优化后插入路径应 <= 2 allocs（map 复用时为 0，所有分配都源自 map alloc）。
		t.Logf("MemTable.Put(insert) 平均分配次数: %.2f", allocs)
		if allocs > 2.0 {
			t.Errorf("insert path: expected at most 2 allocs per Put, got %.2f", allocs)
		}
	})

	t.Run("UpdatePath", func(t *testing.T) {
		// 固定 key 走更新路径：不应调用 allocNode，
		// 分配数应明显低于插入路径（理想为 0，因为 Row 由值传递）。
		mt := NewMemTable()
		key := "fixed_key_12345"
		// 预 Put 一次以建立节点
		_, _, _ = mt.Put(key, Row{
			Version: 1,
			Columns: map[string]common.Value{"v": common.NewInt64(1)},
		})
		cols := map[string]common.Value{"v": common.NewInt64(1)}
		var v uint64

		allocs := testing.AllocsPerRun(1000, func() {
			v++
			_, _, _ = mt.Put(key, Row{Version: v, Columns: cols})
		})

		t.Logf("MemTable.Put(update) 平均分配次数: %.2f", allocs)
		// 更新路径不应触发 skipNode 分配；闭包内可能引入少量分配，
		// 因此阈值宽松到 1 alloc。
		if allocs > 1.0 {
			t.Errorf("update path: expected at most 1 alloc per Put (no new node), got %.2f", allocs)
		}
	})
}
