package common

import (
	"testing"
)

func TestBitmapBasic(t *testing.T) {
	bm := NewBitmap(100)

	if bm.Len() != 100 {
		t.Errorf("Len() = %d, want 100", bm.Len())
	}

	bm.Set(10)
	bm.Set(50)
	bm.Set(99)

	if !bm.Get(10) {
		t.Error("Get(10) = false, want true")
	}
	if !bm.Get(50) {
		t.Error("Get(50) = false, want true")
	}
	if !bm.Get(99) {
		t.Error("Get(99) = false, want true")
	}
	if bm.Get(11) {
		t.Error("Get(11) = true, want false")
	}

	if bm.Count() != 3 {
		t.Errorf("Count() = %d, want 3", bm.Count())
	}

	bm.Clear(50)
	if bm.Get(50) {
		t.Error("Get(50) after Clear = true, want false")
	}
	if bm.Count() != 2 {
		t.Errorf("Count() after Clear = %d, want 2", bm.Count())
	}
}

func TestBitmapEdgeCases(t *testing.T) {
	bm := NewBitmap(0)
	if !bm.IsEmpty() {
		t.Error("Empty bitmap should be empty")
	}

	bm2 := NewBitmap(1)
	bm2.Set(0)
	if bm2.Count() != 1 {
		t.Errorf("Count() = %d, want 1", bm2.Count())
	}

	bm3 := NewBitmap(64)
	bm3.Set(63)
	if !bm3.Get(63) {
		t.Error("Get(63) = false, want true")
	}
}

func TestBitmapOperations(t *testing.T) {
	bm1 := NewBitmap(10)
	bm1.Set(1)
	bm1.Set(3)

	bm2 := NewBitmap(10)
	bm2.Set(3)
	bm2.Set(5)

	bmAnd := bm1.Clone()
	bmAnd.And(bm2)
	if bmAnd.Count() != 1 || !bmAnd.Get(3) {
		t.Error("And operation failed")
	}

	bmOr := bm1.Clone()
	bmOr.Or(bm2)
	if bmOr.Count() != 3 {
		t.Errorf("Or Count() = %d, want 3", bmOr.Count())
	}

	bmXor := bm1.Clone()
	bmXor.Xor(bm2)
	if bmXor.Count() != 2 {
		t.Errorf("Xor Count() = %d, want 2", bmXor.Count())
	}
}

func TestBitmapBytes(t *testing.T) {
	bm := NewBitmap(10)
	bm.Set(0)
	bm.Set(1)
	bm.Set(7)

	bytes := bm.ToBytes()
	if len(bytes) != 2 {
		t.Errorf("ToBytes() len = %d, want 2", len(bytes))
	}

	bm2 := NewBitmapFromBytes(bytes)
	if !bm2.Get(0) || !bm2.Get(1) || !bm2.Get(7) {
		t.Error("NewBitmapFromBytes failed")
	}
}

func TestBitmapForEach(t *testing.T) {
	bm := NewBitmap(10)
	bm.Set(2)
	bm.Set(5)
	bm.Set(7)

	count := 0
	bm.ForEach(func(idx uint32) {
		count++
	})
	if count != 3 {
		t.Errorf("ForEach count = %d, want 3", count)
	}
}
