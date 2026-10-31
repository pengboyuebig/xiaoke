// Package settings 提供「小柯」的运行时可变设置：持久化到 data/settings.json，
// 支持热更新（改完即生效，无需重启）。
//
// 优先级：默认值 <- config.json <- settings.json <- 环境变量（XIAOKE_*，凭据可不落盘）。
package settings

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

	// AutoCompact 上下文主动压缩：历史超过预算时，把旧回合总结成摘要（用当前模型），
	// 替代「直接裁掉」，保留更早的语境。被动裁剪仍作为兜底。
	AutoCompact bool `json:"autoCompact"`
	// CompactKeepCount 压缩时保留最近多少条消息不参与摘要（<=0 时用 MaxHistory）。
	CompactKeepCount int `json:"compactKeepCount"`
	// CommandTimeoutSec run_command 超时（秒；<=0 = 默认 30，上限 600）。
	CommandTimeoutSec int `json:"commandTimeoutSec"`

	// Permissions 运行时权限规则：按书写顺序评估，取最后一条命中的规则
	//（last-match-wins）。effect: allow（直接放行）/ ask（弹窗审批）/ deny（直接拒绝）。
	Permissions []PermissionRule `json:"permissions"`
	// Commands 用户自定义斜杠命令：前端输入 /name 时展开模板后发送（纯前端展开）。
	Commands []CustomCommand `json:"commands"`

	// LLMProfiles 模型配置档案：每个档案是完整的一套可切换配置（名称/模式/
	// baseURL/key/主模型/轻量模型）。激活 = 把档案值拷入上面的 LLM 字段，
	// LLM 字段永远代表「当前实际生效值」。温度/历史条数/maxTokens/contextWindow
	// 属于全局共享，不进档案。
	LLMProfiles []LLMProfile `json:"llmProfiles"`
	// ActiveProfile 当前激活的档案 ID（空 = 未绑定档案，LLM 字段为手动自定义值）。
	ActiveProfile string `json:"activeProfile,omitempty"`
}

// LLMProfile 是一份可一键切换的模型配置档案。
type LLMProfile struct {
	ID         string `json:"id"`
	Name       string `json:"name"`       // 显示名，如「DeepSeek 官方」
	Mode       string `json:"mode"`       // mock | openai（激活时连同运行模式一起切）
	BaseURL    string `json:"baseURL"`
	APIKey     string `json:"apiKey,omitempty"` // 脱敏下发：前端只见 尾4位，空串=保持
	Model      string `json:"model"`
	SmallModel string `json:"smallModel,omitempty"`
}

// PermissionRule 一条工具权限规则。Tool 是工具名（内置精确名或 MCP 原始名），
// Pattern 是资源摘要的模式（* ? 通配；空 = 只按工具名匹配任意参数）。
type PermissionRule struct {
	Tool    string `json:"tool"`
	Pattern string `json:"pattern,omitempty"`
	Effect  string `json:"effect"` // allow | ask | deny
}

// CustomCommand 斜杠命令模板：$ARGUMENTS 展开为全部参数文本，
// $1..$9 展开为按空白分词的位置参数。
type CustomCommand struct {
	Name     string `json:"name"`
	Template string `json:"template"`
}

type LLMSettings struct {
	Mode        string  `json:"mode"` // "mock" | "openai"
	BaseURL     string  `json:"baseURL"`
	APIKey      string  `json:"apiKey"`
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
	MaxHistory  int     `json:"maxHistory"`
	// SmallModel 轻量任务模型（可空 = 用主模型）：上下文压缩摘要、会话自动起标题
	// 等不需要强推理的辅助调用走它，省钱且更快。
	SmallModel string `json:"smallModel,omitempty"`
	// MaxTokens 单次回复的最大 token（<=0 = 不注入，由上游默认）。
	MaxTokens int `json:"maxTokens,omitempty"`
	// ContextWindow 上游模型的上下文窗口（token）。>0 时压缩触发按 token 水位
	//（bytes/4 估算）计算，更贴近上游真实约束；0 = 未知，按字节水位触发。
	ContextWindow int `json:"contextWindow,omitempty"`
}

// MCPServer 描述一个 stdio MCP server（与 opensquilla 的 [[mcp.servers]] 同形态）。
type MCPServer struct {
	Name     string            `json:"name"`
	Command  string            `json:"command"`
	Args     []string          `json:"args"`
	Env      map[string]string `json:"env,omitempty"`
	Enabled  bool              `json:"enabled"`
	ReadOnly bool              `json:"readOnly,omitempty"` // 为 true 时不把写/删/移动类工具暴露给模型
	// TimeoutSec 单次工具调用超时（秒；<=0 用默认 60）。重活类 server（构建/爬取）可调大。
	TimeoutSec int `json:"timeoutSec,omitempty"`
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
	} else if err != nil {
		return nil, err
	} else {
		if err := json.Unmarshal(data, &s.data); err != nil {
			return nil, err
		}
		s.normalize()
	}
	// 迁移：没有任何档案但已配置过模型 → 把当前生效配置生成第一个档案并激活。
	// 零感升级：打开设置页即见自己的配置已成档案，可继续添加更多。
	if len(s.data.LLMProfiles) == 0 && strings.TrimSpace(s.data.LLM.Model) != "" {
		p := LLMProfile{
			ID:         newProfileID(),
			Name:       "默认配置",
			Mode:       s.data.LLM.Mode,
			BaseURL:    s.data.LLM.BaseURL,
			APIKey:     s.data.LLM.APIKey,
			Model:      s.data.LLM.Model,
			SmallModel: s.data.LLM.SmallModel,
		}
		s.data.LLMProfiles = []LLMProfile{p}
		s.data.ActiveProfile = p.ID
	}
	if s.data.LLMProfiles != nil || s.data.ActiveProfile != "" {
		if err := s.save(); err != nil {
			return nil, err
		}
	}
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
		AutoCompact:      true, // 上下文超预算时主动总结，而不是硬裁
		CompactKeepCount: 0,    // 0 = 跟随 MaxHistory
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
	if s.data.LLM.MaxHistory > 200 {
		s.data.LLM.MaxHistory = 200
	}
	if s.data.LLM.Temperature <= 0 {
		s.data.LLM.Temperature = 0.7
	}
	if s.data.LLM.Temperature > 2 {
		s.data.LLM.Temperature = 2
	}
	if s.data.LLM.MaxTokens < 0 {
		s.data.LLM.MaxTokens = 0
	}
	if s.data.LLM.ContextWindow < 0 {
		s.data.LLM.ContextWindow = 0
	}
	if s.data.LLM.ContextWindow > 0 && s.data.LLM.ContextWindow < 4096 {
		// 有值但过小没有意义（比被动裁剪窗口还小），视为未配置
		s.data.LLM.ContextWindow = 0
	}
	if s.data.LLM.ContextWindow > 10_000_000 {
		s.data.LLM.ContextWindow = 10_000_000
	}
	if s.data.CompactKeepCount < 0 {
		s.data.CompactKeepCount = 0
	}
	if s.data.CompactKeepCount > s.data.LLM.MaxHistory {
		// 保留区不可能比裁剪窗口还大，否则压缩永远不触发
		s.data.CompactKeepCount = s.data.LLM.MaxHistory
	}
	for i := range s.data.MCPServers {
		if s.data.MCPServers[i].TimeoutSec < 0 {
			s.data.MCPServers[i].TimeoutSec = 0
		}
		if s.data.MCPServers[i].TimeoutSec > 600 {
			s.data.MCPServers[i].TimeoutSec = 600
		}
	}
	if s.data.CommandTimeoutSec < 0 {
		s.data.CommandTimeoutSec = 0
	}
	if s.data.CommandTimeoutSec > 600 {
		s.data.CommandTimeoutSec = 600
	}
	// 权限规则：effect 必须是三态之一，非法整条丢弃；条数封顶防配置膨胀。
	validEffects := map[string]bool{"allow": true, "ask": true, "deny": true}
	rules := make([]PermissionRule, 0, len(s.data.Permissions))
	for _, r := range s.data.Permissions {
		r.Tool = strings.TrimSpace(r.Tool)
		if r.Tool == "" || !validEffects[r.Effect] {
			continue
		}
		rules = append(rules, r)
		if len(rules) >= 64 {
			break
		}
	}
	s.data.Permissions = rules
	// 斜杠命令：名字限字母数字-_，模板非空；条数封顶。
	cmds := make([]CustomCommand, 0, len(s.data.Commands))
	for _, c := range s.data.Commands {
		c.Name = strings.Trim(strings.TrimSpace(c.Name), "/")
		c.Template = strings.TrimSpace(c.Template)
		if c.Name == "" || len(c.Name) > 32 || c.Template == "" || len(c.Template) > 32*1024 {
			continue
		}
		cmds = append(cmds, c)
		if len(cmds) >= 64 {
			break
		}
	}
	s.data.Commands = cmds
	if s.data.MCPServers == nil {
		s.data.MCPServers = []MCPServer{}
	}
	if s.data.ReadDirs == nil {
		s.data.ReadDirs = []string{}
	}
	if s.data.CommandAllowlist == nil {
		s.data.CommandAllowlist = []string{"git", "npm", "npx", "node", "python", "pip"}
	}
	if s.data.Permissions == nil {
		s.data.Permissions = []PermissionRule{}
	}
	if s.data.Commands == nil {
		s.data.Commands = []CustomCommand{}
	}
	s.normalizeProfiles()
}

// maxProfiles 档案数量上限（防配置膨胀；个人使用远用不到 20 套）。
const maxProfiles = 20

// normalizeProfiles 校验档案列表：清理无效项、补 ID、去重、维护 ActiveProfile 指向。
// 调用方需已持锁（normalize 由 Update/Open 在锁内调用）。
func (s *Store) normalizeProfiles() {
	profiles := make([]LLMProfile, 0, len(s.data.LLMProfiles))
	seen := map[string]bool{}
	for _, p := range s.data.LLMProfiles {
		p.Name = strings.TrimSpace(p.Name)
		p.Model = strings.TrimSpace(p.Model)
		p.BaseURL = strings.TrimSpace(p.BaseURL)
		p.Mode = strings.ToLower(strings.TrimSpace(p.Mode))
		// 模式合法值；未填按 openai（绝大多数档案是真实上游）
		if p.Mode != "mock" {
			p.Mode = "openai"
		}
		// 既没模型又没 baseURL 的空壳丢弃；没起名用模型名兜底
		if p.Model == "" && p.BaseURL == "" {
			continue
		}
		if p.Name == "" {
			p.Name = p.Model
			if p.Name == "" {
				p.Name = "未命名配置"
			}
		}
		if p.ID == "" || seen[p.ID] {
			if p.ID != "" {
				continue // 重复 ID：丢弃后续项（真正的去重，而不是换编号保留）
			}
			p.ID = newProfileID()
		}
		seen[p.ID] = true
		profiles = append(profiles, p)
		if len(profiles) >= maxProfiles {
			break
		}
	}
	s.data.LLMProfiles = profiles
	if s.data.ActiveProfile != "" && !seen[s.data.ActiveProfile] {
		s.data.ActiveProfile = "" // 指向已删除的档案：回到「手动自定义」
	}
}

// newProfileID 生成档案 ID（8 字节 hex，与 store 会话 ID 同风格）。
func newProfileID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("p%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// ActivateProfile 把指定档案的值拷入当前生效配置（LLM 字段），并记为激活。
// LLM 字段永远是「实际生效值」：下游 Effective() 零改动。档案不存在返回错误。
func (s *Store) ActivateProfile(id string) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, p := range s.data.LLMProfiles {
		if p.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return s.snapshot(), fmt.Errorf("配置档案 %q 不存在", id)
	}
	p := s.data.LLMProfiles[idx]
	s.data.LLM.Mode = p.Mode
	s.data.LLM.BaseURL = p.BaseURL
	s.data.LLM.APIKey = p.APIKey
	s.data.LLM.Model = p.Model
	s.data.LLM.SmallModel = p.SmallModel
	s.data.ActiveProfile = id
	if err := s.save(); err != nil {
		return s.snapshot(), err
	}
	return s.snapshot(), nil
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
	cp.Permissions = make([]PermissionRule, len(s.data.Permissions))
	copy(cp.Permissions, s.data.Permissions)
	cp.Commands = make([]CustomCommand, len(s.data.Commands))
	copy(cp.Commands, s.data.Commands)
	cp.LLMProfiles = make([]LLMProfile, len(s.data.LLMProfiles))
	copy(cp.LLMProfiles, s.data.LLMProfiles)
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
