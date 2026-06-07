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
func (e *Engine) WriteBatch(rows []WriteRow) error {
	if len(rows) == 0 {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	payload, err := serializeBatchWriteRecord(rows, e.nextVersion)
	if err != nil {
		return fmt.Errorf("engine write batch: serialize: %w", err)
	}
	if err := e.wal.AppendBatch([]BatchRecord{{Type: walTypeBatchWrite, Payload: payload}}); err != nil {
		return fmt.Errorf("engine write batch: wal: %w", err)
	}
	if err := e.wal.Sync(); err != nil {
		return fmt.Errorf("engine write batch: sync: %w", err)
	}
	// 先写入所有行到 MemTable，全部成功后再递增版本号，避免部分失败导致版本号跳跃
	baseVersion := e.nextVersion
	for i := range rows {
		if _, _, err := e.activeMem.Put(rows[i].Key, Row{Version: baseVersion + uint64(i), Columns: rows[i].Values}); err != nil {
			return fmt.Errorf("engine write batch: %w", err)
		}
	}
	e.nextVersion += uint64(len(rows))
	if e.activeMem.ShouldFlush() {
		if err := e.rotateMemTable(); err != nil {
			return fmt.Errorf("engine write batch: rotate: %w", err)
		}
	}
	return nil
}
