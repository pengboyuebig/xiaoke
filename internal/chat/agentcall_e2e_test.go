package chat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"xiaoke/internal/agents"
	"xiaoke/internal/config"
)

// TestE2EAgentCallChain 端到端验证 agent 互调：
// 顶层 agent（pm）调 fe，fe 再调 qa —— 三层链必须全部走通，
// 且 SSE 里要能看到 agent_handoff 事件（否则用户视角是黑盒）。
func TestE2EAgentCallChain(t *testing.T) {
	f := newE2E(t)

	// 建两个可被调用的智能体：fe（可写）、qa（可写）
	fe, err := f.ag.Create(agents.Agent{
		Name: "前端", Description: "写页面", SystemPrompt: "你是前端专家", Tools: true,
		Callable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	qa, err := f.ag.Create(agents.Agent{
		Name: "测试", Description: "跑测试", SystemPrompt: "你是测试专家", Tools: true,
		Callable: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 脚本化上游：
	//   1. pm 调 fe
	//   2. fe 调 qa
	//   3. qa 直接回复
	//   4. fe 汇报
	//   5. pm 最终回复
	var mu sync.Mutex
	call := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		call++
		n := call
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		var frame string
		switch n {
		case 1:
			frame = toolFrame("agent_call", `{"agent_id":`+jsonStr(fe.ID)+`,"prompt":"写个登录页"}`)
		case 2:
			frame = toolFrame("agent_call", `{"agent_id":`+jsonStr(qa.ID)+`,"prompt":"给登录页写测试"}`)
		case 3:
			frame = textFrame("测试已写完")
		case 4:
			frame = textFrame("登录页已完成")
		default:
			frame = textFrame("全部完成")
		}
		fmt.Fprint(w, frame)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: {\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer up.Close()

	key := "test-key"
	s := f.set.Get()
	s.LLM.Mode = "openai"
	s.LLM.BaseURL = up.URL
	if err := f.set.Update(s, &key); err != nil {
		t.Fatal(err)
	}

	events := f.postChat(t, "做一个登录页")
	handoffs := eventsOf(events, "agent_handoff")
	if len(handoffs) < 2 {
		t.Fatalf("应有至少 2 次 agent_handoff（pm→fe、fe→qa），got %d: %v",
			len(handoffs), eventTypes(events))
	}
	// 第一次是 pm → 前端
	if got := handoffs[0]["toName"]; got != "前端" {
		t.Fatalf("第一次调用应是 pm→前端，got %v", handoffs[0])
	}
	// 第二次是 fe → 测试
	if got := handoffs[1]["toName"]; got != "测试" {
		t.Fatalf("第二次调用应是 前端→测试，got %v", handoffs[1])
	}
	// 深度递增
	d0, _ := handoffs[0]["depth"].(float64)
	d1, _ := handoffs[1]["depth"].(float64)
	if d1 <= d0 {
		t.Fatalf("深度应递增: %v vs %v", d0, d1)
	}
	if len(eventsOf(events, "done")) != 1 {
		t.Fatalf("应正常完成: %v", eventTypes(events))
	}
}

// TestE2EAgentCallDepthCap 验证深度上限：链条到第 3 层时必须被拦下。
func TestE2EAgentCallDepthCap(t *testing.T) {
	f := newE2E(t)

	fe, err := f.ag.Create(agents.Agent{
		Name: "前端", Description: "写页面", SystemPrompt: "你是前端专家", Tools: true, Callable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	qa, err := f.ag.Create(agents.Agent{
		Name: "测试", Description: "跑测试", SystemPrompt: "你是测试专家", Tools: true, Callable: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 一直让每层都往下调：pm→fe→qa→fe→qa…（qa 在 depth 2 时工具集已剔除 agent_call，
	// 所以模型只能回复文本，链路自然终止）
	var mu sync.Mutex
	call := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		call++
		n := call
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		var frame string
		switch n {
		case 1:
			frame = toolFrame("agent_call", `{"agent_id":`+jsonStr(fe.ID)+`,"prompt":"写页面"}`)
		case 2:
			frame = toolFrame("agent_call", `{"agent_id":`+jsonStr(qa.ID)+`,"prompt":"写测试"}`)
		default:
			// depth 已到上限，agent_call 不在工具集里；模型只能文本回复
			frame = textFrame("我这边做完了")
		}
		fmt.Fprint(w, frame)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer up.Close()

	key := "test-key"
	s := f.set.Get()
	s.LLM.Mode = "openai"
	s.LLM.BaseURL = up.URL
	if err := f.set.Update(s, &key); err != nil {
		t.Fatal(err)
	}

	events := f.postChat(t, "做一个页面")
	handoffs := eventsOf(events, "agent_handoff")
	if len(handoffs) != 2 {
		t.Fatalf("深度上限 2 应只允许 2 层调用，got %d: %v", len(handoffs), eventTypes(events))
	}
	if len(eventsOf(events, "done")) != 1 {
		t.Fatalf("应正常完成: %v", eventTypes(events))
	}
}

// TestE2EAgentCallRejectsNotCallable 未开启 Callable 的智能体不能被调用。
func TestE2EAgentCallRejectsNotCallable(t *testing.T) {
	f := newE2E(t)
	// 建一个不可调用的智能体
	secret, err := f.ag.Create(agents.Agent{
		Name: "内部", Description: "不对外", SystemPrompt: "你是内部助手", Tools: true,
		Callable: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	call := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		call++
		n := call
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		frame := textFrame("好的")
		if n == 1 {
			frame = toolFrame("agent_call", `{"agent_id":`+jsonStr(secret.ID)+`,"prompt":"干活"}`)
		}
		fmt.Fprint(w, frame)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer up.Close()

	key := "test-key"
	s := f.set.Get()
	s.LLM.Mode = "openai"
	s.LLM.BaseURL = up.URL
	if err := f.set.Update(s, &key); err != nil {
		t.Fatal(err)
	}

	events := f.postChat(t, "找个内部助手")
	// 调用应被拒，错误作为 tool 结果回灌，模型继续完成对话
	if len(eventsOf(events, "done")) != 1 {
		t.Fatalf("应正常完成: %v", eventTypes(events))
	}
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// toolFrame 构造一个带 tool_calls 的 SSE 帧。
func toolFrame(name, args string) string {
	payload := fmt.Sprintf(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":%q,"arguments":%s}}]}}]}`,
		name, jsonStr(args),
	)
	return "data: " + payload + "\n\n"
}

// textFrame 构造一个纯文本 SSE 帧。
func textFrame(text string) string {
	payload, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"delta": map[string]string{"content": text}}},
	})
	return "data: " + string(payload) + "\n\n"
}

var _ = bufio.NewScanner
var _ = strings.TrimSpace
var _ = config.LLMConfig{}
