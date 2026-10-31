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

// goodSummary 是能通过退化检测的摘要样本（≥80 rune，信息密度足够低开销）。
const goodSummary = "目标：完成数据导出功能。\n已完成：调研了 store 层；确认导出走 JSON；写好了字段映射表。\n重要决定：导出文件放在 data/exports 下，命名带时间戳。\n未完成：写导出实现与测试；补 README 使用说明。\n偏好：先测试后提交，不要自动 push。"

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
	view := maybeCompact(context.Background(), st, full, 4, 6*1024, testSummarizer(&realCalls, goodSummary), config.LLMConfig{Mode: "openai"}, 0)
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
	view := maybeCompact(context.Background(), st, full, 4, 6*1024, testSummarizer(&calls, goodSummary), config.LLMConfig{}, 0)
	if calls != 1 || view.Summary == "" {
		t.Fatalf("首次应触发压缩: calls=%d summary=%q", calls, view.Summary)
	}
	// 同一会话立刻再来一次：冷却期内不调模型，视图沿用已持久化的摘要
	full2, _ := st.Get(sess.ID)
	view2 := maybeCompact(context.Background(), st, full2, 4, 6*1024, testSummarizer(&calls, goodSummary), config.LLMConfig{}, 0)
	if calls != 1 {
		t.Fatalf("冷却期内不应再次调用摘要，calls=%d", calls)
	}
	if view2.Summary == "" {
		t.Fatal("冷却期内应沿用已持久化摘要")
	}

	// 失败场景：新会话 + 摘要持续报错 → 重试后仍失败，返回无摘要视图，且不落库
	compactLastTry = map[string]time.Time{} // 重置冷却
	sess2, _ := st.Create("失败测试", "")
	for i := 0; i < 6; i++ {
		body := strings.Repeat(fmt.Sprintf("失败用例材料%d。", i), 30)
		_ = st.Append(sess2.ID, userMsg(fmt.Sprintf("q%d：%s", i, body), base.Add(time.Duration(i)*time.Minute)))
		_ = st.Append(sess2.ID, asstMsg(fmt.Sprintf("a%d：%s", i, body), base.Add(time.Duration(i)*time.Minute+30*time.Second)))
	}
	full3, _ := st.Get(sess2.ID)
	failCalls := 0
	view3 := maybeCompact(context.Background(), st, full3, 4, 6*1024, testFailingSummarizer(&failCalls), config.LLMConfig{}, 0)
	if failCalls != compactMaxAttempts {
		t.Fatalf("失败应重试到上限: %d", failCalls)
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
	view := maybeCompact(context.Background(), st, full, 20, 256*1024, testSummarizer(&calls, goodSummary), config.LLMConfig{}, 0)
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

// testSummarizerSeq 按调用次序返回预设回复（超出后重复最后一个）。
func testSummarizerSeq(calls *int, replies []string) StreamFn {
	return func(ctx context.Context, cfg config.LLMConfig, msgs []ChatMessage, defs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error) {
		i := *calls
		*calls++
		if i >= len(replies) {
			i = len(replies) - 1
		}
		return replies[i], nil, nil
	}
}

// 用户场景回归：条数超了但字节预算充足 → 上下文并不吃紧，不应压缩。
// （30 条一句话消息 ≈ 3KB，远小于 256KB 预算的 70% 水位）
func TestCompactSkipsWhenBudgetHealthy(t *testing.T) {
	st, _ := store.New(t.TempDir())
	sess, _ := st.Create("健康预算", "")
	base := time.Now()
	for i := 0; i < 30; i++ {
		_ = st.Append(sess.ID, store.Message{Role: "user", Content: fmt.Sprintf("第%d句话", i), CreatedAt: base})
		_ = st.Append(sess.ID, store.Message{Role: "assistant", Content: "好", CreatedAt: base})
	}
	full, _ := st.Get(sess.ID)
	calls := 0
	view := maybeCompact(context.Background(), st, full, 4, 256*1024, testSummarizer(&calls, goodSummary), config.LLMConfig{}, 0)
	if calls != 0 {
		t.Fatalf("上下文充足时不应压缩，calls=%d", calls)
	}
	if len(view.Recent) != len(full.Messages) {
		t.Fatal("未压缩时应保留完整消息供被动裁剪处理")
	}
}

// token 水位：配置了 ContextWindow 后按 bytes/4 估算触发，字节预算不再参与。
func TestCompactTokenWatermark(t *testing.T) {
	st, _ := store.New(t.TempDir())
	sess, _ := st.Create("token水位", "")
	base := time.Now()
	// ~5.5KB 消息；字节模式下（预算 256KB）永远不会触发
	for i := 0; i < 30; i++ {
		_ = st.Append(sess.ID, store.Message{Role: "user", Content: strings.Repeat("字", 90), CreatedAt: base})
		_ = st.Append(sess.ID, store.Message{Role: "assistant", Content: "好", CreatedAt: base})
	}
	full, _ := st.Get(sess.ID)
	calls := 0
	// 字节预算为 0（字节模式直接不触发）+ 小窗口：token 水位接管
	view := maybeCompact(context.Background(), st, full, 4, 0, testSummarizer(&calls, goodSummary), config.LLMConfig{}, 1000)
	if calls == 0 {
		t.Fatal("token 水位超限应触发压缩")
	}
	if view.Summary == "" {
		t.Fatal("token 水位触发后应有摘要")
	}
	// 大窗口（上下文充足）：不触发
	compactLastTry = map[string]time.Time{}
	calls2 := 0
	maybeCompact(context.Background(), st, full, 4, 0, testSummarizer(&calls2, goodSummary), config.LLMConfig{}, 10_000_000)
	if calls2 != 0 {
		t.Fatalf("窗口充足时不应压缩，calls=%d", calls2)
	}
}

// 真实 usage 触发：LastPromptTokens 有值时优先于字节估算。
func TestCompactTriggeredByRealUsage(t *testing.T) {
	st, _ := store.New(t.TempDir())
	sess, _ := st.Create("真实usage", "")
	base := time.Now()
	// 材料过 1KB 门槛（12 条 ~100B），但 bytes/4 估算 ≈ 300 token 远低于水位；
	// 触发只能来自上一次请求的真实 prompt tokens
	for i := 0; i < 6; i++ {
		_ = st.Append(sess.ID, store.Message{Role: "user", Content: strings.Repeat("消", 50), CreatedAt: base})
		_ = st.Append(sess.ID, store.Message{Role: "assistant", Content: strings.Repeat("回", 40), CreatedAt: base})
	}
	_ = st.AddUsage(sess.ID, 9000, 100) // LastPromptTokens=9000
	full, _ := st.Get(sess.ID)
	if full.LastPromptTokens != 9000 {
		t.Fatalf("前置：LastPromptTokens 应落库: %d", full.LastPromptTokens)
	}
	compactLastTry = map[string]time.Time{}
	calls := 0
	// 窗口 10000 → 水位 7000：真实 usage 9000 ≥ 7000 应触发
	view := maybeCompact(context.Background(), st, full, 4, 256*1024, testSummarizer(&calls, goodSummary), config.LLMConfig{}, 10000)
	if calls == 0 || view.Summary == "" {
		t.Fatalf("真实 usage 超水位应触发压缩: calls=%d", calls)
	}
	// 对照：真实 usage 不足时不触发（字节估算同样不足）
	_ = st.AddUsage(sess.ID, 100, 10)
	compactLastTry = map[string]time.Time{}
	full2, _ := st.Get(sess.ID)
	calls2 := 0
	maybeCompact(context.Background(), st, full2, 4, 256*1024, testSummarizer(&calls2, goodSummary), config.LLMConfig{}, 10000)
	if calls2 != 0 {
		t.Fatalf("真实 usage 不足时不应触发，calls=%d", calls2)
	}
}

// todo 提醒：显式编号步骤 / ≥3 个连接词 且没有待办才提醒；已有待办不打扰。
func TestTodoNudgeHeuristic(t *testing.T) {
	numbered := "帮我做三件事：\n1. 调研\n2. 实现\n3. 测试"
	if !planOrBuildNeedsNudge(numbered, nil) {
		t.Fatal("编号步骤应触发提醒")
	}
	chained := "先帮我调研一下这个项目，然后写实现，接着补测试，最后更新文档，再帮我跑一遍构建"
	if !planOrBuildNeedsNudge(chained, nil) {
		t.Fatal("多连接词应触发提醒")
	}
	// 已有待办：不再打扰
	if planOrBuildNeedsNudge(numbered, []store.Todo{{Content: "x", Status: "pending"}}) {
		t.Fatal("已有待办不应提醒")
	}
	// 单步请求：不打扰
	if planOrBuildNeedsNudge("帮我看看这个报错是什么意思", nil) {
		t.Fatal("单步请求不应提醒")
	}
	// 两个连接词：不到阈值
	if planOrBuildNeedsNudge("先调研，然后实现，看看再说", nil) {
		t.Fatal("连接词不足 3 个不应提醒")
	}
}

// 退化摘要：第一次输出过短判退化 → 重试成功。
func TestCompactDegenerateRetry(t *testing.T) {
	st, _ := store.New(t.TempDir())
	sess, _ := st.Create("退化重试", "")
	base := time.Now()
	for i := 0; i < 6; i++ {
		body := strings.Repeat(fmt.Sprintf("这是第%d个任务的详细背景材料。", i), 30)
		_ = st.Append(sess.ID, userMsg(fmt.Sprintf("q%d：%s", i, body), base.Add(time.Duration(i)*time.Minute)))
		_ = st.Append(sess.ID, asstMsg(fmt.Sprintf("**小柯** 回答%d。", i), base))
	}
	full, _ := st.Get(sess.ID)
	compactLastTry = map[string]time.Time{}
	calls := 0
	seq := testSummarizerSeq(&calls, []string{strings.Repeat("短", 5), goodSummary})
	view := maybeCompact(context.Background(), st, full, 4, 6*1024, seq, config.LLMConfig{}, 0)
	if calls != 2 {
		t.Fatalf("退化后应重试一次，calls=%d", calls)
	}
	if view.Summary != goodSummary {
		t.Fatalf("重试成功后应采用第二次摘要: %q", view.Summary)
	}
}

// 缩减率守卫：摘要几乎不比原文短 → 放弃压缩，不落库。
func TestCompactReductionGuard(t *testing.T) {
	st, _ := store.New(t.TempDir())
	sess, _ := st.Create("缩减守卫", "")
	base := time.Now()
	for i := 0; i < 6; i++ {
		_ = st.Append(sess.ID, userMsg(fmt.Sprintf("q%d", i), base))
		_ = st.Append(sess.ID, asstMsg("a", base))
	}
	full, _ := st.Get(sess.ID)
	compactLastTry = map[string]time.Time{}
	calls := 0
	// 原文很短（12 条一句话 ≈ 数百字节），返回一条超长摘要 → 守卫应拦下
	bloated := strings.Repeat("这是复读机式的超长摘要。", 200)
	view := maybeCompact(context.Background(), st, full, 4, 6*1024, testSummarizer(&calls, bloated), config.LLMConfig{}, 0)
	if view.Summary != "" {
		t.Fatalf("缩减率不足应放弃压缩: %q", view.Summary)
	}
	if got, _ := st.Get(sess.ID); got.Summary != "" {
		t.Fatal("守卫拦截后不应落库摘要")
	}
}

// cleanSummary：剥掉整体包裹的代码围栏。
func TestCleanSummary(t *testing.T) {
	if got := cleanSummary("```markdown\n正文内容\n```"); got != "正文内容" {
		t.Fatalf("应剥围栏: %q", got)
	}
	if got := cleanSummary("  普通摘要  "); got != "普通摘要" {
		t.Fatalf("普通文本应只去空白: %q", got)
	}
}
