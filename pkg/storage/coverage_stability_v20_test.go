package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ---------------------------------------------------------------------------
// logRemove: 错误路径和成功路径
// ---------------------------------------------------------------------------

func TestCoverageStabilityLogRemoveNonExistent(t *testing.T) {
	// 删除不存在的文件应仅记录日志，不会 panic
	logRemove("/tmp/no_such_file_for_test_v20_" + filepath.Base(t.Name()))
}

func TestCoverageStabilityLogRemoveSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "to_remove.tmp")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatalf("创建文件失败: %v", err)
	}
	logRemove(path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("文件应已被删除")
	}
}

// ---------------------------------------------------------------------------
// recoverOpen: 错误路径（WAL 文件被删除）和成功路径
// ---------------------------------------------------------------------------

func TestCoverageStabilityRecoverOpenDeletedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recover.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	// 删除底层文件，使 recoverOpen 遇到错误
	_ = w.file.Close()
	_ = os.Remove(path)

	// recoverOpen 应不会 panic，只是记录日志
	w.recoverOpen()
	// w.file 可能是 nil 或指向新文件，取决于 OS 行为
}

func TestCoverageStabilityRecoverOpenSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recover_ok.wal")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}
	_ = w.file.Close()

	// recoverOpen 应能重新打开文件
	w.recoverOpen()
	if w.file == nil {
		t.Fatal("recoverOpen 后 file 不应为 nil")
	}
	_ = w.file.Close()
}

// ---------------------------------------------------------------------------
// getColCache: 越界、nil data、有效数据
// ---------------------------------------------------------------------------

func TestCoverageStabilityGetColCacheOutOfRange(t *testing.T) {
	s := &Segment{
		Columns: []EncodedColumn{},
	}
	s.ensureColCache()
	_, ok := s.getColCache(0)
	if ok {
		t.Error("空 Segment 的 getColCache(0) 应返回 false")
	}
}

func TestCoverageStabilityGetColCacheNilData(t *testing.T) {
	s := &Segment{
		Columns: make([]EncodedColumn, 2),
	}
	s.ensureColCache()
	// colCache 已初始化但 data 为 nil
	_, ok := s.getColCache(0)
	if ok {
		t.Error("未解码的列应返回 false")
	}
}

func TestCoverageStabilityGetColCacheValidData(t *testing.T) {
	s := &Segment{
		Columns: make([]EncodedColumn, 1),
	}
	s.ensureColCache()
	// 手动设置缓存数据
	s.colCache[0] = decodedColumn{
		data:   []int64{42},
		nulls:  nil,
		typ:    common.TypeInt64,
		encTyp: EncodingPlain,
	}
	dc, ok := s.getColCache(0)
	if !ok {
		t.Fatal("已缓存列应返回 true")
	}
	if dc.typ != common.TypeInt64 {
		t.Errorf("typ = %v, want TypeInt64", dc.typ)
	}
}

// ---------------------------------------------------------------------------
// ColumnVector.Slice: 5 种数据类型、空切片、全量切片、越界错误、NULL 值
// ---------------------------------------------------------------------------

func TestCoverageStabilitySliceInt64(t *testing.T) {
	cv := NewColumnVector(0, common.TypeInt64, 8)
	for i := int64(0); i < 5; i++ {
		_ = cv.Append(common.NewInt64(i * 10))
	}
	// 部分切片
	sliced, err := cv.Slice(1, 4)
	if err != nil {
		t.Fatalf("Slice 失败: %v", err)
	}
	if sliced.Len() != 3 {
		t.Fatalf("Len = %d, want 3", sliced.Len())
	}
	for i := uint32(0); i < 3; i++ {
		v := sliced.GetValue(i)
		if v.Int64 != int64((i+1)*10) {
			t.Errorf("row %d: got %d, want %d", i, v.Int64, (i+1)*10)
		}
	}
}

func TestCoverageStabilitySliceFloat64(t *testing.T) {
	cv := NewColumnVector(1, common.TypeFloat64, 8)
	for i := 0; i < 5; i++ {
		_ = cv.Append(common.NewFloat64(float64(i) * 1.5))
	}
	sliced, err := cv.Slice(2, 5)
	if err != nil {
		t.Fatalf("Slice 失败: %v", err)
	}
	if sliced.Len() != 3 {
		t.Fatalf("Len = %d, want 3", sliced.Len())
	}
	for i := uint32(0); i < 3; i++ {
		v := sliced.GetValue(i)
		if v.Float64 != float64(i+2)*1.5 {
			t.Errorf("row %d: got %f, want %f", i, v.Float64, float64(i+2)*1.5)
		}
	}
}

func TestCoverageStabilitySliceString(t *testing.T) {
	cv := NewColumnVector(2, common.TypeString, 8)
	for _, s := range []string{"alpha", "beta", "gamma", "delta"} {
		_ = cv.Append(common.NewString(s))
	}
	sliced, err := cv.Slice(1, 3)
	if err != nil {
		t.Fatalf("Slice 失败: %v", err)
	}
	if sliced.Len() != 2 {
		t.Fatalf("Len = %d, want 2", sliced.Len())
	}
	if sliced.GetValue(0).Str != "beta" {
		t.Errorf("row 0: got %q, want %q", sliced.GetValue(0).Str, "beta")
	}
	if sliced.GetValue(1).Str != "gamma" {
		t.Errorf("row 1: got %q, want %q", sliced.GetValue(1).Str, "gamma")
	}
}

func TestCoverageStabilitySliceTimestamp(t *testing.T) {
	cv := NewColumnVector(3, common.TypeTimestamp, 8)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		_ = cv.Append(common.NewTimestamp(base.Add(time.Duration(i) * time.Hour)))
	}
	sliced, err := cv.Slice(1, 3)
	if err != nil {
		t.Fatalf("Slice 失败: %v", err)
	}
	if sliced.Len() != 2 {
		t.Fatalf("Len = %d, want 2", sliced.Len())
	}
	want := base.Add(1 * time.Hour)
	got := sliced.GetValue(0).Time
	if !got.Equal(want) {
		t.Errorf("row 0: got %v, want %v", got, want)
	}
}

func TestCoverageStabilitySliceBool(t *testing.T) {
	cv := NewColumnVector(4, common.TypeBool, 8)
	_ = cv.Append(common.NewBool(true))
	_ = cv.Append(common.NewBool(false))
	_ = cv.Append(common.NewBool(true))
	_ = cv.Append(common.NewBool(true))

	// 全量切片（从 0 开始），bitmap 拷贝后位偏移一致
	sliced, err := cv.Slice(0, 4)
	if err != nil {
		t.Fatalf("Slice 失败: %v", err)
	}
	if sliced.Len() != 4 {
		t.Fatalf("Len = %d, want 4", sliced.Len())
	}
	if !sliced.GetValue(0).IsNull() && sliced.GetValue(0).Int64 != 1 {
		t.Error("row 0: expected true")
	}
	if !sliced.GetValue(1).IsNull() && sliced.GetValue(1).Int64 != 0 {
		t.Error("row 1: expected false")
	}
	if !sliced.GetValue(2).IsNull() && sliced.GetValue(2).Int64 != 1 {
		t.Error("row 2: expected true")
	}
	if !sliced.GetValue(3).IsNull() && sliced.GetValue(3).Int64 != 1 {
		t.Error("row 3: expected true")
	}
}

func TestCoverageStabilitySliceEmpty(t *testing.T) {
	cv := NewColumnVector(0, common.TypeInt64, 8)
	_ = cv.Append(common.NewInt64(1))
	_ = cv.Append(common.NewInt64(2))

	sliced, err := cv.Slice(1, 1)
	if err != nil {
		t.Fatalf("空切片不应返回错误: %v", err)
	}
	if sliced.Len() != 0 {
		t.Errorf("Len = %d, want 0", sliced.Len())
	}
}

func TestCoverageStabilitySliceFull(t *testing.T) {
	cv := NewColumnVector(0, common.TypeInt64, 8)
	_ = cv.Append(common.NewInt64(10))
	_ = cv.Append(common.NewInt64(20))
	_ = cv.Append(common.NewInt64(30))

	sliced, err := cv.Slice(0, 3)
	if err != nil {
		t.Fatalf("全量切片失败: %v", err)
	}
	if sliced.Len() != 3 {
		t.Fatalf("Len = %d, want 3", sliced.Len())
	}
	if sliced.GetValue(0).Int64 != 10 {
		t.Errorf("row 0: got %d, want 10", sliced.GetValue(0).Int64)
	}
}

func TestCoverageStabilitySliceOutOfRange(t *testing.T) {
	cv := NewColumnVector(0, common.TypeInt64, 8)
	_ = cv.Append(common.NewInt64(1))

	tests := []struct {
		name    string
		start   uint32
		end     uint32
		wantErr bool
	}{
		{"end 超出长度", 0, 5, true},
		{"start > end", 2, 1, true},
		{"start == end == 超出", 3, 3, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := cv.Slice(tt.start, tt.end)
			if (err != nil) != tt.wantErr {
				t.Errorf("Slice(%d, %d): err = %v, wantErr = %v", tt.start, tt.end, err, tt.wantErr)
			}
		})
	}
}

func TestCoverageStabilitySliceWithNulls(t *testing.T) {
	cv := NewColumnVector(0, common.TypeInt64, 8)
	_ = cv.Append(common.NewInt64(100))
	_ = cv.Append(common.NewNull())
	_ = cv.Append(common.NewInt64(300))
	_ = cv.Append(common.NewNull())

	sliced, err := cv.Slice(1, 3)
	if err != nil {
		t.Fatalf("Slice 失败: %v", err)
	}
	if sliced.Len() != 2 {
		t.Fatalf("Len = %d, want 2", sliced.Len())
	}
	if !sliced.GetValue(0).IsNull() {
		t.Error("row 0 应为 NULL")
	}
	if sliced.GetValue(1).Int64 != 300 {
		t.Errorf("row 1: got %d, want 300", sliced.GetValue(1).Int64)
	}
}

// ---------------------------------------------------------------------------
// 辅助：验证 newErrorResponse 的 JSON payload（在 storage 包中不可用，
// 此处仅验证 Slice 的 String 类型含 NULL 值）
// ---------------------------------------------------------------------------

func TestCoverageStabilitySliceStringWithNulls(t *testing.T) {
	cv := NewColumnVector(0, common.TypeString, 8)
	_ = cv.Append(common.NewString("hello"))
	_ = cv.Append(common.NewNull())
	_ = cv.Append(common.NewString("world"))

	sliced, err := cv.Slice(0, 3)
	if err != nil {
		t.Fatalf("Slice 失败: %v", err)
	}
	if !sliced.GetValue(1).IsNull() {
		t.Error("row 1 应为 NULL")
	}
	if sliced.GetValue(2).Str != "world" {
		t.Errorf("row 2: got %q, want %q", sliced.GetValue(2).Str, "world")
	}
}

// suppress unused import warning for json (used indirectly by WAL record tests)
var _ = json.Marshal
