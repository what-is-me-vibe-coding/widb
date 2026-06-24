package server

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestNewSlowQueryLogDefaults 验证默认容量与零阈值的禁用行为。
func TestNewSlowQueryLogDefaults(t *testing.T) {
	l := NewSlowQueryLog(0, 0)
	if l.Enabled() {
		t.Fatalf("threshold<=0 时应禁用")
	}
	if l.Capacity() != 100 {
		t.Fatalf("默认 capacity = %d, 期望 100", l.Capacity())
	}
	if l.Threshold() != 0 {
		t.Fatalf("默认 threshold = %v, 期望 0", l.Threshold())
	}
	// 禁用状态下 Record 应为 no-op
	l.Record(time.Second, SlowQuerySourceHTTP, "SELECT 1", "")
	if got := l.Snapshot(); len(got) != 0 {
		t.Fatalf("禁用日志应不记录任何条目，实际 %d 条", len(got))
	}
}

// TestSlowQueryLogEnabledButBelowThreshold 验证启用但未超阈值的查询不进入日志。
func TestSlowQueryLogEnabledButBelowThreshold(t *testing.T) {
	l := NewSlowQueryLog(100*time.Millisecond, 10)
	if !l.Enabled() {
		t.Fatalf("threshold>0 时应启用")
	}
	l.Record(50*time.Millisecond, SlowQuerySourceHTTP, "SELECT 1", "")
	if got := l.Snapshot(); len(got) != 0 {
		t.Fatalf("duration<threshold 时不应记录，实际 %d 条", len(got))
	}
}

// TestSlowQueryLogRecordOrder 验证 Snapshot 始终按"最新优先"返回。
func TestSlowQueryLogRecordOrder(t *testing.T) {
	l := NewSlowQueryLog(10*time.Millisecond, 10)
	for i := 0; i < 5; i++ {
		l.Record(20*time.Millisecond, SlowQuerySourceHTTP, "Q", "")
		time.Sleep(2 * time.Millisecond) // 保证时间戳可分辨
	}
	got := l.Snapshot()
	if len(got) != 5 {
		t.Fatalf("Snapshot 长度 = %d, 期望 5", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].Timestamp.After(got[i-1].Timestamp) {
			t.Fatalf("Snapshot 应按时间倒序，第 %d 条比第 %d 条更新", i, i-1)
		}
	}
}

// TestSlowQueryLogRingBufferOverflow 验证环形缓冲容量到达上限后覆盖最旧记录。
func TestSlowQueryLogRingBufferOverflow(t *testing.T) {
	l := NewSlowQueryLog(time.Millisecond, 3)
	for i := 0; i < 5; i++ {
		l.Record(5*time.Millisecond, SlowQuerySourceHTTP, "Q", "")
		time.Sleep(time.Millisecond)
	}
	got := l.Snapshot()
	if len(got) != 3 {
		t.Fatalf("环形缓冲大小应被裁剪到 3，实际 %d", len(got))
	}
	// 最新写入的 3 条应保留；最早 2 条（索引 0/1）应被覆盖
	// 这里通过检查时间单调性间接验证：3 条记录应保持时间倒序
	for i := 1; i < len(got); i++ {
		if got[i].Timestamp.After(got[i-1].Timestamp) {
			t.Fatalf("覆盖后顺序错乱: idx=%d", i)
		}
	}
}

// TestSlowQueryLogRecordError 验证错误信息正确写入 Error 字段。
func TestSlowQueryLogRecordError(t *testing.T) {
	l := NewSlowQueryLog(time.Millisecond, 10)
	l.Record(10*time.Millisecond, SlowQuerySourceTCP, "BAD SQL", "syntax error at line 1")
	got := l.Snapshot()
	if len(got) != 1 {
		t.Fatalf("长度 = %d, 期望 1", len(got))
	}
	if got[0].Error != "syntax error at line 1" {
		t.Fatalf("Error = %q, 期望 %q", got[0].Error, "syntax error at line 1")
	}
	if got[0].SQL != "BAD SQL" {
		t.Fatalf("SQL = %q, 期望 %q", got[0].SQL, "BAD SQL")
	}
}

// TestSlowQueryLogTruncatesLongSQL 验证过长 SQL 会被截断并追加标记。
func TestSlowQueryLogTruncatesLongSQL(t *testing.T) {
	l := NewSlowQueryLog(time.Millisecond, 10)
	long := strings.Repeat("x", slowQueryMaxSQLLength+200)
	l.Record(5*time.Millisecond, SlowQuerySourceHTTP, long, "")
	got := l.Snapshot()
	if len(got) != 1 {
		t.Fatalf("长度 = %d, 期望 1", len(got))
	}
	if len(got[0].SQL) != slowQueryMaxSQLLength {
		t.Fatalf("截断后长度 = %d, 期望 %d", len(got[0].SQL), slowQueryMaxSQLLength)
	}
	if !strings.HasSuffix(got[0].SQL, "... (truncated)") {
		t.Fatalf("缺少截断标记: %q", got[0].SQL[slowQueryMaxSQLLength-20:])
	}
}

// TestSlowQueryLogNilSafe 验证对 nil 接收者的所有方法均为安全 no-op。
func TestSlowQueryLogNilSafe(t *testing.T) {
	var l *SlowQueryLog
	if l.Enabled() {
		t.Fatalf("nil Enabled 应为 false")
	}
	if l.Threshold() != 0 || l.Capacity() != 0 {
		t.Fatalf("nil 阈值/容量应为 0")
	}
	l.Record(time.Second, SlowQuerySourceHTTP, "x", "")
	l.Reset()
	if got := l.Snapshot(); len(got) != 0 {
		t.Fatalf("nil Snapshot 应为空，实际 %d 条", len(got))
	}
}

// TestSlowQueryLogConcurrentRecord 验证并发记录下的线程安全与最终一致性。
func TestSlowQueryLogConcurrentRecord(t *testing.T) {
	l := NewSlowQueryLog(time.Microsecond, 200)
	const goroutines = 16
	const perG = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				l.Record(time.Millisecond, SlowQuerySourceHTTP, "Q", "")
			}
		}(g)
	}
	wg.Wait()
	got := l.Snapshot()
	// 容量 200 共写入 320 条，应被裁剪到 200
	if len(got) != 200 {
		t.Fatalf("并发写入后 Snapshot 长度 = %d, 期望 200", len(got))
	}
	// 验证时间单调性
	for i := 1; i < len(got); i++ {
		if got[i].Timestamp.After(got[i-1].Timestamp) {
			t.Fatalf("并发场景下顺序错乱: idx=%d", i)
		}
	}
}

// TestSlowQueryLogResetClears 验证 Reset 后 Snapshot 为空且能继续记录。
func TestSlowQueryLogResetClears(t *testing.T) {
	l := NewSlowQueryLog(time.Millisecond, 10)
	l.Record(5*time.Millisecond, SlowQuerySourceHTTP, "Q1", "")
	l.Record(5*time.Millisecond, SlowQuerySourceHTTP, "Q2", "")
	if got := l.Snapshot(); len(got) != 2 {
		t.Fatalf("Reset 前长度 = %d, 期望 2", len(got))
	}
	l.Reset()
	if got := l.Snapshot(); len(got) != 0 {
		t.Fatalf("Reset 后长度 = %d, 期望 0", len(got))
	}
	// Reset 后 head 已重置，新记录应从 0 开始
	l.Record(5*time.Millisecond, SlowQuerySourceHTTP, "Q3", "")
	if got := l.Snapshot(); len(got) != 1 {
		t.Fatalf("Reset 后再写入长度 = %d, 期望 1", len(got))
	}
}

// TestTruncateSQLNoop 验证短 SQL 不被修改、空 max 不截断。
func TestTruncateSQLNoop(t *testing.T) {
	if got := truncateSQL("short", 100); got != "short" {
		t.Fatalf("短 SQL 不应被修改，实际 %q", got)
	}
	if got := truncateSQL("xxx", 0); got != "xxx" {
		t.Fatalf("max=0 时应原样返回，实际 %q", got)
	}
	// max 小于 suffix 长度时只能返回 suffix（无法在 max 内放下有意义前缀）
	got := truncateSQL("abcdef", 2)
	if !strings.HasSuffix(got, "... (truncated)") {
		t.Fatalf("超小 max 截断应保留 suffix 标记: %q", got)
	}
}

// TestSlowQueryLogRecordErrorInterface 验证传入 error 类型时也能正确记录。
// 间接保证 Record 的 errMsg 形参在调用方传 errors.New(...) 时仍能工作。
func TestSlowQueryLogRecordErrorInterface(t *testing.T) {
	l := NewSlowQueryLog(time.Millisecond, 10)
	err := errors.New("boom")
	l.Record(5*time.Millisecond, SlowQuerySourcePGWire, "X", err.Error())
	got := l.Snapshot()
	if len(got) != 1 || got[0].Error != "boom" {
		t.Fatalf("错误接口记录异常: %+v", got)
	}
}
