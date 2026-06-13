package common

import (
	"testing"
)

// TestCopyFrom_AlignedFullWords 测试源起始位对齐（srcBitOff==0）且 count 恰好填满完整 word。
func TestCopyFrom_AlignedFullWords(t *testing.T) {
	// 构造源位图：128 位，设置一些位
	src := NewBitmap(128)
	src.Set(0)
	src.Set(1)
	src.Set(63)
	src.Set(64)
	src.Set(127)

	// 目标位图：128 位，初始全零
	dst := NewBitmap(128)
	dst.CopyFrom(src, 0, 128)

	// 验证对齐路径：srcBitOff == 0，直接按 word 拷贝
	if !dst.Get(0) {
		t.Error("期望 dst[0] = true")
	}
	if !dst.Get(1) {
		t.Error("期望 dst[1] = true")
	}
	if !dst.Get(63) {
		t.Error("期望 dst[63] = true")
	}
	if !dst.Get(64) {
		t.Error("期望 dst[64] = true")
	}
	if !dst.Get(127) {
		t.Error("期望 dst[127] = true")
	}
	if dst.Get(62) {
		t.Error("期望 dst[62] = false")
	}
}

// TestCopyFrom_MisalignedSource 测试源起始位未对齐（srcBitOff != 0），跨 word 拼接路径。
func TestCopyFrom_MisalignedSource(t *testing.T) {
	// 构造源位图：200 位
	src := NewBitmap(200)
	// 在偏移 3 处开始设置位
	src.Set(3)
	src.Set(10)
	src.Set(66) // 跨 word 边界
	src.Set(67)

	// 从 srcStart=3 开始复制 64 位到 dst
	dst := NewBitmap(64)
	dst.CopyFrom(src, 3, 64)

	// dst[0] 对应 src[3]
	if !dst.Get(0) {
		t.Error("期望 dst[0] = true（来自 src[3]）")
	}
	// dst[7] 对应 src[10]
	if !dst.Get(7) {
		t.Error("期望 dst[7] = true（来自 src[10]）")
	}
}

// TestCopyFrom_MisalignedCrossWord 测试未对齐源跨 word 边界拼接。
func TestCopyFrom_MisalignedCrossWord(t *testing.T) {
	// 构造源位图：192 位（3 个 word）
	src := NewBitmap(192)
	// 在 src[63] 设置位，当 srcStart=1 时，复制第一轮会跨越 word 0 和 word 1
	src.Set(63)
	src.Set(64)

	// 从 srcStart=1 开始复制 64 位
	dst := NewBitmap(64)
	dst.CopyFrom(src, 1, 64)

	// dst[62] 对应 src[63]
	if !dst.Get(62) {
		t.Error("期望 dst[62] = true（来自 src[63]）")
	}
	// dst[63] 对应 src[64]
	if !dst.Get(63) {
		t.Error("期望 dst[63] = true（来自 src[64]）")
	}
}

// TestCopyFrom_PartialLastWord 测试最后一轮截断（remaining < 64）。
func TestCopyFrom_PartialLastWord(t *testing.T) {
	// 构造源位图：128 位
	src := NewBitmap(128)
	for i := uint32(0); i < 128; i++ {
		src.Set(i)
	}

	// 只复制 65 位，最后一个 word 只有 1 位有效
	dst := NewBitmap(128)
	dst.CopyFrom(src, 0, 65)

	// 前 64 位应该全部为 true
	for i := uint32(0); i < 64; i++ {
		if !dst.Get(i) {
			t.Errorf("期望 dst[%d] = true", i)
		}
	}
	// 第 65 位（dst[64]）应该为 true
	if !dst.Get(64) {
		t.Error("期望 dst[64] = true")
	}
	// 第 66 位（dst[65]）应该为 false（超出 count 范围）
	if dst.Get(65) {
		t.Error("期望 dst[65] = false（超出复制范围）")
	}
}

// TestCopyFrom_PartialLastWordPreservesDst 测试部分 word 复制保留目标 word 中超出范围的原值。
func TestCopyFrom_PartialLastWordPreservesDst(t *testing.T) {
	src := NewBitmap(64)
	src.Set(0)
	src.Set(1)
	src.Set(2)

	// 目标位图先设置一些位
	dst := NewBitmap(64)
	dst.Set(10)
	dst.Set(20)
	dst.Set(63)

	// 只复制 3 位，最后一个 word 只有 3 位有效
	// 应保留 dst 中 bit 3~63 的原值
	dst.CopyFrom(src, 0, 3)

	if !dst.Get(0) || !dst.Get(1) || !dst.Get(2) {
		t.Error("期望 dst[0,1,2] = true")
	}
	if dst.Get(3) {
		t.Error("期望 dst[3] = false（src 中为 false）")
	}
	// bit 10 和 bit 20 应该被保留（mask 之外的位）
	if !dst.Get(10) {
		t.Error("期望 dst[10] = true（保留原值）")
	}
	if !dst.Get(20) {
		t.Error("期望 dst[20] = true（保留原值）")
	}
	if !dst.Get(63) {
		t.Error("期望 dst[63] = true（保留原值）")
	}
}

// TestCopyFrom_CountExceedsDst 测试 count 超过目标位图长度时截断。
func TestCopyFrom_CountExceedsDst(t *testing.T) {
	src := NewBitmap(256)
	for i := uint32(0); i < 256; i++ {
		src.Set(i)
	}

	// 目标位图只有 64 位，但 count=200
	dst := NewBitmap(64)
	dst.CopyFrom(src, 0, 200)

	// 应该只复制了 64 位
	for i := uint32(0); i < 64; i++ {
		if !dst.Get(i) {
			t.Errorf("期望 dst[%d] = true", i)
		}
	}
}

// TestCopyFrom_SrcStartPlusCountExceedsSrc 测试 srcStart+count 超过源位图长度时截断。
func TestCopyFrom_SrcStartPlusCountExceedsSrc(t *testing.T) {
	src := NewBitmap(100)
	for i := uint32(0); i < 100; i++ {
		src.Set(i)
	}

	// srcStart=50, count=100，但 src 只有 100 位，srcEnd 会被截断为 100
	dst := NewBitmap(128)
	dst.CopyFrom(src, 50, 100)

	// 实际只复制了 50 位（src[50..99]）
	for i := uint32(0); i < 50; i++ {
		if !dst.Get(i) {
			t.Errorf("期望 dst[%d] = true（来自 src[%d]）", i, i+50)
		}
	}
	// dst[50] 应该为 false
	if dst.Get(50) {
		t.Error("期望 dst[50] = false（超出实际复制范围）")
	}
}

// TestCopyFrom_CountZero 测试 count=0 时为空操作。
func TestCopyFrom_CountZero(t *testing.T) {
	src := NewBitmap(64)
	src.Set(0)
	src.Set(63)

	dst := NewBitmap(64)
	dst.CopyFrom(src, 0, 0)

	// 目标位图应保持全零
	if !dst.IsEmpty() {
		t.Error("期望 count=0 时目标位图为空")
	}
}

// TestCopyFrom_SrcIdxOutOfBounds 测试 srcIdx 超出源位图 word 范围时提前退出。
func TestCopyFrom_SrcIdxOutOfBounds(t *testing.T) {
	// 源位图只有 1 个 word（64 位）
	src := NewBitmap(64)
	src.Set(0)

	// 目标位图有 4 个 word（256 位），但 count=200
	dst := NewBitmap(256)
	dst.CopyFrom(src, 0, 200)

	// srcIdx >= len(src.bits) 时应 break
	// 只有第一个 word 被复制
	if !dst.Get(0) {
		t.Error("期望 dst[0] = true")
	}
	// dst[64] 应该为 false（源位图没有第二个 word）
	if dst.Get(64) {
		t.Error("期望 dst[64] = false（源位图 word 不足）")
	}
}

// TestCopyFrom_DstIdxOutOfBounds 测试 i 超出目标位图 word 范围时提前退出。
func TestCopyFrom_DstIdxOutOfBounds(t *testing.T) {
	src := NewBitmap(256)
	for i := uint32(0); i < 256; i++ {
		src.Set(i)
	}

	// 目标位图只有 1 个 word（64 位）
	dst := NewBitmap(64)
	dst.CopyFrom(src, 0, 200)

	// i >= len(dst.bits) 时应 break
	for i := uint32(0); i < 64; i++ {
		if !dst.Get(i) {
			t.Errorf("期望 dst[%d] = true", i)
		}
	}
}

// TestCopyFrom_MisalignedPartialLastWord 测试未对齐源且最后一轮截断的组合场景。
func TestCopyFrom_MisalignedPartialLastWord(t *testing.T) {
	// 源位图：192 位
	src := NewBitmap(192)
	for i := uint32(0); i < 192; i++ {
		src.Set(i)
	}

	// 从 srcStart=3 开始复制 65 位
	// srcBitOff=3，需要跨 word 拼接
	// 最后一轮 remaining=1 < 64，需要截断
	dst := NewBitmap(128)
	dst.CopyFrom(src, 3, 65)

	// dst[0] 对应 src[3]
	if !dst.Get(0) {
		t.Error("期望 dst[0] = true（来自 src[3]）")
	}
	// dst[64] 对应 src[67]
	if !dst.Get(64) {
		t.Error("期望 dst[64] = true（来自 src[67]）")
	}
	// dst[65] 超出 count 范围
	if dst.Get(65) {
		t.Error("期望 dst[65] = false（超出复制范围）")
	}
}

// TestCopyFrom_MisalignedLastWordNoNextWord 测试未对齐源且最后一轮时 srcIdx+1 超出范围。
func TestCopyFrom_MisalignedLastWordNoNextWord(t *testing.T) {
	// 源位图只有 2 个 word（128 位）
	src := NewBitmap(128)
	src.Set(63)
	src.Set(64)

	// 从 srcStart=1 开始复制 127 位
	// 第二轮（i=1）时 srcIdx=1，srcIdx+1=2 >= len(src.bits)=2
	// 所以不会尝试读取 src.bits[2]
	dst := NewBitmap(128)
	dst.CopyFrom(src, 1, 127)

	// dst[62] 对应 src[63]
	if !dst.Get(62) {
		t.Error("期望 dst[62] = true（来自 src[63]）")
	}
	// dst[63] 对应 src[64]
	if !dst.Get(63) {
		t.Error("期望 dst[63] = true（来自 src[64]）")
	}
}
