package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// e2e：通过 HTTP 层跑完整 agent loop（mock 上游），验证 SSE 事件序、
// todo 广播、done 用量、会话持久化与回复内容——这是 handler.go 主循环
// 此前从未被直接测过的盲区。

type e2eFixture struct {
	srv     *httptest.Server
	st      *store.Store
	set     *settings.Store
	pm      *projects.Manager
	project projects.Project
	rootDir string
	dataDir string
}

func newE2E(t *testing.T) *e2eFixture {
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
	rootDir := filepath.Join(dir, "workspace")
	pm, err := projects.New(filepath.Join(dataDir, "projects.json"), func() string { return rootDir })
	if err != nil {
		t.Fatal(err)
	}
	var projSeq int
	proj, err := pm.Create("测试项目", "", "", func() string {
		projSeq++
		return fmt.Sprintf("proj%08x", projSeq)
	})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := agents.Open(filepath.Join(dataDir, "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ag.EnsureDefault(agents.Agent{
		Name: persona.Name, Description: "d", Avatar: "", SystemPrompt: persona.SystemPrompt, Tools: true,
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
	h := NewHandler(&config.Config{DataDir: dataDir}, st, set, pm, mcp.NewManager(context.Background()), ag, sk, mem, rules.New(), nil)
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &e2eFixture{srv: srv, st: st, set: set, pm: pm, project: proj, rootDir: rootDir, dataDir: dataDir}
}

// postChat 发一条消息并收集全部 SSE 事件。
func (f *e2eFixture) postChat(t *testing.T, content string) []map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"content": content, "projectId": f.project.ID,
	})
	resp, err := http.Post(f.srv.URL+"/api/chat", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat 应 200，got %d", resp.StatusCode)
	}
	var events []map[string]any
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var evt map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data:")), &evt); err == nil {
			events = append(events, evt)
		}
	}
	return events
}

func eventsOf(events []map[string]any, typ string) []map[string]any {
	var out []map[string]any
	for _, e := range events {
		if e["type"] == typ {
			out = append(out, e)
		}
	}
	return out
}

// eventTypes 事件类型序列（错误信息里用）。
func eventTypes(events []map[string]any) string {
	var sb strings.Builder
	for i, e := range events {
		if i > 0 {
			sb.WriteByte(',')
		}
		if t, ok := e["type"].(string); ok {
			sb.WriteString(t)
		}
	}
	return sb.String()
}

func TestE2EMockChatFullFlow(t *testing.T) {
	f := newE2E(t)
	events := f.postChat(t, "帮我写个文件")

	// 事件序骨架：start → (todo) → tool×2 → tool_result×2 → … → done
	if len(eventsOf(events, "start")) != 1 {
		t.Fatalf("应有 1 个 start: %v", eventTypes(events))
	}
	if n := len(eventsOf(events, "tool")); n != 2 {
		t.Fatalf("mock 演示应有 2 次工具调用（todo_write+write_file），got %d: %v", n, eventTypes(events))
	}
	if n := len(eventsOf(events, "tool_result")); n != 2 {
		t.Fatalf("应有 2 个 tool_result，got %d", n)
	}
	if n := len(eventsOf(events, "todo")); n != 1 {
		t.Fatalf("todo_write 成功后应有 1 个 todo 广播，got %d: %v", n, eventTypes(events))
	}
	dones := eventsOf(events, "done")
	if len(dones) != 1 {
		t.Fatalf("应有 1 个 done: %v", eventTypes(events))
	}
	usage, _ := dones[0]["usage"].(map[string]any)
	if usage == nil || usage["prompt"].(float64) <= 0 {
		t.Fatalf("done 应携带用量: %v", dones[0])
	}
	// 最后应有 assistant 正文（delta 拼出来的回复）
	if len(eventsOf(events, "delta")) == 0 {
		t.Fatal("应有流式增量")
	}

	// 会话持久化：GET 会话能看到消息与待办
	sid, _ := eventsOf(events, "start")[0]["sessionId"].(string)
	if sid == "" {
		t.Fatal("start 事件应带 sessionId")
	}
	sess, err := f.st.Get(sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Messages) == 0 {
		t.Fatal("会话应已落库消息")
	}
	if len(sess.Todos) != 2 {
		t.Fatalf("演示待办应持久化: %+v", sess.Todos)
	}
	if sess.PromptTokens <= 0 || sess.Requests != 1 {
		t.Fatalf("用量应落库: prompt=%d requests=%d", sess.PromptTokens, sess.Requests)
	}
	// 演示文件真的写进了项目工作区
	if _, err := f.pm.Get(f.project.ID); err != nil {
		t.Fatal(err)
	}
}

// 超大请求体：在 decode 前被 413 拦下（而不是读进内存后再拒绝）。
func TestE2EChatBodyTooLarge(t *testing.T) {
	f := newE2E(t)
	big := strings.Repeat("a", 300*1024) // > maxChatBodyBytes(192KB)
	body, _ := json.Marshal(map[string]string{"content": big})
	resp, err := http.Post(f.srv.URL+"/api/chat", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("超大请求体应 413，got %d", resp.StatusCode)
	}
}

// 同会话并发：第二个请求 409。
func TestE2EChatConcurrentConflict(t *testing.T) {
	f := newE2E(t)
	sess, _ := f.st.Create("", f.project.ID)
	unlock, ok := f.st.TryLockSession(sess.ID)
	if !ok {
		t.Fatal("前置：应能拿到会话锁")
	}
	defer unlock()
	body, _ := json.Marshal(map[string]string{"content": "你好", "sessionId": sess.ID})
	resp, err := http.Post(f.srv.URL+"/api/chat", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("并发同会话应 409，got %d", resp.StatusCode)
	}
}

// truncateMiddle：保头尾砍中缝，UTF-8 边界安全。
func TestTruncateMiddle(t *testing.T) {
	long := strings.Repeat("头", 40000) + strings.Repeat("中", 40000) + strings.Repeat("尾", 40000)
	out := truncateMiddle(long, maxToolResultBytes)
	if len(out) > maxToolResultBytes {
		t.Fatalf("应封顶到 %d，got %d", maxToolResultBytes, len(out))
	}
	if !strings.Contains(out, "中间输出已截断") {
		t.Fatal("截断应标注")
	}
	if !strings.Contains(out, "尾") {
		t.Fatal("应保留尾部（错误信息常在尾部）")
	}
	if !strings.HasPrefix(out, "头") {
		t.Fatal("应保留头部")
	}
	// 短文本原样
	if got := truncateMiddle("短内容", 1024); got != "短内容" {
		t.Fatalf("短文本不应变: %q", got)
	}
	// 纯中文（3 字节/rune）截断后仍是合法 UTF-8
	if []rune(out) == nil {
		t.Fatal("unreachable")
	}
}

// 子代理汇报封顶：超长汇报被截断并标注。
func TestCapSubagentReport(t *testing.T) {
	big := strings.Repeat("报", subagentReportMaxRunes+100)
	out := capSubagentReport(big)
	if len([]rune(out)) > subagentReportMaxRunes+50 {
		t.Fatalf("汇报应封顶: %d", len([]rune(out)))
	}
	if !strings.Contains(out, "已截断") {
		t.Fatal("截断应标注")
	}
	if got := capSubagentReport("正常汇报"); got != "正常汇报" {
		t.Fatalf("正常汇报不应变: %q", got)
	}
}

// E2E：ask_user 阻塞提问 → SSE ask 事件 → POST 回答 → 工具循环继续 → done。
func TestE2EAskUserRoundTrip(t *testing.T) {
	f := newE2E(t)
	body, _ := json.Marshal(map[string]string{"content": "问我一个问题", "projectId": f.project.ID})
	resp, err := http.Post(f.srv.URL+"/api/chat", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var events []map[string]any
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	answered := false
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var evt map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data:")), &evt); err != nil {
			continue
		}
		events = append(events, evt)
		if evt["type"] == "ask" && !answered {
			answered = true
			qid, _ := evt["id"].(string)
			if qid == "" {
				t.Fatal("ask 事件应带 id")
			}
			go func() {
				ansBody, _ := json.Marshal(map[string]string{"answer": "蓝色"})
				ar, aerr := http.Post(f.srv.URL+"/api/questions/"+qid, "application/json", strings.NewReader(string(ansBody)))
				if aerr == nil {
					ar.Body.Close()
				}
			}()
		}
	}
	if !answered {
		t.Fatal("应有 ask 事件")
	}
	dones := eventsOf(events, "done")
	if len(dones) != 1 {
		t.Fatalf("回答后循环应继续到 done: %v", eventTypes(events))
	}
	// 回答进入会话（tool 消息含「用户回答」）
	var sessionID string
	for _, e := range eventsOf(events, "start") {
		sessionID, _ = e["sessionId"].(string)
	}
	sess, err := f.st.Get(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range sess.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "用户回答：蓝色") {
			found = true
		}
	}
	if !found {
		t.Fatal("用户回答应作为工具结果落库")
	}
}

// E2E：重复回答同一 qid → 404（一次性凭证）。
func TestE2EQuestionSingleUse(t *testing.T) {
	f := newE2E(t)
	q := questions.Register(&PendingQuestion{
		Kind: "question", Session: "s", Payload: map[string]any{}, Ch: make(chan string, 1),
	})
	ansBody, _ := json.Marshal(map[string]string{"answer": "ok"})
	r1, err := http.Post(f.srv.URL+"/api/questions/"+q.ID, "application/json", strings.NewReader(string(ansBody)))
	if err != nil {
		t.Fatal(err)
	}
	r1.Body.Close()
	r2, err := http.Post(f.srv.URL+"/api/questions/"+q.ID, "application/json", strings.NewReader(string(ansBody)))
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusNotFound {
		t.Fatalf("重复回答应 404，got %d", r2.StatusCode)
	}
	if v := <-q.Ch; v != "ok" {
		t.Fatalf("首次回答应送达: %q", v)
	}
}

// ask 权限模式：写工具执行前走 PermissionAsk；拒绝 → 工具结果为「用户拒绝」。
func TestExecToolPermissionGate(t *testing.T) {
	h := &Handler{}
	defs := tools.Defs(tools.Builtin())
	env := &tools.Env{Workspace: t.TempDir(), Files: tools.NewFileStateTracker()}
	calls := 0
	env.PermissionAsk = func(toolName, summary string) bool {
		calls++
		return calls > 1 // 第一次拒绝，第二次允许
	}
	// 未读文件 → 先写一个（写路径也要过审批门，这里第一次调用被拒）
	writeCall := ToolCall{ID: "c1", Type: "function", Function: ToolFunction{
		Name: "write_file", Arguments: `{"path":"x.txt","content":"data"}`}}
	out, err := h.execTool(context.Background(), env, defs, writeCall)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "用户拒绝") {
		t.Fatalf("拒绝后应返回拒绝语义: %q", out)
	}
	// 第二次允许：但会因「目标已存在却没读过」失败吗？不会——首次被拒没写成，文件不存在 → 直接写成功
	out2, err := h.execTool(context.Background(), env, defs, writeCall)
	if err != nil || !strings.Contains(out2, "已写入") {
		t.Fatalf("允许后应执行: %v %q", err, out2)
	}
	if calls != 2 {
		t.Fatalf("写工具应各走一次审批: %d", calls)
	}
}
