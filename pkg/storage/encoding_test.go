package storage

import (
	"math"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestEncodingTypeString(t *testing.T) {
	tests := []struct {
		enc  EncodingType
		want string
	}{
		{EncodingPlain, "Plain"},
		{EncodingDict, "Dict"},
		{EncodingRLE, "RLE"},
		{EncodingBitmap, "Bitmap"},
		{EncodingType(99), "Unknown(99)"},
	}
	for _, tt := range tests {
		got := tt.enc.String()
		if got != tt.want {
			t.Errorf("EncodingType(%d).String() = %q, want %q", tt.enc, got, tt.want)
		}
	}
}

func TestEncodeDecodePlainInt64(t *testing.T) {
	data := []int64{0, 1, -1, math.MaxInt64, math.MinInt64, 42, -100}
	enc, err := EncodeColumn(common.TypeInt64, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}
	if enc.Encoding != EncodingPlain {
		t.Errorf("encoding = %v, want Plain", enc.Encoding)
	}
	if enc.RowCount != uint32(len(data)) {
		t.Errorf("rowCount = %d, want %d", enc.RowCount, len(data))
	}

	decoded, nulls, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	if nulls != nil {
		t.Error("expected no nulls in plain int64 decode")
	}
	ints, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("expected []int64, got %T", decoded)
	}
	if len(ints) != len(data) {
		t.Fatalf("len = %d, want %d", len(ints), len(data))
	}
	for i, v := range data {
		if ints[i] != v {
			t.Errorf("row %d = %d, want %d", i, ints[i], v)
		}
	}
}

func TestEncodeDecodePlainFloat64(t *testing.T) {
	data := []float64{0.0, 1.5, -3.14, math.MaxFloat64, math.SmallestNonzeroFloat64}
	enc, err := EncodeColumn(common.TypeFloat64, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}
	if enc.Encoding != EncodingPlain {
		t.Errorf("encoding = %v, want Plain", enc.Encoding)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	floats, ok := decoded.([]float64)
	if !ok {
		t.Fatalf("expected []float64, got %T", decoded)
	}
	for i, v := range data {
		if floats[i] != v {
			t.Errorf("row %d = %f, want %f", i, floats[i], v)
		}
	}
}

func TestEncodeDecodePlainTimestamp(t *testing.T) {
	data := []int64{0, 1, 1620000000000000000, -1}
	enc, err := EncodeColumn(common.TypeTimestamp, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}
	if enc.Encoding != EncodingPlain {
		t.Errorf("encoding = %v, want Plain", enc.Encoding)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	times, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("expected []int64, got %T", decoded)
	}
	for i, v := range data {
		if times[i] != v {
			t.Errorf("row %d = %d, want %d", i, times[i], v)
		}
	}
}

const (
	testStrHello = "hello"
	testStrApple = "apple"
	testStrWorld = "world"
	testStrFoo   = "foo"
	testStrTest  = "test"
)

func TestEncodeDecodeEmpty(t *testing.T) {
	enc, err := EncodeColumn(common.TypeInt64, []int64{}, 0, nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}
	if enc.RowCount != 0 {
		t.Errorf("rowCount = %d, want 0", enc.RowCount)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	ints, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("expected []int64, got %T", decoded)
	}
	if len(ints) != 0 {
		t.Errorf("len = %d, want 0", len(ints))
	}
}

func TestSelectEncodingInt64RLE(t *testing.T) {
	data := []int64{1, 1, 1, 1, 1, 2, 2, 3, 3, 3}
	enc := selectEncoding(common.TypeInt64, data, uint32(len(data)))
	if enc != EncodingRLE {
		t.Errorf("encoding = %v, want RLE for repetitive data", enc)
	}
}

func TestSelectEncodingInt64Plain(t *testing.T) {
	data := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	enc := selectEncoding(common.TypeInt64, data, uint32(len(data)))
	if enc != EncodingPlain {
		t.Errorf("encoding = %v, want Plain for unique data", enc)
	}
}

func TestSelectEncodingTypeBool(t *testing.T) {
	enc := selectEncoding(common.TypeBool, nil, 10)
	if enc != EncodingBitmap {
		t.Errorf("encoding = %v, want Bitmap for bool type", enc)
	}
}

func TestSelectEncodingTypeString(t *testing.T) {
	enc := selectEncoding(common.TypeString, nil, 10)
	if enc != EncodingDict {
		t.Errorf("encoding = %v, want Dict for string type", enc)
	}
}

func TestIndexWidth(t *testing.T) {
	tests := []struct {
		size     uint32
		hasNulls bool
		want     int
	}{
		{0, false, 1},
		{1, false, 1},
		{256, false, 1},
		{256, true, 2},
		{255, true, 1},
		{257, false, 2},
		{65535, false, 2},
		{65536, false, 2},
		{65536, true, 4},
		{65537, false, 4},
	}
	for _, tt := range tests {
		got := indexWidth(tt.size, tt.hasNulls)
		if got != tt.want {
			t.Errorf("indexWidth(%d, %v) = %d, want %d", tt.size, tt.hasNulls, got, tt.want)
		}
	}
}

func TestNullMarkerForWidth(t *testing.T) {
	tests := []struct {
		width int
		want  uint32
	}{
		{1, 0xFF},
		{2, 0xFFFF},
		{4, 0xFFFFFFFF},
	}
	for _, tt := range tests {
		got := nullMarkerForWidth(tt.width)
		if got != tt.want {
			t.Errorf("nullMarkerForWidth(%d) = %d, want %d", tt.width, got, tt.want)
		}
	}
}

func TestEncodeDecodeRoundTripInt64(t *testing.T) {
	data := []int64{10, 20, 30, 40, 50}
	enc, err := EncodeColumn(common.TypeInt64, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn: %v", err)
	}
	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn: %v", err)
	}
	ints := decoded.([]int64)
	for i, v := range data {
		if ints[i] != v {
			t.Errorf("row %d = %d, want %d", i, ints[i], v)
		}
	}
}

func TestEncodeDecodeRoundTripFloat64(t *testing.T) {
	data := []float64{1.1, 2.2, 3.3}
	enc, err := EncodeColumn(common.TypeFloat64, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn: %v", err)
	}
	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn: %v", err)
	}
	floats := decoded.([]float64)
	for i, v := range data {
		if floats[i] != v {
			t.Errorf("row %d = %f, want %f", i, floats[i], v)
		}
	}
}

func TestEncodeDecodeRoundTripString(t *testing.T) {
	data := []string{testStrHello, testStrWorld, testStrHello, testStrTest}
	enc, err := EncodeColumn(common.TypeString, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn: %v", err)
	}
	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn: %v", err)
	}
	strs := decoded.([]string)
	for i, v := range data {
		if strs[i] != v {
			t.Errorf("row %d = %q, want %q", i, strs[i], v)
		}
	}
}

func TestEncodeDecodeRoundTripBool(t *testing.T) {
	data := []uint64{1, 0, 1, 1, 0}
	enc, err := EncodeColumn(common.TypeBool, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn: %v", err)
	}
	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn: %v", err)
	}
	bools := decoded.([]uint64)
	for i, v := range data {
		if bools[i] != v {
			t.Errorf("row %d = %d, want %d", i, bools[i], v)
		}
	}
}

func TestEncodeColumnUnsupportedType(t *testing.T) {
	_, err := EncodeColumn(common.TypeNull, nil, 1, nil)
	if err == nil {
		t.Error("expected error for unsupported type")
	}
}

func TestEncodeColumnInvalidData(t *testing.T) {
	_, err := EncodeColumn(common.TypeInt64, "not ints", 1, nil)
	if err == nil {
		t.Error("expected error for invalid data type")
	}
}
