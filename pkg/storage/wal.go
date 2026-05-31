package storage

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sync"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const (
	walDefaultMaxSize = 64 << 20 // 64MB

	walTypeWrite      byte = 1
	walTypeCommit     byte = 2
	walTypeCheckpoint byte = 3

	walHeaderSize = 4 // 4 字节记录长度
	walTypeSize   = 1
	walCRCSize    = 4
	walMetaSize   = walHeaderSize + walTypeSize + walCRCSize // = 9

	maxRecordPayload = 8 << 20 // 8MB 单条记录最大长度
)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

// WAL 是预写日志实现，提供顺序追加写与崩溃恢复能力。
type WAL struct {
	file    *os.File
	path    string
	offset  int64
	maxSize int64
	mu      sync.Mutex
}

// RawRecord 表示从 WAL 文件中回放的一条原始记录。
type RawRecord struct {
	Type    byte
	Payload []byte
}

// CreateWAL 创建新的 WAL 文件。
func CreateWAL(path string) (*WAL, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("wal create: %w", err)
	}
	return &WAL{
		file:    f,
		path:    path,
		offset:  0,
		maxSize: walDefaultMaxSize,
	}, nil
}

// OpenWAL 打开已有 WAL 文件用于恢复，回放所有有效记录。
func OpenWAL(path string) (*WAL, []RawRecord, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("wal open: %w", err)
		}
		return nil, nil, fmt.Errorf("wal open: %w", err)
	}

	records, fileSize, err := replayWAL(f)
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("wal replay: %w", err)
	}

	w := &WAL{
		file:    f,
		path:    path,
		offset:  fileSize,
		maxSize: walDefaultMaxSize,
	}

	return w, records, nil
}

// Append 向 WAL 追加密一条类型为 tp、内容为 payload 的记录。
func (w *WAL) Append(tp byte, payload []byte) error {
	if len(payload) > maxRecordPayload {
		return fmt.Errorf("wal append: payload too large (%d bytes)", len(payload))
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.maybeRotate(); err != nil {
		return err
	}

	buf := encodeRecord(tp, payload)
	n, err := w.file.Write(buf)
	if err != nil {
		return fmt.Errorf("wal write: %w", err)
	}
	w.offset += int64(n)
	return nil
}

// AppendWrite 追加一条 Write 类型记录。
func (w *WAL) AppendWrite(payload []byte) error {
	return w.Append(walTypeWrite, payload)
}

// AppendCommit 追加一条 Commit 类型记录。
func (w *WAL) AppendCommit(payload []byte) error {
	return w.Append(walTypeCommit, payload)
}

// AppendCheckpoint 追加一条 Checkpoint 类型记录。
func (w *WAL) AppendCheckpoint(payload []byte) error {
	return w.Append(walTypeCheckpoint, payload)
}

// Sync 将 WAL 文件缓冲区刷入磁盘。
func (w *WAL) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Sync()
}

// Size 返回当前 WAL 文件的字节偏移量。
func (w *WAL) Size() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.offset
}

// Close 关闭 WAL 文件。
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

// maybeRotate 在未持有锁时检查是否需要切分 WAL 文件。
func (w *WAL) maybeRotate() error {
	if w.offset < w.maxSize {
		return nil
	}

	old := w.file
	rotatedPath := w.path + ".prev"

	if err := old.Close(); err != nil {
		return fmt.Errorf("wal rotate close: %w", err)
	}

	if err := os.Rename(w.path, rotatedPath); err != nil {
		return fmt.Errorf("wal rotate rename: %w", err)
	}

	f, err := os.Create(w.path)
	if err != nil {
		return fmt.Errorf("wal rotate create: %w", err)
	}
	w.file = f
	w.offset = 0
	return nil
}

// encodeRecord 将一条记录编码为字节流。
func encodeRecord(tp byte, payload []byte) []byte {
	totalLen := walTypeSize + len(payload) + walCRCSize
	buf := make([]byte, walHeaderSize+totalLen)

	binary.LittleEndian.PutUint32(buf[0:4], uint32(totalLen))
	buf[4] = tp
	copy(buf[5:], payload)

	crc := crc32.Checksum(buf[4:4+walTypeSize+len(payload)], crcTable)
	binary.LittleEndian.PutUint32(buf[5+len(payload):], crc)

	return buf
}

// replayWAL 从文件中回放所有有效记录，返回记录列表和文件大小。
func replayWAL(f *os.File) ([]RawRecord, int64, error) {
	var records []RawRecord
	header := make([]byte, walHeaderSize)

	for {
		_, err := io.ReadFull(f, header)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, 0, fmt.Errorf("wal replay read header: %w", err)
		}

		totalLen := binary.LittleEndian.Uint32(header)
		if totalLen < walTypeSize+walCRCSize || totalLen > uint32(maxRecordPayload+walTypeSize+walCRCSize) {
			return nil, 0, fmt.Errorf("%w: invalid record length %d", common.ErrCorruptedData, totalLen)
		}

		bodyLen := int(totalLen)
		body := make([]byte, bodyLen)
		if _, err := io.ReadFull(f, body); err != nil {
			return nil, 0, fmt.Errorf("wal replay read body: %w", err)
		}

		tp := body[0]
		payloadLen := bodyLen - walTypeSize - walCRCSize
		payload := body[1 : 1+payloadLen]

		storedCRC := binary.LittleEndian.Uint32(body[1+payloadLen:])
		computedCRC := crc32.Checksum(body[:1+payloadLen], crcTable)

		if storedCRC != computedCRC {
			return nil, 0, fmt.Errorf("%w: crc mismatch at record %d", common.ErrCorruptedData, len(records))
		}

		records = append(records, RawRecord{
			Type:    tp,
			Payload: payload,
		})
	}

	offset, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, 0, fmt.Errorf("wal replay seek: %w", err)
	}

	return records, offset, nil
}
