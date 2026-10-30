// 上下文主动压缩：历史超过预算时，用当前模型把旧回合总结成摘要，
// 之后每次请求用「摘要 + 摘要之后的消息」替代完整历史。
//
// 与被动裁剪（trimHistory，硬截断）的关系：
//   - 被动裁剪永远作为兜底——摘要生成失败/关闭时行为与旧版一致
//   - 主动压缩在「将要丢东西」时触发，把要丢的部分变成摘要，保留更早的语境
package chat

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"xiaoke/internal/config"
	"xiaoke/internal/store"
)

const (
	// compactMinMessages / compactMinBytes：要被摘要的部分至少有这么多材料，
	// 否则不值得花一次模型调用（总结太短没有信息量）。
	compactMinMessages = 4
	compactMinBytes    = 1 << 10 // 1KB

	// compactInputLimit 喂给摘要模型的最大输入字节（防止摘要请求自身超预算）。
	compactInputLimit = 80 * 1024

	// compactCooldown 两次压缩之间的最小间隔（避免每条消息都触发一次总结）。
	compactCooldown = time.Minute

	// compactTimeout 单次摘要调用的超时。
	compactTimeout = 60 * time.Second
)

// compactLast 记录每个会话上次尝试压缩的时间（失败也计入，避免反复烧钱重试）。
var (
	compactMu      sync.Mutex
	compactLastTry = map[string]time.Time{}
)

// summarizerFunc 与 StreamFn 同形，但摘要场景不关心流式增量。
type summarizerFunc = StreamFn

// summaryView 是压缩后用于组装上下文的视图。
type summaryView struct {
	Summary string          // 可为空 = 没有摘要
	Recent  []store.Message // 摘要之后的消息（从一条 user 开始）
}

// splitBySummary 把完整消息列表按已持久化的摘要边界切成「摘要 + 近期」。
func splitBySummary(msgs []store.Message, summary string, upTo time.Time) summaryView {
	if strings.TrimSpace(summary) == "" || upTo.IsZero() {
		return summaryView{Recent: msgs}
	}
	for i, m := range msgs {
		if m.CreatedAt.After(upTo) {
			return summaryView{Summary: summary, Recent: msgs[i:]}
		}
	}
	// 全部都在摘要覆盖范围内（不该发生，兜底返回空近段）
	return summaryView{Summary: summary, Recent: nil}
}

// historyBytes 计算消息列表的总字节量（与 trimHistory 同一口径）。
func historyBytes(msgs []store.Message) int {
	n := 0
	for _, m := range msgs {
		n += messageSize(m)
	}
	return n
}

// cutForSummary 选出摘要的截止点：尽量保留最近 keep 条，且截止点必须
// 落在一条 user 消息之前（剩余窗口以 user 开头，天然满足裁剪对齐要求）。
// 返回 -1 表示找不到合适的切点。
func cutForSummary(msgs []store.Message, keep int) int {
	if len(msgs) <= keep || keep < 0 {
		return -1
	}
	cut := len(msgs) - keep
	for cut < len(msgs) && msgs[cut].Role != "user" {
		cut++
	}
	if cut >= len(msgs) {
		return -1 // 没有可用的 user 边界（超长工具循环），放弃
	}
	// 摘要范围里至少要有一条 user 消息，否则没有可总结的内容
	hasUser := false
	for _, m := range msgs[:cut] {
		if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
			hasUser = true
			break
		}
	}
	if !hasUser {
		return -1
	}
	return cut
}

// maybeCompact 判断是否需要压缩并执行；返回用于本次请求的摘要视图。
// 任何失败都只记日志并退回「无新摘要」——压缩永远不能阻断对话。
func maybeCompact(
	ctx context.Context,
	st *store.Store,
	sess *store.Session,
	keepCount int,
	byteBudget int,
	summarize summarizerFunc,
	cfg config.LLMConfig,
) summaryView {
	view := splitBySummary(sess.Messages, sess.Summary, sess.SummaryUpTo)

	// 触发条件：近期窗口本身就超预算（条数或字节），且够格做摘要
	overCount := keepCount > 0 && len(view.Recent) > keepCount
	overBytes := byteBudget > 0 && historyBytes(view.Recent) > byteBudget
	if !overCount && !overBytes {
		return view
	}

	compactMu.Lock()
	if len(compactLastTry) > 1024 {
		// 删除会话不经过这里清理，做个粗粒度防泄漏：超阈值时丢掉久未触碰的条目
		now := time.Now()
		for k, t := range compactLastTry {
			if now.Sub(t) > time.Hour {
				delete(compactLastTry, k)
			}
		}
	}
	last, seen := compactLastTry[sess.ID]
	compactLastTry[sess.ID] = time.Now()
	compactMu.Unlock()
	if seen && time.Since(last) < compactCooldown {
		return view
	}

	// 截止点基于完整列表：摘要范围 = 已有摘要覆盖的 + 近期窗口要丢掉的部分。
	// 这样旧摘要的内容自然融入新摘要，不需要单独合并逻辑。
	cut := cutForSummary(sess.Messages, keepCount)
	if cut < 0 {
		return view
	}
	old := sess.Messages[:cut]
	if len(old) < compactMinMessages || historyBytes(old) < compactMinBytes {
		return view
	}

	summary, err := summarizeMessages(ctx, old, summarize, cfg)
	if err != nil {
		log.Printf("会话 %s 上下文压缩失败（退回被动裁剪）: %v", sess.ID, err)
		return view
	}
	upTo := old[len(old)-1].CreatedAt
	if err := st.SetSummary(sess.ID, summary, upTo); err != nil {
		log.Printf("会话 %s 摘要持久化失败: %v", sess.ID, err)
		return view
	}
	log.Printf("会话 %s 已压缩上下文：摘要覆盖 %d 条消息", sess.ID, len(old))
	return splitBySummary(sess.Messages, summary, upTo)
}

// summarizeMessages 调一次非流式补全生成结构化摘要。
func summarizeMessages(ctx context.Context, old []store.Message, summarize summarizerFunc, cfg config.LLMConfig) (string, error) {
	sys := `你是对话记录摘要器。把下面的对话历史压缩成一份给后续对话用的背景摘要，要求：
1. 保留：用户的目标与偏好、已完成的动作（引用具体文件路径/命令）、重要决定、尚未完成的事、用户的原话承诺。
2. 丢弃：寒暄、重复内容、工具输出的原文细节（保留结论即可）。
3. 用简体中文，条目式，总长不超过 500 字。只输出摘要正文，不要任何开场白或标题。`
	user := transcriptForSummary(old)
	if user == "" {
		return "", fmt.Errorf("没有可摘要的内容")
	}
	cctx, cancel := context.WithTimeout(ctx, compactTimeout)
	defer cancel()
	out, _, err := summarize(cctx, config.LLMConfig{
		Mode: cfg.Mode, BaseURL: cfg.BaseURL, APIKey: cfg.APIKey,
		Model: cfg.Model, Temperature: 0.3,
	}, []ChatMessage{
		{Role: "system", Content: sys},
		{Role: "user", Content: user},
	}, nil, Hooks{}, "")
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", fmt.Errorf("摘要为空")
	}
	return out, nil
}

// transcriptForSummary 把消息列表转成带角色标注的纯文本（截断长工具结果）。
func transcriptForSummary(msgs []store.Message) string {
	var b strings.Builder
	budget := compactInputLimit
	writeLine := func(s string) {
		if budget <= 0 {
			return
		}
		if len(s) > budget {
			s = s[:budget] + "…（截断）"
		}
		budget -= len(s)
		b.WriteString(s)
		b.WriteByte('\n')
	}
	for _, m := range msgs {
		switch m.Role {
		case "user":
			if m.Hidden {
				continue // 批准计划的自动指令对摘要没有价值
			}
			writeLine("[用户] " + truncateRunes(m.Content, 2000))
		case "assistant":
			line := truncateRunes(strings.TrimSpace(m.Content), 800)
			for _, c := range m.ToolCalls {
				line += "\n  [调用工具] " + c.Name + "(" + truncateRunes(c.Arguments, 200) + ")"
			}
			if strings.TrimSpace(line) != "" {
				writeLine("[助手] " + line)
			}
		case "tool":
			writeLine("  [工具结果] " + truncateRunes(m.Content, 400))
		}
	}
	return strings.TrimSpace(b.String())
}
