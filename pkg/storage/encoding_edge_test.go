package storage

import (
	"math"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestEncodeDecodeRLEInt64(t *testing.T) {
	data := []int64{1, 1, 1, 1, 1, 2, 2, 3, 3, 3}
	enc, err := EncodeColumn(common.TypeInt64, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}
	if enc.Encoding != EncodingRLE {
		t.Errorf("encoding = %v, want RLE", enc.Encoding)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	ints, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("expected []int64, got %T", decoded)
	}
	for i, v := range data {
		if ints[i] != v {
			t.Errorf("row %d = %d, want %d", i, ints[i], v)
		}
	}
}

func TestEncodeDecodeRLEInt64WithNulls(t *testing.T) {
	data := []int64{1, 1, 0, 2, 2, 0, 3, 3, 3, 3}
	nulls := common.NewBitmap(10)
	nulls.Set(2)
	nulls.Set(5)

	enc, err := EncodeColumn(common.TypeInt64, data, uint32(len(data)), nulls)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}
	if enc.Encoding != EncodingRLE {
		t.Errorf("encoding = %v, want RLE", enc.Encoding)
	}

	decoded, decodedNulls, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	ints, ok := decoded.([]int64)
	if !ok {
		t.Fatalf("expected []int64, got %T", decoded)
	}

	for i := uint32(0); i < 10; i++ {
		if nulls.Get(i) != decodedNulls.Get(i) {
			t.Errorf("row %d null mismatch: expected %v, got %v", i, nulls.Get(i), decodedNulls.Get(i))
		}
		if !nulls.Get(i) && ints[i] != data[i] {
			t.Errorf("row %d = %d, want %d", i, ints[i], data[i])
		}
	}
}

func TestEncodeRLEInvalidType(t *testing.T) {
	_, err := encodeRLE(common.TypeFloat64, []float64{1.0}, 1, nil)
	if err == nil {
		t.Error("expected error for non-int64 RLE")
	}
}

func TestEncodeDecodeBitmap(t *testing.T) {
	data := []uint64{1, 0, 1, 1, 0, 0, 1, 0}
	enc, err := EncodeColumn(common.TypeBool, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}
	if enc.Encoding != EncodingBitmap {
		t.Errorf("encoding = %v, want Bitmap", enc.Encoding)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	bools, ok := decoded.([]uint64)
	if !ok {
		t.Fatalf("expected []uint64, got %T", decoded)
	}
	for i, v := range data {
		if bools[i] != v {
			t.Errorf("row %d = %d, want %d", i, bools[i], v)
		}
	}
}

func TestEncodeBitmapWithNulls(t *testing.T) {
	data := []uint64{1, 0, 1}
	nulls := common.NewBitmap(3)
	nulls.Set(1)

	enc, err := encodeBitmap(data, 3, nulls)
	if err != nil {
		t.Fatalf("encodeBitmap: %v", err)
	}
	if len(enc.Nulls) == 0 {
		t.Error("expected nulls in encoded column")
	}
}

func TestEncodeBitmapInvalidData(t *testing.T) {
	_, err := encodeBitmap("not bools", 1, nil)
	if err == nil {
		t.Error("expected error for invalid bitmap data")
	}
}

func TestDecodeBitmapWithNulls(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingBitmap,
		Type:     common.TypeBool,
		RowCount: 3,
		Data:     common.NewBitmap(3).ToBytes(),
		Nulls:    common.NewBitmap(3).ToBytes(),
	}
	_, nulls, err := decodeBitmap(enc)
	if err != nil {
		t.Fatalf("decodeBitmap: %v", err)
	}
	if nulls == nil {
		t.Error("expected non-nil nulls")
	}
}

func TestNullBitmapRoundTrip(t *testing.T) {
	t.Run("Int64", func(t *testing.T) {
		data := []int64{1, 0, 2, 0, 3}
		nulls := common.NewBitmap(5)
		nulls.Set(1)
		nulls.Set(3)

		enc, err := EncodeColumn(common.TypeInt64, data, 5, nulls)
		if err != nil {
			t.Fatalf("EncodeColumn: %v", err)
		}
		decoded, decodedNulls, err := DecodeColumn(enc)
		if err != nil {
			t.Fatalf("DecodeColumn: %v", err)
		}
		if decodedNulls == nil {
			t.Fatal("expected non-nil nulls")
		}
		for i := uint32(0); i < 5; i++ {
			if nulls.Get(i) != decodedNulls.Get(i) {
				t.Errorf("row %d null mismatch: %v vs %v", i, nulls.Get(i), decodedNulls.Get(i))
			}
		}
		_ = decoded
	})

	t.Run("Float64", func(t *testing.T) {
		data := []float64{1.0, 0.0, 2.0}
		nulls := common.NewBitmap(3)
		nulls.Set(1)

		enc, err := EncodeColumn(common.TypeFloat64, data, 3, nulls)
		if err != nil {
			t.Fatalf("EncodeColumn: %v", err)
		}
		_, decodedNulls, err := DecodeColumn(enc)
		if err != nil {
			t.Fatalf("DecodeColumn: %v", err)
		}
		if decodedNulls == nil {
			t.Fatal("expected non-nil nulls")
		}
		if !decodedNulls.Get(1) {
			t.Error("row 1 should be null")
		}
	})
}

func TestEncodePlainStrings(t *testing.T) {
	data := []string{testStrHello, testStrWorld, "", testStrFoo}
	enc, err := encodePlainStrings(data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("encodePlainStrings: %v", err)
	}
	if enc.Encoding != EncodingPlain {
		t.Errorf("encoding = %v, want Plain", enc.Encoding)
	}
	if len(enc.Offsets) != 5 {
		t.Errorf("offsets len = %d, want 5", len(enc.Offsets))
	}
}

func TestEncodePlainStringsWithNulls(t *testing.T) {
	data := []string{"a", "b", "c"}
	nulls := common.NewBitmap(3)
	nulls.Set(1)

	enc, err := encodePlainStrings(data, 3, nulls)
	if err != nil {
		t.Fatalf("encodePlainStrings: %v", err)
	}
	if len(enc.Nulls) == 0 {
		t.Error("expected nulls in encoded column")
	}
}

func TestEncodePlainInvalidTimestamp(t *testing.T) {
	_, err := encodePlain(common.TypeTimestamp, "not ints", 1, nil)
	if err == nil {
		t.Error("expected error for invalid timestamp data")
	}
}

func TestDecodePlainString(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeString,
		RowCount: 2,
		Data:     []byte("ab"),
		Offsets:  []uint32{0, 1, 2},
	}
	decoded, _, err := decodePlain(enc)
	if err != nil {
		t.Fatalf("decodePlain: %v", err)
	}
	strs := decoded.([]string)
	if strs[0] != "a" || strs[1] != "b" {
		t.Errorf("got %q, %q", strs[0], strs[1])
	}
}

func TestDecodePlainUnsupportedType(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeNull,
		RowCount: 1,
		Data:     []byte{0},
	}
	_, _, err := decodePlain(enc)
	if err == nil {
		t.Error("expected error for unsupported type in plain decode")
	}
}

func TestEncodingTypeUnknown(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: 99,
		Type:     common.TypeInt64,
		RowCount: 1,
		Data:     make([]byte, 8),
	}
	_, _, err := DecodeColumn(enc)
	if err == nil {
		t.Error("expected error for unknown encoding")
	}
}

func TestEncodeDecodeRoundTripTimestamp(t *testing.T) {
	data := []int64{100, 200, 300}
	enc, err := EncodeColumn(common.TypeTimestamp, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn: %v", err)
	}
	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn: %v", err)
	}
	times := decoded.([]int64)
	for i, v := range data {
		if times[i] != v {
			t.Errorf("row %d = %d, want %d", i, times[i], v)
		}
	}
}

func TestReadWriteIndex(t *testing.T) {
	tests := []struct {
		width int
		idx   uint32
	}{
		{1, 0},
		{1, 255},
		{2, 0},
		{2, 65535},
		{4, 0},
		{4, math.MaxUint32},
	}

	for _, tt := range tests {
		buf := make([]byte, tt.width)
		writeIndex(buf, 0, tt.width, tt.idx)
		got := readIndex(buf, 0, tt.width)
		if got != tt.idx {
			t.Errorf("width=%d: readWriteIndex = %d, want %d", tt.width, got, tt.idx)
		}
	}
}
