package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// Engine 是存储引擎的顶层协调器，管理 WAL、MemTable 和 Segment。
type Engine struct {
	mu          sync.RWMutex
	activeMem   *MemTable
	immutable   []*MemTable
	wal         *WAL
	flusher     *Flusher
	segments    []*Segment
	nextVersion uint64
}

// EngineConfig 配置 Engine 的参数。
type EngineConfig struct {
	DataDir         string
	MaxMemTableSize int64
}

// NewEngine 创建一个新的存储引擎实例。
func NewEngine(cfg EngineConfig) (*Engine, error) {
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("engine: create data dir: %w", err)
	}

	walPath := filepath.Join(cfg.DataDir, "wal.log")
	wal, _, err := OpenWAL(walPath)
	if err != nil {
		wal, err = CreateWAL(walPath)
		if err != nil {
			return nil, fmt.Errorf("engine: create wal: %w", err)
		}
	}

	maxSize := cfg.MaxMemTableSize
	if maxSize <= 0 {
		maxSize = memTableDefaultSize
	}

	eng := &Engine{
		activeMem:   NewMemTableWithSize(maxSize),
		wal:         wal,
		flusher:     NewFlusher(cfg.DataDir),
		nextVersion: 1,
	}

	return eng, nil
}

// Write 写入一行数据，先写 WAL 再写 MemTable。
func (e *Engine) Write(key string, values map[string]common.Value) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	row := Row{
		Version: e.nextVersion,
		Columns: values,
	}
	e.nextVersion++

	if e.activeMem.ShouldFlush() {
		if err := e.rotateMemTable(); err != nil {
			return fmt.Errorf("engine write: rotate memtable: %w", err)
		}
	}

	_, _, err := e.activeMem.Put(key, row)
	if err != nil {
		return fmt.Errorf("engine write: %w", err)
	}

	return nil
}

// Get 从 MemTable 和 Immutable 中查询键对应的值。
func (e *Engine) Get(key string) (Row, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if row, ok := e.activeMem.Get(key); ok {
		return row, true
	}

	for i := len(e.immutable) - 1; i >= 0; i-- {
		if row, ok := e.immutable[i].Get(key); ok {
			return row, true
		}
	}

	return Row{}, false
}

// Scan 在 [start, end] 范围内扫描。
func (e *Engine) Scan(start, end string) []struct {
	Key   string
	Value Row
} {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var results []struct {
		Key   string
		Value Row
	}

	results = append(results, e.activeMem.Scan(start, end)...)

	for i := len(e.immutable) - 1; i >= 0; i-- {
		results = append(results, e.immutable[i].Scan(start, end)...)
	}

	return results
}

// Flush 将所有 ImmutableMemTable 刷盘为 Segment。
func (e *Engine) Flush(cols []ColumnMeta) error {
	e.mu.Lock()

	if e.activeMem.Len() > 0 {
		e.activeMem.Freeze()
		e.immutable = append(e.immutable, e.activeMem)
		e.activeMem = NewMemTableWithSize(e.activeMem.maxSize)
	}

	immutable := e.immutable
	e.immutable = nil
	e.mu.Unlock()

	for _, mem := range immutable {
		seg, err := e.flusher.Flush(mem, cols)
		if err != nil {
			return fmt.Errorf("engine flush: %w", err)
		}

		e.mu.Lock()
		e.segments = append(e.segments, seg)
		e.mu.Unlock()
	}

	return nil
}

// Segments 返回所有已刷盘的 Segment 列表。
func (e *Engine) Segments() []*Segment {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]*Segment, len(e.segments))
	copy(result, e.segments)
	return result
}

// MemTableSize 返回活跃 MemTable 的估算大小。
func (e *Engine) MemTableSize() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.activeMem.Size()
}

// Close 关闭引擎，清理资源。
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.wal.Sync(); err != nil {
		return fmt.Errorf("engine close: sync wal: %w", err)
	}
	if err := e.wal.Close(); err != nil {
		return fmt.Errorf("engine close: close wal: %w", err)
	}

	return nil
}

// rotateMemTable 冻结当前 MemTable 并创建新的。
func (e *Engine) rotateMemTable() error {
	if e.activeMem.Len() == 0 {
		return nil
	}

	e.activeMem.Freeze()
	e.immutable = append(e.immutable, e.activeMem)
	e.activeMem = NewMemTableWithSize(e.activeMem.maxSize)
	return nil
}
