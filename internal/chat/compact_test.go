package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"xiaoke/internal/config"
	"xiaoke/internal/store"
	"xiaoke/internal/tools"
)

func userMsg(content string, at time.Time) store.Message {
	return store.Message{Role: "user", Content: content, CreatedAt: at}
}
func asstMsg(content string, at time.Time) store.Message {
	return store.Message{Role: "assistant", Content: content, CreatedAt: at}
}

// 截止点必须落在 user 消息之前；材料不足/没有 user 时放弃。
func TestCutForSummary(t *testing.T) {
	base := time.Unix(1700000000, 0)
	msgs := []store.Message{}
	for i := 0; i < 6; i++ {
		msgs = append(msgs, userMsg(fmt.Sprintf("q%d", i), base.Add(time.Duration(i)*time.Minute)))
		msgs = append(msgs, asstMsg(fmt.Sprintf("a%d", i), base.Add(time.Duration(i)*time.Minute+30*time.Second)))
	}
	// 12 条，keep=4 → 截止点应落在 q4（index 8）
	cut := cutForSummary(msgs, 4)
	if cut != 8 {
		t.Fatalf("cut 应为 8（q4 的位置），got %d", cut)
	}
	if msgs[cut].Role != "user" {
		t.Fatalf("截止点应落在 user 消息上，got %s", msgs[cut].Role)
	}
	// keep >= len → 不压缩
	if cutForSummary(msgs, 12) != -1 || cutForSummary(msgs, 99) != -1 {
		t.Fatal("keep>=len 应返回 -1")
	}
	// 没有任何 user 消息 → 放弃
	tools := []store.Message{asstMsg("a", base), {Role: "tool", Content: "r", CreatedAt: base}}
	if cutForSummary(tools, 0) != -1 {
		t.Fatal("无 user 消息应返回 -1")
	}
}

// 摘要边界切分：Recent 从摘要覆盖点之后的第一条开始。
func TestSplitBySummary(t *testing.T) {
	base := time.Unix(1700000000, 0)
	msgs := []store.Message{
		userMsg("q0", base), asstMsg("a0", base.Add(time.Second)),
		userMsg("q1", base.Add(time.Minute)), asstMsg("a1", base.Add(time.Minute+time.Second)),
	}
	upTo := msgs[1].CreatedAt
	view := splitBySummary(msgs, "摘要内容", upTo)
	if view.Summary != "摘要内容" || len(view.Recent) != 2 || view.Recent[0].Content != "q1" {
		t.Fatalf("切分错误: %+v", view)
	}
	// 无摘要：原样返回
	all := splitBySummary(msgs, "", time.Time{})
	if len(all.Recent) != 4 || all.Summary != "" {
		t.Fatalf("无摘要应原样返回: %+v", all)
	}
	// 全部被覆盖（不该发生，兜底不 panic）
	empty := splitBySummary(msgs, "s", msgs[3].CreatedAt)
	if len(empty.Recent) != 0 {
		t.Fatalf("全覆盖应返回空近段: %+v", empty)
	}
}

// 触发压缩：用假 summarizer 验证摘要生成、持久化与视图更新。
func TestMaybeCompactTriggers(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Create("压缩测试", "")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	// 6 个回合 = 12 条；keep=4 → 会触发
	for i := 0; i < 6; i++ {
		body := strings.Repeat(fmt.Sprintf("这是第%d个任务的详细背景。", i), 30)
		if err := st.Append(sess.ID, userMsg(fmt.Sprintf("问题%d：%s", i, body), base.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
		if err := st.Append(sess.ID, asstMsg(fmt.Sprintf("**小柯** 完成%d号任务。%s", i, body), base.Add(time.Duration(i)*time.Minute+30*time.Second))); err != nil {
			t.Fatal(err)
		}
	}

	realCalls := 0
	full, err := st.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	view := maybeCompact(context.Background(), st, full, 4, 256*1024, testSummarizer(&realCalls, "用户做了 6 个任务，全部完成"), config.LLMConfig{Mode: "openai"})
	if realCalls != 1 {
		t.Fatalf("摘要应被调用 1 次，got %d", realCalls)
	}
	if view.Summary == "" || len(view.Recent) != 4 {
		t.Fatalf("压缩后视图不对: summary=%q recent=%d", view.Summary, len(view.Recent))
	}
	if view.Recent[0].Role != "user" {
		t.Fatalf("近段应以 user 开头，got %s", view.Recent[0].Role)
	}
	// 持久化验证：重新加载会话，摘要在
	again, err := st.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Summary == "" || again.SummaryUpTo.IsZero() {
		t.Fatalf("摘要未持久化: %+v", again)
	}
}

// 冷却期内不重复压缩；摘要失败时退回被动裁剪视图。
func TestMaybeCompactCooldownAndFailure(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := st.Create("冷却测试", "")
	base := time.Now()
	for i := 0; i < 6; i++ {
		body := strings.Repeat(fmt.Sprintf("背景材料%d。", i), 30)
		_ = st.Append(sess.ID, userMsg(fmt.Sprintf("q%d：%s", i, body), base.Add(time.Duration(i)*time.Minute)))
		_ = st.Append(sess.ID, asstMsg(fmt.Sprintf("a%d：%s", i, body), base.Add(time.Duration(i)*time.Minute+30*time.Second)))
	}
	full, _ := st.Get(sess.ID)

	calls := 0
	view := maybeCompact(context.Background(), st, full, 4, 256*1024, testSummarizer(&calls, "摘要"), config.LLMConfig{})
	if calls != 1 || view.Summary == "" {
		t.Fatalf("首次应触发压缩: calls=%d summary=%q", calls, view.Summary)
	}
	// 同一会话立刻再来一次：冷却期内不调模型，视图沿用已持久化的摘要
	full2, _ := st.Get(sess.ID)
	view2 := maybeCompact(context.Background(), st, full2, 4, 256*1024, testSummarizer(&calls, "摘要"), config.LLMConfig{})
	if calls != 1 {
		t.Fatalf("冷却期内不应再次调用摘要，calls=%d", calls)
	}
	if view2.Summary == "" {
		t.Fatal("冷却期内应沿用已持久化摘要")
	}

	// 失败场景：新会话 + 摘要报错 → 返回无摘要视图，且不落库
	compactLastTry = map[string]time.Time{} // 重置冷却
	sess2, _ := st.Create("失败测试", "")
	for i := 0; i < 6; i++ {
		body := strings.Repeat(fmt.Sprintf("失败用例材料%d。", i), 30)
		_ = st.Append(sess2.ID, userMsg(fmt.Sprintf("q%d：%s", i, body), base.Add(time.Duration(i)*time.Minute)))
		_ = st.Append(sess2.ID, asstMsg(fmt.Sprintf("a%d：%s", i, body), base.Add(time.Duration(i)*time.Minute+30*time.Second)))
	}
	full3, _ := st.Get(sess2.ID)
	failCalls := 0
	view3 := maybeCompact(context.Background(), st, full3, 4, 256*1024, testFailingSummarizer(&failCalls), config.LLMConfig{})
	if failCalls != 1 {
		t.Fatalf("失败重试应被调用一次: %d", failCalls)
	}
	if view3.Summary != "" || len(view3.Recent) != len(full3.Messages) {
		t.Fatalf("失败应退回被动视图: summary=%q recent=%d", view3.Summary, len(view3.Recent))
	}
	if got, _ := st.Get(sess2.ID); got.Summary != "" {
		t.Fatal("失败不应落库摘要")
	}
}

// 未超预算时不触发。
func TestMaybeCompactSkipsSmallHistory(t *testing.T) {
	st, _ := store.New(t.TempDir())
	sess, _ := st.Create("小会话", "")
	base := time.Now()
	for i := 0; i < 3; i++ {
		_ = st.Append(sess.ID, userMsg(fmt.Sprintf("q%d", i), base))
		_ = st.Append(sess.ID, asstMsg(fmt.Sprintf("a%d", i), base))
	}
	full, _ := st.Get(sess.ID)
	calls := 0
	view := maybeCompact(context.Background(), st, full, 20, 256*1024, testSummarizer(&calls, "不该被调用"), config.LLMConfig{})
	if calls != 0 {
		t.Fatalf("小历史不应触发压缩，calls=%d", calls)
	}
	if len(view.Recent) != len(full.Messages) {
		t.Fatal("未触发时应返回完整消息")
	}
}

// 摘要转写：隐藏消息跳过、长内容截断。
func TestTranscriptForSummary(t *testing.T) {
	base := time.Now()
	msgs := []store.Message{
		{Role: "user", Content: "可见的问题", Hidden: false, CreatedAt: base},
		{Role: "user", Content: "批准计划的隐藏指令", Hidden: true, CreatedAt: base},
		{Role: "tool", Content: strings.Repeat("x", 1000), CreatedAt: base},
	}
	out := transcriptForSummary(msgs)
	if strings.Contains(out, "批准计划的隐藏指令") {
		t.Fatal("隐藏消息不应出现在摘要输入里")
	}
	if !strings.Contains(out, "[用户] 可见的问题") || !strings.Contains(out, "[工具结果]") {
		t.Fatalf("转写格式不对: %s", out)
	}
	if len(out) > compactInputLimit+100 {
		t.Fatalf("转写应受限，len=%d", len(out))
	}
}

// ---- 测试辅助：符合 StreamFn 签名的假实现 ----

func testSummarizer(calls *int, reply string) StreamFn {
	return func(ctx context.Context, cfg config.LLMConfig, msgs []ChatMessage, defs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error) {
		*calls++
		return reply, nil, nil
	}
}

func testFailingSummarizer(calls *int) StreamFn {
	return func(ctx context.Context, cfg config.LLMConfig, msgs []ChatMessage, defs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error) {
		*calls++
		return "", nil, errors.New("模拟上游故障")
	}
}
