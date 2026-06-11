package storage

import (
	"fmt"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// WriteRow 是批量写入的单行数据。
type WriteRow struct {
	Key    string
	Values map[string]common.Value
}

// WriteBatch 批量写入多行数据，所有行共享一次 WAL sync，大幅提升批量写入吞吐。
// 优化：释放引擎锁进行 WAL I/O，避免阻塞并发读写；支持 GroupCommitter。
func (e *Engine) WriteBatch(rows []WriteRow) error {
	if len(rows) == 0 {
		return nil
	}

	// Step 1: Allocate versions under lock (brief hold)
	e.mu.Lock()
	baseVersion := e.nextVersion
	e.nextVersion += uint64(len(rows))
	e.mu.Unlock()

	// Step 2: Serialize WAL record (no lock needed, CPU-bound)
	payload, err := serializeBatchWriteRecord(rows, baseVersion)
	if err != nil {
		// Rollback version on serialization failure
		e.mu.Lock()
		e.nextVersion = baseVersion
		e.mu.Unlock()
		return fmt.Errorf("engine write batch: serialize: %w", err)
	}

	// Step 3: WAL append + sync (I/O-bound, no engine lock needed)
	if err := e.wal.AppendBatch([]BatchRecord{{Type: walTypeBatchWrite, Payload: payload}}); err != nil {
		e.mu.Lock()
		e.nextVersion = baseVersion
		e.mu.Unlock()
		return fmt.Errorf("engine write batch: wal: %w", err)
	}

	var syncCh <-chan struct{}
	if e.groupCommitter != nil {
		syncCh = e.groupCommitter.Submit()
	} else if err := e.wal.Sync(); err != nil {
		e.mu.Lock()
		e.nextVersion = baseVersion
		e.mu.Unlock()
		return fmt.Errorf("engine write batch: sync: %w", err)
	}

	// Step 4: Put all rows to memtable under lock (brief hold)
	e.mu.Lock()
	for i := range rows {
		if _, _, err := e.activeMem.Put(rows[i].Key, Row{Version: baseVersion + uint64(i), Columns: rows[i].Values}); err != nil {
			e.mu.Unlock()
			return fmt.Errorf("engine write batch: %w", err)
		}
	}
	if e.activeMem.ShouldFlush() {
		if err := e.rotateMemTable(); err != nil {
			e.mu.Unlock()
			return fmt.Errorf("engine write batch: rotate: %w", err)
		}
	}
	e.mu.Unlock()

	// Step 5: Wait for WAL sync completion (outside engine lock)
	if syncCh != nil {
		<-syncCh
	}

	return nil
}
