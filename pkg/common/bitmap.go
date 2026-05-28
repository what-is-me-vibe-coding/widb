package common

import "math/bits"

// Bitmap 是一个位图实现，用于高效存储布尔值集合。
// 底层使用 uint64 数组，每 bit 表示一个布尔值。
type Bitmap struct {
	bits []uint64
	len  uint32
}

// NewBitmap 创建一个新的位图，初始化时可指定长度。
func NewBitmap(length uint32) *Bitmap {
	words := (length + 63) / 64
	return &Bitmap{
		bits: make([]uint64, words),
		len:  length,
	}
}

// NewBitmapFromBytes 从字节切片创建位图。
func NewBitmapFromBytes(data []byte) *Bitmap {
	if len(data) == 0 {
		return &Bitmap{}
	}
	words := (len(data) + 7) / 8
	bits := make([]uint64, words)
	for i := 0; i < len(data); i++ {
		word := i / 8
		for bit := uint(0); bit < 8; bit++ {
			if (data[i] & (1 << bit)) != 0 {
				bits[word] |= 1 << uint(i*8+int(bit))
			}
		}
	}
	return &Bitmap{
		bits: bits,
		len:  uint32(len(data) * 8),
	}
}

// Len 返回位图的长度（位数）。
func (b *Bitmap) Len() uint32 {
	return b.len
}

// Set 将指定位置设为 true。
func (b *Bitmap) Set(i uint32) {
	if i >= b.len {
		return
	}
	word := i / 64
	bit := i % 64
	b.bits[word] |= 1 << bit
}

// Clear 将指定位置设为 false。
func (b *Bitmap) Clear(i uint32) {
	if i >= b.len {
		return
	}
	word := i / 64
	bit := i % 64
	b.bits[word] &^= 1 << bit
}

// Get 获取指定位置的值。
func (b *Bitmap) Get(i uint32) bool {
	if i >= b.len {
		return false
	}
	word := i / 64
	bit := i % 64
	return (b.bits[word] & (1 << bit)) != 0
}

// Count 返回位图中 true 的个数。
func (b *Bitmap) Count() uint32 {
	var count uint32
	for _, word := range b.bits {
		count += uint32(bits.OnesCount64(word))
	}
	return count
}

// IsEmpty 判断位图是否全为 false。
func (b *Bitmap) IsEmpty() bool {
	for _, word := range b.bits {
		if word != 0 {
			return false
		}
	}
	return true
}

// Flip 翻转指定位的值。
func (b *Bitmap) Flip(i uint32) {
	if i >= b.len {
		return
	}
	word := i / 64
	bit := i % 64
	b.bits[word] ^= 1 << bit
}

// And 与另一个位图进行按位与操作，结果存入当前位图。
func (b *Bitmap) And(other *Bitmap) {
	minWords := len(b.bits)
	if len(other.bits) < minWords {
		minWords = len(other.bits)
	}
	for i := 0; i < minWords; i++ {
		b.bits[i] &= other.bits[i]
	}
	for i := minWords; i < len(b.bits); i++ {
		b.bits[i] = 0
	}
}

// Or 与另一个位图进行按位或操作，结果存入当前位图。
func (b *Bitmap) Or(other *Bitmap) {
	maxWords := len(b.bits)
	if len(other.bits) > maxWords {
		maxWords = len(other.bits)
	}
	if maxWords > len(b.bits) {
		newBits := make([]uint64, maxWords)
		copy(newBits, b.bits)
		b.bits = newBits
	}
	for i := 0; i < len(other.bits); i++ {
		b.bits[i] |= other.bits[i]
	}
}

// Xor 与另一个位图进行按位异或操作，结果存入当前位图。
func (b *Bitmap) Xor(other *Bitmap) {
	maxWords := len(b.bits)
	if len(other.bits) > maxWords {
		maxWords = len(other.bits)
	}
	if maxWords > len(b.bits) {
		newBits := make([]uint64, maxWords)
		copy(newBits, b.bits)
		b.bits = newBits
	}
	for i := 0; i < len(other.bits); i++ {
		b.bits[i] ^= other.bits[i]
	}
}

// Not 对当前位图取反。
func (b *Bitmap) Not() {
	for i := range b.bits {
		b.bits[i] = ^b.bits[i]
	}
}

// Equals 判断两个位图是否相等。
func (b *Bitmap) Equals(other *Bitmap) bool {
	if b.len != other.len {
		return false
	}
	for i := range b.bits {
		if b.bits[i] != other.bits[i] {
			return false
		}
	}
	return true
}

// ToBytes 将位图转换为字节切片。
func (b *Bitmap) ToBytes() []byte {
	if len(b.bits) == 0 {
		return nil
	}
	bytesLen := (b.len + 7) / 8
	result := make([]byte, bytesLen)
	for i := uint32(0); i < b.len; i++ {
		byteIdx := i / 8
		bitIdx := uint(i % 8)
		wordIdx := i / 64
		wordBitIdx := uint(i % 64)
		if wordIdx < uint32(len(b.bits)) && (b.bits[wordIdx]&(1<<wordBitIdx)) != 0 {
			result[byteIdx] |= 1 << bitIdx
		}
	}
	return result
}

// Clone 创建位图的副本。
func (b *Bitmap) Clone() *Bitmap {
	newBits := make([]uint64, len(b.bits))
	copy(newBits, b.bits)
	return &Bitmap{
		bits: newBits,
		len:  b.len,
	}
}

// ForEach 遍历所有为 true 的位置，调用回调函数。
func (b *Bitmap) ForEach(fn func(idx uint32)) {
	for i, word := range b.bits {
		if word == 0 {
			continue
		}
		for j := 0; j < 64; j++ {
			if (word & (1 << j)) != 0 {
				fn(uint32(i*64 + j))
			}
		}
	}
}
