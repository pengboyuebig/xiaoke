package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"xiaoke/internal/config"
	"xiaoke/internal/store"
	"xiaoke/internal/tools"
)

// 上下文隔离铁律：会话 A 的任何内容（消息/摘要）都不允许出现在会话 B 的请求里。
// 回归场景：A 里聊过「夜枭计划」，切到 B 提问时，发给上游的消息里不得包含 A 的内容。
func TestCrossSessionIsolation(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := config.LLMConfig{Mode: "openai", Model: "test"}

	// 会话 A：制造足够长的历史并触发一次压缩（摘要是跨会话泄漏的最大嫌疑）
	sessA, _ := st.Create("A", "")
	secretA := "绝密代号夜枭计划的口令是蓝鲸"
	if err := st.Append(sessA.ID, store.Message{Role: "user", Content: secretA, CreatedAt: timeNow()}); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(sessA.ID, store.Message{Role: "assistant", Content: "**小柯** 已记住。", CreatedAt: timeNow()}); err != nil {
		t.Fatal(err)
	}
	// 触发压缩：往 A 里塞满超过预算的消息，让 maybeCompact 生成摘要
	longBody := strings.Repeat("A 会话的填充内容，用于撑大历史体积。", 40)
	for i := 0; i < 8; i++ {
		_ = st.Append(sessA.ID, store.Message{Role: "user", Content: longBody, CreatedAt: timeNow()})
		_ = st.Append(sessA.ID, store.Message{Role: "assistant", Content: longBody, CreatedAt: timeNow()})
	}
	fullA, _ := st.Get(sessA.ID)
	var compactCalls int
	summarizeA := func(ctx context.Context, cfg config.LLMConfig, msgs []ChatMessage, defs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error) {
		compactCalls++
		return "会话 A 的摘要：用户聊过夜枭计划。", nil, nil
	}
	viewA := maybeCompact(context.Background(), st, fullA, 4, 6*1024, summarizeA, base)
	if compactCalls == 0 || viewA.Summary == "" {
		t.Fatalf("前置条件：A 应成功生成摘要（calls=%d summary=%q）", compactCalls, viewA.Summary)
	}

	// 会话 B：全新会话，问一个新问题
	sessB, _ := st.Create("B", "")
	_ = st.Append(sessB.ID, store.Message{Role: "user", Content: "今天天气如何", CreatedAt: timeNow()})
	fullB, _ := st.Get(sessB.ID)

	// 用抓包式 StreamFn 捕获发给上游的完整消息
	var sentToUpstream []ChatMessage
	capture := func(ctx context.Context, cfg config.LLMConfig, msgs []ChatMessage, defs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error) {
		sentToUpstream = msgs
		return "**小柯** 不知道。", nil, nil
	}
	viewB := maybeCompact(context.Background(), st, fullB, 4, 6*1024, summarizeA, base)
	convo := []ChatMessage{{Role: "system", Content: "系统提示词"}}
	if strings.TrimSpace(viewB.Summary) != "" {
		convo = append(convo, ChatMessage{Role: "system", Content: viewB.Summary})
	}
	history := sanitizeHistory(trimHistory(viewB.Recent, 4, 256*1024))
	convo = append(convo, toWire(history)...)
	_, _, _ = capture(context.Background(), base, convo, nil, Hooks{}, "")

	all := ""
	for _, m := range sentToUpstream {
		all += m.Content + "\n"
		for _, c := range m.ToolCalls {
			all += c.Function.Arguments + "\n"
		}
	}
	if strings.Contains(all, "夜枭") || strings.Contains(all, "蓝鲸") || strings.Contains(all, "会话 A 的摘要") {
		t.Fatalf("跨会话泄漏！会话 B 的上游请求里出现了会话 A 的内容:\n%s", all)
	}
	if !strings.Contains(all, "今天天气如何") {
		t.Fatal("会话 B 自己的消息应在请求里")
	}
}

// 压缩只影响本会话：A 压缩后，B 的 Get 不应有 Summary。
func TestSummaryStaysInOwnSession(t *testing.T) {
	st, _ := store.New(t.TempDir())
	a, _ := st.Create("A", "")
	b, _ := st.Create("B", "")
	_ = st.Append(a.ID, store.Message{Role: "user", Content: strings.Repeat("x", 2000), CreatedAt: timeNow()})
	for i := 0; i < 6; i++ {
		_ = st.Append(a.ID, store.Message{Role: "user", Content: strings.Repeat("填充", 300), CreatedAt: timeNow()})
		_ = st.Append(a.ID, store.Message{Role: "assistant", Content: strings.Repeat("回", 300), CreatedAt: timeNow()})
	}
	fullA, _ := st.Get(a.ID)
	view := maybeCompact(context.Background(), st, fullA, 4, 6*1024, testSummarizer(new(int), "A 的摘要"), config.LLMConfig{})
	if view.Summary == "" {
		t.Skip("压缩未触发（材料不足），跳过")
	}
	gotB, _ := st.Get(b.ID)
	if gotB.Summary != "" || !gotB.SummaryUpTo.IsZero() {
		t.Fatalf("摘要泄漏到了会话 B: summary=%q", gotB.Summary)
	}
}

// compactLastTry 的冷却按会话隔离：A 刚压缩过，B 首次压缩不应被 A 的冷却挡住。
func TestCompactCooldownPerSession(t *testing.T) {
	st, _ := store.New(t.TempDir())
	mk := func(name string) *store.Session {
		s, _ := st.Create(name, "")
		for i := 0; i < 6; i++ {
			_ = st.Append(s.ID, store.Message{Role: "user", Content: strings.Repeat(name+"填充", 300), CreatedAt: timeNow()})
			_ = st.Append(s.ID, store.Message{Role: "assistant", Content: strings.Repeat("回", 300), CreatedAt: timeNow()})
		}
		return s
	}
	a := mk("A")
	b := mk("B")
	fullA, _ := st.Get(a.ID)
	if v := maybeCompact(context.Background(), st, fullA, 4, 6*1024, testSummarizer(new(int), "a"), config.LLMConfig{}); v.Summary == "" {
		t.Skip("A 未触发压缩")
	}
	fullB, _ := st.Get(b.ID)
	calls := 0
	vb := maybeCompact(context.Background(), st, fullB, 4, 6*1024, testSummarizer(&calls, "b"), config.LLMConfig{})
	if vb.Summary == "" || calls != 1 {
		t.Fatalf("B 的首次压缩不应被 A 的冷却挡住: calls=%d summary=%q", calls, vb.Summary)
	}
}

// timeNow 统一测试时间戳。
func timeNow() time.Time { return time.Now() }

// JSON 序列化 Smoke：Session 含 Summary 字段时的导出仍可解析。
func TestSessionJSONRoundTrip(t *testing.T) {
	raw := `{"id":"0123456789abcdef","summary":"摘要","summaryUpTo":"2026-10-31T00:00:00Z","messages":[]}`
	var sess store.Session
	if err := json.Unmarshal([]byte(raw), &sess); err != nil {
		t.Fatal(err)
	}
	if sess.Summary != "摘要" || sess.SummaryUpTo.IsZero() {
		t.Fatalf("字段未解析: %+v", sess)
	}
}
