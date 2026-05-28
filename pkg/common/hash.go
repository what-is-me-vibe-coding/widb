package common

import (
	"encoding/binary"

	"github.com/cespare/xxhash/v2"
)

// Hash 计算字节切片的 64 位哈希值。
func Hash(data []byte) uint64 {
	return xxhash.Sum64(data)
}

// HashString 计算字符串的 64 位哈希值。
func HashString(s string) uint64 {
	return xxhash.Sum64String(s)
}

// HashInt64 计算 int64 的哈希值。
func HashInt64(v int64) uint64 {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(v))
	return xxhash.Sum64(buf[:])
}

// HashUint64 计算 uint64 的哈希值。
func HashUint64(v uint64) uint64 {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], v)
	return xxhash.Sum64(buf[:])
}

// HashFloat64 计算 float64 的哈希值。
func HashFloat64(v float64) uint64 {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(v))
	return xxhash.Sum64(buf[:])
}
