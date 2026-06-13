package index

import (
	"testing"
)

// v4 测试用键名常量，避免 goconst 重复字符串警告。
const (
	v4Key1 = "key1"
	v4Key2 = "key2"
	v4Key3 = "key3"
)

// TestBuildAndRegister_EmptyKeys tests BuildAndRegister with empty keys (should return nil, nil).
func TestBuildAndRegister_EmptyKeys(t *testing.T) {
	bi := NewBloomIndex()

	err := bi.BuildAndRegister(1, []string{}, DefaultBloomFPRate)
	if err != nil {
		t.Errorf("BuildAndRegister with empty keys should return nil, got: %v", err)
	}

	// No filter should be registered for empty keys
	if bi.Len() != 0 {
		t.Errorf("Len after empty BuildAndRegister: got %d, want 0", bi.Len())
	}
}

// TestBuildAndRegister_InvalidFPRateZero tests BuildAndRegister with zero fpRate.
func TestBuildAndRegister_InvalidFPRateZero(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{v4Key1, v4Key2, v4Key3}
	err := bi.BuildAndRegister(1, keys, 0)
	if err != nil {
		t.Errorf("BuildAndRegister with fpRate=0 should use default, got: %v", err)
	}

	// Filter should be registered
	if bi.Len() != 1 {
		t.Errorf("Len after BuildAndRegister with fpRate=0: got %d, want 1", bi.Len())
	}

	// Keys should be found
	for _, k := range keys {
		if !bi.MayContainString(1, k) {
			t.Errorf("MayContainString(%q): expected true", k)
		}
	}
}

// TestBuildAndRegister_InvalidFPRateNegative tests BuildAndRegister with negative fpRate.
func TestBuildAndRegister_InvalidFPRateNegative(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{v4Key1, v4Key2}
	err := bi.BuildAndRegister(1, keys, -0.5)
	if err != nil {
		t.Errorf("BuildAndRegister with negative fpRate should use default, got: %v", err)
	}

	if bi.Len() != 1 {
		t.Errorf("Len after BuildAndRegister with negative fpRate: got %d, want 1", bi.Len())
	}
}

// TestBuildAndRegister_InvalidFPRateOne tests BuildAndRegister with fpRate=1.0.
func TestBuildAndRegister_InvalidFPRateOne(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{v4Key1, v4Key2}
	err := bi.BuildAndRegister(1, keys, 1.0)
	if err != nil {
		t.Errorf("BuildAndRegister with fpRate=1.0 should use default, got: %v", err)
	}

	if bi.Len() != 1 {
		t.Errorf("Len after BuildAndRegister with fpRate=1.0: got %d, want 1", bi.Len())
	}
}

// TestBuildAndRegister_ValidFPRate tests BuildAndRegister with valid fpRate.
func TestBuildAndRegister_ValidFPRate(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{v4Key1, v4Key2, v4Key3}
	err := bi.BuildAndRegister(1, keys, 0.05)
	if err != nil {
		t.Fatalf("BuildAndRegister failed: %v", err)
	}

	if bi.Len() != 1 {
		t.Errorf("Len: got %d, want 1", bi.Len())
	}

	// All keys should be found
	for _, k := range keys {
		if !bi.MayContainString(1, k) {
			t.Errorf("MayContainString(%q): expected true", k)
		}
	}
}

// TestBuildAndRegister_MultipleSegments tests BuildAndRegister for multiple segments.
func TestBuildAndRegister_MultipleSegments(t *testing.T) {
	bi := NewBloomIndex()

	keys1 := []string{"a", "b", "c"}
	keys2 := []string{"d", "e", "f"}

	if err := bi.BuildAndRegister(1, keys1, DefaultBloomFPRate); err != nil {
		t.Fatalf("BuildAndRegister seg1: %v", err)
	}
	if err := bi.BuildAndRegister(2, keys2, DefaultBloomFPRate); err != nil {
		t.Fatalf("BuildAndRegister seg2: %v", err)
	}

	if bi.Len() != 2 {
		t.Errorf("Len: got %d, want 2", bi.Len())
	}

	// Keys from seg1 should be found in seg1
	for _, k := range keys1 {
		if !bi.MayContainString(1, k) {
			t.Errorf("MayContainString(1, %q): expected true", k)
		}
	}

	// Keys from seg2 should be found in seg2
	for _, k := range keys2 {
		if !bi.MayContainString(2, k) {
			t.Errorf("MayContainString(2, %q): expected true", k)
		}
	}

	// Verify stats are accessible
	hit, miss := bi.Stats()
	_ = hit
	_ = miss
}

// TestBuildFromKeys_EmptyKeys tests BuildFromKeys with empty keys.
func TestBuildFromKeys_EmptyKeys(t *testing.T) {
	data, err := BuildFromKeys([]string{}, DefaultBloomFPRate)
	if err != nil {
		t.Errorf("BuildFromKeys with empty keys should return nil, got: %v", err)
	}
	if data != nil {
		t.Errorf("expected nil data for empty keys, got %d bytes", len(data))
	}
}

// TestBuildFromKeys_NilKeys tests BuildFromKeys with nil keys.
func TestBuildFromKeys_NilKeys(t *testing.T) {
	data, err := BuildFromKeys(nil, DefaultBloomFPRate)
	if err != nil {
		t.Errorf("BuildFromKeys with nil keys should return nil, got: %v", err)
	}
	if data != nil {
		t.Errorf("expected nil data for nil keys, got %d bytes", len(data))
	}
}

// TestBuildFromKeys_InvalidFPRate tests BuildFromKeys with invalid fpRate.
func TestBuildFromKeys_InvalidFPRate(t *testing.T) {
	keys := []string{v4Key1, v4Key2}

	// fpRate = 0 should use default
	data, err := BuildFromKeys(keys, 0)
	if err != nil {
		t.Errorf("BuildFromKeys with fpRate=0: %v", err)
	}
	if data == nil {
		t.Error("expected non-nil data for valid keys with fpRate=0")
	}

	// fpRate < 0 should use default
	data, err = BuildFromKeys(keys, -1)
	if err != nil {
		t.Errorf("BuildFromKeys with fpRate<0: %v", err)
	}
	if data == nil {
		t.Error("expected non-nil data for valid keys with fpRate<0")
	}

	// fpRate >= 1 should use default
	data, err = BuildFromKeys(keys, 1.5)
	if err != nil {
		t.Errorf("BuildFromKeys with fpRate>=1: %v", err)
	}
	if data == nil {
		t.Error("expected non-nil data for valid keys with fpRate>=1")
	}
}

// TestBloomIndex_Register_NilFilter tests Register with nil filter.
func TestBloomIndex_Register_NilFilter(t *testing.T) {
	bi := NewBloomIndex()

	err := bi.Register(1, nil)
	if err == nil {
		t.Error("expected error for nil filter, got nil")
	}
}

// TestBloomIndex_Clear tests Clear resets the index.
func TestBloomIndex_Clear(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{v4Key1, v4Key2}
	if err := bi.BuildAndRegister(1, keys, DefaultBloomFPRate); err != nil {
		t.Fatalf("BuildAndRegister: %v", err)
	}

	if bi.Len() != 1 {
		t.Errorf("Len before Clear: got %d, want 1", bi.Len())
	}

	bi.Clear()

	if bi.Len() != 0 {
		t.Errorf("Len after Clear: got %d, want 0", bi.Len())
	}

	// Stats should be reset
	hit, miss := bi.Stats()
	if hit != 0 || miss != 0 {
		t.Errorf("Stats after Clear: hit=%d miss=%d, want 0 0", hit, miss)
	}
}

// TestBloomIndex_Unregister tests Unregister removes a segment.
func TestBloomIndex_Unregister(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{v4Key1}
	if err := bi.BuildAndRegister(1, keys, DefaultBloomFPRate); err != nil {
		t.Fatalf("BuildAndRegister: %v", err)
	}

	if bi.Len() != 1 {
		t.Errorf("Len before Unregister: got %d, want 1", bi.Len())
	}

	bi.Unregister(1)

	if bi.Len() != 0 {
		t.Errorf("Len after Unregister: got %d, want 0", bi.Len())
	}

	// Unregistered segment should return true (conservative)
	if !bi.MayContainString(1, v4Key1) {
		t.Error("MayContainString for unregistered segment should return true")
	}
}

// TestBloomIndex_MayContain tests MayContain with byte keys.
func TestBloomIndex_MayContain(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{"alpha", "beta"}
	if err := bi.BuildAndRegister(1, keys, DefaultBloomFPRate); err != nil {
		t.Fatalf("BuildAndRegister: %v", err)
	}

	// Registered key should be found
	if !bi.MayContain(1, []byte("alpha")) {
		t.Error("MayContain for registered key should return true")
	}

	// Unregistered segment should return true (conservative)
	if !bi.MayContain(99, []byte("any")) {
		t.Error("MayContain for unregistered segment should return true")
	}
}
