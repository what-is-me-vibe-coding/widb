package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// errorIterator 用于测试 NewMergeIterator 错误路径的模拟迭代器
type errorIterator struct {
	err error
}

func (it *errorIterator) Next() bool       { return false }
func (it *errorIterator) Entry() ScanEntry { return ScanEntry{} }
func (it *errorIterator) Err() error       { return it.err }
func (it *errorIterator) Close()           {}

// TestRegisterSegmentIndexesBloomError 测试 registerSegmentIndexes 在布隆索引注册失败时返回错误
func TestRegisterSegmentIndexesBloomError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("创建引擎失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入数据并刷盘以创建 segment
	if err := eng.Write(crKey1, map[string]common.Value{
		colName: common.NewString("value1"),
	}); err != nil {
		t.Fatalf("写入数据失败: %v", err)
	}

	cols := []ColumnMeta{
		{ID: 0, Name: colName, Type: common.TypeString},
	}
	if err := eng.Flush(cols); err != nil {
		t.Fatalf("刷盘失败: %v", err)
	}

	// 获取已创建的 segment，并破坏其布隆过滤器数据
	eng.mu.RLock()
	var seg *Segment
	if len(eng.segments) > 0 {
		seg = eng.segments[0]
	}
	eng.mu.RUnlock()

	if seg == nil {
		t.Fatal("未找到 segment")
	}

	// 构造一个无效的布隆过滤器字节，使 RegisterFromBytes 返回错误
	// 先注销原有索引
	eng.mu.Lock()
	eng.unregisterSegmentIndexes(seg.ID)
	// 破坏布隆过滤器数据
	originalBloom := seg.Footer.BloomFilter
	seg.Footer.BloomFilter = []byte{0xFF, 0xFE, 0xFD, 0xFC, 0xFB} // 无效的布隆过滤器数据
	err = eng.registerSegmentIndexes(seg, 0)
	seg.Footer.BloomFilter = originalBloom // 恢复原始数据
	eng.mu.Unlock()

	if err == nil {
		t.Error("期望 registerSegmentIndexes 在布隆索引注册失败时返回错误，但得到了 nil")
	}
}

// TestOpenWALTruncateErrorPerm 测试 OpenWAL 在 Truncate 失败时的错误路径
func TestOpenWALTruncateErrorPerm(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root 用户绕过文件权限检查")
	}

	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	// 创建一个有效的 WAL 文件并写入一些数据
	wal, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("创建 WAL 失败: %v", err)
	}
	if err := wal.AppendWrite([]byte("test data")); err != nil {
		t.Fatalf("写入 WAL 失败: %v", err)
	}
	if err := wal.Sync(); err != nil {
		t.Fatalf("同步 WAL 失败: %v", err)
	}
	if err := wal.Close(); err != nil {
		t.Fatalf("关闭 WAL 失败: %v", err)
	}

	// 将 WAL 文件设为只读，使 Truncate 失败
	if err := os.Chmod(walPath, 0444); err != nil {
		t.Fatalf("修改文件权限失败: %v", err)
	}
	defer func() { _ = os.Chmod(walPath, 0644) }() // 恢复权限以便清理

	// OpenWAL 应该在 Truncate 时失败
	_, _, err = OpenWAL(walPath)
	if err == nil {
		t.Error("期望 OpenWAL 在 Truncate 失败时返回错误，但得到了 nil")
	}
}

// TestOpenWALSeekError 测试 OpenWAL 的正常路径（Seek 失败难以直接触发）
func TestOpenWALSeekError(t *testing.T) {
	// Seek 失败在常规场景下难以直接触发
	// 此测试验证 OpenWAL 的正常路径，确保回放和 Truncate 成功
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	wal, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("创建 WAL 失败: %v", err)
	}
	if err := wal.AppendWrite([]byte("test data")); err != nil {
		t.Fatalf("写入 WAL 失败: %v", err)
	}
	if err := wal.Sync(); err != nil {
		t.Fatalf("同步 WAL 失败: %v", err)
	}
	if err := wal.Close(); err != nil {
		t.Fatalf("关闭 WAL 失败: %v", err)
	}

	// 正常打开 WAL，验证基本功能
	openedWAL, records, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("打开 WAL 失败: %v", err)
	}
	defer func() { _ = openedWAL.Close() }()

	if len(records) != 1 {
		t.Errorf("期望 1 条记录，得到 %d 条", len(records))
	}
}

// TestWriteBatchSerializeError 测试 WriteBatch 在 WAL 操作失败时的错误路径
// 通过关闭 WAL 后调用 WriteBatch 来触发 WAL 写入错误
func TestWriteBatchSerializeError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("创建引擎失败: %v", err)
	}

	// 先关闭 WAL，使后续 WriteBatch 的 WAL 操作失败
	if err := eng.wal.Close(); err != nil {
		t.Fatalf("关闭 WAL 失败: %v", err)
	}

	rows := []WriteRow{
		{
			Key: crKey1,
			Values: map[string]common.Value{
				colName: common.NewString("value1"),
			},
		},
	}

	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("期望 WriteBatch 在 WAL 关闭后返回错误，但得到了 nil")
	}
}

// TestWriteBatchMemPutError 测试 WriteBatch 在 mem.Put 失败时的错误路径
// 通过冻结 memtable 来触发 ErrReadOnly 错误
func TestWriteBatchMemPutError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("创建引擎失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 冻结 activeMem，使 Put 返回 ErrReadOnly
	eng.activeMem.Freeze()

	rows := []WriteRow{
		{
			Key: crKey1,
			Values: map[string]common.Value{
				colName: common.NewString("value1"),
			},
		},
	}

	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("期望 WriteBatch 在 memtable 冻结后返回错误，但得到了 nil")
	}
}

// TestNewMergeIteratorError 测试 NewMergeIterator 在迭代器有错误时的处理
func TestNewMergeIteratorError(t *testing.T) {
	testErr := errors.New("测试迭代器错误")
	it := &errorIterator{err: testErr}

	mi := NewMergeIterator(it)
	if mi.Err() == nil {
		t.Error("期望 MergeIterator 有错误，但 Err() 返回 nil")
	}
	if mi.Err() != testErr {
		t.Errorf("期望错误为 %v，得到 %v", testErr, mi.Err())
	}
}

// TestMergeIteratorNextFinished 测试 MergeIterator.Next 在已完成时的行为
func TestMergeIteratorNextFinished(t *testing.T) {
	// 创建一个空迭代器，使 MergeIterator 立即完成
	entries := []ScanEntry{}
	it := newSliceIterator(entries)

	mi := NewMergeIterator(it)
	// 第一次 Next 应该返回 false（空迭代器）
	if mi.Next() {
		t.Error("期望 Next 返回 false（迭代器已完成），但返回了 true")
	}
	// 第二次 Next 也应该返回 false（已标记为 finished）
	if mi.Next() {
		t.Error("期望 Next 在 finished 后返回 false，但返回了 true")
	}
}

// TestMergeIteratorNextError 测试 MergeIterator.Next 在有错误时的行为
func TestMergeIteratorNextError(t *testing.T) {
	testErr := errors.New("测试迭代器错误")
	it := &errorIterator{err: testErr}

	mi := NewMergeIterator(it)
	// Next 在有错误时应返回 false
	if mi.Next() {
		t.Error("期望 Next 在有错误时返回 false，但返回了 true")
	}
	// 再次调用 Next 也应返回 false
	if mi.Next() {
		t.Error("期望 Next 在有错误时持续返回 false，但返回了 true")
	}
}

// TestGetColumnValueFromDecodedOutOfRange 测试 getColumnValueFromDecoded 在 colIdx 越界时返回 Null
func TestGetColumnValueFromDecodedOutOfRange(t *testing.T) {
	seg := &Segment{
		Columns: []EncodedColumn{
			{Type: common.TypeString, RowCount: 1},
		},
		Keys: []string{crKey1},
	}

	// 创建一个只包含一列的 decodedColumn 切片
	decodedCols := []decodedColumn{
		{typ: common.TypeString, encTyp: EncodingPlain},
	}

	// 使用越界的 colIdx
	result := seg.getColumnValueFromDecoded(decodedCols, 5, 0)
	if !result.IsNull() {
		t.Errorf("期望越界 colIdx 返回 Null，得到 %v", result)
	}
}

// TestGetColumnValueOutOfRange 测试 GetColumnValue 在 colIdx 越界时返回错误
func TestGetColumnValueOutOfRange(t *testing.T) {
	seg := &Segment{
		Columns: []EncodedColumn{
			{Type: common.TypeString, RowCount: 1},
		},
		Keys: []string{crKey1},
	}

	// 使用越界的 colIdx
	val, err := seg.GetColumnValue(5, 0)
	if err == nil {
		t.Error("期望 GetColumnValue 在 colIdx 越界时返回错误，但得到了 nil")
	}
	if !val.IsNull() {
		t.Errorf("期望越界 colIdx 返回 Null 值，得到 %v", val)
	}
}

// TestTryCleanWALStatError 测试 tryCleanWAL 在 os.Stat 返回非 IsNotExist 错误时的行为
func TestTryCleanWALStatError(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("创建引擎失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{
		FlushInterval:     time.Hour,
		CompactInterval:   time.Hour,
		WALCleanInterval:  time.Hour,
		WALCleanThreshold: 1,
	})

	// 将 WAL 路径指向一个权限不足的目录，使 .prev 文件的 Stat 返回非 IsNotExist 错误
	// 创建一个无权限的目录
	noPermDir := filepath.Join(dir, "noperm")
	if err := os.MkdirAll(noPermDir, 0000); err != nil {
		t.Fatalf("创建无权限目录失败: %v", err)
	}
	defer func() { _ = os.Chmod(noPermDir, 0755) }() // 恢复权限以便清理

	eng.mu.Lock()
	originalWALPath := eng.wal.path
	eng.wal.path = filepath.Join(noPermDir, "wal.log")
	eng.mu.Unlock()

	err = sched.tryCleanWAL()

	// 恢复原始路径
	eng.mu.Lock()
	eng.wal.path = originalWALPath
	eng.mu.Unlock()

	// 在 root 用户下权限检查不生效，Stat 可能返回 IsNotExist
	if os.Getuid() == 0 {
		t.Skip("root 用户绕过文件权限检查")
	}

	if err == nil {
		t.Error("期望 tryCleanWAL 在 Stat 返回非 IsNotExist 错误时返回错误，但得到了 nil")
	}
}

// TestTryCleanWALRemoveError 测试 tryCleanWAL 在 os.Remove 失败时的行为
func TestTryCleanWALRemoveError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root 用户绕过文件权限检查")
	}

	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("创建引擎失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	sched := NewScheduler(eng, SchedulerConfig{
		FlushInterval:     time.Hour,
		CompactInterval:   time.Hour,
		WALCleanInterval:  time.Hour,
		WALCleanThreshold: 1, // 设置很小的阈值，使任何文件都满足条件
	})

	// 创建 .prev 文件，满足大小条件
	prevPath := eng.wal.path + ".prev"
	largeData := make([]byte, 100) // 超过阈值 1 字节
	if err := os.WriteFile(prevPath, largeData, 0644); err != nil {
		t.Fatalf("创建 .prev 文件失败: %v", err)
	}

	// 将 .prev 文件所在目录设为只读，使 Remove 失败
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatalf("修改目录权限失败: %v", err)
	}
	defer func() { _ = os.Chmod(dir, 0755) }() // 恢复权限以便清理

	err = sched.tryCleanWAL()
	if err == nil {
		t.Error("期望 tryCleanWAL 在 Remove 失败时返回错误，但得到了 nil")
	}
}

// TestReadColumnStatTruncated 测试 readColumnStat 在数据截断时的各种错误路径
func TestReadColumnStatTruncated(t *testing.T) {
	// 测试 pos+4 > len(data) 的情况（column ID 截断）
	_, _, err := readColumnStat([]byte{0x01, 0x02}, 1, 0)
	if err == nil {
		t.Error("期望 readColumnStat 在 column ID 截断时返回错误，但得到了 nil")
	}

	// 测试 min length 字段截断：提供 colID(4字节) + 不够 minLen(4字节) 的数据
	shortData := make([]byte, 6)
	_, _, err = readColumnStat(shortData, 0, 0)
	if err == nil {
		t.Error("期望 readColumnStat 在 min length 截断时返回错误，但得到了 nil")
	}

	// 测试 min data 截断：colID(4) + minLen=5(4) + 但只有1字节数据
	data := make([]byte, 9)
	data[4] = 5 // minLen = 5
	data[8] = 0xAA
	_, _, err = readColumnStat(data, 0, 0)
	if err == nil {
		t.Error("期望 readColumnStat 在 min data 截断时返回错误，但得到了 nil")
	}

	// 测试 max length 截断：colID(4) + minLen=0(4) + maxLen 字段不完整
	data = make([]byte, 9)
	_, _, err = readColumnStat(data, 0, 0)
	if err == nil {
		t.Error("期望 readColumnStat 在 max length 截断时返回错误，但得到了 nil")
	}

	// 测试 max data 截断：colID(4) + minLen=0(4) + maxLen=5(4) + 但只有1字节数据
	data = make([]byte, 13)
	data[8] = 5 // maxLen = 5
	data[12] = 0xBB
	_, _, err = readColumnStat(data, 0, 0)
	if err == nil {
		t.Error("期望 readColumnStat 在 max data 截断时返回错误，但得到了 nil")
	}
}

// TestReadColumnStatNullCountTruncated 测试 readColumnStat 在 null count 截断时的错误路径
func TestReadColumnStatNullCountTruncated(t *testing.T) {
	// 构造数据：colID(4) + minLen=0(4) + maxLen=0(4) + nullCount 字段不完整
	data := make([]byte, 13)
	// colID(4) + minLen=0(4) + maxLen=0(4) = 12 字节
	// 只有 1 字节给 nullCount，需要 4 字节

	_, _, err := readColumnStat(data, 0, 0)
	if err == nil {
		t.Error("期望 readColumnStat 在 null count 截断时返回错误，但得到了 nil")
	}

	// 验证完整的 readColumnStat 调用能成功
	fullData := make([]byte, 16)
	// colID(4) + minLen=0(4) + maxLen=0(4) + nullCount=0(4) = 16 字节

	stat, newPos, err := readColumnStat(fullData, 0, 0)
	if err != nil {
		t.Errorf("期望 readColumnStat 在完整数据时成功，但得到错误: %v", err)
	}
	if newPos != 16 {
		t.Errorf("期望 newPos=16，得到 %d", newPos)
	}
	if stat.ColumnID != 0 {
		t.Errorf("期望 ColumnID=0，得到 %d", stat.ColumnID)
	}
	if stat.NullCount != 0 {
		t.Errorf("期望 NullCount=0，得到 %d", stat.NullCount)
	}
}
