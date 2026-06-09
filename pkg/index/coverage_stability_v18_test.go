package index

import (
	"testing"
)

// ---------------------------------------------------------------------------
// BuildAndRegister 错误路径补充
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

// ---------------------------------------------------------------------------
// RegisterFromBytes 空数据路径
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// MayContain 未注册 segment 测试
// ---------------------------------------------------------------------------

// TestMayContain_UnregisteredSegment_V18 测试 MayContain 对未注册 segment 返回 true。
func TestMayContain_UnregisteredSegment_V18(t *testing.T) {
	bi := NewBloomIndex()

	result := bi.MayContain(99, []byte("any-key"))
	if !result {
		t.Error("MayContain should return true for unregistered segment")
	}
}

// ---------------------------------------------------------------------------
// BuildFromKeys 边界条件（补充无效 fpRate 路径）
// ---------------------------------------------------------------------------

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
