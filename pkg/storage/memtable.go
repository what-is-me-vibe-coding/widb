package storage

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

const (
	maxLevel            = 16
	skipListP           = 0.5
	memTableDefaultSize = 32 << 20 // 32MB
)

// skipNodePool 缓存跳表节点，减少高频写入场景下的 GC 压力。
// 每次 put 操作需要分配 skipNode + forward 切片，在 100k+ rows/s 写入下
// 会产生大量短生命周期对象。sync.Pool 让这些节点在 GC 间被复用。
var skipNodePool = sync.Pool{
	New: func() any {
		return &skipNode{
			forward: make([]*skipNode, maxLevel),
		}
	},
}

// Row 表示 MemTable 中的一行数据，包含版本号与列值映射。
// Tombstone 为 true 时表示该行是 DELETE 写入的墓碑标记，在读取时应被跳过。
type Row struct {
	Version   uint64
	Columns   map[string]common.Value
	Tombstone bool
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

	// 从池中获取节点，复用 forward 切片减少分配
	node := skipNodePool.Get().(*skipNode)
	node.key = key
	node.value = value
	// 清零超出 level 的 forward 指针（池化节点可能残留旧数据）
	for i := level + 1; i < maxLevel; i++ {
		node.forward[i] = nil
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

	// 归还节点到池中，减少 GC 压力
	node.key = ""
	node.value = Row{}
	for i := range node.forward {
		node.forward[i] = nil
	}
	skipNodePool.Put(node)

	return old, true
}

// scanRange 返回 [start, end] 范围内的所有键值对。
// 使用 findLess 定位起始节点，利用跳表 O(log n) 查找能力，
// 避免从 head.forward[0] 线性遍历的 O(n) 开销。
func (sl *skipList) scanRange(start, end string) []struct {
	Key   string
	Value Row
} {
	// 根据跳表大小估算结果容量，减少 append 扩容次数。
	// 估算因子 1/4：大多数范围查询仅命中跳表的一小部分数据，
	// 1/4 是在"小范围查询避免过度分配"与"大范围查询减少扩容"之间的折中。
	// 实际命中比例取决于业务负载，此处为经验值；若场景偏向全表扫描可适当增大。
	estCap := sl.size / 4
	if estCap < 16 {
		estCap = 16
	}
	if estCap > sl.size {
		estCap = sl.size
	}
	result := make([]struct {
		Key   string
		Value Row
	}, 0, estCap)

	// 使用 findLess 在 O(log n) 内定位 >= start 的第一个节点
	x := sl.findLess(start, nil)
	if x.forward[0] != nil && x.forward[0].key >= start {
		x = x.forward[0]
	} else {
		// x.forward[0] 为 nil 或 key < start，无满足条件的节点
		return result
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

// ScanRange 返回 [start, end] 范围内的所有键值对，直接以 ScanEntry 格式返回，
// 避免调用方再做一次结构体转换拷贝。
func (m *MemTable) ScanRange(start, end string) []ScanEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pairs := m.tree.scanRange(start, end)
	entries := make([]ScanEntry, len(pairs))
	for i, p := range pairs {
		entries[i] = ScanEntry{Key: p.Key, Value: p.Value}
	}
	return entries
}

// Scan 返回 [start, end] 范围内的所有键值对。
// 保留此方法以兼容已有调用方。
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
// 优化：使用固定估算值替代逐列遍历，减少 Put 热路径的 CPU 开销。
// 估算精度对 MemTable 刷盘阈值影响有限（阈值本身是经验值），
// 但减少每次 Put 的计算量对高吞吐写入场景有显著收益。
func estimateRowSize(row Row) int64 {
	size := int64(8) // Version
	colCount := len(row.Columns)
	if colCount == 0 {
		return size
	}
	// 每列平均开销：key(16) + Value(24) + map 开销(48) ≈ 88 字节
	// 这是基于常见 OLAP 负载的经验估算，比逐列遍历快 10x+
	size += int64(colCount) * 88
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
