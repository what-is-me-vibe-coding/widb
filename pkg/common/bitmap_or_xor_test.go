package common

import (
	"testing"
)

// --- Bitmap Or 表驱动测试 ---

func TestBitmapOr(t *testing.T) {
	tests := []struct {
		name      string
		b1Setup   func() *Bitmap
		b2Setup   func() *Bitmap
		wantCount uint32
		wantGet   map[uint32]bool
	}{
		{
			name: testBitmapNonEmptyWithIntersection,
			b1Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(1)
				bm.Set(3)
				return bm
			},
			b2Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(3)
				bm.Set(5)
				return bm
			},
			wantCount: 3,
			wantGet:   map[uint32]bool{1: true, 3: true, 5: true},
		},
		{
			name: testBitmapNonEmptyNoIntersection,
			b1Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(1)
				return bm
			},
			b2Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(5)
				return bm
			},
			wantCount: 2,
			wantGet:   map[uint32]bool{1: true, 5: true},
		},
		{
			name: "空 bitmap Or 非空 bitmap",
			b1Setup: func() *Bitmap {
				return NewBitmap(10)
			},
			b2Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(1)
				bm.Set(3)
				return bm
			},
			wantCount: 2,
			wantGet:   map[uint32]bool{1: true, 3: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b1 := tt.b1Setup()
			b2 := tt.b2Setup()
			b1.Or(b2)
			if b1.Count() != tt.wantCount {
				t.Errorf("Or 后 Count() = %d, want %d", b1.Count(), tt.wantCount)
			}
			for idx, want := range tt.wantGet {
				if got := b1.Get(idx); got != want {
					t.Errorf("Or 后 Get(%d) = %v, want %v", idx, got, want)
				}
			}
		})
	}
}

func TestBitmapOr_EmptyBitmaps(t *testing.T) {
	tests := []struct {
		name      string
		b1Setup   func() *Bitmap
		b2Setup   func() *Bitmap
		wantCount uint32
		wantGet   map[uint32]bool
	}{
		{
			name: "非空 bitmap Or 空 bitmap",
			b1Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(1)
				bm.Set(3)
				return bm
			},
			b2Setup: func() *Bitmap {
				return NewBitmap(10)
			},
			wantCount: 2,
			wantGet:   map[uint32]bool{1: true, 3: true},
		},
		{
			name: testBitmapBothEmpty,
			b1Setup: func() *Bitmap {
				return NewBitmap(10)
			},
			b2Setup: func() *Bitmap {
				return NewBitmap(10)
			},
			wantCount: 0,
			wantGet:   map[uint32]bool{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b1 := tt.b1Setup()
			b2 := tt.b2Setup()
			b1.Or(b2)
			if b1.Count() != tt.wantCount {
				t.Errorf("Or 后 Count() = %d, want %d", b1.Count(), tt.wantCount)
			}
			for idx, want := range tt.wantGet {
				if got := b1.Get(idx); got != want {
					t.Errorf("Or 后 Get(%d) = %v, want %v", idx, got, want)
				}
			}
		})
	}
}

func TestBitmapOr_DifferentSizes(t *testing.T) {
	tests := []struct {
		name      string
		b1Setup   func() *Bitmap
		b2Setup   func() *Bitmap
		wantCount uint32
		wantGet   map[uint32]bool
	}{
		{
			name: "不同长度 - b1 更短，扩展到 b2 的长度",
			b1Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(1)
				return bm
			},
			b2Setup: func() *Bitmap {
				bm := NewBitmap(128)
				bm.Set(3)
				bm.Set(70)
				return bm
			},
			wantCount: 3,
			wantGet:   map[uint32]bool{1: true, 3: true, 70: true},
		},
		{
			name: testBitmapBothZeroLength,
			b1Setup: func() *Bitmap {
				return NewBitmap(0)
			},
			b2Setup: func() *Bitmap {
				return NewBitmap(0)
			},
			wantCount: 0,
			wantGet:   map[uint32]bool{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b1 := tt.b1Setup()
			b2 := tt.b2Setup()
			b1.Or(b2)
			if b1.Count() != tt.wantCount {
				t.Errorf("Or 后 Count() = %d, want %d", b1.Count(), tt.wantCount)
			}
			for idx, want := range tt.wantGet {
				if got := b1.Get(idx); got != want {
					t.Errorf("Or 后 Get(%d) = %v, want %v", idx, got, want)
				}
			}
		})
	}
}

// --- Bitmap Xor 表驱动测试 ---

func TestBitmapXor(t *testing.T) {
	tests := []struct {
		name      string
		b1Setup   func() *Bitmap
		b2Setup   func() *Bitmap
		wantCount uint32
		wantGet   map[uint32]bool
	}{
		{
			name: testBitmapNonEmptyWithIntersection,
			b1Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(1)
				bm.Set(3)
				return bm
			},
			b2Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(3)
				bm.Set(5)
				return bm
			},
			wantCount: 2,
			wantGet:   map[uint32]bool{1: true, 3: false, 5: true},
		},
		{
			name: testBitmapNonEmptyNoIntersection,
			b1Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(1)
				return bm
			},
			b2Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(5)
				return bm
			},
			wantCount: 2,
			wantGet:   map[uint32]bool{1: true, 5: true},
		},
		{
			name: "空 bitmap Xor 非空 bitmap",
			b1Setup: func() *Bitmap {
				return NewBitmap(10)
			},
			b2Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(1)
				bm.Set(3)
				return bm
			},
			wantCount: 2,
			wantGet:   map[uint32]bool{1: true, 3: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b1 := tt.b1Setup()
			b2 := tt.b2Setup()
			b1.Xor(b2)
			if b1.Count() != tt.wantCount {
				t.Errorf("Xor 后 Count() = %d, want %d", b1.Count(), tt.wantCount)
			}
			for idx, want := range tt.wantGet {
				if got := b1.Get(idx); got != want {
					t.Errorf("Xor 后 Get(%d) = %v, want %v", idx, got, want)
				}
			}
		})
	}
}

func TestBitmapXor_EmptyBitmaps(t *testing.T) {
	tests := []struct {
		name      string
		b1Setup   func() *Bitmap
		b2Setup   func() *Bitmap
		wantCount uint32
		wantGet   map[uint32]bool
	}{
		{
			name: "非空 bitmap Xor 空 bitmap",
			b1Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(1)
				bm.Set(3)
				return bm
			},
			b2Setup: func() *Bitmap {
				return NewBitmap(10)
			},
			wantCount: 2,
			wantGet:   map[uint32]bool{1: true, 3: true},
		},
		{
			name: testBitmapBothEmpty,
			b1Setup: func() *Bitmap {
				return NewBitmap(10)
			},
			b2Setup: func() *Bitmap {
				return NewBitmap(10)
			},
			wantCount: 0,
			wantGet:   map[uint32]bool{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b1 := tt.b1Setup()
			b2 := tt.b2Setup()
			b1.Xor(b2)
			if b1.Count() != tt.wantCount {
				t.Errorf("Xor 后 Count() = %d, want %d", b1.Count(), tt.wantCount)
			}
			for idx, want := range tt.wantGet {
				if got := b1.Get(idx); got != want {
					t.Errorf("Xor 后 Get(%d) = %v, want %v", idx, got, want)
				}
			}
		})
	}
}

func TestBitmapXor_DifferentSizes(t *testing.T) {
	tests := []struct {
		name      string
		b1Setup   func() *Bitmap
		b2Setup   func() *Bitmap
		wantCount uint32
		wantGet   map[uint32]bool
	}{
		{
			name: "不同长度 - b1 更短，扩展到 b2 的长度",
			b1Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(1)
				return bm
			},
			b2Setup: func() *Bitmap {
				bm := NewBitmap(128)
				bm.Set(1)
				bm.Set(70)
				return bm
			},
			wantCount: 1,
			wantGet:   map[uint32]bool{1: false, 70: true},
		},
		{
			name: "两个相同 bitmap 异或结果为空",
			b1Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(1)
				bm.Set(3)
				return bm
			},
			b2Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(1)
				bm.Set(3)
				return bm
			},
			wantCount: 0,
			wantGet:   map[uint32]bool{1: false, 3: false},
		},
		{
			name: testBitmapBothZeroLength,
			b1Setup: func() *Bitmap {
				return NewBitmap(0)
			},
			b2Setup: func() *Bitmap {
				return NewBitmap(0)
			},
			wantCount: 0,
			wantGet:   map[uint32]bool{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b1 := tt.b1Setup()
			b2 := tt.b2Setup()
			b1.Xor(b2)
			if b1.Count() != tt.wantCount {
				t.Errorf("Xor 后 Count() = %d, want %d", b1.Count(), tt.wantCount)
			}
			for idx, want := range tt.wantGet {
				if got := b1.Get(idx); got != want {
					t.Errorf("Xor 后 Get(%d) = %v, want %v", idx, got, want)
				}
			}
		})
	}
}
