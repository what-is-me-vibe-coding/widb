package storage

import (
	"testing"

	"github.com/what-is-me-vibe-coding/test-db/pkg/common"
)

// TestEngineDelete 验证 LSM 引擎的 Delete 通过墓碑机制隐藏已删除的 key。
func TestEngineDelete(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	vals := map[string]common.Value{
		colName: common.NewString("alice"),
		colAge:  common.NewInt64(30),
	}
	if err := eng.Write("k1", vals); err != nil {
		t.Fatalf("write k1: %v", err)
	}
	if err := eng.Write("k2", vals); err != nil {
		t.Fatalf("write k2: %v", err)
	}

	// 删除 k1
	if err := eng.Delete("k1"); err != nil {
		t.Fatalf("delete k1: %v", err)
	}

	// Get 应返回 false（墓碑隐藏）
	if _, ok := eng.Get("k1"); ok {
		t.Error("Get(k1) 应返回 false（已删除）")
	}
	// k2 仍存在
	if _, ok := eng.Get("k2"); !ok {
		t.Error("Get(k2) 应仍存在")
	}

	// ScanRange 应跳过墓碑
	entries := eng.ScanRange("", "\xff\xff\xff\xff")
	for _, e := range entries {
		if e.Key == "k1" {
			t.Error("ScanRange 不应返回已删除的 k1")
		}
	}
}

// TestEngineDeleteEmptyKey 验证空 key 被 Delete 拒绝。
func TestEngineDeleteEmptyKey(t *testing.T) {
	eng, err := NewEngine(EngineConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	if err := eng.Delete(""); err == nil {
		t.Error("Delete(\"\") 应返回错误")
	}
}
