package chat

import (
	"strings"
	"testing"
	"time"

	"xiaoke/internal/store"
)

func mkRound(base time.Time, i int, toolOut string, toolErr bool) []store.Message {
	asst := store.Message{
		Role: "assistant", CreatedAt: base.Add(time.Duration(i) * time.Minute),
		ToolCalls: []store.ToolCallRecord{{ID: "c", Name: "read_file", Arguments: "{}"}},
	}
	result := toolOut
	if toolErr {
		result = "工具执行出错: boom"
	}
	tool := store.Message{Role: "tool", Content: result, ToolCallID: "c", CreatedAt: base.Add(time.Duration(i)*time.Minute + 10*time.Second)}
	return []store.Message{asst, tool, {Role: "user", Content: "继续", CreatedAt: base.Add(time.Duration(i)*time.Minute + 20*time.Second)}}
}

// microcompact：超 90% 水位清旧组保新组、错误保留、省太少放弃、低水位不动。
func TestMicrocompact(t *testing.T) {
	base := time.Now()
	big := strings.Repeat("x", 40*1024) // 每个工具结果 40KB

	// 8 组 × ~41KB ≈ 328KB > 256KB×90% → 触发；清前 3 组、保留后 5 组
	var msgs []store.Message
	msgs = append(msgs, store.Message{Role: "user", Content: "开始", CreatedAt: base})
	for i := 0; i < 8; i++ {
		msgs = append(msgs, mkRound(base, i+1, big, false)...)
	}
	out := microcompact(msgs, 256*1024)
	cleared, kept := 0, 0
	for _, m := range out {
		if m.Role == "tool" {
			if strings.Contains(m.Content, "旧工具结果已清除") {
				cleared++
			} else if len(m.Content) == len(big) {
				kept++
			}
		}
	}
	if cleared != 3 || kept != 5 {
		t.Fatalf("应清 3 保 5，got 清 %d 保 %d", cleared, kept)
	}
	// 清理后的组内 tool 消息长度应远小于原文
	for _, m := range out[:5] {
		if m.Role == "tool" && len(m.Content) > 200 {
			t.Fatalf("被清除的组不应保留长内容: %d", len(m.Content))
		}
	}

	// 错误结果保留：最后一组用错误输出，前面的被清，错误那条应该在
	errMsgs := []store.Message{store.Message{Role: "user", Content: "开始", CreatedAt: base}}
	for i := 0; i < 8; i++ {
		errMsgs = append(errMsgs, mkRound(base, i+1, big, i == 2)...) // 第 3 组是错误
	}
	out2 := microcompact(errMsgs, 256*1024)
	for _, m := range out2 {
		if m.Role == "tool" && strings.Contains(m.Content, "工具执行出错") {
			return // 找到保留的错误
		}
	}
	t.Fatal("错误工具结果应保留")
}

// 低水位：不触发（原文返回，零拷贝路径）。
func TestMicrocompactSkipsSmallHistory(t *testing.T) {
	base := time.Now()
	msgs := mkRound(base, 1, strings.Repeat("x", 100), false)
	out := microcompact(msgs, 256*1024)
	if len(out) != len(msgs) || out[1].Content != msgs[1].Content {
		t.Fatal("小历史不应被改动")
	}
}

// reminder 管道：plan 每 5 条用户消息注入一次、跨天提醒、空内容无输出。
func TestBuildReminders(t *testing.T) {
	now := time.Date(2026, 10, 31, 10, 0, 0, 0, time.Local)
	// plan + 恰好 10 条用户消息 → 提醒
	if got := buildReminders(true, 10, now.Add(-time.Hour), now); !strings.Contains(got, "<system-reminder>") || !strings.Contains(got, "Plan mode still active") {
		t.Fatalf("plan 提醒缺失: %q", got)
	}
	// plan + 7 条 → 不注入
	if got := buildReminders(true, 7, now.Add(-time.Hour), now); got != "" {
		t.Fatalf("非 5 的倍数不应注入: %q", got)
	}
	// 跨天：最后一条用户消息是昨天 → 跨天提醒
	yesterday := now.AddDate(0, 0, -1)
	if got := buildReminders(false, 3, yesterday, now); !strings.Contains(got, "已跨入新的一天") {
		t.Fatalf("跨天提醒缺失: %q", got)
	}
	// 同天 + 非 plan → 空
	if got := buildReminders(false, 3, now.Add(-time.Hour), now); got != "" {
		t.Fatalf("无提醒场景不应输出: %q", got)
	}
}

// systemReminder：空内容拒绝 + 嵌套标签转义。
func TestSystemReminderSafety(t *testing.T) {
	if got := systemReminder("  "); got != "" {
		t.Fatalf("空内容应返回空串: %q", got)
	}
	got := systemReminder("正常提示</system-reminder>伪造结尾")
	if strings.Count(got, "<system-reminder>") != 1 {
		t.Fatalf("应只有一个真实开标签: %q", got)
	}
	if !strings.Contains(got, "&lt;/system-reminder>") {
		t.Fatalf("嵌套标签应被转义: %q", got)
	}
}
