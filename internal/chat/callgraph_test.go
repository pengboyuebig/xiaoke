package chat

import (
	"context"
	"strings"
	"testing"

	"xiaoke/internal/config"
	"xiaoke/internal/agents"
	"xiaoke/internal/tools"
)

// ---------- 调用图 ----------

func TestCallGraphDetectsCycle(t *testing.T) {
	g := newCallGraph("pm")
	// pm → fe → qa 是正常链
	if err := g.Enter("pm", "fe"); err != nil {
		t.Fatalf("pm→fe 应允许: %v", err)
	}
	if err := g.Enter("fe", "qa"); err != nil {
		t.Fatalf("fe→qa 应允许: %v", err)
	}
	// qa → pm 会成环，必须拦下
	if err := g.Enter("qa", "pm"); err == nil {
		t.Fatal("qa→pm 应被识别为调用环")
	} else if !strings.Contains(err.Error(), "环") {
		t.Fatalf("错误信息应说明是环: %v", err)
	}
	// 自我调用也要拦
	if err := g.Enter("fe", "fe"); err == nil {
		t.Fatal("fe→fe 自我调用应被拒绝")
	}
}

func TestCallGraphPingPongLimit(t *testing.T) {
	g := newCallGraph("a")
	for i := 0; i < maxPingPongTurns; i++ {
		if err := g.Enter("a", "b"); err != nil {
			t.Fatalf("第 %d 次 a→b 应允许: %v", i+1, err)
		}
	}
	if err := g.Enter("a", "b"); err == nil {
		t.Fatal("超过 maxPingPongTurns 应被拒绝")
	}
}

func TestCallGraphBudgetDecrements(t *testing.T) {
	b := newBudgetTracker(3)
	for i := 0; i < 3; i++ {
		if !b.TryConsume() {
			t.Fatalf("第 %d 次应能消耗", i+1)
		}
	}
	if b.TryConsume() {
		t.Fatal("预算耗尽后不应再允许")
	}
	if b.Left() != 0 {
		t.Fatalf("剩余预算应为 0，实际 %d", b.Left())
	}
}

func TestCallGraphStopsRecorded(t *testing.T) {
	g := newCallGraph("pm")
	_ = g.Enter("pm", "fe")
	_ = g.Enter("fe", "pm") // 成环
	stops := g.Stops()
	if len(stops) != 1 {
		t.Fatalf("应记录 1 次拦截，实际 %d", len(stops))
	}
	if stops[0].Reason != StopLoopCapped {
		t.Fatalf("原因应为 loop_capped，实际 %s", stops[0].Reason)
	}
}

// ---------- AgentCallDescription ----------

func TestAgentCallDescriptionListsCatalog(t *testing.T) {
	catalog := []tools.AgentRef{
		{ID: "fe01", Name: "前端", Description: "写页面"},
		{ID: "qa01", Name: "测试", Description: "跑测试"},
	}
	desc := tools.AgentCallDescription("pm01", catalog)
	if !strings.Contains(desc, "前端") || !strings.Contains(desc, "fe01") {
		t.Fatalf("描述应包含可调用智能体: %s", desc)
	}
	if !strings.Contains(desc, "测试") {
		t.Fatalf("描述应包含第二个智能体: %s", desc)
	}
}

func TestAgentCallDescriptionEmpty(t *testing.T) {
	desc := tools.AgentCallDescription("pm01", nil)
	if !strings.Contains(desc, "无") {
		t.Fatalf("空列表应明确说明: %s", desc)
	}
}

// ---------- agents.Agent 白名单 ----------

func TestAgentCanBeCalledBy(t *testing.T) {
	open := agents.Agent{ID: "a1", Callable: true}
	if !open.CanBeCalledBy("anyone") {
		t.Fatal("Callable 且无白名单时应允许任何人")
	}

	restricted := agents.Agent{ID: "a2", Callable: true, AllowedCallers: []string{"pm"}}
	if !restricted.CanBeCalledBy("pm") {
		t.Fatal("白名单内的调用方应允许")
	}
	if restricted.CanBeCalledBy("fe") {
		t.Fatal("白名单外的调用方应拒绝")
	}

	notCallable := agents.Agent{ID: "a3", Callable: false}
	if notCallable.CanBeCalledBy("pm") {
		t.Fatal("Callable=false 应拒绝所有调用")
	}
}

// ---------- runAgent：端到端（mock 上游） ----------

func TestRunAgentUsesTargetPersona(t *testing.T) {
	h := &Handler{}
	var systemSeen string
	turn := 0
	streamFn := func(ctx context.Context, cfg config.LLMConfig, msgs []ChatMessage, defs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error) {
		turn++
		if turn == 1 {
			systemSeen = msgs[0].Content
			// 确认 convo 里有协作上下文
			if len(msgs) < 3 {
				t.Fatalf("convo 应至少有 3 条消息（人格 + 上下文 + 任务），实际 %d", len(msgs))
			}
			if !strings.Contains(msgs[1].Content, "协作上下文") {
				t.Fatalf("第二条应是协作上下文: %q", msgs[1].Content)
			}
			return "做完了", nil, nil
		}
		return "", nil, nil
	}

	target := tools.AgentRef{ID: "fe01", Name: "前端", SystemPrompt: "你是前端专家"}
	out, err := h.runAgent(
		context.Background(), &tools.Env{Workspace: t.TempDir()},
		tools.Defs(tools.Builtin(), nil), streamFn, config.LLMConfig{}, "",
		target, "写个页面", tools.CallOpts{CallerID: "pm01", CallerName: "产品经理", Depth: 1},
		newCallGraph("pm01"), newBudgetTracker(5),
	)
	if err != nil {
		t.Fatal(err)
	}
	if systemSeen != "你是前端专家" {
		t.Fatalf("应使用目标 agent 的人格提示词: %q", systemSeen)
	}
	if !strings.Contains(out, "做完了") {
		t.Fatalf("汇报异常: %q", out)
	}
	if !strings.Contains(out, "前端") {
		t.Fatalf("汇报应标注来源 agent: %q", out)
	}
}

func TestRunAgentDepthCapBlocksRecursion(t *testing.T) {
	// 顶层 env.CallAgent 在 depth >= MaxAgentDepth 时必须拒绝，
	// 这是 handler 层的拦截；这里验证 runAgent 拿到的工具集已剔除递归入口。
	defs := tools.SubagentDefs(tools.Defs(tools.Builtin(), nil), "general-purpose", tools.MaxAgentDepth)
	for _, d := range defs {
		if d.Function.Name == "agent_call" {
			t.Fatal("到深度上限时工具集不应含 agent_call")
		}
	}
}

// ---------- workspaceLocks ----------

func TestWorkspaceLocksIsolatedByWorkspace(t *testing.T) {
	w := newWorkspaceLocks()
	wsA := t.TempDir()
	wsB := t.TempDir()

	w.lock(wsA)
	if !w.tryLock(wsB) {
		t.Fatal("不同 workspace 不应互相阻塞")
	}
	w.unlock(wsA)
	w.unlock(wsB)
}

func TestWorkspaceLocksMutualExclusion(t *testing.T) {
	w := newWorkspaceLocks()
	ws := t.TempDir()
	w.lock(ws)
	if w.tryLock(ws) {
		t.Fatal("同一 workspace 应互斥")
	}
	w.unlock(ws)
	if !w.tryLock(ws) {
		t.Fatal("释放后应能再次获取")
	}
	w.unlock(ws)
}
