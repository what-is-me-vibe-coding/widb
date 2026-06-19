package storage

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestDeserializeSegmentInvalidFooterOffsetTooSmall 验证损坏的 footerOffset（< 4）
// 返回错误而非触发 slice 越界 panic。
func TestDeserializeSegmentInvalidFooterOffsetTooSmall(t *testing.T) {
	data := buildValidSegmentBytes(t)
	// 末尾 8 字节是 footerOffset，置 0 使 data[footerOffset-4:] 越界。
	footerOffsetPos := len(data) - 8
	binary.LittleEndian.PutUint64(data[footerOffsetPos:], 0)

	_, err := DeserializeSegment(data)
	if err == nil {
		t.Fatal("expected error for footerOffset=0, got nil")
	}
	if !strings.Contains(err.Error(), "invalid footer offset") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestDeserializeSegmentInvalidFooterOffsetTooLarge 验证 footerOffset 超过尾部偏移区
// 返回错误而非越界 panic。
func TestDeserializeSegmentInvalidFooterOffsetTooLarge(t *testing.T) {
	data := buildValidSegmentBytes(t)
	footerOffsetPos := len(data) - 8
	// 设置一个远超数据长度的非法偏移。
	binary.LittleEndian.PutUint64(data[footerOffsetPos:], uint64(len(data)+1024))

	_, err := DeserializeSegment(data)
	if err == nil {
		t.Fatal("expected error for oversized footerOffset, got nil")
	}
	if !strings.Contains(err.Error(), "invalid footer offset") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestDeserializeSegmentTruncated 验证截断到最小长度以下时返回错误。
func TestDeserializeSegmentTruncated(t *testing.T) {
	// 22 字节是 DeserializeSegment 接受的最小长度边界。
	_, err := DeserializeSegment(make([]byte, 21))
	if err == nil {
		t.Fatal("expected error for too-short data")
	}
}

// buildValidSegmentBytes 构造一个合法的段字节流，供损坏测试使用。
func buildValidSegmentBytes(t *testing.T) []byte {
	t.Helper()
	rowCount := uint32(8)
	ints := make([]int64, rowCount)
	for i := uint32(0); i < rowCount; i++ {
		ints[i] = int64(i)
	}
	enc, err := EncodeColumn(common.TypeInt64, ints, rowCount, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	builder := NewSegmentBuilder(100, "k0", "k7")
	builder.AddEncodedColumn(enc)
	seg, err := builder.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	data, err := seg.Serialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	// 确保基准数据可正常反序列化。
	if _, err := DeserializeSegment(data); err != nil {
		t.Fatalf("baseline deserialize failed: %v", err)
	}
	return data
}

// TestDecodePlainStringOffsetsTooShort 验证 Offsets 长度不足时返回错误而非越界 panic。
func TestDecodePlainStringOffsetsTooShort(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeString,
		RowCount: 3,
		Data:     []byte("abc"),
		// RowCount=3 需要 4 个 offset，仅提供 2 个。
		Offsets: []uint32{0, 1},
	}
	_, _, err := decodePlain(enc)
	if err == nil {
		t.Fatal("expected error for short offsets")
	}
	if !strings.Contains(err.Error(), "offsets length") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestDecodePlainStringOffsetOutOfRange 验证 offset 超出 Data 范围时返回错误。
func TestDecodePlainStringOffsetOutOfRange(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeString,
		RowCount: 1,
		Data:     []byte("ab"),
		// end=100 远超 Data 长度。
		Offsets: []uint32{0, 100},
	}
	_, _, err := decodePlain(enc)
	if err == nil {
		t.Fatal("expected error for out-of-range offset")
	}
	if !strings.Contains(err.Error(), "invalid string offset") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestDecodePlainStringStartGreaterThanEnd 验证 start > end 时返回错误。
func TestDecodePlainStringStartGreaterThanEnd(t *testing.T) {
	enc := &EncodedColumn{
		Encoding: EncodingPlain,
		Type:     common.TypeString,
		RowCount: 1,
		Data:     []byte("ab"),
		Offsets:  []uint32{5, 2},
	}
	_, _, err := decodePlain(enc)
	if err == nil {
		t.Fatal("expected error for start > end")
	}
	if !strings.Contains(err.Error(), "invalid string offset") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestBlockCacheRejectOversizedEntry 验证单条目超过容量时被拒绝，
// 且 used <= capacity 不变式保持，缓存不会陷入病态。
func TestBlockCacheRejectOversizedEntry(t *testing.T) {
	// 容量很小，单条目超过它。
	cache := NewBlockCache(64)
	key := CacheKey{SegmentID: 1, ColumnIdx: 0}
	// []int64 长度 16 => 16*8 + overhead(64) = 192 > 64。
	cache.put(key, decodedColumn{data: make([]int64, 16), typ: common.TypeInt64})

	stats := cache.Stats()
	if stats.Entries != 0 {
		t.Fatalf("oversized entry should be rejected, got entries=%d", stats.Entries)
	}
	if stats.Size > stats.Capacity {
		t.Fatalf("invariant violated: used=%d > capacity=%d", stats.Size, stats.Capacity)
	}
	if _, ok := cache.get(key); ok {
		t.Fatal("rejected entry should not be retrievable")
	}
}

// TestBlockCacheOversizedDoesNotEvictExisting 验证拒绝超大条目时不会驱逐已有有效条目。
func TestBlockCacheOversizedDoesNotEvictExisting(t *testing.T) {
	cache := NewBlockCache(200)
	small := CacheKey{SegmentID: 1, ColumnIdx: 0}
	// []int64{1,2,3} => 3*8 + 64 = 88 <= 200。
	cache.put(small, decodedColumn{data: []int64{1, 2, 3}, typ: common.TypeInt64})

	big := CacheKey{SegmentID: 1, ColumnIdx: 1}
	// 超大条目应被拒绝，且不影响 small。
	cache.put(big, decodedColumn{data: make([]int64, 100), typ: common.TypeInt64})

	if _, ok := cache.get(small); !ok {
		t.Fatal("existing small entry must remain after rejecting oversized entry")
	}
	if _, ok := cache.get(big); ok {
		t.Fatal("oversized entry should not be cached")
	}
	stats := cache.Stats()
	if stats.Size > stats.Capacity {
		t.Fatalf("invariant violated: used=%d > capacity=%d", stats.Size, stats.Capacity)
	}
}

// TestBlockCacheUpdateToOversizedEvictsSelf 验证已存在条目更新为超大尺寸时被自我淘汰，
// 维持 used <= capacity 不变式。
func TestBlockCacheUpdateToOversizedEvictsSelf(t *testing.T) {
	cache := NewBlockCache(200)
	key := CacheKey{SegmentID: 1, ColumnIdx: 0}
	cache.put(key, decodedColumn{data: []int64{1, 2, 3}, typ: common.TypeInt64})
	// 更新为超大尺寸：应自我淘汰。
	cache.put(key, decodedColumn{data: make([]int64, 100), typ: common.TypeInt64})

	stats := cache.Stats()
	if stats.Entries != 0 {
		t.Fatalf("updated oversized entry should be evicted, got entries=%d", stats.Entries)
	}
	if stats.Size > stats.Capacity {
		t.Fatalf("invariant violated: used=%d > capacity=%d", stats.Size, stats.Capacity)
	}
	if _, ok := cache.get(key); ok {
		t.Fatal("self-evicted entry should not be retrievable")
	}
}
