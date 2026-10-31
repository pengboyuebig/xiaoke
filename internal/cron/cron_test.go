package cron

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Next 的黄金表：每个用例验证「给定时刻之后的下一次触发」。
func TestNextGolden(t *testing.T) {
	loc := time.Local
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04:05", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		expr  string
		after string
		want  string
	}{
		{"*/5 * * * *", "2026-10-31 10:02:00", "2026-10-31 10:05:00"},
		{"*/5 * * * *", "2026-10-31 10:05:00", "2026-10-31 10:10:00"},       // 不含当前刻
		{"30 9 * * *", "2026-10-31 10:00:00", "2026-11-01 09:30:00"},        // 明天 9:30
		{"0 12 1 * *", "2026-10-31 10:00:00", "2026-11-01 12:00:00"},        // 每月 1 日
		{"0 12 * * mon", "2026-10-31 10:00:00", "2026-11-02 12:00:00"},      // 10-31 是周六 → 下周一
		{"0 0 * * 0", "2026-10-31 10:00:00", "2026-11-01 00:00:00"},         // 周日
		{"0 9-17 * * *", "2026-10-31 15:30:00", "2026-10-31 16:00:00"},      // 区间
		{"0 9,18 * * *", "2026-10-31 10:00:00", "2026-10-31 18:00:00"},      // 列表
		{"*/15 1-15/2 1 * *", "2026-10-31 10:00:00", "2026-11-01 01:00:00"}, // 组合：1 日（1-15/2 含 1）1 点
		{"59 23 31 12 *", "2026-10-31 10:00:00", "2026-12-31 23:59:00"},     // 平安夜前夜
	}
	for _, c := range cases {
		sched, err := Parse(c.expr)
		if err != nil {
			t.Fatalf("%q 解析失败: %v", c.expr, err)
		}
		got := sched.Next(at(c.after))
		want := at(c.want)
		if got.IsZero() || !got.Equal(want) {
			t.Fatalf("%q after %s: want %s got %s", c.expr, c.after, want, got)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, expr := range []string{
		"", "*/5 * * *", // 段数
		"60 * * * *", "*/0 * * * *", "1-0 * * * *", // 值/步长/区间
		"0 0 31 2 *", // 2 月 31 日：日历不存在
	} {
		if _, err := Parse(expr); err == nil {
			t.Fatalf("%q 应解析失败", expr)
		}
	}
}

// Due：到期领取（NextRun 推进、计数、MaxRuns 停用）。
func TestDueClaimsAndAdvances(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "automations.json"))
	now := time.Now()
	a, err := s.Create("每日站会", "30 9 * * *", "汇总昨天进展", "", 2, now)
	if err != nil {
		t.Fatal(err)
	}
	// 未到期
	if got := s.Due(now); len(got) != 0 {
		t.Fatalf("未到期不应触发: %d", len(got))
	}
	// 到期：领取一次
	fire := a.NextRunAt
	got := s.Due(fire.Add(time.Minute))
	if len(got) != 1 || got[0].RunCount != 1 {
		t.Fatalf("到期应领取 1 次: %+v", got)
	}
	// 推进到下一次
	stored, _ := s.Get(a.ID)
	if !stored.NextRunAt.After(fire) {
		t.Fatalf("NextRun 应推进: %v", stored.NextRunAt)
	}
	// 再跑一次到 MaxRuns=2 → 自动停用
	got2 := s.Due(stored.NextRunAt.Add(time.Minute))
	if len(got2) != 1 {
		t.Fatal("第二次应触发")
	}
	if a3, _ := s.Get(a.ID); a3.Enabled {
		t.Fatal("跑满 MaxRuns 应自动停用")
	}
	if got3 := s.Due(time.Now().Add(48 * time.Hour)); len(got3) != 0 {
		t.Fatal("停用后不应再触发")
	}
}

// 上限与防重复。
func TestCreateLimits(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "automations.json"))
	now := time.Now()
	for i := 0; i < MaxAutomations; i++ {
		if _, err := s.Create("任务", "0 9 * * *", "做点事", "", 0, now); err != nil {
			t.Fatalf("第 %d 条不应失败: %v", i+1, err)
		}
	}
	if _, err := s.Create("超限", "0 9 * * *", "做点事", "", 0, now); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("超限应报错: %v", err)
	}
	// 删一条后能再建
	if err := s.Delete("a_nope"); err == nil {
		t.Fatal("不存在的 id 应报错")
	}
	list := s.List()
	if err := s.Delete(list[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("再建", "0 9 * * *", "做点事", "", 0, now); err != nil {
		t.Fatalf("删除后应可再建: %v", err)
	}
}

// Update：改表达式重算 NextRun；启停切换。
func TestUpdateAndToggle(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "automations.json"))
	now := time.Now()
	a, _ := s.Create("改名前", "30 9 * * *", "做点事", "", 0, now)

	disabled := false
	if _, err := s.Update(a.ID, "改名后", "0 12 * * *", "", -1, &disabled, now); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(a.ID)
	if got.Title != "改名后" || got.Enabled || got.Cron != "0 12 * * *" {
		t.Fatalf("更新未生效: %+v", got)
	}
	enabled := true
	if _, err := s.Update(a.ID, "", "", "", -1, &enabled, now); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.Get(a.ID)
	if !got2.Enabled || got2.NextRunAt.IsZero() {
		t.Fatalf("重新启用应重算 NextRun: %+v", got2)
	}
	if _, err := s.Update(a.ID, "", "bad", "", -1, nil, now); err == nil {
		t.Fatal("坏表达式应拒绝")
	}
	if _, err := s.Update("a_nope", "x", "", "", -1, nil, now); err == nil {
		t.Fatal("不存在的 id 应报错")
	}
}

// 持久化：重开还原。
func TestPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "automations.json")
	s, _ := Open(path)
	now := time.Now()
	a, _ := s.Create("持久化", "*/10 * * * *", "做点事", "proj1", 5, now)
	s2, _ := Open(path)
	got, ok := s2.Get(a.ID)
	if !ok || got.Title != "持久化" || got.ProjectID != "proj1" || got.MaxRuns != 5 {
		t.Fatalf("重开应还原: %+v", got)
	}
}

// 一次性延迟任务（once:N）：创建即定 NextRun、到期领取、跑完自动停用。
func TestOnceDelayLifecycle(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "a.json"))
	now := time.Now()
	a, err := s.Create("延迟任务", "once:30", "做点事", "", 0, now)
	if err != nil {
		t.Fatalf("once: 创建应成功: %v", err)
	}
	if want := now.Add(30 * time.Minute); !a.NextRunAt.Equal(want) {
		t.Fatalf("NextRun 应为 +30min: got %v want %v", a.NextRunAt, want)
	}
	if a.MaxRuns != 1 {
		t.Fatalf("一次性任务应自动 MaxRuns=1: %d", a.MaxRuns)
	}
	// 未到期不触发
	if got := s.Due(now.Add(time.Minute)); len(got) != 0 {
		t.Fatalf("未到期不应触发: %d", len(got))
	}
	// 到期领取：RunCount=1、停用、NextRun 清零
	got := s.Due(a.NextRunAt.Add(time.Minute))
	if len(got) != 1 || got[0].RunCount != 1 {
		t.Fatalf("到期应领取: %+v", got)
	}
	stored, _ := s.Get(a.ID)
	if stored.Enabled || !stored.NextRunAt.IsZero() {
		t.Fatalf("跑完应停用且清 NextRun: %+v", stored)
	}
	// 再也不触发
	if got2 := s.Due(now.Add(24 * time.Hour)); len(got2) != 0 {
		t.Fatal("停用后不应再触发")
	}
	// 非法延迟
	if _, err := s.Create("坏", "once:0", "x", "", 0, now); err == nil {
		t.Fatal("once:0 应拒绝")
	}
	if _, err := s.Create("坏2", "once:99999999", "x", "", 0, now); err == nil {
		t.Fatal("超范围延迟应拒绝")
	}
}
