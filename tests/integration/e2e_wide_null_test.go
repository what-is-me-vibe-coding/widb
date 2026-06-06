package integration

import (
	"fmt"
	"os"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// verifyNullRow 验证含 NULL 值的行
func verifyNullRow(t *testing.T, row storage.Row, nameExpected string, nameIsNull, ageIsNull bool, scoreVal float64, suffix string) {
	t.Helper()
	if nameIsNull {
		if v := row.Columns[colName]; !v.IsNull() {
			t.Errorf("name%s: expected NULL, got %v", suffix, v)
		}
	} else if v := row.Columns[colName]; v.Str != nameExpected {
		t.Errorf("name%s: expected %s, got %s", suffix, nameExpected, v.Str)
	}
	if v := row.Columns[colAge]; v.IsNull() != ageIsNull {
		t.Errorf("age%s: isNull=%v, expected=%v", suffix, v.IsNull(), ageIsNull)
	}
	if v := row.Columns[colScore]; !v.IsNull() {
		if v.Float64 != scoreVal {
			t.Errorf("score%s: expected %g, got %g", suffix, scoreVal, v.Float64)
		}
	}
}

// TestEndToEndNullValues 测试写入包含 NULL 值的行，验证 NULL 值正确存储和检索
func TestEndToEndNullValues(t *testing.T) {
	dir, err := os.MkdirTemp("", "e2e_null_values")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	eng, err := storage.NewEngine(storage.EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	cols := []storage.ColumnMeta{
		{ID: 0, Name: colName, Type: common.TypeString},
		{ID: 1, Name: colAge, Type: common.TypeInt64},
		{ID: 2, Name: colScore, Type: common.TypeFloat64},
	}

	_ = eng.Write("row1", map[string]common.Value{
		colName: common.NewString("alice"), colAge: common.NewInt64(30), colScore: common.NewNull(),
	})
	_ = eng.Write("row2", map[string]common.Value{
		colName: common.NewNull(), colAge: common.NewNull(), colScore: common.NewFloat64(99.5),
	})

	// 刷盘前验证
	row, ok := eng.Get("row1")
	if !ok {
		t.Fatal("expected to find row1 before flush")
	}
	verifyNullRow(t, row, "alice", false, false, 0, " row1 before flush")

	row, ok = eng.Get("row2")
	if !ok {
		t.Fatal("expected to find row2 before flush")
	}
	verifyNullRow(t, row, "", true, true, 99.5, " row2 before flush")

	// 刷盘后验证
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}
	row, ok = eng.Get("row1")
	if !ok {
		t.Fatal("expected to find row1 after flush")
	}
	verifyNullRow(t, row, "alice", false, false, 0, " row1 after flush")
}

// buildWideCols 构建 50 列的元数据
func buildWideCols(numCols int) []storage.ColumnMeta {
	cols := make([]storage.ColumnMeta, numCols)
	for i := 0; i < numCols; i++ {
		name := fmt.Sprintf("col_%d", i)
		var typ common.DataType
		switch i % 4 {
		case 0:
			typ = common.TypeInt64
		case 1:
			typ = common.TypeString
		case 2:
			typ = common.TypeFloat64
		case 3:
			typ = common.TypeBool
		}
		cols[i] = storage.ColumnMeta{ID: uint32(i), Name: name, Type: typ}
	}
	return cols
}

// buildWideValues 构建 50 列的值
func buildWideValues(cols []storage.ColumnMeta) map[string]common.Value {
	values := make(map[string]common.Value, len(cols))
	for i, col := range cols {
		switch i % 4 {
		case 0:
			values[col.Name] = common.NewInt64(int64(i * 100))
		case 1:
			values[col.Name] = common.NewString(fmt.Sprintf("str_%d", i))
		case 2:
			values[col.Name] = common.NewFloat64(float64(i) * 1.1)
		case 3:
			values[col.Name] = common.NewBool(i%2 == 0)
		}
	}
	return values
}

// verifyWideRow 验证宽表行的数据正确性
func verifyWideRow(t *testing.T, row storage.Row, cols []storage.ColumnMeta, suffix string) {
	t.Helper()
	for i, col := range cols {
		val, exists := row.Columns[col.Name]
		if !exists {
			t.Errorf("column %s%s not found in result", col.Name, suffix)
			continue
		}
		switch i % 4 {
		case 0:
			if val.Int64 != int64(i*100) {
				t.Errorf("col %s%s: expected %d, got %d", col.Name, suffix, i*100, val.Int64)
			}
		case 1:
			expected := fmt.Sprintf("str_%d", i)
			if val.Str != expected {
				t.Errorf("col %s%s: expected %s, got %s", col.Name, suffix, expected, val.Str)
			}
		case 2:
			if val.Float64 != float64(i)*1.1 {
				t.Errorf("col %s%s: expected %g, got %g", col.Name, suffix, float64(i)*1.1, val.Float64)
			}
		case 3:
			if (val.Int64 != 0) != (i%2 == 0) {
				t.Errorf("col %s%s: bool mismatch", col.Name, suffix)
			}
		}
	}
}

// TestEndToEndWideTable 测试宽表场景（50列），验证宽表支持
func TestEndToEndWideTable(t *testing.T) {
	dir, err := os.MkdirTemp("", "e2e_wide_table")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	eng, err := storage.NewEngine(storage.EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	cols := buildWideCols(50)
	values := buildWideValues(cols)
	_ = eng.Write("wide_row", values)

	// 刷盘前验证
	row, ok := eng.Get("wide_row")
	if !ok {
		t.Fatal("expected to find wide_row before flush")
	}
	verifyWideRow(t, row, cols, " before flush")

	// 刷盘后验证
	if err := eng.Flush(cols); err != nil {
		t.Fatal(err)
	}
	row, ok = eng.Get("wide_row")
	if !ok {
		t.Fatal("expected to find wide_row after flush")
	}
	verifyWideRow(t, row, cols, " after flush")
}
