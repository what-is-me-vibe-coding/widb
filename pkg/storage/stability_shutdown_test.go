package storage

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWALFunctionalAfterRotateRenameError 验证 WAL 在 maybeRotate 重命名失败后仍然可用。
// 修复前：os.Rename(w.path, rotatedPath) 失败时，newF 文件句柄未关闭（泄漏），
// 且 recoverOpen 后 WAL 可能无法正常使用。修复后通过 logClose(newF) 确保句柄关闭，
// 并通过 recoverOpen 恢复 WAL 可用状态。
func TestWALFunctionalAfterRotateRenameError(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入初始数据
	if err := w.AppendWrite([]byte("initial_data_1")); err != nil {
		t.Fatalf("AppendWrite 初始数据失败: %v", err)
	}
	if err := w.AppendWrite([]byte("initial_data_2")); err != nil {
		t.Fatalf("AppendWrite 初始数据失败: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync 失败: %v", err)
	}

	// 设置极小的 maxSize 使下次写入触发轮转
	w.maxSize = 1

	// 在 .prev 路径创建非空目录，使 Rename(walPath, .prev) 失败
	prevPath := walPath + ".prev"
	if err := os.MkdirAll(prevPath, 0755); err != nil {
		t.Fatalf("创建 .prev 目录失败: %v", err)
	}
	dummyFile := filepath.Join(prevPath, "dummy")
	if err := os.WriteFile(dummyFile, []byte("x"), 0644); err != nil {
		t.Fatalf("创建 dummy 文件失败: %v", err)
	}
	defer func() { _ = os.RemoveAll(prevPath) }()

	// 尝试写入触发轮转，应返回错误
	err = w.AppendWrite([]byte("trigger_rotate"))
	if err == nil {
		t.Error("期望 AppendWrite 返回轮转错误，但返回 nil")
		_ = w.Close()
		return
	}

	// 清理 .prev 目录，使后续操作不再受影响
	_ = os.RemoveAll(prevPath)
	// 清理可能残留的 .tmp 文件
	_ = os.Remove(walPath + ".tmp")

	// 关闭 WAL
	if err := w.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	// 重新打开 WAL，验证原始数据仍然完整
	w2, records, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}

	if len(records) != 2 {
		t.Fatalf("期望 2 条记录，实际 %d 条", len(records))
	}
	if string(records[0].Payload) != "initial_data_1" {
		t.Errorf("第 1 条记录: %q, 期望 %q", string(records[0].Payload), "initial_data_1")
	}
	if string(records[1].Payload) != "initial_data_2" {
		t.Errorf("第 2 条记录: %q, 期望 %q", string(records[1].Payload), "initial_data_2")
	}

	// 验证恢复后可以继续写入
	if err := w2.AppendWrite([]byte("after_recovery")); err != nil {
		t.Fatalf("恢复后 AppendWrite 失败: %v", err)
	}
	if err := w2.Sync(); err != nil {
		t.Fatalf("Sync 失败: %v", err)
	}
	_ = w2.Close()

	// 再次打开验证所有数据
	w3, records2, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("第二次 OpenWAL 失败: %v", err)
	}
	defer func() { _ = w3.Close() }()

	if len(records2) != 3 {
		t.Fatalf("期望 3 条记录，实际 %d 条", len(records2))
	}
	if string(records2[2].Payload) != "after_recovery" {
		t.Errorf("第 3 条记录: %q, 期望 %q", string(records2[2].Payload), "after_recovery")
	}
}

// TestWALFunctionalAfterRotateCloseOldError 验证 WAL 在 maybeRotate 关闭旧文件失败后仍然可用。
// 修复前：关闭旧文件失败时 newF 文件句柄未关闭（泄漏）。修复后通过 logClose(newF) 确保句柄关闭。
func TestWALFunctionalAfterRotateCloseOldError(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入初始数据
	if err := w.AppendWrite([]byte("data_before_error")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync 失败: %v", err)
	}

	// 设置极小的 maxSize 使下次写入触发轮转
	w.maxSize = 1

	// 预先关闭底层文件，使 old.Close() 在 maybeRotate 中失败
	if err := w.file.Close(); err != nil {
		t.Fatalf("预关闭文件失败: %v", err)
	}

	// 尝试写入触发轮转，应返回错误
	err = w.AppendWrite([]byte("trigger_rotate"))
	if err == nil {
		t.Error("期望 AppendWrite 返回轮转错误，但返回 nil")
		return
	}

	// 清理 .tmp 文件
	_ = os.Remove(walPath + ".tmp")

	// 关闭 WAL（文件已被预关闭，Close 可能返回错误，但不应 panic）
	_ = w.Close()

	// 重新打开 WAL，验证原始数据仍然完整
	w2, records, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("期望 1 条记录，实际 %d 条", len(records))
	}
	if string(records[0].Payload) != "data_before_error" {
		t.Errorf("记录: %q, 期望 %q", string(records[0].Payload), "data_before_error")
	}

	// 验证恢复后可以继续写入
	if err := w2.AppendWrite([]byte("after_close_error")); err != nil {
		t.Fatalf("恢复后 AppendWrite 失败: %v", err)
	}
	_ = w2.Close()
}

// TestWALTruncateAtomicity 验证 WAL Truncate 使用原子重命名方式清空文件后仍然可用。
// 修复前：Truncate 直接创建新文件覆盖旧文件，如果创建失败则旧文件数据丢失。
// 修复后：先创建临时文件，再原子重命名替换，确保在任何步骤失败时旧数据不会丢失。
func TestWALTruncateAtomicity(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入多条记录
	for i := 0; i < 5; i++ {
		if err := w.AppendWrite([]byte("data_before_truncate")); err != nil {
			t.Fatalf("AppendWrite #%d 失败: %v", i, err)
		}
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync 失败: %v", err)
	}

	if w.Size() == 0 {
		t.Fatal("期望 Truncate 前有非零大小")
	}

	// 调用 Truncate 清空 WAL
	if err := w.Truncate(); err != nil {
		t.Fatalf("Truncate 失败: %v", err)
	}

	// 验证 Truncate 后大小为 0
	if w.Size() != 0 {
		t.Errorf("期望 Truncate 后大小为 0，实际 %d", w.Size())
	}

	// 验证 Truncate 后可以继续写入
	if err := w.AppendWrite([]byte("after_truncate_1")); err != nil {
		t.Fatalf("Truncate 后 AppendWrite 失败: %v", err)
	}
	if err := w.AppendWrite([]byte("after_truncate_2")); err != nil {
		t.Fatalf("Truncate 后 AppendWrite 失败: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync 失败: %v", err)
	}

	_ = w.Close()

	// 重新打开验证只有 Truncate 后的记录
	w2, records, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if len(records) != 2 {
		t.Fatalf("期望 2 条记录，实际 %d 条", len(records))
	}
	if string(records[0].Payload) != "after_truncate_1" {
		t.Errorf("第 1 条记录: %q, 期望 %q", string(records[0].Payload), "after_truncate_1")
	}
	if string(records[1].Payload) != "after_truncate_2" {
		t.Errorf("第 2 条记录: %q, 期望 %q", string(records[1].Payload), "after_truncate_2")
	}
}

// TestWALTruncateAtomicity_TempCreateFailure 验证 Truncate 在临时文件创建失败时，
// 原始数据仍然可以恢复。这是原子重命名策略的关键属性：
// 如果新文件创建失败，旧文件不受影响。
func TestWALTruncateAtomicity_TempCreateFailure(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入重要数据
	if err := w.AppendWrite([]byte("important_data_1")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	if err := w.AppendWrite([]byte("important_data_2")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync 失败: %v", err)
	}

	// 在临时文件路径创建目录，使 os.Create(tmpPath) 失败
	tmpPath := walPath + ".tmp"
	if err := os.MkdirAll(tmpPath, 0755); err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpPath) }()

	// Truncate 应失败（无法创建临时文件）
	err = w.Truncate()
	if err == nil {
		t.Error("期望 Truncate 返回错误，但返回 nil")
		_ = w.Close()
		return
	}

	// 清理临时目录
	_ = os.RemoveAll(tmpPath)

	// 验证 WAL 仍然可用（旧文件未受影响）
	if err := w.AppendWrite([]byte("still_works")); err != nil {
		t.Fatalf("Truncate 失败后 AppendWrite 失败: %v", err)
	}
	_ = w.Close()

	// 重新打开验证所有数据完整
	w2, records, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if len(records) != 3 {
		t.Fatalf("期望 3 条记录，实际 %d 条", len(records))
	}
	if string(records[0].Payload) != "important_data_1" {
		t.Errorf("第 1 条记录: %q, 期望 %q", string(records[0].Payload), "important_data_1")
	}
	if string(records[1].Payload) != "important_data_2" {
		t.Errorf("第 2 条记录: %q, 期望 %q", string(records[1].Payload), "important_data_2")
	}
	if string(records[2].Payload) != "still_works" {
		t.Errorf("第 3 条记录: %q, 期望 %q", string(records[2].Payload), "still_works")
	}
}

// TestWALTruncateAtomicity_CloseOldFailure 验证 Truncate 在关闭旧文件失败时，
// 通过 recoverOpen 恢复后原始数据仍然可以恢复。
func TestWALTruncateAtomicity_CloseOldFailure(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	// 写入重要数据
	if err := w.AppendWrite([]byte("persistent_data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync 失败: %v", err)
	}

	// 预先关闭底层文件，使 w.file.Close() 在 Truncate 中失败
	if err := w.file.Close(); err != nil {
		t.Fatalf("预关闭文件失败: %v", err)
	}

	// Truncate 应失败
	err = w.Truncate()
	if err == nil {
		t.Error("期望 Truncate 返回错误，但返回 nil")
		return
	}

	// 清理临时文件
	_ = os.Remove(walPath + ".tmp")

	// recoverOpen 应该已经重新打开了文件
	if w.file == nil {
		t.Fatal("recoverOpen 后 w.file 不应为 nil")
	}

	// 关闭 WAL
	_ = w.Close()

	// 重新打开验证原始数据完整
	w2, records, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL 失败: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if len(records) != 1 {
		t.Fatalf("期望 1 条记录，实际 %d 条", len(records))
	}
	if string(records[0].Payload) != "persistent_data" {
		t.Errorf("记录: %q, 期望 %q", string(records[0].Payload), "persistent_data")
	}
}

// TestWALTruncateAtomicity_NoLeftoverTempFile 验证 Truncate 成功后不会残留临时文件。
// 这是原子重命名策略的附加保证：成功后 .tmp 文件应已被重命名为正式 WAL 文件。
func TestWALTruncateAtomicity_NoLeftoverTempFile(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "test.wal")

	w, err := CreateWAL(walPath)
	if err != nil {
		t.Fatalf("CreateWAL 失败: %v", err)
	}

	if err := w.AppendWrite([]byte("data")); err != nil {
		t.Fatalf("AppendWrite 失败: %v", err)
	}

	if err := w.Truncate(); err != nil {
		t.Fatalf("Truncate 失败: %v", err)
	}

	_ = w.Close()

	// 验证没有残留的临时文件
	if _, err := os.Stat(walPath + ".tmp"); err == nil {
		t.Error("Truncate 成功后不应残留 .tmp 文件")
	}
}
