package api

import (
	"testing"

	"xiaoke/internal/settings"
)

// 档案级上下文窗口必须下发给前端，否则编辑一次档案就会把窗口清成 0。
func TestProfileViewsKeepsContextWindow(t *testing.T) {
	views := profileViews([]settings.LLMProfile{
		{ID: "p1", Name: "A", Model: "m1", ContextWindow: 128_000},
		{ID: "p2", Name: "B", Model: "m2", ContextWindow: 0},
	})
	if len(views) != 2 {
		t.Fatalf("期望 2 个视图，got %d", len(views))
	}
	if views[0].ContextWindow != 128_000 {
		t.Fatalf("档案窗口未下发：%d", views[0].ContextWindow)
	}
	if views[1].ContextWindow != 0 {
		t.Fatalf("0 应保持 0：%d", views[1].ContextWindow)
	}
}
