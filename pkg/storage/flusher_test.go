package storage

import (
	"os"
	"testing"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestFlusherFlush(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	mem := NewMemTable()

	rows := []struct {
		key string
		row Row
	}{
		{crKey1, Row{Version: 1, Columns: map[string]common.Value{
			"id":    common.NewInt64(1),
			colName: common.NewString("alice"),
			colAge:  common.NewInt64(30),
		}}},
		{crKey2, Row{Version: 1, Columns: map[string]common.Value{
			"id":    common.NewInt64(2),
			colName: common.NewString("bob"),
			colAge:  common.NewInt64(25),
		}}},
		{crKey3, Row{Version: 1, Columns: map[string]common.Value{
			"id":    common.NewInt64(3),
			colName: common.NewString("charlie"),
			colAge:  common.NewInt64(35),
		}}},
	}

	for _, r := range rows {
		_, _, err := mem.Put(r.key, r.row)
		if err != nil {
			t.Fatalf("put %s: %v", r.key, err)
		}
	}

	cols := []ColumnMeta{
		{ID: 0, Name: "id", Type: common.TypeInt64},
		{ID: 1, Name: colName, Type: common.TypeString},
		{ID: 2, Name: colAge, Type: common.TypeInt64},
	}

	seg, err := flusher.Flush(mem, cols)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}

	if seg.ID != 1 {
		t.Errorf("expected segment ID 1, got %d", seg.ID)
	}
	if seg.MinKey != crKey1 {
		t.Errorf("expected minKey=key1, got %s", seg.MinKey)
	}
	if seg.MaxKey != "key3" {
		t.Errorf("expected maxKey=key3, got %s", seg.MaxKey)
	}
	if seg.RowCount != 3 {
		t.Errorf("expected rowCount=3, got %d", seg.RowCount)
	}
	if seg.FilePath == "" {
		t.Error("expected non-empty FilePath")
	}

	if _, err := os.Stat(seg.FilePath); err != nil {
		t.Errorf("segment file not found: %v", err)
	}

	verifySegmentRoundTrip(t, seg)
}

func TestFlusherFlushWithNulls(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	mem := NewMemTable()
	_, _, _ = mem.Put("k1", Row{Version: 1, Columns: map[string]common.Value{
		colVal: common.NewInt64(100),
	}})
	_, _, _ = mem.Put("k2", Row{Version: 1, Columns: map[string]common.Value{
		colVal: common.NewNull(),
	}})
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
	if len(seg.Footer.ColumnStats) != 1 {
		t.Fatalf("expected 1 column stat, got %d", len(seg.Footer.ColumnStats))
	}
	if seg.Footer.ColumnStats[0].NullCount != 1 {
		t.Errorf("expected NullCount=1, got %d", seg.Footer.ColumnStats[0].NullCount)
	}

	verifySegmentRoundTrip(t, seg)
}

func TestFlusherFlushFloat64(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	mem := NewMemTable()
	_, _, _ = mem.Put("k1", Row{Version: 1, Columns: map[string]common.Value{
		colScore: common.NewFloat64(1.5),
	}})
	_, _, _ = mem.Put("k2", Row{Version: 1, Columns: map[string]common.Value{
		colScore: common.NewFloat64(2.7),
	}})

	cols := []ColumnMeta{
		{ID: 0, Name: colScore, Type: common.TypeFloat64},
	}

	seg, err := flusher.Flush(mem, cols)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}

	verifySegmentRoundTrip(t, seg)
}

func TestFlusherFlushBool(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	mem := NewMemTable()
	_, _, _ = mem.Put("k1", Row{Version: 1, Columns: map[string]common.Value{
		colActive: common.NewBool(true),
	}})
	_, _, _ = mem.Put("k2", Row{Version: 1, Columns: map[string]common.Value{
		colActive: common.NewBool(false),
	}})

	cols := []ColumnMeta{
		{ID: 0, Name: colActive, Type: common.TypeBool},
	}

	seg, err := flusher.Flush(mem, cols)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}

	verifySegmentRoundTrip(t, seg)
}

func TestFlusherFlushTimestamp(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	now := time.Now()
	mem := NewMemTable()
	_, _, _ = mem.Put("k1", Row{Version: 1, Columns: map[string]common.Value{
		"ts": common.NewTimestamp(now),
	}})
	_, _, _ = mem.Put("k2", Row{Version: 1, Columns: map[string]common.Value{
		"ts": common.NewTimestamp(now.Add(time.Hour)),
	}})

	cols := []ColumnMeta{
		{ID: 0, Name: "ts", Type: common.TypeTimestamp},
	}

	seg, err := flusher.Flush(mem, cols)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}

	verifySegmentRoundTrip(t, seg)
}

func TestFlusherFlushEmptyMemTable(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	mem := NewMemTable()
	cols := []ColumnMeta{
		{ID: 0, Name: colVal, Type: common.TypeInt64},
	}

	_, err := flusher.Flush(mem, cols)
	if err == nil {
		t.Fatal("expected error for empty memtable")
	}
}

func TestFlusherFlushMultiSegment(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	cols := []ColumnMeta{
		{ID: 0, Name: colVal, Type: common.TypeInt64},
	}

	mem1 := NewMemTable()
	_, _, _ = mem1.Put("k1", Row{Version: 1, Columns: map[string]common.Value{
		colVal: common.NewInt64(1),
	}})

	seg1, err := flusher.Flush(mem1, cols)
	if err != nil {
		t.Fatalf("flush 1: %v", err)
	}

	mem2 := NewMemTable()
	_, _, _ = mem2.Put("k2", Row{Version: 1, Columns: map[string]common.Value{
		colVal: common.NewInt64(2),
	}})

	seg2, err := flusher.Flush(mem2, cols)
	if err != nil {
		t.Fatalf("flush 2: %v", err)
	}

	if seg1.ID == seg2.ID {
		t.Errorf("expected different segment IDs, got %d and %d", seg1.ID, seg2.ID)
	}
	if seg1.FilePath == seg2.FilePath {
		t.Error("expected different file paths")
	}
}

func TestFlusherFlushMissingColumn(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	mem := NewMemTable()
	_, _, _ = mem.Put("k1", Row{Version: 1, Columns: map[string]common.Value{
		colVal: common.NewInt64(1),
	}})

	cols := []ColumnMeta{
		{ID: 0, Name: testColMissing, Type: common.TypeInt64},
	}

	seg, err := flusher.Flush(mem, cols)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}

	if seg.Footer.ColumnStats[0].NullCount != 1 {
		t.Errorf("expected NullCount=1 for missing column, got %d", seg.Footer.ColumnStats[0].NullCount)
	}
}

func TestFlusherWriteSegmentInvalidDir(t *testing.T) {
	// Use a path where a file exists as a directory name, causing MkdirAll to fail
	tmpFile, err := os.CreateTemp("", "flusher-blocker-*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	// Use the file path as dataDir - MkdirAll will fail because it's a file
	flusher := NewFlusher(tmpPath + "/subdir")

	seg := &Segment{ID: 1}
	_, err = flusher.writeSegment(seg)
	if err == nil {
		t.Error("expected error when writing segment to invalid directory")
	}
}

func TestFlusherBuildEncodedColumnString(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	mem := NewMemTable()
	_, _, _ = mem.Put("k1", Row{Version: 1, Columns: map[string]common.Value{
		colName: common.NewString(testStrHello),
	}})
	_, _, _ = mem.Put("k2", Row{Version: 1, Columns: map[string]common.Value{
		colName: common.NewString("world"),
	}})

	cols := []ColumnMeta{
		{ID: 0, Name: colName, Type: common.TypeString},
	}

	seg, err := flusher.Flush(mem, cols)
	if err != nil {
		t.Fatalf("flush string column: %v", err)
	}

	if seg.RowCount != 2 {
		t.Errorf("expected rowCount=2, got %d", seg.RowCount)
	}

	verifySegmentRoundTrip(t, seg)
}

func TestFlusherBuildEncodedColumnBool(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	mem := NewMemTable()
	_, _, _ = mem.Put("k1", Row{Version: 1, Columns: map[string]common.Value{
		colActive: common.NewBool(true),
	}})
	_, _, _ = mem.Put("k2", Row{Version: 1, Columns: map[string]common.Value{
		colActive: common.NewBool(false),
	}})

	cols := []ColumnMeta{
		{ID: 0, Name: colActive, Type: common.TypeBool},
	}

	seg, err := flusher.Flush(mem, cols)
	if err != nil {
		t.Fatalf("flush bool column: %v", err)
	}

	verifySegmentRoundTrip(t, seg)
}

func TestFlusherBuildEncodedColumnFloat64(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	mem := NewMemTable()
	_, _, _ = mem.Put("k1", Row{Version: 1, Columns: map[string]common.Value{
		colScore: common.NewFloat64(3.14),
	}})
	_, _, _ = mem.Put("k2", Row{Version: 1, Columns: map[string]common.Value{
		colScore: common.NewFloat64(2.71),
	}})

	cols := []ColumnMeta{
		{ID: 0, Name: colScore, Type: common.TypeFloat64},
	}

	seg, err := flusher.Flush(mem, cols)
	if err != nil {
		t.Fatalf("flush float64 column: %v", err)
	}

	verifySegmentRoundTrip(t, seg)
}

func TestFlusherBuildEncodedColumnTimestamp(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	now := time.Now()
	mem := NewMemTable()
	_, _, _ = mem.Put("k1", Row{Version: 1, Columns: map[string]common.Value{
		"ts": common.NewTimestamp(now),
	}})
	_, _, _ = mem.Put("k2", Row{Version: 1, Columns: map[string]common.Value{
		"ts": common.NewTimestamp(now.Add(time.Hour)),
	}})

	cols := []ColumnMeta{
		{ID: 0, Name: "ts", Type: common.TypeTimestamp},
	}

	seg, err := flusher.Flush(mem, cols)
	if err != nil {
		t.Fatalf("flush timestamp column: %v", err)
	}

	verifySegmentRoundTrip(t, seg)
}

func verifySegmentRoundTrip(t *testing.T, seg *Segment) {
	t.Helper()

	data, err := os.ReadFile(seg.FilePath)
	if err != nil {
		t.Fatalf("read segment file: %v", err)
	}

	deserialized, err := DeserializeSegment(data)
	if err != nil {
		t.Fatalf("deserialize segment: %v", err)
	}

	if deserialized.RowCount != seg.RowCount {
		t.Errorf("deserialized RowCount mismatch: got %d, want %d", deserialized.RowCount, seg.RowCount)
	}
	if len(deserialized.Columns) != len(seg.Columns) {
		t.Errorf("deserialized Columns count mismatch: got %d, want %d", len(deserialized.Columns), len(seg.Columns))
	}
	if len(deserialized.Footer.ColumnStats) != len(seg.Footer.ColumnStats) {
		t.Errorf("deserialized Footer.ColumnStats count mismatch: got %d, want %d",
			len(deserialized.Footer.ColumnStats), len(seg.Footer.ColumnStats))
	}
}

// TestFlusherFlushWithNullValues 测试 Flusher 处理包含 NULL 值的 MemTable
func TestFlusherFlushWithNullValues(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	mem := NewMemTable()
	// 第一行有完整值，第二行某些列为 NULL，第三行某些列为 NULL
	_, _, _ = mem.Put("k1", Row{Version: 1, Columns: map[string]common.Value{
		colVal:    common.NewInt64(100),
		colName:   common.NewString("alice"),
		colActive: common.NewBool(true),
	}})
	_, _, _ = mem.Put("k2", Row{Version: 1, Columns: map[string]common.Value{
		colVal: common.NewInt64(200),
		// colName 缺失 -> NULL
		colActive: common.NewBool(false),
	}})
	_, _, _ = mem.Put("k3", Row{Version: 1, Columns: map[string]common.Value{
		// colVal 缺失 -> NULL
		colName: common.NewString("charlie"),
		// colActive 缺失 -> NULL
	}})

	cols := []ColumnMeta{
		{ID: 0, Name: colVal, Type: common.TypeInt64},
		{ID: 1, Name: colName, Type: common.TypeString},
		{ID: 2, Name: colActive, Type: common.TypeBool},
	}

	seg, err := flusher.Flush(mem, cols)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}

	if seg.RowCount != 3 {
		t.Errorf("expected rowCount=3, got %d", seg.RowCount)
	}

	verifySegmentRoundTrip(t, seg)
}

// TestFlusherFlushTimestampValues 测试 Flusher 处理包含 Timestamp 值的 MemTable
func TestFlusherFlushTimestampValues(t *testing.T) {
	tmpDir := t.TempDir()
	flusher := NewFlusher(tmpDir)

	now := time.Now()
	mem := NewMemTable()
	_, _, _ = mem.Put("k1", Row{Version: 1, Columns: map[string]common.Value{
		"ts": common.NewTimestamp(now),
	}})
	_, _, _ = mem.Put("k2", Row{Version: 1, Columns: map[string]common.Value{
		"ts": common.NewTimestamp(now.Add(time.Hour)),
	}})
	_, _, _ = mem.Put("k3", Row{Version: 1, Columns: map[string]common.Value{
		"ts": common.NewTimestamp(now.Add(2 * time.Hour)),
	}})

	cols := []ColumnMeta{
		{ID: 0, Name: "ts", Type: common.TypeTimestamp},
	}

	seg, err := flusher.Flush(mem, cols)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}

	if seg.RowCount != 3 {
		t.Errorf("expected rowCount=3, got %d", seg.RowCount)
	}

	verifySegmentRoundTrip(t, seg)
}
