// Package agents 管理「智能体」：每个智能体有独立的名字、头像、人格系统提示词。
// 持久化到 data/agents.json；首次启动自动种子默认智能体「小柯」。
package agents

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

var ErrNotFound = errors.New("agent not found")

var validName = regexp.MustCompile(`^[\p{Han}\w\- ]{1,16}$`)
var validID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// Agent 是一个可选的智能体人格。SystemPrompt 为空时回退到默认「小柯」人设。
type Agent struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	Avatar       string    `json:"avatar,omitempty"` // 图片 URL（/assets/...）或单个 emoji
	SystemPrompt string    `json:"systemPrompt,omitempty"`
	Tools        bool      `json:"tools"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type indexFile struct {
	Agents []Agent `json:"agents"`
}

// Store 是并发安全的智能体仓库。
type Store struct {
	mu    sync.Mutex
	path  string
	items []Agent
}

// Open 打开（或初始化）智能体索引；seed 在首个文件不存在时调用。
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
		s.items = idx.Agents
	}
	return s, nil
}

// EnsureDefault 确保至少有一个默认智能体（小柯）。
func (s *Store) EnsureDefault(seed Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) > 0 {
		return nil
	}
	seed.ID = genID()
	now := time.Now()
	seed.CreatedAt, seed.UpdatedAt = now, now
	s.items = []Agent{seed}
	return s.saveLocked()
}

// genID 生成 16 位 hex ID（与 store/projects 同格式）。
func genID() string {
	b := make([]byte, 8)
	if _, err := crand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (s *Store) List() []Agent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Agent, len(s.items))
	copy(out, s.items)
	sortByCreated(out)
	return out
}

func (s *Store) Get(id string) (Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID.MatchString(id) {
		return Agent{}, ErrNotFound
	}
	for _, a := range s.items {
		if a.ID == id {
			return a, nil
		}
	}
	return Agent{}, ErrNotFound
}

// Create 新建智能体。
func (s *Store) Create(in Agent) (Agent, error) {
	in.Name = strings.TrimSpace(in.Name)
	if !validName.MatchString(in.Name) {
		return Agent{}, fmt.Errorf("名字只能包含中文、字母、数字、空格、下划线和短横线（1-16 字符）")
	}
	in.Description = strings.TrimSpace(in.Description)
	in.SystemPrompt = strings.TrimSpace(in.SystemPrompt)
	if err := validateAvatar(in.Avatar); err != nil {
		return Agent{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	in.ID = genID()
	in.CreatedAt, in.UpdatedAt = now, now
	s.items = append(s.items, in)
	if err := s.saveLocked(); err != nil {
		return Agent{}, err
	}
	return in, nil
}

// Update 修改智能体（保留 ID 与创建时间）。
func (s *Store) Update(id string, in Agent) (Agent, error) {
	if !validID.MatchString(id) {
		return Agent{}, ErrNotFound
	}
	in.Name = strings.TrimSpace(in.Name)
	if !validName.MatchString(in.Name) {
		return Agent{}, fmt.Errorf("名字只能包含中文、字母、数字、空格、下划线和短横线（1-16 字符）")
	}
	if err := validateAvatar(in.Avatar); err != nil {
		return Agent{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, a := range s.items {
		if a.ID != id {
			continue
		}
		in.ID = id
		in.CreatedAt = a.CreatedAt
		in.UpdatedAt = time.Now()
		s.items[i] = in
		if err := s.saveLocked(); err != nil {
			return Agent{}, err
		}
		return in, nil
	}
	return Agent{}, ErrNotFound
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) <= 1 {
		return fmt.Errorf("至少保留一个智能体")
	}
	idx := -1
	for i, a := range s.items {
		if a.ID == id {
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

// validateAvatar 只允许：空、单 emoji（≤4 rune）、/assets/ 开头的安全路径。
func validateAvatar(a string) error {
	a = strings.TrimSpace(a)
	if a == "" {
		return nil
	}
	if strings.HasPrefix(a, "/assets/") {
		return nil
	}
	if len([]rune(a)) <= 4 {
		return nil
	}
	return fmt.Errorf("头像需要是：图片 URL（/assets/...）或不超过 4 个字符的 emoji")
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(indexFile{Agents: s.items}, "", "  ")
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

func sortByCreated(items []Agent) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].CreatedAt.Before(items[j-1].CreatedAt); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
