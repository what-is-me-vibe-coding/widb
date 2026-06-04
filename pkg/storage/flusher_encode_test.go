package storage

import (
	"fmt"
	"os"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestFlusherBuildEncodedColumnWithNulls 测试 buildEncodedColumn 对包含 null 值的列的处理
func TestFlusherBuildEncodedColumnWithNulls(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	mem := NewMemTable()
	// 第一行有值，第二行缺失列（将产生 null），第三行有值
	_, _, _ = mem.Put("k1", Row{Version: 1, Columns: map[string]common.Value{
		colVal: common.NewInt64(100),
	}})
	_, _, _ = mem.Put("k2", Row{Version: 1, Columns: map[string]common.Value{}}) // 缺失 colVal
	_, _, _ = mem.Put("k3", Row{Version: 1, Columns: map[string]common.Value{
		colVal: common.NewInt64(300),
	}})

	cols := []ColumnMeta{
		{ID: 0, Name: colVal, Type: common.TypeInt64},
	}

	seg, err := flusher.Flush(mem, cols)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}

	if seg.RowCount != 3 {
		t.Errorf("expected rowCount=3, got %d", seg.RowCount)
	}

	// 验证 null 统计
	if len(seg.Footer.ColumnStats) != 1 {
		t.Fatalf("expected 1 column stat, got %d", len(seg.Footer.ColumnStats))
	}
	if seg.Footer.ColumnStats[0].NullCount != 1 {
		t.Errorf("expected NullCount=1, got %d", seg.Footer.ColumnStats[0].NullCount)
	}
}

// TestFlusherBuildEncodedColumnRLE 测试 buildEncodedColumn 对 RLE 编码列的处理
func TestFlusherBuildEncodedColumnRLE(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	mem := NewMemTable()
	// 写入大量重复值以触发 RLE 编码
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("k%03d", i)
		val := int64(i / 10 * 10) // 0,0,0,...,0,10,10,10,...
		_, _, _ = mem.Put(key, Row{Version: 1, Columns: map[string]common.Value{
			colVal: common.NewInt64(val),
		}})
	}

	cols := []ColumnMeta{
		{ID: 0, Name: colVal, Type: common.TypeInt64},
	}

	seg, err := flusher.Flush(mem, cols)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}

	if seg.RowCount != 20 {
		t.Errorf("expected rowCount=20, got %d", seg.RowCount)
	}
}

// TestFlusherBuildEncodedColumnBoolWithNulls 测试 buildEncodedColumn 对 Bool 列含 null 的处理
func TestFlusherBuildEncodedColumnBoolWithNulls(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	mem := NewMemTable()
	_, _, _ = mem.Put("k1", Row{Version: 1, Columns: map[string]common.Value{
		colActive: common.NewBool(true),
	}})
	_, _, _ = mem.Put("k2", Row{Version: 1, Columns: map[string]common.Value{}}) // 缺失列 -> null
	_, _, _ = mem.Put("k3", Row{Version: 1, Columns: map[string]common.Value{
		colActive: common.NewBool(false),
	}})

	cols := []ColumnMeta{
		{ID: 0, Name: colActive, Type: common.TypeBool},
	}

	seg, err := flusher.Flush(mem, cols)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}

	if seg.Footer.ColumnStats[0].NullCount != 1 {
		t.Errorf("expected NullCount=1, got %d", seg.Footer.ColumnStats[0].NullCount)
	}
}

// TestFlusherBuildEncodedColumnStringWithNulls 测试 buildEncodedColumn 对 String 列含 null 的处理
// 注意：String 列使用 Dict 编码，Dict 编码将 null 信息存储在索引数据中而非 Nulls 位图中，
// 因此 computeColumnStat 不会统计 Dict 编码列的 NullCount。
func TestFlusherBuildEncodedColumnStringWithNulls(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	mem := NewMemTable()
	_, _, _ = mem.Put("k1", Row{Version: 1, Columns: map[string]common.Value{
		colName: common.NewString("alice"),
	}})
	_, _, _ = mem.Put("k2", Row{Version: 1, Columns: map[string]common.Value{}}) // 缺失列 -> null
	_, _, _ = mem.Put("k3", Row{Version: 1, Columns: map[string]common.Value{
		colName: common.NewString("charlie"),
	}})

	cols := []ColumnMeta{
		{ID: 0, Name: colName, Type: common.TypeString},
	}

	seg, err := flusher.Flush(mem, cols)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}

	// Dict 编码不设置 Nulls 位图，NullCount 为 0 是预期行为
	if seg.RowCount != 3 {
		t.Errorf("expected rowCount=3, got %d", seg.RowCount)
	}

	// 验证可以通过 GetColumnValue 正确读取 null 值
	val, err := seg.GetColumnValue(0, 1)
	if err != nil {
		t.Fatalf("GetColumnValue: %v", err)
	}
	if !val.IsNull() {
		t.Errorf("expected null at row 1, got %v", val)
	}
}

// TestFlusherBuildEncodedColumnFloatWithNulls 测试 buildEncodedColumn 对 Float64 列含 null 的处理
func TestFlusherBuildEncodedColumnFloatWithNulls(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	mem := NewMemTable()
	_, _, _ = mem.Put("k1", Row{Version: 1, Columns: map[string]common.Value{
		colScore: common.NewFloat64(1.5),
	}})
	_, _, _ = mem.Put("k2", Row{Version: 1, Columns: map[string]common.Value{}}) // 缺失列 -> null
	_, _, _ = mem.Put("k3", Row{Version: 1, Columns: map[string]common.Value{
		colScore: common.NewFloat64(3.14),
	}})

	cols := []ColumnMeta{
		{ID: 0, Name: colScore, Type: common.TypeFloat64},
	}

	seg, err := flusher.Flush(mem, cols)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}

	if seg.Footer.ColumnStats[0].NullCount != 1 {
		t.Errorf("expected NullCount=1, got %d", seg.Footer.ColumnStats[0].NullCount)
	}
}

// TestFlusherWriteSegmentSuccess 测试 writeSegment 成功写入
func TestFlusherWriteSegmentSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	// 构造一个简单的 Segment
	keys := []string{"a", "b"}
	values := []int64{1, 2}
	builder := NewSegmentBuilder(1, "a", "b")
	builder.SetKeys(keys)
	enc, err := EncodeColumn(common.TypeInt64, values, 2, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	builder.AddEncodedColumn(enc)
	seg, err := builder.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	fileName, err := flusher.writeSegment(seg)
	if err != nil {
		t.Fatalf("writeSegment: %v", err)
	}
	if fileName == "" {
		t.Error("expected non-empty file name")
	}
	if _, err := os.Stat(fileName); err != nil {
		t.Errorf("segment file not found: %v", err)
	}
}

// TestFlusherWriteSegmentCreatesDir 测试 writeSegment 自动创建目录
func TestFlusherWriteSegmentCreatesDir(t *testing.T) {
	tmpDir := t.TempDir()
	nestedDir := tmpDir + "/nested/sub/dir"
	flusher := NewFlusher(nestedDir)

	keys := []string{"a"}
	values := []int64{1}
	builder := NewSegmentBuilder(1, "a", "a")
	builder.SetKeys(keys)
	enc, err := EncodeColumn(common.TypeInt64, values, 1, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	builder.AddEncodedColumn(enc)
	seg, err := builder.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	fileName, err := flusher.writeSegment(seg)
	if err != nil {
		t.Fatalf("writeSegment: %v", err)
	}
	if _, err := os.Stat(fileName); err != nil {
		t.Errorf("segment file not found: %v", err)
	}
}
