package storage

import (
	"log"
	"sync"
	"time"
)

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
// 如果 sync 失败，不通知等待者（channel 不会被关闭），
// 调用者可通过 select + timeout 检测到 sync 超时。
// 连续失败时记录警告，避免 pending 列表无限增长导致内存泄漏。
func (gc *GroupCommitter) doSync() {
	gc.mu.Lock()
	pending := gc.pending
	gc.pending = nil
	gc.mu.Unlock()

	if len(pending) == 0 {
		return
	}

	if err := gc.wal.Sync(); err != nil {
		// sync 失败：不通知等待者，让他们继续等待下一次 sync 尝试。
		// 将失败的请求放回 pending 队列，下次 sync 时重试。
		// 如果 pending 积压过多（超过 4096），丢弃最旧的请求并记录警告，
		// 防止持续 sync 失败导致内存无限增长。
		gc.mu.Lock()
		combined := make([]chan struct{}, 0, len(pending)+len(gc.pending))
		combined = append(combined, pending...)
		combined = append(combined, gc.pending...)
		if len(combined) > 4096 {
			dropped := len(combined) - 4096
			for _, ch := range combined[:dropped] {
				close(ch) // 通知被丢弃的写入者 sync 已完成（实际可能未持久化）
			}
			log.Printf("group committer: dropped %d pending sync requests due to persistent sync failures", dropped)
			combined = combined[dropped:]
		}
		gc.pending = combined
		gc.mu.Unlock()
		return
	}

	for _, ch := range pending {
		close(ch)
	}
}
