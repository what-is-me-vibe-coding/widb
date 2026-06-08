package index

import (
	"math"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestNewSparseIndex(t *testing.T) {
	si := NewSparseIndex()
	if si == nil {
		t.Fatal("expected non-nil SparseIndex")
	}
	if si.StatCount() != 0 {
		t.Errorf("expected 0 stats, got %d", si.StatCount())
	}
}

func TestRegisterColumnStat(t *testing.T) {
	si := NewSparseIndex()

	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)

	css, ok := si.GetColumnStat(1, 0)
	if !ok {
		t.Fatal("expected stat to exist")
	}
	if !css.HasValues {
		t.Error("expected HasValues to be true")
	}
	if css.MinValue.Int64 != 10 {
		t.Errorf("expected Min 10, got %d", css.MinValue.Int64)
	}
	if css.MaxValue.Int64 != 100 {
		t.Errorf("expected Max 100, got %d", css.MaxValue.Int64)
	}
}

func TestRegisterColumnStatEmptyMinMax(t *testing.T) {
	si := NewSparseIndex()

	si.RegisterColumnStat(1, 0, nil, nil, 5, common.TypeInt64)

	css, ok := si.GetColumnStat(1, 0)
	if !ok {
		t.Fatal("expected stat to exist")
	}
	if css.HasValues {
		t.Error("expected HasValues to be false for empty min/max")
	}
	if css.NullCount != 5 {
		t.Errorf("expected NullCount 5, got %d", css.NullCount)
	}
}

func TestCanSkipEqual(t *testing.T) {
	si := NewSparseIndex()

	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)

	if si.CanSkip(1, 0, OpEqual, common.NewInt64(50)) {
		t.Error("should not skip when value is in range")
	}
	if !si.CanSkip(1, 0, OpEqual, common.NewInt64(5)) {
		t.Error("should skip when value is below min")
	}
	if !si.CanSkip(1, 0, OpEqual, common.NewInt64(200)) {
		t.Error("should skip when value is above max")
	}
}

func TestCanSkipLess(t *testing.T) {
	si := NewSparseIndex()

	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)

	if !si.CanSkip(1, 0, OpLess, common.NewInt64(5)) {
		t.Error("should skip when max < 5 is false (min is 10)")
	}
	if !si.CanSkip(1, 0, OpLess, common.NewInt64(10)) {
		t.Error("should skip when max < 10 is false (min is 10)")
	}
	if si.CanSkip(1, 0, OpLess, common.NewInt64(150)) {
		t.Error("should not skip when some values may be less than 150")
	}
}

func TestCanSkipLessEqual(t *testing.T) {
	si := NewSparseIndex()

	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)

	if !si.CanSkip(1, 0, OpLessEqual, common.NewInt64(5)) {
		t.Error("should skip when all values > 5 (min=10)")
	}
	if si.CanSkip(1, 0, OpLessEqual, common.NewInt64(20)) {
		t.Error("should not skip when some values <= 20")
	}
	if si.CanSkip(1, 0, OpLessEqual, common.NewInt64(200)) {
		t.Error("should not skip when all values <= 200")
	}
}

func TestCanSkipGreater(t *testing.T) {
	si := NewSparseIndex()

	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)

	if !si.CanSkip(1, 0, OpGreater, common.NewInt64(200)) {
		t.Error("should skip when all values are <= 200, none > 200")
	}
	if si.CanSkip(1, 0, OpGreater, common.NewInt64(50)) {
		t.Error("should not skip when some values may be > 50")
	}
}

func TestCanSkipGreaterEqual(t *testing.T) {
	si := NewSparseIndex()

	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)

	if !si.CanSkip(1, 0, OpGreaterEqual, common.NewInt64(200)) {
		t.Error("should skip when all values < 200")
	}
	if si.CanSkip(1, 0, OpGreaterEqual, common.NewInt64(50)) {
		t.Error("should not skip when some values >= 50")
	}
}

func TestCanSkipNoStat(t *testing.T) {
	si := NewSparseIndex()

	if si.CanSkip(999, 0, OpEqual, common.NewInt64(10)) {
		t.Error("should not skip when no stat exists")
	}
}

func TestCanSkipString(t *testing.T) {
	si := NewSparseIndex()

	si.RegisterColumnStat(1, 0, []byte("apple"), []byte("zebra"), 0, common.TypeString)

	if si.CanSkip(1, 0, OpEqual, common.NewString("mango")) {
		t.Error("should not skip when string is in range")
	}
	if !si.CanSkip(1, 0, OpEqual, common.NewString("aaa")) {
		t.Error("should skip when string is below min")
	}
	if !si.CanSkip(1, 0, OpEqual, common.NewString("zzz")) {
		t.Error("should skip when string is above max")
	}
}

func TestCanSkipFloat64(t *testing.T) {
	si := NewSparseIndex()

	si.RegisterColumnStat(1, 0, float64ToBytes(1.5), float64ToBytes(99.9), 0, common.TypeFloat64)

	if si.CanSkip(1, 0, OpEqual, common.NewFloat64(50.0)) {
		t.Error("should not skip when value is in range")
	}
	if !si.CanSkip(1, 0, OpEqual, common.NewFloat64(0.1)) {
		t.Error("should skip when value is below min")
	}
	if !si.CanSkip(1, 0, OpEqual, common.NewFloat64(200.0)) {
		t.Error("should skip when value is above max")
	}
}

func TestSparseUnregisterSegment(t *testing.T) {
	si := NewSparseIndex()

	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)
	si.RegisterColumnStat(1, 1, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)
	si.RegisterColumnStat(2, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)

	if si.StatCount() != 3 {
		t.Fatalf("expected 3 stats, got %d", si.StatCount())
	}

	si.UnregisterSegment(1)

	if si.StatCount() != 1 {
		t.Errorf("expected 1 stat after unregister, got %d", si.StatCount())
	}

	_, ok := si.GetColumnStat(1, 0)
	if ok {
		t.Error("segment 1 stat should have been removed")
	}
	_, ok = si.GetColumnStat(2, 0)
	if !ok {
		t.Error("segment 2 stat should still exist")
	}
}

func TestSparseClear(t *testing.T) {
	si := NewSparseIndex()

	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)
	si.RegisterColumnStat(2, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)

	si.Clear()
	if si.StatCount() != 0 {
		t.Errorf("expected 0 stats after clear, got %d", si.StatCount())
	}
}

type mockSegmentStats struct {
	id   uint64
	cols []mockColStat
}

type mockColStat struct {
	colID     uint32
	colType   common.DataType
	min       []byte
	max       []byte
	nullCount uint32
}

func (m *mockSegmentStats) SegmentID() uint64 {
	return m.id
}

func (m *mockSegmentStats) ForEachColumnStat(fn func(colID uint32, colType common.DataType, minVal, maxVal []byte, nullCount uint32)) {
	for _, c := range m.cols {
		fn(c.colID, c.colType, c.min, c.max, c.nullCount)
	}
}

func TestLoadFromSegment(t *testing.T) {
	si := NewSparseIndex()

	seg := &mockSegmentStats{
		id: 42,
		cols: []mockColStat{
			{colID: 0, colType: common.TypeInt64, min: int64ToBytes(1), max: int64ToBytes(1000), nullCount: 3},
			{colID: 1, colType: common.TypeString, min: []byte("alpha"), max: []byte("omega"), nullCount: 0},
		},
	}

	si.LoadFromSegment(seg, "a", "z", 0)

	if si.StatCount() != 2 {
		t.Fatalf("expected 2 stats, got %d", si.StatCount())
	}

	css, ok := si.GetColumnStat(42, 0)
	if !ok {
		t.Fatal("expected column 0 stat to exist")
	}
	if css.MinValue.Int64 != 1 {
		t.Errorf("expected Min 1, got %d", css.MinValue.Int64)
	}
	if css.MaxValue.Int64 != 1000 {
		t.Errorf("expected Max 1000, got %d", css.MaxValue.Int64)
	}

	css, ok = si.GetColumnStat(42, 1)
	if !ok {
		t.Fatal("expected column 1 stat to exist")
	}
	if css.MinValue.Str != testAlpha {
		t.Errorf("expected Min 'alpha', got %q", css.MinValue.Str)
	}
}

func TestLoadFromSegmentNil(t *testing.T) {
	si := NewSparseIndex()
	si.LoadFromSegment(nil, "", "", 0)
	if si.StatCount() != 0 {
		t.Errorf("expected 0 stats for nil segment, got %d", si.StatCount())
	}
}

type mockColumnVector struct {
	len    uint32
	nulls  *common.Bitmap
	values []common.Value
}

func newMockColumnVector(values []common.Value, nullIndices map[int]bool) *mockColumnVector {
	cv := &mockColumnVector{
		len:    uint32(len(values)),
		nulls:  common.NewBitmap(uint32(len(values))),
		values: values,
	}
	for idx := range nullIndices {
		cv.nulls.Set(uint32(idx))
	}
	return cv
}

func (m *mockColumnVector) Len() uint32 {
	return m.len
}

func (m *mockColumnVector) NullBitmap() *common.Bitmap {
	return m.nulls
}

func (m *mockColumnVector) GetValue(i uint32) common.Value {
	if i >= uint32(len(m.values)) {
		return common.NewNull()
	}
	return m.values[i]
}

func TestBuildFromColumnVector(t *testing.T) {
	si := NewSparseIndex()

	cv := newMockColumnVector([]common.Value{
		common.NewInt64(42),
		common.NewInt64(10),
		common.NewInt64(99),
		common.NewInt64(5),
		common.NewInt64(200),
	}, nil)

	si.BuildFromColumnVector(1, 0, cv)

	css, ok := si.GetColumnStat(1, 0)
	if !ok {
		t.Fatal("expected stat to exist")
	}
	if !css.HasValues {
		t.Error("expected HasValues to be true")
	}
	if css.MinValue.Int64 != 5 {
		t.Errorf("expected Min 5, got %d", css.MinValue.Int64)
	}
	if css.MaxValue.Int64 != 200 {
		t.Errorf("expected Max 200, got %d", css.MaxValue.Int64)
	}
}

func TestBuildFromColumnVectorWithNulls(t *testing.T) {
	si := NewSparseIndex()

	cv := newMockColumnVector([]common.Value{
		common.NewInt64(100),
		common.NewNull(),
		common.NewInt64(200),
		common.NewNull(),
		common.NewInt64(50),
	}, map[int]bool{1: true, 3: true})

	si.BuildFromColumnVector(1, 0, cv)

	css, ok := si.GetColumnStat(1, 0)
	if !ok {
		t.Fatal("expected stat to exist")
	}
	if css.MinValue.Int64 != 50 {
		t.Errorf("expected Min 50, got %d", css.MinValue.Int64)
	}
	if css.MaxValue.Int64 != 200 {
		t.Errorf("expected Max 200, got %d", css.MaxValue.Int64)
	}
	if css.NullCount != 2 {
		t.Errorf("expected NullCount 2, got %d", css.NullCount)
	}
}

func TestBuildFromColumnVectorEmpty(t *testing.T) {
	si := NewSparseIndex()

	cv := &mockColumnVector{len: 0}
	si.BuildFromColumnVector(1, 0, cv)

	if si.StatCount() != 0 {
		t.Errorf("expected 0 stats for empty vector, got %d", si.StatCount())
	}
}

func TestBuildFromColumnVectorNil(t *testing.T) {
	si := NewSparseIndex()
	si.BuildFromColumnVector(1, 0, nil)

	if si.StatCount() != 0 {
		t.Errorf("expected 0 stats for nil vector, got %d", si.StatCount())
	}
}

func TestCanSkipNotEqual(t *testing.T) {
	si := NewSparseIndex()

	si.RegisterColumnStat(1, 0, int64ToBytes(10), int64ToBytes(100), 0, common.TypeInt64)

	if si.CanSkip(1, 0, OpNotEqual, common.NewInt64(5)) {
		t.Error("cannot skip NotEqual based on min/max alone")
	}
	if si.CanSkip(1, 0, OpNotEqual, common.NewInt64(200)) {
		t.Error("cannot skip NotEqual based on min/max alone")
	}
}

func TestConcurrentReadWrite(_ *testing.T) {
	si := NewSparseIndex()

	done := make(chan bool, 10)
	for i := 0; i < 5; i++ {
		go func(id uint64) {
			for j := 0; j < 100; j++ {
				si.RegisterColumnStat(id, uint32(j), int64ToBytes(int64(j*10)), int64ToBytes(int64(j*10+100)), 0, common.TypeInt64)
			}
			done <- true
		}(uint64(i))
	}

	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				si.GetColumnStat(1, 0)
				si.CanSkip(1, 0, OpEqual, common.NewInt64(50))
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func int64ToBytes(v int64) []byte {
	b := make([]byte, 8)
	_ = b[7]
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
	b[4] = byte(v >> 32)
	b[5] = byte(v >> 40)
	b[6] = byte(v >> 48)
	b[7] = byte(v >> 56)
	return b
}

func float64ToBytes(v float64) []byte {
	b := make([]byte, 8)
	bits := math.Float64bits(v)
	b[0] = byte(bits)
	b[1] = byte(bits >> 8)
	b[2] = byte(bits >> 16)
	b[3] = byte(bits >> 24)
	b[4] = byte(bits >> 32)
	b[5] = byte(bits >> 40)
	b[6] = byte(bits >> 48)
	b[7] = byte(bits >> 56)
	return b
}
