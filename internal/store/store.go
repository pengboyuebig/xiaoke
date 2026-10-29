// Package store 用「一个会话一个 JSON 文件」的方式持久化聊天记录，
// 零外部依赖，方便备份与手动查看。
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

var ErrNotFound = errors.New("session not found")

var validID = regexp.MustCompile(`^[0-9a-f]{16}$`)

type Message struct {
	Role      string    `json:"role"` // "user" | "assistant" | "tool"
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
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
	Messages  []Message `json:"messages"`
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
}

type Store struct {
	dir string
	mu  sync.Mutex
}

func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

// SetAgent 把会话绑定到智能体（仅在会话还没有绑定时生效）。
func (s *Store) SetAgent(id, agentID string) error {
	if agentID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.loadChecked(id)
	if err != nil {
		return err
	}
	if sess.AgentID == agentID {
		return nil
	}
	sess.AgentID = agentID
	sess.UpdatedAt = time.Now()
	return s.save(sess)
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
	if err := s.save(sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// List 返回全部会话的摘要，按更新时间倒序。
func (s *Store) List() ([]Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	metas := []Meta{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := e.Name()[:len(e.Name())-5]
		if !validID.MatchString(id) {
			continue
		}
		sess, err := s.load(id)
		if err != nil {
			continue // 单个文件损坏不影响列表
		}
		metas = append(metas, Meta{
			ID: sess.ID, Title: sess.Title, ProjectID: sess.ProjectID, AgentID: sess.AgentID,
			CreatedAt: sess.CreatedAt, UpdatedAt: sess.UpdatedAt,
			MessageCount: len(sess.Messages),
		})
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].UpdatedAt.After(metas[j].UpdatedAt) })
	return metas, nil
}

func (s *Store) Get(id string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadChecked(id)
}

// Append 向会话追加一条消息并刷新更新时间。
func (s *Store) Append(id string, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.loadChecked(id)
	if err != nil {
		return err
	}
	sess.Messages = append(sess.Messages, msg)
	sess.UpdatedAt = time.Now()
	// 首条用户消息作为会话标题
	if sess.Title == "" && msg.Role == "user" {
		sess.Title = truncate([]rune(msg.Content), 24)
	}
	return s.save(sess)
}

// SetProject 把会话绑定到项目（仅在会话还没有绑定时生效）。
func (s *Store) SetProject(id, projectID string) error {
	if projectID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.loadChecked(id)
	if err != nil {
		return err
	}
	if sess.ProjectID == projectID {
		return nil
	}
	sess.ProjectID = projectID
	sess.UpdatedAt = time.Now()
	return s.save(sess)
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.loadChecked(id); err != nil {
		return err
	}
	return os.Remove(s.path(id))
}

// loadChecked 校验 ID 后读取（防止路径穿越），调用方需持有锁。
func (s *Store) loadChecked(id string) (*Session, error) {
	if !validID.MatchString(id) {
		return nil, ErrNotFound
	}
	return s.load(id)
}

func (s *Store) load(id string) (*Session, error) {
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
	return &sess, nil
}

func (s *Store) save(sess *Session) error {
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

func truncate(rs []rune, n int) string {
	if len(rs) <= n {
		return string(rs)
	}
	return string(rs[:n]) + "…"
}
