package index

import (
	"testing"
)

// --- BuildAndRegister with empty keys ---

// TestBuildAndRegister_EmptyKeys_V2 tests BuildAndRegister with no keys.
func TestBuildAndRegister_EmptyKeys_V2(t *testing.T) {
	bi := NewBloomIndex()
	err := bi.BuildAndRegister(1, []string{}, 0.01)
	if err != nil {
		t.Errorf("expected nil error for empty keys, got %v", err)
	}
	if bi.Len() != 0 {
		t.Errorf("expected 0 bloom filters, got %d", bi.Len())
	}
}

// --- BuildAndRegister with valid keys ---

// TestBuildAndRegister_ValidKeys_V2 tests BuildAndRegister with valid keys.
func TestBuildAndRegister_ValidKeys_V2(t *testing.T) {
	bi := NewBloomIndex()
	keys := []string{"key1", "key2", "key3"}
	err := bi.BuildAndRegister(1, keys, 0.01)
	if err != nil {
		t.Fatalf("BuildAndRegister: %v", err)
	}
	if bi.Len() != 1 {
		t.Errorf("expected 1 bloom filter, got %d", bi.Len())
	}

	// Verify the filter works
	if !bi.MayContainString(1, "key1") {
		t.Error("expected key1 to be found in bloom filter")
	}
}

// --- Lookup with overlapping L0 segments ---

// TestLookup_OverlappingL0Segments_V2 tests Lookup with multiple L0 segments
// that have overlapping key ranges.
func TestLookup_OverlappingL0Segments_V2(t *testing.T) {
	pi := NewPrimaryIndex()

	// Register two L0 segments with overlapping ranges
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "z", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "m", MaxKey: "z", Level: 0})

	// Lookup a key in the overlap region
	result := pi.Lookup("p")
	if len(result) != 2 {
		t.Errorf("expected 2 segment IDs for overlapping key, got %d: %v", len(result), result)
	}

	// Lookup a key only in segment 1
	result = pi.Lookup("b")
	if len(result) != 1 {
		t.Errorf("expected 1 segment ID for key 'b', got %d: %v", len(result), result)
	}
	if result[0] != 1 {
		t.Errorf("expected segment ID 1, got %d", result[0])
	}
}

// TestLookup_MultipleOverlappingSegments_V2 tests Lookup with many overlapping L0 segments.
func TestLookup_MultipleOverlappingSegments_V2(t *testing.T) {
	pi := NewPrimaryIndex()

	// Register three L0 segments all containing key "c"
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "e", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "b", MaxKey: "d", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 3, MinKey: "c", MaxKey: "f", Level: 0})

	result := pi.Lookup("c")
	if len(result) != 3 {
		t.Errorf("expected 3 segment IDs for key 'c', got %d: %v", len(result), result)
	}
}

// --- Lookup with key not in any segment ---

// TestLookup_KeyNotInAnySegment_V2 tests Lookup with a key that doesn't fall
// in any segment's range.
func TestLookup_KeyNotInAnySegment_V2(t *testing.T) {
	pi := NewPrimaryIndex()

	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "e", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "g", MaxKey: "m", Level: 0})

	// Key "f" is between the two segments
	result := pi.Lookup("f")
	if len(result) != 0 {
		t.Errorf("expected 0 segment IDs for key 'f', got %d: %v", len(result), result)
	}
}

// TestLookup_NoSegments_V2 tests Lookup when no segments are registered.
func TestLookup_NoSegments_V2(t *testing.T) {
	pi := NewPrimaryIndex()
	result := pi.Lookup("any")
	if result != nil {
		t.Errorf("expected nil for no segments, got %v", result)
	}
}

// TestLookup_ExactBoundary_V2 tests Lookup with keys at segment boundaries.
func TestLookup_ExactBoundary_V2(t *testing.T) {
	pi := NewPrimaryIndex()

	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "e", Level: 0})

	// Test min key boundary
	result := pi.Lookup("a")
	if len(result) != 1 {
		t.Errorf("expected 1 segment for min key 'a', got %d", len(result))
	}

	// Test max key boundary
	result = pi.Lookup("e")
	if len(result) != 1 {
		t.Errorf("expected 1 segment for max key 'e', got %d", len(result))
	}
}

// --- keyInRange with empty min/max keys ---

// TestKeyInRange_EmptyMinMax_V2 tests keyInRange with empty min/max keys.
func TestKeyInRange_EmptyMinMax_V2(t *testing.T) {
	result := keyInRange("a", "", "")
	if result {
		t.Error("expected false for empty min/max keys")
	}
}

// --- Range with overlapping L0 segments ---

// TestRange_OverlappingL0Segments_V2 tests Range with overlapping L0 segments.
func TestRange_OverlappingL0Segments_V2(t *testing.T) {
	pi := NewPrimaryIndex()

	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "e", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "c", MaxKey: "g", Level: 0})

	result := pi.Range("b", "d")
	if len(result) != 2 {
		t.Errorf("expected 2 segment IDs for range [b,d], got %d: %v", len(result), result)
	}
}

// TestRange_NoOverlap_V2 tests Range with no overlapping segments.
func TestRange_NoOverlap_V2(t *testing.T) {
	pi := NewPrimaryIndex()

	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "c", Level: 0})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "x", MaxKey: "z", Level: 0})

	result := pi.Range("d", "w")
	if len(result) != 0 {
		t.Errorf("expected 0 segment IDs for non-overlapping range, got %d: %v", len(result), result)
	}
}

// --- Register with invalid segment ---

// TestRegisterSegment_InvalidID_V2 tests RegisterSegment with ID 0.
func TestRegisterSegment_InvalidID_V2(t *testing.T) {
	pi := NewPrimaryIndex()
	err := pi.RegisterSegment(SegmentMeta{ID: 0, MinKey: "a", MaxKey: "z"})
	if err == nil {
		t.Error("expected error for segment ID 0, got nil")
	}
}

// TestRegisterSegment_InvalidRange_V2 tests RegisterSegment with min > max.
func TestRegisterSegment_InvalidRange_V2(t *testing.T) {
	pi := NewPrimaryIndex()
	err := pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "z", MaxKey: "a"})
	if err == nil {
		t.Error("expected error for min > max, got nil")
	}
}

// --- BloomIndex MayContain for unregistered segment ---

// TestBloomIndex_MayContain_UnregisteredSegment_V2 tests MayContain for a segment
// that is not registered (should return true - might contain).
func TestBloomIndex_MayContain_UnregisteredSegment_V2(t *testing.T) {
	bi := NewBloomIndex()
	result := bi.MayContain(999, []byte("key"))
	if !result {
		t.Error("expected true for unregistered segment (might contain)")
	}
}

// TestBloomIndex_MayContainString_UnregisteredSegment_V2 tests MayContainString for
// a segment that is not registered.
func TestBloomIndex_MayContainString_UnregisteredSegment_V2(t *testing.T) {
	bi := NewBloomIndex()
	result := bi.MayContainString(999, "key")
	if !result {
		t.Error("expected true for unregistered segment (might contain)")
	}
}

// --- BloomIndex Stats ---

// TestBloomIndex_Stats_V2 tests the Stats method.
func TestBloomIndex_Stats_V2(t *testing.T) {
	bi := NewBloomIndex()
	_ = bi.BuildAndRegister(1, []string{"key1", "key2"}, 0.01)

	hit, miss := bi.Stats()
	if hit != 0 || miss != 0 {
		t.Errorf("expected (0, 0), got (%d, %d)", hit, miss)
	}

	// Query for existing key
	_ = bi.MayContainString(1, "key1")
	_, _ = bi.Stats()

	// Query for non-existing key (should miss)
	_ = bi.MayContainString(1, "nonexistent_key_xyz")
	_, _ = bi.Stats()
}

// --- PrimaryIndex GetSegment ---

// TestPrimaryIndex_GetSegment_V2 tests GetSegment.
func TestPrimaryIndex_GetSegment_V2(t *testing.T) {
	pi := NewPrimaryIndex()
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "z"})

	meta, ok := pi.GetSegment(1)
	if !ok {
		t.Error("expected to find segment 1")
	}
	if meta.ID != 1 {
		t.Errorf("expected ID 1, got %d", meta.ID)
	}

	_, ok = pi.GetSegment(999)
	if ok {
		t.Error("expected not to find segment 999")
	}
}

// --- PrimaryIndex UnregisterSegment ---

// TestPrimaryIndex_UnregisterSegment_V2 tests UnregisterSegment.
func TestPrimaryIndex_UnregisterSegment_V2(t *testing.T) {
	pi := NewPrimaryIndex()
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "z"})
	if pi.SegmentCount() != 1 {
		t.Errorf("expected 1, got %d", pi.SegmentCount())
	}

	err := pi.UnregisterSegment(1)
	if err != nil {
		t.Errorf("UnregisterSegment: %v", err)
	}
	if pi.SegmentCount() != 0 {
		t.Errorf("expected 0 after unregister, got %d", pi.SegmentCount())
	}

	// Unregister non-existent segment
	err = pi.UnregisterSegment(999)
	if err == nil {
		t.Error("expected error for non-existent segment, got nil")
	}
}

// --- PrimaryIndex Clear ---

// TestPrimaryIndex_Clear_V2 tests Clear.
func TestPrimaryIndex_Clear_V2(t *testing.T) {
	pi := NewPrimaryIndex()
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "z"})
	_ = pi.RegisterSegment(SegmentMeta{ID: 2, MinKey: "m", MaxKey: "z"})

	pi.Clear()
	if pi.SegmentCount() != 0 {
		t.Errorf("expected 0 after clear, got %d", pi.SegmentCount())
	}
}

// --- rangeOverlap with empty min/max ---

// TestRangeOverlap_EmptyMinMax_V2 tests rangeOverlap with empty min/max keys.
func TestRangeOverlap_EmptyMinMax_V2(t *testing.T) {
	result := rangeOverlap("a", "z", "", "")
	if result {
		t.Error("expected false for empty min/max keys in rangeOverlap")
	}
}
