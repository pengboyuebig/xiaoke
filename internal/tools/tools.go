// Package tools 提供「小柯」的内置工具集：文件读写、目录浏览、搜索、受限命令。
//
// 安全模型（对标 opensquilla 的 workspace 隔离）：
//   - 写操作与命令执行仅限当前项目工作区（Workspace）
//   - 读操作额外允许 ReadDirs 里的只读授权目录
//   - 命令白名单 + argv 直传（不经 shell）+ 超时 + 输出截断
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TodoItem 是一条待办（tools 层自有类型，handler 负责与 store 互转）。
type TodoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"` // pending | in_progress | completed
}

// Env 是一次工具执行的环境（每次请求按当前设置/项目构造）。
type Env struct {
	Workspace string   // 可读写的工作区根（项目目录）；为空表示未选择项目
	ReadDirs  []string // 只读授权目录
	Allowlist []string // 可执行命令白名单（basename 匹配）
	// CommandTimeoutSec run_command 超时（秒；<=0 用默认 30）。构建/安装类命令可能需要调大。
	CommandTimeoutSec int

	// SetTodos / MemoryAppend / MemoryRead / RunSubagent 是会话级能力的回调，
	// 由 handler 在每次请求时用闭包注入（携带会话 ID、项目 ID 等上下文），
	// tools 包保持零依赖。为 nil 表示该能力本次不可用（工具执行时报错并提示）。
	SetTodos     func(todos []TodoItem) error
	MemoryAppend func(scope, content string) (string, error)
	MemoryRead   func(scope string) (string, error)
	// RunSubagent 派一个子代理执行子任务：kind 为 "explore"（只读）或
	// "general-purpose"（完整工具），prompt 必须自包含。返回子代理的最终汇报。
	RunSubagent func(kind, prompt string) (string, error)
	// SkillRead 读取一个已启用技能的完整正文（系统提示词只注入技能清单）。
	SkillRead func(name string) (string, error)
	// AskUser 阻塞式向用户提问（ask_user 工具回调）：问题 + 选项（可空 = 自由输入）
	// + 是否多选。返回用户回答（多选时逗号分隔 / 自由输入时原文）。
	// 为 nil 表示本次会话无法交互（工具执行时报错）。
	AskUser func(question string, options []string, multiSelect bool) (string, error)
	// PermissionAsk 是 ask 模式的执行前审批回调（仅对写类工具调用）：
	// 返回 false = 用户拒绝，工具不执行。nil = 不拦截（非 ask 模式）。
	PermissionAsk func(toolName, summary string) bool
	// Cron 族回调：定时任务管理。CronCreate 为 nil 表示当前会话禁建
	//（自动化触发的会话，防递归）；其余 nil = 功能不可用。
	CronCreate func(title, expr, prompt, projectID string, maxRuns int) (string, error)
	CronList   func() (string, error)
	CronUpdate func(id, title, expr, prompt string, maxRuns int, enabled *bool) (string, error)
	CronDelete func(id string) (string, error)
	// Files 是会话级「已读文件」状态表（nil = 未启用新鲜度护栏）。
	// read_file 登记、edit_file/write_file 修改已有文件前校验，防盲写覆盖。
	Files *FileStateTracker
}

func (e *Env) clone() *Env {
	cp := *e
	cp.ReadDirs = append([]string(nil), e.ReadDirs...)
	cp.Allowlist = append([]string(nil), e.Allowlist...)
	return &cp
}

// Tool 是一个可被模型调用的工具（OpenAI function 形态）。
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema
	Execute     func(ctx context.Context, env *Env, args json.RawMessage) (string, error)
}

// Builtin 返回全部内置工具。
func Builtin() []Tool {
	return []Tool{
		toolListFiles, toolReadFile, toolEditFile, toolWriteFile,
		toolSearchFiles, toolFindFiles, toolTodoWrite, toolMemoryAppend,
		toolMemoryRead, toolSkillRead, toolWebFetch, toolTask,
		toolAskUser, toolCronCreate, toolCronList, toolCronUpdate,
		toolCronDelete, toolRunCommand, toolTaskOutput, toolTaskKill,
	}
}

// SubagentDefs 按子代理类型筛选工具集：
//   - explore：只保留只读工具（复用 IsWriteCapable 过滤，含只读 MCP）
//   - general-purpose：全量，但剔除 task 本身（防止子代理再派子代理的无限递归；
//     深度上限在最外层控制，这里直接从源头去掉递归入口）
func SubagentDefs(defs []ToolDef, kind string) []ToolDef {
	out := make([]ToolDef, 0, len(defs))
	for _, d := range defs {
		name := d.Function.Name
		if d.FromMCP != "" {
			name = d.MCPTool
		}
		if name == "task" {
			continue
		}
		if kind == "explore" && IsWriteCapable(name) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// ToolDef 是发给上游 OpenAI 接口的 tools 数组元素。
type ToolDef struct {
	Type     string                                                                    `json:"type"`
	Function ToolFunction                                                              `json:"function"`
	Exec     func(ctx context.Context, env *Env, args json.RawMessage) (string, error) `json:"-"`
	FromMCP  string                                                                    `json:"-"` // 非空表示来自该 MCP server
	MCPTool  string                                                                    `json:"-"` // MCP server 侧的原始工具名
}

// MarshalJSON 只输出 OpenAI 协议字段，避免把 Exec 函数（不可序列化）带进请求体。
func (d ToolDef) MarshalJSON() ([]byte, error) {
	type wire struct {
		Type     string       `json:"type"`
		Function ToolFunction `json:"function"`
	}
	return json.Marshal(wire{Type: d.Type, Function: d.Function})
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Defs 把工具列表转成上游请求形态（内置工具可直接执行）。
func Defs(all []Tool) []ToolDef {
	defs := make([]ToolDef, 0, len(all))
	for _, t := range all {
		defs = append(defs, ToolDef{
			Type: "function",
			Function: ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
			Exec: t.Execute,
		})
	}
	return defs
}

// IsWriteCapable 判断一个工具名是否具有写入/修改/执行副作用。
// 用于两个地方：
//   - 计划模式：只保留只读工具
//   - MCP server 标记 readOnly：从暴露给模型的工具列表里过滤掉写类工具
//
// 内置工具按精确名判断；MCP 工具名不统一，按常见动词模式匹配。
func IsWriteCapable(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	switch n {
	case "write_file", "run_command", "task_kill":
		return true
	case "cron_create", "cron_update", "cron_delete":
		// 调度类副作用：plan 模式过滤，ask 模式审批
		return true
	case "todo_write", "memory_append":
		// 只写智能体自己的内部状态（会话待办 / 长期记忆文件），不碰用户项目文件：
		// 计划模式下同样可用，不算写操作。
		return false
	}
	for _, kw := range []string{
		"write", "edit", "create", "mkdir", "move", "rename", "delete", "remove",
		"patch", "append", "insert", "replace", "copy", "truncate", "chmod",
		"chown", "symlink", "upload", "save", "persist", "update", "mkfile",
		"put_", "exec", "run_", "shell", "terminal",
	} {
		if strings.Contains(n, kw) {
			return true
		}
	}
	// rm/mv/cp/touch 这类两字母动词按「整词」匹配，避免误伤 format/confirm 等单词。
	for _, w := range []string{"rm", "mv", "cp", "touch"} {
		if n == w || strings.HasPrefix(n, w+"_") || strings.HasSuffix(n, "_"+w) ||
			strings.Contains(n, "_"+w+"_") || strings.Contains(n, w+"_file") {
			return true
		}
	}
	return false
}

// ---------- 路径安全 ----------

// resolveRead 把用户路径解析为可读的绝对路径：先试工作区，再试只读目录。
func (e *Env) resolveRead(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		p = "."
	}
	if e.Workspace != "" {
		if abs, err := safeJoin(e.Workspace, p); err == nil {
			return abs, nil
		}
	}
	for _, dir := range e.ReadDirs {
		if abs, err := safeJoin(dir, p); err == nil {
			return abs, nil
		}
	}
	return "", fmt.Errorf("路径 %q 不在工作区或已授权的只读目录内", p)
}

// resolveWrite 把用户路径解析为工作区内的可写绝对路径（未选项目时报错）。
func (e *Env) resolveWrite(p string) (string, error) {
	if strings.TrimSpace(e.Workspace) == "" {
		return "", fmt.Errorf("尚未选择项目：请先创建或选择一个项目，才能写入文件")
	}
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	return safeJoin(e.Workspace, p)
}

// safeJoin 把用户相对路径安全地拼到 root 下，拒绝一切逃逸（..、绝对路径注入、盘符），
// 并解析符号链接/junction，防止工作区内的链接指向外部。
func safeJoin(root, userPath string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	// 绝对路径/盘符开头：只允许它本来就等于 root 或在 root 下
	clean := filepath.Clean(userPath)
	if filepath.IsAbs(clean) {
		if _, err := relChecked(absRoot, clean); err != nil {
			return "", err
		}
		if err := ensureNoSymlinkEscape(absRoot, clean); err != nil {
			return "", err
		}
		return clean, nil
	}
	joined := filepath.Join(absRoot, clean)
	if _, err := relChecked(absRoot, joined); err != nil {
		return "", err
	}
	if err := ensureNoSymlinkEscape(absRoot, joined); err != nil {
		return "", err
	}
	return joined, nil
}

// ensureNoSymlinkEscape 解析 target 最深的已存在祖先的真实路径，
// 确认它仍在 root 的真实路径内。root 不存在时跳过（后续 os 操作会报错）。
func ensureNoSymlinkEscape(root, target string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil
	}
	probe := target
	suffix := []string{}
	for {
		if _, err := os.Lstat(probe); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return nil // 权限等错误不在这一层判断
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return nil
		}
		suffix = append([]string{filepath.Base(probe)}, suffix...)
		probe = parent
	}
	realProbe, err := filepath.EvalSymlinks(probe)
	if err != nil {
		return fmt.Errorf("解析路径失败: %w", err)
	}
	realTarget := filepath.Join(append([]string{realProbe}, suffix...)...)
	if _, err := relChecked(realRoot, realTarget); err != nil {
		return fmt.Errorf("路径经过符号链接越出授权根目录: %w", err)
	}
	return nil
}

// relChecked 校验 target 必须位于 root 内（含 root 本身），返回相对路径。
func relChecked(root, target string) (string, error) {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", fmt.Errorf("路径越界: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("路径 %q 越出授权根目录", target)
	}
	return rel, nil
}
