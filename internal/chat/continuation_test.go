package chat

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"xiaoke/internal/agents"
	"xiaoke/internal/config"
	"xiaoke/internal/mcp"
	"xiaoke/internal/memory"
	"xiaoke/internal/persona"
	"xiaoke/internal/projects"
	"xiaoke/internal/rules"
	"xiaoke/internal/settings"
	"xiaoke/internal/skills"
	"xiaoke/internal/store"
	"xiaoke/internal/tools"
)

// newContinuationFixture 构造一个可注入假上游的最小 Handler。
// maxTotal <= 0 时用默认总预算。
func newContinuationFixture(t *testing.T, maxSteps, maxTotal int) (*Handler, *store.Store, projects.Project) {
	t.Helper()
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	st, err := store.New(filepath.Join(dataDir, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	set, err := settings.Open(filepath.Join(dataDir, "settings.json"), config.LLMConfig{Mode: "mock"})
	if err != nil {
		t.Fatal(err)
	}
	// 压缩单段轮数，方便快速撞上限
	cur := set.Get()
	cur.MaxToolSteps = maxSteps
	if maxTotal > 0 {
		cur.MaxTotalSteps = maxTotal
	}
	if err := set.Update(cur, nil); err != nil {
		t.Fatal(err)
	}
	rootDir := filepath.Join(dir, "workspace")
	pm, err := projects.New(filepath.Join(dataDir, "projects.json"), func() string { return rootDir })
	if err != nil {
		t.Fatal(err)
	}
	proj, err := pm.Create("续跑测试", "", "", func() string { return "projcont0001" })
	if err != nil {
		t.Fatal(err)
	}
	ag, err := agents.Open(filepath.Join(dataDir, "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ag.EnsureDefault(agents.Agent{
		Name: persona.Name, Description: "d", SystemPrompt: "你是小柯", Tools: true,
	}); err != nil {
		t.Fatal(err)
	}
	sk, err := skills.Open(filepath.Join(dataDir, "skills.json"))
	if err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(filepath.Join(dataDir, "memory"))
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(&config.Config{DataDir: dataDir}, st, set, pm,
		mcp.NewManager(context.Background()), ag, sk, mem, rules.New(), nil)
	return h, st, proj
}

// 撞到单段轮数上限后必须自动续跑，不能停下来要求用户输入「继续」。
// 假上游：前 N 轮都返回工具调用，之后返回最终文本。
func TestConverseAutoContinuesAfterStepLimit(t *testing.T) {
	const maxSteps = 2
	const maxTotal = 8 // 8 轮总预算 → 4 段
	h, st, proj := newContinuationFixture(t, maxSteps, maxTotal)

	sess, err := st.Create("", proj.ID)
	if err != nil {
		t.Fatal(err)
	}

	var calls int
	// 让工具轮跑满总预算，之后给最终答复
	toolRounds := maxTotal
	h.streamFnOverride = func(ctx context.Context, llm config.LLMConfig, msgs []ChatMessage,
		defs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error) {
		calls++
		if calls > toolRounds {
			return "**小柯** 全部做完了。", nil, nil
		}
		return "", []ToolCall{{
			ID:   fmt.Sprintf("call_%d", calls),
			Type: "function",
			Function: ToolFunction{
				Name:      "list_files",
				Arguments: `{"path":"."}`,
			},
		}}, nil
	}

	var events []map[string]any
	done := false
	err = h.converse(context.Background(), converseParams{
		SessionID: sess.ID, Content: "把所有文件都检查一遍", ProjectID: proj.ID,
	}, func(evt map[string]any) {
		events = append(events, evt)
		if evt["type"] == "done" {
			done = true
		}
	})
	if err != nil {
		t.Fatalf("converse 出错: %v", err)
	}
	if !done {
		t.Fatalf("应正常结束（收到 done 事件），事件数=%d", len(events))
	}
	// 关键断言：工具轮数超过单段上限 → 说明真的续跑了
	if calls <= maxSteps {
		t.Errorf("只调用 %d 轮，未触发续跑（单段上限 %d）", calls, maxSteps)
	}
	// 最终答复不能要求用户输入「继续」
	for _, evt := range events {
		if s, ok := evt["text"].(string); ok && strings.Contains(s, "回一句") {
			t.Errorf("最终答复要求用户输入继续: %q", s)
		}
	}
}

// 任务在单段内就完成时，不应触发续跑（不能白白多跑几段）。
func TestConverseNoContinuationWhenDoneEarly(t *testing.T) {
	h, st, proj := newContinuationFixture(t, 4, 0)
	sess, err := st.Create("", proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	h.streamFnOverride = func(ctx context.Context, llm config.LLMConfig, msgs []ChatMessage,
		defs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error) {
		calls++
		if calls == 1 {
			return "", []ToolCall{{
				ID: "c1", Type: "function",
				Function: ToolFunction{Name: "list_files", Arguments: `{"path":"."}`},
			}}, nil
		}
		return "**小柯** 看完了，就这些。", nil, nil
	}
	done := false
	err = h.converse(context.Background(), converseParams{
		SessionID: sess.ID, Content: "看看目录", ProjectID: proj.ID,
	}, func(evt map[string]any) {
		if evt["type"] == "done" {
			done = true
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Fatal("应正常结束")
	}
	if calls != 2 {
		t.Errorf("单段内完成应只调用 2 次（1 轮工具 + 1 轮答复），实际 %d", calls)
	}
}

// 撞上限续跑后，最终落库的 assistant 消息应是真正的完成答复，
// 而不是「请回一句继续」这类要求用户再催的话术。
func TestContinuationFinalMessagePersisted(t *testing.T) {
	h, st, proj := newContinuationFixture(t, 2, 8)
	sess, err := st.Create("", proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	rounds := 0
	h.streamFnOverride = func(ctx context.Context, llm config.LLMConfig, msgs []ChatMessage,
		defs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error) {
		rounds++
		if rounds > 6 {
			return "**小柯** 全部完成。", nil, nil
		}
		return "", []ToolCall{{
			ID: fmt.Sprintf("h%d", rounds), Type: "function",
			Function: ToolFunction{Name: "list_files", Arguments: `{"path":"."}`},
		}}, nil
	}

	done := false
	if err := h.converse(context.Background(), converseParams{
		SessionID: sess.ID, Content: "全项目检查", ProjectID: proj.ID,
	}, func(evt map[string]any) {
		if evt["type"] == "done" {
			done = true
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Fatal("应收到 done 事件")
	}
	if rounds <= 2 {
		t.Errorf("应触发续跑（单段上限 2），实际只跑 %d 轮", rounds)
	}

	got, err := st.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var lastAssistant string
	for _, m := range got.Messages {
		if m.Role == "assistant" && strings.TrimSpace(m.Content) != "" {
			lastAssistant = m.Content
		}
	}
	if lastAssistant == "" {
		t.Fatal("应有一条最终 assistant 消息")
	}
	if strings.Contains(lastAssistant, "回一句") || strings.Contains(lastAssistant, "输入继续") {
		t.Errorf("最终答复仍在要求用户输入继续: %q", lastAssistant)
	}
	if !strings.Contains(lastAssistant, "全部完成") {
		t.Errorf("最终答复应来自最后一轮，实际 %q", lastAssistant)
	}
}

// 续跑段数必须严格由总预算推导，且总轮数不超过预算。
func TestContinuationRoundsFromBudget(t *testing.T) {
	cases := []struct {
		maxSteps, maxTotal, wantRounds int
	}{
		// 总预算 <= 单段：不续跑（0 会被 normalize 补成默认 120，故此处不用 0）
		{20, 20, 0},
		{20, 10, 0},
		// 正好整段
		{20, 120, 5}, // 20 + 5*20 = 120
		{20, 300, 14},
		// 非整段：向下取整，保证不超预算
		{20, 130, 5}, // 20 + 100 = 120 <= 130
		{2, 8, 3},    // 2 + 6 = 8
		// 单段很大时最多续一次
		{200, 300, 0}, // 200 + 0 = 200 <= 300
	}
	for _, c := range cases {
		total := c.maxTotal
		// 模拟 settings.normalize：<=0 用默认预算，小于单段则抬到单段
		if total <= 0 {
			total = 120
		}
		if total < c.maxSteps {
			total = c.maxSteps
		}
		got := 0
		if total > c.maxSteps {
			got = (total - c.maxSteps) / c.maxSteps
			if got > maxStepsContinuationsCeiling {
				got = maxStepsContinuationsCeiling
			}
		}
		if got != c.wantRounds {
			t.Errorf("maxSteps=%d 预算=%d：期望续 %d 段，实际 %d", c.maxSteps, c.maxTotal, c.wantRounds, got)
		}
		// 总轮数不超过预算
		if used := c.maxSteps * (1 + got); used > total {
			t.Errorf("maxSteps=%d 预算=%d：总轮数 %d 超过预算", c.maxSteps, c.maxTotal, used)
		}
	}
}

// 假上游返回「仅推理、无正文无工具调用」：应正常收尾，而不是发 error 事件。
// 这对应推理模型偶发只吐 reasoning_content 的场景（上游层已把它与「完全空响应」区分）。
func TestConverseReasoningOnlyDoesNotError(t *testing.T) {
	h, st, proj := newContinuationFixture(t, 4, 0)
	sess, err := st.Create("", proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.streamFnOverride = func(ctx context.Context, llm config.LLMConfig, msgs []ChatMessage,
		defs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error) {
		return "", nil, nil // 仅推理：上游层返回空内容、无错误
	}

	var events []map[string]any
	done := false
	err = h.converse(context.Background(), converseParams{
		SessionID: sess.ID, Content: "分析一下", ProjectID: proj.ID,
	}, func(evt map[string]any) {
		events = append(events, evt)
		if evt["type"] == "done" {
			done = true
		}
	})
	if err != nil {
		t.Fatalf("converse 不应返回错误: %v", err)
	}
	if !done {
		t.Fatalf("应正常结束（收到 done 事件），事件数=%d", len(events))
	}
	for _, evt := range events {
		if evt["type"] == "error" {
			t.Fatalf("仅推理不应发 error 事件: %v", evt)
		}
	}
}

// 空内容（仅推理）收尾时应发出 done 事件，且回复落库，不留半截会话。
func TestConverseReasoningOnlyPersistsReply(t *testing.T) {
	h, st, proj := newContinuationFixture(t, 4, 0)
	sess, err := st.Create("", proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.streamFnOverride = func(ctx context.Context, llm config.LLMConfig, msgs []ChatMessage,
		defs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error) {
		return "", nil, nil
	}

	var done bool
	err = h.converse(context.Background(), converseParams{
		SessionID: sess.ID, Content: "分析一下", ProjectID: proj.ID,
	}, func(evt map[string]any) {
		if evt["type"] == "done" {
			done = true
		}
	})
	if err != nil {
		t.Fatalf("converse 出错: %v", err)
	}
	if !done {
		t.Fatal("应发出 done 事件")
	}
	got, err := st.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := got.Messages[len(got.Messages)-1]
	if last.Role != "assistant" {
		t.Fatalf("最后一条应是 assistant，实际 %q", last.Role)
	}
}
