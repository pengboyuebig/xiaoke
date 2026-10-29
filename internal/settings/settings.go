// Package settings 提供「小柯」的运行时可变设置：持久化到 data/settings.json，
// 支持热更新（改完即生效，无需重启）。
//
// 优先级：默认值 <- config.json <- settings.json <- 环境变量（XIAOKE_*，凭据可不落盘）。
package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"xiaoke/internal/config"
)

// Settings 是用户可在设置界面修改的全部内容。
type Settings struct {
	LLM              LLMSettings `json:"llm"`
	MCPServers       []MCPServer `json:"mcpServers"`
	ReadDirs         []string    `json:"readDirs"`         // 只读授权目录（用户手动添加）
	ProjectsRoot     string      `json:"projectsRoot"`     // 项目根目录
	MaxToolSteps     int         `json:"maxToolSteps"`     // agent 工具循环最大轮数
	CommandAllowlist []string    `json:"commandAllowlist"` // 允许执行的命令白名单
}

type LLMSettings struct {
	Mode        string  `json:"mode"` // "mock" | "openai"
	BaseURL     string  `json:"baseURL"`
	APIKey      string  `json:"apiKey"`
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
	MaxHistory  int     `json:"maxHistory"`
}

// MCPServer 描述一个 stdio MCP server（与 opensquilla 的 [[mcp.servers]] 同形态）。
type MCPServer struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled bool              `json:"enabled"`
}

// Store 是并发安全的设置仓库。
type Store struct {
	mu   sync.RWMutex
	path string
	base config.LLMConfig // config.json 里的 LLM 兜底值
	data Settings
}

// Open 加载（或初始化）设置文件。base 为 config.json 的 LLM 段，作为首启动种子值。
func Open(path string, base config.LLMConfig) (*Store, error) {
	s := &Store{path: path, base: base}
	s.data = s.defaults()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// 首次：用 config.json 的 LLM 值做种子并落盘
		s.data.LLM = LLMSettings{
			Mode: base.Mode, BaseURL: base.BaseURL, APIKey: base.APIKey,
			Model: base.Model, Temperature: base.Temperature, MaxHistory: base.MaxHistory,
		}
		s.normalize()
		if err := s.save(); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.data); err != nil {
		return nil, err
	}
	s.normalize()
	return s, nil
}

func (s *Store) defaults() Settings {
	return Settings{
		LLM: LLMSettings{
			Mode: "mock", BaseURL: "https://api.deepseek.com",
			Model: "deepseek-chat", Temperature: 0.7, MaxHistory: 20,
		},
		MCPServers:       []MCPServer{},
		ReadDirs:         []string{},
		ProjectsRoot:     "", // 留空 = 用 <小柯目录>/projects
		MaxToolSteps:     8,
		CommandAllowlist: []string{"git", "npm", "npx", "node", "python", "pip"},
	}
}

// normalize 修正越界值，保证切片非 nil（便于前端直接编辑）。
func (s *Store) normalize() {
	if s.data.MaxToolSteps <= 0 {
		s.data.MaxToolSteps = 8
	}
	if s.data.MaxToolSteps > 32 {
		s.data.MaxToolSteps = 32
	}
	if s.data.LLM.MaxHistory <= 0 {
		s.data.LLM.MaxHistory = 20
	}
	if s.data.LLM.Temperature <= 0 {
		s.data.LLM.Temperature = 0.7
	}
	if s.data.MCPServers == nil {
		s.data.MCPServers = []MCPServer{}
	}
	if s.data.ReadDirs == nil {
		s.data.ReadDirs = []string{}
	}
	if s.data.CommandAllowlist == nil {
		s.data.CommandAllowlist = []string{"git", "npm", "npx", "node", "python", "pip"}
	}
}

// Get 返回设置的深拷贝快照。
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot()
}

func (s *Store) snapshot() Settings {
	cp := s.data
	// make+copy 而非 append(nil)：空切片必须保持 []，不能退化成 nil（JSON 会变 null）
	cp.MCPServers = make([]MCPServer, len(s.data.MCPServers))
	copy(cp.MCPServers, s.data.MCPServers)
	cp.ReadDirs = make([]string, len(s.data.ReadDirs))
	copy(cp.ReadDirs, s.data.ReadDirs)
	cp.CommandAllowlist = make([]string, len(s.data.CommandAllowlist))
	copy(cp.CommandAllowlist, s.data.CommandAllowlist)
	return cp
}

// Update 合并保存：apiKey 为 nil 指针表示「保持原值」，空串表示清除。
func (s *Store) Update(next Settings, apiKey *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if apiKey != nil {
		next.LLM.APIKey = *apiKey
	} else {
		next.LLM.APIKey = s.data.LLM.APIKey
	}
	s.data = next
	s.normalize()
	return s.save()
}

// Effective 返回应用环境变量覆盖后的最终 LLM 配置（env 优先级最高，凭据可不落盘）。
func (s *Store) Effective() LLMSettings {
	s.mu.RLock()
	llm := s.data.LLM
	s.mu.RUnlock()
	if v := os.Getenv("XIAOKE_API_KEY"); v != "" {
		llm.APIKey = v
	}
	if v := os.Getenv("XIAOKE_BASE_URL"); v != "" {
		llm.BaseURL = v
	}
	if v := os.Getenv("XIAOKE_MODEL"); v != "" {
		llm.Model = v
	}
	if v := os.Getenv("XIAOKE_MODE"); v != "" {
		llm.Mode = v
	}
	return llm
}

// save 落盘（调用方需持锁）。API Key 会明文写入 data/settings.json（已被 gitignore）。
func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// MaskKey 返回脱敏展示用的 key 描述（是否有 Key + 尾 4 位）。
func MaskKey(key string) (has bool, tail string) {
	if strings.TrimSpace(key) == "" {
		return false, ""
	}
	r := []rune(strings.TrimSpace(key))
	if len(r) > 4 {
		return true, string(r[len(r)-4:])
	}
	return true, "****"
}
