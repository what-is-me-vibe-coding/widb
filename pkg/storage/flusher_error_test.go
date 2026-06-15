package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestBuildEncodedColumnTypeMismatch 测试 buildEncodedColumn 在值类型不匹配时返回错误
// 当列定义为 INT64 但行数据包含 STRING 值时，cv.Append 应返回类型不匹配错误
func TestBuildEncodedColumnTypeMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	idGen := newSegmentIDGen()
	flusher := NewFlusher(tmpDir, idGen)

	// 构造类型不匹配的行数据：列定义为 INT64，但值是 STRING
	rows := []KeyValue{
		{Key: "k1", Value: Row{Version: 1, Columns: map[string]common.Value{
			colVal: common.NewString("wrong_type"), // 列定义是 INT64，但值是 STRING
		}}},
	}

	colMeta := ColumnMeta{ID: 0, Name: colVal, Type: common.TypeInt64}
	_, err := flusher.buildEncodedColumn(colMeta, rows, 1)
	if err == nil {
		t.Fatal("期望类型不匹配时返回错误，但得到了 nil")
	}
}

// TestBuildEncodedColumnNullAppendError 测试 buildEncodedColumn 在 null append 路径出错
// 由于 NewNull().IsNull() == true，SetValue 会走 SetNull 路径而不会类型不匹配，
// 所以这里通过构造一个非 Null 但类型不匹配的值来覆盖非 null 路径的错误
func TestBuildEncodedColumnNullAppendError(t *testing.T) {
	tmpDir := t.TempDir()
	idGen := newSegmentIDGen()
	flusher := NewFlusher(tmpDir, idGen)

	// 列定义是 STRING，但值是 INT64（非 null，类型不匹配）
	rows := []KeyValue{
		{Key: "k1", Value: Row{Version: 1, Columns: map[string]common.Value{
			colName: common.NewInt64(42), // 列定义是 STRING，但值是 INT64
		}}},
	}

	colMeta := ColumnMeta{ID: 0, Name: colName, Type: common.TypeString}
	_, err := flusher.buildEncodedColumn(colMeta, rows, 1)
	if err == nil {
		t.Fatal("期望类型不匹配时返回错误，但得到了 nil")
	}
}

// TestBuildEncodedColumnFloatTypeMismatch 测试 buildEncodedColumn 在 Float64 列收到错误类型时的错误
func TestBuildEncodedColumnFloatTypeMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	idGen := newSegmentIDGen()
	flusher := NewFlusher(tmpDir, idGen)

	rows := []KeyValue{
		{Key: "k1", Value: Row{Version: 1, Columns: map[string]common.Value{
			colScore: common.NewString("not_a_float"), // 列定义是 FLOAT64，但值是 STRING
		}}},
	}

	colMeta := ColumnMeta{ID: 0, Name: colScore, Type: common.TypeFloat64}
	_, err := flusher.buildEncodedColumn(colMeta, rows, 1)
	if err == nil {
		t.Fatal("期望类型不匹配时返回错误，但得到了 nil")
	}
}

// TestWriteSegmentMkdirAllError 测试 writeSegment 在无法创建目录时返回错误
// 通过将 dataDir 设置为一个已存在文件的路径来触发 MkdirAll 失败
func TestWriteSegmentMkdirAllError(t *testing.T) {
	// 创建一个临时文件，将其路径作为 dataDir 的父路径
	tmpFile, err := os.CreateTemp("", "flusher-mkdir-blocker-*")
	if err != nil {
		t.Fatalf("创建临时文件失败: %v", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	// dataDir 指向文件路径的子目录，MkdirAll 会因为父路径是文件而失败
	idGen := newSegmentIDGen()
	flusher := NewFlusher(tmpPath+"/subdir/data", idGen)

	seg := &Segment{ID: 1}
	_, err = writeSegmentFile(flusher.dataDir, seg)
	if err == nil {
		t.Error("期望 MkdirAll 失败时返回错误，但得到了 nil")
	}
}

// TestWriteSegmentWriteFileError 测试 writeSegment 在写入文件失败时返回错误
// 通过在目标文件路径创建一个目录来触发 WriteFile 失败（不能对目录执行 WriteFile）
func TestWriteSegmentWriteFileError(t *testing.T) {
	tmpDir := t.TempDir()
	idGen := newSegmentIDGen()
	flusher := NewFlusher(tmpDir, idGen)

	// 预先创建一个目录，路径与 writeSegment 将要写入的文件路径相同
	// writeSegment 会写入 segment_{ID}.widb，所以创建同名目录
	fileDir := filepath.Join(tmpDir, "segment_1.widb")
	if err := os.MkdirAll(fileDir, 0755); err != nil {
		t.Fatalf("创建阻塞目录失败: %v", err)
	}

	seg := &Segment{ID: 1}
	_, err := writeSegmentFile(flusher.dataDir, seg)
	if err == nil {
		t.Error("期望写入文件失败时返回错误，但得到了 nil")
	}
}

// TestCompactorBuildSegmentMkdirAllError 测试 Compactor.buildSegment 在无法创建目录时返回错误
func TestCompactorBuildSegmentMkdirAllError(t *testing.T) {
	// 创建一个临时文件，将其路径作为 dataDir 的父路径
	tmpFile, err := os.CreateTemp("", "compactor-mkdir-blocker-*")
	if err != nil {
		t.Fatalf("创建临时文件失败: %v", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	compactor := NewCompactor(tmpPath+"/subdir/data", newSegmentIDGen())
	compactor.idGen.InitIfLarger(1)

	rows := []memRow{
		{Key: "k1", Values: []common.Value{
			common.NewInt64(1),
		}},
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	_, err = compactor.buildSegment(rows, cols)
	if err == nil {
		t.Error("期望 MkdirAll 失败时返回错误，但得到了 nil")
	}
}

// TestCompactorBuildSegmentWriteFileError 测试 Compactor.buildSegment 在写入文件失败时返回错误
// 通过在目标文件路径创建一个目录来触发 WriteFile 失败
func TestCompactorBuildSegmentWriteFileError(t *testing.T) {
	tmpDir := t.TempDir()
	compactor := NewCompactor(tmpDir, newSegmentIDGen())
	compactor.idGen.InitIfLarger(1)

	// 预先创建一个目录，路径与 buildSegment 将要写入的文件路径相同
	// buildSegment 会先执行 c.nextID++，所以 nextID 从 1 变为 2，文件名为 segment_2.widb
	fileDir := filepath.Join(tmpDir, "segment_2.widb")
	if err := os.MkdirAll(fileDir, 0755); err != nil {
		t.Fatalf("创建阻塞目录失败: %v", err)
	}

	rows := []memRow{
		{Key: "k1", Values: []common.Value{
			common.NewInt64(1),
		}},
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	_, err := compactor.buildSegment(rows, cols)
	if err == nil {
		t.Error("期望写入文件失败时返回错误，但得到了 nil")
	}
}

// TestCompactorCompactEmptySegments 测试 Compact 在空 segments 时返回错误
func TestCompactorCompactEmptySegments(t *testing.T) {
	tmpDir := t.TempDir()
	compactor := NewCompactor(tmpDir, newSegmentIDGen())

	_, err := compactor.Compact(nil, nil)
	if err == nil {
		t.Error("期望空 segments 时返回错误，但得到了 nil")
	}
}

// TestCompactorCompactMergeError 测试 Compact 在 readSegmentRows 失败时的错误
// 通过传入一个包含损坏数据的 Segment 来触发解码失败
func TestCompactorCompactMergeError(t *testing.T) {
	tmpDir := t.TempDir()
	compactor := NewCompactor(tmpDir, newSegmentIDGen())

	// 构造一个包含损坏列数据的 Segment，readSegmentRows 会在解码时失败
	segments := []*Segment{
		{
			ID:       1,
			MinKey:   "k1",
			MaxKey:   "k1",
			RowCount: 1,
			FilePath: "/nonexistent/path/segment_1.widb",
			Columns: []EncodedColumn{
				{
					Encoding: EncodingPlain,
					Type:     common.TypeInt64,
					RowCount: 1,
					Data:     []byte{1, 0, 0, 0, 0, 0, 0, 0},
				},
			},
		},
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	_, err := compactor.Compact(segments, cols)
	if err == nil {
		t.Error("期望 merge 失败时返回错误，但得到了 nil")
	}
}

// TestCompactorCleanupSegmentsRemoveError 测试 CleanupSegments 在删除文件失败时的错误
// 通过将 FilePath 设置为一个非空目录来触发 os.Remove 失败（os.Remove 不能删除非空目录）
func TestCompactorCleanupSegmentsRemoveError(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建一个非空目录作为 FilePath
	dirPath := tmpDir + "/nonempty_dir"
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	// 在目录中创建一个文件，使其成为非空目录
	if err := os.WriteFile(filepath.Join(dirPath, "inner_file"), []byte("test"), 0644); err != nil {
		t.Fatalf("创建内部文件失败: %v", err)
	}

	compactor := NewCompactor(tmpDir, newSegmentIDGen())
	segments := []*Segment{
		{ID: 1, FilePath: dirPath},
	}

	err := compactor.CleanupSegments(segments)
	if err == nil {
		t.Error("期望删除非空目录时返回错误，但得到了 nil")
	}
}

// TestCompactorBuildSegmentTypeMismatch 测试 Compactor.buildSegment 在值类型不匹配时的错误
func TestCompactorBuildSegmentTypeMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	compactor := NewCompactor(tmpDir, newSegmentIDGen())
	compactor.idGen.InitIfLarger(1)

	// 列定义是 INT64，但行数据中是 STRING
	rows := []memRow{
		{Key: "k1", Values: []common.Value{
			common.NewString("wrong_type"),
		}},
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}
	_, err := compactor.buildSegment(rows, cols)
	if err == nil {
		t.Error("期望类型不匹配时返回错误，但得到了 nil")
	}
}

// TestCompactorBuildSegmentNullTypeMismatch 测试 Compactor.buildSegment 在 null append 路径之后的类型不匹配错误
func TestCompactorBuildSegmentNullTypeMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	compactor := NewCompactor(tmpDir, newSegmentIDGen())
	compactor.idGen.InitIfLarger(1)

	// 列定义是 STRING，但行数据中是 INT64（非 null，类型不匹配）
	rows := []memRow{
		{Key: "k1", Values: []common.Value{
			common.NewInt64(42),
		}},
	}

	cols := []ColumnMeta{{ID: 0, Name: colName, Type: common.TypeString}}
	_, err := compactor.buildSegment(rows, cols)
	if err == nil {
		t.Error("期望类型不匹配时返回错误，但得到了 nil")
	}
}

// TestFlusherBuildEncodedColumnWithNulls 测试 buildEncodedColumn 对包含 null 值的列的处理
func TestFlusherBuildEncodedColumnWithNulls(t *testing.T) {
	tmpDir := t.TempDir()
	idGen := newSegmentIDGen()
	flusher := NewFlusher(tmpDir, idGen)

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
	idGen := newSegmentIDGen()
	flusher := NewFlusher(tmpDir, idGen)

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
	idGen := newSegmentIDGen()
	flusher := NewFlusher(tmpDir, idGen)

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
	idGen := newSegmentIDGen()
	flusher := NewFlusher(tmpDir, idGen)

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
	idGen := newSegmentIDGen()
	flusher := NewFlusher(tmpDir, idGen)

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
	idGen := newSegmentIDGen()
	flusher := NewFlusher(tmpDir, idGen)

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

	fileName, err := writeSegmentFile(flusher.dataDir, seg)
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
	idGen := newSegmentIDGen()
	flusher := NewFlusher(nestedDir, idGen)

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

	fileName, err := writeSegmentFile(flusher.dataDir, seg)
	if err != nil {
		t.Fatalf("writeSegment: %v", err)
	}
	if _, err := os.Stat(fileName); err != nil {
		t.Errorf("segment file not found: %v", err)
	}
}
