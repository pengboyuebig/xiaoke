// Package skills 管理「技能」：技能是一段可复用的知识/指令（Markdown），
// 启用后会被注入到系统提示词里，让小柯获得特定领域的打法。
// 持久化到 data/skills.json；首次启动自动种子一个示例技能。
package skills

import (
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var ErrNotFound = errors.New("skill not found")

var validName = regexp.MustCompile(`^[\p{Han}\w\-]{1,16}$`)

// Skill 是一条技能。
type Skill struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`        // 技能名（如「金字塔写作」）
	Description string    `json:"description"` // 一句话用途
	Content     string    `json:"content"`     // Markdown 正文（会被注入系统提示词）
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type indexFile struct {
	Skills []Skill `json:"skills"`
}

// Store 是并发安全的技能仓库。
type Store struct {
	mu    sync.Mutex
	path  string
	items []Skill
}

// Open 打开（或初始化）技能索引。
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var idx indexFile
	if err := json.Unmarshal(data, &idx); err == nil {
		s.items = idx.Skills
	}
	return s, nil
}

// EnsureSeed 保证至少有一条技能（否则设置页技能区一片空白，用户不知从何下手）。
func (s *Store) EnsureSeed(seed Skill) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) > 0 {
		return nil
	}
	seed.ID = genID()
	now := time.Now()
	seed.CreatedAt, seed.UpdatedAt = now, now
	s.items = []Skill{seed}
	return s.saveLocked()
}

func (s *Store) List() []Skill {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Skill, len(s.items))
	copy(out, s.items)
	return out
}

func (s *Store) Get(id string) (Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.items {
		if it.ID == id {
			return it, nil
		}
	}
	return Skill{}, ErrNotFound
}

// Create 新建技能（name 唯一）。
func (s *Store) Create(in Skill) (Skill, error) {
	in.Name = strings.TrimSpace(in.Name)
	if !validName.MatchString(in.Name) {
		return Skill{}, fmt.Errorf("技能名只能包含中文、字母、数字、下划线和短横线（1-16 字符）")
	}
	in.Content = strings.TrimSpace(in.Content)
	if in.Content == "" {
		return Skill{}, fmt.Errorf("技能内容不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.items {
		if it.Name == in.Name {
			return Skill{}, fmt.Errorf("技能已存在：%s", in.Name)
		}
	}
	now := time.Now()
	in.ID = genID()
	in.CreatedAt, in.UpdatedAt = now, now
	s.items = append(s.items, in)
	if err := s.saveLocked(); err != nil {
		return Skill{}, err
	}
	return in, nil
}

// Update 修改技能。
func (s *Store) Update(id string, in Skill) (Skill, error) {
	in.Name = strings.TrimSpace(in.Name)
	if !validName.MatchString(in.Name) {
		return Skill{}, fmt.Errorf("技能名只能包含中文、字母、数字、下划线和短横线（1-16 字符）")
	}
	in.Content = strings.TrimSpace(in.Content)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, it := range s.items {
		if it.ID != id {
			continue
		}
		for j, other := range s.items {
			if j != i && other.Name == in.Name {
				return Skill{}, fmt.Errorf("技能名已存在：%s", in.Name)
			}
		}
		in.ID = id
		in.CreatedAt = it.CreatedAt
		in.UpdatedAt = time.Now()
		s.items[i] = in
		if err := s.saveLocked(); err != nil {
			return Skill{}, err
		}
		return in, nil
	}
	return Skill{}, ErrNotFound
}

// Toggle 启用/停用。
func (s *Store) Toggle(id string) (Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, it := range s.items {
		if it.ID != id {
			continue
		}
		s.items[i].Enabled = !it.Enabled
		s.items[i].UpdatedAt = time.Now()
		if err := s.saveLocked(); err != nil {
			return Skill{}, err
		}
		return s.items[i], nil
	}
	return Skill{}, ErrNotFound
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, it := range s.items {
		if it.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ErrNotFound
	}
	s.items = append(s.items[:idx], s.items[idx+1:]...)
	return s.saveLocked()
}

// Assembly 返回注入系统提示词的技能正文（仅启用的）；无启用技能返回空串。
func (s *Store) Assembly() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	for _, it := range s.items {
		if !it.Enabled || strings.TrimSpace(it.Content) == "" {
			continue
		}
		fmt.Fprintf(&b, "\n\n【技能：%s】%s\n%s", it.Name, it.Description, it.Content)
	}
	return b.String()
}

func genID() string {
	b := make([]byte, 8)
	if _, err := crand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(indexFile{Skills: s.items}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
