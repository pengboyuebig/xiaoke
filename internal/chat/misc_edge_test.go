package chat

import (
	"strings"
	"testing"

	"xiaoke/internal/store"
)

// deriveTitle 各种输入的边界。
func TestDeriveTitleEdges(t *testing.T) {
	cases := []struct{ in, wantPrefix string }{
		{"", ""},
		{"   ", ""},
		{"\n\n\n", ""},
		{"你好", "你好"},
		{"# 标题\n正文", "标题"}, // markdown 前缀被剥离（正确行为）
		{"  多行  \n 第二行", "多行"},
		{strings.Repeat("很长的标题", 20), "很长的标题"},
	}
	for _, c := range cases {
		got := deriveTitle(c.in)
		if c.wantPrefix == "" {
			if got != "" {
				t.Errorf("deriveTitle(%q) = %q，期望空", c.in, got)
			}
			continue
		}
		if !strings.HasPrefix(got, c.wantPrefix) {
			t.Errorf("deriveTitle(%q) = %q，期望以 %q 开头", c.in, got, c.wantPrefix)
		}
		// 标题长度限制（store.SetTitle 上限 32 rune）
		if len([]rune(got)) > 33 {
			t.Errorf("deriveTitle 结果过长（%d rune）: %q", len([]rune(got)), got)
		}
	}
}

// todosText / pendingTodos 边界。
func TestTodosHelpers(t *testing.T) {
	var nilTodos []store.Todo
	if got := todosText(nilTodos); got != "" {
		t.Errorf("空待办应返回空串，got %q", got)
	}
	if got := pendingTodos(nilTodos); len(got) != 0 {
		t.Errorf("空待办应返回空切片")
	}
	all := []store.Todo{
		{Content: "a", Status: "completed"},
		{Content: "b", Status: "in_progress"},
	}
	if got := pendingTodos(all); len(got) != 1 || got[0].Content != "b" {
		t.Errorf("应过滤掉已完成项: %+v", got)
	}
}

// planOrBuildNeedsNudge 不应对单步请求误触发。
func TestNudgeNoFalsePositive(t *testing.T) {
	single := []string{
		"你好",
		"帮我读一下 README",
		"这个函数是做什么的",
		"解释一下这段代码",
	}
	for _, c := range single {
		if planOrBuildNeedsNudge(c, nil) {
			t.Errorf("单步请求不应触发 todo 提醒: %q", c)
		}
	}
	// 有 todo 时也不应提醒
	if planOrBuildNeedsNudge("先做A然后做B最后做C", []store.Todo{{Content: "x"}}) {
		t.Error("已有待办时不应再提醒")
	}
}

// toolArgsSummary 各种参数形态。
func TestToolArgsSummary(t *testing.T) {
	cases := []struct{ in, wantContains string }{
		{`{"path":"a.txt"}`, "path=a.txt"},
		{`{"command":"git status"}`, "command=git status"},
		{`{"url":"https://x.com"}`, "url=https://x.com"},
		{`{"unknown":"v"}`, "unknown"}, // 兜底原文
		{`not json`, "not json"},
		{`{}`, "{}"},
	}
	for _, c := range cases {
		got := toolArgsSummary(c.in)
		if !strings.Contains(got, c.wantContains) {
			t.Errorf("toolArgsSummary(%q) = %q，期望包含 %q", c.in, got, c.wantContains)
		}
	}
}
