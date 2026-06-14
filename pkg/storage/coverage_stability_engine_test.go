package storage

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// ---------------------------------------------------------------------------
// Flush (engine): 未覆盖的错误路径
// ---------------------------------------------------------------------------

// TestFlushV13_NoImmutableEarlyReturn 测试 Flush 在没有 immutable memtable 时的提前返回路径。
// 当 activeMem 为空且 immutable 也为空时，Flush 应直接返回 nil。
func TestFlushV13_NoImmutableEarlyReturn(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// 不写入任何数据，activeMem 为空，immutable 也为空
	err = eng.Flush(cols)
	if err != nil {
		t.Errorf("空 Flush 不应返回错误: %v", err)
	}

	// 验证没有产生 segment
	if count := eng.SegmentCount(); count != 0 {
		t.Errorf("期望 0 个 segment，得到 %d", count)
	}
}

// TestFlushV13_ErrorRecoveryPutBackImmutable 测试 Flush 失败时将 immutable memtable 放回。
// 当 flusher.Flush 失败时，未刷写的 immutable memtable 应被放回 e.immutable。
func TestFlushV13_ErrorRecoveryPutBackImmutable(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// 写入数据
	if err := eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}

	// 手动将 activeMem 移到 immutable，并设置 flusher 的 dataDir 为无效路径
	eng.mu.Lock()
	eng.activeMem.Freeze()
	eng.immutable = append(eng.immutable, eng.activeMem)
	eng.activeMem = NewMemTableWithSize(eng.activeMem.maxSize)
	// 将 flusher 的 dataDir 指向一个文件（非目录），使 writeSegment 失败
	tmpFile, tmpErr := os.CreateTemp(dir, "blocker-*")
	if tmpErr != nil {
		eng.mu.Unlock()
		t.Fatalf("CreateTemp 失败: %v", tmpErr)
	}
	blockerPath := tmpFile.Name()
	_ = tmpFile.Close()
	eng.flusher.dataDir = blockerPath
	eng.mu.Unlock()

	err = eng.Flush(cols)
	if err == nil {
		t.Error("期望 Flush 失败返回错误，得到 nil")
	}

	// 验证 immutable memtable 被放回
	eng.mu.Lock()
	immutableCount := len(eng.immutable)
	eng.mu.Unlock()

	if immutableCount == 0 {
		t.Error("期望 Flush 失败后 immutable memtable 被放回，但 immutable 为空")
	}

	// 恢复 flusher 的 dataDir 以便 Close 成功
	eng.mu.Lock()
	eng.flusher.dataDir = dir
	eng.immutable = nil
	eng.mu.Unlock()
	_ = eng.Close()
}

// TestFlushV13_RegisterSegmentIndexesFailure 测试 Flush 时 registerSegmentIndexes 失败的路径。
// 通过让 flusher 产生 ID=0 的 segment（uint64 溢出），使 primaryIndex.RegisterSegment 失败。
// 验证失败后剩余的 immutable memtable 被放回。
func TestFlushV13_RegisterSegmentIndexesFailure(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	// 写入数据并手动创建两个 immutable memtable
	if err := eng.Write("key1", map[string]common.Value{colVal: common.NewInt64(1)}); err != nil {
		t.Fatalf("Write key1 失败: %v", err)
	}
	// 手动将 activeMem 移到 immutable
	eng.mu.Lock()
	eng.activeMem.Freeze()
	eng.immutable = append(eng.immutable, eng.activeMem)
	eng.activeMem = NewMemTableWithSize(eng.activeMem.maxSize)
	eng.mu.Unlock()

	if err := eng.Write("key2", map[string]common.Value{colVal: common.NewInt64(2)}); err != nil {
		t.Fatalf("Write key2 失败: %v", err)
	}
	// 再次手动将 activeMem 移到 immutable
	eng.mu.Lock()
	eng.activeMem.Freeze()
	eng.immutable = append(eng.immutable, eng.activeMem)
	eng.activeMem = NewMemTableWithSize(eng.activeMem.maxSize)
	// 设置 flusher.nextID 为 uint64 最大值，下次 Flush 会产生 ID=0 的 segment
	eng.flusher.nextID.Store(^uint64(0))
	eng.mu.Unlock()

	err = eng.Flush(cols)
	if err == nil {
		t.Error("期望 registerSegmentIndexes 失败返回错误，得到 nil")
	}

	// 验证剩余 immutable memtable 被放回（第二个 memtable 应被放回）
	eng.mu.Lock()
	immutableCount := len(eng.immutable)
	eng.mu.Unlock()

	if immutableCount == 0 {
		t.Error("期望 registerSegmentIndexes 失败后剩余 immutable memtable 被放回")
	}

	// 恢复以便 Close 成功
	eng.mu.Lock()
	eng.flusher.nextID.Store(0)
	eng.immutable = nil
	eng.mu.Unlock()
	_ = eng.Close()
}

// ---------------------------------------------------------------------------
// decodeSegmentColumn: 未覆盖的错误路径
// ---------------------------------------------------------------------------

// TestDecodeSegmentColumnV13_DecompressError 测试 decodeAllColumns 在 DecompressColumn 失败时的行为。
// 使用损坏的压缩数据（非有效 zstd 格式）触发 DecompressColumn 错误。
func TestDecodeSegmentColumnV13_DecompressError(t *testing.T) {
	seg := &Segment{
		Columns: []EncodedColumn{
			{
				Encoding: EncodingPlain,
				Type:     common.TypeInt64,
				RowCount: 1,
				Data:     []byte{0xFF, 0xFE, 0xFD, 0xFC, 0xFB, 0xFA, 0xF9, 0xF8},
			},
		},
		Keys: []string{crKey1},
	}

	_, err := seg.decodeAllColumns()
	if err == nil {
		t.Error("期望 DecompressColumn 失败返回错误，得到 nil")
	}
}

// TestDecodeSegmentColumnV13_DecodeColumnError 测试 decodeAllColumns 在 DecodeColumn 失败时的行为。
// 使用空数据（DecompressColumn 成功）+ 无效编码类型（DecodeColumn 失败）来触发错误。
func TestDecodeSegmentColumnV13_DecodeColumnError(t *testing.T) {
	seg := &Segment{
		Columns: []EncodedColumn{
			{
				Encoding: EncodingType(99), // 无效编码类型
				Type:     common.TypeInt64,
				RowCount: 1,
				Data:     []byte{}, // 空数据，DecompressColumn 会成功（Decompress 对空数据返回 nil）
			},
		},
		Keys: []string{crKey1},
	}

	_, err := seg.decodeAllColumns()
	if err == nil {
		t.Error("期望 DecodeColumn 失败返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// CompressColumn/DecompressColumn: 未覆盖的错误路径
// ---------------------------------------------------------------------------

// TestCompressColumnV13_NilInput 测试 CompressColumn 对 nil 输入返回错误。
func TestCompressColumnV13_NilInput(t *testing.T) {
	err := CompressColumn(nil)
	if err == nil {
		t.Error("期望 CompressColumn(nil) 返回错误，得到 nil")
	}
}

// TestDecompressColumnV13_NilInput 测试 DecompressColumn 对 nil 输入返回错误。
func TestDecompressColumnV13_NilInput(t *testing.T) {
	err := DecompressColumn(nil)
	if err == nil {
		t.Error("期望 DecompressColumn(nil) 返回错误，得到 nil")
	}
}

// TestCompressColumnV13_EmptyData 测试 CompressColumn 对空 Data 的处理。
// Compress 对空数据返回 nil,nil，CompressColumn 应将 enc.Data 设为 nil。
func TestCompressColumnV13_EmptyData(t *testing.T) {
	enc := &EncodedColumn{Data: []byte{}}
	err := CompressColumn(enc)
	if err != nil {
		t.Errorf("CompressColumn 空数据不应返回错误: %v", err)
	}
	if enc.Data != nil {
		t.Errorf("期望空数据压缩后 Data 为 nil，得到 %v", enc.Data)
	}
}

// TestDecompressColumnV13_CorruptedData 测试 DecompressColumn 对损坏压缩数据的处理。
func TestDecompressColumnV13_CorruptedData(t *testing.T) {
	enc := &EncodedColumn{Data: []byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE}}
	err := DecompressColumn(enc)
	if err == nil {
		t.Error("期望 DecompressColumn 损坏数据返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// Write (engine): 未覆盖的错误路径
// ---------------------------------------------------------------------------

// TestWriteV13_WALAppendFailure 测试 Write 在 WAL 追加失败时的行为。
// 通过关闭 WAL 文件描述符来触发 AppendWrite 失败。
func TestWriteV13_WALAppendFailure(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}

	// 关闭 WAL 文件描述符使 AppendWrite 失败
	if err := eng.wal.file.Close(); err != nil {
		t.Fatalf("WAL file Close 失败: %v", err)
	}

	err = eng.Write("key", map[string]common.Value{colVal: common.NewInt64(1)})
	if err == nil {
		t.Error("期望 WAL 追加失败返回错误，得到 nil")
	}
}

// TestWriteV13_WALSyncFailure 测试 Write 在 WAL 同步失败时的行为。
// 通过关闭 WAL 使 Sync 失败。
func TestWriteV13_WALSyncFailure(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}

	// 关闭 WAL 使 Sync 失败
	if err := eng.wal.Close(); err != nil {
		t.Fatalf("WAL Close 失败: %v", err)
	}

	err = eng.Write("key", map[string]common.Value{colVal: common.NewInt64(1)})
	if err == nil {
		t.Error("期望 WAL 同步失败返回错误，得到 nil")
	}
}

// TestWriteV13_RotateMemTableTrigger 测试 Write 触发 MemTable 轮转的路径。
// 使用很小的 MaxMemTableSize 使 ShouldFlush 返回 true，触发 rotateMemTable。
func TestWriteV13_RotateMemTableTrigger(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir, MaxMemTableSize: 256})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 写入足够多的数据以触发 MemTable 轮转
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("key_%04d", i)
		if err := eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(i))}); err != nil {
			t.Fatalf("Write %d 失败: %v", i, err)
		}
	}

	// 验证数据可读
	row, ok := eng.Get("key_0000")
	if !ok {
		t.Error("期望能读取 key_0000")
	} else if row.Columns[colVal] != common.NewInt64(0) {
		t.Errorf("key_0000: 期望 0，得到 %v", row.Columns[colVal])
	}
}

// ---------------------------------------------------------------------------
// WriteBatch: 未覆盖的错误路径
// ---------------------------------------------------------------------------

// TestWriteBatchV13_EmptyBatch 测试 WriteBatch 空 batch 直接返回 nil。
func TestWriteBatchV13_EmptyBatch(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	defer func() { _ = eng.Close() }()

	err = eng.WriteBatch(nil)
	if err != nil {
		t.Errorf("WriteBatch(nil) 不应返回错误: %v", err)
	}

	err = eng.WriteBatch([]WriteRow{})
	if err != nil {
		t.Errorf("WriteBatch([]) 不应返回错误: %v", err)
	}
}

// TestWriteBatchV13_WALAppendFailure 测试 WriteBatch 在 WAL 追加失败时的行为。
// 通过关闭 WAL 使 AppendBatch 失败。
func TestWriteBatchV13_WALAppendFailure(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(EngineConfig{DataDir: dir})
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}

	// 关闭 WAL 使 AppendBatch 失败
	if err := eng.wal.Close(); err != nil {
		t.Fatalf("WAL Close 失败: %v", err)
	}

	rows := []WriteRow{
		{Key: "k1", Values: map[string]common.Value{colVal: common.NewInt64(1)}},
		{Key: "k2", Values: map[string]common.Value{colVal: common.NewInt64(2)}},
	}
	err = eng.WriteBatch(rows)
	if err == nil {
		t.Error("期望 WAL 追加失败返回错误，得到 nil")
	}
}

// ---------------------------------------------------------------------------
// DeserializeSegment: 未覆盖的错误路径
// ---------------------------------------------------------------------------

// TestDeserializeSegmentV13_InvalidMagic 测试 DeserializeSegment 在 magic number 无效时的行为。
func TestDeserializeSegmentV13_InvalidMagic(t *testing.T) {
	// 创建一个有足够长度但 magic number 无效的数据
	data := make([]byte, 22)
	binary.LittleEndian.PutUint32(data[0:], 0xDEADBEEF) // 无效的 magic number
	// footer offset（在末尾 8 字节）
	binary.LittleEndian.PutUint64(data[len(data)-8:], 14)

	_, err := DeserializeSegment(data)
	if err == nil {
		t.Error("期望无效 magic number 返回错误，得到 nil")
	}
}

// TestDeserializeSegmentV13_TruncatedFile 测试 DeserializeSegment 在文件截断时的行为。
func TestDeserializeSegmentV13_TruncatedFile(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "数据太短（小于 22 字节）",
			data: make([]byte, 10),
		},
		{
			name: "只有 magic（4 字节）",
			data: make([]byte, 4),
		},
		{
			name: "空数据",
			data: []byte{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DeserializeSegment(tt.data)
			if err == nil {
				t.Error("期望截断文件返回错误，得到 nil")
			}
		})
	}
}

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
	for _, s := range []string{testStrAlpha, testStrBeta, testStrGamma, testStrDelta} {
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
	if sliced.GetValue(2).Str != testStrWorld {
		t.Errorf("row 2: got %q, want %q", sliced.GetValue(2).Str, testStrWorld)
	}
}

// suppress unused import warning for json (used indirectly by WAL record tests)
var _ = json.Marshal
