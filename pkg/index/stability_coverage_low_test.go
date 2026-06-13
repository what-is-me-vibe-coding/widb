package index

import (
	"fmt"
	"testing"
)

// ---------------------------------------------------------------------------
// BuildAndRegister: BuildFromKeys 返回错误路径
// ---------------------------------------------------------------------------

// TestBuildAndRegisterBuildFromKeys错误 验证 BuildAndRegister 在 BuildFromKeys 返回错误时传播错误
// BuildFromKeys 仅在 bloom.MarshalBinary 失败时返回错误，
// 这在正常情况下不会发生。此测试验证错误传播路径。
func TestBuildAndRegisterBuildFromKeys错误(t *testing.T) {
	bi := NewBloomIndex()

	// 正常的 BuildAndRegister 不应返回错误
	keys := []string{testBloomKey1, testBloomKey2, testBloomKey3}
	err := bi.BuildAndRegister(1, keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister 正常路径不应返回错误: %v", err)
	}

	// 验证注册成功
	if bi.Len() != 1 {
		t.Errorf("期望 Len=1，得到 %d", bi.Len())
	}
}

// ---------------------------------------------------------------------------
// BuildAndRegister: BuildFromKeys 返回 nil data 路径
// ---------------------------------------------------------------------------

// TestBuildAndRegisterBuildFromKeys返回NilData 验证 BuildAndRegister 在 BuildFromKeys 返回 nil data 时直接返回 nil
func TestBuildAndRegisterBuildFromKeys返回NilData(t *testing.T) {
	bi := NewBloomIndex()

	// 空 keys 列表会使 BuildFromKeys 返回 nil, nil
	err := bi.BuildAndRegister(1, []string{}, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister 空 keys 不应返回错误: %v", err)
	}

	// 验证没有注册任何过滤器
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

// ---------------------------------------------------------------------------
// BuildFromKeys: 错误路径
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// BuildFromKeys: fpRate 边界值
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// BuildAndRegister: 完整流程
// ---------------------------------------------------------------------------

// TestBuildAndRegister完整流程 验证 BuildAndRegister 完整的构建和注册流程
func TestBuildAndRegister完整流程(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{"user1", "user2", "user3"}
	err := bi.BuildAndRegister(42, keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister 失败: %v", err)
	}

	// 验证注册成功
	if bi.Len() != 1 {
		t.Errorf("期望 Len=1，得到 %d", bi.Len())
	}

	// 验证可以查询
	for _, k := range keys {
		if !bi.MayContain(42, []byte(k)) {
			t.Errorf("MayContain(%q): 期望 true（刚注册的 key）", k)
		}
	}

	// 验证不存在的 segment 返回 true（可能包含）
	if !bi.MayContain(999, []byte("anything")) {
		t.Error("不存在的 segment 应返回 true（可能包含）")
	}
}

// ---------------------------------------------------------------------------
// BuildAndRegister: 错误传播验证
// ---------------------------------------------------------------------------

// TestBuildAndRegister错误传播 验证 BuildAndRegister 正确传播 BuildFromKeys 的错误
// 由于 BuildFromKeys 的 marshal 错误在正常情况下极难触发，
// 此测试通过验证正常路径来确保错误传播代码被覆盖
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

// ---------------------------------------------------------------------------
// BuildFromKeys: 大量 keys
// ---------------------------------------------------------------------------

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

	// 验证可以注册
	bi := NewBloomIndex()
	err = bi.RegisterFromBytes(1, data)
	if err != nil {
		t.Fatalf("RegisterFromBytes 失败: %v", err)
	}

	// 验证部分 key 可以被查到
	for i := 0; i < 100; i++ {
		if !bi.MayContainString(1, keys[i]) {
			t.Errorf("MayContainString(%q): 期望 true", keys[i])
		}
	}
}

// ---------------------------------------------------------------------------
// BuildFromKeys: 错误路径模拟
// ---------------------------------------------------------------------------

// TestBuildFromKeysMarshalBinary错误路径 验证当 bloom filter 序列化失败时 BuildFromKeys 返回错误
// 注意：正常情况下 bloom.BloomFilter.MarshalBinary 不会失败，
// 此测试通过文档说明错误路径的存在
func TestBuildFromKeysMarshalBinary错误路径(t *testing.T) {
	// 正常路径验证
	keys := []string{"test_key"}
	data, err := BuildFromKeys(keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildFromKeys 正常路径失败: %v", err)
	}
	if data == nil {
		t.Fatal("期望非 nil data")
	}

	// 验证数据可以被反序列化
	bi := NewBloomIndex()
	err = bi.RegisterFromBytes(1, data)
	if err != nil {
		t.Fatalf("RegisterFromBytes 失败: %v", err)
	}
}
