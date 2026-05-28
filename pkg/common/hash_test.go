package common

import (
	"testing"
)

func TestHash(t *testing.T) {
	data := []byte("hello world")
	hash1 := Hash(data)
	hash2 := Hash(data)
	if hash1 != hash2 {
		t.Error("Same data should produce same hash")
	}

	data2 := []byte("hello world!")
	hash3 := Hash(data2)
	if hash1 == hash3 {
		t.Error("Different data should produce different hash")
	}
}

func TestHashString(t *testing.T) {
	hash1 := HashString("test")
	hash2 := HashString("test")
	if hash1 != hash2 {
		t.Error("Same string should produce same hash")
	}
}

func TestHashInt64(t *testing.T) {
	v1 := int64(42)
	v2 := int64(42)
	v3 := int64(43)

	if HashInt64(v1) != HashInt64(v2) {
		t.Error("Same int64 should produce same hash")
	}
	if HashInt64(v1) == HashInt64(v3) {
		t.Error("Different int64 should produce different hash")
	}
}

func TestHashUint64(t *testing.T) {
	v1 := uint64(42)
	v2 := uint64(42)
	v3 := uint64(43)

	if HashUint64(v1) != HashUint64(v2) {
		t.Error("Same uint64 should produce same hash")
	}
	if HashUint64(v1) == HashUint64(v3) {
		t.Error("Different uint64 should produce different hash")
	}
}

func TestHashFloat64(t *testing.T) {
	v1 := float64(3.14)
	v2 := float64(3.14)
	v3 := float64(2.71)

	if HashFloat64(v1) != HashFloat64(v2) {
		t.Error("Same float64 should produce same hash")
	}
	if HashFloat64(v1) == HashFloat64(v3) {
		t.Error("Different float64 should produce different hash")
	}
}
