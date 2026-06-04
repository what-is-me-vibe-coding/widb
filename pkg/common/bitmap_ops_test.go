package common

import (
	"testing"
)

const (
	testBitmapNonEmptyWithIntersection = "两个非空 bitmap 有交集"
	testBitmapNonEmptyNoIntersection   = "两个非空 bitmap 无交集"
	testBitmapBothEmpty                = "两个空 bitmap"
	testBitmapBothZeroLength           = "两个零长度 bitmap"
)

// --- Bitmap And 表驱动测试 ---

func TestBitmapAnd(t *testing.T) {
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
				bm.Set(5)
				return bm
			},
			b2Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(3)
				bm.Set(5)
				bm.Set(7)
				return bm
			},
			wantCount: 2,
			wantGet:   map[uint32]bool{1: false, 3: true, 5: true, 7: false},
		},
		{
			name: testBitmapNonEmptyNoIntersection,
			b1Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(1)
				bm.Set(3)
				return bm
			},
			b2Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(5)
				bm.Set(7)
				return bm
			},
			wantCount: 0,
			wantGet:   map[uint32]bool{1: false, 3: false, 5: false, 7: false},
		},
		{
			name: "空 bitmap And 非空 bitmap",
			b1Setup: func() *Bitmap {
				return NewBitmap(10)
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b1 := tt.b1Setup()
			b2 := tt.b2Setup()
			b1.And(b2)
			if b1.Count() != tt.wantCount {
				t.Errorf("And 后 Count() = %d, want %d", b1.Count(), tt.wantCount)
			}
			for idx, want := range tt.wantGet {
				if got := b1.Get(idx); got != want {
					t.Errorf("And 后 Get(%d) = %v, want %v", idx, got, want)
				}
			}
		})
	}
}

func TestBitmapAnd_EmptyBitmaps(t *testing.T) {
	tests := []struct {
		name      string
		b1Setup   func() *Bitmap
		b2Setup   func() *Bitmap
		wantCount uint32
		wantGet   map[uint32]bool
	}{
		{
			name: "非空 bitmap And 空 bitmap",
			b1Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(1)
				bm.Set(3)
				return bm
			},
			b2Setup: func() *Bitmap {
				return NewBitmap(10)
			},
			wantCount: 0,
			wantGet:   map[uint32]bool{1: false, 3: false},
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
			b1.And(b2)
			if b1.Count() != tt.wantCount {
				t.Errorf("And 后 Count() = %d, want %d", b1.Count(), tt.wantCount)
			}
			for idx, want := range tt.wantGet {
				if got := b1.Get(idx); got != want {
					t.Errorf("And 后 Get(%d) = %v, want %v", idx, got, want)
				}
			}
		})
	}
}

func TestBitmapAnd_DifferentSizes(t *testing.T) {
	tests := []struct {
		name      string
		b1Setup   func() *Bitmap
		b2Setup   func() *Bitmap
		wantCount uint32
		wantGet   map[uint32]bool
	}{
		{
			name: "不同长度 - b1 更长",
			b1Setup: func() *Bitmap {
				bm := NewBitmap(128)
				bm.Set(1)
				bm.Set(70)
				return bm
			},
			b2Setup: func() *Bitmap {
				bm := NewBitmap(10)
				bm.Set(1)
				return bm
			},
			wantCount: 1,
			wantGet:   map[uint32]bool{1: true, 70: false},
		},
		{
			name: "不同长度 - b2 更长",
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
			wantGet:   map[uint32]bool{1: true},
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
			b1.And(b2)
			if b1.Count() != tt.wantCount {
				t.Errorf("And 后 Count() = %d, want %d", b1.Count(), tt.wantCount)
			}
			for idx, want := range tt.wantGet {
				if got := b1.Get(idx); got != want {
					t.Errorf("And 后 Get(%d) = %v, want %v", idx, got, want)
				}
			}
		})
	}
}
