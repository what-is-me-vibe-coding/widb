package storage

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const col0Name = "col_0"

func setupEngine(t *testing.T, dir string, maxSize int64) *Engine {
	t.Helper()
	eng, err := NewEngine(EngineConfig{
		DataDir:         dir,
		MaxMemTableSize: maxSize,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	return eng
}

func writeRows(t *testing.T, eng *Engine, cols []ColumnMeta, count int, offset int) {
	t.Helper()
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("key_%03d", offset+i)
		vals := make(map[string]common.Value, len(cols))
		for _, c := range cols {
			vals[c.Name] = makeValue(c.Type, offset+i)
		}
		if err := eng.Write(key, vals); err != nil {
			t.Fatal(err)
		}
	}
}

func makeValue(typ common.DataType, i int) common.Value {
	switch typ {
	case common.TypeInt64:
		return common.NewInt64(int64(i))
	case common.TypeFloat64:
		return common.NewFloat64(float64(i) * 1.5)
	case common.TypeBool:
		return common.NewBool(i%2 == 0)
	case common.TypeString:
		return common.NewString(fmt.Sprintf("str_%03d", i))
	case common.TypeTimestamp:
		return common.NewTimestamp(time.Unix(int64(i)*1000, 0))
	default:
		return common.NewNull()
	}
}

func createColumns(colTypes []common.DataType) []ColumnMeta {
	cols := make([]ColumnMeta, len(colTypes))
	for i, typ := range colTypes {
		cols[i] = ColumnMeta{ID: uint32(i), Name: fmt.Sprintf("col_%d", i), Type: typ}
	}
	return cols
}

func verifyCompactedSegment(t *testing.T, newSeg *Segment, wantRows, wantCols int) {
	t.Helper()
	if newSeg.RowCount != uint32(wantRows) {
		t.Errorf("expected %d rows, got %d", wantRows, newSeg.RowCount)
	}
	if len(newSeg.Columns) != wantCols {
		t.Errorf("expected %d columns, got %d", len(newSeg.Columns), wantCols)
	}
	if newSeg.FilePath == "" {
		t.Error("expected non-empty file path")
	}
	if _, err := os.Stat(newSeg.FilePath); os.IsNotExist(err) {
		t.Error("compacted segment file does not exist")
	}
}

func runCompactorCase(t *testing.T, dir string, numRows, numFlushes int, colTypes []common.DataType, wantRows, wantCols int) {
	t.Helper()

	cols := createColumns(colTypes)
	eng := setupEngine(t, dir, 64<<20)

	rowsPerFlush := numRows / numFlushes
	for f := 0; f < numFlushes; f++ {
		writeRows(t, eng, cols, rowsPerFlush, f*rowsPerFlush)
		if err := eng.Flush(cols); err != nil {
			t.Fatal(err)
		}
	}

	segments := eng.Segments()
	if len(segments) == 0 {
		t.Fatal("expected segments after flush")
	}

	compactor := NewCompactor(dir)
	newSeg, err := compactor.Compact(segments, cols)
	if err != nil {
		t.Fatal(err)
	}

	verifyCompactedSegment(t, newSeg, wantRows, wantCols)
}

func TestCompactorBasic(t *testing.T) {
	tests := []struct {
		name       string
		numRows    int
		numFlushes int
		colTypes   []common.DataType
		wantRows   int
		wantCols   int
	}{
		{"single segment", 10, 1, []common.DataType{common.TypeInt64}, 10, 1},
		{"multi column", 100, 1, []common.DataType{common.TypeInt64, common.TypeString}, 100, 2},
		{"multi segment merge", 40, 2, []common.DataType{common.TypeInt64}, 40, 1},
		{"all types", 30, 1, []common.DataType{common.TypeInt64, common.TypeFloat64, common.TypeBool, common.TypeString, common.TypeTimestamp}, 30, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "compactor_test")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.RemoveAll(dir) }()

			runCompactorCase(t, dir, tt.numRows, tt.numFlushes, tt.colTypes, tt.wantRows, tt.wantCols)
		})
	}
}

func TestCompactorEmptySegments(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	compactor := NewCompactor(dir)
	_, err = compactor.Compact(nil, nil)
	if err == nil {
		t.Error("expected error for empty segments")
	}
}

func TestCompactorCleanupSegments(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cols := []ColumnMeta{{ID: 0, Name: col0Name, Type: common.TypeInt64}}
	eng := setupEngine(t, dir, 1<<10)
	writeRows(t, eng, cols, 50, 0)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	segments := eng.Segments()
	oldPaths := make([]string, len(segments))
	for i, seg := range segments {
		oldPaths[i] = seg.FilePath
	}

	compactor := NewCompactor(dir)
	if err := compactor.CleanupSegments(segments); err != nil {
		t.Fatal(err)
	}

	for _, p := range oldPaths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("old segment file %s should have been deleted", p)
		}
	}
}

func TestEngineCompactAndShouldCompact(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cols := []ColumnMeta{{ID: 0, Name: col0Name, Type: common.TypeInt64}}
	eng := setupEngine(t, dir, 64<<20)

	if eng.ShouldCompact() {
		t.Error("should not compact with 0 segments")
	}

	if err := eng.Compact(cols); err != nil {
		t.Fatal(err)
	}

	for j := 0; j < 4; j++ {
		writeRows(t, eng, cols, 40, j*40)
		if err := eng.Flush(cols); err != nil {
			t.Fatal(err)
		}
	}

	if !eng.ShouldCompact() {
		t.Errorf("engine should report compaction needed, L0 count=%d", eng.L0SegmentCount())
	}

	if err := eng.Compact(cols); err != nil {
		t.Fatal(err)
	}

	if eng.L0SegmentCount() != 0 {
		t.Errorf("expected 0 L0 segments, got %d", eng.L0SegmentCount())
	}
	if eng.SegmentCount() != 1 {
		t.Errorf("expected 1 segment, got %d", eng.SegmentCount())
	}

	writeRows(t, eng, cols, 40, 200)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	if eng.ShouldCompact() {
		t.Errorf("should not compact with 1 L0 segment, L0 count=%d", eng.L0SegmentCount())
	}
}

// TestCompactorWithNullValues 测试 Compactor 合并包含 NULL 值的 Segment
func TestCompactorWithNullValues(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_null_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cols := []ColumnMeta{
		{ID: 0, Name: colVal, Type: common.TypeInt64},
		{ID: 1, Name: colName, Type: common.TypeString},
	}

	eng := setupEngine(t, dir, 64<<20)

	// 第一批：包含 NULL 值
	_ = eng.Write("k1", map[string]common.Value{
		colVal:  common.NewInt64(100),
		colName: common.NewString("alice"),
	})
	_ = eng.Write("k2", map[string]common.Value{
		colVal: common.NewInt64(200),
		// colName 缺失 -> NULL
	})
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	// 第二批：包含 NULL 值
	_ = eng.Write("k3", map[string]common.Value{
		// colVal 缺失 -> NULL
		colName: common.NewString("charlie"),
	})
	_ = eng.Write("k4", map[string]common.Value{
		colVal:  common.NewInt64(400),
		colName: common.NewString("dave"),
	})
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	segments := eng.Segments()
	compactor := NewCompactor(dir)
	newSeg, err := compactor.Compact(segments, cols)
	if err != nil {
		t.Fatalf("Compact with null values failed: %v", err)
	}

	verifyCompactedSegment(t, newSeg, 4, 2)
}

// TestCompactorWithDifferentDataTypes 测试 Compactor 合并包含不同数据类型的 Segment
func TestCompactorWithDifferentDataTypes(t *testing.T) {
	dir, err := os.MkdirTemp("", "compactor_types_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cols := []ColumnMeta{
		{ID: 0, Name: "col_0", Type: common.TypeInt64},
		{ID: 1, Name: "col_1", Type: common.TypeFloat64},
		{ID: 2, Name: "col_2", Type: common.TypeBool},
		{ID: 3, Name: "col_3", Type: common.TypeString},
		{ID: 4, Name: "col_4", Type: common.TypeTimestamp},
	}

	eng := setupEngine(t, dir, 64<<20)
	writeRows(t, eng, cols, 20, 0)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	writeRows(t, eng, cols, 20, 20)
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}

	segments := eng.Segments()
	compactor := NewCompactor(dir)
	newSeg, err := compactor.Compact(segments, cols)
	if err != nil {
		t.Fatalf("Compact with different types failed: %v", err)
	}

	verifyCompactedSegment(t, newSeg, 40, 5)
}
