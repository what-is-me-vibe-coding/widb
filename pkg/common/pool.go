package common

import (
	"sync"
)

var (
	defaultPool = NewBufferPool()
)

// BufferPool 管理字节切片的复用，减少 GC 压力。
type BufferPool struct {
	pool sync.Pool
}

// NewBufferPool 创建一个新的 BufferPool。
func NewBufferPool() *BufferPool {
	return &BufferPool{
		pool: sync.Pool{
			New: func() any {
				buf := make([]byte, 0, 4096)
				return &buf
			},
		},
	}
}

// Get 从池中获取一个字节切片。
// 返回的切片长度为 0，但有一定的容量。
func (p *BufferPool) Get() []byte {
	bufPtr := p.pool.Get().(*[]byte)
	return (*bufPtr)[:0]
}

// Put 将字节切片放回池中。
// 注意：调用者必须确保不会再使用该切片。
func (p *BufferPool) Put(b []byte) {
	if cap(b) > 0 {
		p.pool.Put(&b)
	}
}

// GetSize 获取指定大小的缓冲区，确保容量足够。
func (p *BufferPool) GetSize(size int) []byte {
	b := p.Get()
	if cap(b) < size {
		b = make([]byte, 0, size)
	}
	return b[:0]
}

// GetDefaultBufferPool 获取默认的 BufferPool。
func GetDefaultBufferPool() *BufferPool {
	return defaultPool
}
