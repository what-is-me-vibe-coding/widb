package query

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// ---------------------------------------------------------------------------
// sliceChunk: 成功、全量切片、越界错误、空切片
// ---------------------------------------------------------------------------

// buildTestChunk 创建一个包含两列（Int64, String）共 5 行的 Chunk
func buildTestChunkForStability(t *testing.T) *storage.Chunk {
	t.Helper()
	chunk := storage.NewChunk(8)

	col1 := storage.NewColumnVector(0, common.TypeInt64, 8)
	for i := int64(10); i < 15; i++ {
		if err := col1.Append(common.NewInt64(i)); err != nil {
			t.Fatalf("Append Int64 失败: %v", err)
		}
	}

	col2 := storage.NewColumnVector(1, common.TypeString, 8)
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		if err := col2.Append(common.NewString(s)); err != nil {
			t.Fatalf("Append String 失败: %v", err)
		}
	}

	if err := chunk.AddColumn(col1); err != nil {
		t.Fatalf("AddColumn Int64 失败: %v", err)
	}
	if err := chunk.AddColumn(col2); err != nil {
		t.Fatalf("AddColumn String 失败: %v", err)
	}
	return chunk
}

func TestCoverageStabilitySliceChunkSuccess(t *testing.T) {
	chunk := buildTestChunkForStability(t)

	result, err := sliceChunk(chunk, 1, 4)
	if err != nil {
		t.Fatalf("sliceChunk 失败: %v", err)
	}
	if result.RowCount() != 3 {
		t.Errorf("RowCount = %d, want 3", result.RowCount())
	}
	if result.ColumnCount() != 2 {
		t.Errorf("ColumnCount = %d, want 2", result.ColumnCount())
	}

	col0, _ := result.GetColumn(0)
	v := col0.GetValue(0)
	if v.Int64 != 11 {
		t.Errorf("col0 row0 = %d, want 11", v.Int64)
	}

	col1, _ := result.GetColumn(1)
	v2 := col1.GetValue(2)
	if v2.Str != "d" {
		t.Errorf("col1 row2 = %q, want %q", v2.Str, "d")
	}
}

func TestCoverageStabilitySliceChunkFull(t *testing.T) {
	chunk := buildTestChunkForStability(t)

	result, err := sliceChunk(chunk, 0, 5)
	if err != nil {
		t.Fatalf("sliceChunk 全量切片失败: %v", err)
	}
	if result.RowCount() != 5 {
		t.Errorf("RowCount = %d, want 5", result.RowCount())
	}
}

func TestCoverageStabilitySliceChunkOutOfRange(t *testing.T) {
	chunk := buildTestChunkForStability(t)

	_, err := sliceChunk(chunk, 0, 100)
	if err == nil {
		t.Error("越界切片应返回错误")
	}
}

func TestCoverageStabilitySliceChunkEmpty(t *testing.T) {
	chunk := buildTestChunkForStability(t)

	result, err := sliceChunk(chunk, 2, 2)
	if err != nil {
		t.Fatalf("空切片不应返回错误: %v", err)
	}
	if result.RowCount() != 0 {
		t.Errorf("RowCount = %d, want 0", result.RowCount())
	}
}
