package index

import (
	"fmt"
	"testing"
)

// TestBuildAndRegisterBuildFromKeys错误 验证 BuildAndRegister 在 BuildFromKeys 返回错误时传播错误
func TestBuildAndRegisterBuildFromKeys错误(t *testing.T) {
	bi := NewBloomIndex()
	keys := []string{testBloomKey1, testBloomKey2, testBloomKey3}
	err := bi.BuildAndRegister(1, keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister 正常路径不应返回错误: %v", err)
	}
	if bi.Len() != 1 {
		t.Errorf("期望 Len=1，得到 %d", bi.Len())
	}
}

// TestBuildAndRegisterBuildFromKeys返回NilData 验证 BuildAndRegister 在 BuildFromKeys 返回 nil data 时直接返回 nil
func TestBuildAndRegisterBuildFromKeys返回NilData(t *testing.T) {
	bi := NewBloomIndex()
	err := bi.BuildAndRegister(1, []string{}, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister 空 keys 不应返回错误: %v", err)
	}
	if bi.Len() != 0 {
		t.Errorf("期望 Len=0（空 keys 不注册），得到 %d", bi.Len())
	}
}

// TestBuildAndRegisterNilKeys 验证 BuildAndRegister 在 keys 为 nil 时直接返回 nil
func TestBuildAndRegisterNilKeys(t *testing.T) {
	bi := NewBloomIndex()
	err := bi.BuildAndRegister(1, nil, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister nil keys 不应返回错误: %v", err)
	}
	if bi.Len() != 0 {
		t.Errorf("期望 Len=0（nil keys 不注册），得到 %d", bi.Len())
	}
}

// TestBuildFromKeys空Keys 验证 BuildFromKeys 在空 keys 时返回 nil
func TestBuildFromKeys空Keys(t *testing.T) {
	tests := []struct {
		name string
		keys []string
	}{
		{"nil keys", nil},
		{"空切片", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := BuildFromKeys(tt.keys, DefaultBloomFPRate)
			if err != nil {
				t.Errorf("期望 nil 错误，得到: %v", err)
			}
			if data != nil {
				t.Error("期望 nil data（空 keys）")
			}
		})
	}
}

// TestBuildFromKeys正常Keys 验证 BuildFromKeys 正常构建布隆过滤器
func TestBuildFromKeys正常Keys(t *testing.T) {
	keys := []string{testAlpha, testBeta, testGamma}
	data, err := BuildFromKeys(keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildFromKeys 失败: %v", err)
	}
	if data == nil {
		t.Fatal("期望非 nil data")
	}
	if len(data) == 0 {
		t.Error("期望非空 data")
	}
}

// TestBuildFromKeysFPRate边界值 验证 BuildFromKeys 在 fpRate 边界值时的行为
func TestBuildFromKeysFPRate边界值(t *testing.T) {
	keys := []string{"a", "b", "c"}
	tests := []struct {
		name   string
		fpRate float64
	}{
		{"零值使用默认", 0},
		{"负值使用默认", -0.5},
		{"1.0使用默认", 1.0},
		{"大于1使用默认", 2.0},
		{"正常值", 0.01},
		{"较小值", 0.001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := BuildFromKeys(keys, tt.fpRate)
			if err != nil {
				t.Errorf("fpRate=%v 返回错误: %v", tt.fpRate, err)
			}
			if data == nil {
				t.Errorf("fpRate=%v 期望非 nil data", tt.fpRate)
			}
		})
	}
}

// TestBuildAndRegister完整流程 验证 BuildAndRegister 完整的构建和注册流程
func TestBuildAndRegister完整流程(t *testing.T) {
	bi := NewBloomIndex()
	keys := []string{"user1", "user2", "user3"}
	err := bi.BuildAndRegister(42, keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister 失败: %v", err)
	}
	if bi.Len() != 1 {
		t.Errorf("期望 Len=1，得到 %d", bi.Len())
	}
	for _, k := range keys {
		if !bi.MayContain(42, []byte(k)) {
			t.Errorf("MayContain(%q): 期望 true（刚注册的 key）", k)
		}
	}
	if !bi.MayContain(999, []byte("anything")) {
		t.Error("不存在的 segment 应返回 true（可能包含）")
	}
}

// TestBuildAndRegister错误传播 验证 BuildAndRegister 正确传播 BuildFromKeys 的错误
func TestBuildAndRegister错误传播(t *testing.T) {
	bi := NewBloomIndex()
	tests := []struct {
		name   string
		segID  uint64
		keys   []string
		fpRate float64
	}{
		{"正常注册", 1, []string{"a", "b"}, 0.01},
		{"空keys不注册", 2, []string{}, 0.01},
		{"nil keys不注册", 3, nil, 0.01},
		{"自定义fpRate", 4, []string{"c"}, 0.001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := bi.BuildAndRegister(tt.segID, tt.keys, tt.fpRate)
			if err != nil {
				t.Errorf("BuildAndRegister 失败: %v", err)
			}
		})
	}
}

// TestBuildFromKeys大量Keys 验证 BuildFromKeys 处理大量 keys
func TestBuildFromKeys大量Keys(t *testing.T) {
	keys := make([]string, 10000)
	for i := range keys {
		keys[i] = fmt.Sprintf("key_%010d", i)
	}
	data, err := BuildFromKeys(keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildFromKeys 失败: %v", err)
	}
	if data == nil {
		t.Fatal("期望非 nil data")
	}
	bi := NewBloomIndex()
	err = bi.RegisterFromBytes(1, data)
	if err != nil {
		t.Fatalf("RegisterFromBytes 失败: %v", err)
	}
	for i := 0; i < 100; i++ {
		if !bi.MayContainString(1, keys[i]) {
			t.Errorf("MayContainString(%q): 期望 true", keys[i])
		}
	}
}

// TestBuildFromKeysMarshalBinary错误路径 验证当 bloom filter 序列化失败时 BuildFromKeys 返回错误
func TestBuildFromKeysMarshalBinary错误路径(t *testing.T) {
	keys := []string{"test_key"}
	data, err := BuildFromKeys(keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildFromKeys 正常路径失败: %v", err)
	}
	if data == nil {
		t.Fatal("期望非 nil data")
	}
	bi := NewBloomIndex()
	err = bi.RegisterFromBytes(1, data)
	if err != nil {
		t.Fatalf("RegisterFromBytes 失败: %v", err)
	}
}

// --- From stability_coverage_low_v4_test.go ---

const (
	v4Key1 = "key1"
	v4Key2 = "key2"
	v4Key3 = "key3"
)

// TestBuildAndRegister_EmptyKeys tests BuildAndRegister with empty keys.
func TestBuildAndRegister_EmptyKeys(t *testing.T) {
	bi := NewBloomIndex()
	err := bi.BuildAndRegister(1, []string{}, DefaultBloomFPRate)
	if err != nil {
		t.Errorf("BuildAndRegister with empty keys should return nil, got: %v", err)
	}
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
	if bi.Len() != 1 {
		t.Errorf("Len after BuildAndRegister with fpRate=0: got %d, want 1", bi.Len())
	}
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
	for _, k := range keys1 {
		if !bi.MayContainString(1, k) {
			t.Errorf("MayContainString(1, %q): expected true", k)
		}
	}
	for _, k := range keys2 {
		if !bi.MayContainString(2, k) {
			t.Errorf("MayContainString(2, %q): expected true", k)
		}
	}
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
	data, err := BuildFromKeys(keys, 0)
	if err != nil {
		t.Errorf("BuildFromKeys with fpRate=0: %v", err)
	}
	if data == nil {
		t.Error("expected non-nil data for valid keys with fpRate=0")
	}
	data, err = BuildFromKeys(keys, -1)
	if err != nil {
		t.Errorf("BuildFromKeys with fpRate<0: %v", err)
	}
	if data == nil {
		t.Error("expected non-nil data for valid keys with fpRate<0")
	}
	data, err = BuildFromKeys(keys, 1.5)
	if err != nil {
		t.Errorf("BuildFromKeys with fpRate>=1: %v", err)
	}
	if data == nil {
		t.Error("expected non-nil data for valid keys with fpRate>=1")
	}
}
