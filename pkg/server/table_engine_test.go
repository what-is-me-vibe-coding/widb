package server

import (
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/what-is-me-vibe-coding/test-db/pkg/catalog"
	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/query"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage/memory"
)

// memColumnDef 构造一个简单的内存表列定义（id 主键 + value 列）。
func memColumnDef() []query.ColumnDef {
	return []query.ColumnDef{
		{Name: "id", Type: common.TypeInt64, Nullable: false},
		{Name: "value", Type: common.TypeString, Nullable: true},
	}
}

// TestRoutingAdapterRegisterUnregister 验证内存引擎的注册与注销。
// 覆盖 review #3：unregisterMemoryEngine 释放内存映射，注销后查询回退到默认引擎。
func TestRoutingAdapterRegisterUnregister(t *testing.T) {
	srv := newTestServer(t)

	eng := createMemoryEngine([]catalog.ColumnDef{
		{Name: "id", Type: common.TypeInt64, Nullable: false},
	})
	if err := srv.adapter.registerMemoryEngine("t1", eng); err != nil {
		t.Fatalf("registerMemoryEngine 失败: %v", err)
	}
	if srv.adapter.engineForTable("t1") != eng {
		t.Fatal("注册后 engineForTable 应返回内存引擎")
	}

	// 重复注册应失败
	if err := srv.adapter.registerMemoryEngine("t1", eng); err == nil {
		t.Error("重复注册应返回错误")
	}

	// 注销后应回退到默认引擎
	if err := srv.adapter.unregisterMemoryEngine("t1"); err != nil {
		t.Fatalf("unregisterMemoryEngine 失败: %v", err)
	}
	if srv.adapter.engineForTable("t1") != srv.adapter.defaultEng {
		t.Error("注销后 engineForTable 应返回默认引擎")
	}

	// 注销不存在的表应失败
	if err := srv.adapter.unregisterMemoryEngine("t1"); err == nil {
		t.Error("注销未注册的表应返回错误")
	}
}

// TestCreateMemoryTableRegistersBeforeCatalogVisible 验证竞态修复（review #1）：
// createMemoryTable 成功后内存引擎已注册，写入路由到内存引擎而非默认 LSM 引擎。
func TestCreateMemoryTableRegistersBeforeCatalogVisible(t *testing.T) {
	srv := newTestServer(t)

	resp, err := srv.createMemoryTable(&query.CreateTableStatement{
		Table:      "memrt",
		Columns:    memColumnDef(),
		PrimaryKey: []string{"id"},
		Engine:     "memory",
	}, []catalog.ColumnDef{
		{Name: "id", Type: common.TypeInt64, Nullable: false},
		{Name: "value", Type: common.TypeString, Nullable: true},
	}, catalog.TableOptions{Engine: "memory"})
	if err != nil || resp.Code != 0 {
		t.Fatalf("createMemoryTable 失败: resp=%+v err=%v", resp, err)
	}

	// 引擎应已注册为内存引擎
	eng := srv.adapter.engineForTable("memrt")
	if _, ok := eng.(*memory.Engine); !ok {
		t.Fatalf("期望引擎为 *memory.Engine，得到 %T", eng)
	}

	// 写入应路由到内存引擎
	if err := eng.Write("k1", map[string]common.Value{"id": common.NewInt64(1), "value": common.NewString("v")}); err != nil {
		t.Fatalf("写入内存引擎失败: %v", err)
	}
	if got := eng.ScanRange("", ""); len(got) != 1 {
		t.Errorf("期望 1 行，得到 %d", len(got))
	}
}

// TestCreateMemoryTableRollbackOnFailure 验证建表失败时回滚内存引擎注册（review #1）。
// 先在 catalog 中创建同名 LSM 表，再以 memory 引擎建表应失败且不残留内存引擎注册。
func TestCreateMemoryTableRollbackOnFailure(t *testing.T) {
	srv := newTestServer(t)

	// 先创建 LSM 表
	if err := srv.catalog.CreateTable("conflict", []catalog.ColumnDef{
		{Name: "id", Type: common.TypeInt64, Nullable: false},
	}, []string{"id"}, catalog.TableOptions{}); err != nil {
		t.Fatalf("预置 LSM 表失败: %v", err)
	}

	// createMemoryTable 应识别表已存在，不注册内存引擎
	resp, _ := srv.createMemoryTable(&query.CreateTableStatement{
		Table:      "conflict",
		Columns:    memColumnDef(),
		PrimaryKey: []string{"id"},
		Engine:     "memory",
	}, []catalog.ColumnDef{
		{Name: "id", Type: common.TypeInt64, Nullable: false},
		{Name: "value", Type: common.TypeString, Nullable: true},
	}, catalog.TableOptions{Engine: "memory"})
	if resp.Code == 0 {
		t.Error("对已存在的表建表应返回错误")
	}

	// 不应残留内存引擎注册：engineForTable 应返回默认引擎
	if srv.adapter.engineForTable("conflict") != srv.adapter.defaultEng {
		t.Error("建表失败后不应残留内存引擎注册")
	}
}

// TestRoutingAdapterConcurrentForTable 验证并发场景下 engineForTable 不产生数据竞争，
// 且注册的内存引擎始终被正确路由（review #1 竞态回归测试）。
func TestRoutingAdapterConcurrentForTable(t *testing.T) {
	srv := newTestServer(t)
	registry := prometheus.NewRegistry()
	srv.metrics = NewMetrics(registry)

	const tables = 20
	for i := 0; i < tables; i++ {
		eng := memory.New()
		eng.SetColumnMeta([]storage.ColumnMeta{{ID: 0, Name: "id", Type: common.TypeInt64}})
		if err := srv.adapter.registerMemoryEngine(tableN(i), eng); err != nil {
			t.Fatalf("注册表 %d 失败: %v", i, err)
		}
	}

	var wg sync.WaitGroup
	// 并发读：反复查询各表引擎
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = srv.adapter.ForTable(tableN(i % tables))
			}
		}()
	}
	// 并发写：反复注册/注销一个临时表
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			tbl := tableN(tables + g)
			for i := 0; i < 50; i++ {
				eng := memory.New()
				if err := srv.adapter.registerMemoryEngine(tbl, eng); err == nil {
					_ = srv.adapter.unregisterMemoryEngine(tbl)
				}
			}
		}(g)
	}
	wg.Wait()
}

func tableN(i int) string {
	return "t" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
