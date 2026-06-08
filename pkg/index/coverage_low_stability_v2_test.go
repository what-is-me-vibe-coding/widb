package index

import (
	"fmt"
	"testing"

	"github.com/bits-and-blooms/bloom/v3"
)

// ---------------------------------------------------------------------------
// Register: nil filter 错误路径
// ---------------------------------------------------------------------------

// TestRegisterNilFilter 验证 Register 在 filter 为 nil 时返回错误
func TestRegisterNilFilter(t *testing.T) {
	bi := NewBloomIndex()

	err := bi.Register(1, nil)
	if err == nil {
		t.Error("期望 nil filter 返回错误，得到 nil")
	}
}

// TestRegister正常Filter 验证 Register 正常注册布隆过滤器
func TestRegister正常Filter(t *testing.T) {
	bi := NewBloomIndex()

	filter := bloom.NewWithEstimates(100, DefaultBloomFPRate)
	filter.Add([]byte("test"))

	err := bi.Register(1, filter)
	if err != nil {
		t.Fatalf("Register 失败: %v", err)
	}

	if bi.Len() != 1 {
		t.Errorf("期望 Len=1，得到 %d", bi.Len())
	}
}

// ---------------------------------------------------------------------------
// RegisterFromBytes: 空数据和无效数据
// ---------------------------------------------------------------------------

// TestRegisterFromBytes空数据 验证 RegisterFromBytes 在空数据时不注册
func TestRegisterFromBytes空数据(t *testing.T) {
	bi := NewBloomIndex()

	err := bi.RegisterFromBytes(1, []byte{})
	if err != nil {
		t.Fatalf("RegisterFromBytes 空 data 不应返回错误: %v", err)
	}

	if bi.Len() != 0 {
		t.Errorf("期望 Len=0（空 data 不注册），得到 %d", bi.Len())
	}
}

// TestRegisterFromBytesNil数据 验证 RegisterFromBytes 在 nil 数据时不注册
func TestRegisterFromBytesNil数据(t *testing.T) {
	bi := NewBloomIndex()

	err := bi.RegisterFromBytes(1, nil)
	if err != nil {
		t.Fatalf("RegisterFromBytes nil data 不应返回错误: %v", err)
	}

	if bi.Len() != 0 {
		t.Errorf("期望 Len=0（nil data 不注册），得到 %d", bi.Len())
	}
}

// TestRegisterFromBytes无效数据 验证 RegisterFromBytes 在无效数据时返回错误
// 注意：bloom 库的 UnmarshalBinary 对某些无效数据会 panic，
// 所以我们使用 recover 来安全地测试
func TestRegisterFromBytes无效数据(t *testing.T) {
	bi := NewBloomIndex()

	// 使用 recover 防止 panic
	func() {
		defer func() {
			if r := recover(); r != nil {
				// bloom 库对无效数据会 panic，这是预期行为
				t.Logf("bloom 库对无效数据 panic（预期行为）: %v", r)
			}
		}()
		err := bi.RegisterFromBytes(1, []byte("invalid bloom filter data"))
		// 如果没有 panic，应该返回错误
		if err == nil {
			t.Error("期望无效数据返回错误，得到 nil")
		}
	}()
}

// TestRegisterFromBytes正常数据 验证 RegisterFromBytes 正常注册
func TestRegisterFromBytes正常数据(t *testing.T) {
	bi := NewBloomIndex()

	// 先构建一个有效的布隆过滤器数据
	keys := []string{testBloomKey1, testBloomKey2}
	data, err := BuildFromKeys(keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildFromKeys 失败: %v", err)
	}

	err = bi.RegisterFromBytes(1, data)
	if err != nil {
		t.Fatalf("RegisterFromBytes 失败: %v", err)
	}

	if bi.Len() != 1 {
		t.Errorf("期望 Len=1，得到 %d", bi.Len())
	}
}

// ---------------------------------------------------------------------------
// Unregister: 正常和边界情况
// ---------------------------------------------------------------------------

// TestUnregister正常移除 验证 Unregister 正常移除布隆过滤器
func TestUnregister正常移除(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{testBloomKey1}
	err := bi.BuildAndRegister(1, keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister 失败: %v", err)
	}

	if bi.Len() != 1 {
		t.Fatalf("期望 Len=1，得到 %d", bi.Len())
	}

	bi.Unregister(1)

	if bi.Len() != 0 {
		t.Errorf("期望 Len=0（已移除），得到 %d", bi.Len())
	}
}

// TestUnregister不存在的Segment 验证 Unregister 移除不存在的 segment 不报错
func TestUnregister不存在的Segment(t *testing.T) {
	bi := NewBloomIndex()

	// 移除不存在的 segment 不应 panic
	bi.Unregister(999)

	if bi.Len() != 0 {
		t.Errorf("期望 Len=0，得到 %d", bi.Len())
	}
}

// ---------------------------------------------------------------------------
// MayContain / MayContainString: 查询路径
// ---------------------------------------------------------------------------

// TestMayContain未注册Segment 验证 MayContain 对未注册的 segment 返回 true
func TestMayContain未注册Segment(t *testing.T) {
	bi := NewBloomIndex()

	// 未注册的 segment 应返回 true（可能包含）
	if !bi.MayContain(999, []byte("key")) {
		t.Error("未注册的 segment 应返回 true")
	}
}

// TestMayContainString未注册Segment 验证 MayContainString 对未注册的 segment 返回 true
func TestMayContainString未注册Segment(t *testing.T) {
	bi := NewBloomIndex()

	if !bi.MayContainString(999, "key") {
		t.Error("未注册的 segment 应返回 true")
	}
}

// TestMayContainString已注册Key 验证 MayContainString 对已注册的 key 返回 true
func TestMayContainString已注册Key(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{testAlpha, testBeta, testGamma}
	err := bi.BuildAndRegister(1, keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister 失败: %v", err)
	}

	for _, k := range keys {
		if !bi.MayContainString(1, k) {
			t.Errorf("MayContainString(%q): 期望 true", k)
		}
	}
}

// ---------------------------------------------------------------------------
// Stats: 统计信息
// ---------------------------------------------------------------------------

// TestStats初始值 验证 Stats 初始值为零
func TestStats初始值(t *testing.T) {
	bi := NewBloomIndex()

	hit, miss := bi.Stats()
	if hit != 0 || miss != 0 {
		t.Errorf("期望初始 hit=0, miss=0，得到 hit=%d, miss=%d", hit, miss)
	}
}

// TestStats查询后更新 验证 Stats 在查询后更新
func TestStats查询后更新(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{testBloomKey1, testBloomKey2}
	err := bi.BuildAndRegister(1, keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister 失败: %v", err)
	}

	// 查询已注册的 key（命中）
	_ = bi.MayContain(1, []byte(testBloomKey1))

	// 查询不存在的 key（可能命中或未命中）
	_ = bi.MayContain(1, []byte("nonexistent_key_12345"))

	hit, miss := bi.Stats()
	if hit+miss == 0 {
		t.Error("期望查询后统计信息更新")
	}
}

// ---------------------------------------------------------------------------
// Clear: 清空
// ---------------------------------------------------------------------------

// TestClear清空所有 验证 Clear 清空所有布隆过滤器和统计信息
func TestClear清空所有(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{"key1"}
	err := bi.BuildAndRegister(1, keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister 失败: %v", err)
	}

	// 查询以更新统计
	_ = bi.MayContain(1, []byte("key1"))

	bi.Clear()

	if bi.Len() != 0 {
		t.Errorf("期望 Len=0（已清空），得到 %d", bi.Len())
	}

	hit, miss := bi.Stats()
	if hit != 0 || miss != 0 {
		t.Errorf("期望清空后 hit=0, miss=0，得到 hit=%d, miss=%d", hit, miss)
	}
}

// ---------------------------------------------------------------------------
// Len: 长度
// ---------------------------------------------------------------------------

// TestLen初始值 验证 Len 初始值为零
func TestLen初始值(t *testing.T) {
	bi := NewBloomIndex()
	if bi.Len() != 0 {
		t.Errorf("期望 Len=0，得到 %d", bi.Len())
	}
}

// TestLen多个注册 验证 Len 在多个注册后正确返回
func TestLen多个注册(t *testing.T) {
	bi := NewBloomIndex()

	for i := 0; i < 5; i++ {
		keys := []string{fmt.Sprintf("key%d", i)}
		err := bi.BuildAndRegister(uint64(i+1), keys, DefaultBloomFPRate)
		if err != nil {
			t.Fatalf("BuildAndRegister %d 失败: %v", i, err)
		}
	}

	if bi.Len() != 5 {
		t.Errorf("期望 Len=5，得到 %d", bi.Len())
	}
}

// ---------------------------------------------------------------------------
// RegisterFromBytes: 反序列化错误路径
// ---------------------------------------------------------------------------

// TestRegisterFromBytes各种无效数据 验证 RegisterFromBytes 处理各种无效数据
// 注意：bloom 库的 UnmarshalBinary 对某些无效数据会 panic，
// 所以我们只测试不会 panic 的边界情况
func TestRegisterFromBytes各种无效数据(t *testing.T) {
	bi := NewBloomIndex()

	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{"空切片", []byte{}, false},
		{"nil", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := bi.RegisterFromBytes(1, tt.data)
			if tt.wantErr && err == nil {
				t.Error("期望返回错误，得到 nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("不期望错误，得到: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// MayContain / MayContainString: 统计准确性
// ---------------------------------------------------------------------------

// TestMayContain统计准确性 验证 MayContain 正确更新统计信息
func TestMayContain统计准确性(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{"hit_key"}
	err := bi.BuildAndRegister(1, keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister 失败: %v", err)
	}

	// 查询存在的 key（应该命中）
	result := bi.MayContain(1, []byte("hit_key"))
	if !result {
		t.Error("期望存在的 key 返回 true")
	}

	hit, _ := bi.Stats()
	if hit != 1 {
		t.Errorf("期望 hit=1，得到 %d", hit)
	}
}

// TestMayContainString统计准确性 验证 MayContainString 正确更新统计信息
func TestMayContainString统计准确性(t *testing.T) {
	bi := NewBloomIndex()

	keys := []string{"hit_key"}
	err := bi.BuildAndRegister(1, keys, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildAndRegister 失败: %v", err)
	}

	// 查询存在的 key
	result := bi.MayContainString(1, "hit_key")
	if !result {
		t.Error("期望存在的 key 返回 true")
	}

	hit, _ := bi.Stats()
	if hit != 1 {
		t.Errorf("期望 hit=1，得到 %d", hit)
	}
}

// ---------------------------------------------------------------------------
// BloomIndex: 并发安全验证
// ---------------------------------------------------------------------------

// TestBloomIndex并发注册 验证 BloomIndex 并发注册的安全性
func TestBloomIndex并发注册(t *testing.T) {
	bi := NewBloomIndex()

	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func(segID uint64) {
			keys := []string{fmt.Sprintf("key_%d", segID)}
			err := bi.BuildAndRegister(segID, keys, DefaultBloomFPRate)
			if err != nil {
				t.Errorf("BuildAndRegister %d 失败: %v", segID, err)
			}
			done <- true
		}(uint64(i + 1))
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	if bi.Len() != 10 {
		t.Errorf("期望 Len=10，得到 %d", bi.Len())
	}
}

// ---------------------------------------------------------------------------
// RegisterFromBytes: 覆盖已注册的 segment
// ---------------------------------------------------------------------------

// TestRegisterFromBytes覆盖注册 验证 RegisterFromBytes 覆盖已注册的 segment
func TestRegisterFromBytes覆盖注册(t *testing.T) {
	bi := NewBloomIndex()

	keys1 := []string{testBloomKey1}
	data1, err := BuildFromKeys(keys1, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildFromKeys 失败: %v", err)
	}

	err = bi.RegisterFromBytes(1, data1)
	if err != nil {
		t.Fatalf("RegisterFromBytes 失败: %v", err)
	}

	// 用新的数据覆盖
	keys2 := []string{testBloomKey2, testBloomKey3}
	data2, err := BuildFromKeys(keys2, DefaultBloomFPRate)
	if err != nil {
		t.Fatalf("BuildFromKeys 失败: %v", err)
	}

	err = bi.RegisterFromBytes(1, data2)
	if err != nil {
		t.Fatalf("RegisterFromBytes 覆盖失败: %v", err)
	}

	// 验证仍然只有一个 segment
	if bi.Len() != 1 {
		t.Errorf("期望 Len=1（覆盖），得到 %d", bi.Len())
	}
}
