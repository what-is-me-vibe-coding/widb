package storage

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const (
	maxLevel            = 16
	skipListP           = 0.5
	memTableDefaultSize = 32 << 20 // 32MB
)

// Row 表示 MemTable 中的一行数据，包含版本号与列值映射。
type Row struct {
	Version uint64
	Columns map[string]common.Value
}

// skipNode 是跳表节点。
type skipNode struct {
	key     string
	value   Row
	forward []*skipNode
}

// skipList 是线程不安全的跳表实现，由 MemTable 通过锁保护。
type skipList struct {
	head  *skipNode
	level int
	size  int
	prev  []*skipNode // 可复用的前驱节点缓冲区，避免每次 put/delete 分配
}

func newSkipList() *skipList {
	return &skipList{
		head: &skipNode{
			forward: make([]*skipNode, maxLevel),
		},
		level: 0,
		prev:  make([]*skipNode, maxLevel),
	}
}

func (sl *skipList) randomLevel() int {
	level := 0
	for rand.Float64() < skipListP && level < maxLevel-1 {
		level++
	}
	return level
}

func (sl *skipList) findLess(key string, prev []*skipNode) *skipNode {
	x := sl.head
	for i := sl.level; i >= 0; i-- {
		for x.forward[i] != nil && x.forward[i].key < key {
			x = x.forward[i]
		}
		if prev != nil {
			prev[i] = x
		}
	}
	return x
}

// put 插入或更新键值对，返回旧值是否存在。
func (sl *skipList) put(key string, value Row) (Row, bool) {
	// 复用 prev 缓冲区，避免每次分配
	for i := range sl.prev {
		sl.prev[i] = nil
	}
	x := sl.findLess(key, sl.prev)

	if x.forward[0] != nil && x.forward[0].key == key {
		old := x.forward[0].value
		x.forward[0].value = value
		return old, true
	}

	level := sl.randomLevel()
	if level > sl.level {
		for i := sl.level + 1; i <= level; i++ {
			sl.prev[i] = sl.head
		}
		sl.level = level
	}

	node := &skipNode{
		key:     key,
		value:   value,
		forward: make([]*skipNode, level+1),
	}

	for i := 0; i <= level; i++ {
		node.forward[i] = sl.prev[i].forward[i]
		sl.prev[i].forward[i] = node
	}

	sl.size++
	return Row{}, false
}

// get 查询键对应的值，不存在时返回 false。
func (sl *skipList) get(key string) (Row, bool) {
	x := sl.findLess(key, nil)
	if x.forward[0] != nil && x.forward[0].key == key {
		return x.forward[0].value, true
	}
	return Row{}, false
}

// delete 删除键值对，返回旧值是否存在。
func (sl *skipList) delete(key string) (Row, bool) {
	// 复用 prev 缓冲区
	for i := range sl.prev {
		sl.prev[i] = nil
	}
	x := sl.findLess(key, sl.prev)

	if x.forward[0] == nil || x.forward[0].key != key {
		return Row{}, false
	}

	node := x.forward[0]
	old := node.value
	for i := 0; i <= sl.level; i++ {
		if sl.prev[i].forward[i] != node {
			break
		}
		sl.prev[i].forward[i] = node.forward[i]
	}

	for sl.level > 0 && sl.head.forward[sl.level] == nil {
		sl.level--
	}

	sl.size--
	return old, true
}

// scanRange 返回 [start, end] 范围内的所有键值对。
func (sl *skipList) scanRange(start, end string) []struct {
	Key   string
	Value Row
} {
	result := make([]struct {
		Key   string
		Value Row
	}, 0, 16)

	x := sl.head.forward[0]
	for x != nil && x.key < start {
		x = x.forward[0]
	}

	for x != nil && x.key <= end {
		result = append(result, struct {
			Key   string
			Value Row
		}{Key: x.key, Value: x.value})
		x = x.forward[0]
	}

	return result
}

// MemTable 是内存表实现，使用并发跳表存储键值对。
// 支持并发读写，达到阈值后可冻结为只读快照。
type MemTable struct {
	tree    *skipList
	size    int64
	mu      sync.RWMutex
	frozen  atomic.Bool
	maxSize int64
}

// NewMemTable 创建默认大小的 MemTable。
func NewMemTable() *MemTable {
	return NewMemTableWithSize(memTableDefaultSize)
}

// NewMemTableWithSize 创建指定大小阈值的 MemTable。
func NewMemTableWithSize(maxSize int64) *MemTable {
	return &MemTable{
		tree:    newSkipList(),
		maxSize: maxSize,
	}
}

// Put 插入或更新键值对，返回旧值是否存在。
func (m *MemTable) Put(key string, value Row) (Row, bool, error) {
	if m.frozen.Load() {
		return Row{}, false, common.ErrReadOnly
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	old, exists := m.tree.put(key, value)

	estimatedSize := int64(len(key)) + estimateRowSize(value)
	if exists {
		oldSize := int64(len(key)) + estimateRowSize(old)
		m.size += estimatedSize - oldSize
	} else {
		m.size += estimatedSize
	}

	return old, exists, nil
}

// Get 查询键对应的值，不存在时返回 false。
func (m *MemTable) Get(key string) (Row, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.tree.get(key)
}

// Delete 删除键值对，返回被删除的值及是否存在。
func (m *MemTable) Delete(key string) (Row, bool, error) {
	if m.frozen.Load() {
		return Row{}, false, common.ErrReadOnly
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	old, exists := m.tree.delete(key)
	if exists {
		estimatedSize := int64(len(key)) + estimateRowSize(old)
		m.size -= estimatedSize
	}

	return old, exists, nil
}

// Scan 返回 [start, end] 范围内的所有键值对。
func (m *MemTable) Scan(start, end string) []struct {
	Key   string
	Value Row
} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.tree.scanRange(start, end)
}

// Len 返回当前键值对数量。
func (m *MemTable) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tree.size
}

// Size 返回估算的内存占用（字节）。
func (m *MemTable) Size() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.size
}

// ShouldFlush 判断是否达到刷盘阈值。
func (m *MemTable) ShouldFlush() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.size >= m.maxSize
}

// Freeze 冻结 MemTable 为只读。
// 冻结后 Put/Delete 操作会返回 ErrReadOnly，Get/Scan 仍可正常使用。
func (m *MemTable) Freeze() {
	m.frozen.Store(true)
}

// IsFrozen 判断是否已冻结。
func (m *MemTable) IsFrozen() bool {
	return m.frozen.Load()
}

// estimateRowSize 估算 Row 的内存占用。
func estimateRowSize(row Row) int64 {
	size := int64(unsafe.Sizeof(row.Version))
	for k, v := range row.Columns {
		size += int64(len(k))
		switch v.Typ {
		case common.TypeString:
			size += int64(len(v.Str))
		case common.TypeInt64, common.TypeFloat64, common.TypeTimestamp:
			size += 8
		case common.TypeBool:
			size++
		}
		size += int64(unsafe.Sizeof(v))
	}
	return size
}

// All 返回 MemTable 中所有键值对，按键顺序排列。
func (m *MemTable) All() []KeyValue {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]KeyValue, 0, m.tree.size)
	x := m.tree.head.forward[0]
	for x != nil {
		result = append(result, KeyValue{Key: x.key, Value: x.value})
		x = x.forward[0]
	}
	return result
}

// String 返回 MemTable 的可读信息。
func (m *MemTable) String() string {
	return fmt.Sprintf("MemTable{entries=%d, size=%d, maxSize=%d, frozen=%v}",
		m.Len(), m.Size(), m.maxSize, m.IsFrozen())
}
