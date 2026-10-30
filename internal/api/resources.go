package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"xiaoke/internal/agents"
	"xiaoke/internal/skills"
)

// Resources 持有智能体/技能仓库与头像目录。
type Resources struct {
	Agents     *agents.Store
	Skills     *skills.Store
	AvatarsDir string // 相对 assetsDir：avatars
	AssetsDir  string
}

func (h *Handler) RegisterResources(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/agents", h.listAgents)
	mux.HandleFunc("POST /api/agents", h.createAgent)
	mux.HandleFunc("PUT /api/agents/{id}", h.updateAgent)
	mux.HandleFunc("DELETE /api/agents/{id}", h.deleteAgent)
	mux.HandleFunc("GET /api/skills", h.listSkills)
	mux.HandleFunc("POST /api/skills", h.createSkill)
	mux.HandleFunc("PUT /api/skills/{id}", h.updateSkill)
	mux.HandleFunc("DELETE /api/skills/{id}", h.deleteSkill)
	mux.HandleFunc("POST /api/agents/{id}/toggle", h.toggleAgentTools) // 智能体：开关工具
	mux.HandleFunc("POST /api/skills/{id}/toggle", h.toggleSkill)      // 技能：开关启用
	mux.HandleFunc("POST /api/upload/avatar", h.uploadAvatar)
}

// ---------- 智能体 ----------

func (h *Handler) listAgents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"agents": h.Res.Agents.List()})
}

func (h *Handler) createAgent(w http.ResponseWriter, r *http.Request) {
	var in agents.Agent
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	a, err := h.Res.Agents.Create(in)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *Handler) updateAgent(w http.ResponseWriter, r *http.Request) {
	var in agents.Agent
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	a, err := h.Res.Agents.Update(r.PathValue("id"), in)
	if errors.Is(err, agents.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "智能体不存在"})
		return
	}
	if errors.Is(err, agents.ErrBuiltin) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *Handler) deleteAgent(w http.ResponseWriter, r *http.Request) {
	err := h.Res.Agents.Delete(r.PathValue("id"))
	if errors.Is(err, agents.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "智能体不存在"})
		return
	}
	if errors.Is(err, agents.ErrBuiltin) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

// toggleAgentTools 智能体的工具开关（POST /api/agents/{id}/toggle）。
func (h *Handler) toggleAgentTools(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, err := h.Res.Agents.Get(id)
	if errors.Is(err, agents.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "智能体不存在"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if a.Builtin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": agents.ErrBuiltin.Error()})
		return
	}
	a.Tools = !a.Tools
	out, err := h.Res.Agents.Update(id, a)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- 技能 ----------

func (h *Handler) listSkills(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"skills": h.Res.Skills.List()})
}

func (h *Handler) createSkill(w http.ResponseWriter, r *http.Request) {
	var in skills.Skill
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	s, err := h.Res.Skills.Create(in)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *Handler) updateSkill(w http.ResponseWriter, r *http.Request) {
	var in skills.Skill
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	s, err := h.Res.Skills.Update(r.PathValue("id"), in)
	if errors.Is(err, skills.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "技能不存在"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *Handler) toggleSkill(w http.ResponseWriter, r *http.Request) {
	s, err := h.Res.Skills.Toggle(r.PathValue("id"))
	if errors.Is(err, skills.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "技能不存在"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *Handler) deleteSkill(w http.ResponseWriter, r *http.Request) {
	err := h.Res.Skills.Delete(r.PathValue("id"))
	if errors.Is(err, skills.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "技能不存在"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

// ---------- 头像上传 ----------

var avatarNameSan = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

var avatarExt = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

// uploadAvatar 接收 multipart 文件（字段名 file），校验类型/大小后存到 assets/avatars/，
// 返回 { url: "/assets/avatars/xxx" }。
func (h *Handler) uploadAvatar(w http.ResponseWriter, r *http.Request) {
	const maxBytes = 2 << 20 // 2MB
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "图片超过 2MB 上限"})
		return
	}
	fh, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 file 字段"})
		return
	}
	defer fh.Close()

	head := make([]byte, 512)
	n, _ := io.ReadFull(fh, head)
	ctype := http.DetectContentType(head[:n])
	ext, ok := avatarExt[ctype]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "只支持 PNG / JPEG / WebP / GIF"})
		return
	}
	if _, err := fh.Seek(0, io.SeekStart); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取图片失败"})
		return
	}

	dir := filepath.Join(h.Res.AssetsDir, h.Res.AvatarsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	name := avatarNameSan.ReplaceAllString(fmt.Sprintf("av_%d", time.Now().UnixNano()), "")
	dst := filepath.Join(dir, name+ext)
	out, err := os.Create(dst)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if _, err := io.Copy(out, fh); err != nil {
		out.Close()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存图片失败"})
		return
	}
	out.Close()
	url := "/assets/" + strings.Trim(filepath.ToSlash(h.Res.AvatarsDir), "/") + "/" + name + ext
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}
