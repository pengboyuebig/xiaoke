package chat

import (
	"strings"

	"xiaoke/internal/store"
)

// trimHistory 按条数与字节预算裁剪历史，并把起点向前对齐到一条 user 消息。
//
// 为什么不能直接取最后 N 条：assistant(tool_calls) 与后续 role=tool 结果是一组，
// 截断点落在中间会让请求以孤立 tool 消息开头，严格 OpenAI 上游会 400。
// 对齐到 user 边界可以保证窗口从一个完整回合开始。
func trimHistory(msgs []store.Message, maxCount, maxBytes int) []store.Message {
	if len(msgs) == 0 {
		return msgs
	}
	start := 0
	if maxCount > 0 && len(msgs) > maxCount {
		start = len(msgs) - maxCount
	}

	// 字节预算：只按条数截断挡不住几条大工具结果（read_file 单条可达 64KB）
	if maxBytes > 0 {
		size := 0
		for _, m := range msgs[start:] {
			size += messageSize(m)
		}
		for start < len(msgs)-1 && size > maxBytes {
			size -= messageSize(msgs[start])
			start++
		}
	}

	// 优先对齐到 user；窗口内没有 user（超长工具循环）时跳过开头的孤立 tool 结果
	aligned := start
	for aligned < len(msgs) && msgs[aligned].Role != "user" {
		aligned++
	}
	if aligned < len(msgs) {
		return msgs[aligned:]
	}
	for start < len(msgs) && msgs[start].Role == "tool" {
		start++
	}
	if start >= len(msgs) {
		start = len(msgs) - 1
	}
	return msgs[start:]
}

func messageSize(m store.Message) int {
	n := len(m.Content) + 16
	for _, c := range m.ToolCalls {
		n += len(c.Name) + len(c.Arguments) + 8
	}
	return n
}

// sanitizeHistory 清理不完整的工具调用序列：
//   - 孤立 tool 消息（找不到对应的 assistant.tool_calls）直接丢掉
//   - assistant.tool_calls 缺少部分/全部 tool 结果时，去掉该消息的 tool_calls
//     （如果正文为空就连消息一起删）
//
// 这样即使上一次请求在工具执行中途被客户端断开、历史里留下半截调用，
// 下一次请求也不会把非法序列发给上游。
func sanitizeHistory(msgs []store.Message) []store.Message {
	out := make([]store.Message, 0, len(msgs))
	pending := map[string]bool{}
	pendingStart := -1

	flushIncomplete := func() {
		if pendingStart < 0 {
			return
		}
		// 全部结果都到齐了：保留原样
		if len(pending) == 0 {
			pendingStart = -1
			pending = map[string]bool{}
			return
		}
		// 有结果缺失：丢弃这轮 assistant 之后的所有 tool 结果，再按需保留正文
		base := out[pendingStart]
		out = out[:pendingStart]
		if strings.TrimSpace(base.Content) != "" {
			base.ToolCalls = nil
			out = append(out, base)
		}
		pendingStart = -1
		pending = map[string]bool{}
	}

	for _, m := range msgs {
		switch m.Role {
		case "assistant":
			flushIncomplete()
			if len(m.ToolCalls) > 0 {
				pendingStart = len(out)
				for _, c := range m.ToolCalls {
					pending[c.ID] = true
				}
			}
			out = append(out, m)
		case "tool":
			if !pending[m.ToolCallID] {
				continue // 孤立结果：丢掉
			}
			delete(pending, m.ToolCallID)
			out = append(out, m)
			if len(pending) == 0 {
				// 本轮工具已完整，后面再出现新消息不必回滚
				pendingStart = -1
			}
		default:
			flushIncomplete()
			out = append(out, m)
		}
	}
	flushIncomplete()
	return out
}
