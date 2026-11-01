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
	"unicode/utf8"
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
	// assembly 缓存：每次对话请求都要拼一次技能正文，改动（增删改/开关）才失效
	assembly    string
	assemblySet bool
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
	if err := s.saveLocked(); err != nil {
		return err
	}
	s.invalidateAssemblyLocked()
	return nil
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
	s.invalidateAssemblyLocked()
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
		s.invalidateAssemblyLocked()
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
		s.invalidateAssemblyLocked()
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
	if err := s.saveLocked(); err != nil {
		return err
	}
	s.invalidateAssemblyLocked()
	return nil
}

// maxAssemblyBytes 是技能注入系统提示词的总字节上限。
// 技能是用户手写的自由文本，没有上限的话十几篇长技能会把上下文吃光，
// 还挤掉真正的对话历史。
const maxAssemblyBytes = 16 * 1024

// maxListingBytes 是技能清单（按需加载模式）注入上下文的字节上限。
// 清单只有名字+一句话描述，几十个技能也远用不满。
const maxListingBytes = 4 * 1024

// Listing 返回启用技能的清单（名字 + 一句话描述，一行一条），供常驻注入。
//
// 对标主流 agent 的 skills 按需加载：正文常驻（旧行为，16KB 上限）会在技能
// 多起来后挤占任务上下文；清单常驻的成本是 KB 级，正文由模型用 skill_read
// 工具在与任务相关时按需读取。无启用技能返回空串。
func (s *Store) Listing() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	budget := maxListingBytes
	for _, it := range s.items {
		if !it.Enabled {
			continue
		}
		line := fmt.Sprintf("\n- %s：%s", it.Name, strings.TrimSpace(it.Description))
		if strings.TrimSpace(it.Description) == "" {
			line = fmt.Sprintf("\n- %s", it.Name)
		}
		if len(line) > budget {
			b.WriteString("\n（更多技能未列入清单，可用 skill_read 按名字读取）")
			break
		}
		budget -= len(line)
		b.WriteString(line)
	}
	return strings.TrimSpace(b.String())
}

// ContentByName 返回指定「启用」技能的完整正文（skill_read 工具的读取入口）。
// 禁用的技能读不到——开关同时是按需加载的准入控制。
func (s *Store) ContentByName(name string) (string, error) {
	name = strings.TrimSpace(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.items {
		if it.Name == name {
			if !it.Enabled {
				return "", fmt.Errorf("技能「%s」已停用：请用户在设置里启用后再用", name)
			}
			if strings.TrimSpace(it.Content) == "" {
				return "", fmt.Errorf("技能「%s」没有正文", name)
			}
			return it.Content, nil
		}
	}
	// 不存在时报错列出可用名字，模型一轮就能自纠
	var names []string
	for _, it := range s.items {
		if it.Enabled {
			names = append(names, it.Name)
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("没有启用的技能（名称需精确匹配）")
	}
	return "", fmt.Errorf("没有叫「%s」的技能；可用的有：%s", name, strings.Join(names, "、"))
}

// Assembly 返回注入系统提示词的技能正文（仅启用的）；无启用技能返回空串。
// 超过上限时按顺序装载，放不下的技能截断并标注——排在前面的优先。
//
// 已被 Listing（清单注入 + skill_read 按需加载）取代，保留供导出/调试。
func (s *Store) Assembly() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.assemblySet {
		return s.assembly
	}
	s.assembly = s.buildAssemblyLocked()
	s.assemblySet = true
	return s.assembly
}

// buildAssemblyLocked 实际拼装（调用方需持有 s.mu）。
func (s *Store) buildAssemblyLocked() string {
	var b strings.Builder
	budget := maxAssemblyBytes
	truncated := false
	for _, it := range s.items {
		if !it.Enabled || strings.TrimSpace(it.Content) == "" {
			continue
		}
		sec := fmt.Sprintf("\n\n【技能：%s】%s\n%s", it.Name, it.Description, it.Content)
		if len(sec) > budget {
			if budget < 200 {
				truncated = true // 连标注都放不下了
				break
			}
			sec = truncateBytes(sec, budget) + "\n…（超出注入上限，已截断）"
			b.WriteString(sec)
			truncated = true
			break
		}
		budget -= len(sec)
		b.WriteString(sec)
	}
	if truncated {
		b.WriteString("\n\n（提示：启用的技能总量过大，只装入了部分。请停用不常用的技能以获得完整注入。）")
	}
	return b.String()
}

// truncateBytes 按字节截断且不切碎 UTF-8 序列：
// 不断回退直到结尾是合法 rune 边界（同时覆盖「续字节」与「悬空起始字节」两种情况）。
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 {
		if r, size := utf8.DecodeLastRuneInString(cut); r != utf8.RuneError || size != 1 {
			break
		}
		cut = cut[:len(cut)-1]
	}
	return cut
}

func genID() string {
	b := make([]byte, 8)
	if _, err := crand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// invalidateAssemblyLocked 任何写操作后调用。调用方需持有 s.mu。
func (s *Store) invalidateAssemblyLocked() {
	s.assembly = ""
	s.assemblySet = false
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
