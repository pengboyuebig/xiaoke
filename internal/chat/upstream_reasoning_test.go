package chat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"xiaoke/internal/config"
	"xiaoke/internal/tools"
)

// sseServer 起一个最简 SSE 上游：按给定 chunk 列表逐条下发，最后 [DONE]。
// chunks 为空表示「完全空响应」。
func sseServer(t *testing.T, chunks []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			if fl != nil {
				fl.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if fl != nil {
			fl.Flush()
		}
	}))
}

func TestParseChunkReasoning(t *testing.T) {
	payload := `{"choices":[{"delta":{"reasoning_content":"思考中"}}]}`
	content, _, reasoning, _, _ := parseChunk(payload)
	if content != "" {
		t.Fatalf("推理不应进入正文: %q", content)
	}
	if reasoning != "思考中" {
		t.Fatalf("应取到 reasoning_content: %q", reasoning)
	}
}

func TestParseChunkReasoningFallback(t *testing.T) {
	// 部分网关用 reasoning 而非 reasoning_content
	payload := `{"choices":[{"delta":{"reasoning":"备用字段"}}]}`
	_, _, reasoning, _, _ := parseChunk(payload)
	if reasoning != "备用字段" {
		t.Fatalf("应回退到 reasoning: %q", reasoning)
	}
}

func TestParseChunkReasoningAndContentTogether(t *testing.T) {
	payload := `{"choices":[{"delta":{"content":"正文","reasoning_content":"思考"}}]}`
	content, _, reasoning, _, _ := parseChunk(payload)
	if content != "正文" || reasoning != "思考" {
		t.Fatalf("正文与推理应分别返回: content=%q reasoning=%q", content, reasoning)
	}
}

// 只有推理、没有正文也没有工具调用：不是上游故障，返回空内容让上层收尾。
func TestStreamOpenAIReasoningOnly(t *testing.T) {
	srv := sseServer(t, []string{
		`{"choices":[{"delta":{"reasoning_content":"先看看目录"}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"结构"}}]}`,
		`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2}}`,
	})
	defer srv.Close()

	var deltas int
	text, calls, err := streamOpenAI(context.Background(), config.LLMConfig{
		Mode: "openai", BaseURL: srv.URL, APIKey: "k",
	}, []ChatMessage{{Role: "user", Content: "hi"}}, nil,
		Hooks{OnDelta: func(string) { deltas++ }}, "")
	if err != nil {
		t.Fatalf("仅推理不应报错: %v", err)
	}
	if text != "" || len(calls) != 0 {
		t.Fatalf("应返回空内容与无调用: text=%q calls=%d", text, len(calls))
	}
	if deltas != 0 {
		t.Fatalf("推理内容不应外发给前端: %d 次 OnDelta", deltas)
	}
}

// 完全空响应：应包装成可重试的 upstreamErr{kind:"empty"}。
func TestStreamOpenAIEmptyIsRetryable(t *testing.T) {
	srv := sseServer(t, nil)
	defer srv.Close()

	_, _, err := streamOpenAI(context.Background(), config.LLMConfig{
		Mode: "openai", BaseURL: srv.URL, APIKey: "k",
	}, []ChatMessage{{Role: "user", Content: "hi"}}, nil, Hooks{}, "")
	if err == nil {
		t.Fatal("完全空响应应报错")
	}
	var ue *upstreamErr
	if !errors.As(err, &ue) {
		t.Fatalf("应是 *upstreamErr，实际 %T: %v", err, err)
	}
	if ue.kind != "empty" {
		t.Fatalf("kind 应为 empty，实际 %q", ue.kind)
	}
	if !retryableUpstream(err) {
		t.Fatal("空响应应可重试")
	}
}

// 空响应后重试成功：复用既有重试包装，验证「empty」真的会被重试。
func TestStreamOpenAIRetryEmptyThenSuccess(t *testing.T) {
	origBase := upstreamBaseDelay
	upstreamBaseDelay = 5 * time.Millisecond
	t.Cleanup(func() { upstreamBaseDelay = origBase })

	attempts := 0
	var impl StreamFn = func(ctx context.Context, cfg config.LLMConfig, msgs []ChatMessage,
		defs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error) {
		attempts++
		if attempts == 1 {
			return "", nil, &upstreamErr{kind: "empty", msg: "空响应"}
		}
		return "成功", nil, nil
	}
	retries := 0
	hooks := Hooks{OnDelta: func(string) {}, OnRetry: func(attempt, maxAttempts int, delayMs int64, reason string) {
		retries++
	}}
	text, _, err := streamOpenAIRetry(context.Background(), config.LLMConfig{}, nil, nil, hooks, "", impl)
	if err != nil {
		t.Fatal(err)
	}
	if text != "成功" {
		t.Fatalf("应返回重试后的结果: %q", text)
	}
	if attempts != 2 || retries != 1 {
		t.Fatalf("应尝试 2 次、回调 1 次: attempts=%d retries=%d", attempts, retries)
	}
}

// 已输出增量后遇到空响应：不重试（防重复输出）。
func TestStreamOpenAIRetryEmptyAfterSent(t *testing.T) {
	origBase := upstreamBaseDelay
	upstreamBaseDelay = 5 * time.Millisecond
	t.Cleanup(func() { upstreamBaseDelay = origBase })

	attempts := 0
	hooks := Hooks{OnDelta: func(string) {}}
	var impl StreamFn = func(ctx context.Context, cfg config.LLMConfig, msgs []ChatMessage,
		defs []tools.ToolDef, h Hooks, reasoning string) (string, []ToolCall, error) {
		attempts++
		h.OnDelta("部分输出")
		return "", nil, &upstreamErr{kind: "empty", msg: "空响应"}
	}
	_, _, err := streamOpenAIRetry(context.Background(), config.LLMConfig{}, nil, nil, hooks, "", impl)
	if err == nil {
		t.Fatal("应返回错误")
	}
	if attempts != 1 {
		t.Fatalf("已输出增量后不应重试: %d 次", attempts)
	}
}

// 上游返回非法 SSE JSON 时静默跳过，不误报成「有内容」。
func TestStreamOpenAIInvalidChunksIgnored(t *testing.T) {
	srv := sseServer(t, []string{"not-json", `{"choices":[{"delta":{"content":"ok"}}]}`})
	defer srv.Close()

	text, _, err := streamOpenAI(context.Background(), config.LLMConfig{
		Mode: "openai", BaseURL: srv.URL, APIKey: "k",
	}, []ChatMessage{{Role: "user", Content: "hi"}}, nil, Hooks{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "ok") {
		t.Fatalf("合法 chunk 应被采纳: %q", text)
	}
}
