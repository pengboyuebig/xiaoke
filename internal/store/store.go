// Package store 持久化聊天记录：
//
//	<id>.json       快照（含最近一次合并的所有消息）
//	<id>.log        追加日志（每条新消息一行 JSON，O(1) 追加）
//	<id>.meta.json  列表摘要（标题/绑定/消息数/更新时间，侧栏只读它）
//
// 追加消息只写一行日志 + 小体积 meta；日志超过阈值时合并回快照并清空。
// 这样既保留「可直接查看的 JSON」的优点，又避免每条消息重写整个会话。
package store

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var ErrNotFound = errors.New("session not found")

var validID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// 日志超过该字节数就合并进快照。变量形式便于测试调小。
var logCompactBytes int64 = 1 << 20

const maxLogLine = 1 << 20

type Message struct {
	Role      string    `json:"role"` // "user" | "assistant" | "tool"
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
	// 隐藏消息：仍参与模型上下文，但前端不渲染（如「批准计划」的自动指令）
	Hidden bool `json:"hidden,omitempty"`
	// assistant 消息携带的工具调用（content 可为空说明文字）
	ToolCalls []ToolCallRecord `json:"toolCalls,omitempty"`
	// role=tool 时：这条结果对应的调用 ID 与工具名
	ToolCallID string `json:"toolCallId,omitempty"`
	Name       string `json:"name,omitempty"`
}

// ToolCallRecord 落库的助手侧工具调用（与 OpenAI tool_calls 同构）。
type ToolCallRecord struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Session struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	ProjectID string    `json:"projectId,omitempty"`
	AgentID   string    `json:"agentId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// LastSeq 是快照已包含到的日志序号（日志合并后写入）
	LastSeq  int64     `json:"lastSeq,omitempty"`
	Messages []Message `json:"messages"`
}

// Meta 是会话列表里展示的摘要（不含消息体）。
type Meta struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	ProjectID    string    `json:"projectId,omitempty"`
	AgentID      string    `json:"agentId,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	MessageCount int       `json:"messageCount"`
	// LastSeq 是已追加到的日志序号（用于保证序号单调）
	LastSeq int64 `json:"lastSeq,omitempty"`
}

// logEntry 是追加日志的一行。
type logEntry struct {
	Seq int64   `json:"seq"`
	Msg Message `json:"msg"`
}

type Store struct {
	dir string
	mu  sync.Mutex

	lockMapMu   sync.Mutex
	sessionLock map[string]*sync.Mutex
}

func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir, sessionLock: map[string]*sync.Mutex{}}, nil
}

// TryLockSession 尝试独占一个会话（同一会话同时只允许一个对话请求）。
// 非法/空 ID 不参与锁（新会话由服务端创建，ID 唯一），直接返回可用。
func (s *Store) TryLockSession(id string) (unlock func(), ok bool) {
	if !validID.MatchString(id) {
		return func() {}, true
	}
	s.lockMapMu.Lock()
	l := s.sessionLock[id]
	if l == nil {
		l = &sync.Mutex{}
		s.sessionLock[id] = l
	}
	s.lockMapMu.Unlock()
	if !l.TryLock() {
		return nil, false
	}
	return l.Unlock, true
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// nextSeq 返回进程内单调递增的纳秒序号（时钟回拨时也比上一次大）。
var seqCounter atomic.Int64

func nextSeq() int64 {
	for {
		now := time.Now().UnixNano()
		old := seqCounter.Load()
		if now <= old {
			now = old + 1
		}
		if seqCounter.CompareAndSwap(old, now) {
			return now
		}
	}
}

func (s *Store) path(id string) string     { return filepath.Join(s.dir, id+".json") }
func (s *Store) logPath(id string) string  { return filepath.Join(s.dir, id+".log") }
func (s *Store) metaPath(id string) string { return filepath.Join(s.dir, id+".meta.json") }

// SetAgent 把会话绑定到智能体。
func (s *Store) SetAgent(id, agentID string) error {
	if agentID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.loadMeta(id)
	if errors.Is(err, ErrNotFound) {
		sess, lerr := s.loadFullLocked(id)
		if lerr != nil {
			return lerr
		}
		meta = metaOf(sess)
	} else if err != nil {
		return err
	}
	if meta.AgentID == agentID {
		return nil
	}
	meta.AgentID = agentID
	meta.UpdatedAt = time.Now()
	return s.saveMeta(meta)
}

// Create 新建一个空会话（可绑定项目）。
func (s *Store) Create(title, projectID string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	sess := &Session{
		ID:        newID(),
		Title:     title,
		ProjectID: projectID,
		CreatedAt: now,
		UpdatedAt: now,
		Messages:  []Message{},
	}
	if err := s.saveSnapshot(sess); err != nil {
		return nil, err
	}
	if err := s.saveMeta(metaOf(sess)); err != nil {
		return nil, err
	}
	return sess, nil
}

// List 返回全部会话的摘要，按更新时间倒序。
// 优先只读 meta 文件；没有 meta 的旧会话回退到完整加载（不写盘）。
func (s *Store) List() ([]Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	metas := []Meta{}
	seen := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		id := strings.TrimSuffix(name, ".meta.json")
		if !validID.MatchString(id) {
			continue
		}
		// 快照不存在时忽略孤儿 meta
		if _, err := os.Stat(s.path(id)); err != nil {
			continue
		}
		m, err := s.loadMeta(id)
		if err != nil {
			continue
		}
		metas = append(metas, m)
		seen[id] = true
	}
	// 兼容没有 meta 的旧会话
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".meta.json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !validID.MatchString(id) || seen[id] {
			continue
		}
		sess, err := s.loadFullLocked(id)
		if err != nil {
			continue // 单个文件损坏不影响列表
		}
		metas = append(metas, metaOf(sess))
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].UpdatedAt.After(metas[j].UpdatedAt) })
	return metas, nil
}

func (s *Store) Get(id string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadFullLocked(id)
}

// Append 向会话追加一条消息并刷新摘要。
// 消息只写一行日志；日志超过阈值时合并进快照。
func (s *Store) Append(id string, msg Message) error {
	if !validID.MatchString(id) {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.path(id)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	meta, err := s.loadMeta(id)
	if errors.Is(err, ErrNotFound) {
		sess, lerr := s.loadFullLocked(id)
		if lerr != nil {
			return lerr
		}
		meta = metaOf(sess)
		if err := s.saveMeta(meta); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	seq := nextSeq()
	if seq <= meta.LastSeq {
		seq = meta.LastSeq + 1
	}
	line, err := json.Marshal(logEntry{Seq: seq, Msg: msg})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.logPath(id), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	meta.LastSeq = seq
	meta.MessageCount++
	meta.UpdatedAt = time.Now()
	// 首条用户消息作为会话标题
	if meta.Title == "" && msg.Role == "user" {
		meta.Title = truncate([]rune(msg.Content), 24)
	}
	if err := s.saveMeta(meta); err != nil {
		return err
	}
	if st, err := os.Stat(s.logPath(id)); err == nil && st.Size() > logCompactBytes {
		return s.compactLocked(id)
	}
	return nil
}

// SetProject 把会话绑定到项目。
func (s *Store) SetProject(id, projectID string) error {
	if projectID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.loadMeta(id)
	if errors.Is(err, ErrNotFound) {
		sess, lerr := s.loadFullLocked(id)
		if lerr != nil {
			return lerr
		}
		meta = metaOf(sess)
	} else if err != nil {
		return err
	}
	if meta.ProjectID == projectID {
		return nil
	}
	meta.ProjectID = projectID
	meta.UpdatedAt = time.Now()
	return s.saveMeta(meta)
}

func (s *Store) Delete(id string) error {
	if !validID.MatchString(id) {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.path(id)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	for _, p := range []string{s.logPath(id), s.metaPath(id), s.path(id)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// loadFullLocked 读取快照 + 未合并的日志，并应用 meta 头字段。调用方需持有 s.mu。
func (s *Store) loadFullLocked(id string) (*Session, error) {
	if !validID.MatchString(id) {
		return nil, ErrNotFound
	}
	sess, err := s.loadSnapshot(id)
	if err != nil {
		return nil, err
	}
	snapshotSeq := sess.LastSeq
	if m, err := s.loadMeta(id); err == nil {
		sess.Title = m.Title
		sess.ProjectID = m.ProjectID
		sess.AgentID = m.AgentID
		if !m.CreatedAt.IsZero() {
			sess.CreatedAt = m.CreatedAt
		}
		if !m.UpdatedAt.IsZero() {
			sess.UpdatedAt = m.UpdatedAt
		}
		if m.LastSeq > sess.LastSeq {
			sess.LastSeq = m.LastSeq
		}
	}
	entries, err := s.loadLog(id)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.Seq <= snapshotSeq {
			continue // 已合并进快照
		}
		sess.Messages = append(sess.Messages, e.Msg)
		if e.Seq > sess.LastSeq {
			sess.LastSeq = e.Seq
		}
	}
	return sess, nil
}

func (s *Store) loadSnapshot(id string) (*Session, error) {
	data, err := os.ReadFile(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("会话 %s 损坏: %w", id, err)
	}
	if sess.ID != id {
		return nil, fmt.Errorf("会话 %s 内容与文件名不一致", id)
	}
	return &sess, nil
}

// loadLog 读取追加日志；半截/损坏的行跳过（进程崩溃时可能出现）。
func (s *Store) loadLog(id string) ([]logEntry, error) {
	f, err := os.Open(s.logPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), maxLogLine)
	out := []logEntry{}
	seen := map[int64]bool{}
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e logEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // 半截/坏行
		}
		if e.Seq <= 0 || seen[e.Seq] {
			continue
		}
		seen[e.Seq] = true
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// compactLocked 把快照 + 日志合并成新快照，并清空日志。调用方需持有 s.mu。
func (s *Store) compactLocked(id string) error {
	sess, err := s.loadFullLocked(id)
	if err != nil {
		return err
	}
	if err := s.saveSnapshot(sess); err != nil {
		return err
	}
	// 删除失败不阻断：快照的 LastSeq 会让旧日志条目在读取时被跳过
	_ = os.Remove(s.logPath(id))
	return s.saveMeta(metaOf(sess))
}

func (s *Store) saveSnapshot(sess *Session) error {
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path(sess.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(sess.ID))
}

func (s *Store) saveMeta(m Meta) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.metaPath(m.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.metaPath(m.ID))
}

func (s *Store) loadMeta(id string) (Meta, error) {
	data, err := os.ReadFile(s.metaPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return Meta{}, ErrNotFound
	}
	if err != nil {
		return Meta{}, err
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil || m.ID != id {
		return Meta{}, ErrNotFound
	}
	return m, nil
}

func metaOf(sess *Session) Meta {
	return Meta{
		ID: sess.ID, Title: sess.Title, ProjectID: sess.ProjectID, AgentID: sess.AgentID,
		CreatedAt: sess.CreatedAt, UpdatedAt: sess.UpdatedAt,
		MessageCount: len(sess.Messages), LastSeq: sess.LastSeq,
	}
}

func truncate(rs []rune, n int) string {
	if len(rs) <= n {
		return string(rs)
	}
	return string(rs[:n]) + "…"
}
