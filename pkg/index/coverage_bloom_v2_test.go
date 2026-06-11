package index

import (
	"testing"
)

// TestBuildAndRegisterEmptyKeys_CovV2 测试 BuildAndRegister 空 keys 时返回 nil 不注册
// 覆盖 bloom.go:165-167 行的 data==nil 分支
func TestBuildAndRegisterEmptyKeys_CovV2(t *testing.T) {
	bi := NewBloomIndex()

	// 空 slice：BuildFromKeys 返回 nil, nil → data==nil → 直接返回 nil
	err := bi.BuildAndRegister(1, []string{}, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister 空 keys 不应返回错误: %v", err)
	}
	if bi.Len() != 0 {
		t.Errorf("空 keys 后 Len = %d，期望 0", bi.Len())
	}

	// nil keys：同样走 data==nil 路径
	err = bi.BuildAndRegister(2, nil, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister nil keys 不应返回错误: %v", err)
	}
	if bi.Len() != 0 {
		t.Errorf("nil keys 后 Len = %d，期望 0", bi.Len())
	}

	// 验证未注册的 Segment 查询返回 true（保守策略：无过滤器时不跳过）
	if !bi.MayContain(1, []byte("any")) {
		t.Error("未注册 Segment 的 MayContain 应返回 true")
	}
}

// TestBuildAndRegisterNormalKeys_CovV2 测试 BuildAndRegister 正常 keys 的完整路径
// 覆盖 bloom.go:161-168 行，包括 BuildFromKeys 成功和 RegisterFromBytes 调用
func TestBuildAndRegisterNormalKeys_CovV2(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{"key-a", "key-b", "key-c"}
	err := bi.BuildAndRegister(1, keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister 正常 keys 不应返回错误: %v", err)
	}
	if bi.Len() != 1 {
		t.Errorf("正常 keys 后 Len = %d，期望 1", bi.Len())
	}

	// 验证注册的 key 可以被查到
	for _, k := range keys {
		if !bi.MayContain(1, []byte(k)) {
			t.Errorf("MayContain(%q): 期望 true", k)
		}
	}

	// 验证 MayContainString 也能正常工作
	for _, k := range keys {
		if !bi.MayContainString(1, k) {
			t.Errorf("MayContainString(%q): 期望 true", k)
		}
	}
}

// TestRegisterFromBytesCorruptData_CovV2 测试 RegisterFromBytes 对损坏数据的错误处理
// 覆盖 bloom.go:53-54 行的 UnmarshalBinary 错误分支
func TestRegisterFromBytesCorruptData_CovV2(t *testing.T) {
	bi := NewBloomIndex()

	// 构建有效过滤器并截断数据以触发反序列化错误
	validData, err := BuildFromKeys([]string{"a", "b"}, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildFromKeys 失败: %v", err)
	}
	if len(validData) < 4 {
		t.Fatalf("有效数据太短: %d 字节", len(validData))
	}

	// 使用截断的数据触发 UnmarshalBinary 错误
	truncatedData := validData[:len(validData)/2]
	err = bi.RegisterFromBytes(1, truncatedData)
	if err == nil {
		t.Error("RegisterFromBytes 截断数据应返回错误")
	}

	// 验证失败注册后索引中没有过滤器
	if bi.Len() != 0 {
		t.Errorf("失败注册后 Len = %d，期望 0", bi.Len())
	}

	// 使用完全随机的无效数据测试
	randomData := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	err = bi.RegisterFromBytes(2, randomData)
	if err == nil {
		t.Error("RegisterFromBytes 随机数据应返回错误")
	}
}

// TestLookupEmptyIndex_CovV2 测试 PrimaryIndex.Lookup 在空索引上返回 nil
// 覆盖 primary.go:66-68 行的空 segments 分支
func TestLookupEmptyIndex_CovV2(t *testing.T) {
	pi := NewPrimaryIndex()

	// 空索引上 Lookup 应返回 nil
	result := pi.Lookup("any-key")
	if result != nil {
		t.Errorf("空索引 Lookup 应返回 nil，得到 %v", result)
	}

	// 验证 SegmentCount 为 0
	if pi.SegmentCount() != 0 {
		t.Errorf("空索引 SegmentCount = %d，期望 0", pi.SegmentCount())
	}

	// 注册后再移除，验证 Lookup 仍返回 nil
	_ = pi.RegisterSegment(SegmentMeta{ID: 1, MinKey: "a", MaxKey: "z"})
	_ = pi.UnregisterSegment(1)

	result = pi.Lookup("m")
	if result != nil {
		t.Errorf("移除所有 segment 后 Lookup 应返回 nil，得到 %v", result)
	}
}
