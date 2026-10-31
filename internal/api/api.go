// Package api 挂载设置、项目、MCP 状态等管理类路由（聊天路由在 internal/chat）。
package api

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"xiaoke/internal/config"
	"xiaoke/internal/mcp"
	"xiaoke/internal/projects"
	"xiaoke/internal/settings"
)

type Handler struct {
	cfg      *config.Config
	set      *settings.Store
	projects *projects.Manager
	mcp      *mcp.Manager
	Res      *Resources
}

func New(cfg *config.Config, set *settings.Store, pm *projects.Manager, mm *mcp.Manager, res *Resources) *Handler {
	return &Handler{cfg: cfg, set: set, projects: pm, mcp: mm, Res: res}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings", h.getSettings)
	mux.HandleFunc("PUT /api/settings", h.putSettings)
	mux.HandleFunc("POST /api/settings/test", h.testLLM)
	mux.HandleFunc("GET /api/models", h.listModels)
	mux.HandleFunc("GET /api/mcp/status", h.mcpStatus)
	mux.HandleFunc("POST /api/mcp/retry", h.retryMCP)
	mux.HandleFunc("GET /api/projects", h.listProjects)
	mux.HandleFunc("POST /api/projects", h.createProject)
	mux.HandleFunc("DELETE /api/projects/{id}", h.deleteProject)
	mux.HandleFunc("GET /api/projects/{id}/tree", h.projectTree)
}

// ---------- 设置 ----------

// settingsView 是给前端的设置视图：apiKey 永不下发，只给「是否已配置 + 尾 4 位」。
type settingsView struct {
	LLM struct {
		Mode          string  `json:"mode"`
		BaseURL       string  `json:"baseURL"`
		APIKeySet     bool    `json:"apiKeySet"`
		APIKeyTail    string  `json:"apiKeyTail"`
		Model         string  `json:"model"`
		SmallModel    string  `json:"smallModel"`
		Temperature   float64 `json:"temperature"`
		MaxHistory    int     `json:"maxHistory"`
		MaxTokens     int     `json:"maxTokens"`
		ContextWindow int     `json:"contextWindow"`
	} `json:"llm"`
	MCPServers            []settings.MCPServer      `json:"mcpServers"`
	ReadDirs              []string                  `json:"readDirs"`
	ProjectsRoot          string                    `json:"projectsRoot"`
	ProjectsRootEffective string                    `json:"projectsRootEffective"`
	MaxToolSteps          int                       `json:"maxToolSteps"`
	CommandAllowlist      []string                  `json:"commandAllowlist"`
	AutoCompact           bool                      `json:"autoCompact"`
	CompactKeepCount      int                       `json:"compactKeepCount"`
	CommandTimeoutSec     int                       `json:"commandTimeoutSec"`
	Permissions           []settings.PermissionRule `json:"permissions"`
	Commands              []settings.CustomCommand  `json:"commands"`
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	s := h.set.Get()
	v := settingsView{
		MCPServers:            s.MCPServers,
		ReadDirs:              s.ReadDirs,
		ProjectsRoot:          s.ProjectsRoot,
		ProjectsRootEffective: h.projects.Root(),
		MaxToolSteps:          s.MaxToolSteps,
		CommandAllowlist:      s.CommandAllowlist,
		Permissions:           s.Permissions,
		Commands:              s.Commands,
	}
	v.LLM.Mode = s.LLM.Mode
	v.LLM.BaseURL = s.LLM.BaseURL
	v.LLM.APIKeySet, v.LLM.APIKeyTail = settings.MaskKey(s.LLM.APIKey)
	v.LLM.Model = s.LLM.Model
	v.LLM.SmallModel = s.LLM.SmallModel
	v.LLM.Temperature = s.LLM.Temperature
	v.LLM.MaxHistory = s.LLM.MaxHistory
	v.LLM.MaxTokens = s.LLM.MaxTokens
	v.LLM.ContextWindow = s.LLM.ContextWindow
	v.AutoCompact = s.AutoCompact
	v.CompactKeepCount = s.CompactKeepCount
	v.CommandTimeoutSec = s.CommandTimeoutSec
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) {
	// 设置体很小，1MB 是宽容上限：decode 前先拦，防程序化调用把超大 body 读进内存
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var in settings.Settings
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "请求体过大"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	// 约定：llm.apiKey 为空串 = 保持现有 Key 不变；要清除 Key 用 clearApiKey=true
	var clearKey bool
	if v, ok := lookClearKey(r); ok {
		clearKey = v
	}
	var newKey *string
	if in.LLM.APIKey != "" {
		newKey = &in.LLM.APIKey
	} else if clearKey {
		empty := ""
		newKey = &empty
	}
	if err := h.set.Update(in, newKey); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// 热更新：MCP server 拓扑可能变了
	h.mcp.Apply(h.set.Get().MCPServers)
	h.getSettings(w, r)
}

// clearApiKey 通过查询参数传递（避免前端构造复杂结构）：?clearApiKey=true
func lookClearKey(r *http.Request) (bool, bool) {
	v := r.URL.Query().Get("clearApiKey")
	if v == "" {
		return false, false
	}
	return v == "true" || v == "1", true
}

// testReq 是「测试连接」的请求体；空字段沿用当前已保存配置。
type testReq struct {
	BaseURL string `json:"baseURL"`
	APIKey  string `json:"apiKey"`
	Model   string `json:"model"`
}

// testLLM 用给定配置请求上游 /models，验证 baseURL+key 是否可用。
func (h *Handler) testLLM(w http.ResponseWriter, r *http.Request) {
	var in testReq
	_ = decodeJSON(w, r, &in)
	eff := h.set.Effective()
	if in.BaseURL != "" {
		eff.BaseURL = in.BaseURL
	}
	if in.APIKey != "" {
		eff.APIKey = in.APIKey
	}
	if in.Model != "" {
		eff.Model = in.Model
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	models, err := fetchModels(ctx, eff.BaseURL, eff.APIKey)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "models": models})
}

// listModels 用当前已保存配置拉取上游模型列表（供设置页下拉）。
func (h *Handler) listModels(w http.ResponseWriter, r *http.Request) {
	eff := h.set.Effective()
	if strings.EqualFold(eff.Mode, "mock") {
		writeJSON(w, http.StatusOK, map[string]any{"models": []string{"deepseek-chat (mock 演示)"}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	models, err := fetchModels(ctx, eff.BaseURL, eff.APIKey)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"models": []string{}, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// fetchModels 请求 OpenAI 兼容的 GET {base}/models，兼容 {data:[{id}]} 与裸数组两种返回。
func fetchModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("baseURL 未配置")
	}
	url := strings.TrimRight(baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 %s 失败: %w", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("上游返回 %s: %s", resp.Status, strings.TrimSpace(string(body[:min(len(body), 300)])))
	}
	var wrap struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &wrap); err == nil && len(wrap.Data) > 0 {
		ids := make([]string, 0, len(wrap.Data))
		for _, m := range wrap.Data {
			ids = append(ids, m.ID)
		}
		return ids, nil
	}
	var bare []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &bare); err == nil && len(bare) > 0 {
		ids := make([]string, 0, len(bare))
		for _, m := range bare {
			ids = append(ids, m.ID)
		}
		return ids, nil
	}
	return nil, fmt.Errorf("无法解析模型列表响应")
}

// ---------- MCP 状态 ----------

func (h *Handler) mcpStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"servers": h.mcp.Status()})
}

// retryMCP 收敛 MCP 进程：退出/启动失败/握手失败的 server 会重新拉起；
// 正在运行或握手中的保持不动。设置页「刷新状态」走这个接口。
func (h *Handler) retryMCP(w http.ResponseWriter, r *http.Request) {
	h.mcp.Apply(h.set.Get().MCPServers)
	writeJSON(w, http.StatusOK, map[string]any{"servers": h.mcp.Status()})
}

// ---------- 项目 ----------

func (h *Handler) listProjects(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"projects": h.projects.List(), "root": h.projects.Root()})
}

type createProjectReq struct {
	Name     string `json:"name"`
	Template string `json:"template"`
	Root     string `json:"root"` // 可选：自定义父目录
}

func (h *Handler) createProject(w http.ResponseWriter, r *http.Request) {
	var in createProjectReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	p, err := h.projects.Create(in.Name, in.Template, in.Root, genID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) deleteProject(w http.ResponseWriter, r *http.Request) {
	err := h.projects.Delete(r.PathValue("id"))
	if errors.Is(err, projects.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "项目不存在"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

func (h *Handler) projectTree(w http.ResponseWriter, r *http.Request) {
	items, err := h.projects.Tree(r.PathValue("id"))
	if errors.Is(err, projects.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "项目不存在"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tree": items})
}

// genID 与 store 保持同一格式（16 位 hex）。
func genID() string {
	const hex = "0123456789abcdef"
	b := make([]byte, 8)
	if _, err := crand.Read(b); err != nil {
		panic(err)
	}
	out := make([]byte, 16)
	for i, v := range b {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&0x0f]
	}
	return string(out)
}

// decodeJSON 统一的请求体解码：1MB 上限（decode 前生效的内存保护），
// 超限 413、坏 JSON 400；返回 false 表示已写响应，调用方直接 return。
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "请求体过大（上限 1MB）"})
			return false
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
