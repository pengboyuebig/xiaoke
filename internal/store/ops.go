// 会话级批量操作：截断回退（配合 chat 层的文件改动台账做「回到某条消息重来」）、
// 分叉复制、跨会话全文搜索。独立成文件避免 store.go 继续膨胀。
package store

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// TruncateAfter 把会话截断到第 index 条消息（含该条）。之后的消息全部删除；
// 摘要覆盖越过截断点时整段失效；待办一并清空（它们描述的进度可能已被回滚）。
// 实现 = 落新快照 + 删日志：旧日志条目序号 ≤ 快照 LastSeq，读取时天然被跳过。
func (s *Store) TruncateAfter(id string, index int) error {
	if !validID.MatchString(id) {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.loadFullLocked(id)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(sess.Messages) {
		return fmt.Errorf("截断位置越界（会话共 %d 条消息）", len(sess.Messages))
	}
	sess.Messages = sess.Messages[:index+1]
	if len(sess.Messages) > 0 {
		last := sess.Messages[len(sess.Messages)-1]
		if sess.SummaryUpTo.After(last.CreatedAt) {
			sess.Summary = ""
			sess.SummaryUpTo = time.Time{}
		}
	} else {
		sess.Summary = ""
		sess.SummaryUpTo = time.Time{}
	}
	sess.Todos = nil
	sess.UpdatedAt = time.Now()
	if err := s.saveSnapshot(sess); err != nil {
		return err
	}
	_ = os.Remove(s.logPath(id))
	return s.saveMeta(metaOf(sess))
}

// Fork 复制一个会话为全新会话（消息、摘要、待办、绑定、累计用量全部带走），
// 标题追加后缀。返回新会话。
func (s *Store) Fork(id, titleSuffix string) (*Session, error) {
	if !validID.MatchString(id) {
		return nil, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	src, err := s.loadFullLocked(id)
	if err != nil {
		return nil, err
	}
	title := src.Title
	if title == "" {
		title = "新对话"
	}
	dst, err := s.CreateLocked(title+titleSuffix, src.ProjectID)
	if err != nil {
		return nil, err
	}
	dst.AgentID = src.AgentID
	dst.Messages = append([]Message(nil), src.Messages...)
	dst.Summary, dst.SummaryUpTo = src.Summary, src.SummaryUpTo
	dst.Todos = append([]Todo(nil), src.Todos...)
	dst.PromptTokens, dst.CompletionTokens = src.PromptTokens, src.CompletionTokens
	dst.Requests, dst.LastPromptTokens = src.Requests, src.LastPromptTokens
	if err := s.saveSnapshot(dst); err != nil {
		return nil, err
	}
	return dst, s.saveMeta(metaOf(dst))
}

// CreateLocked 是 Create 的持锁版本（Fork 复用创建逻辑）。调用方需持有 s.mu。
func (s *Store) CreateLocked(title, projectID string) (*Session, error) {
	id := newID()
	now := time.Now()
	sess := &Session{
		ID: id, Title: strings.TrimSpace(title), ProjectID: projectID,
		CreatedAt: now, UpdatedAt: now, Messages: []Message{},
	}
	if err := s.saveSnapshot(sess); err != nil {
		return nil, err
	}
	return sess, s.saveMeta(metaOf(sess))
}

// SearchHit 是一次跨会话全文搜索的命中。
type SearchHit struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
	Role      string `json:"role"` // user | assistant
	Snippet   string `json:"snippet"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// searchScanLimit 单会话内容扫描字节上限：逐条检查时累计消息内容超过它就跳过
// 该会话剩余部分（大会话的远古消息命中率极低，不值得全量扫）。
const searchScanLimit = 200 << 10

// SearchMessages 跨会话搜索消息内容（大小写不敏感）。q 少于 2 个 rune 返回空。
// 会话按最近更新排序，最多返回 maxHits 条命中；任何失败静默跳过该会话
//（搜索不能因个别损坏会话而整体失败）。
func (s *Store) SearchMessages(q string, maxHits int) []SearchHit {
	q = strings.TrimSpace(q)
	if len([]rune(q)) < 2 || maxHits <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	ids := s.listIDsLocked(entries)
	type sessMeta struct {
		id    string
		title string
		at    time.Time
	}
	list := make([]sessMeta, 0, len(ids))
	for _, id := range ids {
		if m, err := s.loadMeta(id); err == nil {
			list = append(list, sessMeta{id, m.Title, m.UpdatedAt})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].at.After(list[j].at) })

	needle := strings.ToLower(q)
	hits := make([]SearchHit, 0, maxHits)
	for _, sm := range list {
		if len(hits) >= maxHits {
			break
		}
		sess, err := s.loadFullLocked(sm.id)
		if err != nil {
			continue
		}
		scanned := 0
		// 从最新消息往前找：命中即返回该会话的第一条（用户关心的是「哪个会话提到过」）
		for i := len(sess.Messages) - 1; i >= 0; i-- {
			m := sess.Messages[i]
			if m.Hidden || (m.Role != "user" && m.Role != "assistant") || m.Content == "" {
				continue
			}
			scanned += len(m.Content)
			if scanned > searchScanLimit {
				break
			}
			idx := strings.Index(strings.ToLower(m.Content), needle)
			if idx < 0 {
				continue
			}
			hits = append(hits, SearchHit{
				SessionID: sm.id,
				Title:     sm.title,
				Role:      m.Role,
				Snippet:   snippetAround(m.Content, idx, len(q)),
				UpdatedAt: sm.at,
			})
			break
		}
	}
	return hits
}

// snippetAround 取命中位置前后的片段（前后各 ~60 rune，对齐到 rune 边界）。
func snippetAround(content string, matchIdx, matchLen int) string {
	const radius = 60
	rs := []rune(content)
	// matchIdx 是字节偏移，转 rune 偏移
	byteToRune := func(b int) int {
		if b >= len(content) {
			return len(rs)
		}
		return len([]rune(content[:b]))
	}
	start := byteToRune(matchIdx) - radius
	if start < 0 {
		start = 0
	}
	end := byteToRune(matchIdx+matchLen) + radius
	if end > len(rs) {
		end = len(rs)
	}
	out := string(rs[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(rs) {
		out += "…"
	}
	return out
}

// MessageCount 只读 meta 的消息条数（revert 台账记录消息边界用，不加载会话体）。
func (s *Store) MessageCount(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.loadMeta(id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return 0
		}
		return 0
	}
	return m.MessageCount
}

// ReplaceTitle 强制覆盖标题（自动起标题专用：模型生成的标题允许替换本地截断的
// 默认标题；用户手动改名走 RenameTitle，不受这里影响）。
func (s *Store) ReplaceTitle(id, title string) error {
	if !validID.MatchString(id) {
		return ErrNotFound
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}
	if rs := []rune(title); len(rs) > 32 {
		title = string(rs[:32])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.loadMeta(id)
	if err != nil {
		return err
	}
	meta.Title = title
	meta.UpdatedAt = time.Now()
	return s.saveMeta(meta)
}
