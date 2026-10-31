// 长期记忆离线整理（对标主流 agent 的 autoDream）：
// 定时把近期会话记录蒸馏成记忆条目，去重后并入全局 MEMORY.md。
//
// 设计约束：
//   - 只在真实模型模式下运行（mock/无 Key 跳过——离线整理也要烧 token）
//   - 输入有预算（24KB 转录），输出有上限（20 条、单条 300 rune），由 ImportGlobal 兜底
//   - 全程失败只记日志：整理是增值动作，任何故障不影响对话服务
package chat

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"xiaoke/internal/config"
	"xiaoke/internal/store"
)

const (
	// dreamInterval 离线整理周期。
	dreamInterval = 24 * time.Hour
	// dreamStartupDelay 启动后先等一段时间（等服务稳定、避开启动风暴）。
	dreamStartupDelay = 5 * time.Minute
	// dreamMaxSessions / dreamMsgsPerSession 每次整理最多看几个会话、每个会话取几条消息。
	dreamMaxSessions    = 8
	dreamMsgsPerSession = 12
	// dreamTranscriptLimit 喂给整理模型的总输入预算（字节）。
	dreamTranscriptLimit = 24 * 1024
	// dreamMaxItems 单次整理最多新增的记忆条数。
	dreamMaxItems = 20
)

// StartDreamLoop 启动离线整理循环（goroutine，不阻塞启动）。
func (h *Handler) StartDreamLoop() {
	go func() {
		time.Sleep(dreamStartupDelay)
		for {
			// panic 保护：整理是后台增值动作，任何意外都不能带走服务进程
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("记忆离线整理 panic（已忽略，下个周期重试）: %v", r)
					}
				}()
				h.dreamOnce(nil)
			}()
			time.Sleep(dreamInterval)
		}
	}()
}

// dreamOnce 执行一次整理。streamFn 传 nil 用真实上游（测试注入假实现，跳过设置检查）。
func (h *Handler) dreamOnce(streamFn StreamFn) {
	cfg := config.LLMConfig{}
	if streamFn == nil {
		eff := h.set.Effective()
		if strings.EqualFold(eff.Mode, "mock") || strings.TrimSpace(eff.APIKey) == "" {
			return // 免 Key 模式不烧「假 token」
		}
		cfg = effAsConfig(eff)
		streamFn = Upstream(cfg)
	}

	metas, err := h.store.List()
	if err != nil || len(metas) < 3 {
		return // 会话太少没有蒸馏价值（对标 dream 的会话数门）
	}
	transcript, checked := h.dreamTranscript(metas)
	if strings.TrimSpace(transcript) == "" {
		return
	}

	sys := `你是记忆整理器。从下面的对话记录中提炼值得跨会话长期记住的信息：用户偏好、项目结构、重要决定、被纠正过的错误、复现过的坑。
要求：每行一条，以「- 」开头，最多 20 条；只输出条目，不要解释、不要小标题；对话里没有值得记的就只输出「（无）」。`
	cctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	out, _, err := streamFn(cctx, cfg, []ChatMessage{
		{Role: "system", Content: sys},
		{Role: "user", Content: transcript},
	}, nil, Hooks{}, "")
	if err != nil {
		log.Printf("记忆离线整理失败（下个周期重试）: %v", err)
		return
	}
	lines := dreamParseItems(out)
	if len(lines) == 0 {
		return
	}
	added, err := h.memory.ImportGlobal(lines)
	if err != nil {
		log.Printf("记忆导入失败: %v", err)
		return
	}
	if added > 0 {
		log.Printf("记忆离线整理完成：检查 %d 个会话，新增 %d 条记忆（候选 %d 条）", checked, added, len(lines))
	}
}

// dreamTranscript 从最近的会话构建蒸馏输入（总预算 dreamTranscriptLimit）。
// 返回转录文本与实际检查的会话数。
func (h *Handler) dreamTranscript(metas []store.Meta) (string, int) {
	var b strings.Builder
	budget := dreamTranscriptLimit
	checked := 0
	for _, m := range metas {
		if checked >= dreamMaxSessions || budget <= 200 {
			break
		}
		sess, err := h.store.Get(m.ID)
		if err != nil || len(sess.Messages) == 0 {
			continue
		}
		// 只取最近的尾部消息；隐藏消息（批准指令）跳过
		msgs := sess.Messages
		if len(msgs) > dreamMsgsPerSession {
			msgs = msgs[len(msgs)-dreamMsgsPerSession:]
		}
		var sec strings.Builder
		fmt.Fprintf(&sec, "### 会话「%s」\n", firstNonEmpty(sess.Title, "未命名"))
		for _, msg := range msgs {
			if msg.Hidden || strings.TrimSpace(msg.Content) == "" {
				continue
			}
			line := ""
			switch msg.Role {
			case "user":
				line = "[用户] " + truncateRunes(msg.Content, 300)
			case "assistant":
				line = "[助手] " + truncateRunes(msg.Content, 200)
			default:
				continue // 工具结果对记忆提炼价值低
			}
			if len(line) > budget {
				return strings.TrimSpace(b.String()), checked + 1
			}
			sec.WriteString(line + "\n")
			budget -= len(line)
		}
		b.WriteString(sec.String())
		checked++
	}
	return strings.TrimSpace(b.String()), checked
}

// dreamParseItems 解析整理输出为候选条目：（无）/空行/超长行都丢弃，最多 dreamMaxItems 条。
func dreamParseItems(out string) []string {
	var items []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimSpace(line)
		if line == "" || line == "（无）" {
			continue
		}
		items = append(items, line)
		if len(items) >= dreamMaxItems {
			break
		}
	}
	return items
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
