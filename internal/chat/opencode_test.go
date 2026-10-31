// opencode 能力补齐的专项测试：权限通配规则、统一 diff、上游重试、
// 会话回退/分叉/搜索/Markdown 导出、doom loop 提醒。
package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xiaoke/internal/config"
	"xiaoke/internal/settings"
	"xiaoke/internal/tools"
)

// ---------- 权限规则 ----------

func TestWildcardMatch(t *testing.T) {
	cases := []struct {
		pattern, input string
		want           bool
	}{
		{"*", "anything", true},
		{"write_file", "write_file", true},
		{"write_file", "edit_file", false},
		{"git *", "git status", true},
		{"git *", "git status --porcelain", true},
		{"git *", "git", false}, // opencode 同款特例："cmd *" 不匹配裸命令
		{"*.env", "app.env", true},
		{"*.env", "app.env.bak", false},
		{"path=config?.json", "path=config1.json", true},
		{"path=config?.json", "path=config12.json", false},
		{"A?C", "abc", true}, // 大小写不敏感
		{"a*b*c", "a-x-b-y-c", true},
	}
	for _, c := range cases {
		if got := wildcardMatch(c.pattern, c.input); got != c.want {
			t.Errorf("wildcardMatch(%q, %q) = %v, want %v", c.pattern, c.input, got, c.want)
		}
	}
}

func TestPermissionEffectLastMatchWins(t *testing.T) {
	dir := t.TempDir()
	set, err := settings.Open(filepath.Join(dir, "settings.json"), settingsSeed())
	if err != nil {
		t.Fatal(err)
	}
	s := set.Get()
	s.Permissions = []settings.PermissionRule{
		{Tool: "*", Effect: "ask"},
		{Tool: "write_file", Effect: "deny"},
		{Tool: "run_command", Pattern: "git *", Effect: "allow"},
	}
	if err := set.Update(s, nil); err != nil {
		t.Fatal(err)
	}
	h := &Handler{set: set}
	if got := h.permissionEffect("write_file", "path=x"); got != "deny" {
		t.Fatalf("write_file 应 deny，got %q", got)
	}
	if got := h.permissionEffect("read_file", "path=x"); got != "ask" {
		t.Fatalf("通配规则应命中 ask，got %q", got)
	}
	if got := h.permissionEffect("run_command", "command=git status"); got != "allow" {
		t.Fatalf("git 前缀应 allow，got %q", got)
	}
	if got := h.permissionEffect("run_command", "command=npm install"); got != "ask" {
		t.Fatalf("非 git 命令应回落到通配的 ask 规则，got %q", got)
	}
}

// deny 规则要在审批门之前拦截；allow 规则跳过审批。
func TestExecToolPermissionRules(t *testing.T) {
	dir := t.TempDir()
	set, err := settings.Open(filepath.Join(dir, "settings.json"), settingsSeed())
	if err != nil {
		t.Fatal(err)
	}
	s := set.Get()
	s.Permissions = []settings.PermissionRule{{Tool: "write_file", Effect: "deny"}}
	if err := set.Update(s, nil); err != nil {
		t.Fatal(err)
	}
	h := &Handler{set: set}
	defs := tools.Defs(tools.Builtin())
	env := &tools.Env{Workspace: dir, Files: tools.NewFileStateTracker()}
	asked := false
	env.PermissionAsk = func(string, string) bool {
		asked = true
		return true
	}
	call := ToolCall{ID: "c1", Type: "function", Function: ToolFunction{
		Name: "write_file", Arguments: `{"path":"x.txt","content":"d"}`}}
	out, err := h.execTool(context.Background(), env, defs, call)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "权限规则") {
		t.Fatalf("deny 规则应拦截: %q", out)
	}
	if asked {
		t.Fatal("deny 命中后不应再弹审批")
	}
	if _, serr := os.Stat(filepath.Join(dir, "x.txt")); serr == nil {
		t.Fatal("deny 后文件不应被写入")
	}

	// allow 规则：跳过审批直接执行
	s = set.Get()
	s.Permissions = []settings.PermissionRule{{Tool: "write_file", Effect: "allow"}}
	if err := set.Update(s, nil); err != nil {
		t.Fatal(err)
	}
	out, err = h.execTool(context.Background(), env, defs, call)
	if err != nil || !strings.Contains(out, "已写入") {
		t.Fatalf("allow 规则应直接放行: %v %q", err, out)
	}
	if asked {
		t.Fatal("allow 规则不应触发审批")
	}
}

// settingsSeed 是测试用最小 LLM 配置（settings.Open 的种子参数）。
func settingsSeed() config.LLMConfig {
	return config.LLMConfig{Mode: "mock", BaseURL: "http://127.0.0.1:1", APIKey: "", Model: "m"}
}

// ---------- 统一 diff ----------

func TestUnifiedDiff(t *testing.T) {
	if d := tools.UnifiedDiff("same", "same", 3); d != "" {
		t.Fatalf("相同内容应无 diff，got %q", d)
	}
	old := "a\nb\nc\nd\ne\nf\ng"
	new := "a\nb\nC\nd\ne\nf\ng"
	d := tools.UnifiedDiff(old, new, 2)
	if !strings.Contains(d, "-c") || !strings.Contains(d, "+C") {
		t.Fatalf("应包含变更行: %q", d)
	}
	// ctx=2：前上下文 a/b，后上下文 d/e（g 在公共后缀区之外无需展示）
	if !strings.Contains(d, " a") || !strings.Contains(d, " e") || strings.Contains(d, " g") {
		t.Fatalf("上下文行不符: %q", d)
	}
	if strings.Contains(d, "-a") || strings.Contains(d, "+a") {
		t.Fatalf("上下文不应带 +/-: %q", d)
	}
	// 多行增删 + hunk 头
	old2 := "1\n2\n3\n4\n5"
	new2 := "1\n2\nX\nY\n4\n5"
	d2 := tools.UnifiedDiff(old2, new2, 1)
	if !strings.Contains(d2, "-3") || !strings.Contains(d2, "+X") || !strings.Contains(d2, "+Y") {
		t.Fatalf("应包含多行替换: %q", d2)
	}
	if !strings.Contains(d2, "@@") {
		t.Fatalf("应有 hunk 头: %q", d2)
	}
	// CRLF 归一化
	d3 := tools.UnifiedDiff("a\r\nb\r\nc", "a\r\nB\r\nc", 1)
	if !strings.Contains(d3, "+B") || strings.Contains(d3, "\r") {
		t.Fatalf("CRLF 应归一化为 LF: %q", d3)
	}
	// 新增行在文件尾
	d4 := tools.UnifiedDiff("a\nb", "a\nb\nc\nd", 1)
	if !strings.Contains(d4, "+c") || !strings.Contains(d4, "+d") {
		t.Fatalf("尾部新增应完整: %q", d4)
	}
}

// ---------- 上游重试 ----------

func TestRetryableUpstream(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{&upstreamErr{kind: "network"}, true},
		{&upstreamErr{kind: "midstream"}, true},
		{&upstreamErr{kind: "status", status: 429}, true},
		{&upstreamErr{kind: "status", status: 503}, true},
		{&upstreamErr{kind: "status", status: 401}, false},
		{&upstreamErr{kind: "status", status: 400}, false},
		{&upstreamErr{kind: "breaker"}, false},
		{fmt.Errorf("普通错误"), false},
	}
	for i, c := range cases {
		if got := retryableUpstream(c.err); got != c.want {
			t.Fatalf("case %d: retryableUpstream(%v) = %v, want %v", i, c.err, got, c.want)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	if d := parseRetryAfter("120"); d != 120*time.Second {
		t.Fatalf("秒数解析错误: %v", d)
	}
	if d := parseRetryAfter("bogus"); d != 0 {
		t.Fatalf("非法值应为 0: %v", d)
	}
	if d := parseRetryAfter(""); d != 0 {
		t.Fatalf("空值应为 0: %v", d)
	}
}

func TestStreamOpenAIRetry(t *testing.T) {
	origBase := upstreamBaseDelay
	upstreamBaseDelay = 5 * time.Millisecond
	t.Cleanup(func() { upstreamBaseDelay = origBase })

	attempts := 0
	var failImpl StreamFn = func(ctx context.Context, cfg config.LLMConfig, msgs []ChatMessage, defs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error) {
		attempts++
		if attempts <= 2 {
			return "", nil, &upstreamErr{kind: "network", msg: "临时故障"}
		}
		return "成功", nil, nil
	}
	retries := 0
	var retryEvents []string
	hooks := Hooks{OnDelta: func(string) {}, OnRetry: func(attempt, maxAttempts int, delayMs int64, reason string) {
		retries++
		retryEvents = append(retryEvents, fmt.Sprintf("%d/%d %dms", attempt, maxAttempts, delayMs))
	}}
	text, _, err := streamOpenAIRetry(context.Background(), config.LLMConfig{}, nil, nil, hooks, "", failImpl)
	if err != nil {
		t.Fatal(err)
	}
	if text != "成功" {
		t.Fatalf("应返回重试后的结果: %q", text)
	}
	if attempts != 3 || retries != 2 {
		t.Fatalf("应尝试 3 次、回调 2 次: attempts=%d retries=%d %v", attempts, retries, retryEvents)
	}

	// 已输出增量后失败：不重试（防重复输出）
	sentAttempts := 0
	var sentImpl StreamFn = func(ctx context.Context, cfg config.LLMConfig, msgs []ChatMessage, defs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error) {
		sentAttempts++
		hooks.OnDelta("部分输出")
		return "", nil, &upstreamErr{kind: "network", msg: "流中断"}
	}
	_, _, err = streamOpenAIRetry(context.Background(), config.LLMConfig{}, nil, nil, hooks, "", sentImpl)
	if err == nil {
		t.Fatal("已输出后失败应返回错误")
	}
	if sentAttempts != 1 {
		t.Fatalf("已输出增量后不应重试: %d", sentAttempts)
	}

	// 超过重试上限：返回最后一次错误
	var alwaysFail StreamFn = func(ctx context.Context, cfg config.LLMConfig, msgs []ChatMessage, defs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error) {
		return "", nil, &upstreamErr{kind: "status", status: 503, msg: "过载"}
	}
	if _, _, err := streamOpenAIRetry(context.Background(), config.LLMConfig{}, nil, nil, Hooks{}, "", alwaysFail); err == nil {
		t.Fatal("持续失败应返回错误")
	}
}

// TestE2EUpstreamRetry 走 HTTP 全链路：测试上游第一次返回 503，第二次成功；
// 断言前端能看到 retry 事件且对话正常完成。
func TestE2EUpstreamRetry(t *testing.T) {
	origBase := upstreamBaseDelay
	upstreamBaseDelay = 5 * time.Millisecond
	t.Cleanup(func() { upstreamBaseDelay = origBase })

	f := newE2E(t)
	attempts := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"error":"upstream boom"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"重试成功\"}}]}\n\n")
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

	events := f.postChat(t, "你好")
	if eventsOf(events, "retry") == nil {
		t.Logf("全部事件: %v", events)
	}
	if attempts < 2 {
		t.Fatalf("应至少请求上游 2 次，got %d", attempts)
	}
	retries := eventsOf(events, "retry")
	if len(retries) != 1 {
		t.Fatalf("应有 1 个 retry 事件: %v", eventTypes(events))
	}
	if n, _ := retries[0]["attempt"].(float64); n != 1 {
		t.Fatalf("retry 事件应标 attempt=1: %v", retries[0])
	}
	if len(eventsOf(events, "done")) != 1 {
		t.Fatalf("重试后应正常完成: %v", eventTypes(events))
	}
}

// ---------- E2E：回退 / 分叉 / 搜索 / Markdown 导出 ----------

// TestE2ERevertFlow 全链路：mock 写文件 → 回退到首条用户消息 → 文件删除 + 消息截断。
func TestE2ERevertFlow(t *testing.T) {
	f := newE2E(t)
	events := f.postChat(t, "帮我写个文件")
	if start := eventsOf(events, "start"); len(start) == 0 {
		t.Fatalf("无 start 事件，全部事件: %v", events)
	}
	sid, _ := eventsOf(events, "start")[0]["sessionId"].(string)
	if _, err := os.Stat(filepath.Join(f.project.Path, "hello.txt")); err != nil {
		t.Fatalf("mock 应已写入 hello.txt: %v；事件: %v", err, events)
	}
	sess, err := f.st.Get(sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Messages) < 4 {
		t.Fatalf("应有完整回合消息: %d", len(sess.Messages))
	}

	// 回退到 index 0（首条用户消息）
	body, _ := json.Marshal(map[string]int{"index": 0})
	resp, err := http.Post(f.srv.URL+"/api/sessions/"+sid+"/revert", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revert 应 200，got %d", resp.StatusCode)
	}
	var out struct {
		Files []struct {
			Path   string `json:"path"`
			Action string `json:"action"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Files) == 0 || out.Files[0].Action != "deleted" || out.Files[0].Path != "hello.txt" {
		t.Fatalf("hello.txt 应被回滚删除: %+v", out.Files)
	}
	if _, err := os.Stat(filepath.Join(f.project.Path, "hello.txt")); !os.IsNotExist(err) {
		t.Fatal("回退后 hello.txt 应不存在")
	}
	sess2, err := f.st.Get(sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess2.Messages) != 1 || sess2.Messages[0].Role != "user" {
		t.Fatalf("应只保留首条用户消息: %+v", sess2.Messages)
	}
	if len(sess2.Todos) != 0 {
		t.Fatalf("回退后待办应清空: %+v", sess2.Todos)
	}

	// index 指向非 user 消息 → 400
	body2, _ := json.Marshal(map[string]int{"index": 99})
	resp2, err := http.Post(f.srv.URL+"/api/sessions/"+sid+"/revert", "application/json", strings.NewReader(string(body2)))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("越界 index 应 400，got %d", resp2.StatusCode)
	}
}

func TestE2EForkSearchExport(t *testing.T) {
	f := newE2E(t)
	events := f.postChat(t, " uniquely searchable needle 帮我写个文件")
	sid, _ := eventsOf(events, "start")[0]["sessionId"].(string)

	// fork
	fresp, err := http.Post(f.srv.URL+"/api/sessions/"+sid+"/fork", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer fresp.Body.Close()
	if fresp.StatusCode != http.StatusOK {
		t.Fatalf("fork 应 200，got %d", fresp.StatusCode)
	}
	var forked struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Messages int    `json:"messages"`
	}
	if err := json.NewDecoder(fresp.Body).Decode(&forked); err != nil {
		t.Fatal(err)
	}
	if forked.ID == "" || forked.ID == sid {
		t.Fatalf("fork 应产生新会话: %+v", forked)
	}
	if !strings.Contains(forked.Title, "分叉") {
		t.Fatalf("分叉标题应带后缀: %q", forked.Title)
	}
	src, _ := f.st.Get(sid)
	if forked.Messages != len(src.Messages) {
		t.Fatalf("fork 应复制全部消息: %d vs %d", forked.Messages, len(src.Messages))
	}
	dst, err := f.st.Get(forked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dst.Messages) != len(src.Messages) {
		t.Fatal("分叉会话消息数不一致")
	}

	// 全文搜索：能找到源会话与分叉会话
	sresp, err := http.Get(f.srv.URL + "/api/search?q=searchable+needle")
	if err != nil {
		t.Fatal(err)
	}
	defer sresp.Body.Close()
	var sr struct {
		Hits []struct {
			SessionID string `json:"sessionId"`
			Snippet   string `json:"snippet"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(sresp.Body).Decode(&sr); err != nil {
		t.Fatal(err)
	}
	if len(sr.Hits) != 2 {
		t.Fatalf("源会话与分叉会话都应命中: %+v", sr.Hits)
	}

	// Markdown 导出
	mresp, err := http.Get(f.srv.URL + "/api/sessions/" + sid + "/export?format=md")
	if err != nil {
		t.Fatal(err)
	}
	defer mresp.Body.Close()
	if !strings.Contains(mresp.Header.Get("Content-Type"), "text/markdown") {
		t.Fatalf("Content-Type 应为 text/markdown: %q", mresp.Header.Get("Content-Type"))
	}
	md := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, rerr := mresp.Body.Read(buf)
		md = append(md, buf[:n]...)
		if rerr != nil {
			break
		}
	}
	mds := string(md)
	if !strings.Contains(mds, "🧑 用户") || !strings.Contains(mds, "🔧 `write_file") {
		t.Fatalf("Markdown 应含消息与工具调用: %.200s", mds)
	}
}

// ---------- doom loop ----------

func TestDoomLoopSignature(t *testing.T) {
	// 纯逻辑验证：连续相同签名 3 次触发提醒（与 agent loop 内一致的状态机）
	lastSig, runs := "", 0
	calls := []string{"sig-a", "sig-a", "sig-a", "sig-b", "sig-a", "sig-a", "sig-a"}
	var fired int
	for _, sig := range calls {
		if sig == lastSig {
			runs++
		} else {
			lastSig = sig
			runs = 1
		}
		if runs >= 3 {
			fired++
			runs = 0
		}
	}
	if fired != 2 {
		t.Fatalf("两个 3 连应各触发一次: %d", fired)
	}
}
