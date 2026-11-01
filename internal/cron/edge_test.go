package cron

import (
	"testing"
	"time"
)

// 各种畸形表达式必须报错而不是静默接受。
func TestScheduleRejectsMalformed(t *testing.T) {
	bad := []string{
		"",
		"   ",
		"* * *",
		"* * * * * *",    // 6 段
		"60 * * * *",     // 分钟越界
		"* 24 * * *",     // 小时越界
		"* * 32 * *",     // 日越界
		"* * * 13 *",     // 月越界
		"* * * * 8",      // 周越界
		"*/0 * * * *",    // 步长 0
		"*/abc * * * *",  // 步长非数字
		"1-70/5 * * * *", // 区间越界
		"a * * * *",      // 非数字
		"5-3 * * * *",    // 反向区间
	}
	for _, e := range bad {
		if _, err := Parse(e); err == nil {
			t.Errorf("Parse(%q) 应报错，但通过了", e)
		}
	}
}

// 合法表达式必须能算出合理的下次时间。
func TestScheduleNextValid(t *testing.T) {
	base := time.Date(2026, 3, 1, 10, 30, 0, 0, time.Local)
	cases := []struct {
		expr     string
		wantNext string
	}{
		{"* * * * *", "2026-03-01 10:31"},
		{"*/15 * * * *", "2026-03-01 10:45"},
		{"0 9 * * *", "2026-03-02 09:00"},
		{"30 10 * * *", "2026-03-02 10:30"}, // 当天 10:30 已过，次日
		{"0 0 1 * *", "2026-04-01 00:00"},
	}
	for _, c := range cases {
		s, err := Parse(c.expr)
		if err != nil {
			t.Errorf("Parse(%q) 意外报错: %v", c.expr, err)
			continue
		}
		next := s.Next(base)
		if next.IsZero() {
			t.Errorf("Next(%q) 返回零值", c.expr)
			continue
		}
		if got := next.Format("2006-01-02 15:04"); got != c.wantNext {
			t.Errorf("Next(%q) = %s，期望 %s", c.expr, got, c.wantNext)
		}
	}
}

// Next 必须有 5 年上限，永不死循环（如 2 月 30 日这种永不成立的表达式）。
func TestScheduleNextTerminates(t *testing.T) {
	base := time.Now()
	for _, e := range []string{"0 0 30 2 *", "0 0 31 2 *", "0 0 29 2 *"} {
		s, err := Parse(e)
		if err != nil {
			continue // 解析阶段就拒绝也算合理
		}
		done := make(chan time.Time, 1)
		go func() { done <- s.Next(base) }()
		select {
		case <-done:
			// 正常返回（可能是零值）
		case <-time.After(10 * time.Second):
			t.Fatalf("Next(%q) 超过 10 秒未返回，疑似死循环", e)
		}
	}
}
