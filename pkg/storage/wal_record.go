package storage

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"time"

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

// --- 二进制批量写入序列化 ---

// serializeBatchWriteRecord 将多行数据序列化为二进制格式。
// 格式：uint16(行数) + 每行[keyLen(uint16)+key+version(uint64)+colCount(uint16)+每列...]
func serializeBatchWriteRecord(rows []WriteRow, nextVersion uint64) ([]byte, error) {
	// 预估大小
	size := 2 // uint16 行数
	for _, row := range rows {
		size += 2 + len(row.Key) + 8 + 2 // keyLen + key + version + colCount
		for colName, v := range row.Values {
			size += 2 + len(colName) + 1 + 1 + valueBinarySize(v)
		}
	}
	buf := make([]byte, 0, size)
	// 行数
	b := make([]byte, 8)
	binary.LittleEndian.PutUint16(b, uint16(len(rows)))
	buf = append(buf, b[:2]...)
	for _, row := range rows {
		// key
		binary.LittleEndian.PutUint16(b, uint16(len(row.Key)))
		buf = append(buf, b[:2]...)
		buf = append(buf, row.Key...)
		// version
		binary.LittleEndian.PutUint64(b, nextVersion)
		buf = append(buf, b[:8]...)
		nextVersion++
		// 列数
		binary.LittleEndian.PutUint16(b, uint16(len(row.Values)))
		buf = append(buf, b[:2]...)
		// 每列
		for colName, v := range row.Values {
			buf = appendValueBinary(buf, b, colName, v)
		}
	}
	return buf, nil
}

// batchWriteRow 是反序列化后的单行数据。
type batchWriteRow struct {
	Key     string
	Version uint64
	Values  map[string]common.Value
}

// deserializeBatchWriteRecord 从二进制格式反序列化多行数据。
func deserializeBatchWriteRecord(data []byte) ([]batchWriteRow, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("engine: batch write record too short")
	}
	off := 0
	rowCount := int(binary.LittleEndian.Uint16(data[off:]))
	off += 2
	rows := make([]batchWriteRow, 0, rowCount)
	b8 := make([]byte, 8)
	for i := 0; i < rowCount; i++ {
		if off+2 > len(data) {
			return nil, fmt.Errorf("engine: batch write record truncated at row %d key len", i)
		}
		keyLen := int(binary.LittleEndian.Uint16(data[off:]))
		off += 2
		if off+keyLen > len(data) {
			return nil, fmt.Errorf("engine: batch write record truncated at row %d key", i)
		}
		key := string(data[off : off+keyLen])
		off += keyLen
		if off+8 > len(data) {
			return nil, fmt.Errorf("engine: batch write record truncated at row %d version", i)
		}
		copy(b8, data[off:off+8])
		version := binary.LittleEndian.Uint64(b8)
		off += 8
		if off+2 > len(data) {
			return nil, fmt.Errorf("engine: batch write record truncated at row %d col count", i)
		}
		colCount := int(binary.LittleEndian.Uint16(data[off:]))
		off += 2
		values := make(map[string]common.Value, colCount)
		for j := 0; j < colCount; j++ {
			colName, val, n, err := readValueBinary(data[off:])
			if err != nil {
				return nil, fmt.Errorf("engine: batch write record col %d: %w", j, err)
			}
			off += n
			values[colName] = val
		}
		rows = append(rows, batchWriteRow{Key: key, Version: version, Values: values})
	}
	return rows, nil
}

// valueBinarySize 返回 Value 的二进制编码大小（不含列名）。
func valueBinarySize(v common.Value) int {
	switch v.Typ {
	case common.TypeBool:
		return 1
	case common.TypeInt64, common.TypeFloat64, common.TypeTimestamp:
		return 8
	case common.TypeString:
		return 2 + len(v.Str)
	default:
		return 0
	}
}

// appendValueBinary 将一列数据追加到 buf，b 为临时缓冲区。
func appendValueBinary(buf, b []byte, colName string, v common.Value) []byte {
	// 列名
	binary.LittleEndian.PutUint16(b, uint16(len(colName)))
	buf = append(buf, b[:2]...)
	buf = append(buf, colName...)
	// 数据类型
	buf = append(buf, byte(v.Typ))
	// valid 标志
	if v.Valid {
		buf = append(buf, 1)
	} else {
		buf = append(buf, 0)
	}
	// 值
	switch v.Typ {
	case common.TypeBool:
		if v.Int64 != 0 {
			buf = append(buf, 1)
		} else {
			buf = append(buf, 0)
		}
	case common.TypeInt64:
		binary.LittleEndian.PutUint64(b, uint64(v.Int64))
		buf = append(buf, b[:8]...)
	case common.TypeFloat64:
		binary.LittleEndian.PutUint64(b, math.Float64bits(v.Float64))
		buf = append(buf, b[:8]...)
	case common.TypeString:
		binary.LittleEndian.PutUint16(b, uint16(len(v.Str)))
		buf = append(buf, b[:2]...)
		buf = append(buf, v.Str...)
	case common.TypeTimestamp:
		binary.LittleEndian.PutUint64(b, uint64(v.Time.UnixNano()))
		buf = append(buf, b[:8]...)
	}
	return buf
}

// readValueBinary 从 data 读取一列数据，返回列名、值、读取字节数和错误。
func readValueBinary(data []byte) (string, common.Value, int, error) {
	off := 0
	if off+2 > len(data) {
		return "", common.Value{}, 0, fmt.Errorf("truncated col name len")
	}
	nameLen := int(binary.LittleEndian.Uint16(data[off:]))
	off += 2
	if off+nameLen > len(data) {
		return "", common.Value{}, 0, fmt.Errorf("truncated col name")
	}
	colName := string(data[off : off+nameLen])
	off += nameLen
	if off+2 > len(data) {
		return "", common.Value{}, 0, fmt.Errorf("truncated type/valid")
	}
	typ := common.DataType(data[off])
	off++
	valid := data[off] != 0
	off++
	val, n, err := readTypedValue(data[off:], typ)
	if err != nil {
		return "", common.Value{}, 0, err
	}
	val.Valid = valid
	return colName, val, off + n, nil
}

// readTypedValue 根据类型从 data 读取值，返回值、读取字节数和错误。
func readTypedValue(data []byte, typ common.DataType) (common.Value, int, error) {
	switch typ {
	case common.TypeBool:
		if len(data) < 1 {
			return common.Value{}, 0, fmt.Errorf("truncated bool value")
		}
		val := common.Value{Typ: typ}
		if data[0] != 0 {
			val.Int64 = 1
		}
		return val, 1, nil
	case common.TypeInt64:
		if len(data) < 8 {
			return common.Value{}, 0, fmt.Errorf("truncated int64 value")
		}
		return common.Value{Typ: typ, Int64: int64(binary.LittleEndian.Uint64(data[:8]))}, 8, nil
	case common.TypeFloat64:
		if len(data) < 8 {
			return common.Value{}, 0, fmt.Errorf("truncated float64 value")
		}
		return common.Value{Typ: typ, Float64: math.Float64frombits(binary.LittleEndian.Uint64(data[:8]))}, 8, nil
	case common.TypeString:
		if len(data) < 2 {
			return common.Value{}, 0, fmt.Errorf("truncated string len")
		}
		strLen := int(binary.LittleEndian.Uint16(data[:2]))
		if len(data) < 2+strLen {
			return common.Value{}, 0, fmt.Errorf("truncated string value")
		}
		return common.Value{Typ: typ, Str: string(data[2 : 2+strLen])}, 2 + strLen, nil
	case common.TypeTimestamp:
		if len(data) < 8 {
			return common.Value{}, 0, fmt.Errorf("truncated timestamp value")
		}
		return common.Value{Typ: typ, Time: time.Unix(0, int64(binary.LittleEndian.Uint64(data[:8])))}, 8, nil
	default:
		return common.Value{}, 0, fmt.Errorf("unknown value type: %d", typ)
	}
}

// recoverOpen 尝试重新打开 WAL 文件用于错误恢复，失败时记录日志。
func (w *WAL) recoverOpen() {
	f, err := os.OpenFile(w.path, os.O_RDWR|os.O_CREATE, 0644)
	if err == nil {
		w.file = f
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
