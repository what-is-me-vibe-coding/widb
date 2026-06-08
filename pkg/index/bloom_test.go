package index

import (
	"fmt"
	"sync"
	"testing"

	"github.com/bits-and-blooms/bloom/v3"
)

const testBloomKey1 = "key1"
const testBloomKey2 = "key2"
const testBloomKey3 = "key3"
const testAlpha = "alpha"
const testBeta = "beta"
const testGamma = "gamma"

func TestBloomIndexRegisterAndMayContain(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{testBloomKey1, testBloomKey2, testBloomKey3, "key4", "key5"}
	data, err := BuildFromKeys(keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildFromKeys: %v", err)
	}

	err = bi.RegisterFromBytes(1, data)
	if err != nil {
		t.Fatalf("RegisterFromBytes: %v", err)
	}

	if bi.Len() != 1 {
		t.Errorf("Len: got %d, want 1", bi.Len())
	}

	for _, k := range keys {
		if !bi.MayContain(1, []byte(k)) {
			t.Errorf("MayContain(%q): expected true", k)
		}
	}

	if bi.MayContain(1, []byte("nonexistent")) {
		t.Log("MayContain: false positive for nonexistent key (expected with 1%% FP rate)")
	}

	hit, miss := bi.Stats()
	t.Logf("Stats: hit=%d, miss=%d", hit, miss)
}

func TestBloomIndexNoFilter(t *testing.T) {
	bi := NewBloomIndex()

	if !bi.MayContain(99, []byte("any")) {
		t.Error("MayContain should return true when no filter registered")
	}
}

func TestBloomIndexUnregister(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{"a", "b", "c"}
	data, err := BuildFromKeys(keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildFromKeys: %v", err)
	}

	err = bi.RegisterFromBytes(1, data)
	if err != nil {
		t.Fatalf("RegisterFromBytes: %v", err)
	}

	if bi.Len() != 1 {
		t.Errorf("Len: got %d, want 1", bi.Len())
	}

	bi.Unregister(1)

	if bi.Len() != 0 {
		t.Errorf("Len after unregister: got %d, want 0", bi.Len())
	}

	if !bi.MayContain(1, []byte("a")) {
		t.Error("MayContain should return true after unregister (no filter)")
	}
}

func TestBloomIndexClear(t *testing.T) {
	bi := NewBloomIndex()

	for i := 0; i < 5; i++ {
		keys := []string{fmt.Sprintf("k%d", i)}
		data, err := BuildFromKeys(keys, DefaultBloomFPRate)
		if err != nil {
			t.Fatalf("BuildFromKeys: %v", err)
		}
		err = bi.RegisterFromBytes(uint64(i+1), data)
		if err != nil {
			t.Fatalf("RegisterFromBytes: %v", err)
		}
	}

	if bi.Len() != 5 {
		t.Errorf("Len: got %d, want 5", bi.Len())
	}

	bi.Clear()

	if bi.Len() != 0 {
		t.Errorf("Len after clear: got %d, want 0", bi.Len())
	}

	hit, miss := bi.Stats()
	if hit != 0 || miss != 0 {
		t.Errorf("Stats after clear: hit=%d, miss=%d, want 0,0", hit, miss)
	}
}

func TestBloomIndexEmptyKeys(t *testing.T) {
	data, err := BuildFromKeys([]string{}, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildFromKeys: %v", err)
	}
	if data != nil {
		t.Error("BuildFromKeys with empty keys should return nil")
	}
}

func TestBloomIndexNilRegister(t *testing.T) {
	bi := NewBloomIndex()
	err := bi.Register(1, nil)
	if err == nil {
		t.Error("Register with nil filter should return error")
	}
}

func TestBloomIndexRegisterFromEmptyBytes(t *testing.T) {
	bi := NewBloomIndex()
	err := bi.RegisterFromBytes(1, nil)
	if err != nil {
		t.Fatalf("RegisterFromBytes with nil: %v", err)
	}
	if bi.Len() != 0 {
		t.Errorf("Len: got %d, want 0", bi.Len())
	}
}

func TestBloomIndexBuildAndRegister(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{"k1", "k2", "k3"}
	err := bi.BuildAndRegister(1, keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister: %v", err)
	}

	if bi.Len() != 1 {
		t.Errorf("Len: got %d, want 1", bi.Len())
	}

	for _, k := range keys {
		if !bi.MayContain(1, []byte(k)) {
			t.Errorf("MayContain(%q): expected true", k)
		}
	}
}

func TestBuildFromKeysDefaultFPRate(t *testing.T) {
	keys := []string{"x", "y", "z"}
	data, err := BuildFromKeys(keys, 0)
	if err != nil {
		t.Fatalf("BuildFromKeys: %v", err)
	}
	if data == nil {
		t.Fatal("BuildFromKeys should return non-nil data")
	}
}

func TestBloomIndexConcurrentAccess(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{"c1", "c2", "c3", "c4", "c5"}
	data, err := BuildFromKeys(keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildFromKeys: %v", err)
	}
	err = bi.RegisterFromBytes(1, data)
	if err != nil {
		t.Fatalf("RegisterFromBytes: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := fmt.Sprintf("c%d", idx%5+1)
			bi.MayContain(1, []byte(key))
		}(i)
	}
	wg.Wait()

	hit, miss := bi.Stats()
	t.Logf("Concurrent access stats: hit=%d, miss=%d", hit, miss)
}

func TestBloomIndexFalsePositiveRate(t *testing.T) {
	n := 10000
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = fmt.Sprintf("key-%d", i)
	}

	data, err := BuildFromKeys(keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildFromKeys: %v", err)
	}

	bi := NewBloomIndex()
	err = bi.RegisterFromBytes(1, data)
	if err != nil {
		t.Fatalf("RegisterFromBytes: %v", err)
	}

	falsePositives := 0
	nonExistentKeys := 10000
	for i := 0; i < nonExistentKeys; i++ {
		if bi.MayContain(1, []byte(fmt.Sprintf("nonexistent-%d", i))) {
			falsePositives++
		}
	}

	fpRate := float64(falsePositives) / float64(nonExistentKeys)
	t.Logf("False positive rate: %.4f (expected ~0.01), FP count: %d/%d", fpRate, falsePositives, nonExistentKeys)

	if fpRate > 0.05 {
		t.Errorf("False positive rate %.4f exceeds 5%% threshold", fpRate)
	}
}

func TestBloomIndexMultipleSegments(t *testing.T) {
	bi := NewBloomIndex()

	for segID := uint64(1); segID <= 3; segID++ {
		keys := []string{
			fmt.Sprintf("seg%d-a", segID),
			fmt.Sprintf("seg%d-b", segID),
			fmt.Sprintf("seg%d-c", segID),
		}
		err := bi.BuildAndRegister(segID, keys, DefaultBloomFPRate)
		if err != nil {
			t.Fatalf("BuildAndRegister seg %d: %v", segID, err)
		}
	}

	if bi.Len() != 3 {
		t.Errorf("Len: got %d, want 3", bi.Len())
	}

	if !bi.MayContain(2, []byte("seg2-b")) {
		t.Error("seg2 should contain seg2-b")
	}

	if bi.MayContain(2, []byte("seg1-a")) {
		t.Log("seg2 false positive for seg1-a")
	}
}

// TestBloomIndexRegisterNormal 测试 Register 方法的正常注册路径。
// 创建真实的 BloomFilter 对象并注册，验证 MayContain 可以正常工作。
func TestBloomIndexRegisterNormal(t *testing.T) {
	bi := NewBloomIndex()

	// 创建一个真实的布隆过滤器并添加一些 key
	filter := bloom.NewWithEstimates(100, DefaultBloomFPRate)
	keys := []string{"apple", "banana", "cherry"}
	for _, k := range keys {
		filter.Add([]byte(k))
	}

	// 正常注册
	err := bi.Register(1, filter)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if bi.Len() != 1 {
		t.Errorf("Len: got %d, want 1", bi.Len())
	}

	// 验证已注册的 key 可以通过 MayContain 找到
	for _, k := range keys {
		if !bi.MayContain(1, []byte(k)) {
			t.Errorf("MayContain(%q): expected true after Register", k)
		}
	}
}

// TestBloomIndexRegisterOverwrite 测试 Register 覆盖已存在的过滤器。
func TestBloomIndexRegisterOverwrite(t *testing.T) {
	bi := NewBloomIndex()

	// 注册第一个过滤器
	filter1 := bloom.NewWithEstimates(10, DefaultBloomFPRate)
	filter1.Add([]byte("old-key"))
	err := bi.Register(1, filter1)
	if err != nil {
		t.Fatalf("Register first: %v", err)
	}

	if !bi.MayContain(1, []byte("old-key")) {
		t.Error("old-key should be found in first filter")
	}

	// 用新的过滤器覆盖
	filter2 := bloom.NewWithEstimates(10, DefaultBloomFPRate)
	filter2.Add([]byte("new-key"))
	err = bi.Register(1, filter2)
	if err != nil {
		t.Fatalf("Register overwrite: %v", err)
	}

	if bi.Len() != 1 {
		t.Errorf("Len: got %d, want 1 after overwrite", bi.Len())
	}

	// 新 key 应该能找到
	if !bi.MayContain(1, []byte("new-key")) {
		t.Error("new-key should be found after overwrite")
	}
}

// TestBloomIndexRegisterMultipleSegments 测试 Register 注册多个 Segment。
func TestBloomIndexRegisterMultipleSegments(t *testing.T) {
	bi := NewBloomIndex()

	for segID := uint64(1); segID <= 5; segID++ {
		filter := bloom.NewWithEstimates(10, DefaultBloomFPRate)
		filter.Add([]byte(fmt.Sprintf("seg%d-key", segID)))
		err := bi.Register(segID, filter)
		if err != nil {
			t.Fatalf("Register seg %d: %v", segID, err)
		}
	}

	if bi.Len() != 5 {
		t.Errorf("Len: got %d, want 5", bi.Len())
	}

	// 验证每个 Segment 的 key 都能找到
	for segID := uint64(1); segID <= 5; segID++ {
		key := fmt.Sprintf("seg%d-key", segID)
		if !bi.MayContain(segID, []byte(key)) {
			t.Errorf("MayContain seg %d key %q: expected true", segID, key)
		}
	}
}

// TestBuildAndRegisterEmptyKeys 测试 BuildAndRegister 空 keys 时返回 nil 不注册的场景。
func TestBuildAndRegisterEmptyKeys(t *testing.T) {
	bi := NewBloomIndex()

	// 空 keys 不应注册任何过滤器
	err := bi.BuildAndRegister(1, []string{}, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister with empty keys: %v", err)
	}

	if bi.Len() != 0 {
		t.Errorf("Len: got %d, want 0 after empty keys", bi.Len())
	}

	// nil keys 也不应注册
	err = bi.BuildAndRegister(2, nil, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister with nil keys: %v", err)
	}

	if bi.Len() != 0 {
		t.Errorf("Len: got %d, want 0 after nil keys", bi.Len())
	}

	// 验证 MayContain 对未注册 Segment 返回 true（无过滤器时默认不跳过）
	if !bi.MayContain(1, []byte("any")) {
		t.Error("MayContain should return true for unregistered segment")
	}
	if !bi.MayContain(2, []byte("any")) {
		t.Error("MayContain should return true for unregistered segment with nil keys")
	}
}

// TestBuildAndRegisterWithKeys 测试 BuildAndRegister 正常路径后能查到 key。
func TestBuildAndRegisterWithKeys(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{"x1", "x2", "x3"}
	err := bi.BuildAndRegister(10, keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister: %v", err)
	}

	if bi.Len() != 1 {
		t.Errorf("Len: got %d, want 1", bi.Len())
	}

	for _, k := range keys {
		if !bi.MayContain(10, []byte(k)) {
			t.Errorf("MayContain(%q): expected true", k)
		}
	}
}

func TestBloomBuildAndRegister(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{testBloomKey1, testBloomKey2, testBloomKey3}
	if err := bi.BuildAndRegister(1, keys, DefaultBloomFPRate); err != nil {
		t.Fatalf("BuildAndRegister: %v", err)
	}

	for _, k := range keys {
		if !bi.MayContainString(1, k) {
			t.Errorf("MayContainString(%q): expected true after BuildAndRegister", k)
		}
	}
}

func TestBloomBuildAndRegisterEmpty(t *testing.T) {
	bi := NewBloomIndex()

	if err := bi.BuildAndRegister(1, nil, DefaultBloomFPRate); err != nil {
		t.Fatalf("BuildAndRegister with nil keys: %v", err)
	}

	if err := bi.BuildAndRegister(2, []string{}, DefaultBloomFPRate); err != nil {
		t.Fatalf("BuildAndRegister with empty keys: %v", err)
	}
}

func TestBloomBuildFromKeysInvalidFPRate(t *testing.T) {
	keys := []string{testBloomKey1, testBloomKey2}

	tests := []struct {
		name   string
		fpRate float64
	}{
		{"zero", 0},
		{"negative", -0.1},
		{"one", 1.0},
		{"above_one", 1.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := BuildFromKeys(keys, tt.fpRate)
			if err != nil {
				t.Fatalf("BuildFromKeys with fpRate=%v: %v", tt.fpRate, err)
			}
			if data == nil {
				t.Error("expected non-nil data for non-empty keys")
			}
		})
	}
}
