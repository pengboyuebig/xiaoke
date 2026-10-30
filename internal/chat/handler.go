package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"xiaoke/internal/agents"
	"xiaoke/internal/config"
	"xiaoke/internal/mcp"
	"xiaoke/internal/persona"
	"xiaoke/internal/projects"
	"xiaoke/internal/prompt"
	"xiaoke/internal/settings"
	"xiaoke/internal/skills"
	"xiaoke/internal/store"
	"xiaoke/internal/tools"
)

// Handler 挂载聊天与会话路由，并实现工具调用循环（agent loop）。
type Handler struct {
	cfg      *config.Config
	store    *store.Store
	set      *settings.Store
	projects *projects.Manager
	mcp      *mcp.Manager
	agents   *agents.Store
	skills   *skills.Store
}

// maxHistoryBytes 是发给上游的历史消息总字节预算：
// 只按条数截断时，几条大文件结果（单条可达 64KB）就可能撑爆上下文。
const maxHistoryBytes = 256 * 1024

func NewHandler(cfg *config.Config, st *store.Store, set *settings.Store, pm *projects.Manager, mm *mcp.Manager, ag *agents.Store, sk *skills.Store) *Handler {
	return &Handler{cfg: cfg, store: st, set: set, projects: pm, mcp: mm, agents: ag, skills: sk}
}

// Register 把路由挂到 mux 上（Go 1.22+ 方法路由）。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions", h.listSessions)
	mux.HandleFunc("POST /api/sessions", h.createSession)
	mux.HandleFunc("GET /api/sessions/{id}", h.getSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", h.deleteSession)
	mux.HandleFunc("POST /api/chat", h.chat)
}

func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	metas, err := h.store.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sessions": metas, "greeting": persona.Greeting,
		"agentName": persona.Name, "emptyProjectHint": persona.EmptyProjectHint,
	})
}

func (h *Handler) createSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProjectID string `json:"projectId"`
		AgentID   string `json:"agentId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in) // body 可选
	sess, err := h.store.Create("", in.ProjectID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if in.AgentID != "" {
		_ = h.store.SetAgent(sess.ID, in.AgentID)
		sess.AgentID = in.AgentID
	}
	writeJSON(w, http.StatusOK, sess)
}

func (h *Handler) getSession(w http.ResponseWriter, r *http.Request) {
	sess, err := h.store.Get(r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "会话不存在"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (h *Handler) deleteSession(w http.ResponseWriter, r *http.Request) {
	err := h.store.Delete(r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "会话不存在"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

type chatRequest struct {
	SessionID string `json:"sessionId"`
	Content   string `json:"content"`
	ProjectID string `json:"projectId"`
	AgentID   string `json:"agentId"` // 选用的智能体（空 = 默认小柯）
	// Reasoning 请求级思考程度：minimal/low/medium/high/none（前端右下角选择）
	Reasoning string `json:"reasoning"`
	// Mode 工作模式：plan（只读调研+出计划）| build（开发，默认）
	Mode string `json:"mode"`
	// Approve 批准计划：本条 user 消息只用于驱动模型（前端不渲染）
	Approve bool `json:"approve"`
}

// chat 处理一次问答（含工具调用循环）。
//
// SSE 事件：
//
//	data: {"type":"start","sessionId":"..."}
//	data: {"type":"delta","text":"..."}                      // 文本增量（可多轮）
//	data: {"type":"tool","name":"write_file","args":"..."}   // 开始调用工具
//	data: {"type":"tool_result","name":"...","ok":true,"preview":"...","ms":123}
//	data: {"type":"done"}
//	data: {"type":"error","error":"..."}
func (h *Handler) chat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Content == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求需要非空 content 字段"})
		return
	}
	// 同一会话串行处理：第二个并发请求直接返回 409，避免两条流交错写坏 tool 调用序列。
	unlock, ok := h.store.TryLockSession(req.SessionID)
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "该会话正在生成回复，请等待完成或新建会话"})
		return
	}
	defer unlock()
	sset := h.set.Get()
	eff := h.set.Effective()

	// 智能体：请求指定 > 会话已有 > 默认（列表第一个）
	var agent *agents.Agent
	aid := req.AgentID
	sess, sessErr := h.store.Get(req.SessionID)
	if aid == "" && sessErr == nil {
		aid = sess.AgentID
	}
	if aid != "" {
		if a, aerr := h.agents.Get(aid); aerr == nil {
			agent = &a
		}
	}
	if agent == nil {
		list := h.agents.List()
		if len(list) > 0 {
			agent = &list[0]
		}
	}

	// 会话：不存在则新建；项目：请求指定 > 会话已有
	var project *projects.Project
	pid := req.ProjectID
	if pid == "" && sessErr == nil {
		pid = sess.ProjectID
	}
	if pid != "" {
		if p, perr := h.projects.Get(pid); perr == nil {
			project = &p
		}
	}
	if sessErr != nil {
		var err error
		sess, err = h.store.Create("", pid)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	} else {
		if pid != "" && sess.ProjectID != pid {
			// 会话从「通用」切到项目下时补记绑定（下次从列表点进该项目会话可还原）
			_ = h.store.SetProject(sess.ID, pid)
		}
		if agent != nil && sess.AgentID != agent.ID {
			_ = h.store.SetAgent(sess.ID, agent.ID)
		}
	}
	if err := h.store.Append(sess.ID, store.Message{
		Role: "user", Content: req.Content, Hidden: req.Approve, CreatedAt: time.Now(),
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// 组装上下文：系统提示词（智能体人格+技能+项目+工具规范）+ 截断后的历史
	full, err := h.store.Get(sess.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	projName, projPath := "", ""
	if project != nil {
		projName, projPath = project.Name, project.Path
		// 项目目录可能被外部删除（索引仍保留）：自动重建，避免写/执行静默失败
		if err := os.MkdirAll(projPath, 0o755); err != nil {
			log.Printf("重建项目目录失败 %s: %v", projPath, err)
			// 目录不可用时按「未选择项目」处理，避免提示词给出空路径
			projName, projPath = "", ""
		}
	}
	env := &tools.Env{
		Workspace: projPath,
		ReadDirs:  sset.ReadDirs,
		Allowlist: sset.CommandAllowlist,
	}
	defs := tools.Defs(tools.Builtin())
	defs = append(defs, h.mcp.Tools()...)

	// 计划模式：只保留只读工具（list/read/search + 只读 MCP），禁写文件、禁执行命令
	planMode := strings.EqualFold(req.Mode, "plan")
	if planMode {
		readOnly := make([]tools.ToolDef, 0, len(defs))
		for _, d := range defs {
			// MCP 工具用 server 侧的原始名判断（去掉 mcp_<server>_ 前缀的干扰）
			name := d.Function.Name
			if d.FromMCP != "" {
				name = d.MCPTool
			}
			if tools.IsWriteCapable(name) {
				continue
			}
			readOnly = append(readOnly, d)
		}
		defs = readOnly
	}

	agentName, agentBase, agentTools := persona.Name, "", false
	if agent != nil {
		agentName, agentBase, agentTools = agent.Name, agent.SystemPrompt, agent.Tools
	}
	sysPrompt := prompt.Build(prompt.Request{
		AgentName:      agentName,
		AgentBase:      agentBase,
		AgentTools:     agentTools,
		ProjectName:    projName,
		ProjectPath:    projPath,
		ReadDirs:       sset.ReadDirs,
		ToolsAvailable: len(defs) > 0,
		SkillsAssembly: h.skills.Assembly(),
		Instructions:   prompt.PlanGuideFor(planMode),
	})
	convo := []ChatMessage{{Role: "system", Content: sysPrompt}}
	// 历史：先按条数/字节裁剪并向前对齐到 user 边界，再清理孤立/不完整的 tool 消息。
	// 否则截断点落在 assistant.tool_calls 与 tool 结果之间时，严格上游会直接 400。
	history := sanitizeHistory(trimHistory(full.Messages, eff.MaxHistory, maxHistoryBytes))
	convo = append(convo, toWire(history)...)

	maxSteps := sset.MaxToolSteps
	streamFn := Upstream(effAsConfig(eff))
	reasoning := req.Reasoning

	// 切换到 SSE 输出
	fl, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "当前连接不支持流式响应"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl.Flush()

	send := func(evt map[string]any) {
		data, _ := json.Marshal(evt)
		w.Write([]byte("data: " + string(data) + "\n\n"))
		fl.Flush()
	}
	send(map[string]any{"type": "start", "sessionId": sess.ID, "projectId": pid, "agentId": agentIDOf(agent)})
	hooks := Hooks{OnDelta: func(delta string) {
		send(map[string]any{"type": "delta", "text": delta})
	}}

	// ---------- agent loop：最多 maxSteps 轮工具调用 ----------
	for step := 0; step < maxSteps; step++ {
		content, calls, err := streamFn(r.Context(), effAsConfig(eff), convo, defs, hooks, reasoning)
		if err != nil {
			if r.Context().Err() != nil {
				return // 客户端断开
			}
			send(map[string]any{"type": "error", "error": err.Error()})
			return
		}
		if len(calls) == 0 {
			if strings.TrimSpace(content) == "" {
				send(map[string]any{"type": "error", "error": "上游没有返回内容"})
				return
			}
			if err := h.store.Append(sess.ID, store.Message{
				Role: "assistant", Content: content, CreatedAt: time.Now(),
			}); err != nil {
				send(map[string]any{"type": "error", "error": "保存回复失败: " + err.Error()})
				return
			}
			send(map[string]any{"type": "done"})
			return
		}

		// 有工具调用：先落库 assistant 消息（含 tool_calls），逐个执行
		records := make([]store.ToolCallRecord, 0, len(calls))
		for _, c := range calls {
			records = append(records, store.ToolCallRecord{ID: c.ID, Name: c.Function.Name, Arguments: c.Function.Arguments})
		}
		if err := h.store.Append(sess.ID, store.Message{
			Role: "assistant", Content: content, ToolCalls: records, CreatedAt: time.Now(),
		}); err != nil {
			send(map[string]any{"type": "error", "error": "保存消息失败: " + err.Error()})
			return
		}
		convo = append(convo, ChatMessage{Role: "assistant", Content: content, ToolCalls: calls})

		for _, call := range calls {
			// args 不再按 400 字符截断：前端合并成工具区块后只展示摘要行
			send(map[string]any{"type": "tool", "name": call.Function.Name, "args": call.Function.Arguments})
			start := time.Now()
			text, execErr := h.execTool(r.Context(), env, defs, call)
			ms := time.Since(start).Milliseconds()
			evt := map[string]any{
				"type": "tool_result", "name": call.Function.Name,
				"ok": execErr == nil, "ms": ms,
				"preview": truncateRunes(firstLineOK(text, execErr), 500),
			}
			if execErr != nil {
				evt["error"] = execErr.Error()
			}
			send(evt)
			resultText := text
			if execErr != nil {
				resultText = "工具执行出错: " + execErr.Error() + orText(text)
			}
			if err := h.store.Append(sess.ID, store.Message{
				Role: "tool", Content: resultText, ToolCallID: call.ID,
				Name: call.Function.Name, CreatedAt: time.Now(),
			}); err != nil {
				send(map[string]any{"type": "error", "error": "保存工具结果失败: " + err.Error()})
				return
			}
			convo = append(convo, ChatMessage{
				Role: "tool", Content: resultText,
				ToolCallID: call.ID, Name: call.Function.Name,
			})
			if r.Context().Err() != nil {
				return // 客户端断开，停止循环
			}
		}
	}
	// 达到最大轮数：强制做一轮无工具的最终答复
	content, _, err := streamFn(r.Context(), effAsConfig(eff), append(convo, ChatMessage{
		Role: "user", Content: "（系统提示：工具调用轮数已达上限，请直接给出最终答复。）",
	}), nil, hooks, reasoning)
	if err != nil {
		if r.Context().Err() == nil {
			send(map[string]any{"type": "error", "error": err.Error()})
		}
		return
	}
	if content != "" {
		if err := h.store.Append(sess.ID, store.Message{
			Role: "assistant", Content: content, CreatedAt: time.Now(),
		}); err == nil {
			send(map[string]any{"type": "done"})
			return
		}
	}
	send(map[string]any{"type": "error", "error": fmt.Sprintf("已达到最大工具轮数（%d），本轮结束", maxSteps)})
}

// execTool 按名字分发：内置工具直接执行，MCP 工具转发给对应 server。
func (h *Handler) execTool(ctx context.Context, env *tools.Env, defs []tools.ToolDef, call ToolCall) (string, error) {
	var def *tools.ToolDef
	for i := range defs {
		if defs[i].Function.Name == call.Function.Name {
			def = &defs[i]
			break
		}
	}
	if def == nil {
		return "", fmt.Errorf("未知工具 %q", call.Function.Name)
	}
	raw := strings.TrimSpace(call.Function.Arguments)
	if raw == "" || raw == "null" {
		raw = "{}"
	}
	args := json.RawMessage(raw)
	if !json.Valid(args) {
		return "", fmt.Errorf("工具参数不是合法 JSON：%s", truncateRunes(raw, 120))
	}
	if def.FromMCP != "" {
		return h.mcp.Call(ctx, def.FromMCP, def.MCPTool, args)
	}
	return def.Exec(ctx, env, args)
}

// toWire 把落库消息转回 OpenAI 请求格式（含 tool_calls 与 tool 结果的配对回放）。
func toWire(msgs []store.Message) []ChatMessage {
	out := make([]ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		cm := ChatMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID, Name: m.Name}
		for _, tc := range m.ToolCalls {
			cm.ToolCalls = append(cm.ToolCalls, ToolCall{
				ID:       tc.ID,
				Type:     "function",
				Function: ToolFunction{Name: tc.Name, Arguments: tc.Arguments},
			})
		}
		out = append(out, cm)
	}
	return out
}

func effAsConfig(e settings.LLMSettings) config.LLMConfig {
	return config.LLMConfig{
		Mode: e.Mode, BaseURL: e.BaseURL, APIKey: e.APIKey,
		Model: e.Model, Temperature: e.Temperature, MaxHistory: e.MaxHistory,
	}
}

func agentIDOf(a *agents.Agent) string {
	if a == nil {
		return ""
	}
	return a.ID
}

func truncateRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}

// firstLineOK 取工具结果的第一行/错误摘要给前端预览。
func firstLineOK(text string, err error) string {
	if err != nil {
		return err.Error()
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			return line
		}
	}
	return "(无输出)"
}

func orText(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return "\n" + s
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
