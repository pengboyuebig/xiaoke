// Package chat 实现「小柯」的对话核心：OpenAI 兼容流式请求（含 tool_calls）、
// mock 演示上游，以及把工具调用循环串起来的 agent 逻辑。
package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"xiaoke/internal/config"
	"xiaoke/internal/tools"
)

// ToolCall 与 OpenAI 的 tool_calls 元素同构。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type,omitempty"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatMessage 是发给上游的消息（OpenAI 格式，含工具角色）。
type ChatMessage struct {
	Role       string     `json:"role"` // system|user|assistant|tool
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolDelta 是流式过程中一段 tool_calls 增量（按 Index 累加）。
type ToolDelta struct {
	Index    int
	ID       string
	Name     string
	ArgDelta string
}

// Hooks 是流式回调：文本增量 + 工具调用增量。
type Hooks struct {
	OnDelta    func(string)
	OnToolCall func(ToolDelta)
}

// validReasoning 是请求级思考程度的合法取值。
var validReasoning = map[string]bool{
	"minimal": true, "low": true, "medium": true, "high": true,
}

// normalizeReasoning 清洗请求级思考程度：未知值一律丢弃（记日志），
// 而不是原样透传——上游对非法值的行为不可预期（有的 400，有的静默忽略）。
func normalizeReasoning(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" || v == "none" || v == "off" || v == "auto" {
		return ""
	}
	if !validReasoning[v] {
		log.Printf("忽略未知思考程度 %q，可用值: minimal/low/medium/high", v)
		return ""
	}
	return v
}

// withReasoning 把「思考程度」注入请求体（可选字段，
// 上游不识别该字段时按 OpenAI 规范应忽略；想关闭时传空/none 不注入）。
// 入参必须先经 normalizeReasoning 清洗。
func withReasoning(body map[string]any, effort string) {
	if effort == "" {
		return
	}
	body["reasoning_effort"] = effort
}

// StreamFn 跑一轮流式补全，返回最终文本与（可能存在的）工具调用。
// reasoning 是请求级「思考程度」（minimal/low/medium/high；空=不注入）。
type StreamFn func(ctx context.Context, llm config.LLMConfig, messages []ChatMessage, toolDefs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error)

// Upstream 按 mode 返回对应实现（每次请求时构造，支持设置热更新）。
func Upstream(llm config.LLMConfig) StreamFn {
	if strings.EqualFold(llm.Mode, "mock") {
		return func(ctx context.Context, cfg config.LLMConfig, messages []ChatMessage, toolDefs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error) {
			return streamMock(ctx, cfg, messages, toolDefs, hooks)
		}
	}
	return func(ctx context.Context, cfg config.LLMConfig, messages []ChatMessage, toolDefs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error) {
		return streamOpenAI(ctx, cfg, messages, toolDefs, hooks, reasoning)
	}
}

// streamClient 专供流式补全：不设总超时（一次回答可能持续几分钟），
// 但限制「等待响应头」的时间，避免上游完全卡死时请求无限挂起。
//
// 连接池调优：对话是长连接+高频小包场景，默认的 MaxIdleConnsPerHost=2
// 会让并发请求（多会话/并行工具）反复建 TLS 握手，首 token 延迟明显上升。
var streamClient = func() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 60 * time.Second
	t.MaxIdleConns = 100
	t.MaxIdleConnsPerHost = 16
	t.IdleConnTimeout = 90 * time.Second
	// TLSHandshakeTimeout 默认 10s，保持；压缩交给上游协商（SSE 通常不压缩）
	return &http.Client{Transport: t}
}()

// streamOpenAI 调用 OpenAI 兼容 /chat/completions（stream=true），
// 解析文本增量与 tool_calls 增量。baseURL 示例：
//   - DeepSeek 官方:  https://api.deepseek.com
//   - 本地 opencode 网关: http://localhost:3000/api/v1
func streamOpenAI(ctx context.Context, cfg config.LLMConfig, messages []ChatMessage, toolDefs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return "", nil, fmt.Errorf("缺少 API Key：请在设置里填写，或设置环境变量 XIAOKE_API_KEY")
	}
	body := map[string]any{
		"model":       cfg.Model,
		"messages":    messages,
		"stream":      true,
		"temperature": cfg.Temperature,
	}
	if cfg.MaxTokens > 0 {
		body["max_tokens"] = cfg.MaxTokens
	}
	if len(toolDefs) > 0 {
		body["tools"] = toolDefs
		body["tool_choice"] = "auto"
	}
	withReasoning(body, reasoning)
	payload, err := json.Marshal(body)
	if err != nil {
		return "", nil, err
	}
	url := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("User-Agent", "xiaoke")
	// 显式声明 SSE：个别网关会根据 Accept 决定是否缓冲
	req.Header.Set("Accept", "text/event-stream")

	resp, err := streamClient.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("请求上游失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", nil, fmt.Errorf("上游返回 %s: %s", resp.Status, truncateRunes(strings.TrimSpace(string(limited)), 500))
	}

	var full strings.Builder
	acc := map[int]*ToolCall{} // tool_calls 按 index 增量拼装
	order := []int{}
	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	for {
		line, rerr := reader.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "[DONE]" {
				break
			}
			content, deltas, stop := parseChunk(payload)
			if content != "" {
				full.WriteString(content)
				if hooks.OnDelta != nil {
					hooks.OnDelta(content)
				}
			}
			for _, d := range deltas {
				tc, ok := acc[d.Index]
				if !ok {
					tc = &ToolCall{Type: "function"}
					acc[d.Index] = tc
					order = append(order, d.Index)
				}
				if d.ID != "" {
					tc.ID += d.ID
				}
				if d.Name != "" {
					tc.Function.Name += d.Name
				}
				tc.Function.Arguments += d.ArgDelta
				if hooks.OnToolCall != nil {
					hooks.OnToolCall(d)
				}
			}
			if stop == "tool_calls" || stop == "stop" {
				// 仍继续读到 [DONE]，保证统计完整
				_ = stop
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			return full.String(), nil, fmt.Errorf("读取上游流中断: %w", rerr)
		}
	}
	calls := make([]ToolCall, 0, len(order))
	for _, i := range order {
		tc := acc[i]
		if tc.Function.Name == "" {
			continue
		}
		if tc.ID == "" {
			tc.ID = fmt.Sprintf("call_%d_%d", time.Now().UnixMilli(), i)
		}
		calls = append(calls, *tc)
	}
	if full.Len() == 0 && len(calls) == 0 {
		return "", nil, fmt.Errorf("上游没有返回任何内容")
	}
	return full.String(), calls, nil
}

// parseChunk 从一条 SSE 数据里取文本增量、tool_calls 增量与 finish_reason。
func parseChunk(payload string) (content string, deltas []ToolDelta, stop string) {
	var chunk struct {
		Choices []struct {
			Delta struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return "", nil, ""
	}
	if len(chunk.Choices) == 0 {
		return "", nil, ""
	}
	c := chunk.Choices[0]
	if c.FinishReason != "" {
		stop = c.FinishReason
	}
	for _, tc := range c.Delta.ToolCalls {
		deltas = append(deltas, ToolDelta{
			Index: tc.Index, ID: tc.ID, Name: tc.Function.Name, ArgDelta: tc.Function.Arguments,
		})
	}
	return c.Delta.Content, deltas, stop
}

// ---------- mock 上游（免 Key 演示，可模拟一次工具调用闭环） ----------

// mockNeedTool 判断是否演示一次 write_file 工具调用（宽松匹配，mock 只为演示链路）。
func mockNeedTool(userText string) bool {
	lower := strings.ToLower(userText)
	for _, kw := range []string{
		"写文件", "创建文件", "新建文件", "写代码", "写文章", "写个", "写一", "帮我写",
		"建项目", "写项目", "写篇文章", "hello", "write", "create", "build",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// streamMock：第一轮对含关键词的请求返回一个 write_file 工具调用（arguments 分片流出，
// 验证前端与循环的拼装）；收到工具结果（role=tool）后第二轮总结收尾。
func streamMock(ctx context.Context, cfg config.LLMConfig, messages []ChatMessage, toolDefs []tools.ToolDef, hooks Hooks) (string, []ToolCall, error) {
	last := ChatMessage{}
	lastUser := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && lastUser == "" {
			lastUser = messages[i].Content
		}
		if i == len(messages)-1 {
			last = messages[i]
		}
	}
	_ = cfg

	// 第二轮：工具结果已回填，输出总结
	if last.Role == "tool" {
		result := last.Content
		if len([]rune(result)) > 60 {
			result = string([]rune(result)[:60]) + "…"
		}
		reply := fmt.Sprintf("**小柯** 工具跑完了，结果：%s\n\n这是一次 **mock 工具调用演示**——「模型→工具→结果回填→继续生成」这条链路是通的。接上真实模型后我就能真正干活了，现在这副躯壳只是个演示品，别对它期待太多。", result)
		out, err := streamChars(ctx, reply, hooks.OnDelta)
		return out, nil, err
	}

	// 第一轮：命中关键词 → 演示工具调用
	if mockNeedTool(lastUser) {
		arg := map[string]string{
			"path":    "hello.txt",
			"content": "这是小柯（mock 模式）创建的文件。\n如果你能在项目里看到我，说明工具链已经打通。\n",
		}
		raw, _ := json.Marshal(arg)
		argStr := string(raw)
		call := ToolCall{ID: fmt.Sprintf("mock_%d", time.Now().UnixMilli()), Type: "function"}
		call.Function.Name = "write_file"
		call.Function.Arguments = argStr
		// 模拟 arguments 分片增量（4 段），验证拼装逻辑
		if hooks.OnToolCall != nil {
			n := len(argStr)
			step := (n + 3) / 4
			for idx, i := 0, 0; i < n; i, idx = i+step, idx+1 {
				end := i + step
				if end > n {
					end = n
				}
				hooks.OnToolCall(ToolDelta{Index: 0, ID: call.ID, Name: call.Function.Name, ArgDelta: argStr[i:end]})
			}
		}
		// 先给一句说明文字
		note := "我来演示一下工具调用：在工作区里创建一个 hello.txt。"
		if hooks.OnDelta != nil {
			if _, err := streamChars(ctx, note, hooks.OnDelta); err != nil {
				return note, nil, err
			}
		}
		return note, []ToolCall{call}, nil
	}

	// 普通闲聊回复
	q := strings.TrimSpace(lastUser)
	if len([]rune(q)) > 40 {
		q = string([]rune(q)[:40]) + "…"
	}
	reply := fmt.Sprintf("**小柯** 收到，你说的是「%s」。\n\n我现在跑在 **mock 演示模式**下，说穿了就是个会复读的空壳。想让我真正干活（写代码、写文章、调工具）：\n\n1. 点侧边栏 ⚙ 设置\n2. 选预设（DeepSeek / 本地 opencode 网关），填 API Key\n3. 建一个项目，然后对我说「帮我写个 xxx」\n\n——在那之前，请多包涵。", q)
	out, err := streamChars(ctx, reply, hooks.OnDelta)
	return out, nil, err
}

// streamChars 逐字流式输出（带取消检查）。
// 被取消时返回 ctx.Err()，让调用方区分「完整结束」与「用户中途停止」——
// 否则半截内容会被当作完整回复落库。
func streamChars(ctx context.Context, reply string, onDelta func(string)) (string, error) {
	var sent strings.Builder
	for _, r := range reply {
		select {
		case <-ctx.Done():
			return sent.String(), ctx.Err()
		default:
		}
		sent.WriteRune(r)
		if onDelta != nil {
			onDelta(string(r))
		}
		time.Sleep(6 * time.Millisecond)
	}
	return sent.String(), nil
}
