package storage

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

func TestEncodeDecodePlainString(t *testing.T) {
	data := []string{testStrHello, testStrWorld, "", testStrTest, testStrFoo}
	enc, err := EncodeColumn(common.TypeString, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}
	if enc.Encoding != EncodingDict {
		t.Errorf("encoding = %v, want Dict", enc.Encoding)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	strs, ok := decoded.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", decoded)
	}
	for i, v := range data {
		if strs[i] != v {
			t.Errorf("row %d = %q, want %q", i, strs[i], v)
		}
	}
}

func TestEncodeDecodeDictString(t *testing.T) {
	data := []string{testStrApple, "banana", testStrApple, testStrApple, "banana", "cherry", testStrApple}
	enc, err := EncodeColumn(common.TypeString, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}
	if enc.Encoding != EncodingDict {
		t.Errorf("encoding = %v, want Dict", enc.Encoding)
	}
	if len(enc.Dict) != 3 {
		t.Errorf("dict size = %d, want 3", len(enc.Dict))
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	strs, ok := decoded.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", decoded)
	}
	for i, v := range data {
		if strs[i] != v {
			t.Errorf("row %d = %q, want %q", i, strs[i], v)
		}
	}
}

func TestEncodeDecodeDictStringWithNulls(t *testing.T) {
	data := []string{"a", "b", "a", "c", "b"}
	nulls := common.NewBitmap(5)
	nulls.Set(1)
	nulls.Set(3)

	enc, err := EncodeColumn(common.TypeString, data, uint32(len(data)), nulls)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}

	decoded, decodedNulls, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	strs, ok := decoded.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", decoded)
	}

	for i := uint32(0); i < 5; i++ {
		if nulls.Get(i) != decodedNulls.Get(i) {
			t.Errorf("row %d null mismatch: expected %v, got %v", i, nulls.Get(i), decodedNulls.Get(i))
		}
		if !nulls.Get(i) && strs[i] != data[i] {
			t.Errorf("row %d = %q, want %q", i, strs[i], data[i])
		}
	}
}

func TestEncodeDecodeDictLargeIndex(t *testing.T) {
	const n = 300
	data := make([]string, n)
	for i := 0; i < n; i++ {
		data[i] = "value_" + string(rune('a'+i%26))
	}
	enc, err := EncodeColumn(common.TypeString, data, uint32(len(data)), nil)
	if err != nil {
		t.Fatalf("EncodeColumn failed: %v", err)
	}

	decoded, _, err := DecodeColumn(enc)
	if err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	strs, ok := decoded.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", decoded)
	}
	for i := 0; i < n; i++ {
		if strs[i] != data[i] {
			t.Errorf("row %d = %q, want %q", i, strs[i], data[i])
		}
	}
}

func TestEncodeDictInvalidType(t *testing.T) {
	_, err := encodeDict(common.TypeInt64, []int64{1}, 1, nil)
	if err == nil {
		t.Error("expected error for non-string dict")
	}
}

func TestDecodeColumnCorruptedData(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingDict,
		Type:     common.TypeString,
		RowCount: 1,
		Data:     []byte{0x02},
		Dict:     []string{"a", "b"},
	}
	_, _, err := DecodeColumn(enc)
	if err == nil {
		t.Error("expected error for corrupted dict index")
	}
}
