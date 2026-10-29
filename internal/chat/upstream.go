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

// reasoningEffortByRequest 把请求级「思考程度」注入请求体（可选字段，
// 上游不识别该字段时按 OpenAI 规范应忽略；想关闭时传空/none 不注入）。
func withReasoning(body map[string]any, effort string) {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "", "none", "off", "auto":
		return
	case "minimal":
		body["reasoning_effort"] = "minimal"
	case "low":
		body["reasoning_effort"] = "low"
	case "medium":
		body["reasoning_effort"] = "medium"
	case "high":
		body["reasoning_effort"] = "high"
	}
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

	resp, err := http.DefaultClient.Do(req)
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
		reply := fmt.Sprintf("**小柯** 工具跑完了，结果是：%s\n\n这是一次 **mock 工具调用演示**——证明整条「模型→工具→结果回填→继续生成」的链路是通的。接上真实模型后，我就能真正帮你写代码、写文章了。", result)
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
	reply := fmt.Sprintf("**小柯** 收到，你说的是「%s」。\n\n我现在跑在 **mock 演示模式**下。想让我真正干活（写代码、写文章、调工具），请在**设置**里把模型接上：\n\n1. 点左侧边栏的 ⚙ 设置\n2. 选择预设（DeepSeek / 本地 opencode 网关），填入 API Key\n3. 新建一个项目，然后对我说「帮我写个 xxx」", q)
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
