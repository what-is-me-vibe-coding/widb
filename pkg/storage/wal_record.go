package storage

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// walCheckpointRecord 是 Checkpoint 记录的 JSON 序列化格式。
type walCheckpointRecord struct {
	LastFlushedVersion uint64       `json:"last_flushed_version"`
	ColumnMeta         []walColMeta `json:"column_meta,omitempty"`
}

// walColMeta 是 ColumnMeta 的 JSON 序列化格式。
type walColMeta struct {
	ID   uint32 `json:"id"`
	Name string `json:"name"`
	Typ  int    `json:"typ"`
}

func serializeWriteRecord(key string, version uint64, columns map[string]common.Value) ([]byte, error) {
	// 直接生成二进制格式，避免 []WriteRow 中间分配
	size := 2 + 2 + len(key) + 8 + 2
	for colName, v := range columns {
		size += 2 + len(colName) + 1 + 1 + valueBinarySize(v)
	}
	buf := make([]byte, 0, size)
	var b [8]byte // stack-allocated, eliminates one heap allocation per write
	// 行数 = 1
	binary.LittleEndian.PutUint16(b[:], 1)
	buf = append(buf, b[:2]...)
	// key
	binary.LittleEndian.PutUint16(b[:], uint16(len(key)))
	buf = append(buf, b[:2]...)
	buf = append(buf, key...)
	// version
	binary.LittleEndian.PutUint64(b[:], version)
	buf = append(buf, b[:8]...)
	// 列数
	binary.LittleEndian.PutUint16(b[:], uint16(len(columns)))
	buf = append(buf, b[:2]...)
	// 每列
	for colName, v := range columns {
		buf = appendValueBinary(buf, b[:], colName, v)
	}
	return buf, nil
}

func deserializeWriteRecord(data []byte) (string, uint64, map[string]common.Value, error) {
	rows, err := deserializeBatchWriteRecord(data)
	if err != nil {
		return "", 0, nil, fmt.Errorf("engine: deserialize write record: %w", err)
	}
	if len(rows) == 0 {
		return "", 0, nil, fmt.Errorf("engine: empty write record")
	}
	return rows[0].Key, rows[0].Version, rows[0].Values, nil
}

func serializeCheckpointRecord(lastFlushedVersion uint64, colMeta []ColumnMeta) ([]byte, error) {
	rec := walCheckpointRecord{
		LastFlushedVersion: lastFlushedVersion,
	}
	for _, cm := range colMeta {
		rec.ColumnMeta = append(rec.ColumnMeta, walColMeta{
			ID:   cm.ID,
			Name: cm.Name,
			Typ:  int(cm.Type),
		})
	}
	return json.Marshal(rec)
}

func deserializeCheckpointRecord(data []byte) (uint64, []ColumnMeta, error) {
	var rec walCheckpointRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return 0, nil, fmt.Errorf("engine: deserialize checkpoint record: %w", err)
	}
	var colMeta []ColumnMeta
	for _, cm := range rec.ColumnMeta {
		colMeta = append(colMeta, ColumnMeta{
			ID:   cm.ID,
			Name: cm.Name,
			Type: common.DataType(cm.Typ),
		})
	}
	return rec.LastFlushedVersion, colMeta, nil
}

// findLastCheckpoint scans WAL records to find the last checkpoint's flushed version and column meta.
func findLastCheckpoint(records []RawRecord) (uint64, []ColumnMeta) {
	var lastFlushedVersion uint64
	var lastColumnMeta []ColumnMeta
	var failedCount int
	for _, rec := range records {
		if rec.Type == walTypeCheckpoint {
			version, colMeta, err := deserializeCheckpointRecord(rec.Payload)
			if err != nil {
				log.Printf("engine: failed to deserialize checkpoint record: %v", err)
				failedCount++
				continue
			}
			if version > lastFlushedVersion {
				lastFlushedVersion = version
				lastColumnMeta = colMeta
			}
		}
	}
	if failedCount > 0 {
		log.Printf("engine: warning: %d checkpoint records failed to deserialize during recovery", failedCount)
	}
	return lastFlushedVersion, lastColumnMeta
}

// applyWriteRecords applies write records with version > lastFlushedVersion to the memtable,
// returning the maximum version seen and the count of records that failed to deserialize.
func applyWriteRecords(records []RawRecord, lastFlushedVersion uint64, mem *MemTable) (uint64, int) {
	var maxVersion uint64
	var failedCount int
	for _, rec := range records {
		v, ok := applySingleRecord(rec, lastFlushedVersion, mem)
		if !ok {
			failedCount++
			continue
		}
		if v > maxVersion {
			maxVersion = v
		}
	}
	if failedCount > 0 {
		log.Printf("engine: warning: %d write records failed to deserialize during recovery", failedCount)
	}
	return maxVersion, failedCount
}

// applySingleRecord 应用单条 WAL 写入记录到 memtable，返回最大版本号和是否成功。
func applySingleRecord(rec RawRecord, lastFlushedVersion uint64, mem *MemTable) (uint64, bool) {
	switch rec.Type {
	case walTypeWrite:
		return applySingleWriteRecord(rec.Payload, lastFlushedVersion, mem)
	case walTypeBatchWrite:
		return applyBatchWriteRecord(rec.Payload, lastFlushedVersion, mem)
	default:
		return 0, true
	}
}

func applySingleWriteRecord(payload []byte, lastFlushedVersion uint64, mem *MemTable) (uint64, bool) {
	key, version, columns, err := deserializeWriteRecord(payload)
	if err != nil {
		log.Printf("engine: failed to deserialize write record: %v", err)
		return 0, false
	}
	if version <= lastFlushedVersion {
		return 0, true
	}
	if _, _, err := mem.Put(key, Row{Version: version, Columns: columns}); err != nil {
		log.Printf("engine: WAL replay Put failed for key %q: %v", key, err)
		return version, true
	}
	return version, true
}

func applyBatchWriteRecord(payload []byte, lastFlushedVersion uint64, mem *MemTable) (uint64, bool) {
	batchRows, err := deserializeBatchWriteRecord(payload)
	if err != nil {
		log.Printf("engine: failed to deserialize batch write record: %v", err)
		return 0, false
	}
	var maxVersion uint64
	for _, br := range batchRows {
		if br.Version <= lastFlushedVersion {
			continue
		}
		if _, _, err := mem.Put(br.Key, Row{Version: br.Version, Columns: br.Values}); err != nil {
			log.Printf("engine: WAL replay batch Put failed for key %q: %v", br.Key, err)
		}
		if br.Version > maxVersion {
			maxVersion = br.Version
		}
	}
	return maxVersion, true
}

// replayWALRecords 将 WAL 回放记录应用到 MemTable。
func (e *Engine) replayWALRecords(records []RawRecord) error {
	lastFlushedVersion, lastColumnMeta := findLastCheckpoint(records)

	// Restore column meta from checkpoint if not already set
	if len(lastColumnMeta) > 0 && len(e.columnMeta) == 0 {
		e.columnMeta = make([]ColumnMeta, len(lastColumnMeta))
		copy(e.columnMeta, lastColumnMeta)
	}

	// Apply write records with version > lastFlushedVersion
	maxVersion, failedCount := applyWriteRecords(records, lastFlushedVersion, e.activeMem)

	if failedCount > 0 {
		log.Printf("engine: warning: %d write records were skipped during WAL replay due to deserialization errors", failedCount)
	}

	// Update nextVersion to be greater than any version seen
	if maxVersion >= e.nextVersion {
		e.nextVersion = maxVersion + 1
	}

	return nil
}

func (e *Engine) parseSegmentEntry(entry os.DirEntry) (*Segment, uint64, bool, error) {
	if entry.IsDir() {
		return nil, 0, false, nil
	}
	name := entry.Name()
	if !strings.HasPrefix(name, "segment_") || !strings.HasSuffix(name, ".widb") {
		return nil, 0, false, nil
	}

	filePath := filepath.Join(e.flusher.dataDir, name)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, 0, true, fmt.Errorf("failed to read segment file %s: %w", name, err)
	}

	seg, err := DeserializeSegment(data)
	if err != nil {
		return nil, 0, true, fmt.Errorf("failed to deserialize segment file %s: %w", name, err)
	}
	seg.FilePath = filePath

	// Extract segmentID from filename: segment_<id>.widb
	idStr := name[len("segment_") : len(name)-len(".widb")]
	var segID uint64
	if _, err := fmt.Sscanf(idStr, "%d", &segID); err == nil {
		seg.ID = segID
	}

	// Derive MinKey/MaxKey from sorted keys
	if len(seg.Keys) > 0 {
		seg.MinKey = seg.Keys[0]
		seg.MaxKey = seg.Keys[len(seg.Keys)-1]
	}

	return seg, segID, true, nil
}

// loadSegments 从磁盘加载已有的 Segment 文件。
func (e *Engine) loadSegments() error {
	entries, err := os.ReadDir(e.flusher.dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("engine: read data dir: %w", err)
	}

	var maxSegID uint64
	var segFileCount int
	var failedCount int
	for _, entry := range entries {
		seg, segID, isSegFile, parseErr := e.parseSegmentEntry(entry)
		if !isSegFile {
			continue
		}
		segFileCount++
		if parseErr != nil {
			log.Printf("engine: %v", parseErr)
			failedCount++
			continue
		}
		e.segments = append(e.segments, seg)
		e.segmentMap[seg.ID] = seg
		e.segmentLevels = append(e.segmentLevels, 0)
		e.l0SegmentCount++
		if segID > maxSegID {
			maxSegID = segID
		}
	}

	if failedCount > 0 {
		log.Printf("engine: warning: %d of %d segment files failed to load during recovery", failedCount, segFileCount)
		if failedCount == segFileCount {
			return fmt.Errorf("engine: all %d segment files failed to load during recovery", segFileCount)
		}
	}

	// Register indexes for loaded segments
	for i, seg := range e.segments {
		if err := e.registerSegmentIndexes(seg, e.segmentLevels[i]); err != nil {
			return fmt.Errorf("engine: register segment %d indexes: %w", seg.ID, err)
		}
	}

	// Update flusher and compactor nextID to avoid ID collisions
	if maxSegID > 0 {
		e.flusher.SetNextID(maxSegID)
		e.compactor.SetNextID(maxSegID)
	}

	return nil
}

// recoverOpen 尝试重新打开 WAL 文件用于错误恢复，失败时记录日志。
// 重置 offset 为 0，因为恢复打开后文件偏移量不确定，
// 避免残留的旧 offset 导致 maybeRotate 误判是否需要切分。
func (w *WAL) recoverOpen() {
	f, err := os.OpenFile(w.path, os.O_RDWR|os.O_CREATE, 0644)
	if err == nil {
		w.file = f
		w.offset.Store(0)
	} else {
		log.Printf("wal: recovery open failed: %v", err)
	}
}

// logClose 记录文件关闭错误。
func logClose(f *os.File) {
	if err := f.Close(); err != nil {
		log.Printf("wal: close file in error path: %v", err)
	}
}

// logRemove 记录文件删除错误。
func logRemove(path string) {
	if err := os.Remove(path); err != nil {
		log.Printf("wal: remove file in error path: %v", err)
	}
}

// writeAndSyncFile 将数据写入文件并调用 fsync 确保数据持久化到磁盘。
// 使用 OpenFile + Write + Sync + Close 替代 os.WriteFile，
// 避免崩溃时丢失已写入但未落盘的数据。
func writeAndSyncFile(name string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	writeErr := func() error {
		if _, err := f.Write(data); err != nil {
			return err
		}
		return f.Sync()
	}()
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
