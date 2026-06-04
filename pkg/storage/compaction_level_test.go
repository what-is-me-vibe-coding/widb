package storage

import (
	"os"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestCompactorCompactToLevel(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cols := []ColumnMeta{{ID: 0, Name: col0Name, Type: common.TypeInt64}}
	eng := setupEngine(t, dir, 1<<10)
	writeRows(t, eng, cols, 20, 0)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	segments := eng.Segments()
	compactor := NewCompactor(dir)
	newSeg, err := compactor.CompactToLevel(segments, 0, cols)
	if err != nil {
		t.Fatal(err)
	}

	if newSeg.RowCount != 20 {
		t.Errorf("expected 20 rows, got %d", newSeg.RowCount)
	}
}

func TestCompactorWithFloat64Column(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cols := []ColumnMeta{{ID: 0, Name: "score", Type: common.TypeFloat64}}
	eng := setupEngine(t, dir, 64<<20)
	writeRows(t, eng, cols, 20, 0)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	segments := eng.Segments()
	compactor := NewCompactor(dir)
	newSeg, err := compactor.Compact(segments, cols)
	if err != nil {
		t.Fatal(err)
	}
	if newSeg.RowCount != 20 {
		t.Errorf("expected 20 rows, got %d", newSeg.RowCount)
	}
}

func TestCompactorWithBoolColumn(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cols := []ColumnMeta{{ID: 0, Name: "active", Type: common.TypeBool}}
	eng := setupEngine(t, dir, 64<<20)
	writeRows(t, eng, cols, 20, 0)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	segments := eng.Segments()
	compactor := NewCompactor(dir)
	newSeg, err := compactor.Compact(segments, cols)
	if err != nil {
		t.Fatal(err)
	}
	if newSeg.RowCount != 20 {
		t.Errorf("expected 20 rows, got %d", newSeg.RowCount)
	}
}

func TestCompactorWithStringColumn(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cols := []ColumnMeta{{ID: 0, Name: "name", Type: common.TypeString}}
	eng := setupEngine(t, dir, 64<<20)
	writeRows(t, eng, cols, 20, 0)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	segments := eng.Segments()
	compactor := NewCompactor(dir)
	newSeg, err := compactor.Compact(segments, cols)
	if err != nil {
		t.Fatal(err)
	}
	if newSeg.RowCount != 20 {
		t.Errorf("expected 20 rows, got %d", newSeg.RowCount)
	}
}

func TestCompactorWithTimestampColumn(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cols := []ColumnMeta{{ID: 0, Name: "ts", Type: common.TypeTimestamp}}
	eng := setupEngine(t, dir, 64<<20)
	writeRows(t, eng, cols, 20, 0)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	segments := eng.Segments()
	compactor := NewCompactor(dir)
	newSeg, err := compactor.Compact(segments, cols)
	if err != nil {
		t.Fatal(err)
	}
	if newSeg.RowCount != 20 {
		t.Errorf("expected 20 rows, got %d", newSeg.RowCount)
	}
}

func TestCompactorWithMissingColumn(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	// Write data with one column, then compact with a different column name
	cols := []ColumnMeta{{ID: 0, Name: col0Name, Type: common.TypeInt64}}
	eng := setupEngine(t, dir, 64<<20)
	writeRows(t, eng, cols, 10, 0)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	segments := eng.Segments()
	compactor := NewCompactor(dir)

	// Compact with a different column name - should result in null values
	compactCols := []ColumnMeta{{ID: 0, Name: testColMissing, Type: common.TypeInt64}}
	newSeg, err := compactor.Compact(segments, compactCols)
	if err != nil {
		t.Fatal(err)
	}
	if newSeg.RowCount != 10 {
		t.Errorf("expected 10 rows, got %d", newSeg.RowCount)
	}
}

func TestExtractValueWithNulls(t *testing.T) {
	nulls := common.NewBitmap(4)
	nulls.Set(1)
	nulls.Set(3)

	cd := columnData{
		data:  []int64{10, 20, 30, 40},
		nulls: nulls,
		typ:   common.TypeInt64,
	}

	val := extractValue(cd, 0)
	if val.Int64 != 10 {
		t.Errorf("expected 10, got %d", val.Int64)
	}

	val = extractValue(cd, 1)
	if !val.IsNull() {
		t.Errorf("expected null at row 1, got %v", val)
	}

	val = extractValue(cd, 3)
	if !val.IsNull() {
		t.Errorf("expected null at row 3, got %v", val)
	}
}

func TestExtractValueOutOfRange(t *testing.T) {
	cd := columnData{
		data:  []int64{10},
		nulls: nil,
		typ:   common.TypeInt64,
	}

	// Row index out of range
	val := extractValue(cd, 99)
	if !val.IsNull() {
		t.Errorf("expected null for out-of-range row, got %v", val)
	}
}

// TestCompactToLevelEmptySegments 测试 CompactToLevel 对空输入的处理
func TestCompactToLevelEmptySegments(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	compactor := NewCompactor(dir)
	_, err = compactor.CompactToLevel(nil, 0, nil)
	if err == nil {
		t.Error("expected error for empty segments in CompactToLevel")
	}
}

// TestCompactToLevelMultipleSegments 测试 CompactToLevel 合并多个段
func TestCompactToLevelMultipleSegments(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cols := []ColumnMeta{{ID: 0, Name: col0Name, Type: common.TypeInt64}}
	eng := setupEngine(t, dir, 1<<10)

	// 写入并刷盘两个段
	writeRows(t, eng, cols, 10, 0)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}
	writeRows(t, eng, cols, 10, 10)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	segments := eng.Segments()
	if len(segments) < 2 {
		t.Fatalf("expected at least 2 segments, got %d", len(segments))
	}

	compactor := NewCompactor(dir)
	newSeg, err := compactor.CompactToLevel(segments, 0, cols)
	if err != nil {
		t.Fatalf("CompactToLevel: %v", err)
	}

	if newSeg.RowCount != 20 {
		t.Errorf("expected 20 rows, got %d", newSeg.RowCount)
	}
	if newSeg.FilePath == "" {
		t.Error("expected non-empty file path")
	}
	if _, err := os.Stat(newSeg.FilePath); os.IsNotExist(err) {
		t.Error("compacted segment file does not exist")
	}
}

// TestCompactToLevelWithDifferentColumnTypes 测试 CompactToLevel 对不同列类型的处理
func TestCompactToLevelWithDifferentColumnTypes(t *testing.T) {
	tests := []struct {
		name    string
		colType common.DataType
	}{
		{"float64", common.TypeFloat64},
		{"bool", common.TypeBool},
		{"string", common.TypeString},
		{"timestamp", common.TypeTimestamp},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "compactor_test")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.RemoveAll(dir) }()

			cols := []ColumnMeta{{ID: 0, Name: "col", Type: tt.colType}}
			eng := setupEngine(t, dir, 64<<20)
			writeRows(t, eng, cols, 10, 0)
			if err := eng.Flush(cols); err != nil {
				t.Fatal(err)
			}

			segments := eng.Segments()
			compactor := NewCompactor(dir)
			newSeg, err := compactor.CompactToLevel(segments, 0, cols)
			if err != nil {
				t.Fatalf("CompactToLevel %s: %v", tt.name, err)
			}
			if newSeg.RowCount != 10 {
				t.Errorf("expected 10 rows, got %d", newSeg.RowCount)
			}
		})
	}
}

// TestCompactToLevelWithMissingColumn 测试 CompactToLevel 对缺失列的处理
func TestCompactToLevelWithMissingColumn(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cols := []ColumnMeta{{ID: 0, Name: col0Name, Type: common.TypeInt64}}
	eng := setupEngine(t, dir, 64<<20)
	writeRows(t, eng, cols, 10, 0)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	segments := eng.Segments()
	compactor := NewCompactor(dir)

	// 使用不同的列名进行压缩 - 应产生 null 值
	compactCols := []ColumnMeta{{ID: 0, Name: testColMissing, Type: common.TypeInt64}}
	newSeg, err := compactor.CompactToLevel(segments, 0, compactCols)
	if err != nil {
		t.Fatalf("CompactToLevel with missing column: %v", err)
	}
	if newSeg.RowCount != 10 {
		t.Errorf("expected 10 rows, got %d", newSeg.RowCount)
	}
}

// TestCompactToLevelMergedResultEmpty 测试 CompactToLevel 合并结果为空的情况
func TestCompactToLevelMergedResultEmpty(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	// 创建一个 RowCount=0 的段
	builder := NewSegmentBuilder(1, "", "")
	enc, err := EncodeColumn(common.TypeInt64, []int64{}, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	builder.AddEncodedColumn(enc)
	seg, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	compactor := NewCompactor(dir)
	_, err = compactor.CompactToLevel([]*Segment{seg}, 0, []ColumnMeta{{ID: 0, Name: col0Name, Type: common.TypeInt64}})
	if err == nil {
		t.Error("expected error for empty merged result")
	}
}

// TestCompactorCleanupSegmentsNonExistentFile 测试 CleanupSegments 对不存在文件的处理
func TestCompactorCleanupSegmentsNonExistentFile(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	compactor := NewCompactor(dir)
	seg := &Segment{ID: 999, FilePath: "/nonexistent/path/segment_999.widb"}
	// 清理不存在的文件不应报错
	if err := compactor.CleanupSegments([]*Segment{seg}); err != nil {
		t.Errorf("CleanupSegments should not fail for non-existent files: %v", err)
	}
}

// TestCompactorCleanupSegmentsEmptyFilePath 测试 CleanupSegments 对空文件路径的处理
func TestCompactorCleanupSegmentsEmptyFilePath(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	compactor := NewCompactor(dir)
	seg := &Segment{ID: 1, FilePath: ""}
	// 空文件路径不应报错
	if err := compactor.CleanupSegments([]*Segment{seg}); err != nil {
		t.Errorf("CleanupSegments should not fail for empty file path: %v", err)
	}
}
