package storage

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestCrashRecovery_WALCorruption 验证 WAL 文件损坏场景下的恢复能力。
// WAL 尾部截断后，最新写入的记录可能丢失，但引擎应能正常启动，
// 且段文件中已刷盘的数据不受影响。
func TestCrashRecovery_WALCorruption(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	rng := rand.New(rand.NewSource(789))

	for i := 0; i < 15; i++ {
		eng, err := NewEngine(cfg)
		if err != nil {
			t.Fatalf("iteration %d: new engine: %v", i, err)
		}

		for j := 0; j < 10; j++ {
			key := fmt.Sprintf("key_%04d", rng.Intn(200))
			val := int64(rng.Intn(100000))
			_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(val)})
		}

		if err := eng.Close(); err != nil {
			t.Fatalf("iteration %d: close engine: %v", i, err)
		}

		walPath := filepath.Join(dir, "wal.log")
		if rng.Float64() < 0.5 {
			corruptWALTail(t, walPath, rng)
		}
	}

	// 引擎应能正常启动
	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("final recovery: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// 新写入应正常工作
	_ = eng.Write("post_corruption", map[string]common.Value{colVal: common.NewInt64(99999)})
	row, ok := eng.Get("post_corruption")
	if !ok || row.Columns[colVal].Int64 != 99999 {
		t.Error("post_corruption write should work")
	}
}

// corruptWALTail 损坏 WAL 文件尾部，模拟写入过程中断电。
func corruptWALTail(t *testing.T, walPath string, rng *rand.Rand) {
	t.Helper()
	info, err := os.Stat(walPath)
	if err != nil || info.Size() == 0 {
		return
	}
	truncateBytes := rng.Intn(50) + 1
	newSize := info.Size() - int64(truncateBytes)
	if newSize < 0 {
		newSize = 0
	}
	if err := os.Truncate(walPath, newSize); err != nil {
		t.Fatalf("truncate WAL: %v", err)
	}
}

// TestCrashRecovery_SegmentFileCorruption 验证段文件损坏时的恢复能力。
func TestCrashRecovery_SegmentFileCorruption(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	for batch := 0; batch < 4; batch++ {
		for j := 0; j < 5; j++ {
			key := fmt.Sprintf("batch%d_key_%d", batch, j)
			_ = eng.Write(key, map[string]common.Value{colVal: common.NewInt64(int64(batch*100 + j))})
		}
		if err := eng.Flush(cols); err != nil {
			t.Fatalf("flush batch %d: %v", batch, err)
		}
	}

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	var segFiles []string
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".widb" {
			segFiles = append(segFiles, entry.Name())
		}
	}

	if len(segFiles) >= 2 {
		targetIdx := len(segFiles) / 2
		targetPath := filepath.Join(dir, segFiles[targetIdx])
		if err := os.Remove(targetPath); err != nil {
			t.Fatalf("remove segment file %s: %v", targetPath, err)
		}
	}

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine after segment corruption: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	_ = eng2.Write("after_corruption", map[string]common.Value{colVal: common.NewInt64(9999)})
	row, ok := eng2.Get("after_corruption")
	if !ok || row.Columns[colVal].Int64 != 9999 {
		t.Error("write after segment corruption should work")
	}
}

// TestCrashRecovery_CorruptedWALRecordCRC 验证 WAL 记录 CRC 校验失败时的恢复行为。
func TestCrashRecovery_CorruptedWALRecordCRC(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	_ = eng.Write("safe_key1", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("safe_key2", map[string]common.Value{colVal: common.NewInt64(2)})
	_ = eng.Write("unsafe_key", map[string]common.Value{colVal: common.NewInt64(3)})
	_ = eng.Write("after_unsafe", map[string]common.Value{colVal: common.NewInt64(4)})

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	walPath := filepath.Join(dir, "wal.log")
	corruptWALRecordCRC(t, walPath, 2)

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine with corrupted CRC: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	row, ok := eng2.Get("safe_key1")
	if !ok || row.Columns[colVal].Int64 != 1 {
		t.Error("safe_key1 should be recovered")
	}

	row, ok = eng2.Get("safe_key2")
	if !ok || row.Columns[colVal].Int64 != 2 {
		t.Error("safe_key2 should be recovered")
	}
}

// corruptWALRecordCRC 损坏 WAL 文件中第 recordIdx 条记录的 CRC。
func corruptWALRecordCRC(t *testing.T, walPath string, recordIdx int) {
	t.Helper()
	data, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatalf("read WAL: %v", err)
	}

	offset := 0
	for i := 0; i <= recordIdx; i++ {
		if offset+walHeaderSize > len(data) {
			t.Fatalf("WAL too short to find record %d", recordIdx)
		}
		totalLen := int(binary.LittleEndian.Uint32(data[offset : offset+walHeaderSize]))
		if i == recordIdx {
			crcOffset := offset + walHeaderSize + walTypeSize + (totalLen - walTypeSize - walCRCSize)
			if crcOffset+walCRCSize <= len(data) {
				data[crcOffset] ^= 0xFF
			}
			break
		}
		offset += walHeaderSize + totalLen
	}

	if err := os.WriteFile(walPath, data, 0644); err != nil {
		t.Fatalf("write corrupted WAL: %v", err)
	}
}

// TestCrashRecovery_PartialWALRecord 验证 WAL 中存在部分写入记录时的恢复。
func TestCrashRecovery_PartialWALRecord(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	_ = eng.Write("safe1", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("safe2", map[string]common.Value{colVal: common.NewInt64(2)})
	_ = eng.Write("safe3", map[string]common.Value{colVal: common.NewInt64(3)})

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	walPath := filepath.Join(dir, "wal.log")
	f, err := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("open WAL for append: %v", err)
	}

	partialRecord := make([]byte, 7)
	binary.LittleEndian.PutUint32(partialRecord[0:4], 100)
	partialRecord[4] = walTypeWrite
	partialRecord[5] = 0xAA
	partialRecord[6] = 0xBB
	_, _ = f.Write(partialRecord)
	_ = f.Close()

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine with partial WAL record: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	row, ok := eng2.Get("safe1")
	if !ok || row.Columns[colVal].Int64 != 1 {
		t.Error("safe1 not recovered")
	}
	row, ok = eng2.Get("safe2")
	if !ok || row.Columns[colVal].Int64 != 2 {
		t.Error("safe2 not recovered")
	}
	row, ok = eng2.Get("safe3")
	if !ok || row.Columns[colVal].Int64 != 3 {
		t.Error("safe3 not recovered")
	}
}

// TestCrashRecovery_CorruptedSegmentHeader 验证段文件头部损坏时的恢复行为。
func TestCrashRecovery_CorruptedSegmentHeader(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	cols := []ColumnMeta{{ID: 0, Name: colVal, Type: common.TypeInt64}}

	_ = eng.Write("a", map[string]common.Value{colVal: common.NewInt64(1)})
	_ = eng.Write("b", map[string]common.Value{colVal: common.NewInt64(2)})
	_ = eng.Flush(cols)

	_ = eng.Write("c", map[string]common.Value{colVal: common.NewInt64(3)})
	_ = eng.Write("d", map[string]common.Value{colVal: common.NewInt64(4)})
	_ = eng.Flush(cols)

	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	var segFiles []string
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".widb" {
			segFiles = append(segFiles, filepath.Join(dir, entry.Name()))
		}
	}

	if len(segFiles) >= 2 {
		f, err := os.OpenFile(segFiles[0], os.O_WRONLY, 0644)
		if err != nil {
			t.Fatalf("open segment file: %v", err)
		}
		_, _ = f.WriteAt([]byte{0xDE, 0xAD, 0xBE, 0xEF}, 0)
		_ = f.Close()
	}

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine with corrupted segment: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	_ = eng2.Write("new_key", map[string]common.Value{colVal: common.NewInt64(999)})
	row, ok := eng2.Get("new_key")
	if !ok || row.Columns[colVal].Int64 != 999 {
		t.Error("new write should work after segment corruption")
	}
}

// TestCrashRecovery_CRCValidation 验证 WAL CRC 校验的正确性。
func TestCrashRecovery_CRCValidation(t *testing.T) {
	defer suppressLog()()
	dir := t.TempDir()
	cfg := EngineConfig{DataDir: dir}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	_ = eng.Write("valid_key", map[string]common.Value{colVal: common.NewInt64(42)})
	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	walPath := filepath.Join(dir, "wal.log")
	f, err := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("open WAL for append: %v", err)
	}

	payload := []byte("invalid payload data")
	totalLen := walTypeSize + len(payload) + walCRCSize
	buf := make([]byte, walHeaderSize+totalLen)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(totalLen))
	buf[4] = walTypeWrite
	copy(buf[5:], payload)
	binary.LittleEndian.PutUint32(buf[5+len(payload):], 0xDEADBEEF)
	_, _ = f.Write(buf)
	_ = f.Close()

	eng2, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer func() { _ = eng2.Close() }()

	row, ok := eng2.Get("valid_key")
	if !ok || row.Columns[colVal].Int64 != 42 {
		t.Error("valid_key should be recovered")
	}
}
