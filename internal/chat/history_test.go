package chat

import (
	"strings"
	"testing"

	"xiaoke/internal/store"
)

func TestTrimHistoryAlignsToUser(t *testing.T) {
	msgs := []store.Message{
		{Role: "user", Content: "u1"},
		{Role: "assistant", ToolCalls: []store.ToolCallRecord{{ID: "c1", Name: "read_file"}}},
		{Role: "tool", Content: "r1", ToolCallID: "c1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", ToolCalls: []store.ToolCallRecord{{ID: "c2", Name: "write_file"}}},
		{Role: "tool", Content: "r2", ToolCallID: "c2"},
		{Role: "assistant", Content: "a2"},
	}

	got := trimHistory(msgs, 3, 0)
	if len(got) != 3 || got[0].Role != "assistant" {
		t.Fatalf("窗口内无 user 时应保留完整工具轮，实际: %+v", got)
	}
	if got[0].Role == "tool" {
		t.Fatal("历史不能以 tool 消息开头")
	}

	got = trimHistory(msgs, 4, 0)
	if len(got) != 4 || got[0].Role != "user" || got[0].Content != "u2" {
		t.Fatalf("应向前对齐到最近的 user，实际: %+v", got)
	}
}

func TestTrimHistorySkipsLeadingToolWhenNoUser(t *testing.T) {
	msgs := []store.Message{
		{Role: "user", Content: "u1"},
		{Role: "assistant", ToolCalls: []store.ToolCallRecord{{ID: "c1"}}},
		{Role: "tool", Content: "r1", ToolCallID: "c1"},
		{Role: "tool", Content: "r2", ToolCallID: "c1"},
		{Role: "assistant", Content: "a1"},
	}
	got := trimHistory(msgs, 3, 0) // 从 index 2（tool）开始
	if len(got) != 1 || got[0].Role != "assistant" {
		t.Fatalf("窗口以 tool 开头时应跳到后续非 tool 消息，实际: %+v", got)
	}
}

func TestTrimHistoryByteBudget(t *testing.T) {
	big := strings.Repeat("x", 1000)
	msgs := []store.Message{
		{Role: "user", Content: big},
		{Role: "assistant", Content: big},
		{Role: "user", Content: "last"},
		{Role: "assistant", Content: "ok"},
	}
	got := trimHistory(msgs, 0, 1500)
	if len(got) != 2 || got[0].Role != "user" || got[0].Content != "last" {
		t.Fatalf("字节预算应丢弃最旧的大消息并对齐到 user，实际: %+v", got)
	}
}

func TestSanitizeDropsOrphanTool(t *testing.T) {
	msgs := []store.Message{
		{Role: "user", Content: "hi"},
		{Role: "tool", Content: "orphan", ToolCallID: "missing"},
		{Role: "assistant", Content: "answer"},
	}
	got := sanitizeHistory(msgs)
	if len(got) != 2 {
		t.Fatalf("孤立 tool 消息应被丢弃，实际: %+v", got)
	}
	for _, m := range got {
		if m.Role == "tool" {
			t.Fatalf("结果里不应有 tool 消息: %+v", got)
		}
	}
}

func TestSanitizeIncompleteToolCalls(t *testing.T) {
	msgs := []store.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "我来写文件", ToolCalls: []store.ToolCallRecord{
			{ID: "c1", Name: "write_file"},
			{ID: "c2", Name: "write_file"},
		}},
		{Role: "tool", Content: "ok1", ToolCallID: "c1"}, // c2 缺失
		{Role: "assistant", Content: "done"},
	}
	got := sanitizeHistory(msgs)
	if len(got) != 3 {
		t.Fatalf("应保留 user、assistant 正文和最终答复，实际: %+v", got)
	}
	if len(got[1].ToolCalls) != 0 {
		t.Fatalf("未完成的 tool_calls 应被去掉: %+v", got[1])
	}
	for _, m := range got {
		if m.Role == "tool" {
			t.Fatalf("部分完成的 tool 结果也应一并丢弃: %+v", got)
		}
	}
}

func TestSanitizeKeepsCompleteToolRound(t *testing.T) {
	msgs := []store.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []store.ToolCallRecord{{ID: "c1", Name: "read_file"}}},
		{Role: "tool", Content: "file content", ToolCallID: "c1"},
		{Role: "assistant", Content: "done"},
	}
	got := sanitizeHistory(msgs)
	if len(got) != 4 || got[1].ToolCalls[0].ID != "c1" || got[2].Role != "tool" {
		t.Fatalf("完整的工具轮不应被修改，实际: %+v", got)
	}
}
