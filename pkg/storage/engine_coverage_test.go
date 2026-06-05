package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestNewEngineWithDefaultMaxMemTableSize 测试 MaxMemTableSize 为 0 时使用默认值
func TestNewEngineWithDefaultMaxMemTableSize(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir:         t.TempDir(),
		MaxMemTableSize: 0, // 应使用默认值
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 验证引擎可以正常工作
	if err := eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
}

// TestNewEngineWithNegativeMaxMemTableSize 测试 MaxMemTableSize 为负数时使用默认值
func TestNewEngineWithNegativeMaxMemTableSize(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir:         t.TempDir(),
		MaxMemTableSize: -100, // 应使用默认值
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	if err := eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
}

// TestNewEngineWithCustomMaxMemTableSize 测试自定义 MaxMemTableSize
func TestNewEngineWithCustomMaxMemTableSize(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir:         t.TempDir(),
		MaxMemTableSize: 1024, // 自定义大小
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	if err := eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
}

// TestNewEngineWithExistingDataDir 测试在已有数据目录上创建引擎
func TestNewEngineWithExistingDataDir(t *testing.T) {
	dir := t.TempDir()

	// 先创建引擎并写入数据
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("第一次 NewEngine 失败: %v", err)
	}
	_ = eng.Write("k1", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("k2", map[string]common.Value{colVal: common.NewInt64(2)})
	if err := eng.Close(); err != nil {
		t.Fatalf("关闭引擎失败: %v", err)
	}

	// 重新打开引擎
	eng2, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("第二次 NewEngine 失败: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	// 验证数据可以读取
	row, ok := eng2.Get("k1")
	if !ok {
		t.Error("k1 未找到")
	} else if row.Columns[colVal].Int64 != 1 {
		t.Errorf("k1: 期望 1，得到 %d", row.Columns[colVal].Int64)
	}
}

// TestNewEngineWithEmptyWALRecovery 测试 WAL 文件为空时的恢复
func TestNewEngineWithEmptyWALRecovery(t *testing.T) {
	dir := t.TempDir()

	// 创建引擎并关闭（不写入数据）
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("关闭引擎失败: %v", err)
	}

	// 重新打开，WAL 为空
	eng2, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("第二次 NewEngine 失败: %v", err)
	}
	defer func() { _ = eng2.Close() }()
}

// TestEngineWriteAfterCloseErrors 测试引擎关闭后写入返回错误
func TestEngineWriteAfterCloseErrors(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}

	// 正常关闭引擎
	if err := eng.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	// 关闭后写入应返回错误
	err = eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(1)})
	if err == nil {
		t.Error("期望关闭后写入返回错误")
	}
}

// TestEngineWriteWALAppendError 测试 Write 在 WAL 追加失败时的行为
func TestEngineWriteWALAppendError(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}

	// 关闭 WAL 以触发追加错误
	_ = eng.wal.Close()

	err = eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(1)})
	if err == nil {
		t.Error("期望 WAL 关闭后写入返回错误")
	}
}

// TestEngineWriteFrozenMemTable 测试写入冻结的 MemTable 时返回错误
func TestEngineWriteFrozenMemTable(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 先正常写入一条记录
	if err := eng.Write(crKey1, map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
		t.Fatalf("正常 Write 失败: %v", err)
	}

	// 冻结 activeMem 以触发 Put 错误
	eng.activeMem.Freeze()

	err = eng.Write("key_frozen", map[string]common.Value{colVal: common.NewInt64(2)})
	if err == nil {
		t.Error("期望冻结 MemTable 后写入返回错误")
	}
}

// TestEngineWriteMultipleColumns 测试写入多列数据
func TestEngineWriteMultipleColumns(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	vals := map[string]common.Value{
		colName:   common.NewString(benchValName),
		colAge:    common.NewInt64(30),
		colScore:  common.NewFloat64(95.5),
		colActive: common.NewBool(true),
	}

	if err := eng.Write(crKey1, vals); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}

	row, ok := eng.Get(crKey1)
	if !ok {
		t.Fatal("key1 未找到")
	}
	if row.Columns[colName].Str != benchValName {
		t.Errorf("name: 期望 %q，得到 %q", benchValName, row.Columns[colName].Str)
	}
	if row.Columns[colAge].Int64 != 30 {
		t.Errorf("age: 期望 30，得到 %d", row.Columns[colAge].Int64)
	}
	if row.Columns[colScore].Float64 != 95.5 {
		t.Errorf("score: 期望 95.5，得到 %f", row.Columns[colScore].Float64)
	}
}

// TestEngineWriteEmptyValues 测试写入空值映射
func TestEngineWriteEmptyValues(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入空值映射
	if err := eng.Write(crKey1, map[string]common.Value{}); err != nil {
		t.Fatalf("Write 空值映射失败: %v", err)
	}

	row, ok := eng.Get(crKey1)
	if !ok {
		t.Fatal("key1 未找到")
	}
	if len(row.Columns) != 0 {
		t.Errorf("期望 0 列，得到 %d", len(row.Columns))
	}
}

// TestEngineWriteNullValue 测试写入包含 NULL 值的数据
func TestEngineWriteNullValue(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	vals := map[string]common.Value{
		colName: common.NewNull(),
		colAge:  common.NewInt64(25),
	}

	if err := eng.Write(crKey1, vals); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}

	row, ok := eng.Get(crKey1)
	if !ok {
		t.Fatal("key1 未找到")
	}
	if row.Columns[colName].Valid {
		t.Error("期望 name 为 NULL")
	}
	if row.Columns[colAge].Int64 != 25 {
		t.Errorf("age: 期望 25，得到 %d", row.Columns[colAge].Int64)
	}
}

// TestEngineWriteTimestampValue 测试写入包含时间戳的数据
func TestEngineWriteTimestampValue(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	_, exists := eng.activeMem.Get("nonexistent")
	_ = exists // 键不存在，使用零值时间
	ts := common.NewTimestamp(time.Time{})
	vals := map[string]common.Value{
		colVal:  common.NewInt64(1),
		colName: ts,
	}

	if err := eng.Write(crKey1, vals); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
}

// TestNewEngineDataDirCreation 测试 NewEngine 自动创建数据目录
func TestNewEngineDataDirCreation(t *testing.T) {
	parentDir := t.TempDir()
	dataDir := filepath.Join(parentDir, "nested", "data", "dir")

	eng, err := NewEngine(EngineConfig{DataDir: dataDir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 验证目录已创建
	info, err := os.Stat(dataDir)
	if err != nil {
		t.Fatalf("数据目录未创建: %v", err)
	}
	if !info.IsDir() {
		t.Error("数据路径不是目录")
	}
}

// TestEngineWriteAndReadMultipleKeys 测试写入和读取多个键
func TestEngineWriteAndReadMultipleKeys(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	keys := []string{crKey1, "key2", crKey3, "key4", "key5"}
	for i, key := range keys {
		if err := eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))}); err != nil {
			t.Fatalf("Write %s 失败: %v", key, err)
		}
	}

	for i, key := range keys {
		row, ok := eng.Get(key)
		if !ok {
			t.Errorf("%s 未找到", key)
			continue
		}
		if row.Columns[colVal].Int64 != int64(i) {
			t.Errorf("%s: 期望 %d，得到 %d", key, i, row.Columns[colVal].Int64)
		}
	}
}

// TestEngineWriteVersionIncrement 测试每次写入版本号递增
func TestEngineWriteVersionIncrement(t *testing.T) {
	eng, err := NewEngine(EngineConfig{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	for i := 0; i < 10; i++ {
		key := "key_" + string(rune('a'+i))
		if err := eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))}); err != nil {
			t.Fatalf("Write %s 失败: %v", key, err)
		}
	}

	// 验证版本号递增
	row, ok := eng.Get("key_j")
	if !ok {
		t.Fatal("key_j 未找到")
	}
	// 版本号应等于写入次数
	if row.Version != 10 {
		t.Errorf("期望版本号 10，得到 %d", row.Version)
	}
}
