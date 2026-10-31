// 会话周边端点：分叉、跨会话搜索、Markdown 导出。
package chat

import (
	"fmt"
	"net/http"
	"strings"

	"xiaoke/internal/store"
)

// forkSession 处理 POST /api/sessions/{id}/fork：复制会话为全新会话。
func (h *Handler) forkSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dst, err := h.store.Fork(id, "（分叉）")
	if err == store.ErrNotFound {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "会话不存在"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": dst.ID, "title": dst.Title, "messages": len(dst.Messages)})
}

// searchMessages 处理 GET /api/search?q=：跨会话搜消息内容。
func (h *Handler) searchMessages(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) < 2 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "搜索词至少 2 个字符"})
		return
	}
	hits := h.store.SearchMessages(q, 50)
	if hits == nil {
		hits = []store.SearchHit{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"q": q, "hits": hits})
}

// mdExportMaxMessages 单次导出的消息条数上限（防御性：正常会话远小于此）。
const mdExportMaxMessages = 5000

// exportSessionMarkdown 生成会话的 Markdown 版本（/export?format=md）。
// 结构：头部元信息 + 逐条消息；工具调用折叠为引用行，长结果截断——
// 导出是给人读/归档的，不是数据备份（JSON 导出承担后者）。
func (h *Handler) exportSessionMarkdown(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := h.store.Get(id)
	if err == store.ErrNotFound {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "会话不存在"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var b strings.Builder
	title := strings.TrimSpace(sess.Title)
	if title == "" {
		title = "未命名会话"
	}
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "> 会话 `%s` · 创建于 %s · 共 %d 条消息\n\n",
		sess.ID, sess.CreatedAt.Format("2006-01-02 15:04"), len(sess.Messages))
	// 摘要（若有）
	if strings.TrimSpace(sess.Summary) != "" {
		b.WriteString("## 历史摘要\n\n")
		b.WriteString(strings.TrimSpace(sess.Summary))
		b.WriteString("\n\n---\n\n")
	}
	count := 0
	for _, m := range sess.Messages {
		if count >= mdExportMaxMessages {
			b.WriteString("\n（消息过多，已截断）\n")
			break
		}
		count++
		ts := m.CreatedAt.Format("01-02 15:04")
		switch m.Role {
		case "user":
			if m.Hidden {
				continue // 批准计划的自动指令不进导出
			}
			fmt.Fprintf(&b, "## 🧑 用户 · %s\n\n%s\n\n", ts, strings.TrimSpace(m.Content))
		case "assistant":
			fmt.Fprintf(&b, "## 🤖 小柯 · %s\n\n", ts)
			if strings.TrimSpace(m.Content) != "" {
				b.WriteString(strings.TrimSpace(m.Content))
				b.WriteString("\n\n")
			}
			for _, c := range m.ToolCalls {
				fmt.Fprintf(&b, "> 🔧 `%s(%s)`\n", c.Name, truncateRunesForMd(c.Arguments, 160))
			}
			if len(m.ToolCalls) > 0 {
				b.WriteString("\n")
			}
		case "tool":
			// 工具结果压成一行引用：长输出对读者是噪音
			fmt.Fprintf(&b, "> ↳ 结果：%s\n\n", truncateRunesForMd(firstLine(m.Content), 200))
		}
	}
	filename := fmt.Sprintf("session-%s-%s.md", id, sanitizeFilename(title))
	serveDownload(w, filename, "text/markdown; charset=utf-8", []byte(b.String()))
}

// firstLine 取第一行非空文本。
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return "（无输出）"
}

// truncateRunesForMd 按 rune 截断并转义会破坏 Markdown 引用格式的字符。
func truncateRunesForMd(s string, n int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "`", "'")
	s = strings.ReplaceAll(s, "\n", " ")
	if rs := []rune(s); len(rs) > n {
		s = string(rs[:n]) + "…"
	}
	return s
}

// exportSession 现按 ?format=md 分流到 Markdown 导出（默认仍是 JSON）。
// 分流逻辑加在原 exportSession 里，见 handler.go。
