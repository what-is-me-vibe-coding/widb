package storage

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// walWriteRecord 是 Write 操作的 JSON 序列化格式。
type walWriteRecord struct {
	Key     string                    `json:"key"`
	Version uint64                    `json:"version"`
	Columns map[string]walValueRecord `json:"columns"`
}

// walValueRecord 是 common.Value 的 JSON 序列化格式。
type walValueRecord struct {
	Typ     int     `json:"typ"`
	Valid   bool    `json:"valid"`
	Int64   int64   `json:"int64"`
	Float64 float64 `json:"float64"`
	Str     string  `json:"str"`
	Time    string  `json:"time"`
}

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
	rec := walWriteRecord{
		Key:     key,
		Version: version,
		Columns: make(map[string]walValueRecord, len(columns)),
	}
	for colName, v := range columns {
		rec.Columns[colName] = walValueRecord{
			Typ:     int(v.Typ),
			Valid:   v.Valid,
			Int64:   v.Int64,
			Float64: v.Float64,
			Str:     v.Str,
			Time:    v.Time.Format(time.RFC3339Nano),
		}
	}
	return json.Marshal(rec)
}

func deserializeWriteRecord(data []byte) (string, uint64, map[string]common.Value, error) {
	var rec walWriteRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return "", 0, nil, fmt.Errorf("engine: deserialize write record: %w", err)
	}
	columns := make(map[string]common.Value, len(rec.Columns))
	for colName, v := range rec.Columns {
		val := common.Value{
			Typ:     common.DataType(v.Typ),
			Valid:   v.Valid,
			Int64:   v.Int64,
			Float64: v.Float64,
			Str:     v.Str,
		}
		if v.Time != "" {
			t, err := time.Parse(time.RFC3339Nano, v.Time)
			if err != nil {
				return "", 0, nil, fmt.Errorf("engine: parse time %q: %w", v.Time, err)
			}
			val.Time = t
		}
		columns[colName] = val
	}
	return rec.Key, rec.Version, columns, nil
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
		if rec.Type == walTypeWrite {
			key, version, columns, err := deserializeWriteRecord(rec.Payload)
			if err != nil {
				log.Printf("engine: failed to deserialize write record: %v", err)
				failedCount++
				continue
			}
			if version <= lastFlushedVersion {
				continue
			}
			row := Row{
				Version: version,
				Columns: columns,
			}
			_, _, _ = mem.Put(key, row)
			if version > maxVersion {
				maxVersion = version
			}
		}
	}
	if failedCount > 0 {
		log.Printf("engine: warning: %d write records failed to deserialize during recovery", failedCount)
	}
	return maxVersion, failedCount
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

// parseSegmentEntry parses a directory entry into a Segment, returning the segment,
// its ID, whether the entry was a segment file, and an error if it was a segment file
// that failed to load.
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

	// Extract segment ID from filename: segment_<id>.widb
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
		e.segmentLevels = append(e.segmentLevels, 0)
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
		e.registerSegmentIndexes(seg, e.segmentLevels[i])
	}

	// Update flusher and compactor nextID to avoid ID collisions
	if maxSegID > 0 {
		e.flusher.nextID = maxSegID
		e.compactor.nextID = maxSegID
	}

	return nil
}
