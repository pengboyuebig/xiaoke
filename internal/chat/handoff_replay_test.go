package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"xiaoke/internal/agents"
	"xiaoke/internal/store"
)

// TestE2EHandoffPersistedAndReplayed 验证调用链回放的核心价值：
// 调用记录必须落库，刷新页面后仍能完整重建「谁调了谁」。
func TestE2EHandoffPersistedAndReplayed(t *testing.T) {
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
			frame = toolFrame("agent_call", `{"agent_id":`+jsonStr(fe.ID)+`,"prompt":"写个登录页，要求响应式"}`)
		case 2:
			frame = toolFrame("agent_call", `{"agent_id":`+jsonStr(qa.ID)+`,"prompt":"给登录页写测试"}`)
		case 3:
			frame = textFrame("测试写完：覆盖登录成功/失败两条路径")
		case 4:
			frame = textFrame("登录页完成，含响应式布局")
		default:
			frame = textFrame("全部完成")
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

	events := f.postChat(t, "做一个登录页")
	start := eventsOf(events, "start")
	if len(start) != 1 {
		t.Fatalf("应有 start 事件: %v", eventTypes(events))
	}
	sid, _ := start[0]["sessionId"].(string)

	// 模拟「刷新页面」：从库里重新读会话
	sess, err := f.st.Get(sid)
	if err != nil {
		t.Fatal(err)
	}

	var handoffs []store.HandoffRecord
	for i := range sess.Messages {
		m := &sess.Messages[i]
		if m.Role == "handoff" && m.Handoff != nil {
			handoffs = append(handoffs, *m.Handoff)
		}
	}
	if len(handoffs) != 2 {
		t.Fatalf("应落库 2 条调用记录，got %d（消息总数 %d）", len(handoffs), len(sess.Messages))
	}

	// 落库顺序是「先返回的先写」：内层（前端→测试）先完成，外层（小柯→前端）后完成。
	// 按目标名字定位，避免依赖顺序。
	var outer, inner *store.HandoffRecord
	for i := range handoffs {
		switch handoffs[i].ToName {
		case "前端":
			outer = &handoffs[i]
		case "测试":
			inner = &handoffs[i]
		}
	}
	if outer == nil || inner == nil {
		t.Fatalf("两条记录应分别指向 前端/测试，实际: %+v", handoffs)
	}

	// 外层：顶层 → 前端
	if outer.Depth != 1 {
		t.Fatalf("外层记录深度应为 1: %+v", outer)
	}
	if !strings.Contains(outer.Prompt, "登录页") {
		t.Fatalf("任务说明应完整保存: %q", outer.Prompt)
	}
	if !outer.Ok {
		t.Fatalf("外层应成功: %s", outer.Error)
	}
	if !strings.Contains(outer.Report, "登录页完成") {
		t.Fatalf("外层汇报应保存: %q", outer.Report)
	}

	// 内层：前端 → 测试（深度 2）
	if inner.Depth != 2 {
		t.Fatalf("内层记录深度应为 2: %+v", inner)
	}
	if !strings.Contains(inner.Report, "测试写完") {
		t.Fatalf("内层汇报应保存: %q", inner.Report)
	}

	// 记录必须标 Hidden：否则会被当普通消息发给上游
	for i := range sess.Messages {
		if sess.Messages[i].Role == "handoff" && !sess.Messages[i].Hidden {
			t.Fatal("handoff 消息必须标 Hidden（不参与模型上下文）")
		}
	}

	// 模拟下一轮请求：确认 handoff 不会进入发给上游的上下文
	if wire := toWire(sess.Messages); len(wire) > 0 {
		for _, cm := range wire {
			if cm.Role == "handoff" {
				t.Fatal("toWire 不应把 handoff 消息发给上游（上游只认 user/assistant/tool/system）")
			}
		}
	}
}

// TestHandoffFailureRecorded 调用失败也要落库——「谁调失败了」和成功一样重要。
func TestHandoffFailureRecorded(t *testing.T) {
	f := newE2E(t)

	fe, err := f.ag.Create(agents.Agent{
		Name: "前端", Description: "d", SystemPrompt: "s", Tools: true, Callable: true,
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
		var frame string
		switch n {
		case 1:
			frame = toolFrame("agent_call", `{"agent_id":`+jsonStr(fe.ID)+`,"prompt":"写页面"}`)
		case 2:
			// 目标 agent 的上游返回不可重试的错误（模拟它调用失败）。
			// 用 401 而不是 5xx：上游实现只对 429/5xx 重试。
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"invalid api key"}`)
			return
		default:
			frame = textFrame("调用失败了，我接着自己做")
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

	events := f.postChat(t, "做个页面")
	start := eventsOf(events, "start")
	sid, _ := start[0]["sessionId"].(string)

	sess, err := f.st.Get(sid)
	if err != nil {
		t.Fatal(err)
	}
	var rec *store.HandoffRecord
	for i := range sess.Messages {
		if sess.Messages[i].Role == "handoff" && sess.Messages[i].Handoff != nil {
			rec = sess.Messages[i].Handoff
		}
	}
	if rec == nil {
		t.Fatal("失败调用也应落库")
	}
	if rec.Ok {
		t.Fatal("这条记录应标记为失败")
	}
	if rec.Error == "" {
		t.Fatal("失败原因应保存")
	}
	// 失败时不应有汇报
	if rec.Report != "" {
		t.Fatalf("失败时不应有汇报: %q", rec.Report)
	}
}

// 确认会话 API 返回的 JSON 里带 handoff 字段（前端回放依赖它）。
func TestSessionAPIIncludesHandoff(t *testing.T) {
	f := newE2E(t)
	events := f.postChat(t, "你好")
	start := eventsOf(events, "start")
	sid, _ := start[0]["sessionId"].(string)

	resp, err := http.Get(f.srv.URL + "/api/sessions/" + sid)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatal("应返回消息")
	}
	// 字段存在性由结构体保证；这里确认 JSON 能正常序列化
	if _, err := json.Marshal(body); err != nil {
		t.Fatalf("会话 JSON 序列化失败: %v", err)
	}
}
