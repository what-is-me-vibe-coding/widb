package storage

import (
	"fmt"
	"os"
	"sync"
	"time"
)

const (
	defaultFlushInterval     = 5 * time.Second
	defaultCompactInterval   = 10 * time.Second
	defaultWALCleanInterval  = 30 * time.Second
	defaultWALCleanThreshold = 64 << 20 // 64MB
)

// SchedulerConfig 是后台任务调度器的配置参数。
type SchedulerConfig struct {
	FlushInterval     time.Duration // 自动刷盘检查间隔，0 表示禁用
	CompactInterval   time.Duration // 自动 Compaction 检查间隔，0 表示禁用
	WALCleanInterval  time.Duration // WAL 清理检查间隔，0 表示禁用
	WALCleanThreshold int64         // WAL 文件大小超过此阈值时清理旧文件（字节）
}

// SchedulerStats 是调度器的运行统计信息。
type SchedulerStats struct {
	FlushCount    int
	CompactCount  int
	WALCleanCount int
	LastError     string
}

// Scheduler 后台任务调度器，定时执行刷盘、Compaction 和 WAL 清理。
type Scheduler struct {
	engine *Engine
	config SchedulerConfig

	mu      sync.Mutex
	stopCh  chan struct{}
	stopped bool
	wg      sync.WaitGroup

	stats SchedulerStats
}

// NewScheduler 创建一个后台任务调度器。
func NewScheduler(engine *Engine, cfg SchedulerConfig) *Scheduler {
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = defaultFlushInterval
	}
	if cfg.CompactInterval <= 0 {
		cfg.CompactInterval = defaultCompactInterval
	}
	if cfg.WALCleanInterval <= 0 {
		cfg.WALCleanInterval = defaultWALCleanInterval
	}
	if cfg.WALCleanThreshold <= 0 {
		cfg.WALCleanThreshold = defaultWALCleanThreshold
	}

	return &Scheduler{
		engine:  engine,
		config:  cfg,
		stopCh:  make(chan struct{}),
		stopped: true,
	}
}

// Start 启动后台任务调度器。
func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.stopped {
		// 已在运行
		return
	}
	s.stopped = false
	s.stopCh = make(chan struct{})

	s.wg.Add(3)
	go s.runFlushLoop()
	go s.runCompactLoop()
	go s.runWALCleanLoop()
}

// Stop 停止后台任务调度器，等待所有任务完成。
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	close(s.stopCh)
	s.mu.Unlock()

	s.wg.Wait()
}

// Stats 返回调度器的运行统计信息。
func (s *Scheduler) Stats() SchedulerStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// runFlushLoop 定时检查并刷盘 Immutable MemTable。
func (s *Scheduler) runFlushLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.config.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			if err := s.tryFlush(); err != nil {
				s.recordError(err)
			}
		}
	}
}

// runCompactLoop 定时检查并执行 Compaction。
func (s *Scheduler) runCompactLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.config.CompactInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			if err := s.tryCompact(); err != nil {
				s.recordError(err)
			}
		}
	}
}

// runWALCleanLoop 定时检查并清理旧 WAL 文件。
func (s *Scheduler) runWALCleanLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.config.WALCleanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			if err := s.tryCleanWAL(); err != nil {
				s.recordError(err)
			}
		}
	}
}

// tryFlush 尝试刷盘 Immutable MemTable。
func (s *Scheduler) tryFlush() error {
	s.engine.mu.RLock()
	hasImmutable := len(s.engine.immutable) > 0
	shouldFlush := s.engine.activeMem.ShouldFlush()
	cols := s.engine.columnMeta
	s.engine.mu.RUnlock()

	if !hasImmutable && !shouldFlush {
		return nil
	}

	if err := s.engine.Flush(cols); err != nil {
		return fmt.Errorf("scheduler flush: %w", err)
	}

	s.mu.Lock()
	s.stats.FlushCount++
	s.mu.Unlock()

	return nil
}

// tryCompact 检查是否需要 Compaction 并执行。
func (s *Scheduler) tryCompact() error {
	if !s.engine.ShouldCompact() {
		return nil
	}

	cols := s.engine.ColumnMeta()

	if err := s.engine.Compact(cols); err != nil {
		return fmt.Errorf("scheduler compact: %w", err)
	}

	s.mu.Lock()
	s.stats.CompactCount++
	s.mu.Unlock()

	return nil
}

// tryCleanWAL 检查并清理旧 WAL 文件。
func (s *Scheduler) tryCleanWAL() error {
	s.engine.mu.RLock()
	walPath := s.engine.wal.path
	s.engine.mu.RUnlock()

	prevPath := walPath + ".prev"
	info, err := os.Stat(prevPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("scheduler wal clean stat: %w", err)
	}

	if info.Size() < s.config.WALCleanThreshold {
		return nil
	}

	if err := os.Remove(prevPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("scheduler wal clean remove: %w", err)
	}

	s.mu.Lock()
	s.stats.WALCleanCount++
	s.mu.Unlock()

	return nil
}

// recordError 记录最近一次错误信息。
func (s *Scheduler) recordError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.LastError = err.Error()
}
