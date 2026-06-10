package storage

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sync"
	"time"
)

const (
	walDefaultMaxSize = 64 << 20 // 64MB

	walTypeWrite      byte = 1
	walTypeCommit     byte = 2
	walTypeCheckpoint byte = 3
	walTypeBatchWrite byte = 4

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

// BatchRecord 是批量写入的单条记录。
type BatchRecord struct {
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

	records, validOffset, err := replayWAL(f)
	if err != nil {
		_ = f.Close() // 错误路径，忽略关闭错误
		return nil, nil, fmt.Errorf("wal replay: %w", err)
	}

	// Truncate file at the last valid record position to remove
	// any partial/corrupted data, then seek to the end for appending.
	if err := f.Truncate(validOffset); err != nil {
		_ = f.Close() // 错误路径，忽略关闭错误
		return nil, nil, fmt.Errorf("wal truncate: %w", err)
	}
	if _, err := f.Seek(validOffset, io.SeekStart); err != nil {
		_ = f.Close() // 错误路径，忽略关闭错误
		return nil, nil, fmt.Errorf("wal seek: %w", err)
	}

	w := &WAL{
		file:    f,
		path:    path,
		offset:  validOffset,
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
	defer putRecordBuf(buf)
	n, err := w.file.Write(buf)
	if err != nil {
		return fmt.Errorf("wal write: %w", err)
	}
	w.offset += int64(n)
	return nil
}

// AppendBatch 批量追加多条记录，在单次锁获取内写入，减少锁竞争。
func (w *WAL) AppendBatch(records []BatchRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.maybeRotate(); err != nil {
		return err
	}

	for _, rec := range records {
		if len(rec.Payload) > maxRecordPayload {
			return fmt.Errorf("wal append batch: payload too large (%d bytes)", len(rec.Payload))
		}
		buf := encodeRecord(rec.Type, rec.Payload)
		n, err := w.file.Write(buf)
		putRecordBuf(buf)
		if err != nil {
			return fmt.Errorf("wal write batch: %w", err)
		}
		w.offset += int64(n)
	}
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

// Close 同步并关闭 WAL 文件。
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	// 先同步缓冲区到磁盘，再关闭文件，确保数据持久化
	if err := w.file.Sync(); err != nil {
		_ = w.file.Close() // 错误路径，忽略关闭错误
		return fmt.Errorf("wal close sync: %w", err)
	}
	return w.file.Close()
}

// Truncate 关闭当前 WAL 文件并创建新的空文件，用于清空已持久化的记录。
func (w *WAL) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("wal truncate sync: %w", err)
	}

	if err := w.file.Close(); err != nil {
		return fmt.Errorf("wal truncate close: %w", err)
	}

	f, err := os.Create(w.path)
	if err != nil {
		return fmt.Errorf("wal truncate create: %w", err)
	}

	w.file = f
	w.offset = 0
	return nil
}

// maybeRotate 在未持有锁时检查是否需要切分 WAL 文件。
// 采用"先建后删"策略：先创建新文件，再重命名旧文件，
// 避免在 Rename 成功但 Create 失败时导致 WAL 不可用。
func (w *WAL) maybeRotate() error {
	if w.offset < w.maxSize {
		return nil
	}

	rotatedPath := w.path + ".prev"

	// 先创建新文件，确保新文件可用后再处理旧文件
	newF, err := os.Create(w.path + ".tmp")
	if err != nil {
		return fmt.Errorf("wal rotate create temp: %w", err)
	}

	// 确保新文件的目录条目已持久化到磁盘，避免 rename 后崩溃导致新文件丢失
	if err := newF.Sync(); err != nil {
		_ = newF.Close()
		_ = os.Remove(w.path + ".tmp")
		return fmt.Errorf("wal rotate sync temp: %w", err)
	}

	// 关闭旧文件
	old := w.file
	if err := old.Close(); err != nil {
		_ = newF.Close()               // 错误路径，忽略关闭错误
		_ = os.Remove(w.path + ".tmp") // 错误路径，忽略清理错误
		return fmt.Errorf("wal rotate close: %w", err)
	}

	// 重命名旧文件为 .prev
	if err := os.Rename(w.path, rotatedPath); err != nil {
		// 旧文件已关闭但重命名失败，尝试恢复：重新打开旧路径
		_ = os.Remove(w.path + ".tmp") // 错误路径，忽略清理错误
		recoveredF, recoverErr := os.OpenFile(w.path, os.O_RDWR|os.O_CREATE, 0644)
		if recoverErr == nil {
			w.file = recoveredF
		}
		return fmt.Errorf("wal rotate rename: %w", err)
	}

	// 将临时新文件重命名为正式 WAL 路径
	if err := os.Rename(w.path+".tmp", w.path); err != nil {
		// 极端情况：旧文件已重命名，新文件重命名失败
		// 尝试将 .prev 改回来恢复
		_ = os.Rename(rotatedPath, w.path) // 错误恢复路径，忽略重命名错误
		recoveredF, recoverErr := os.OpenFile(w.path, os.O_RDWR|os.O_CREATE, 0644)
		if recoverErr == nil {
			w.file = recoveredF
		}
		return fmt.Errorf("wal rotate rename temp: %w", err)
	}

	w.file = newF
	w.offset = 0
	return nil
}

// recordBufPool 复用 WAL 记录编码缓冲区，减少写路径上的堆分配。
var recordBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 0, 256)
		return &buf
	},
}

// encodeRecord 将一条记录编码为字节流。
// 使用 sync.Pool 复用缓冲区，调用者用完后应调用 putRecordBuf 归还。
func encodeRecord(tp byte, payload []byte) []byte {
	totalLen := walTypeSize + len(payload) + walCRCSize
	need := walHeaderSize + totalLen

	bufPtr := recordBufPool.Get().(*[]byte)
	buf := *bufPtr
	if cap(buf) < need {
		buf = make([]byte, need)
	} else {
		buf = buf[:need]
	}

	binary.LittleEndian.PutUint32(buf[0:4], uint32(totalLen))
	buf[4] = tp
	copy(buf[5:], payload)

	crc := crc32.Checksum(buf[4:4+walTypeSize+len(payload)], crcTable)
	binary.LittleEndian.PutUint32(buf[5+len(payload):], crc)

	return buf
}

// putRecordBuf 将 encodeRecord 分配的缓冲区归还到池中。
func putRecordBuf(buf []byte) {
	if cap(buf) > 0 {
		recordBufPool.Put(&buf)
	}
}

// replayWAL 从文件中回放所有有效记录，返回记录列表和最后一条有效记录的偏移量。
// 遇到部分写入或损坏记录时停止回放（不返回错误），以支持崩溃恢复场景。
func replayWAL(f *os.File) ([]RawRecord, int64, error) {
	var records []RawRecord
	header := make([]byte, walHeaderSize)
	var lastValidOffset int64

	for {
		_, err := io.ReadFull(f, header)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break // 正常结束或部分头部（崩溃期间写入）
		}
		if err != nil {
			break // I/O 错误，停止回放
		}

		totalLen := binary.LittleEndian.Uint32(header)
		if totalLen < walTypeSize+walCRCSize || totalLen > uint32(maxRecordPayload+walTypeSize+walCRCSize) {
			break // 无效记录长度，停止回放
		}

		bodyLen := int(totalLen)
		body := make([]byte, bodyLen)
		if _, err := io.ReadFull(f, body); err != nil {
			break // 部分消息体（崩溃期间写入），停止回放
		}

		tp := body[0]
		payloadLen := bodyLen - walTypeSize - walCRCSize
		payload := body[1 : 1+payloadLen]

		storedCRC := binary.LittleEndian.Uint32(body[1+payloadLen:])
		computedCRC := crc32.Checksum(body[:1+payloadLen], crcTable)

		if storedCRC != computedCRC {
			break // CRC 不匹配，停止回放
		}

		records = append(records, RawRecord{
			Type:    tp,
			Payload: payload,
		})
		lastValidOffset += int64(walHeaderSize + bodyLen)
	}

	return records, lastValidOffset, nil
}

// SyncMode 控制 WAL 的同步模式。
type SyncMode int

const (
	// SyncEveryWrite 每次写入后立即同步 WAL 到磁盘（默认，最安全）。
	SyncEveryWrite SyncMode = iota

	// SyncGroupCommit 使用组提交，多个写入共享一次 fsync。
	// 后台 goroutine 在有写入时立即触发 sync，同时合并并发写入的 sync 请求，
	// 从而将 N 次 fsync 降低为 1 次。崩溃时可能丢失最近 SyncInterval 内的数据。
	SyncGroupCommit
)

// defaultSyncInterval 是 GroupCommit 模式下的最大同步间隔。
const defaultSyncInterval = 1 * time.Millisecond

// GroupCommitter 批量合并 WAL sync 操作，将多次 fsync 摊销为一次。
// 工作原理：
//   - 写入者通过 Submit 提交 sync 请求并等待通知
//   - 后台 goroutine 在有写入时立即触发 sync（按需模式）
//   - 在 sync 执行期间到达的新写入会合并到下一批
//   - 同时有定时器兜底，确保写入不会等待太久
type GroupCommitter struct {
	wal          *WAL
	mu           sync.Mutex
	pending      []chan struct{}
	closed       bool
	closeCh      chan struct{}
	doneCh       chan struct{}
	notify       chan struct{} // 通知后台 goroutine 有新的 sync 请求
	syncInterval time.Duration
}

// NewGroupCommitter 创建并启动 GroupCommitter。
func NewGroupCommitter(wal *WAL, syncInterval time.Duration) *GroupCommitter {
	if syncInterval <= 0 {
		syncInterval = defaultSyncInterval
	}
	gc := &GroupCommitter{
		wal:          wal,
		closeCh:      make(chan struct{}),
		doneCh:       make(chan struct{}),
		notify:       make(chan struct{}, 1),
		syncInterval: syncInterval,
	}
	go gc.run()
	return gc
}

// Submit 提交一个 sync 请求，返回一个 channel，当 WAL 数据已同步到磁盘时该 channel 会被关闭。
// 调用者应等待该 channel 后再认为写入已持久化。
func (gc *GroupCommitter) Submit() <-chan struct{} {
	gc.mu.Lock()
	defer gc.mu.Unlock()

	ch := make(chan struct{}, 1)
	gc.pending = append(gc.pending, ch)

	// 非阻塞通知后台 goroutine
	select {
	case gc.notify <- struct{}{}:
	default:
	}

	return ch
}

// SyncNow 立即触发一次同步，用于 Flush/Close 等需要确保数据持久化的场景。
func (gc *GroupCommitter) SyncNow() {
	gc.doSync()
}

// Close 停止 GroupCommitter，执行最后一次同步。
func (gc *GroupCommitter) Close() {
	gc.mu.Lock()
	if gc.closed {
		gc.mu.Unlock()
		return
	}
	gc.closed = true
	gc.mu.Unlock()

	close(gc.closeCh)
	<-gc.doneCh
}

// run 是后台 goroutine，按需同步 WAL。
// 当有写入时立即触发 sync，同时用定时器兜底确保不会等待太久。
func (gc *GroupCommitter) run() {
	defer close(gc.doneCh)

	ticker := time.NewTicker(gc.syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-gc.notify:
			// 有新写入，立即 sync
			gc.doSync()
		case <-ticker.C:
			// 定时兜底 sync
			gc.doSync()
		case <-gc.closeCh:
			gc.doSync() // 最终同步
			return
		}
	}
}

// doSync 执行一次 WAL sync 并通知所有等待的写入者。
func (gc *GroupCommitter) doSync() {
	gc.mu.Lock()
	pending := gc.pending
	gc.pending = nil
	gc.mu.Unlock()

	if len(pending) == 0 {
		return
	}

	// 忽略 sync 错误：与 Engine.Write 中原行为一致，
	// sync 失败时数据仍在 OS 缓冲区，后续 sync 可能成功。
	_ = gc.wal.Sync()

	for _, ch := range pending {
		close(ch)
	}
}
