package index

import (
	"testing"
)

// ---------------------------------------------------------------------------
// From coverage_stability_test.go
// ---------------------------------------------------------------------------

// TestCoverageStabilityBuildAndRegisterEmptyKeys 测试 BuildAndRegister 空 keys
func TestCoverageStabilityBuildAndRegisterEmptyKeys(t *testing.T) {
	bi := NewBloomIndex()

	err := bi.BuildAndRegister(1, nil, DefaultBloomFPRate)
	if err != nil {
		t.Errorf("空 keys 不应返回错误: %v", err)
	}
	if bi.Len() != 0 {
		t.Errorf("空 keys 不应注册过滤器，Len = %d", bi.Len())
	}

	err = bi.BuildAndRegister(2, []string{}, DefaultBloomFPRate)
	if err != nil {
		t.Errorf("空 keys 切片不应返回错误: %v", err)
	}
	if bi.Len() != 0 {
		t.Errorf("空 keys 切片不应注册过滤器，Len = %d", bi.Len())
	}
}

func TestCoverageStabilityBuildAndRegisterSuccess(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{"user_1", "user_2", "user_3"}
	err := bi.BuildAndRegister(10, keys, 0.01)
	if err != nil {
		t.Fatalf("BuildAndRegister 失败: %v", err)
	}
	if bi.Len() != 1 {
		t.Fatalf("Len = %d, want 1", bi.Len())
	}

	// 验证已注册的过滤器能正确判断
	if !bi.MayContainString(10, "user_1") {
		t.Error("已注册的 key 应被 MayContainString 找到")
	}
	if !bi.MayContainString(10, "user_2") {
		t.Error("已注册的 key 应被 MayContainString 找到")
	}
}

func TestCoverageStabilityBuildAndRegisterInvalidFPRate(t *testing.T) {
	tests := []struct {
		name   string
		fpRate float64
	}{
		{"fpRate 为零", 0},
		{"fpRate 为负数", -0.5},
		{"fpRate 大于 1", 1.5},
		{"fpRate 等于 1", 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bi := NewBloomIndex()
			err := bi.BuildAndRegister(1, []string{"key_a", "key_b"}, tt.fpRate)
			if err != nil {
				t.Errorf("无效 fpRate 不应返回错误（应使用默认值）: %v", err)
			}
			if bi.Len() != 1 {
				t.Errorf("应使用默认 fpRate 注册过滤器，Len = %d", bi.Len())
			}
			// 验证过滤器仍然可用
			if !bi.MayContainString(1, "key_a") {
				t.Error("已注册的 key 应被找到")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// From coverage_low_stability_v3_test.go
// ---------------------------------------------------------------------------

// TestBuildAndRegisterEmptyKeysCov_V3 测试 BuildAndRegister 传入空键切片
// 覆盖 bloom.go:165-167 行的空键路径（BuildFromKeys 返回 nil data -> 直接返回 nil）
func TestBuildAndRegisterEmptyKeysCov_V3(t *testing.T) {
	bi := NewBloomIndex()
	err := bi.BuildAndRegister(1, []string{}, 0.01)
	if err != nil {
		t.Errorf("期望空键返回 nil 错误，得到: %v", err)
	}
	if bi.Len() != 0 {
		t.Errorf("期望 0 个过滤器，得到 %d", bi.Len())
	}
}

// TestBuildAndRegisterNilKeysCov_V3 测试 BuildAndRegister 传入 nil 键切片
// 覆盖 bloom.go:165-167 行的空键路径
func TestBuildAndRegisterNilKeysCov_V3(t *testing.T) {
	bi := NewBloomIndex()
	err := bi.BuildAndRegister(2, nil, 0.01)
	if err != nil {
		t.Errorf("期望 nil 键返回 nil 错误，得到: %v", err)
	}
	if bi.Len() != 0 {
		t.Errorf("期望 0 个过滤器，得到 %d", bi.Len())
	}
}

// TestBuildAndRegisterValidKeysCov_V3 测试 BuildAndRegister 正常注册路径
// 覆盖 bloom.go:168 行的 RegisterFromBytes 正常调用路径
func TestBuildAndRegisterValidKeysCov_V3(t *testing.T) {
	bi := NewBloomIndex()
	keys := []string{"key_a", "key_b", "key_c"}
	err := bi.BuildAndRegister(10, keys, 0.01)
	if err != nil {
		t.Fatalf("BuildAndRegister 失败: %v", err)
	}
	if bi.Len() != 1 {
		t.Errorf("期望 1 个过滤器，得到 %d", bi.Len())
	}
	// 验证已注册的 key 可以被查询到
	if !bi.MayContain(10, []byte("key_a")) {
		t.Error("期望 key_a 存在于布隆过滤器中")
	}
}

// TestBuildAndRegisterInvalidFpRateCov_V3 测试 BuildAndRegister 对无效误判率的处理
// fpRate <= 0 或 >= 1 时会被修正为默认值，不应返回错误
func TestBuildAndRegisterInvalidFpRateCov_V3(t *testing.T) {
	bi := NewBloomIndex()
	keys := []string{"key_x", "key_y"}

	tests := []struct {
		name    string
		fpRate  float64
		wantErr bool
	}{
		{"零误判率", 0, false},
		{"负误判率", -0.5, false},
		{"误判率等于1", 1.0, false},
		{"误判率大于1", 2.0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := bi.BuildAndRegister(20, keys, tt.fpRate)
			if tt.wantErr && err == nil {
				t.Error("期望返回错误，得到 nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("不期望错误，得到: %v", err)
			}
		})
	}
}

// TestBuildAndRegisterOverwriteCov_V3 测试 BuildAndRegister 覆盖已注册的 segment
// 验证同一 segID 多次注册时后一次覆盖前一次
func TestBuildAndRegisterOverwriteCov_V3(t *testing.T) {
	bi := NewBloomIndex()

	// 第一次注册
	err := bi.BuildAndRegister(30, []string{"first"}, 0.01)
	if err != nil {
		t.Fatalf("第一次 BuildAndRegister 失败: %v", err)
	}

	// 第二次注册同一 segID（覆盖）
	err = bi.BuildAndRegister(30, []string{"second", "third"}, 0.01)
	if err != nil {
		t.Fatalf("第二次 BuildAndRegister 失败: %v", err)
	}

	if bi.Len() != 1 {
		t.Errorf("期望 1 个过滤器（覆盖），得到 %d", bi.Len())
	}
}

// TestBuildAndRegisterMultipleSegmentsCov_V3 测试 BuildAndRegister 注册多个 segment
func TestBuildAndRegisterMultipleSegmentsCov_V3(t *testing.T) {
	bi := NewBloomIndex()

	for i := 0; i < 5; i++ {
		keys := []string{string(rune('a' + i))}
		err := bi.BuildAndRegister(uint64(i+1), keys, 0.01)
		if err != nil {
			t.Fatalf("BuildAndRegister %d 失败: %v", i, err)
		}
	}

	if bi.Len() != 5 {
		t.Errorf("期望 5 个过滤器，得到 %d", bi.Len())
	}
}

// ---------------------------------------------------------------------------
// From coverage_stability_v18_test.go
// ---------------------------------------------------------------------------

// TestBuildAndRegister_InvalidFPRate_V18 测试 BuildAndRegister 使用无效误判率时使用默认值。
func TestBuildAndRegister_InvalidFPRate_V18(t *testing.T) {
	bi := NewBloomIndex()

	// fpRate <= 0 应使用默认值
	err := bi.BuildAndRegister(1, []string{testBloomKey1, testBloomKey2}, 0)
	if err != nil {
		t.Errorf("BuildAndRegister with fpRate=0 should not error: %v", err)
	}
	if bi.Len() != 1 {
		t.Errorf("expected 1 filter, got %d", bi.Len())
	}

	// fpRate >= 1 应使用默认值
	bi.Clear()
	err = bi.BuildAndRegister(2, []string{testBloomKey3}, 1.5)
	if err != nil {
		t.Errorf("BuildAndRegister with fpRate>=1 should not error: %v", err)
	}
	if bi.Len() != 1 {
		t.Errorf("expected 1 filter, got %d", bi.Len())
	}
}

// TestBuildAndRegister_NilFilter_V18 测试 Register 方法传入 nil filter 的错误路径。
func TestBuildAndRegister_NilFilter_V18(t *testing.T) {
	bi := NewBloomIndex()

	err := bi.Register(1, nil)
	if err == nil {
		t.Error("expected error for nil filter, got nil")
	}
}

// TestRegisterFromBytes_EmptyData_V18 测试 RegisterFromBytes 传入空数据时直接返回 nil。
func TestRegisterFromBytes_EmptyData_V18(t *testing.T) {
	bi := NewBloomIndex()

	err := bi.RegisterFromBytes(1, []byte{})
	if err != nil {
		t.Errorf("RegisterFromBytes with empty data should not error: %v", err)
	}
	if bi.Len() != 0 {
		t.Errorf("expected 0 filters after empty data registration, got %d", bi.Len())
	}
}

// TestRegisterFromBytes_NilData_V18 测试 RegisterFromBytes 传入 nil 数据时直接返回 nil。
func TestRegisterFromBytes_NilData_V18(t *testing.T) {
	bi := NewBloomIndex()

	err := bi.RegisterFromBytes(1, nil)
	if err != nil {
		t.Errorf("RegisterFromBytes with nil data should not error: %v", err)
	}
}

// TestMayContain_UnregisteredSegment_V18 测试 MayContain 对未注册 segment 返回 true。
func TestMayContain_UnregisteredSegment_V18(t *testing.T) {
	bi := NewBloomIndex()

	result := bi.MayContain(99, []byte("any-key"))
	if !result {
		t.Error("MayContain should return true for unregistered segment")
	}
}

// TestBuildFromKeys_InvalidFPRate_V18 测试 BuildFromKeys 使用无效误判率时使用默认值。
func TestBuildFromKeys_InvalidFPRate_V18(t *testing.T) {
	// fpRate <= 0 应使用默认值
	data, err := BuildFromKeys([]string{testBloomKey1}, 0)
	if err != nil {
		t.Errorf("BuildFromKeys with fpRate=0 should not error: %v", err)
	}
	if data == nil {
		t.Error("expected non-nil data for valid keys with fpRate=0")
	}

	// fpRate >= 1 应使用默认值
	data, err = BuildFromKeys([]string{testBloomKey2}, 2.0)
	if err != nil {
		t.Errorf("BuildFromKeys with fpRate>=1 should not error: %v", err)
	}
	if data == nil {
		t.Error("expected non-nil data for valid keys with fpRate>=1")
	}
}
