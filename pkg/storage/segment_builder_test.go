package storage

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestSegmentBuilderSetBloomFPRate(t *testing.T) {
	keys := []string{"a", "b", "c"}
	values := []int64{1, 2, 3}

	builder := NewSegmentBuilder(100, "a", "c")
	builder.SetKeys(keys)
	builder.SetBloomFPRate(0.001) // Custom FP rate

	enc, err := EncodeColumn(common.TypeInt64, values, 3, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	builder.AddEncodedColumn(enc)

	seg, err := builder.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if len(seg.Footer.BloomFilter) == 0 {
		t.Error("expected bloom filter to be built with custom FP rate")
	}
}

func TestSegmentGetColumnValueOutOfRangeIndex(t *testing.T) {
	seg := buildTestSegmentForSegment(t)

	// Request column index that doesn't exist
	_, err := seg.GetColumnValue(99, 0)
	if err == nil {
		t.Error("expected error for out-of-range column index")
	}
}

func TestSegmentFindRowByKeyNotFoundInList(t *testing.T) {
	seg := &Segment{Keys: []string{"a", "c", "e"}}
	_, found := seg.FindRowByKey("b")
	if found {
		t.Error("expected false for key not in sorted list")
	}
}

func TestSegmentForEachColumnStat(t *testing.T) {
	seg := buildTestSegmentForSegment(t)

	var colIDs []uint32
	seg.ForEachColumnStat(func(colID uint32, _ common.DataType, _, _ []byte, _ uint32) {
		colIDs = append(colIDs, colID)
	})
	if len(colIDs) == 0 {
		t.Error("expected at least one column stat")
	}
}

func TestSegmentGetAllColumnValuesFromBuilder(t *testing.T) {
	seg := buildTestSegmentForSegment(t)
	colMeta := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	vals, err := seg.GetAllColumnValues(0, colMeta)
	if err != nil {
		t.Fatalf("GetAllColumnValues: %v", err)
	}
	if len(vals) == 0 {
		t.Error("expected at least one column value")
	}
}

func buildTestSegmentForSegment(t *testing.T) *Segment {
	t.Helper()
	keys := []string{"a", "b", "c"}
	values := []int64{1, 2, 3}

	builder := NewSegmentBuilder(50, "a", "c")
	builder.SetKeys(keys)

	enc, err := EncodeColumn(common.TypeInt64, values, 3, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	builder.AddEncodedColumn(enc)

	seg, err := builder.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return seg
}
