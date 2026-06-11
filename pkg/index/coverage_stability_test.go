package index

import (
	"testing"
)

// ---------------------------------------------------------------------------
// BuildAndRegister: 空 keys、成功、无效 fpRate
// ---------------------------------------------------------------------------

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
