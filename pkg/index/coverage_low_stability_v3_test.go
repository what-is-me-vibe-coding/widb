package index

import (
	"testing"
)

// ---------------------------------------------------------------------------
// BuildAndRegister 错误路径覆盖（bloom.go:160）
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
