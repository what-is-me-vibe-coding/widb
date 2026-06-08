package index

import (
	"fmt"
	"testing"
)

// TestRegisterFromBytes_CorruptedData tests that RegisterFromBytes returns an error
// when given truncated bloom filter data that cannot be unmarshaled.
// Arbitrary corrupted bytes can cause the bloom library to panic, so we use
// a truncated valid marshaled filter to trigger the UnmarshalBinary error path.
func TestRegisterFromBytes_CorruptedData(t *testing.T) {
	bi := NewBloomIndex()

	// Build a valid filter and marshal it, then truncate the bytes to cause
	// an unmarshal error (io.UnexpectedEOF) rather than a panic.
	validData, err := BuildFromKeys([]string{"a", "b"}, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildFromKeys: %v", err)
	}
	if len(validData) < 4 {
		t.Fatalf("valid data too short: %d bytes", len(validData))
	}
	truncatedData := validData[:len(validData)/2]

	err = bi.RegisterFromBytes(1, truncatedData)
	if err == nil {
		t.Error("RegisterFromBytes should return error for truncated data")
	}

	if bi.Len() != 0 {
		t.Errorf("Len: got %d, want 0 after failed registration", bi.Len())
	}
}

// TestMayContainString_UnregisteredSegment tests that MayContainString returns true
// for segments that have no registered bloom filter (conservative default).
func TestMayContainString_UnregisteredSegment(t *testing.T) {
	bi := NewBloomIndex()

	result := bi.MayContainString(99, "any-key")
	if !result {
		t.Error("MayContainString should return true for unregistered segment")
	}
}

// TestMayContainString_RegisteredSegment tests that MayContainString correctly
// reports keys that were added to the bloom filter.
func TestMayContainString_RegisteredSegment(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{testAlpha, testBeta, testGamma}
	err := bi.BuildAndRegister(1, keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister: %v", err)
	}

	for _, k := range keys {
		if !bi.MayContainString(1, k) {
			t.Errorf("MayContainString(%q): expected true for registered key", k)
		}
	}
}

// TestMayContainString_UpdatesStats tests that MayContainString updates the
// hit and miss counters correctly, and that unregistered segments do not
// affect the statistics.
func TestMayContainString_UpdatesStats(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{"stat-key"}
	err := bi.BuildAndRegister(1, keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister: %v", err)
	}

	// Query a key that was added — should be a hit
	if !bi.MayContainString(1, "stat-key") {
		t.Error("MayContainString should return true for added key")
	}
	hit, miss := bi.Stats()
	if hit != 1 {
		t.Errorf("hit count after querying added key: got %d, want 1", hit)
	}
	if miss != 0 {
		t.Errorf("miss count after querying added key: got %d, want 0", miss)
	}

	// Query an unregistered segment — should return true without updating stats
	hitBefore, missBefore := bi.Stats()
	bi.MayContainString(999, "any")
	hitAfter, missAfter := bi.Stats()
	if hitAfter != hitBefore || missAfter != missBefore {
		t.Errorf("stats should not change for unregistered segment: hit %d->%d, miss %d->%d",
			hitBefore, hitAfter, missBefore, missAfter)
	}
}

// TestMayContainString_MissPath tests that MayContainString increments the miss
// counter when the filter determines a key is definitely not present.
func TestMayContainString_MissPath(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{"only-this-key"}
	err := bi.BuildAndRegister(1, keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister: %v", err)
	}

	// Query many different keys that were not added — at least some should be
	// true negatives (misses) given the low false-positive rate.
	for i := 0; i < 1000; i++ {
		bi.MayContainString(1, fmt.Sprintf("nonexistent-key-%d", i))
	}

	_, miss := bi.Stats()
	if miss == 0 {
		t.Error("miss counter should be > 0 after querying many non-existent keys")
	}
}
